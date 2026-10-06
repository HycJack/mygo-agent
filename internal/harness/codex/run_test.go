package codex

import (
	"strings"
	"sync"
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
	// The Host projector opens a block per tool_start and closes the
	// newest one on tool_end, so an extra start is a block that spins
	// forever. Starts and ends have to balance.
	if starts := find(evs, harness.EventToolStart); len(starts) != len(ends) {
		t.Fatalf("unbalanced tool cards: %d starts, %d ends", len(starts), len(ends))
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

// TestAppServerItemStartsOnce pins the app-server wire's two frames per
// item: item/started and item/completed both route into the item mapping,
// and a command must open exactly one card — otherwise the first block
// the projector opened is never closed and spins forever.
func TestAppServerItemStartsOnce(t *testing.T) {
	var evs []harness.Event
	r := newRun(harness.Turn{}, func(ev harness.Event) { evs = append(evs, ev) })
	r.notify("item/started", []byte(`{"item":{"id":"item_0","type":"command_execution","command":"go build ./...","status":"in_progress"}}`))
	r.notify("item/completed", []byte(`{"item":{"id":"item_0","type":"command_execution","command":"go build ./...","status":"completed","exitCode":0,"aggregatedOutput":"ok"}}`))
	r.notify("item/started", []byte(`{"item":{"id":"item_1","type":"command_execution","command":"go vet ./...","status":"in_progress"}}`))
	r.notify("item/completed", []byte(`{"item":{"id":"item_1","type":"command_execution","command":"go vet ./...","status":"completed","exitCode":1,"aggregatedOutput":"boom"}}`))

	starts, ends := find(evs, harness.EventToolStart), find(evs, harness.EventToolEnd)
	if len(starts) != 2 || len(ends) != 2 {
		t.Fatalf("tool cards unbalanced: %d starts, %d ends (%+v)", len(starts), len(ends), evs)
	}
	// Each end must match a start on the same id, and the ends carry the
	// command's own output and exit.
	for _, ev := range ends {
		found := false
		for _, st := range starts {
			if st.ToolCall.ID == ev.ToolCall.ID {
				found = true
			}
		}
		if !found {
			t.Fatalf("tool end with no start: %+v", ev)
		}
	}
	if ends[0].Output != "ok" || ends[0].Exit != 0 || ends[1].Exit != 1 {
		t.Fatalf("tool end payloads: %+v", ends)
	}
	// The wire carries no duration, so the card reports its own elapsed
	// time rather than a permanent 0 ms.
	if ends[0].Ms < 0 {
		t.Fatalf("negative duration: %+v", ends[0])
	}
}

// TestTurnFailed pins that a failed turn is never silent: the reason
// reaches the stream and Run has it to return.
func TestTurnFailed(t *testing.T) {
	var evs []harness.Event
	r := newRun(harness.Turn{}, func(ev harness.Event) { evs = append(evs, ev) })
	r.notify("turn/failed", []byte(`{"error":{"message":"model stream broke"}}`))

	errs := find(evs, harness.EventError)
	if len(errs) != 1 || !strings.Contains(errs[0].Err, "model stream broke") {
		t.Fatalf("error events: %+v", errs)
	}
	fail, saw, _ := r.outcome()
	if fail != "model stream broke" {
		t.Fatalf("turn failure reason %q", fail)
	}
	if !saw {
		t.Fatal("a failed turn produced an event but counts as silent")
	}
}

// TestTurnFailedShapes pins the payload variants: the app-server nests
// the reason under error, and a bare string, message or reason all have
// to yield something rather than an empty failure.
func TestTurnFailedShapes(t *testing.T) {
	for _, tc := range []struct{ params, want string }{
		{`{"error":{"message":"stream broke"}}`, "stream broke"},
		{`{"error":"rate limited"}`, "rate limited"},
		{`{"message":"no credentials"}`, "no credentials"},
		{`{"reason":"cancelled upstream"}`, "cancelled upstream"},
		{`{}`, "The agent failed."},
	} {
		var evs []harness.Event
		r := newRun(harness.Turn{}, func(ev harness.Event) { evs = append(evs, ev) })
		r.notify("turn/failed", []byte(tc.params))
		if len(evs) != 1 || evs[0].Kind != harness.EventError || evs[0].Err != tc.want {
			t.Fatalf("params %s: events %+v", tc.params, evs)
		}
	}
}

// TestTokenUsageNote pins the closing note: the turn ends by saying what
// it cost in tokens — its own usage frame when it carries one, else the
// running thread/tokenUsage/updated total. A server that never reports
// usage stays silent, as before the note existed.
func TestTokenUsageNote(t *testing.T) {
	// The turn's own usage frame is enough.
	var evs []harness.Event
	r := newRun(harness.Turn{}, func(ev harness.Event) { evs = append(evs, ev) })
	r.notify("turn/completed", []byte(`{"usage":{"inputTokens":110,"outputTokens":17}}`))
	if notes := find(evs, harness.EventNote); len(notes) != 1 || notes[0].Text != "Done · 127 tokens" {
		t.Fatalf("usage on turn/completed: %+v", notes)
	}

	// The running total is the fallback, and the updates emit nothing
	// on their own.
	evs = nil
	r = newRun(harness.Turn{}, func(ev harness.Event) { evs = append(evs, ev) })
	r.notify("thread/tokenUsage/updated", []byte(`{"threadId":"t0","tokenUsage":{"input_tokens":90,"output_tokens":10}}`))
	if len(evs) != 0 {
		t.Fatalf("usage updates emit nothing: %+v", evs)
	}
	r.notify("turn/completed", []byte(`{}`))
	if notes := find(evs, harness.EventNote); len(notes) != 1 || notes[0].Text != "Done · 100 tokens" {
		t.Fatalf("running total fallback: %+v", notes)
	}

	// No usage anywhere, no note.
	evs = nil
	r = newRun(harness.Turn{}, func(ev harness.Event) { evs = append(evs, ev) })
	r.notify("turn/completed", []byte(`{}`))
	if notes := find(evs, harness.EventNote); len(notes) != 0 {
		t.Fatalf("a silent server grew a note: %+v", notes)
	}
}

// TestSawEventNeedsOutput pins that an unreadable stream is not a clean
// turn: sawEvent only becomes true once something was actually emitted,
// and dropped frames are counted so the transport can fail the turn.
func TestSawEventNeedsOutput(t *testing.T) {
	var evs []harness.Event
	r := newRun(harness.Turn{}, func(ev harness.Event) { evs = append(evs, ev) })
	r.notify("item/started", []byte(`{not json`))
	r.notify("item/agentMessage/delta", []byte(`{"itemId":"m1","delta":""}`))
	r.notify("turn/completed", []byte(`{}`))

	if _, saw, garbage := r.outcome(); saw || garbage != 1 {
		t.Fatalf("garbage stream reported as clean: saw=%v garbage=%d", saw, garbage)
	}
	if len(evs) != 0 {
		t.Fatalf("unreadable frames emitted events: %+v", evs)
	}

	// One usable event is enough to make the turn a real one.
	r.notify("item/agentMessage/delta", []byte(`{"itemId":"m1","delta":"hi"}`))
	if _, saw, _ := r.outcome(); !saw {
		t.Fatal("a produced event did not count")
	}
}

// TestSealDropsLateEvents pins the emit-after-return guard: the Host
// finishes the thread the instant Run returns, so a send after seal must
// reach nothing.
func TestSealDropsLateEvents(t *testing.T) {
	var evs []harness.Event
	r := newRun(harness.Turn{}, func(ev harness.Event) { evs = append(evs, ev) })
	r.notify("item/agentMessage/delta", []byte(`{"itemId":"m1","delta":"before"}`))
	r.seal()
	r.notify("item/agentMessage/delta", []byte(`{"itemId":"m1","delta":"after"}`))

	text := ""
	for _, ev := range find(evs, harness.EventText) {
		text += ev.TextDelta
	}
	if text != "before" {
		t.Fatalf("text after seal: %q", text)
	}
}

// TestSealRacesNoEmit pins the invariant the Host actually depends on:
// however hard the reader goroutine is pushing, no event reaches emit
// once seal has returned. send holds the same lock seal takes, so a send
// already in flight either completes before seal returns or is dropped —
// which is what makes a post-return emit impossible rather than merely
// unlikely.
func TestSealRacesNoEmit(t *testing.T) {
	for i := 0; i < 200; i++ {
		var mu sync.Mutex
		var emitted, late int
		sealed := false
		r := newRun(harness.Turn{}, func(harness.Event) {
			mu.Lock()
			defer mu.Unlock()
			if sealed {
				late++
				return
			}
			emitted++
		})
		start := make(chan struct{})
		var wg sync.WaitGroup
		for j := 0; j < 4; j++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				for k := 0; k < 50; k++ {
					r.send(harness.Event{Kind: harness.EventText, TextDelta: "x"})
				}
			}()
		}
		close(start)
		r.seal()
		mu.Lock()
		sealed = true
		mu.Unlock()
		wg.Wait()
		if late != 0 {
			t.Fatalf("iteration %d: %d of %d events landed after seal returned", i, late, emitted+late)
		}
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

// TestItemTypesFromBothWires pins the two spellings the app-server and
// exec wires use. Matching only snake_case meant a real `agentMessage` or
// `commandExecution` item mapped to nothing: the reply came back empty
// with no error anywhere.
func TestItemTypesFromBothWires(t *testing.T) {
	cases := []struct {
		wire, typ string
		want      string
	}{
		{"app-server", "commandExecution", harness.EventToolStart},
		{"exec", "command_execution", harness.EventToolStart},
		{"app-server", "fileChange", harness.EventFileChange},
		{"exec", "file_change", harness.EventFileChange},
		{"app-server", "agentMessage", harness.EventText},
		{"exec", "agent_message", harness.EventText},
	}
	for _, tc := range cases {
		t.Run(tc.wire+"/"+tc.typ, func(t *testing.T) {
			var kinds []string
			r := newRun(harness.Turn{}, func(e harness.Event) { kinds = append(kinds, e.Kind) })
			r.item(codexItem{ID: "i1", Type: tc.typ, Command: "go test", Text: "hello"}, true)
			found := false
			for _, k := range kinds {
				if k == tc.want {
					found = true
				}
			}
			if !found {
				t.Fatalf("item type %q produced %v, want a %s event", tc.typ, kinds, tc.want)
			}
		})
	}
}

func TestNormalizeItemType(t *testing.T) {
	for in, want := range map[string]string{
		"commandExecution":  "command_execution",
		"fileChange":        "file_change",
		"agentMessage":      "agent_message",
		"command_execution": "command_execution",
		"reasoning":         "reasoning",
		"":                  "",
	} {
		if got := normalizeItemType(in); got != want {
			t.Errorf("normalizeItemType(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestCompactionIsAnItemNotANotification pins where codex actually
// announces a context compaction. The binary contains a
// `thread/compacted` method name, and subscribing to it looks right, but
// a real app-server never sends it: the compaction arrives as an item of
// type contextCompaction, announced twice. Matching the notification
// would have bound a handler to nothing at all — a compaction would then
// be invisible, which is the exact failure this event exists to prevent.
func TestCompactionIsAnItemNotANotification(t *testing.T) {
	var evs []harness.Event
	r := newRun(harness.Turn{}, func(ev harness.Event) { evs = append(evs, ev) })

	r.notify("item/started", []byte(`{"item":{"id":"c1","type":"contextCompaction"}}`))
	if notes := find(evs, harness.EventNote); len(notes) != 0 {
		t.Fatalf("the started frame already reported it: %+v", notes)
	}

	r.notify("item/completed", []byte(`{"item":{"id":"c1","type":"contextCompaction"}}`))
	notes := find(evs, harness.EventNote)
	if len(notes) != 1 {
		t.Fatalf("notes %d, want exactly 1: %+v", len(notes), notes)
	}
	if !strings.Contains(notes[0].Text, "codex") || !strings.Contains(notes[0].Text, "compacted") {
		t.Fatalf("note %q", notes[0].Text)
	}

	// The method name that reads correct stays inert on purpose: if codex
	// ever starts sending it, this fails loudly instead of quietly
	// reporting every compaction twice.
	r.notify("thread/compacted", []byte(`{"threadId":"t1"}`))
	if notes := find(evs, harness.EventNote); len(notes) != 1 {
		t.Fatalf("thread/compacted added a second note: %+v", notes)
	}
}
