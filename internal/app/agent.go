package app

import (
	"fmt"
	"strings"
	"time"

	"mygo-agent/internal/harness/builtin"
)

// send takes the draft, appends it to the thread, and starts the
// harness. Other threads' turns do not block a send — only this
// thread's own running turn does (spec/agents.md P2) — and a running
// routed relay takes the message as an interjection instead: it joins
// the transcript now and the next member's handoff (spec/
// relay-router.md).
func (a *app) send() {
	prompt := strings.TrimSpace(a.draft)
	if prompt == "" {
		return
	}
	th := a.currentThread()
	if th != nil && a.isRunning(th.ID) {
		a.interject(th, prompt)
		return // this thread's turn is still in flight; the draft goes in as an interjection or stays
	}
	a.setDraft("")
	if th == nil {
		th = a.createThread()
	}
	a.startTurn(th, prompt)
}

// interject delivers the user's message into a running routed relay:
// it lands in the transcript immediately and rides the next member's
// handoff prompt. A solo turn or a sequence relay is not interruptible
// — the draft stays — and without a relay there is nothing to hand it
// to.
func (a *app) interject(th *Thread, prompt string) {
	st := a.groupQueue[th.ID]
	if st == nil || a.relayRouteMode(th) != "router" {
		return
	}
	st.pending = append(st.pending, prompt)
	now := time.Now()
	th.Messages = append(th.Messages, Message{ID: uid(), Role: "user", Text: prompt, At: now})
	th.Updated = now
	a.setDraft("")
	a.saveThread(th)
}

// resend re-runs a prompt the user already sent, as a fresh turn.
func (a *app) resend(th *Thread, text string) {
	if a.isRunning(th.ID) || strings.TrimSpace(text) == "" {
		return
	}
	a.startTurn(th, text)
}

// regenerate drops the last turn's replies and runs it again. On a
// group thread the whole panel re-runs: every member's reply after the
// last user message goes, and the relay starts over (spec/agents.md).
// The built-in backend rewinds its ChatLog to where the turn started,
// so the retry sees the same history.
func (a *app) regenerate(th *Thread) {
	if a.isRunning(th.ID) || len(th.Messages) == 0 {
		return
	}
	if th.Messages[len(th.Messages)-1].Role != "assistant" {
		return
	}
	panel := a.panelFor(a.agentFor(th))
	// Where the turn to rewind begins: the first reply after the last
	// user message — on a group thread that is the FIRST member's reply.
	userAt := -1
	for i := len(th.Messages) - 1; i >= 0; i-- {
		if th.Messages[i].Role == "user" {
			userAt = i
			break
		}
	}
	if userAt < 0 || userAt == len(th.Messages)-1 {
		return
	}
	logAt := th.Messages[userAt+1].LogAt
	promptForSend := th.Messages[userAt].Text
	th.Messages = th.Messages[:userAt+1]
	if a.backendFor(th) == "builtin" {
		cut := min(int(logAt), len(th.ChatLog))
		// Compaction re-indexes the log, so logAt can point past it —
		// the clamp above then keeps the whole log, the reply being
		// regenerated included, and the retry answers a prompt whose
		// old answer still sits in its history. The summary marker is
		// the one seam that survives re-indexing: rewinding to just
		// past it drops exactly the exchange being regenerated.
		if cut == len(th.ChatLog) {
			for i, m := range th.ChatLog {
				if body, _ := m.Content.(string); strings.HasPrefix(body, builtin.CompactedPrefix) {
					cut = i + 1
				}
			}
		}
		th.ChatLog = th.ChatLog[:cut]
	}
	th.invalidateDiffCount() // the messages were rewound
	now := time.Now()
	th.Messages = append(th.Messages, Message{ID: uid(), Role: "assistant", Running: true, At: now, LogAt: logAt,
		AgentID: a.agentFor(th).ID})
	a.saveThread(th)
	at := len(th.Messages) - 1
	// The built-in transcript already holds the user's turn — re-sending
	// the prompt would append it twice. The CLI backends start from their
	// own session and need the prompt again.
	if a.backendFor(th) == "builtin" {
		promptForSend = ""
	}
	if len(panel) > 0 {
		// The relay re-runs whole: this dispatch is member one, the rest
		// queue for finish to hand over to. Router mode re-runs the same
		// way — member one first, the coordinator takes over from there
		// (spec/relay-router.md).
		ids := make([]string, 0, len(panel)-1)
		for _, m := range panel[1:] {
			ids = append(ids, m.ID)
		}
		a.groupQueue[th.ID] = &relayState{queue: ids, rounds: 1, start: now}
		a.dispatchParticipant(th, promptForSend, at, panel[0])
		return
	}
	a.dispatch(th, promptForSend, at)
}

// panelNudge hands the relay to the next member: a real user message in
// the shared transcript (some providers require the last message to be
// the user's), terse enough not to get in the way.
const panelNudge = "(Panel relay: it is your turn — add your contribution.)"

// panelDigest renders the thread's conversation for a member whose
// backend cannot read the shared transcript (the CLI sessions are
// private): who said what, bounded to the most recent tail.
func (a *app) panelDigest(th *Thread, at int, limit int) string {
	var b strings.Builder
	for _, m := range th.Messages[:min(at, len(th.Messages))] {
		who := "User"
		if m.Role == "assistant" {
			who = "Assistant"
			if ag := a.agentByID(m.AgentID); ag != nil {
				who = ag.Name
			}
		}
		text := strings.TrimSpace(m.Text)
		if text == "" {
			continue
		}
		fmt.Fprintf(&b, "%s: %s\n\n", who, text)
	}
	out := b.String()
	if len(out) > limit {
		cut := len(out) - limit
		if i := strings.IndexByte(out[cut:], '\n'); i >= 0 {
			cut += i
		}
		out = out[cut:]
	}
	return strings.TrimSpace(out)
}

// startTurn appends the user's prompt and a placeholder reply, then
// dispatches to the backend. A thread bound to an agent with a panel
// becomes a group relay: the members answer in order, each seeing the
// earlier replies in the shared conversation (spec/agents.md).
func (a *app) startTurn(th *Thread, prompt string) {
	now := time.Now()
	th.Messages = append(th.Messages, Message{ID: uid(), Role: "user", Text: prompt, At: now})
	if th.Title == "" {
		th.Title = truncTitle(prompt, 44)
	}
	th.Updated = now
	a.focusComposer = true
	panel := a.panelFor(a.agentFor(th))
	first := a.agentFor(th)
	if len(panel) > 0 {
		first = panel[0]
		ids := make([]string, 0, len(panel)-1)
		for _, m := range panel[1:] {
			ids = append(ids, m.ID)
		}
		a.groupQueue[th.ID] = &relayState{queue: ids, rounds: 1, start: now}
	}
	th.Messages = append(th.Messages, Message{ID: uid(), Role: "assistant", Running: true, At: now,
		AgentID: first.ID})
	at := len(th.Messages) - 1
	a.saveThread(th)
	if a.win != nil {
		a.win.SetTitle("Codex — " + th.Title)
	}
	a.dispatchParticipant(th, prompt, at, first)
}

// nextPanelMember pops the group turn's next member: nil ends the
// relay — queue empty, or the run was stopped (stopThread clears both).
func (a *app) nextPanelMember(th *Thread) *Agent {
	st := a.groupQueue[th.ID]
	if st == nil {
		return nil
	}
	for len(st.queue) > 0 && a.isRunning(th.ID) {
		queue := st.queue
		st.queue = queue[1:]
		// A member deleted mid-relay is skipped, not fatal.
		if ag := a.agentByID(queue[0]); ag != nil {
			return ag
		}
	}
	return nil
}

// relayRouteMode reports how this thread's relay picks the next speaker:
// "router" when the bound agent opted in (spec/relay-router.md), else
// "sequence" — the configured order, one reply per member. A thread
// without a panel never reaches the relay at all.
func (a *app) relayRouteMode(th *Thread) string {
	ag := a.agentFor(th)
	if ag != nil && ag.PanelRoute == "router" && len(ag.Panel) > 0 {
		return "router"
	}
	return "sequence"
}

// finish settles the running reply. On a group thread the relay
// continues: a sequence relay hands over to the next panel member, a
// router relay asks the coordinator which member speaks next (or that
// the relay is done) — and the run registry carries the whole chain.
func (a *app) finish(th *Thread, at int, errText string) {
	a.update(func() {
		// The registry entry is only cleared when the chain ends: a
		// stopped run loses its entry (stopThread), which is what ends
		// the relay here.
		router := a.relayRouteMode(th) == "router"
		var next *Agent
		if !router {
			next = a.nextPanelMember(th)
		}
		if cur := a.byID(th.ID); cur != nil && at < len(cur.Messages) {
			m := &th.Messages[at]
			m.Running = false
			if errText != "" {
				m.Blocks = append(m.Blocks, Block{Type: "error", Text: errText})
			}
			if m.Text == "" && errText == "" && len(m.Blocks) == 0 {
				m.Text = "(no response)"
			}
			// The relay's token budget counts what its members spend
			// (spec/relay-router.md); traceTurn clears the accumulators.
			if st := a.groupQueue[th.ID]; st != nil {
				st.tokens += m.turnTokens
			}
			th.Updated = time.Now()
			a.saveThread(th)
			a.traceTurn(th, at, errText)
		}
		if (next == nil && !router) || a.byID(th.ID) == nil {
			a.runEnd(th.ID)
			delete(a.groupQueue, th.ID)
			if a.wsOpen {
				a.refreshGit() // the agent may have changed files
			}
			return
		}
		if router {
			a.routeRelay(th, at)
			return
		}
		// The sequence relay continues: a fresh placeholder for the next
		// member.
		now := time.Now()
		th.Messages = append(th.Messages, Message{ID: uid(), Role: "assistant", Running: true, At: now,
			AgentID: next.ID})
		at := len(th.Messages) - 1
		a.saveThread(th)
		prompt := panelNudge
		if a.resolveAgent(next).backend != "builtin" {
			// A CLI member cannot read the shared transcript — its
			// session is private — so the host hands it the conversation
			// digest (spec/agents.md, honest mapping).
			prompt = fmt.Sprintf("You are %s in a panel of agents. The conversation so far:\n\n%s\n\n%s",
				next.Name, a.panelDigest(th, at, 8<<10), panelNudge)
		}
		a.dispatchParticipant(th, prompt, at, next)
	})
}

// reply ensures the running message exists at at and returns it.
func reply(th *Thread, at int) *Message {
	if at < len(th.Messages) {
		return &th.Messages[at]
	}
	return nil
}
