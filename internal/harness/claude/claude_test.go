//go:build !windows

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
	if strings.Contains(notes[0].Text, "tokens") {
		t.Fatalf("a result line without usage grew a token count: %q", notes[0].Text)
	}
	if !r.sawResult {
		t.Fatal("the result line did not set sawResult — the run would never settle")
	}
}

// TestResultNoteTokens pins the usage member of the result line: when
// the CLI reports token totals, the note carries their sum.
func TestResultNoteTokens(t *testing.T) {
	r, evs := collect(harness.Turn{SessionID: "sess-abc123"})
	r.handle(`{"type":"result","subtype":"success","is_error":false,"duration_ms":2345,` +
		`"total_cost_usd":0.0042,"usage":{"input_tokens":110,"output_tokens":17}}`)

	notes := kind(evs, harness.EventNote)
	if len(notes) != 1 || !strings.Contains(notes[0].Text, "127 tokens") {
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

// TestSilentCLIIsNotACleanTurn pins the failure mode where the CLI
// produces nothing at all and is then cut off: a hang before its first
// line, a crash that left no stderr, a binary that never started. All
// three look identical on the wire, and reporting them as a finished turn
// leaves the user an empty reply with no reason for it — the one outcome
// they cannot act on. The run has to say that it stopped without answering.
func TestSilentCLIIsNotACleanTurn(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "silent-claude.sh")
	// Reads its input, then hangs: no init, no events, no stderr.
	if err := os.WriteFile(script, []byte("#!/bin/bash\nIFS= read -r _\nsleep 300\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := New(script).Run(ctx, harness.Turn{Workdir: dir}, func(harness.Event) {})
	if err == nil {
		t.Fatal("a CLI that produced nothing reported a clean turn")
	}
	msg := err.Error()
	if !strings.Contains(msg, "stopped before it answered") {
		t.Fatalf("the error does not say the CLI never answered: %q", msg)
	}
	if strings.Contains(msg, "claude:claude:") {
		t.Fatalf("the prefix is doubled: %q", msg)
	}
}

// TestStopAfterOutputIsStillClean is the other half: a run that got on
// with it and was then stopped by the user is not a failure, and the fix
// above must not turn a deliberate stop into an error card.
func TestStopAfterOutputIsStillClean(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "slow-claude.sh")
	// Streams one line, then keeps the turn open.
	if err := os.WriteFile(script, []byte(`#!/bin/bash
IFS= read -r _
echo '{"type":"assistant","message":{"content":[{"type":"text","text":"working"}]}}'
sleep 300
`), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var text string
	if err := New(script).Run(ctx, harness.Turn{Workdir: dir}, func(ev harness.Event) {
		if ev.Kind == harness.EventText {
			text += ev.TextDelta
		}
	}); err != nil {
		t.Fatalf("a stopped run that had already produced output reported a failure: %v", err)
	}
	if text != "working" {
		t.Fatalf("what arrived was lost: %q", text)
	}
}

// The CLI's path boundary is decided inside the CLI, below the layer
// where can_use_tool lives, so a read outside the workspace never reaches
// the approval card — the model is simply told the permission was not
// granted and moves on. These pin that the run asks instead, once, and
// only when the answer is yes does the grant reach the command line.

func TestOutsideDirArgsAsksOnceAndGrantsWhatIsApproved(t *testing.T) {
	ws := t.TempDir()
	// Three files in two directories is one decision, not three.
	prompt := "compare /tmp/a/one.txt with /tmp/a/two.txt and /tmp/b/three.txt"

	var asked harness.OutsideDirRequest
	calls := 0
	turn := harness.Turn{
		Workdir: ws, Prompt: prompt,
		OnOutsideDir: func(_ context.Context, req harness.OutsideDirRequest) bool {
			calls++
			asked = req
			return true
		},
	}
	args := outsideDirArgs(context.Background(), turn)
	if calls != 1 {
		t.Fatalf("asked %d times, want once for the whole prompt", calls)
	}
	if len(asked.Dirs) != 2 {
		t.Fatalf("asked about %v, want two directories", asked.Dirs)
	}
	if asked.Workdir != ws {
		t.Fatalf("the ask did not name the workspace: %q", asked.Workdir)
	}
	// The grant has to be on the command line, as a directory, or the
	// CLI refuses the read exactly as it did before.
	joined := strings.Join(args, " ")
	if n := strings.Count(joined, "--add-dir"); n != 2 {
		t.Fatalf("args carry %d --add-dir flags, want 2: %v", n, args)
	}
	for _, d := range asked.Dirs {
		if !strings.Contains(joined, d) {
			t.Fatalf("%q was approved but is not on the command line: %v", d, args)
		}
	}
}

func TestOutsideDirArgsGrantsNothingOnRefusal(t *testing.T) {
	ws := t.TempDir()
	turn := harness.Turn{
		Workdir: ws, Prompt: "read /etc/hosts",
		OnOutsideDir: func(context.Context, harness.OutsideDirRequest) bool { return false },
	}
	if args := outsideDirArgs(context.Background(), turn); len(args) != 0 {
		t.Fatalf("a refused directory reached the command line: %v", args)
	}
}

func TestOutsideDirArgsStaysQuietForAPromptInsideTheWorkspace(t *testing.T) {
	ws := t.TempDir()
	asked := false
	turn := harness.Turn{
		Workdir: ws, Prompt: "read " + filepath.Join(ws, "main.go") + " and fix it",
		OnOutsideDir: func(context.Context, harness.OutsideDirRequest) bool {
			asked = true
			return true
		},
	}
	if args := outsideDirArgs(context.Background(), turn); len(args) != 0 {
		t.Fatalf("a prompt that stayed in the workspace produced %v", args)
	}
	if asked {
		t.Fatal("the user was asked about a path inside the workspace they are already in")
	}
}

// A run with nowhere to ask runs confined. That is the safe reading of a
// boundary it cannot raise, and it is what a nil callback has to mean.
func TestOutsideDirArgsWithNoHandlerGrantsNothing(t *testing.T) {
	turn := harness.Turn{Workdir: t.TempDir(), Prompt: "read /etc/hosts"}
	if args := outsideDirArgs(context.Background(), turn); len(args) != 0 {
		t.Fatalf("a run with no handler was granted %v", args)
	}
}

// TestCompactionSaysSoOnTheWire pins the frame that tells the user the
// model stopped seeing the earlier conversation. Without it the only
// symptom is the agent quietly contradicting an instruction it accepted
// twenty turns ago.
func TestCompactionSaysSoOnTheWire(t *testing.T) {
	r, evs := collect(harness.Turn{})
	r.handle(`{"type":"system","subtype":"compact_boundary","session_id":"s1",` +
		`"compact_metadata":{"trigger":"manual","pre_tokens":25876,"post_tokens":5253,` +
		`"cumulative_dropped_tokens":20623,"duration_ms":16001}}`)

	notes := kind(evs, harness.EventNote)
	if len(notes) != 1 {
		t.Fatalf("notes %d, want 1: %+v", len(notes), notes)
	}
	text := notes[0].Text
	if !strings.Contains(text, "claude") || !strings.Contains(text, "compacted") {
		t.Fatalf("note %q", text)
	}
	// The counts live in compact_metadata, which is snake_case on this
	// wire. Reading it as compactMetadata decodes nothing and quietly
	// leaves both counts at zero, which is why the numbers are asserted
	// rather than merely the presence of a note.
	if !strings.Contains(text, "25.9k") || !strings.Contains(text, "5.3k") {
		t.Fatalf("note %q lost the token counts", text)
	}
}

// A boundary frame with no metadata still means the context was folded.
func TestCompactionWithoutMetadataStillReports(t *testing.T) {
	r, evs := collect(harness.Turn{})
	r.handle(`{"type":"system","subtype":"compact_boundary"}`)
	notes := kind(evs, harness.EventNote)
	if len(notes) != 1 {
		t.Fatalf("a metadata-less boundary went unreported: %+v", notes)
	}
	if strings.Contains(notes[0].Text, "→") {
		t.Fatalf("a note with no counts invented them: %q", notes[0].Text)
	}
}

// The other system frames must stay quiet: init reports the session, and
// neither is a compaction.
func TestOtherSystemFramesAreNotCompactions(t *testing.T) {
	r, evs := collect(harness.Turn{})
	r.handle(`{"type":"system","subtype":"init","session_id":"s1"}`)
	r.handle(`{"type":"system","subtype":"status"}`)
	if notes := kind(evs, harness.EventNote); len(notes) != 0 {
		t.Fatalf("unrelated system frames produced notes: %+v", notes)
	}
}

// TestRunFlagsCarryAgentMapping pins the two agent-facing flags
// (spec/agents.md): the profile's system prompt rides
// --append-system-prompt and the turn's MCP servers land in a
// --mcp-config file that is gone when the turn ends. A bare turn asks
// for neither.
func TestRunFlagsCarryAgentMapping(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	script := `#!/bin/bash
printf '%s\n' "$@" > "` + argsFile + `"
echo '{"type":"result","subtype":"success","is_error":false,"duration_ms":1,"result":"done"}'
`
	scriptPath := filepath.Join(dir, "fake-claude.sh")
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	h := &Harness{Bin: scriptPath}

	turn := harness.Turn{Workdir: dir, Model: "m", SystemPrompt: "Be terse.",
		MCPServers: []harness.MCPServer{{Name: "fs", Command: "npx", Args: []string{"-y", "srv"}}}}
	if err := h.Run(context.Background(), turn, func(harness.Event) {}); err != nil {
		t.Fatalf("run: %v", err)
	}
	raw, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	args := string(raw)
	if !strings.Contains(args, "--append-system-prompt") || !strings.Contains(args, "Be terse.") {
		t.Fatalf("system prompt flag missing: %s", args)
	}
	at := strings.Index(args, "--mcp-config")
	if at < 0 {
		t.Fatalf("mcp-config flag missing: %s", args)
	}
	rest := args[at+len("--mcp-config\n"):]
	path := strings.TrimSpace(rest[:strings.IndexByte(rest, '\n')])
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the mcp config file outlived the turn: %s", path)
	}

	// A bare turn asks for neither flag.
	if err := h.Run(context.Background(), harness.Turn{Workdir: dir, Model: "m"}, func(harness.Event) {}); err != nil {
		t.Fatalf("bare run: %v", err)
	}
	raw, err = os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "append-system-prompt") || strings.Contains(string(raw), "mcp-config") {
		t.Fatalf("bare turn grew agent flags: %s", raw)
	}
}
