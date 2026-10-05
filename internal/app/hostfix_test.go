package app

import (
	"strings"
	"sync"
	"testing"

	"mygo-agent/internal/harness"
)

// The Host owns every thread field. These tests pin the two rules that
// keep a turn from corrupting or racing that ownership
// (spec/architecture.md, "Threading").

// TestLoadTranscriptIsolatesTheCaller proves the Memory read hands back a
// copy: the built-in loop appends to what it is given, and a shared
// backing array would race the main thread's own saves.
func TestLoadTranscriptIsolatesTheCaller(t *testing.T) {
	a := newTestApp(t)
	th := &Thread{ID: "t1", ProjectID: "default", ChatLog: []harness.ChatMessage{{Role: "user", Content: "hi"}}}
	a.threads = []*Thread{th}
	m := threadMemory{a: a}
	key := harness.MemoryKey("default", "t1")

	got := m.LoadTranscript(key)
	if len(got) != 1 {
		t.Fatalf("LoadTranscript returned %d messages, want 1", len(got))
	}
	// Mutate the returned slice the way the loop does when it appends a
	// turn: the host's own transcript must not change underneath it.
	got = append(got, harness.ChatMessage{Role: "assistant", Content: "appended"})
	got[0].Content = "mutated"
	if len(th.ChatLog) != 1 || th.ChatLog[0].Content != "hi" {
		t.Fatalf("the caller's slice aliases host state: %+v", th.ChatLog)
	}
}

// TestLoadTranscriptSurvivesConcurrentThreadEdits runs the Memory read
// against a main thread that keeps mutating the thread list. Under -race
// this is the test that would have caught the unsynchronized read.
func TestLoadTranscriptSurvivesConcurrentThreadEdits(t *testing.T) {
	a := newTestApp(t)
	th := &Thread{ID: "t1", ProjectID: "default", ChatLog: []harness.ChatMessage{{Role: "user", Content: "hi"}}}
	a.threads = []*Thread{th}
	m := threadMemory{a: a}
	key := harness.MemoryKey("default", "t1")

	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(2)
	go func() { // the "harness goroutine"
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			_ = m.LoadTranscript(key)
		}
	}()
	go func() { // the main thread
		defer wg.Done()
		for i := 0; i < 200; i++ {
			a.update(func() {
				a.threads = append(a.threads, &Thread{ID: "x", ProjectID: "default"})
				a.threads = a.threads[:len(a.threads)-1]
			})
		}
		close(stop)
	}()
	wg.Wait()
}

// TestStoreTranscriptCopies proves the write path snapshots too: the loop
// hands over a slice it may keep appending to.
func TestStoreTranscriptCopies(t *testing.T) {
	a := newTestApp(t)
	th := &Thread{ID: "t1", ProjectID: "default"}
	a.threads = []*Thread{th}
	m := threadMemory{a: a}
	key := harness.MemoryKey("default", "t1")

	sent := []harness.ChatMessage{{Role: "user", Content: "one"}}
	m.StoreTranscript(key, sent)
	sent[0].Content = "changed after the store"
	if th.ChatLog[0].Content != "one" {
		t.Fatalf("the stored transcript aliases the caller's slice: %+v", th.ChatLog)
	}
}

// TestProjectorIgnoresEventsAfterSettlement pins the second threading
// rule: the Host calls finish the instant Run returns, so a late event
// from an adapter whose reader outlives it must not land in a message
// that is already done and saved.
func TestProjectorIgnoresEventsAfterSettlement(t *testing.T) {
	a := newTestApp(t)
	th := &Thread{ID: "t1", ProjectID: "default"}
	th.Messages = []Message{{ID: "m0", Role: "assistant", Running: true}}
	a.threads = []*Thread{th}

	a.applyEvent(th, 0, "builtin", harness.Event{Kind: harness.EventText, TextDelta: "before "})
	a.finish(th, 0, "")
	// The adapter's reader goroutine outlives Run and emits one more.
	a.applyEvent(th, 0, "builtin", harness.Event{Kind: harness.EventText, TextDelta: "after"})
	a.applyEvent(th, 0, "builtin", harness.Event{Kind: harness.EventToolStart,
		ToolCall: harness.ToolCall{ID: "c1", Function: struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		}{Name: "bash", Arguments: `{"command":"rm -rf /"}`}}})

	if got := th.Messages[0].Text; got != "before " {
		t.Fatalf("a settled message took %q; it must keep exactly what arrived before finish", got)
	}
	// Count cards, not blocks: the prose that arrived before finish is
	// itself an ordered text block, and it is supposed to be there.
	cards := 0
	for _, b := range th.Messages[0].Blocks {
		if b.Type != blockText {
			cards++
		}
	}
	if cards != 0 {
		t.Fatalf("a settled message gained %d card(s) after finish", cards)
	}
}

// TestToolDisplayRedactsSecrets proves a card title never carries a
// credential: the card is persisted into the thread file, not just drawn.
func TestToolDisplayRedactsSecrets(t *testing.T) {
	cases := []struct {
		name            string
		call            harness.ToolCall
		absent, present []string
	}{
		{
			name:    "bash command with a bearer token",
			call:    toolCall("bash", `{"command":"curl -H 'Authorization: Bearer sk-abcdefgh12345678' https://api.test"}`),
			absent:  []string{"sk-abcdefgh12345678"},
			present: []string{"Authorization", "api.test"},
		},
		{
			name:    "read_file of a credential path still shows the path",
			call:    toolCall("read_file", `{"path":"~/.aws/credentials"}`),
			present: []string{"read_file", ".aws/credentials"},
		},
		{
			name:    "mcp tool keeps its free-form arguments bounded",
			call:    toolCall("mcp_github_create_issue", `{"title":"bug","body":"details"}`),
			present: []string{"github_create_issue", "bug"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := toolDisplayFor(tc.call)
			for _, s := range tc.absent {
				if strings.Contains(got, s) {
					t.Fatalf("card title %q leaks %q", got, s)
				}
			}
			for _, s := range tc.present {
				if !strings.Contains(got, s) {
					t.Fatalf("card title %q lost %q; it must stay readable", got, s)
				}
			}
		})
	}
}

// TestModeAndEffortAreClamped proves the dispatch boundary cannot pass a
// value an adapter would index with. Nothing in the app recovers from a
// panic, so the clamp has to happen here.
func TestModeAndEffortAreClamped(t *testing.T) {
	a := newTestApp(t)
	th := &Thread{ID: "t1", ProjectID: "default"}

	for _, mode := range []int{-3, 0, 1, 2, 9, 1 << 20} {
		a.mode = mode
		turn := a.turnFor(th, "hi")
		if turn.Mode < harness.ModeReadOnly || turn.Mode > harness.ModeFull {
			t.Fatalf("mode %d reached the adapters as %d", mode, turn.Mode)
		}
		// Unknown values fail toward the most restrictive mode.
		if mode < 0 || mode > 2 {
			if turn.Mode != harness.ModeReadOnly {
				t.Fatalf("mode %d should clamp to read-only, got %d", mode, turn.Mode)
			}
		}
	}
	for _, eff := range []int{-1, 0, 2, 7, 1 << 20} {
		a.effort = eff
		turn := a.turnFor(th, "hi")
		if turn.Effort < 0 || turn.Effort > 2 {
			t.Fatalf("effort %d reached the adapters as %d", eff, turn.Effort)
		}
	}
}

// TestSetModeClamps guards the UI entry point too, so an out-of-range
// value never even reaches app state. An unknown mode fails to
// read-only: clamping to the nearest end would turn a corrupt 42 into
// FULL ACCESS, and clamping down would silently demote a real "agent".
func TestSetModeClamps(t *testing.T) {
	a := newTestApp(t)
	h := homeActions{a: a}
	h.SetMode(42)
	if a.mode != int(harness.ModeReadOnly) {
		t.Fatalf("SetMode(42) stored %d, want %d (unknown fails closed)", a.mode, int(harness.ModeReadOnly))
	}
	h.SetMode(-5)
	if a.mode != int(harness.ModeReadOnly) {
		t.Fatalf("SetMode(-5) stored %d, want %d", a.mode, int(harness.ModeReadOnly))
	}
	// The legitimate modes pass through untouched.
	h.SetMode(int(harness.ModeFull))
	if a.mode != int(harness.ModeFull) {
		t.Fatalf("SetMode(2) stored %d, want %d", a.mode, int(harness.ModeFull))
	}
	h.SetMode(int(harness.ModeAgent))
	if a.mode != int(harness.ModeAgent) {
		t.Fatalf("SetMode(1) stored %d, want %d", a.mode, int(harness.ModeAgent))
	}
	h.SetEffort(99)
	if a.effort != 2 {
		t.Fatalf("SetEffort(99) stored %d, want 2", a.effort)
	}
}

// TestBuiltinHarnessSnapshotsConfig proves a turn reads the provider it
// started with: changing the provider mid-run must not swap the endpoint
// underneath the approval cards the user is deciding on.
func TestBuiltinHarnessSnapshotsConfig(t *testing.T) {
	a := newTestApp(t)
	a.providers = []Provider{{ID: "p1", Name: "one", BaseURL: "https://one.test", APIKey: "k1", Models: []string{"m"}}}
	a.providerID = "p1"

	h := newBuiltinHarness(a, &Thread{ID: "t1", ProjectID: "default"}, harness.Turn{Workdir: a.workdir})
	if h.provider.BaseURL != "https://one.test" {
		t.Fatalf("snapshot base URL = %q", h.provider.BaseURL)
	}
	// Mutate live state after the snapshot was taken.
	a.providers[0].BaseURL = "https://two.test"
	a.providers[0].APIKey = "k2"
	if h.provider.BaseURL != "https://one.test" || h.provider.APIKey != "k1" {
		t.Fatalf("the snapshot followed live state: %+v", h.provider)
	}
}

func toolCall(name, args string) harness.ToolCall {
	return harness.ToolCall{
		ID:   "c1",
		Type: "function",
		Function: struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		}{Name: name, Arguments: args},
	}
}
