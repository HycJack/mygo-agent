package app

// The group relay (spec/agents.md): a thread bound to an agent with a
// panel gets one reply per member, in order, each seeing the earlier
// replies in the shared conversation.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"mygo-agent/internal/harness"
)

// panelFixture serves scripted replies (one per call, in order) and
// records every request body.
func panelFixture(t *testing.T, replies ...string) (*app, *httptest.Server, *[]string) {
	t.Helper()
	a := newTestApp(t)
	a.backend = "builtin"
	a.mode = 2
	var bodies []string
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(data))
		w.Header().Set("Content-Type", "text/event-stream")
		text := "all done"
		if calls < len(replies) {
			text = replies[calls]
		}
		calls++
		fmt.Fprint(w, "event: response.output_text.delta\n"+
			`data: {"type":"response.output_text.delta","delta":"`+text+`"}`+"\n\n"+
			"event: response.completed\n"+
			`data: {"type":"response.completed","response":{"output":[]}}`+"\n\n")
	}))
	a.providers = []Provider{{ID: "p1", Name: "Test", BaseURL: srv.URL, APIKey: "k",
		Models: []string{"test-model"}, Wire: harness.WireResponses}}
	a.providerID, a.model = "p1", "test-model"
	return a, srv, &bodies
}

func TestPanelRelayRunsMembersInOrder(t *testing.T) {
	a, srv, bodies := panelFixture(t, "alpha", "beta")
	defer srv.Close()
	a.agents = []Agent{
		{ID: "default", Name: "Default"},
		{ID: "ag-a", Name: "A", SystemPrompt: "MEMBER-A-HEAD"},
		{ID: "ag-b", Name: "B", SystemPrompt: "MEMBER-B-HEAD"},
		{ID: "ag-team", Name: "Team", Panel: []string{"A", "B"}},
	}

	now := time.Now()
	th := &Thread{ID: "t1", ProjectID: "default", AgentID: "ag-team", Created: now, Updated: now}
	a.threads = append(a.threads, th)
	a.startTurn(th, "plan the thing")
	waitTurn(t, a, th, 2) // the last member's reply

	if len(th.Messages) != 3 {
		t.Fatalf("messages = %d, want user + two member replies", len(th.Messages))
	}
	r1, r2 := th.Messages[1], th.Messages[2]
	if r1.AgentID != "ag-a" || r2.AgentID != "ag-b" {
		t.Fatalf("attribution: %q then %q", r1.AgentID, r2.AgentID)
	}
	if r1.Text != "alpha" || r2.Text != "beta" {
		t.Fatalf("replies: %q then %q", r1.Text, r2.Text)
	}
	if len(*bodies) != 2 {
		t.Fatalf("provider calls = %d, want one per member", len(*bodies))
	}
	// Shared context: the second member's request carries the first
	// member's reply, its own head (not the first member's), and the
	// relay nudge.
	second := (*bodies)[1]
	if !strings.Contains(second, "alpha") {
		t.Fatal("the second member did not see the first member's reply")
	}
	if !strings.Contains(second, "MEMBER-B-HEAD") || strings.Contains(second, "MEMBER-A-HEAD") {
		t.Fatal("the second member ran under the wrong system head")
	}
	if !strings.Contains(second, panelNudge) {
		t.Fatal("the relay nudge did not reach the next member")
	}
	if !strings.Contains((*bodies)[0], "MEMBER-A-HEAD") {
		t.Fatal("the first member lost its own head")
	}
	// The relay is over: the registry and queue are empty.
	a.update(func() {
		if a.isRunning("t1") || len(a.groupQueue["t1"]) != 0 {
			t.Fatal("the relay left state behind")
		}
	})
}

func TestPanelStopMidChain(t *testing.T) {
	a, srv, _ := panelFixture(t, "alpha", "beta")
	defer srv.Close()
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(3 * time.Second) // the first member hangs; the stop lands mid-turn
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: response.output_text.delta\n"+
			`data: {"type":"response.output_text.delta","delta":"alpha"}`+"\n\n"+
			"event: response.completed\n"+
			`data: {"type":"response.completed","response":{"output":[]}}`+"\n\n")
	})
	a.agents = []Agent{
		{ID: "default", Name: "Default"},
		{ID: "ag-a", Name: "A"},
		{ID: "ag-b", Name: "B"},
		{ID: "ag-team", Name: "Team", Panel: []string{"A", "B"}},
	}
	now := time.Now()
	th := &Thread{ID: "t1", ProjectID: "default", AgentID: "ag-team", Created: now, Updated: now}
	a.threads = append(a.threads, th)
	a.startTurn(th, "go")
	for deadline := time.Now().Add(5 * time.Second); ; {
		running := false
		a.update(func() { running = a.isRunning("t1") })
		if running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the relay never started")
		}
		time.Sleep(10 * time.Millisecond)
	}
	a.stopThread("t1")
	for deadline := time.Now().Add(5 * time.Second); ; {
		done := false
		a.update(func() { done = !a.isRunning("t1") })
		if done {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the stop never settled")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// A stopped relay does not continue: no second reply may appear.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if len(th.Messages) > 2 {
			t.Fatal("the stopped relay continued to the next member")
		}
		time.Sleep(20 * time.Millisecond)
	}
	a.update(func() {
		if len(a.groupQueue["t1"]) != 0 {
			t.Fatal("the queue survived the stop")
		}
	})
}

func TestPanelSettingsRoundTrip(t *testing.T) {
	a := newTestApp(t)
	a.agents = []Agent{
		{ID: "default", Name: "Default"},
		{ID: "ag-a", Name: "A"},
		{ID: "ag-b", Name: "B"},
		{ID: "ag-team", Name: "Team"},
	}

	vm := a.settingsVM()
	ai := -1
	for i := range vm.Agents {
		if vm.Agents[i].ID == "ag-team" {
			ai = i
		}
	}
	vm.Agents[ai].Panel = []string{"A", "B"}
	a.syncSettings(vm)

	team := a.agentByName("Team")
	if team == nil || len(team.Panel) != 2 || team.Panel[0] != "A" {
		t.Fatalf("panel did not sync: %+v", team)
	}
	if sub := a.agentSub(team); sub != "panel · 2 agents" {
		t.Fatalf("subtitle = %q", sub)
	}
	// A member deleted after being queued is skipped, not fatal.
	a.groupQueue["t1"] = []string{"ag-a", "ghost", "ag-b"}
	a.runStart("t1", func() {}) // the relay pops only while the run lives
	if got := a.nextPanelMember(&Thread{ID: "t1"}); got == nil || got.ID != "ag-a" {
		t.Fatalf("first pop = %+v", got)
	}
	if got := a.nextPanelMember(&Thread{ID: "t1"}); got == nil || got.ID != "ag-b" {
		t.Fatalf("ghost not skipped: %+v", got)
	}
	if got := a.nextPanelMember(&Thread{ID: "t1"}); got != nil {
		t.Fatalf("queue did not empty: %+v", got)
	}
}

// TestPanelDigestBoundsAndFormats pins the CLI member's handoff: the
// digest names the speakers, keeps the recent tail, and drops empty
// messages.
func TestPanelDigestBoundsAndFormats(t *testing.T) {
	a := newTestApp(t)
	a.agents = []Agent{
		{ID: "default", Name: "Default"},
		{ID: "ag-b", Name: "B", SystemPrompt: "MEMBER-B-HEAD"},
	}
	now := time.Now()
	th := &Thread{ID: "t1", ProjectID: "default", AgentID: "default", Created: now, Updated: now,
		Messages: []Message{
			{ID: "m0", Role: "user", Text: "plan the thing", At: now},
			{ID: "m1", Role: "assistant", AgentID: "default", Text: "here is the plan", At: now},
			{ID: "m2", Role: "assistant", AgentID: "default", Text: "", At: now},
			{ID: "m3", Role: "assistant", Running: true, AgentID: "ag-b", At: now},
		}}
	got := a.panelDigest(th, 3, 8<<10)
	for _, want := range []string{"User: plan the thing", "Default: here is the plan"} {
		if !strings.Contains(got, want) {
			t.Fatalf("digest lacks %q: %q", want, got)
		}
	}
	if strings.Contains(got, "B:") {
		t.Fatalf("the digest includes the member being briefed: %q", got)
	}
	small := a.panelDigest(th, 3, 16)
	if len(small) > 16+2+len("User: plan the thing") {
		t.Fatalf("the tail bound did not hold: %d bytes", len(small))
	}
	_ = json.Marshal
}
