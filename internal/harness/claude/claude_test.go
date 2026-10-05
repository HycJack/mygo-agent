package claude

import (
	"strings"
	"testing"

	"mygo-agent/internal/harness"
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
