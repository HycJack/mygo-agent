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
		fmt.Fprint(w, `{"choices":[{"message":{"content":`+quoteJSON(content)+`}}],"usage":{"total_tokens":123}}`)
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
// the sequence tests use; the router server is chat JSON. Both sides'
// request bodies are recorded.
func routerRelayFixture(t *testing.T, release <-chan struct{}, memberReplies []string, routes ...string) (*app, *Thread, *httptest.Server, *[]string, *[]string) {
	t.Helper()
	a := newTestApp(t)
	a.backend = "builtin"
	a.mode = 2
	var mu sync.Mutex
	var bodies []string
	var calls int
	memberSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(data))
		text := "all done"
		if calls < len(memberReplies) {
			text = memberReplies[calls]
		}
		calls++
		mu.Unlock()
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
	return a, th, memberSrv, &bodies, routerBodies
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
	a, th, srv, bodies, _ := routerRelayFixture(t, release,
		[]string{"alpha", "beta"},
		`{"next":"B","reason":"after the user's note"}`)
	defer srv.Close()
	a.current = "t1"
	a.startTurn(th, "plan the thing")
	// The first-speaker routing is in flight (the run registry is held).
	deadline := time.Now().Add(10 * time.Second)
	for {
		running := false
		a.update(func() { running = a.isRunning("t1") })
		if running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the first routing never started")
		}
		time.Sleep(10 * time.Millisecond)
	}

	a.draft = "focus on the security angle"
	a.send()
	a.update(func() {
		if a.draft != "" {
			t.Fatal("the interjection left the draft behind")
		}
	})
	if len(th.Messages) != 2 || th.Messages[1].Role != "user" || th.Messages[1].Text != "focus on the security angle" {
		t.Fatalf("the interjection never joined the transcript: %+v", th.Messages)
	}
	close(release) // the coordinator answers; B is dispatched with the user's words
	waitTurn(t, a, th, 2)
	if !strings.Contains((*bodies)[0], "focus on the security angle") {
		t.Fatal("B never saw the user's interjection")
	}
	if !strings.Contains((*bodies)[0], "The user added while the panel was talking") {
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
			relayState{rounds: 1, last: "A", sameStreak: 5, start: time.Now()}, `stalled on "A" for 5`},
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

			// The guard also triggers the wrap-up turn; the synthesizer
			// (the bound agent, no provider here) fails fast and the
			// turn ends. Settle before asserting.
			deadline := time.Now().Add(10 * time.Second)
			for {
				settled := false
				a.update(func() {
					settled = !a.isRunning("t1") && a.groupQueue["t1"] == nil
				})
				if settled {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("the guard did not end the relay")
				}
				time.Sleep(20 * time.Millisecond)
			}
			if note := noteText(th.Messages[0]); !strings.Contains(note, tc.value) {
				t.Fatalf("note = %q, want it to carry %q", note, tc.value)
			}
		})
	}
}

// TestPanelProtocolReachesEveryBackend pins the channel split: builtin
// and claude members get the protocol via their system prompt, codex
// and pi — which drop turn.SystemPrompt — via the handoff text.
func TestPanelProtocolReachesEveryBackend(t *testing.T) {
	a := newTestApp(t)
	a.agents = []Agent{
		{ID: "default", Name: "Default"},
		{ID: "ag-builtin", Name: "B", Backend: "builtin"},
		{ID: "ag-claude", Name: "C", Backend: "claude"},
		{ID: "ag-codex", Name: "X", Backend: "codex"},
	}
	if a.protocolViaPrompt(a.agentByID("ag-builtin")) || a.protocolViaPrompt(a.agentByID("ag-claude")) {
		t.Fatal("builtin/claude should carry the protocol in their system prompt")
	}
	if !a.protocolViaPrompt(a.agentByID("ag-codex")) {
		t.Fatal("codex drops the system prompt — the protocol must ride the handoff text")
	}
	// The profile itself stays clean: routing and settings read the
	// original prompt.
	proto := panelMemberAgent(a.agentByID("ag-codex"))
	if !strings.Contains(proto.SystemPrompt, panelProtocol) {
		t.Fatal("the dispatched profile lost the protocol")
	}
	if strings.Contains(a.agentByID("ag-codex").SystemPrompt, panelProtocol) {
		t.Fatal("the protocol leaked into the stored profile")
	}
}

// TestRouterRelayFollowsCoordinator runs the relay end to end against
// the fake members and the fake coordinator.
func TestRouterRelayFollowsCoordinator(t *testing.T) {
	a, th, srv, bodies, routerBodies := routerRelayFixture(t, nil,
		[]string{"alpha", "beta"},
		`{"next":"B","reason":"needs review"}`,
		`{"next":"","reason":"all done"}`)
	defer srv.Close()
	a.startTurn(th, "plan the thing")
	waitTurn(t, a, th, 1) // the coordinator picks the first speaker too

	if th.Messages[1].AgentID != "ag-b" {
		t.Fatalf("first speaker = %q, want the coordinator's pick B", th.Messages[1].AgentID)
	}
	// The coordinator's decision rides along as a note and reaches the
	// member in the handoff prompt.
	if note := noteText(th.Messages[1]); !strings.Contains(note, "→ B") || !strings.Contains(note, "needs review") {
		t.Fatalf("B's note = %q", note)
	}
	// The coordinator ended the relay — and said so on the transcript —
	// then the wrap-up turn synthesizes the conclusion. The end decision
	// is a second router call, so settle before asserting anything that
	// reads the fixtures' recorded bodies: a handler goroutine may still
	// be appending.
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
	// The first routing call sees the request and the roster before
	// anyone has spoken.
	if !strings.Contains((*routerBodies)[0], "plan the thing") || !strings.Contains((*routerBodies)[0], "MEMBER-B-HEAD") {
		t.Fatalf("the first brief lacks the request or the roster: %s", (*routerBodies)[0])
	}
	if !strings.Contains((*bodies)[0], "handed the floor") {
		t.Fatal("B never learned why the floor came to it")
	}
	// Members speak under the panel protocol: they decide and recommend
	// instead of handing choices back to the user.
	if !strings.Contains((*bodies)[0], "do not end your reply by handing the question back to the user") {
		t.Fatal("the panel protocol never reached the member")
	}
	if note := noteText(th.Messages[1]); !strings.Contains(note, "all done") {
		t.Fatalf("the coordinator's end reason never surfaced: %q", note)
	}
	// The wrap-up: the thread's own agent (no panel_summarizer set)
	// writes the conclusion the user asked for.
	if len(th.Messages) != 3 {
		t.Fatalf("messages = %d, want user + member + wrap-up", len(th.Messages))
	}
	if th.Messages[2].AgentID != "ag-team" {
		t.Fatalf("the wrap-up author = %q, want the bound agent", th.Messages[2].AgentID)
	}
	if note := noteText(th.Messages[2]); !strings.Contains(note, "final wrap-up") {
		t.Fatalf("the wrap-up note is missing: %q", note)
	}
	for deadline := time.Now().Add(500 * time.Millisecond); time.Now().Before(deadline); {
		if len(th.Messages) > 3 {
			t.Fatal("the relay continued past the wrap-up")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestRelayWrapUpDesignee pins panel_summarizer: the designated member
// writes the conclusion instead of the bound agent.
func TestRelayWrapUpDesignee(t *testing.T) {
	a, th, srv, _, _ := routerRelayFixture(t, nil,
		[]string{"alpha", "beta"},
		`{"next":"A","reason":"start at requirements"}`,
		`{"next":"","reason":"all done"}`)
	defer srv.Close()
	a.update(func() { a.agentByName("Team").PanelSummarizer = "A" })
	a.startTurn(th, "go")
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
	if th.Messages[2].AgentID != "ag-a" {
		t.Fatalf("the wrap-up author = %q, want the designated member A", th.Messages[2].AgentID)
	}
	if note := noteText(th.Messages[2]); !strings.Contains(note, "final wrap-up") {
		t.Fatalf("the wrap-up note is missing: %q", note)
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
	a, th, srv, _, _ := routerRelayFixture(t, nil,
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
	if len(th.Messages) != 4 {
		t.Fatalf("messages = %d, want the round limit to stop at two replies plus the wrap-up", len(th.Messages))
	}
	if note := noteText(th.Messages[2]); !strings.Contains(note, "round limit") {
		t.Fatalf("the round-limit note is missing: %q", note)
	}
	if th.Messages[3].AgentID != "ag-team" {
		t.Fatalf("the wrap-up author = %q, want the bound agent", th.Messages[3].AgentID)
	}
}

func TestRouterRelayStallGuard(t *testing.T) {
	a, th, srv, _, _ := routerRelayFixture(t, nil,
		[]string{"alpha", "alpha", "alpha"},
		`{"next":"A","reason":"keep going"}`)
	defer srv.Close()
	// A tight stall cap: three consecutive replies from one member end
	// the relay (the default is five — a member deep in real work
	// legitimately speaks more than three times in a row). The cap
	// counts the routed first speaker too.
	a.update(func() { a.agentByName("Team").PanelStallRounds = 3 })
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
		t.Fatalf("messages = %d, want the stall guard to stop at three replies plus the wrap-up", len(th.Messages))
	}
	if note := noteText(th.Messages[3]); !strings.Contains(note, "stalled") {
		t.Fatalf("the stall note is missing: %q", note)
	}
	if th.Messages[4].AgentID != "ag-team" {
		t.Fatalf("the wrap-up author = %q, want the bound agent", th.Messages[4].AgentID)
	}
}

func TestRouterRelayUnknownMemberDegrades(t *testing.T) {
	a, th, srv, _, _ := routerRelayFixture(t, nil,
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
	if len(th.Messages) != 3 {
		t.Fatalf("messages = %d, want the relay to end after the unknown name plus the wrap-up", len(th.Messages))
	}
	if note := noteText(th.Messages[1]); !strings.Contains(note, "Zed") {
		t.Fatalf("the degrade note is missing: %q", note)
	}
	if th.Messages[2].AgentID != "ag-team" {
		t.Fatalf("the wrap-up author = %q, want the bound agent", th.Messages[2].AgentID)
	}
}

func TestRouterRelayStopDuringRouting(t *testing.T) {
	release := make(chan struct{})
	a, th, srv, _, _ := routerRelayFixture(t, release,
		[]string{"alpha"},
		`{"next":"B","reason":"needs review"}`)
	defer srv.Close()
	a.startTurn(th, "go")
	// The first-speaker routing is in flight (the run registry is held).
	deadline := time.Now().Add(10 * time.Second)
	for {
		running := false
		a.update(func() { running = a.isRunning("t1") })
		if running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the first routing never started")
		}
		time.Sleep(10 * time.Millisecond)
	}
	a.stopThread("t1") // while the coordinator thinks
	close(release)
	for time.Now().Before(deadline) {
		done := false
		a.update(func() { done = !a.isRunning("t1") })
		if done {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(th.Messages) != 1 {
		t.Fatalf("messages = %d, want the stop to drop the routing decision", len(th.Messages))
	}
}

// TestMemberDescPrefersBlurb: the routing roster reads panel_blurb
// when the member wrote one — retuning the coordinator's criteria
// never touches the member's persona.
func TestMemberDescPrefersBlurb(t *testing.T) {
	ag := &Agent{Name: "A", SystemPrompt: "PERSONA-LINE\nrest", PanelBlurb: "ROUTING-LINE"}
	if got := memberDesc(ag); got != "ROUTING-LINE" {
		t.Fatalf("desc = %q, want the blurb", got)
	}
	if got := memberDesc(&Agent{Name: "B", SystemPrompt: "PERSONA-LINE\nrest"}); got != "PERSONA-LINE" {
		t.Fatalf("desc = %q, want the prompt head", got)
	}
}

// TestRouteDecisionCountsTokens pins the usage ride-along: the trace's
// route line carries what the coordinator call cost.
func TestRouteDecisionCountsTokens(t *testing.T) {
	srv, _ := routerFixture(t, nil, `{"next":"B","reason":"go"}`)
	defer srv.Close()
	route, err := routeDecision(t.Context(), testSnapshot(srv.URL))
	if err != nil || route.Next != "B" {
		t.Fatalf("route = %+v, err = %v", route, err)
	}
	if route.Tokens != 123 {
		t.Fatalf("tokens = %d, want the reply's usage", route.Tokens)
	}
}

// TestRelayMemberRetry: a member turn that dies on the wire is
// replayed once — a provider hiccup must not write the member off —
// and the retry is visible as a note.
func TestRelayMemberRetry(t *testing.T) {
	a := newTestApp(t)
	a.backend = "builtin"
	a.mode = 2
	var calls int
	memberSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls <= 3 {
			// The whole first turn fails: doWithRetry burns its three
			// attempts on these, so the failure reaches finish.
			http.Error(w, "provider hiccup", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: response.output_text.delta\n"+
			`data: {"type":"response.output_text.delta","delta":"recovered"}`+"\n\n"+
			"event: response.completed\n"+
			`data: {"type":"response.completed","response":{"output":[]}}`+"\n\n")
	}))
	routerSrv, _ := routerFixture(t, nil,
		`{"next":"A","reason":"start at the top"}`,
		`{"next":"","reason":"all done"}`)
	a.providers = []Provider{
		{ID: "p1", Name: "Test", BaseURL: memberSrv.URL, APIKey: "k",
			Models: []string{"test-model"}, Wire: harness.WireResponses},
		{ID: "prt", Name: "Router", BaseURL: routerSrv.URL, Models: []string{"qwen3:8b"}},
	}
	a.providerID, a.model = "p1", "test-model"
	a.agents = []Agent{
		{ID: "default", Name: "Default"},
		{ID: "ag-a", Name: "A", SystemPrompt: "MEMBER-A-HEAD"},
		{ID: "ag-team", Name: "Team", Panel: []string{"A"}, PanelRoute: "router",
			RouterProvider: "prt", RouterModel: "qwen3:8b"},
	}
	now := time.Now()
	th := &Thread{ID: "t1", ProjectID: "default", AgentID: "ag-team", Created: now, Updated: now}
	a.threads = append(a.threads, th)
	a.startTurn(th, "go")

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
	// user + failed A + recovered A + wrap-up.
	if len(th.Messages) != 4 {
		t.Fatalf("messages = %d, want the failed turn, its replay and the wrap-up", len(th.Messages))
	}
	if th.Messages[2].AgentID != "ag-a" || !strings.Contains(th.Messages[2].Text, "recovered") {
		t.Fatalf("the replayed turn: %+v", th.Messages[2])
	}
	if note := noteText(th.Messages[2]); !strings.Contains(note, "retrying") {
		t.Fatalf("the retry note is missing: %q", note)
	}
}

// TestRelayHandoffSkipsTheCoordinator: a member who names the next
// member with @ passes the floor directly — one routing call for the
// whole turn instead of two.
func TestRelayHandoffSkipsTheCoordinator(t *testing.T) {
	a, th, srv, _, routerBodies := routerRelayFixture(t, nil,
		[]string{"alpha @B 该你了", "beta"},
		`{"next":"A","reason":"start at the top"}`,
		`{"next":"","reason":"all done"}`)
	defer srv.Close()
	a.startTurn(th, "go")
	deadline := time.Now().Add(20 * time.Second)
	for {
		settled := false
		a.update(func() { settled = !a.isRunning("t1") && a.groupQueue["t1"] == nil })
		if settled {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the relay never settled")
		}
		time.Sleep(20 * time.Millisecond)
	}
	// user + A + B + wrap-up; the router was called twice — first
	// speaker and the end — the handoff between them never asked.
	if len(th.Messages) != 4 {
		t.Fatalf("messages = %d, want user + A + B + wrap-up", len(th.Messages))
	}
	if th.Messages[2].AgentID != "ag-b" {
		t.Fatalf("the handoff landed on %q, want B", th.Messages[2].AgentID)
	}
	if len(*routerBodies) != 2 {
		t.Fatalf("router calls = %d, want the handoff to skip the coordinator", len(*routerBodies))
	}
	if note := noteText(th.Messages[2]); !strings.Contains(note, "straight from A") {
		t.Fatalf("B's note lost the handoff: %q", note)
	}
}

// TestRelayOutlineRidesTheBrief: each settled reply contributes a
// bounded gist to the coordinator's brief, so early decisions survive
// the tail-bounded digest.
func TestRelayOutlineRidesTheBrief(t *testing.T) {
	a, th, srv, _, routerBodies := routerRelayFixture(t, nil,
		[]string{"beta"},
		`{"next":"B","reason":"needs review"}`,
		`{"next":"","reason":"all done"}`)
	defer srv.Close()
	a.startTurn(th, "plan the thing")
	deadline := time.Now().Add(20 * time.Second)
	for {
		settled := false
		a.update(func() { settled = !a.isRunning("t1") && a.groupQueue["t1"] == nil })
		if settled {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the relay never settled")
		}
		time.Sleep(20 * time.Millisecond)
	}
	// The end decision's brief carries the outline: what B established.
	if len(*routerBodies) < 2 {
		t.Fatalf("router calls = %d", len(*routerBodies))
	}
	if !strings.Contains((*routerBodies)[1], "What each reply established") ||
		!strings.Contains((*routerBodies)[1], "- B: beta") {
		t.Fatalf("the end brief lacks the outline: %s", (*routerBodies)[1])
	}
}

// TestExtractMentionsOrderAndDedup: mentions come back in appearance
// order, deduped; a second mention of the same member does not dispatch
// it twice.
func TestExtractMentionsOrderAndDedup(t *testing.T) {
	roster := []relayMember{{Name: "架构"}, {Name: "测试"}, {Name: "发布"}}
	got := extractMentions("@测试 先看风险，@架构 定方案，最后 @测试 复核", roster)
	if len(got) != 2 || got[0] != "测试" || got[1] != "架构" {
		t.Fatalf("mentions = %v, want 测试 then 架构", got)
	}
	if got := extractMentions("no mentions here", roster); len(got) != 0 {
		t.Fatalf("mentions = %v, want none", got)
	}
}

// fanoutFixture wires three panel members whose replies hold 300ms, and
// records each request's arrival: two members genuinely in flight overlap
// — the second arrives before the first's hold expires — while a serial
// dispatch arrives only after the first finished. No gate, no way for a
// late request (the wrap-up turn) to wedge the server.
func fanoutFixture(t *testing.T, memberTail string, routes ...string) (*app, *Thread, *httptest.Server, *map[string]time.Time) {
	t.Helper()
	a := newTestApp(t)
	a.backend = "builtin"
	a.mode = 2
	arrivals := map[string]time.Time{}
	routerSrv, _ := routerFixture(t, nil, routes...)
	memberSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		body := string(data)
		who := "wrapup"
		for _, h := range []string{"MEMBER-A-HEAD", "MEMBER-B-HEAD", "MEMBER-C-HEAD"} {
			if strings.Contains(body, h) {
				who = h
			}
		}
		arrivalMu.Lock()
		arrivals[who] = time.Now()
		arrivalMu.Unlock()
		time.Sleep(300 * time.Millisecond)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: response.output_text.delta\n"+
			`data: {"type":"response.output_text.delta","delta":"done by `+who+` `+memberTail+`"}`+"\n\n"+
			"event: response.completed\n"+
			`data: {"type":"response.completed","response":{"output":[]}}`+"\n\n")
	}))
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
		{ID: "ag-c", Name: "C", SystemPrompt: "MEMBER-C-HEAD"},
		{ID: "ag-team", Name: "Team", Panel: []string{"A", "B", "C"}, PanelRoute: "router",
			RouterProvider: "prt", RouterModel: "qwen3:8b"},
	}
	now := time.Now()
	th := &Thread{ID: "t1", ProjectID: "default", AgentID: "ag-team", Created: now, Updated: now}
	a.threads = append(a.threads, th)
	return a, th, memberSrv, &arrivals
}

var arrivalMu sync.Mutex

func waitSettled(t *testing.T, a *app, th *Thread) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		settled := false
		a.update(func() { settled = !a.isRunning(th.ID) && a.groupQueue[th.ID] == nil })
		if settled {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the relay never settled")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestComposerMentionFansOut: the user naming two members dispatches
// them in parallel — B's request arrives while C's is still being held —
// and the relay routes only after both land.
func TestComposerMentionFansOut(t *testing.T) {
	a, th, srv, arrivals := fanoutFixture(t, "",
		`{"next":"","reason":"both answered"}`)
	defer srv.Close()
	a.startTurn(th, "@B @C evaluate this")
	waitSettled(t, a, th)

	// user + the two parallel members + wrap-up.
	if len(th.Messages) != 4 {
		t.Fatalf("messages = %d, want user + B + C + wrap-up", len(th.Messages))
	}
	if th.Messages[1].AgentID != "ag-b" || th.Messages[2].AgentID != "ag-c" {
		t.Fatalf("attribution: %q then %q", th.Messages[1].AgentID, th.Messages[2].AgentID)
	}
	ab, ac := (*arrivals)["MEMBER-B-HEAD"], (*arrivals)["MEMBER-C-HEAD"]
	if ab.IsZero() || ac.IsZero() {
		t.Fatalf("a member never arrived: B=%v C=%v", ab, ac)
	}
	first, second := ab, ac
	if second.Before(first) {
		first, second = second, first
	}
	if second.Before(first.Add(250 * time.Millisecond)) {
		t.Fatalf("the members did not overlap: arrivals %v then %v — serial dispatch",
			first.Format("15:04:05.000"), second.Format("15:04:05.000"))
	}
	if th.Messages[3].AgentID != "ag-team" {
		t.Fatalf("wrap-up author = %q", th.Messages[3].AgentID)
	}
}

// TestMemberHandoffFansOut: a member naming two peers hands the floor
// to both at once, no coordinator call in between.
func TestMemberHandoffFansOut(t *testing.T) {
	a, th, srv, _ := fanoutFixture(t, "@B @C 你们的看法呢",
		`{"next":"A","reason":"start at the top"}`,
		`{"next":"","reason":"all done"}`)
	defer srv.Close()
	a.startTurn(th, "go")
	waitSettled(t, a, th)

	agents := map[string]bool{}
	for _, m := range th.Messages {
		if m.Role == "assistant" {
			agents[m.AgentID] = true
		}
	}
	if !agents["ag-b"] || !agents["ag-c"] {
		t.Fatalf("the handoff never reached B and C: %v", agents)
	}
}

// TestInterjectionMentionRoutesTheBatch: an interjection that names a
// member hands the floor to them once the current reply lands — the
// named member's handoff carries the user's words.
func TestInterjectionMentionRoutesTheBatch(t *testing.T) {
	release := make(chan struct{})
	a, th, srv, bodies, routerBodies := routerRelayFixture(t, release,
		[]string{"alpha", "beta"},
		`{"next":"B","reason":"after the user's note"}`,
		`{"next":"","reason":"all done"}`)
	defer srv.Close()
	a.current = "t1"
	a.startTurn(th, "plan the thing")
	// The first-speaker routing holds the registry while it thinks.
	deadline := time.Now().Add(10 * time.Second)
	for {
		running := false
		a.update(func() { running = a.isRunning("t1") })
		if running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the first routing never started")
		}
		time.Sleep(10 * time.Millisecond)
	}
	a.draft = "@A 重点看安全问题"
	a.send()
	a.update(func() {
		if a.draft != "" {
			t.Fatal("the interjection left the draft behind")
		}
	})
	if len(th.Messages) != 2 || th.Messages[1].Role != "user" {
		t.Fatalf("the interjection never joined the transcript: %+v", th.Messages)
	}
	close(release) // the coordinator names B; the interjection waits in pending
	deadline = time.Now().Add(10 * time.Second)
	for {
		placed := false
		a.update(func() { placed = len(th.Messages) >= 3 && th.Messages[2].AgentID == "ag-b" })
		if placed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("B was never placed")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// B landed; the pending "@A" mention fans out to A before the
	// coordinator is ever asked again.
	waitSettled(t, a, th)

	// user + interjection + B + A(the named member) + wrap-up.
	if len(th.Messages) != 5 {
		t.Fatalf("messages = %d, want user + interjection + B + A + wrap-up", len(th.Messages))
	}
	if th.Messages[2].AgentID != "ag-b" {
		t.Fatalf("first speaker = %q, want B", th.Messages[2].AgentID)
	}
	if th.Messages[3].AgentID != "ag-a" {
		t.Fatalf("the named member = %q, want A", th.Messages[3].AgentID)
	}
	if !strings.Contains((*bodies)[1], "重点看安全问题") {
		t.Fatal("A never saw the user's interjection")
	}
	// router calls: first speaker + the end. The @A handoff was direct.
	if len(*routerBodies) != 2 {
		t.Fatalf("router calls = %d, want first-speaker and end only", len(*routerBodies))
	}
}
