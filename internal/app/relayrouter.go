package app

// The router relay (spec/relay-router.md): after every member's reply,
// a cheap coordinator model decides which panel member speaks next —
// or that the relay is done. The client is a plain non-streaming
// chat-completions call against the provider the agent picked (a local
// Ollama being the point); the decision comes back as strict JSON with
// tolerant parsing, and every failure degrades to "relay ends" rather
// than leaving the user stuck in a running thread.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// defaultPanelMaxRounds is the round budget when the agent did not set
// one: a router relay without it is a money fire the coordinator can
// only make worse.
const defaultPanelMaxRounds = 8

// maxSameStreak ends a relay that keeps handing the floor to the same
// member: two agents complimenting each other in a loop is the classic
// coordinator failure (Magentic-One's stall detection, minimal form).
const maxSameStreak = 3

// relayState is one group turn's relay: the sequence queue (untouched
// by router mode), and the router's guard counters.
type relayState struct {
	queue      []string // sequence mode: remaining member ids, FIFO
	rounds     int      // member dispatches made this user turn
	last       string   // the last member's name (router stall guard)
	sameStreak int      // consecutive dispatches of that member
}

// relayRoute is the coordinator's decision.
type relayRoute struct {
	Next   string `json:"next"`
	Reason string `json:"reason"`
}

// relayMember is one roster entry the coordinator sees.
type relayMember struct {
	Name string
	Desc string
}

// panelSnapshot is everything a routing decision needs, snapshotted on
// the main thread before the call runs off it: the live thread may
// change — or go — while the local model thinks (the builtinHarness
// snapshot rule, applied to the router).
type panelSnapshot struct {
	threadID string
	roster   []relayMember
	digest   string
	at       int // the settled message a routing-end note lands on

	baseURL string
	apiKey  string
	model   string
}

// promptHead is a member's duty description for the coordinator: the
// first line of their system prompt, bounded. This is what the
// coordinator's choice is made from, so an agent that wants to be
// picked for the right jobs describes itself there.
func promptHead(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "(no description)"
	}
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	r := []rune(s)
	if len(r) > 160 {
		r = r[:160]
	}
	return string(r)
}

// panelSnapshot captures the relay as it stands at finish time.
func (a *app) panelSnapshot(th *Thread, ag *Agent, at int) panelSnapshot {
	snap := panelSnapshot{threadID: th.ID, at: at}
	for _, m := range a.panelFor(ag) {
		snap.roster = append(snap.roster, relayMember{Name: m.Name, Desc: promptHead(m.SystemPrompt)})
	}
	// The tail through the settled reply: what the coordinator sees is
	// what the transcript shows (the digest is the panelDigest the CLI
	// members already read, bounded harder — the roster rides along).
	snap.digest = a.panelDigest(th, at, 6<<10)
	provID, model := a.providerID, a.model
	if ag.RouterProvider != "" {
		provID = ag.RouterProvider
	}
	if ag.RouterModel != "" {
		model = ag.RouterModel
	}
	snap.model = model
	if p := a.providerByID(provID); p != nil {
		snap.baseURL, snap.apiKey = p.BaseURL, p.APIKey
	}
	return snap
}

// routeRelay continues a router relay, called from finish on the main
// thread. Guards run first — they are the hard stop the coordinator
// cannot talk its way past — then the decision is asked off-thread.
// The run registry entry stays while routing: the thread keeps its
// running state (no concurrent send), and a stop clears the entry,
// which is what drops the decision on revalidation.
func (a *app) routeRelay(th *Thread, at int) {
	st := a.groupQueue[th.ID]
	ag := a.agentFor(th)
	if st == nil || ag == nil || !a.isRunning(th.ID) {
		a.endRelay(th, "")
		return
	}
	maxRounds := ag.PanelMaxRounds
	if maxRounds <= 0 {
		maxRounds = defaultPanelMaxRounds
	}
	switch {
	case st.rounds >= maxRounds:
		a.endRelay(th, fmt.Sprintf("relay reached the round limit (%d)", maxRounds))
	case st.sameStreak >= maxSameStreak:
		a.endRelay(th, fmt.Sprintf("relay stalled on %q for %d rounds", st.last, st.sameStreak))
	default:
		snap := a.panelSnapshot(th, ag, at)
		go a.askRouter(snap)
	}
}

// endRelay settles the group turn: the registry entry goes, the state
// goes, and a reason (when there is one) lands as a note on the last
// reply so the transcript says why the relay stopped.
func (a *app) endRelay(th *Thread, reason string) {
	if reason != "" {
		a.noteLast(th, reason)
	}
	a.runEnd(th.ID)
	delete(a.groupQueue, th.ID)
	if a.wsOpen {
		a.refreshGit() // the agent may have changed files
	}
}

// noteLast appends a note block to the thread's last settled message.
func (a *app) noteLast(th *Thread, text string) {
	for i := len(th.Messages) - 1; i >= 0; i-- {
		if th.Messages[i].Running {
			continue
		}
		th.Messages[i].Blocks = append(th.Messages[i].Blocks, Block{Type: blockNote, Text: text})
		th.Updated = time.Now()
		a.saveThread(th)
		return
	}
}

// askRouter applies the coordinator's decision against live state. It
// runs after the off-thread call, back inside update: the thread may
// have been stopped or deleted meanwhile, and that outcome wins — the
// decision is for a relay that no longer exists.
func (a *app) askRouter(snap panelSnapshot) {
	route, err := routeDecision(context.Background(), snap)
	a.update(func() {
		th := a.byID(snap.threadID)
		if th == nil || !a.isRunning(snap.threadID) {
			return // stopped or deleted mid-route: nothing to continue
		}
		if err != nil {
			a.endRelay(th, "coordinator unavailable: "+err.Error())
			return
		}
		if route.Next == "" {
			a.endRelay(th, "") // the coordinator says the work is done
			return
		}
		next := a.memberByName(a.agentFor(th), route.Next)
		if next == nil {
			a.endRelay(th, fmt.Sprintf("coordinator named %q, who is not on the panel", route.Next))
			return
		}
		a.dispatchRouterMember(th, next, route.Reason)
	})
}

// memberByName resolves one of the relay's panel members by name.
func (a *app) memberByName(ag *Agent, name string) *Agent {
	for _, m := range a.panelFor(ag) {
		if m.Name == name {
			return m
		}
	}
	return nil
}

// dispatchRouterMember appends the chosen member's placeholder and
// dispatches it — the router-mode twin of finish's sequence tail. The
// decision rides along as a note and in the member's handoff prompt,
// so a member knows why the floor came to it.
func (a *app) dispatchRouterMember(th *Thread, next *Agent, reason string) {
	st := a.groupQueue[th.ID]
	if st == nil {
		a.endRelay(th, "")
		return
	}
	if st.last == next.Name {
		st.sameStreak++
	} else {
		st.sameStreak = 1
	}
	st.last = next.Name
	st.rounds++
	note := "→ " + next.Name
	if reason != "" {
		note += ": " + truncRunes(reason, 200)
	}
	now := time.Now()
	msg := Message{ID: uid(), Role: "assistant", Running: true, At: now, AgentID: next.ID}
	msg.Blocks = append(msg.Blocks, Block{Type: blockNote, Text: note})
	th.Messages = append(th.Messages, msg)
	at := len(th.Messages) - 1
	a.saveThread(th)
	prompt := fmt.Sprintf("(Panel coordinator handed the floor to you: %s)", reason)
	if a.resolveAgent(next).backend != "builtin" {
		// The CLI member's session is private: digest, as in sequence.
		prompt = fmt.Sprintf("You are %s in a panel of agents. The conversation so far:\n\n%s\n\n%s",
			next.Name, a.panelDigest(th, at, 8<<10), prompt)
	}
	a.dispatchParticipant(th, prompt, at, next)
}

// routeDecision asks the coordinator which member speaks next.
// Non-streaming, temperature 0, JSON out. A transient failure retries
// once; a reply naming someone off the roster gets one corrective
// round-trip — small models hallucinate names, and one nudge usually
// fixes it. Anything else is an error and the relay degrades.
func routeDecision(ctx context.Context, snap panelSnapshot) (relayRoute, error) {
	if snap.baseURL == "" {
		return relayRoute{}, fmt.Errorf("router provider has no endpoint")
	}
	if len(snap.roster) == 0 {
		return relayRoute{}, fmt.Errorf("the panel is empty")
	}
	msgs := []map[string]string{
		{"role": "system", "content": routerSystemPrompt(snap)},
		{"role": "user", "content": "Which member speaks next? Answer with the JSON object only."},
	}
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		content, err := routerChat(callCtx, snap, msgs)
		cancel()
		if err != nil {
			lastErr = err
			continue
		}
		route, err := parseRouteReply(content)
		if err != nil {
			lastErr = err
			continue
		}
		if routeNameKnown(route.Next, snap.roster) {
			return route, nil
		}
		lastErr = fmt.Errorf("coordinator named %q, who is not on the roster", route.Next)
		msgs = append(msgs,
			map[string]string{"role": "assistant", "content": content},
			map[string]string{"role": "user", "content": `"` + route.Next + `" is not on the roster. Answer again with exactly one roster name, or "" if the relay should end.`})
	}
	return relayRoute{}, lastErr
}

// routeNameKnown reports whether next is a roster name or the empty
// end marker.
func routeNameKnown(next string, roster []relayMember) bool {
	if next == "" {
		return true
	}
	for _, m := range roster {
		if m.Name == next {
			return true
		}
	}
	return false
}

// routerSystemPrompt is the coordinator's brief: the roster with each
// member's duty and the bounded transcript tail.
func routerSystemPrompt(snap panelSnapshot) string {
	var b strings.Builder
	b.WriteString("You are the coordinator of a panel of AI agents. Roster:\n")
	for _, m := range snap.roster {
		fmt.Fprintf(&b, "- %s: %s\n", m.Name, m.Desc)
	}
	b.WriteString("\nConversation so far:\n" + snap.digest + `
Decide which member should speak next. Respond ONLY with a JSON object:
{"next": "<member name>", "reason": "<one short sentence>"}
Use {"next": "", "reason": "..."} when the user's request has been fully
addressed and another reply would add nothing. You may pick the same
member again if they should continue. Never pick a member whose duties
do not match what the conversation needs next.`)
	return b.String()
}

// routerChat is one non-streaming chat completion against the
// OpenAI-compatible endpoint (Ollama's is one). Bounded retry of the
// transient failures only, as the built-in loop does.
func routerChat(ctx context.Context, snap panelSnapshot, msgs []map[string]string) (string, error) {
	body, err := json.Marshal(map[string]any{
		"model":           snap.model,
		"messages":        msgs,
		"temperature":     0,
		"response_format": map[string]string{"type": "json_object"},
	})
	if err != nil {
		return "", err
	}
	url := strings.TrimRight(snap.baseURL, "/") + "/chat/completions"
	client := &http.Client{Timeout: 30 * time.Second}
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			select {
			case <-time.After(500 * time.Millisecond):
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return "", err
		}
		req.Header.Set("Content-Type", "application/json")
		if snap.apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+snap.apiKey)
		}
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("router provider returned %s: %s", resp.Status, strings.TrimSpace(string(data)))
			if resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode < 500 {
				return "", lastErr // a 4xx is the request's own fault
			}
			continue
		}
		if readErr != nil {
			lastErr = readErr
			continue
		}
		var out struct {
			Choices []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
		}
		if err := json.Unmarshal(data, &out); err != nil {
			return "", fmt.Errorf("router reply was not chat completions: %w", err)
		}
		if len(out.Choices) == 0 {
			return "", fmt.Errorf("router reply had no choices")
		}
		return out.Choices[0].Message.Content, nil
	}
	return "", lastErr
}

// parseRouteReply reads the coordinator's answer: strict JSON first,
// then the first {...} span — small models like to talk around the
// object even when asked not to.
func parseRouteReply(s string) (relayRoute, error) {
	var r relayRoute
	s = strings.TrimSpace(s)
	if err := json.Unmarshal([]byte(s), &r); err == nil {
		return r, nil
	}
	if i := strings.IndexByte(s, '{'); i >= 0 {
		if j := strings.LastIndexByte(s, '}'); j > i {
			if err := json.Unmarshal([]byte(s[i:j+1]), &r); err == nil {
				return r, nil
			}
		}
	}
	return relayRoute{}, fmt.Errorf("coordinator reply was not JSON: %.80s", s)
}

// truncRunes cuts s to n runes so a rambling reason cannot bloat the
// transcript note.
func truncRunes(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}
