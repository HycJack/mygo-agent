package app

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"mygo-agent/internal/config"
	"mygo-agent/internal/harness"
)

// builtinFixture wires a test app to a fake streaming provider whose
// odd calls request a bash tool run and even calls finish the turn.
func builtinFixture(t *testing.T, dir string) (*app, *httptest.Server) {
	t.Helper()
	a := newTestApp(t)
	a.workdir = dir
	a.backend = "builtin"
	// Full access: these tests exercise the loop and the ChatLog, not the
	// sandbox (agent mode runs the shell inside it, covered by the
	// sandbox tests on platforms that have one).
	a.mode = 2
	a.projects = []Project{{ID: "p1", Path: dir}}
	a.activeProject = "p1"

	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		if n%2 == 1 {
			fmt.Fprint(w, "event: response.output_item.added\n"+
				`data: {"type":"response.output_item.added","item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"bash","arguments":""}}`+"\n\n"+
				"event: response.output_item.done\n"+
				`data: {"type":"response.output_item.done","item":{"type":"function_call","call_id":"call_1","name":"bash","arguments":"{\"command\":\"echo hello-from-tool\"}"}}`+"\n\n"+
				"event: response.completed\n"+
				`data: {"type":"response.completed","response":{"output":[]}}`+"\n\n")
			return
		}
		fmt.Fprint(w, "event: response.output_text.delta\n"+
			`data: {"type":"response.output_text.delta","delta":"all done"}`+"\n\n"+
			"event: response.completed\n"+
			`data: {"type":"response.completed","response":{"output":[]}}`+"\n\n")
	}))
	a.providers = []config.Provider{{
		ID: "p1", Name: "Test", BaseURL: srv.URL, APIKey: "k",
		Models: []string{"test-model"}, Wire: harness.WireResponses,
	}}
	a.providerID, a.model = "p1", "test-model"
	t.Cleanup(srv.Close)
	return a, srv
}

func TestBuiltinChatLogRoundTrip(t *testing.T) {
	dir := t.TempDir()
	a, _ := builtinFixture(t, dir)
	th := &Thread{ID: "t1", ProjectID: "p1"}
	th.Messages = []Message{{ID: "m0", Role: "assistant", Running: true}}
	a.threads = append(a.threads, th)
	a.current = "t1"

	a.runStart("t1", func() {}) // the run registry is dispatch's; tests seed it
	go runBackend(a, th, "run the tool", 0)
	waitTurn(t, a, th, len(th.Messages)-1)

	// The tool card captured the real echo output, and the reply that
	// followed it sits AFTER it in the ordered sequence — that ordering is
	// the point of the sequence, not an accident of this test.
	m := &th.Messages[0]
	if len(m.Blocks) != 2 {
		t.Fatalf("blocks: %+v", m.Blocks)
	}
	if m.Blocks[0].Type != blockCommand || m.Blocks[0].Output != "hello-from-tool" {
		t.Fatalf("the first block is not the tool call: %+v", m.Blocks[0])
	}
	if m.Blocks[1].Type != blockText || !strings.Contains(m.Blocks[1].Text, "all done") {
		t.Fatalf("the reply did not follow the tool call: %+v", m.Blocks[1])
	}
	if !strings.Contains(m.Text, "all done") {
		t.Fatalf("reply text %q", m.Text)
	}
	// The ChatLog holds the full round trip.
	if len(th.ChatLog) != 5 {
		t.Fatalf("chat log %d turns: %+v", len(th.ChatLog), th.ChatLog)
	}
	roles := []string{}
	for _, c := range th.ChatLog {
		roles = append(roles, c.Role)
	}
	want := "system,user,assistant,tool,assistant"
	if strings.Join(roles, ",") != want {
		t.Fatalf("roles %v, want %v", roles, want)
	}
	// The placeholder marked where its turn began.
	if m.LogAt != 2 {
		t.Fatalf("LogAt %d, want 2", m.LogAt)
	}
}

func TestBuiltinChatLogPersistsAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	a, _ := builtinFixture(t, dir)
	store := filepath.Join(t.TempDir(), "threads")
	a.threadsDir = threadsLayout{root: store}
	a.workdir = dir
	th := &Thread{ID: "t1", ProjectID: "p1"}
	th.Messages = []Message{{ID: "m0", Role: "assistant", Running: true}}
	a.threads = append(a.threads, th)
	a.current = "t1"

	a.runStart("t1", func() {}) // the run registry is dispatch's; tests seed it
	go runBackend(a, th, "run the tool", 0)
	waitTurn(t, a, th, len(th.Messages)-1)

	// Restart: load the persisted threads — the ChatLog comes back.
	b := newTestApp(t)
	b.threadsDir = threadsLayout{root: store}
	b.activeProject = "p1"
	b.loadThreads()
	if len(b.threads) != 1 || len(b.threads[0].ChatLog) != 5 {
		t.Fatalf("chat log did not persist: %+v", b.threads)
	}
}

func TestRegenerateRewindsChatLog(t *testing.T) {
	dir := t.TempDir()
	a, _ := builtinFixture(t, dir)
	th := &Thread{ID: "t1", ProjectID: "p1"}
	a.threads = append(a.threads, th)
	a.current = "t1"

	a.draft = "run the tool"
	a.send()
	waitTurn(t, a, th, len(th.Messages)-1)
	before := len(th.ChatLog)
	if before == 0 {
		t.Fatal("no chat log after the first turn")
	}

	// Regenerate: the log rewinds to where the turn began, then the
	// retry fills it again.
	a.regenerate(th)
	waitTurn(t, a, th, len(th.Messages)-1)
	if len(th.ChatLog) != before {
		t.Fatalf("chat log %d after regenerate, want %d", len(th.ChatLog), before)
	}
	if len(th.Messages) != 2 || th.Messages[1].Role != "assistant" {
		t.Fatalf("messages after regenerate: %d", len(th.Messages))
	}
}

// TestToolEndOpensHeredocWrites: a multi-line command opens on finish —
// a relay member's heredoc file-writes are the work, and hiding them
// behind one truncated title line made the transcript read as nothing
// but collapsed rows.
func TestToolEndOpensHeredocWrites(t *testing.T) {
	a := newTestApp(t)
	now := time.Now()
	th := &Thread{ID: "t1", ProjectID: "default", Created: now, Updated: now,
		Messages: []Message{{ID: "m0", Role: "assistant", Running: true, At: now}}}
	a.threads = append(a.threads, th)
	th.Messages[0].Blocks = []Block{{Type: blockCommand, Text: "$ cat > f <<'EOF'\ncode\nEOF", Running: true, Exit: -1}}

	a.applyEvent(th, 0, "builtin", harness.Event{
		Kind: harness.EventToolEnd, Exit: 0, Ms: 12, Output: "",
	})

	if !th.Messages[0].Blocks[0].Open {
		t.Fatal("a heredoc write stayed collapsed on finish")
	}
}
