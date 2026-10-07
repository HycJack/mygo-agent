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
		request:  "review my pr",
		last:     "A",
		spoken:   []string{"A"},
		rounds:   1,
		model:    "qwen3:8b",
		baseURL:  baseURL,
	}
}

func decisionSnapshot(baseURL string) panelSnapshot {
	snap := testSnapshot(baseURL)
	snap.wire = "decision"
	snap.model = "tev1"
	snap.judgeBaseURL, snap.judgeAPIKey, snap.judgeModel = baseURL, "k", "tev1"
	snap.request = "review my pr"
	snap.last = "A"
	return snap
}

// hybridSnapshot wires the two-level coordinator: the advisor (a big
// chat model) on the chat endpoint, the judge (tev1) on the decision
// endpoint.
func hybridSnapshot(advisorURL, judgeURL string) panelSnapshot {
	snap := testSnapshot(advisorURL)
	snap.wire = "hybrid"
	snap.judgeBaseURL, snap.judgeAPIKey, snap.judgeModel = judgeURL, "k", "tev1"
	snap.request = "review my pr"
	snap.last = "A"
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
		// Open choices left by a member are routable work, not an end.
		if !strings.Contains((*bodies)[0], "open options") {
			t.Fatal("the advisor was never told to flag pending choices")
		}
		// The rotation state rides ahead of the digest: without it a
		// coordinator cannot even take fair turns.
		if !strings.Contains((*bodies)[0], "Speakers in order: A") ||
			!strings.Contains((*bodies)[0], "Not yet spoken: B") ||
			!strings.Contains((*bodies)[0], "The user's request") {
			t.Fatalf("the brief lacks the rotation state or the request: %s", (*bodies)[0])
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

// TestRouteDecisionHybrid covers the two-level coordinator: the
// advisor's brief rides the judge's state, a dead advisor degrades to
// the digest state, and the scores still make the decision.
func TestRouteDecisionHybrid(t *testing.T) {
	judge := func(t *testing.T, answers map[string]any) (*httptest.Server, *[]string) {
		return decisionFixture(t, answers)
	}
	advisor := func(t *testing.T, content string) (*httptest.Server, *int) {
		var calls int
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"choices":[{"message":{"content":`+quoteJSON(content)+`}}]}`)
		}))
		return srv, &calls
	}
	t.Run("brief rides the judge's state", func(t *testing.T) {
		asrv, advisorCalls := advisor(t, `{"brief":"review is mid-flight; security checks remain"}`)
		defer asrv.Close()
		jsrv, bodies := judge(t, map[string]any{
			"next": map[string]any{"type": "choice", "choice": "B",
				"probabilities": map[string]float64{"A": 0.3, "B": 0.7}, "confidence": 0.8},
			"done": map[string]any{"type": "noul", "noul": 0.1},
		})
		defer jsrv.Close()
		route, err := routeDecision(t.Context(), hybridSnapshot(asrv.URL, jsrv.URL))
		if err != nil || route.Next != "B" {
			t.Fatalf("route = %+v, err = %v", route, err)
		}
		// The member's handoff gets the comprehension AND the scores.
		if !strings.Contains(route.Reason, "security checks") || !strings.Contains(route.Reason, "0.70") {
			t.Fatalf("reason lost the brief or the scores: %q", route.Reason)
		}
		if *advisorCalls != 1 {
			t.Fatalf("advisor calls = %d, want 1", *advisorCalls)
		}
		var req struct {
			State map[string]any `json:"state"`
		}
		if err := json.Unmarshal([]byte((*bodies)[0]), &req); err != nil {
			t.Fatal(err)
		}
		if req.State["brief"] == nil || req.State["request"] != "review my pr" || req.State["last_speaker"] != "A" {
			t.Fatalf("the judge's state lacks the brief, the request or the last speaker: %v", req.State)
		}
		if _, ok := req.State["conversation"]; ok {
			t.Fatal("the hybrid judge read the raw digest instead of the brief")
		}
	})
	t.Run("a dead advisor falls back to the digest", func(t *testing.T) {
		asrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "down", http.StatusInternalServerError)
		}))
		defer asrv.Close()
		jsrv, bodies := judge(t, map[string]any{
			"next": map[string]any{"type": "choice", "choice": "A",
				"probabilities": map[string]float64{"A": 0.9, "B": 0.1}, "confidence": 0.6},
			"done": map[string]any{"type": "noul", "noul": 0.2},
		})
		defer jsrv.Close()
		route, err := routeDecision(t.Context(), hybridSnapshot(asrv.URL, jsrv.URL))
		if err != nil || route.Next != "A" {
			t.Fatalf("route = %+v, err = %v", route, err)
		}
		var req struct {
			State map[string]any `json:"state"`
		}
		if err := json.Unmarshal([]byte((*bodies)[0]), &req); err != nil {
			t.Fatal(err)
		}
		if req.State["conversation"] == nil || req.State["brief"] != nil {
			t.Fatalf("the fallback did not judge on the digest: %v", req.State)
		}
	})
	t.Run("done still ends it", func(t *testing.T) {
		asrv, _ := advisor(t, `{"brief":"everything addressed"}`)
		defer asrv.Close()
		jsrv, _ := judge(t, map[string]any{
			"done": map[string]any{"type": "noul", "noul": 0.95},
		})
		defer jsrv.Close()
		route, err := routeDecision(t.Context(), hybridSnapshot(asrv.URL, jsrv.URL))
		if err != nil || route.Next != "" || !strings.Contains(route.Reason, "done") {
			t.Fatalf("route = %+v, err = %v", route, err)
		}
	})
}

// TestRelayInterjection delivers the user's mid-relay message to the
// next member: it lands in the transcript while the coordinator thinks
// and rides the next handoff prompt.
func TestRelayInterjection(t *testing.T) {
	release := make(chan struct{})
	a, th, srv, bodies := routerRelayFixture(t, release,
		[]string{"alpha", "beta"},
		`{"next":"B","reason":"after the user's note"}`)
	defer srv.Close()
	a.current = "t1"
	a.startTurn(th, "plan the thing")
	waitTurn(t, a, th, 1) // A settled; the coordinator is thinking

	a.draft = "focus on the security angle"
	a.send()
	a.update(func() {
		if a.draft != "" {
			t.Fatal("the interjection left the draft behind")
		}
	})
	if len(th.Messages) != 3 || th.Messages[2].Role != "user" || th.Messages[2].Text != "focus on the security angle" {
		t.Fatalf("the interjection never joined the transcript: %+v", th.Messages)
	}
	close(release) // the coordinator answers; B is dispatched with the user's words
	waitTurn(t, a, th, 3)
	if !strings.Contains((*bodies)[1], "focus on the security angle") {
		t.Fatal("B never saw the user's interjection")
	}
	if !strings.Contains((*bodies)[1], "The user added while the panel was talking") {
		t.Fatal("B was not told the words came mid-relay")
	}
}

// TestRelayNotInterjectable pins the negative: a sequence relay and a
// solo turn keep the old behavior — a send during a running turn is
// dropped and the draft stays.
func TestRelayNotInterjectable(t *testing.T) {
	a := newTestApp(t)
	a.agents = []Agent{
		{ID: "default", Name: "Default"},
		{ID: "ag-a", Name: "A"},
		{ID: "ag-team", Name: "Team", Panel: []string{"A"}}, // sequence
	}
	th := &Thread{ID: "t1", ProjectID: "default", AgentID: "ag-team"}
	a.threads = append(a.threads, th)
	a.runStart("t1", func() {})
	a.groupQueue["t1"] = &relayState{queue: []string{}, rounds: 1, start: time.Now()}
	a.interject(th, "hello?")
	if len(th.Messages) != 0 {
		t.Fatal("a sequence relay took an interjection")
	}
	// A solo turn has no relay state at all.
	th2 := &Thread{ID: "t2", ProjectID: "default"}
	a.threads = append(a.threads, th2)
	a.interject(th2, "hello?")
	if len(th2.Messages) != 0 {
		t.Fatal("a solo turn took an interjection")
	}
}

// TestRelayGuards walks the configurable stops: each guard ends the
// relay with a note saying which one fired (spec/relay-router.md).
func TestRelayGuards(t *testing.T) {
	cases := []struct {
		name  string
		ag    Agent
		st    relayState
		value string // the note fragment the fired guard must carry
	}{
		{"round limit", Agent{PanelMaxRounds: 2},
			relayState{rounds: 2, start: time.Now()}, "round limit (2)"},
		{"token budget", Agent{PanelMaxTokens: 100},
			relayState{rounds: 1, tokens: 150, start: time.Now()}, "token budget (150 of 100)"},
		{"timeout", Agent{PanelTimeout: 10},
			relayState{rounds: 1, start: time.Now().Add(-time.Minute)}, "timed out after 10s"},
		{"stall default", Agent{},
			relayState{rounds: 1, last: "A", sameStreak: 3, start: time.Now()}, `stalled on "A" for 3`},
		{"stall custom", Agent{PanelStallRounds: 2},
			relayState{rounds: 1, last: "B", sameStreak: 2, start: time.Now()}, `stalled on "B" for 2`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newTestApp(t)
			a.agents = []Agent{
				{ID: "default", Name: "Default"},
				{ID: "ag-a", Name: "A"},
				{ID: "ag-team", Name: "Team", Panel: []string{"A"}, PanelRoute: "router"},
			}
			for i := range a.agents {
				if a.agents[i].ID == "ag-team" {
					a.agents[i].PanelMaxRounds = tc.ag.PanelMaxRounds
					a.agents[i].PanelStallRounds = tc.ag.PanelStallRounds
					a.agents[i].PanelMaxTokens = tc.ag.PanelMaxTokens
					a.agents[i].PanelTimeout = tc.ag.PanelTimeout
				}
			}
			now := time.Now()
			th := &Thread{ID: "t1", ProjectID: "default", AgentID: "ag-team", Created: now, Updated: now,
				Messages: []Message{{ID: "m0", Role: "assistant", AgentID: "ag-a", Text: "settled", At: now}}}
			a.threads = append(a.threads, th)
			a.runStart("t1", func() {})
			st := tc.st
			a.groupQueue["t1"] = &st

			a.routeRelay(th, 0)

			a.update(func() {
				if a.isRunning("t1") || a.groupQueue["t1"] != nil {
					t.Fatal("the guard did not end the relay")
				}
			})
			if note := noteText(th.Messages[0]); !strings.Contains(note, tc.value) {
				t.Fatalf("note = %q, want it to carry %q", note, tc.value)
			}
		})
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
	// Members speak under the panel protocol: they decide and recommend
	// instead of handing choices back to the user.
	if !strings.Contains((*bodies)[1], "do not end your reply by handing the question back to the user") {
		t.Fatal("the panel protocol never reached the member")
	}
	// The coordinator ended the relay — and said so on the transcript:
	// no third reply, the reason lands as a note, nothing running. The
	// end decision is a second router call, so settle before asserting.
	deadline := time.Now().Add(20 * time.Second)
	for {
		settled := false
		a.update(func() {
			settled = !a.isRunning("t1") && a.groupQueue["t1"] == nil
		})
		if settled {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the relay never settled")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if note := noteText(th.Messages[2]); !strings.Contains(note, "all done") {
		t.Fatalf("the coordinator's end reason never surfaced: %q", note)
	}
	for deadline := time.Now().Add(500 * time.Millisecond); time.Now().Before(deadline); {
		if len(th.Messages) > 3 {
			t.Fatal("the relay continued past the coordinator's end")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestGroupDialogStartsRouted pins the group dialog's default: a group
// created there routes (spec/relay-router.md) — a bare panel would run
// one sequence pass and stop, which is exactly the stall the router
// exists to fix.
func TestGroupDialogStartsRouted(t *testing.T) {
	a := newTestApp(t)
	a.agents = []Agent{
		{ID: "ag-a", Name: "A"},
		{ID: "ag-b", Name: "B"},
	}
	a.groupDraftName = "设计组"
	a.groupDraftOn["ag-a"] = true
	a.groupDraftOn["ag-b"] = true
	a.startGroupChat()

	group := a.agentByName("设计组")
	if group == nil || len(group.Panel) != 2 {
		t.Fatalf("the group profile was not created: %+v", group)
	}
	if group.PanelRoute != "router" {
		t.Fatalf("the group defaults to %q, want router", group.PanelRoute)
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
