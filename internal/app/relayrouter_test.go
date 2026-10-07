package app

// The router relay (spec/relay-router.md): the coordinator decides who
// speaks next and when the relay is done, with hard guards behind it.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"mygo-agent/internal/harness"
)

// TestParseRouteReply pins the tolerant reading of the coordinator's
// answer: strict JSON first, then the first {...} span.
func TestParseRouteReply(t *testing.T) {
	cases := []struct {
		name, in, next string
		wantErr        bool
	}{
		{"plain", `{"next":"B","reason":"review"}`, "B", false},
		{"fenced", "```json\n{\"next\":\"B\",\"reason\":\"review\"}\n```", "B", false},
		{"surrounded", `Sure! {"next":"B","reason":"review"} hope that helps`, "B", false},
		{"end marker", `{"next":"","reason":"all done"}`, "", false},
		{"not json", `I would pick B next.`, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := parseRouteReply(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseRouteReply(%q) = %+v, want error", tc.in, r)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseRouteReply(%q): %v", tc.in, err)
			}
			if r.Next != tc.next {
				t.Fatalf("next = %q, want %q", r.Next, tc.next)
			}
		})
	}
}

// routerFixture serves scripted coordinator answers (the content of a
// chat completion's message), records every request body, and can hold
// a call until released.
func routerFixture(t *testing.T, release <-chan struct{}, replies ...string) (*httptest.Server, *[]string) {
	t.Helper()
	var mu sync.Mutex
	var bodies []string
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(data))
		i := calls
		if i >= len(replies) {
			i = len(replies) - 1
		}
		calls++
		mu.Unlock()
		if release != nil {
			<-release
		}
		content := replies[i]
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"content":`+quoteJSON(content)+`}}]}`)
	}))
	mu.Lock()
	defer mu.Unlock()
	return srv, &bodies
}

// quoteJSON keeps the fixture's JSON building honest about
// embedding the content string.
func quoteJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func testSnapshot(baseURL string) panelSnapshot {
	return panelSnapshot{
		threadID: "t1",
		roster:   []relayMember{{Name: "A", Desc: "architect"}, {Name: "B", Desc: "reviewer"}},
		digest:   "User: hi\n\nA: hello\n",
		at:       2,
		model:    "qwen3:8b",
		baseURL:  baseURL,
	}
}

func decisionSnapshot(baseURL string) panelSnapshot {
	snap := testSnapshot(baseURL)
	snap.wire = "decision"
	snap.model = "tev1"
	return snap
}

// decisionFixture serves canned systemone answers (raw JSON, no chat
// wrapper — the decision wire's response has no choices) and records
// every request body.
func decisionFixture(t *testing.T, answers map[string]any) (*httptest.Server, *[]string) {
	t.Helper()
	var mu sync.Mutex
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(data))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, mustJSON(map[string]any{"answers": answers}))
	}))
	mu.Lock()
	defer mu.Unlock()
	return srv, &bodies
}

// mustJSON marshals or panics — fixtures only ever hold literal maps.
func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func TestRouteDecisionSystemone(t *testing.T) {
	choiceB := map[string]any{"type": "choice", "choice": "B",
		"probabilities": map[string]float64{"A": 0.4, "B": 0.6}, "confidence": 0.7}
	t.Run("picks a member", func(t *testing.T) {
		srv, bodies := decisionFixture(t, map[string]any{
			"next": choiceB,
			"done": map[string]any{"type": "noul", "noul": 0.2},
		})
		defer srv.Close()
		route, err := routeDecision(t.Context(), decisionSnapshot(srv.URL))
		if err != nil || route.Next != "B" {
			t.Fatalf("route = %+v, err = %v", route, err)
		}
		if !strings.Contains(route.Reason, "0.60") || !strings.Contains(route.Reason, "0.70") {
			t.Fatalf("the scores never made the reason: %q", route.Reason)
		}
		var req struct {
			Questions map[string]json.RawMessage `json:"questions"`
			State     map[string]json.RawMessage `json:"state"`
		}
		if err := json.Unmarshal([]byte((*bodies)[0]), &req); err != nil {
			t.Fatal(err)
		}
		var next struct {
			Criteria map[string]string `json:"criteria"`
		}
		if err := json.Unmarshal(req.Questions["next"], &next); err != nil {
			t.Fatal(err)
		}
		if next.Criteria["A"] != "architect" || len(next.Criteria) != 2 {
			t.Fatalf("the choice criteria are not the roster: %v", next.Criteria)
		}
		if _, ok := req.Questions["done"]; !ok {
			t.Fatal("the done question is missing")
		}
		var full struct {
			State struct {
				Members      []map[string]any `json:"members"`
				Conversation string           `json:"conversation"`
			} `json:"state"`
		}
		if err := json.Unmarshal([]byte((*bodies)[0]), &full); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(full.State.Conversation, "A: hello") || len(full.State.Members) != 2 {
			t.Fatalf("the state lacks the transcript or the roster: %s", (*bodies)[0])
		}
	})
	t.Run("done ends the relay", func(t *testing.T) {
		srv, _ := decisionFixture(t, map[string]any{
			"next": choiceB,
			"done": map[string]any{"type": "noul", "noul": 0.9},
		})
		defer srv.Close()
		route, err := routeDecision(t.Context(), decisionSnapshot(srv.URL))
		if err != nil || route.Next != "" || !strings.Contains(route.Reason, "done") {
			t.Fatalf("route = %+v, err = %v", route, err)
		}
	})
	t.Run("a panel of one asks no choice", func(t *testing.T) {
		srv, bodies := decisionFixture(t, map[string]any{
			"done": map[string]any{"type": "noul", "noul": 0.1},
		})
		defer srv.Close()
		snap := decisionSnapshot(srv.URL)
		snap.roster = snap.roster[:1]
		route, err := routeDecision(t.Context(), snap)
		if err != nil || route.Next != "A" {
			t.Fatalf("route = %+v, err = %v", route, err)
		}
		if strings.Contains((*bodies)[0], `"next"`) {
			t.Fatalf("a choice was asked for a one-member panel: %s", (*bodies)[0])
		}
	})
	t.Run("malformed answers degrade", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, `{"answers": {}}`)
		}))
		defer srv.Close()
		if _, err := routeDecision(t.Context(), decisionSnapshot(srv.URL)); err == nil {
			t.Fatal("a choice-less answer was accepted")
		}
	})
	t.Run("off-roster choice degrades", func(t *testing.T) {
		srv, _ := decisionFixture(t, map[string]any{
			"next": map[string]any{"type": "choice", "choice": "Ghost",
				"probabilities": map[string]float64{"Ghost": 1.0}, "confidence": 0.9},
			"done": map[string]any{"type": "noul", "noul": 0.0},
		})
		defer srv.Close()
		if _, err := routeDecision(t.Context(), decisionSnapshot(srv.URL)); err == nil {
			t.Fatal("an off-roster choice was accepted")
		}
	})
}

// TestRouteDecisionCoversTheWire covers the client end to end against
// a fake OpenAI-compatible server: success, the corrective round-trip
// for a hallucinated name, and the failures that degrade the relay.
func TestRouteDecisionCoversTheWire(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		srv, bodies := routerFixture(t, nil, `{"next":"B","reason":"needs review"}`)
		defer srv.Close()
		route, err := routeDecision(t.Context(), testSnapshot(srv.URL))
		if err != nil || route.Next != "B" || route.Reason != "needs review" {
			t.Fatalf("route = %+v, err = %v", route, err)
		}
		if len(*bodies) != 1 || !strings.Contains((*bodies)[0], "reviewer") ||
			!strings.Contains((*bodies)[0], "A: hello") {
			t.Fatalf("the brief lacks the roster or the transcript: %s", (*bodies)[0])
		}
	})
	t.Run("unknown name is corrected once", func(t *testing.T) {
		srv, bodies := routerFixture(t, nil, `{"next":"Ghost"}`, `{"next":"B"}`)
		defer srv.Close()
		route, err := routeDecision(t.Context(), testSnapshot(srv.URL))
		if err != nil || route.Next != "B" {
			t.Fatalf("route = %+v, err = %v", route, err)
		}
		if len(*bodies) != 2 || !strings.Contains((*bodies)[1], "not on the roster") {
			t.Fatalf("the correction never reached the coordinator: %v", *bodies)
		}
	})
	t.Run("garbage degrades", func(t *testing.T) {
		srv, _ := routerFixture(t, nil, `not json at all`)
		defer srv.Close()
		if _, err := routeDecision(t.Context(), testSnapshot(srv.URL)); err == nil {
			t.Fatal("garbage was accepted")
		}
	})
	t.Run("server error degrades", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "down", http.StatusInternalServerError)
		}))
		defer srv.Close()
		if _, err := routeDecision(t.Context(), testSnapshot(srv.URL)); err == nil {
			t.Fatal("a failing router was accepted")
		}
	})
	t.Run("no endpoint", func(t *testing.T) {
		if _, err := routeDecision(t.Context(), testSnapshot("")); err == nil {
			t.Fatal("a missing endpoint was accepted")
		}
	})
}

// routerRelayFixture wires a two-member panel whose relay is routed by
// the fake coordinator. The member server is the responses-wire fixture
// the sequence tests use; the router server is chat JSON.
func routerRelayFixture(t *testing.T, release <-chan struct{}, memberReplies []string, routes ...string) (*app, *Thread, *httptest.Server, *[]string) {
	t.Helper()
	a := newTestApp(t)
	a.backend = "builtin"
	a.mode = 2
	var bodies []string
	var calls int
	memberSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(data))
		text := "all done"
		if calls < len(memberReplies) {
			text = memberReplies[calls]
		}
		calls++
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: response.output_text.delta\n"+
			`data: {"type":"response.output_text.delta","delta":"`+text+`"}`+"\n\n"+
			"event: response.completed\n"+
			`data: {"type":"response.completed","response":{"output":[]}}`+"\n\n")
	}))
	routerSrv, routerBodies := routerFixture(t, release, routes...)
	a.providers = []Provider{
		{ID: "p1", Name: "Test", BaseURL: memberSrv.URL, APIKey: "k",
			Models: []string{"test-model"}, Wire: harness.WireResponses},
		{ID: "prt", Name: "Router", BaseURL: routerSrv.URL, Models: []string{"qwen3:8b"}},
	}
	a.providerID, a.model = "p1", "test-model"
	a.agents = []Agent{
		{ID: "default", Name: "Default"},
		{ID: "ag-a", Name: "A", SystemPrompt: "MEMBER-A-HEAD"},
		{ID: "ag-b", Name: "B", SystemPrompt: "MEMBER-B-HEAD"},
		{ID: "ag-team", Name: "Team", Panel: []string{"A", "B"}, PanelRoute: "router",
			RouterProvider: "prt", RouterModel: "qwen3:8b"},
	}
	now := time.Now()
	th := &Thread{ID: "t1", ProjectID: "default", AgentID: "ag-team", Created: now, Updated: now}
	a.threads = append(a.threads, th)
	_ = routerBodies
	return a, th, memberSrv, &bodies
}

func noteText(m Message) string {
	var b strings.Builder
	for _, x := range m.Blocks {
		if x.Type == blockNote {
			b.WriteString(x.Text)
			b.WriteString(" | ")
		}
	}
	return b.String()
}

// TestLiveDecisionRouter runs the decision wire against a real local
// coordinator (Ollama serving tev1). Skipped unless MYGO_LIVE_ROUTER is
// set — the suite must not depend on a daemon — and it documents the
// one smoke check the fake servers cannot: that the wire contract
// holds against the model itself.
func TestLiveDecisionRouter(t *testing.T) {
	if os.Getenv("MYGO_LIVE_ROUTER") == "" {
		t.Skip("set MYGO_LIVE_ROUTER=1 with ollama serving tev1 to run")
	}
	snap := decisionSnapshot("http://localhost:11434/v1")
	route, err := routeDecision(t.Context(), snap)
	if err != nil {
		t.Fatalf("live decision failed: %v", err)
	}
	t.Logf("route: next=%q reason=%q", route.Next, route.Reason)
	if !routeNameKnown(route.Next, snap.roster) {
		t.Fatalf("live coordinator named %q, off the roster", route.Next)
	}
}

// TestRouterRelayFollowsCoordinator runs the relay end to end against
// the fake members and the fake coordinator.
func TestRouterRelayFollowsCoordinator(t *testing.T) {
	a, th, srv, bodies := routerRelayFixture(t, nil,
		[]string{"alpha", "beta"},
		`{"next":"B","reason":"needs review"}`,
		`{"next":"","reason":"all done"}`)
	defer srv.Close()
	a.startTurn(th, "plan the thing")
	waitTurn(t, a, th, 2)

	if len(th.Messages) != 3 {
		t.Fatalf("messages = %d, want user + A + B", len(th.Messages))
	}
	if th.Messages[1].AgentID != "ag-a" || th.Messages[2].AgentID != "ag-b" {
		t.Fatalf("attribution: %q then %q", th.Messages[1].AgentID, th.Messages[2].AgentID)
	}
	// The coordinator's decision rides along as a note and reaches the
	// member in the handoff prompt.
	if note := noteText(th.Messages[2]); !strings.Contains(note, "→ B") || !strings.Contains(note, "needs review") {
		t.Fatalf("B's note = %q", note)
	}
	if !strings.Contains((*bodies)[1], "handed the floor") {
		t.Fatal("B never learned why the floor came to it")
	}
	// The coordinator ended the relay: no third reply, nothing running.
	a.update(func() {
		if a.isRunning("t1") || a.groupQueue["t1"] != nil {
			t.Fatal("the relay left state behind")
		}
	})
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if len(th.Messages) > 3 {
			t.Fatal("the relay continued past the coordinator's end")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestRouterRelayRoundLimit(t *testing.T) {
	a, th, srv, _ := routerRelayFixture(t, nil,
		[]string{"alpha", "alpha", "alpha"},
		`{"next":"A","reason":"keep going"}`)
	defer srv.Close()
	a.update(func() {
		a.agentByName("Team").PanelMaxRounds = 2
	})
	a.startTurn(th, "go")
	waitTurn(t, a, th, 2)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		settled := false
		a.update(func() {
			settled = !a.isRunning("t1") && a.groupQueue["t1"] == nil
		})
		if settled {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(th.Messages) != 3 {
		t.Fatalf("messages = %d, want the round limit to stop at two replies", len(th.Messages))
	}
	if note := noteText(th.Messages[2]); !strings.Contains(note, "round limit") {
		t.Fatalf("the round-limit note is missing: %q", note)
	}
}

func TestRouterRelayStallGuard(t *testing.T) {
	a, th, srv, _ := routerRelayFixture(t, nil,
		[]string{"alpha", "alpha", "alpha", "alpha"},
		`{"next":"A","reason":"keep going"}`)
	defer srv.Close()
	// No PanelMaxRounds: the default 8 applies, the stall guard ends it
	// first.
	a.startTurn(th, "go")
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		settled := false
		a.update(func() {
			settled = !a.isRunning("t1") && a.groupQueue["t1"] == nil
		})
		if settled {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(th.Messages) != 5 {
		t.Fatalf("messages = %d, want the stall guard to stop at four replies", len(th.Messages))
	}
	if note := noteText(th.Messages[4]); !strings.Contains(note, "stalled") {
		t.Fatalf("the stall note is missing: %q", note)
	}
}

func TestRouterRelayUnknownMemberDegrades(t *testing.T) {
	a, th, srv, _ := routerRelayFixture(t, nil,
		[]string{"alpha"},
		`{"next":"Zed","reason":"who?"}`)
	defer srv.Close()
	a.startTurn(th, "go")
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		settled := false
		a.update(func() {
			settled = !a.isRunning("t1") && a.groupQueue["t1"] == nil
		})
		if settled {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(th.Messages) != 2 {
		t.Fatalf("messages = %d, want the relay to end after the unknown name", len(th.Messages))
	}
	if note := noteText(th.Messages[1]); !strings.Contains(note, "Zed") {
		t.Fatalf("the degrade note is missing: %q", note)
	}
}

func TestRouterRelayStopDuringRouting(t *testing.T) {
	release := make(chan struct{})
	a, th, srv, _ := routerRelayFixture(t, release,
		[]string{"alpha"},
		`{"next":"B","reason":"needs review"}`)
	defer srv.Close()
	a.startTurn(th, "go")
	waitTurn(t, a, th, 1) // A's reply settled; the router call is now in flight
	a.stopThread("t1")    // while the coordinator thinks
	close(release)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		done := false
		a.update(func() { done = !a.isRunning("t1") })
		if done {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(th.Messages) != 2 {
		t.Fatalf("messages = %d, want the stop to drop the routing decision", len(th.Messages))
	}
}
