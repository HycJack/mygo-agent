package app

// The router relay (spec/relay-router.md): after every member's reply,
// a cheap coordinator model decides which panel member speaks next —
// or that the relay is done. Two wires: a plain non-streaming
// chat-completions call with a JSON-reply prompt, and the decision API
// (Ollama's /v1/systemone, the tev1 class), whose choice/noul answers
// come back constrained and scored. Every failure degrades to "relay
// ends" rather than leaving the user stuck in a running thread.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"mygo-agent/internal/harness"
)

// defaultPanelMaxRounds is the round budget when the agent did not set
// one: a router relay without it is a money fire the coordinator can
// only make worse.
const defaultPanelMaxRounds = 8

// maxSameStreak ends a relay that keeps handing the floor to the same
// member without new substance: two agents complimenting each other in
// a loop is the classic coordinator failure (Magentic-One's stall
// detection, minimal form). Five, not three: a member deep in real
// work — writing code, running tests — legitimately speaks many times
// in a row, and a tight cap kills exactly the execution a task needs.
const maxSameStreak = 5

// routerChatDigestLimit bounds the transcript tail a chat coordinator
// sees; routerDecisionDigestLimit the much shorter tail a decision
// coordinator sees — the tev1 class works in roughly a 2k-token window,
// so roster and conversation have to be terse to fit.
const (
	routerChatDigestLimit     = 6 << 10
	routerDecisionDigestLimit = 2500
)

// maxDecisionOptions is the decision API's choice-question ceiling: a
// panel larger than that cannot be routed by the decision wire.
const maxDecisionOptions = 26

// routerCallTimeout bounds one coordinator call. Two minutes, not a
// token-windows worry — the inputs are bounded to kilobytes — but a
// reasoning model reading a long digest can legitimately think past
// half a minute, and a premature deadline ends the relay as
// "coordinator unavailable".
const routerCallTimeout = 2 * time.Minute

// doneProbability is where a decision router's noul answer counts as
// "the work is done": the answer IS the probability, so the honest
// midpoint is the threshold.
const doneProbability = 0.5

// relayState is one group turn's relay: the sequence queue (untouched
// by router mode), the router's guard counters, and the user's
// mid-relay messages waiting for the next handoff.
type relayState struct {
	queue      []string  // sequence mode: remaining member ids, FIFO
	rounds     int       // member dispatches made this user turn
	last       string    // the last member's name (router stall guard)
	sameStreak int       // consecutive dispatches of that member
	tokens     int64     // tokens the relay's members have spent so far
	start      time.Time // when the relay began (timeout guard)
	pending    []string  // user interjections awaiting the next handoff
	spoken     []string  // member names in dispatch order (the rotation state the coordinator reads)
	retries    int       // member turns replayed after a wire failure
	outline    []string  // one bounded gist per member reply — the relay's rolling memory

	// A parallel handoff (spec/relay-router.md): several members named
	// at once run concurrently, each in its own message slot, and the
	// relay routes only when the last of them lands.
	batchLeft int
	batchByAt map[int]*batchMember
	batchSeq  []*batchMember // non-builtin members run one at a time after the batch

	// honored are the members a MEMBER's reply has already handed the
	// floor to this turn. A reply quotes the ask — "@A" echoes through
	// the transcript — and honoring the echo would re-dispatch the same
	// member on every landing: a round-bounded loop where the
	// coordinator never thinks. Member mentions fire once per turn; the
	// user's own @ bypasses the set (saying it twice means twice).
	honored map[string]bool
}

// batchMember is one member of a parallel handoff.
type batchMember struct {
	ag     *Agent
	at     int
	prompt string      // the CLI stragglers' handoff, built at fan-out time
	fork   *forkMemory // builtin members only: an isolated transcript view
}

// relayRoute is the coordinator's decision, with what it cost.
type relayRoute struct {
	Next   string `json:"next"`
	Reason string `json:"reason"`
	Tokens int64  `json:"-"`
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

	// request is the user's ask this turn serves, and last the member
	// who spoke before this decision: the "has the request been
	// addressed" question is unanswerable without knowing the request,
	// and a tail-bounded digest can lose it. spoken/rounds are the
	// rotation state — without them a coordinator cannot even take fair
	// turns, let alone route by need.
	request string
	last    string
	spoken  []string
	rounds  int
	outline []string

	// The chat side: the chat coordinator, or the hybrid advisor.
	baseURL string
	apiKey  string
	model   string
	wire    string // "" (chat completions), "decision" or "hybrid" (/v1/systemone)

	// The judge side: the decision model of the decision and hybrid
	// wires (Ollama's /v1/systemone).
	judgeBaseURL string
	judgeAPIKey  string
	judgeModel   string
}

// promptHead is a member's duty description from their system prompt:
// the first line, bounded.
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

// memberDesc is what the routing roster reads for one member: their
// panel_blurb when they wrote one — routing and persona stay decoupled,
// so retuning the coordinator's criteria never touches the member's
// behavior — else the head of the system prompt.
func memberDesc(m *Agent) string {
	if s := strings.TrimSpace(m.PanelBlurb); s != "" {
		return truncRunes(s, 200)
	}
	return promptHead(m.SystemPrompt)
}

// panelSnapshot captures the relay as it stands at finish time.
func (a *app) panelSnapshot(th *Thread, ag *Agent, at int) panelSnapshot {
	snap := panelSnapshot{threadID: th.ID, at: at}
	for _, m := range a.panelFor(ag) {
		snap.roster = append(snap.roster, relayMember{Name: m.Name, Desc: memberDesc(m)})
	}
	// The tail through the settled reply: what the coordinator sees is
	// what the transcript shows (the digest is the panelDigest the CLI
	// members already read, bounded by the wire's budget — the roster
	// rides along).
	snap.wire = ag.RouterWire
	limit := routerChatDigestLimit
	if snap.wire == "decision" {
		limit = routerDecisionDigestLimit
	}
	snap.digest = a.panelDigest(th, at, limit)
	// The turn's own ask, and who spoke last: both go into the decision
	// state (spec/relay-router.md).
	for i := at; i >= 0 && i < len(th.Messages); i-- {
		if th.Messages[i].Role == "user" {
			snap.request = truncRunes(th.Messages[i].Text, 1000)
			break
		}
	}
	if st := a.groupQueue[th.ID]; st != nil {
		snap.last = st.last
		snap.spoken = append([]string(nil), st.spoken...)
		snap.rounds = st.rounds
		snap.outline = append([]string(nil), st.outline...)
	}
	// Chat side: the chat coordinator, or the hybrid advisor — the
	// router fields over the app's.
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
	// Judge side: the decision model — explicit judge fields win, then
	// the router fields themselves (a pure-decision config points the
	// router fields at the decision model), then the app's.
	if ag.RouterJudgeProvider != "" {
		provID = ag.RouterJudgeProvider
	}
	if ag.RouterJudgeModel != "" {
		model = ag.RouterJudgeModel
	}
	snap.judgeModel = model
	if p := a.providerByID(provID); p != nil {
		snap.judgeBaseURL, snap.judgeAPIKey = p.BaseURL, p.APIKey
	}
	return snap
}

// routeRelay continues a router relay, called from finish on the main
// thread. The guards run first — they are the hard stops the
// coordinator cannot talk its way past, each one configurable on the
// agent (panel_max_rounds / panel_stall_rounds / panel_max_tokens /
// panel_timeout, zero = its default or off) — then the decision is
// asked off-thread. The run registry entry stays while routing: the
// thread keeps its running state (no concurrent send; the user's
// mid-relay messages interject instead), and a stop clears the entry,
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
	stall := ag.PanelStallRounds
	if stall <= 0 {
		stall = maxSameStreak
	}
	switch {
	case st.rounds >= maxRounds:
		a.wrapUpRelay(th, fmt.Sprintf("relay reached the round limit (%d)", maxRounds))
	case ag.PanelMaxTokens > 0 && st.tokens >= int64(ag.PanelMaxTokens):
		a.wrapUpRelay(th, fmt.Sprintf("relay reached the token budget (%d of %d)",
			st.tokens, ag.PanelMaxTokens))
	case ag.PanelTimeout > 0 && st.start.Add(time.Duration(ag.PanelTimeout)*time.Second).Before(time.Now()):
		a.wrapUpRelay(th, fmt.Sprintf("relay timed out after %ds", ag.PanelTimeout))
	case st.sameStreak >= stall:
		a.wrapUpRelay(th, fmt.Sprintf("relay stalled on %q for %d rounds", st.last, st.sameStreak))
	default:
		snap := a.panelSnapshot(th, ag, at)
		// The user's fresh words outrank everything: an interjection
		// that names members hands the floor to them once the current
		// reply lands (spec/relay-router.md).
		if len(st.pending) > 0 {
			if names := a.mentionedMembers(ag, strings.Join(st.pending, "\n")); len(names) > 0 {
				a.startFanout(th, ag, names,
					fmt.Sprintf("the user named you while the panel was talking (@%s)", strings.Join(names, " @")), "")
				return
			}
		}
		// A member who knows exactly who is next says so: "@Name" in
		// their reply passes the floor directly — no coordinator call.
		// Two or more names run as a parallel batch. The guards above
		// are the central authority that can still stop it.
		names := a.mentionedMembers(ag, th.Messages[at].Text)
		filtered := names[:0]
		for _, n := range names {
			if !st.honored[n] {
				filtered = append(filtered, n)
			}
		}
		names = filtered
		if len(names) > 0 && !(len(names) == 1 && names[0] == st.last) {
			who := st.last
			if len(names) == 1 {
				a.traceRoute(th, 0, relayRoute{Next: names[0],
					Reason: "handed off directly by " + who + " (@" + names[0] + ")"}, nil)
				if next := a.memberByName(ag, names[0]); next != nil {
					if st.honored == nil {
						st.honored = map[string]bool{}
					}
					st.honored[names[0]] = true
					a.dispatchRouterMember(th, next,
						fmt.Sprintf("the floor came straight from %s, who named you (@%s)", who, names[0]), "")
					return
				}
			} else {
				a.startFanout(th, ag, names,
					fmt.Sprintf("%s named you for this (@%s)", who, strings.Join(names, " @")), "")
				return
			}
		}
		go a.askRouter(snap)
	}
}

// startFanout dispatches a parallel handoff: every named member gets
// its own message slot and runs at once (builtin members on an isolated
// transcript view — two loops must not interleave one shared history),
// and the relay routes only when the last of them lands. A non-builtin
// member shares its CLI session with the thread, so those run one at a
// time after the concurrent batch drains. The user's pending
// interjections ride every member's handoff — they are why the batch
// exists (spec/relay-router.md).
func (a *app) startFanout(th *Thread, ag *Agent, names []string, reason, base string) {
	st := a.groupQueue[th.ID]
	if st == nil {
		st = &relayState{start: time.Now()}
		a.groupQueue[th.ID] = st
		a.runStart(th.ID, nil)
	}
	st.batchByAt = map[int]*batchMember{}
	pending := strings.Join(st.pending, "\n---\n")
	st.pending = nil
	first := true
	for _, name := range names {
		member := a.memberByName(ag, name)
		if member == nil {
			continue
		}
		note := "→ " + name
		if reason != "" {
			note += ": " + truncRunes(reason, 200)
		}
		now := time.Now()
		msg := Message{ID: uid(), Role: "assistant", Running: true, At: now, AgentID: member.ID}
		msg.Blocks = append(msg.Blocks, Block{Type: blockNote, Text: note})
		th.Messages = append(th.Messages, msg)
		at := len(th.Messages) - 1
		prompt := base
		if first && pending != "" {
			if prompt != "" {
				prompt += "\n\n"
			}
			prompt += "The user added while the panel was talking:\n" + pending
		}
		first = false
		if reason != "" {
			if prompt != "" {
				prompt += "\n\n"
			}
			prompt += fmt.Sprintf("(Panel coordinator handed the floor to you: %s)", reason)
		}
		if prompt == "" {
			prompt = panelNudge
		}
		bm := &batchMember{ag: member, at: at}
		st.batchByAt[at] = bm
		if a.resolveAgent(member).backend == "builtin" {
			bm.fork = &forkMemory{a: a,
				key:  harness.MemoryKey(th.ProjectID, th.ID),
				priv: slices.Clone(th.ChatLog), seed: len(th.ChatLog)}
			a.dispatchBatchMember(th, prompt, at, panelMemberAgent(member), bm.fork)
		} else {
			// One CLI session per thread: the batch's CLI members queue
			// and run one at a time as their peers land.
			bm.prompt = prompt
			st.batchSeq = append(st.batchSeq, bm)
		}
		st.rounds++
		st.spoken = append(st.spoken, name)
		if st.honored == nil {
			st.honored = map[string]bool{}
		}
		st.honored[name] = true
	}
	st.last = "" // a batch has no single last speaker
	st.sameStreak = 1
	st.batchLeft = len(st.batchByAt) + len(st.batchSeq)
	a.saveThread(th)
}

// batchLanded settles one member of a parallel handoff: merge its
// transcript view, run down the counter, queue the CLI stragglers — and
// when the batch has fully landed, hand back whether it is time to
// route. The retry of a failed batch member stays inside the batch.
func (a *app) batchLanded(th *Thread, at int, errText string) (routing bool, retry *batchMember, next *batchMember) {
	st := a.groupQueue[th.ID]
	if st == nil {
		return true, nil, nil
	}
	bm := st.batchByAt[at]
	if bm == nil {
		return true, nil, nil
	}
	delete(st.batchByAt, at)
	if bm.fork != nil {
		bm.fork.merge()
	}
	st.batchLeft--
	if errText != "" && st.retries < 1 {
		// One replay, as a solo member turn gets: the batch waits for it.
		st.retries++
		return false, bm, nil
	}
	if errText == "" {
		st.retries = 0
	}
	if st.batchLeft > 0 {
		if len(st.batchByAt) == 0 && len(st.batchSeq) > 0 {
			// The concurrent members all landed: the CLI straggler's turn.
			nx := st.batchSeq[0]
			st.batchSeq = st.batchSeq[1:]
			return false, nil, nx
		}
		return false, nil, nil
	}
	return true, nil, nil
}

// mentionedMembers resolves the panel members a text names with @.
func (a *app) mentionedMembers(ag *Agent, text string) []string {
	var roster []relayMember
	for _, m := range a.panelFor(ag) {
		roster = append(roster, relayMember{Name: m.Name})
	}
	return extractMentions(text, roster)
}

// extractMentions reads the panel members a text names with @, in the
// order they appear, deduped — the floor handoff a member writes into a
// reply ("@B — your call") and the composer's @-naming share one syntax
// (spec/relay-router.md).
func extractMentions(text string, roster []relayMember) []string {
	type hit struct {
		at      int
		name    string
		nameLen int
	}
	var hits []hit
	for _, m := range roster {
		needle := "@" + m.Name
		start := 0
		for start < len(text) {
			idx := strings.Index(text[start:], needle)
			if idx < 0 {
				break
			}
			at := start + idx
			hits = append(hits, hit{at: at, name: m.Name, nameLen: len(needle)})
			start = at + 1
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].at != hits[j].at {
			return hits[i].at < hits[j].at
		}
		return hits[i].nameLen > hits[j].nameLen
	})
	var out []string
	seen := map[string]bool{}
	for _, h := range hits {
		if !seen[h.name] {
			seen[h.name] = true
			out = append(out, h.name)
		}
	}
	return out
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

// wrapUpRelay ends the relay and gives the user a conclusion: the
// synthesizer — a designated member (panel_summarizer), else the
// thread's own agent — writes the final answer over the whole
// discussion. Without it a relay ends on the last specialist's reply,
// and the user is left to assemble the conclusion themselves. An
// aborted relay wraps up too: partial work still deserves a summary of
// what was settled and what was not. When the wrap-up turn itself
// finishes, finish finds no relay state and the turn simply ends.
func (a *app) wrapUpRelay(th *Thread, reason string) {
	rounds := 0
	if st := a.groupQueue[th.ID]; st != nil {
		rounds = st.rounds
	}
	a.endRelay(th, reason)
	if rounds == 0 {
		return // nobody spoke; there is nothing to synthesize
	}
	synth := a.agentFor(th)
	if ag := a.agentFor(th); ag != nil && ag.PanelSummarizer != "" {
		if m := a.memberByName(ag, ag.PanelSummarizer); m != nil {
			synth = m
		}
	}
	if synth == nil {
		return
	}
	now := time.Now()
	msg := Message{ID: uid(), Role: "assistant", Running: true, At: now, AgentID: synth.ID}
	msg.Blocks = append(msg.Blocks, Block{Type: blockNote, Text: "→ " + synth.Name + ": final wrap-up"})
	th.Messages = append(th.Messages, msg)
	at := len(th.Messages) - 1
	a.saveThread(th)
	endNote := reason
	if endNote == "" {
		endNote = "the coordinator judged the discussion complete"
	}
	prompt := fmt.Sprintf(`The panel of agents has finished its discussion. Write the final conclusion for the user, in the user's language: the decision or recommendation, the key rationale, and any open questions or next steps. Be concise — do not restate every reply. (The coordinator ended the relay: %s.)`, endNote)
	if a.resolveAgent(synth).backend != "builtin" {
		// The CLI synthesizer's session is private: digest, as always.
		prompt = fmt.Sprintf("You are %s. The panel conversation so far:\n\n%s\n\n%s",
			synth.Name, a.panelDigest(th, at, 8<<10), prompt)
	}
	a.dispatchParticipant(th, prompt, at, synth)
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

// routeFirstSpeaker starts a routed relay with the coordinator picking
// the first speaker: who answers a fresh ask is the same routing
// question as who speaks next, and a fixed panel[0] made every
// conversation open with the same agent (spec/relay-router.md). The
// registry entry is held while routing — the thread is running, a stop
// drops the decision, and an interjection rides the first handoff.
// base is the turn's own user text, needed by members whose backend
// cannot read the transcript.
func (a *app) routeFirstSpeaker(th *Thread, base string) {
	a.groupQueue[th.ID] = &relayState{rounds: 0, start: time.Now()}
	a.saveThread(th)
	a.runStart(th.ID, nil)
	snap := a.panelSnapshot(th, a.agentFor(th), len(th.Messages)-1)
	go a.askFirstSpeaker(snap, base)
}

// askFirstSpeaker applies the first-speaker decision. With no
// coordinator opinion — call failed, relay done, unknown name — the
// configured order takes the first turn: a relay that dies before
// anyone spoke is worse than a default pick.
func (a *app) askFirstSpeaker(snap panelSnapshot, base string) {
	start := time.Now()
	route, err := routeDecision(context.Background(), snap)
	ms := time.Since(start).Milliseconds()
	a.update(func() {
		th := a.byID(snap.threadID)
		if th == nil || !a.isRunning(snap.threadID) {
			return // stopped or deleted mid-route: nothing to start
		}
		a.traceRoute(th, ms, route, err)
		if a.groupQueue[snap.threadID] == nil {
			return
		}
		members := a.panelFor(a.agentFor(th))
		if len(members) == 0 {
			a.endRelay(th, "the panel is empty")
			return
		}
		next := a.memberByName(a.agentFor(th), route.Next)
		reason := route.Reason
		if err != nil || next == nil {
			next, reason = members[0], ""
		}
		a.dispatchRouterMember(th, next, reason, base)
	})
}

// traceRoute records one coordinator decision in the thread's trace:
// who it picked (or why the call failed), how long it thought, what it
// cost. Derived data — a failed append never fails the relay.
func (a *app) traceRoute(th *Thread, ms int64, route relayRoute, err error) {
	te := traceEvent{At: time.Now(), Kind: "route", Ms: ms, Tokens: route.Tokens, Summary: "next=" + route.Next}
	if err != nil {
		te.Failed = true
		te.Summary = "error: " + err.Error()
	} else if route.Reason != "" {
		te.Summary += "; " + route.Reason
	}
	a.appendTrace(th, te)
}

// askRouter applies the coordinator's decision against live state. It
// runs after the off-thread call, back inside update: the thread may
// have been stopped or deleted meanwhile, and that outcome wins — the
// decision is for a relay that no longer exists.
func (a *app) askRouter(snap panelSnapshot) {
	start := time.Now()
	route, err := routeDecision(context.Background(), snap)
	ms := time.Since(start).Milliseconds()
	a.update(func() {
		th := a.byID(snap.threadID)
		if th == nil || !a.isRunning(snap.threadID) {
			return // stopped or deleted mid-route: nothing to continue
		}
		a.traceRoute(th, ms, route, err)
		if err != nil {
			a.wrapUpRelay(th, "coordinator unavailable: "+err.Error())
			return
		}
		if route.Next == "" {
			// The coordinator says the work is done — the reason (chat
			// wire: its own words; decision wire: the scored p(done)) is
			// why the relay stopped, so it lands as a note — and the
			// synthesizer writes the conclusion.
			a.wrapUpRelay(th, route.Reason)
			return
		}
		next := a.memberByName(a.agentFor(th), route.Next)
		if next == nil {
			a.wrapUpRelay(th, fmt.Sprintf("coordinator named %q, who is not on the panel", route.Next))
			return
		}
		a.dispatchRouterMember(th, next, route.Reason, "")
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
// so a member knows why the floor came to it. base is the turn's own
// user text for the FIRST dispatch — no turn has run yet, so the
// transcript does not hold the request; later dispatches pass "" and
// rely on the shared transcript.
func (a *app) dispatchRouterMember(th *Thread, next *Agent, reason, base string) {
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
	st.spoken = append(st.spoken, next.Name)
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
	prompt := base
	if reason != "" {
		if prompt != "" {
			prompt += "\n\n"
		}
		prompt += fmt.Sprintf("(Panel coordinator handed the floor to you: %s)", reason)
	}
	if prompt == "" {
		prompt = panelNudge
	}
	// The user may have spoken while the panel was talking: the floor
	// comes with what they added (the messages already sit in the
	// transcript; the CLI digest picks them up from there). An
	// interjection that NAMES a member is a routing instruction, not
	// context — it stays pending for the relay's next decision instead
	// of being consumed here.
	if len(st.pending) > 0 {
		var pass, keep []string
		for _, p := range st.pending {
			if len(a.mentionedMembers(a.agentFor(th), p)) > 0 {
				keep = append(keep, p)
			} else {
				pass = append(pass, p)
			}
		}
		if len(pass) > 0 {
			prompt += "\n\nThe user added while the panel was talking:\n" +
				strings.Join(pass, "\n---\n")
		}
		st.pending = keep
	}
	if a.resolveAgent(next).backend != "builtin" {
		// The CLI member's session is private: digest, as in sequence.
		prompt = fmt.Sprintf("You are %s in a panel of agents. The conversation so far:\n\n%s\n\n%s",
			next.Name, a.panelDigest(th, at, 8<<10), prompt)
	}
	if a.protocolViaPrompt(next) {
		prompt += panelProtocol
	}
	a.dispatchParticipant(th, prompt, at, panelMemberAgent(next))
}

// panelProtocol is appended to every panel member's system prompt at
// dispatch: the members' own prompts are written as if talking to a
// user, and left alone they end replies by handing choices back to the
// user — which stalls the panel the router exists to drive. The panel
// decides; the user is asked only when a real business trade-off needs
// them.
const panelProtocol = "\n\n[Panel protocol] You are a member of a multi-agent panel, with a routing coordinator deciding who speaks next. When you hit a choice: give a clear recommendation (option, rationale, cost) and let the panel carry it forward — do not end your reply by handing the question back to the user. Reserve the user only for a genuine business trade-off they must own, and state your recommendation even then. You may hand the floor directly: end your reply with a line naming the member who should take over, like \"@B — this needs your call\" — use it when you know exactly who is next; otherwise the coordinator routes."

// panelMemberAgent returns the member's profile with the panel
// protocol appended. A copy: the profile itself is untouched, so the
// routing roster (promptHead) and the settings dialog keep reading the
// clean prompt.
func panelMemberAgent(ag *Agent) *Agent {
	c := *ag
	c.SystemPrompt = strings.TrimSpace(ag.SystemPrompt) + panelProtocol
	return &c
}

// protocolViaPrompt reports whether this member can only receive the
// panel protocol through the handoff text: the codex adapter drops
// turn.SystemPrompt entirely and pi never reads it, so for them the
// prompt is the only channel (claude carries it via
// --append-system-prompt, builtin via its per-turn system head).
func (a *app) protocolViaPrompt(next *Agent) bool {
	switch a.resolveAgent(next).backend {
	case "codex", "pi":
		return true
	}
	return false
}

// routeDecision asks the coordinator which member speaks next. Three
// wires share the question (spec/relay-router.md): chat completions
// with a JSON-reply prompt; the decision API (/v1/systemone, the tev1
// class), whose answers come back constrained and scored; and hybrid —
// a big chat model writes a situation brief, the decision model makes
// the final call on it. All degrade to an error the relay ends on.
func routeDecision(ctx context.Context, snap panelSnapshot) (relayRoute, error) {
	if snap.baseURL == "" {
		return relayRoute{}, fmt.Errorf("router provider has no endpoint")
	}
	if len(snap.roster) == 0 {
		return relayRoute{}, fmt.Errorf("the panel is empty")
	}
	switch snap.wire {
	case "decision":
		return routeDecisionSystemone(ctx, snap, "")
	case "hybrid":
		return routeDecisionHybrid(ctx, snap)
	default:
		return routeDecisionChat(ctx, snap)
	}
}

// routeDecisionHybrid is the two-level coordinator: the advisor (the
// big chat model) comprehends — stage, what is established, what is
// needed next — and the judge (the decision model) makes the final,
// scored call on the brief. The advisor is a booster, not a dependency:
// if it fails, the judge still decides on the digest state rather than
// the relay dying for its sake.
func routeDecisionHybrid(ctx context.Context, snap panelSnapshot) (relayRoute, error) {
	brief, err := routerBrief(ctx, snap)
	if err != nil {
		return routeDecisionSystemone(ctx, snap, "")
	}
	route, err := routeDecisionSystemone(ctx, snap, brief)
	if err != nil {
		return relayRoute{}, err
	}
	if route.Next != "" {
		// The handoff carries the advisor's comprehension plus the
		// judge's scores: the member gets both, the note shows the head.
		route.Reason = truncRunes(brief, 600) + " | " + route.Reason
	}
	return route, nil
}

// routeDecisionChat is the chat wire: temperature 0, JSON out. A
// transient failure retries once; a reply naming someone off the roster
// gets one corrective round-trip — small models hallucinate names, and
// one nudge usually fixes it.
func routeDecisionChat(ctx context.Context, snap panelSnapshot) (relayRoute, error) {
	msgs := []map[string]string{
		{"role": "system", "content": routerSystemPrompt(snap)},
		{"role": "user", "content": "Which member speaks next? Answer with the JSON object only."},
	}
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		callCtx, cancel := context.WithTimeout(ctx, routerCallTimeout)
		content, tokens, err := routerChat(callCtx, snap, msgs)
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
		route.Tokens = tokens
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

// routeDecisionSystemone is the judge: the Jev systemone API (Ollama's
// /v1/systemone, the tev1 class). The panel is a choice question —
// options ARE the member names, so nothing off-roster can come back —
// and ending the relay is its own noul question, scored in the same
// call. A panel of one needs no choice question: the sole member
// speaks unless the relay is done. The API allows 2–26 choice options,
// so a larger panel is a degrade. A non-empty brief is the hybrid
// advisor's comprehension: it replaces the raw digest as what the
// judge reads.
func routeDecisionSystemone(ctx context.Context, snap panelSnapshot, brief string) (relayRoute, error) {
	if len(snap.roster) > maxDecisionOptions {
		return relayRoute{}, fmt.Errorf("panel of %d exceeds the decision router's %d options",
			len(snap.roster), maxDecisionOptions)
	}
	if snap.judgeBaseURL == "" {
		return relayRoute{}, fmt.Errorf("router judge has no endpoint")
	}
	questions := map[string]any{
		"done": map[string]any{
			"type": "noul",
			"instructions": "Has the user's request been fully addressed, so another member reply would add nothing? " +
				"A member ending with a question or a set of open options does NOT mean done: if any panel member can decide or answer it, the relay continues.",
		},
	}
	if len(snap.roster) >= 2 {
		criteria := map[string]string{}
		for _, m := range snap.roster {
			criteria[m.Name] = m.Desc
		}
		questions["next"] = map[string]any{
			"type":         "choice",
			"instructions": "Which member should speak next, given what the conversation still needs?",
			"criteria":     criteria,
		}
	}
	body, err := json.Marshal(map[string]any{
		"model":     snap.judgeModel,
		"state":     decisionState(snap, brief),
		"questions": questions,
	})
	if err != nil {
		return relayRoute{}, err
	}
	data, err := routerPost(ctx, snap.judgeBaseURL, snap.judgeAPIKey, "/systemone", body)
	if err != nil {
		return relayRoute{}, err
	}
	var out struct {
		Answers map[string]struct {
			Choice        string             `json:"choice"`
			Probabilities map[string]float64 `json:"probabilities"`
			Confidence    float64            `json:"confidence"`
			Noul          float64            `json:"noul"`
		} `json:"answers"`
		Usage struct {
			InputTokens  int64 `json:"input_tokens"`
			OutputTokens int64 `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return relayRoute{}, fmt.Errorf("decision reply was not systemone: %w", err)
	}
	tokens := out.Usage.InputTokens + out.Usage.OutputTokens
	done := out.Answers["done"]
	if done.Noul >= doneProbability {
		return relayRoute{Reason: fmt.Sprintf("coordinator says done (p=%.2f)", done.Noul), Tokens: tokens}, nil
	}
	if len(snap.roster) == 1 {
		// No choice was asked: the sole member continues.
		return relayRoute{Next: snap.roster[0].Name,
			Reason: fmt.Sprintf("p(done)=%.2f", done.Noul), Tokens: tokens}, nil
	}
	pick := out.Answers["next"]
	if pick.Choice == "" {
		return relayRoute{}, fmt.Errorf("decision reply had no choice")
	}
	if !routeNameKnown(pick.Choice, snap.roster) {
		return relayRoute{}, fmt.Errorf("decision named %q, who is not on the roster", pick.Choice)
	}
	p := pick.Probabilities[pick.Choice]
	return relayRoute{Next: pick.Choice,
		Reason: fmt.Sprintf("p=%.2f, confidence %.2f", p, pick.Confidence), Tokens: tokens}, nil
}

// decisionState is the judge's view. The members ride as structured
// criteria; the situation is either the hybrid advisor's brief (who has
// already read the full digest) or the raw bounded digest, and the
// turn's own request and the last speaker ride along either way — the
// "has the request been addressed" question is unanswerable without
// the request.
func decisionState(snap panelSnapshot, brief string) map[string]any {
	members := make([]map[string]any, len(snap.roster))
	for i, m := range snap.roster {
		desc := any(m.Desc)
		if m.Desc == "" || m.Desc == "(no description)" {
			desc = nil
		}
		members[i] = map[string]any{"name": m.Name, "duties": desc}
	}
	state := map[string]any{"members": members}
	if snap.request != "" {
		state["request"] = snap.request
	}
	if snap.last != "" {
		state["last_speaker"] = snap.last
	}
	if len(snap.spoken) > 0 {
		state["spoken_history"] = snap.spoken
	}
	if len(snap.outline) > 0 {
		state["outline"] = snap.outline
	}
	if brief != "" {
		state["brief"] = brief
	} else {
		state["conversation"] = snap.digest
	}
	return state
}

// routerBrief asks the hybrid advisor — the big chat model — for the
// situation brief the judge decides on: what stage the work is at,
// what has been established, what the panel should do next (or why it
// is finished). The advisor sees the full chat-sized digest the judge's
// 2k-token window cannot.
func routerBrief(ctx context.Context, snap panelSnapshot) (string, error) {
	msgs := []map[string]string{
		{"role": "system", "content": advisorSystemPrompt(snap)},
		{"role": "user", "content": "Write the brief now. Answer with the JSON object only."},
	}
	content, _, err := routerChat(ctx, snap, msgs)
	if err != nil {
		return "", err
	}
	return parseBrief(content)
}

// advisorSystemPrompt briefs the advisor: roster, the user's ask, the
// transcript tail.
func advisorSystemPrompt(snap panelSnapshot) string {
	var b strings.Builder
	b.WriteString("You advise the coordinator of a panel of AI agents. Roster:\n")
	for _, m := range snap.roster {
		fmt.Fprintf(&b, "- %s: %s\n", m.Name, m.Desc)
	}
	if snap.request != "" {
		b.WriteString("\nThe user's request:\n" + snap.request)
	}
	if len(snap.outline) > 0 {
		b.WriteString("\nWhat each reply established:")
		for _, line := range snap.outline {
			b.WriteString("\n- " + line)
		}
	}
	b.WriteString("\nConversation so far:\n" + snap.digest + `
Write a brief for the routing decision, 2-3 sentences, in the user's
language: what stage the work is at, what the conversation has
established, and what the panel should do next — or, if the user's
request has been fully addressed and another reply would add nothing,
say so plainly. If a member ended with a question or open options, the
brief should name them and say which member can decide or answer.
Respond ONLY with a JSON object:
{"brief": "<your brief>"}`)
	return b.String()
}

// parseBrief reads the advisor's answer: strict JSON first, then the
// first {...} span — same tolerance the chat wire gets.
func parseBrief(s string) (string, error) {
	var r struct {
		Brief string `json:"brief"`
	}
	s = strings.TrimSpace(s)
	if err := json.Unmarshal([]byte(s), &r); err == nil && r.Brief != "" {
		return r.Brief, nil
	}
	if i := strings.IndexByte(s, '{'); i >= 0 {
		if j := strings.LastIndexByte(s, '}'); j > i {
			if err := json.Unmarshal([]byte(s[i:j+1]), &r); err == nil && r.Brief != "" {
				return r.Brief, nil
			}
		}
	}
	return "", fmt.Errorf("advisor reply had no brief: %.80s", s)
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

// routerSystemPrompt is the coordinator's brief: roster, the user's
// ask, the rotation state, then the transcript tail — the state rides
// ahead of the digest, which is the part a long tail can crowd out.
func routerSystemPrompt(snap panelSnapshot) string {
	var b strings.Builder
	b.WriteString("You are the coordinator of a panel of AI agents. Roster:\n")
	for _, m := range snap.roster {
		fmt.Fprintf(&b, "- %s: %s\n", m.Name, m.Desc)
	}
	if snap.request != "" {
		b.WriteString("\nThe user's request:\n" + snap.request)
	}
	if len(snap.spoken) > 0 {
		fmt.Fprintf(&b, "\nRelay state: %d replies so far. Speakers in order: %s.",
			snap.rounds, strings.Join(snap.spoken, " → "))
		if pending := notYetSpoken(snap); len(pending) > 0 {
			b.WriteString(" Not yet spoken: " + strings.Join(pending, ", ") + ".")
		}
	}
	if len(snap.outline) > 0 {
		b.WriteString("\nWhat each reply established:")
		for _, line := range snap.outline {
			b.WriteString("\n- " + line)
		}
	}
	b.WriteString("\nConversation so far:\n" + snap.digest + `
Decide which member should speak next. Respond ONLY with a JSON object:
{"next": "<member name>", "reason": "<one short sentence>"}
Take fair turns first: before anyone speaks twice, every member who can
contribute gets a turn; afterwards route by what the task needs. Use
{"next": "", "reason": "..."} when the user's request has been fully
addressed and another reply would add nothing. You may pick the same
member again if they should continue. Never pick a member whose duties
do not match what the conversation needs next. A member ending with a
question or open options is not done: route it to whoever is best
placed to decide or answer, unless only the user can.`)
	return b.String()
}

// notYetSpoken lists roster members the relay has not dispatched yet.
func notYetSpoken(snap panelSnapshot) []string {
	var out []string
	for _, m := range snap.roster {
		if !slices.Contains(snap.spoken, m.Name) {
			out = append(out, m.Name)
		}
	}
	return out
}

// routerPost is the HTTP half every router wire shares: one bounded
// POST against an OpenAI-compatible endpoint, with the transient-
// failure retry the built-in loop keeps. path is relative to the base
// URL ("/chat/completions", "/systemone").
func routerPost(ctx context.Context, baseURL, apiKey, path string, body []byte) ([]byte, error) {
	url := strings.TrimRight(baseURL, "/") + path
	client := &http.Client{Timeout: routerCallTimeout}
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			select {
			case <-time.After(500 * time.Millisecond):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		if apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+apiKey)
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
				return nil, lastErr // a 4xx is the request's own fault
			}
			continue
		}
		if readErr != nil {
			lastErr = readErr
			continue
		}
		return data, nil
	}
	if errors.Is(lastErr, context.DeadlineExceeded) {
		return nil, fmt.Errorf("coordinator call timed out (limit %v per attempt)", routerCallTimeout)
	}
	return nil, lastErr
}

// routerChat is one non-streaming chat completion against the
// OpenAI-compatible endpoint (Ollama's is one), with the usage the
// reply reports.
func routerChat(ctx context.Context, snap panelSnapshot, msgs []map[string]string) (string, int64, error) {
	body, err := json.Marshal(map[string]any{
		"model":           snap.model,
		"messages":        msgs,
		"temperature":     0,
		"response_format": map[string]string{"type": "json_object"},
	})
	if err != nil {
		return "", 0, err
	}
	data, err := routerPost(ctx, snap.baseURL, snap.apiKey, "/chat/completions", body)
	if err != nil {
		return "", 0, err
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			TotalTokens int64 `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return "", 0, fmt.Errorf("router reply was not chat completions: %w", err)
	}
	if len(out.Choices) == 0 {
		return "", 0, fmt.Errorf("router reply had no choices")
	}
	return out.Choices[0].Message.Content, out.Usage.TotalTokens, nil
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

// forkMemory is one parallel member's isolated transcript view: it
// seeds from the shared ChatLog at dispatch and keeps the member's
// exchanges private while it runs, so two concurrent loops never
// interleave writes into one history (threadMemory is the shared home).
// When the member lands, the host appends the private suffix to the
// shared log, in landing order.
type forkMemory struct {
	a    *app
	key  string
	priv []harness.ChatMessage
	seed int // priv entries that are the shared log's, not the member's
}

func (f *forkMemory) LoadTranscript(key string) []harness.ChatMessage {
	return slices.Clone(f.priv)
}

func (f *forkMemory) StoreTranscript(key string, msgs []harness.ChatMessage) {
	f.priv = slices.Clone(msgs)
}

// merge appends the member's own entries to the shared transcript. Runs
// inside a.update; merging twice is a no-op.
func (f *forkMemory) merge() {
	if f.seed >= len(f.priv) {
		return
	}
	th := f.a.threadByMemoryKey(f.key)
	if th == nil {
		return
	}
	th.ChatLog = append(th.ChatLog, f.priv[f.seed:]...)
	f.seed = len(f.priv)
}
