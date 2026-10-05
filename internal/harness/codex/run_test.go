package codex

import (
	"strings"
	"testing"

	"mygo-agent/internal/harness"
)

// feed replays a recorded codex exec session into a fresh run and
// returns the events it emitted.
func feed(t *testing.T, lines ...string) []harness.Event {
	t.Helper()
	var evs []harness.Event
	r := newRun(harness.Turn{}, func(ev harness.Event) { evs = append(evs, ev) })
	for _, l := range lines {
		r.handle(l)
	}
	return evs
}

func find(evs []harness.Event, kind string) []harness.Event {
	var out []harness.Event
	for _, ev := range evs {
		if ev.Kind == kind {
			out = append(out, ev)
		}
	}
	return out
}

// TestRunFeed pins the exec --json mapping: session id, tool cards with
// output and exit, file changes with their diff, and the reply text.
func TestRunFeed(t *testing.T) {
	evs := feed(t,
		`{"type":"thread.started","thread_id":"sess-42"}`,
		`{"type":"item.started","item":{"id":"item_0","type":"command_execution","command":"go test ./...","status":"in_progress"}}`,
		`{"type":"item.completed","item":{"id":"item_0","type":"command_execution","command":"go test ./...","status":"completed","exit_code":0,"aggregated_output":"ok"}}`,
		`{"type":"item.completed","item":{"id":"item_1","type":"file_change","changes":[{"path":"main.go","kind":"modify"}],"diff":"--- a\n+++ b\n@@ -1 +1 @@\n-old\n+new"}}`,
		`{"type":"item.completed","item":{"id":"item_2","type":"agent_message","text":"All green."}}`,
		`{"type":"turn.completed","usage":{"input_tokens":10}}`,
	)

	sessions := find(evs, harness.EventSession)
	if len(sessions) != 1 || sessions[0].SessionID != "sess-42" {
		t.Fatalf("session events: %+v", sessions)
	}
	ends := find(evs, harness.EventToolEnd)
	if len(ends) != 1 || ends[0].Output != "ok" || ends[0].Exit != 0 {
		t.Fatalf("tool end events: %+v", ends)
	}
	files := find(evs, harness.EventFileChange)
	if len(files) != 1 || files[0].File != "main.go" || files[0].Diff == "" {
		t.Fatalf("file change events: %+v", files)
	}
	text := ""
	for _, ev := range find(evs, harness.EventText) {
		text += ev.TextDelta
	}
	if text != "All green." {
		t.Fatalf("reply text %q", text)
	}
}

// TestNoDoubleText pins the app-server wire contract: the reply arrives
// as deltas and the completed item repeats the text whole — the run must
// emit it once, not twice.
func TestNoDoubleText(t *testing.T) {
	var evs []harness.Event
	r := newRun(harness.Turn{}, func(ev harness.Event) { evs = append(evs, ev) })
	const id = "msg_1"
	r.notify("item/started", []byte(`{"item":{"id":"`+id+`","type":"agentMessage","text":""}}`))
	r.notify("item/agentMessage/delta", []byte(`{"itemId":"`+id+`","delta":"Hello"}`))
	r.notify("item/agentMessage/delta", []byte(`{"itemId":"`+id+`","delta":" world"}`))
	r.notify("item/completed", []byte(`{"item":{"id":"`+id+`","type":"agentMessage","text":"Hello world"}}`))

	text := ""
	for _, ev := range find(evs, harness.EventText) {
		text += ev.TextDelta
	}
	if text != "Hello world" {
		t.Fatalf("reply text %q", text)
	}
}

// TestReasoningBuffered pins that reasoning deltas become one reasoning
// event at item completion, not one per delta.
func TestReasoningBuffered(t *testing.T) {
	var evs []harness.Event
	r := newRun(harness.Turn{}, func(ev harness.Event) { evs = append(evs, ev) })
	const id = "rs_1"
	r.notify("item/reasoning/summaryTextDelta", []byte(`{"itemId":"`+id+`","delta":"think "}`))
	r.notify("item/reasoning/summaryTextDelta", []byte(`{"itemId":"`+id+`","delta":"hard"}`))
	r.notify("item/completed", []byte(`{"item":{"id":"`+id+`","type":"reasoning","text":"","summary":[]}}`))

	notes := find(evs, harness.EventReasoning)
	if len(notes) != 1 || notes[0].Text != "think hard" {
		t.Fatalf("reasoning events: %+v", notes)
	}
}

// TestRunErrors pins the error mapping and that garbage lines are
// skipped without effect.
func TestRunErrors(t *testing.T) {
	evs := feed(t,
		`{"type":"error","message":"quota exhausted"}`,
		`not json`,
		``,
	)
	errs := find(evs, harness.EventError)
	if len(errs) != 1 || !strings.Contains(errs[0].Err, "quota") {
		t.Fatalf("error events: %+v", errs)
	}
}
