package claude

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mygo-agent/internal/harness"
	"mygo-agent/internal/harness/cli"
)

// collect builds a run that appends every event to the slice.
func collect(turn harness.Turn) (*run, *[]harness.Event) {
	evs := &[]harness.Event{}
	r := &run{turn: turn, emit: func(ev harness.Event) { *evs = append(*evs, ev) },
		cards: map[string]int{}}
	return r, evs
}

func kind(evs *[]harness.Event, k string) []harness.Event {
	var out []harness.Event
	for _, ev := range *evs {
		if ev.Kind == k {
			out = append(out, ev)
		}
	}
	return out
}

// TestLineHandling pins the stream-json mapping: the session id, reply
// text, and an Edit tool call whose own old/new strings become the diff
// preview, settled by its tool_result.
func TestLineHandling(t *testing.T) {
	r, evs := collect(harness.Turn{})

	r.handle(`{"type":"system","subtype":"init","session_id":"sess-abc123"}`)
	r.handle(`{"type":"assistant","message":{"content":[{"type":"text","text":"Hello "}]}}`)
	r.handle(`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"tu1","name":"Edit","input":{"file_path":"a.go","old_string":"alpha\n","new_string":"beta\n"}}]}}`)
	r.handle(`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"tu1","content":"file edited"}]}}`)

	sessions := kind(evs, harness.EventSession)
	if len(sessions) != 1 || sessions[0].SessionID != "sess-abc123" {
		t.Fatalf("session events: %+v", sessions)
	}
	text := ""
	for _, ev := range kind(evs, harness.EventText) {
		text += ev.TextDelta
	}
	if !strings.HasPrefix(text, "Hello ") {
		t.Fatalf("text %q", text)
	}
	starts := kind(evs, harness.EventToolStart)
	if len(starts) != 1 || !starts[0].Edit || starts[0].File != "a.go" ||
		!strings.Contains(starts[0].Diff, "alpha") || !strings.Contains(starts[0].Diff, "beta") {
		t.Fatalf("edit start events: %+v", starts)
	}
	ends := kind(evs, harness.EventToolEnd)
	if len(ends) != 1 || ends[0].Output != "file edited" || ends[0].Exit != 0 {
		t.Fatalf("tool end events: %+v", ends)
	}
}

// TestResultNote pins the final summary note: cost and session.
func TestResultNote(t *testing.T) {
	r, evs := collect(harness.Turn{SessionID: "sess-abc123"})
	r.handle(`{"type":"result","subtype":"success","is_error":false,"duration_ms":12000,"total_cost_usd":0.0042,"result":"done"}`)

	notes := kind(evs, harness.EventNote)
	if len(notes) != 1 || !strings.Contains(notes[0].Text, "$0.0042") ||
		!strings.Contains(notes[0].Text, "sess-abc") {
		t.Fatalf("result notes: %+v", notes)
	}
	if !r.sawResult {
		t.Fatal("the result line did not set sawResult — the run would never settle")
	}
}

// TestReadOnlyAutoDeny pins that read-only mode denies a can_use_tool
// up front with a reply naming the mode — no approval callback consulted.
func TestReadOnlyAutoDeny(t *testing.T) {
	r, evs := collect(harness.Turn{Mode: harness.ModeReadOnly})
	r.handle(`{"type":"control_request","request_id":"req-1","request":{"subtype":"can_use_tool","tool_name":"Bash","input":{"command":"rm -rf /tmp/x"}}}`)

	if len(*evs) != 0 {
		t.Fatalf("unexpected events: %+v", *evs)
	}
	if len(r.wrote) != 1 {
		t.Fatalf("control responses written: %+v", r.wrote)
	}
	resp := r.wrote[0]["response"].(map[string]any)
	if resp["behavior"] != "deny" || !strings.Contains(resp["message"].(string), "read-only mode") {
		t.Fatalf("deny response: %+v", resp)
	}
}

// TestApprovalTimeoutIsADenial pins that an unanswered approval settles
// as a denial carrying the timeout reason — never an open card, never a
// failed turn, and never an unbounded wait (spec/approvals.md).
func TestApprovalTimeoutIsADenial(t *testing.T) {
	var sawDeadline bool
	r, evs := collect(harness.Turn{
		// Agent mode: read-only denies up front without asking.
		Mode: harness.ModeAgent,
		OnApproval: func(ctx context.Context, req harness.ApprovalRequest) harness.ApprovalDecision {
			_, sawDeadline = ctx.Deadline()
			<-ctx.Done() // the card is never answered
			return harness.ApprovalDecision{Approved: true}
		},
	})
	// collect leaves mode at its zero value (read-only), which denies up
	// front without asking; agent mode is what reaches the callback.
	r.mode = harness.ModeAgent
	r.ctx = context.Background()
	r.approvalTimeout = 50 * time.Millisecond

	r.handle(`{"type":"control_request","request_id":"req-1","request":{"subtype":"can_use_tool","tool_name":"Bash","input":{"command":"ls"}}}`)

	if !sawDeadline {
		t.Fatal("the approval wait got no deadline — a card could sit forever")
	}
	if len(r.wrote) != 1 {
		t.Fatalf("control responses written: %+v", r.wrote)
	}
	resp := r.wrote[0]["response"].(map[string]any)
	// A decision that arrives after the deadline is not a decision.
	if resp["behavior"] != "deny" {
		t.Fatalf("expired approval allowed: %+v", resp)
	}
	if msg := resp["message"].(string); msg != "approval timed out" {
		t.Fatalf("denial reason %q, want the timeout reason", msg)
	}
	if len(*evs) != 0 {
		t.Fatalf("a denial is a settled result in the stream, not events: %+v", *evs)
	}
}

// TestRunSettlesOnLingeringCLI drives a fake CLI through the whole
// control path: a can_use_tool request the host never answers, a slow but
// alive stretch that must not be cut short, then the result event — after
// which the CLI sleeps forever. Run must still return nil, promptly, with
// the denial settled on the wire.
func TestRunSettlesOnLingeringCLI(t *testing.T) {
	old := cli.ReapGrace
	cli.ReapGrace = 200 * time.Millisecond
	defer func() { cli.ReapGrace = old }()

	dir := t.TempDir()
	script := filepath.Join(dir, "fake-claude.sh")
	body := `#!/bin/bash
read -r -t 5 _ || true
echo '{"type":"system","subtype":"init","session_id":"sess-live"}'
echo '{"type":"control_request","request_id":"req-1","request":{"subtype":"can_use_tool","tool_name":"Bash","input":{"command":"ls"}}}'
if read -r -t 5 resp; then printf '%s' "$resp" > "{{dir}}/control.txt"; fi
echo '{"type":"assistant","message":{"content":[{"type":"text","text":"working"}]}}'
sleep 0.4
echo '{"type":"result","subtype":"success","is_error":false,"duration_ms":900,"total_cost_usd":0.001,"result":"done"}'
# The real CLI keeps its streams open until reaped; the host breaks first.
sleep 30
`
	body = strings.ReplaceAll(body, "{{dir}}", dir)
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	var evs []harness.Event
	h := &Harness{Bin: script, ApprovalTimeout: 50 * time.Millisecond}
	done := make(chan error, 1)
	start := time.Now()
	go func() {
		done <- h.Run(context.Background(), harness.Turn{Workdir: dir, Mode: harness.ModeAgent,
			OnApproval: func(ctx context.Context, _ harness.ApprovalRequest) harness.ApprovalDecision {
				<-ctx.Done() // nobody ever clicks the card
				return harness.ApprovalDecision{Approved: true}
			}},
			func(ev harness.Event) { evs = append(evs, ev) })
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("run did not settle — the lingering CLI held the turn")
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("run took %s; the reap was not bounded", elapsed)
	}

	data, err := os.ReadFile(filepath.Join(dir, "control.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"behavior":"deny"`) ||
		!strings.Contains(string(data), "approval timed out") {
		t.Fatalf("control response on the wire: %s", data)
	}

	text, note := "", false
	for _, ev := range evs {
		if ev.Kind == harness.EventText {
			text += ev.TextDelta
		}
		if ev.Kind == harness.EventNote && strings.Contains(ev.Text, "Done") {
			note = true // the result survived the slow stretch
		}
	}
	if text != "working" || !note {
		t.Fatalf("events incomplete — text:%q result note:%v", text, note)
	}
}
