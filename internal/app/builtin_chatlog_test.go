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

	"mygo-agent/internal/agent"
	"mygo-agent/internal/config"
)

// builtinFixture wires a test app to a fake streaming provider whose
// odd calls request a bash tool run and even calls finish the turn.
func builtinFixture(t *testing.T, dir string) (*app, *httptest.Server) {
	t.Helper()
	a := newTestApp(t)
	a.workdir = dir
	a.backend = "builtin"
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
		Models: []string{"test-model"}, Wire: agent.WireResponses,
	}}
	a.providerID, a.model = "p1", "test-model"
	t.Cleanup(srv.Close)
	return a, srv
}

func waitForBuiltin(t *testing.T, timeout time.Duration, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if done() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timed out")
}

func TestBuiltinChatLogRoundTrip(t *testing.T) {
	dir := t.TempDir()
	a, _ := builtinFixture(t, dir)
	th := &Thread{ID: "t1", ProjectID: "p1"}
	th.Messages = []Message{{ID: "m0", Role: "assistant", Running: true}}
	a.threads = append(a.threads, th)
	a.current = "t1"

	a.running = true
	a.running = true
	go a.runBuiltin(th, "run the tool", 0)
	waitForBuiltin(t, 15*time.Second, func() bool { return !a.running })

	// The tool card captured the real echo output.
	m := &th.Messages[0]
	if len(m.Blocks) != 1 || m.Blocks[0].Output != "hello-from-tool" {
		t.Fatalf("blocks: %+v", m.Blocks)
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
	cfgPath := filepath.Join(t.TempDir(), "threads.json")
	a.savePath = cfgPath
	a.workdir = dir
	th := &Thread{ID: "t1", ProjectID: "p1"}
	th.Messages = []Message{{ID: "m0", Role: "assistant", Running: true}}
	a.threads = append(a.threads, th)
	a.current = "t1"

	a.running = true
	a.running = true
	go a.runBuiltin(th, "run the tool", 0)
	waitForBuiltin(t, 15*time.Second, func() bool { return !a.running })

	// Restart: load the persisted threads — the ChatLog comes back.
	b := newTestApp(t)
	b.savePath = cfgPath
	b.load()
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
	waitForBuiltin(t, 15*time.Second, func() bool { return !a.running })
	before := len(th.ChatLog)
	if before == 0 {
		t.Fatal("no chat log after the first turn")
	}

	// Regenerate: the log rewinds to where the turn began, then the
	// retry fills it again.
	a.regenerate(th)
	waitForBuiltin(t, 15*time.Second, func() bool { return !a.running })
	if len(th.ChatLog) != before {
		t.Fatalf("chat log %d after regenerate, want %d", len(th.ChatLog), before)
	}
	if len(th.Messages) != 2 || th.Messages[1].Role != "assistant" {
		t.Fatalf("messages after regenerate: %d", len(th.Messages))
	}
}
