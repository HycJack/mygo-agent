package app

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"mygo-agent/internal/harness"
	"mygo-agent/internal/harness/builtin"
)

// runBuiltin drives the built-in agent loop against the selected
// provider: streaming replies, tool calls as cards, skills and MCP
// tools, all in-process. Like every adapter it returns an error — nil
// for a clean or stopped turn — and the host settles the reply.
//
// Everything it needs comes from the snapshot the harness captured on the
// main thread, never from the live app: a turn must not change its
// endpoint, model or tool set underneath the cards the user is deciding on.
func (h builtinHarness) runBuiltin(ctx context.Context, emit func(harness.Event)) error {
	a, th, turn := h.a, h.th, h.turn
	// The message index and the seed history were captured on the main
	// thread when the harness was built. Rescanning th.Messages here would
	// be an unsynchronized read racing the main thread's own appends and
	// block toggles — the same mistake LoadTranscript used to make.
	prompt, at := turn.Prompt, h.at

	p := h.provider
	if p.BaseURL == "" || p.APIKey == "" {
		return errors.New("The built-in agent needs a provider with a base URL and an API key. Open the model picker → Manage providers & models…, fill them in, then pick a model from that provider.")
	}

	skills := builtin.DiscoverSkills(turn.Workdir)
	// The mode tunes how the tools execute (spec/permissions.md): agent
	// mode runs the shell inside the sandbox and confines writes to the
	// workspace; full access does neither; read-only denies both before
	// execution.
	tools := builtin.Tools(turn.Workdir, skills, builtin.ToolOptions{
		Sandbox:       turn.Sandbox,
		ConfineWrites: turn.Mode != harness.ModeFull,
	})
	mcpClients := a.connectMCP(ctx, h.mcpServers)
	defer func() {
		for _, c := range mcpClients {
			c.Close()
		}
	}()
	for _, mc := range mcpClients {
		mt, err := mc.ListTools(ctx)
		if err != nil {
			mt = nil
		}
		tools = append(tools, mt...)
	}

	// Text deltas are batched onto a 40 ms ticker so a fast stream does
	// not redraw per token; tool events flush immediately.
	var mu sync.Mutex
	var pending strings.Builder
	toolStarts := map[string]time.Time{}
	done := make(chan struct{})
	// The lock spans the update, not just the take. Two flushes can run
	// concurrently — the ticker and the final one after close(done) — and
	// if the lock were released after reading, the later taker could win
	// the race to the main thread and the two chunks would be appended in
	// the wrong order, scrambling the reply's text. update() only posts
	// to the main queue (or takes app.mu headless) and never calls back
	// into flush, so holding it here cannot deadlock.
	flush := func() {
		mu.Lock()
		defer mu.Unlock()
		s := pending.String()
		pending.Reset()
		if s == "" {
			return
		}
		// Through the shared projector, not a direct Text append: the prose
		// has to land in the ordered block sequence, or a tool call that
		// arrived mid-stream would render after the text it interrupted.
		a.update(func() {
			a.projectEvent(th, at, "builtin", harness.Event{Kind: harness.EventText, TextDelta: s})
		})
	}
	project := func(ev harness.Event) { a.applyEvent(th, at, "builtin", ev) }
	go func() {
		ticker := time.NewTicker(40 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				flush()
			}
		}
	}()

	wire := p.Wire
	if wire == "" {
		wire = harness.WireChat
	}
	cfg := builtin.LoopConfig{
		BaseURL:         p.BaseURL,
		APIKey:          p.APIKey,
		Model:           turn.Model,
		Wire:            wire,
		ReasoningEffort: []string{"low", "medium", "high"}[turn.Effort],
		SystemPrompt:    builtinSystemPrompt(turn.Workdir, skills),
		Tools:           tools,
		Policy:          harness.Policy{Mode: turn.Mode, Rules: turn.Rules},
		// Approvals surface as cards on the reply; the loop's timeout
		// denies what the user never answers, and the context it hands
		// the host settles the card when it does (spec/approvals.md).
		OnApproval:      turn.OnApproval,
		ApprovalTimeout: h.approvalLimit,
		MaxTurns:        turn.MaxTurns,
		MaxMessages:     turn.MaxTurns*6 + 12,
		OnEvent: func(e harness.Event) {
			switch e.Kind {
			case harness.EventText:
				mu.Lock()
				pending.WriteString(e.TextDelta)
				mu.Unlock()
			case harness.EventToolStart, harness.EventToolEnd, harness.EventError,
				harness.EventNote, harness.EventReasoning:
				// Tool cards and reports go through the shared projector;
				// the text stays on its 40ms batching ticker above.
				// Reasoning belongs in this filter too: it was missing
				// from it entirely, so the built-in agent's thinking was
				// dropped on the floor while every other backend's
				// reached the transcript.
				flush()
				if e.Kind == harness.EventToolStart {
					toolStarts[e.ToolCall.ID] = time.Now()
					e.Text = toolDisplayFor(e.ToolCall)
					if e.ToolCall.Function.Name == "edit_file" {
						e.Edit = true
					}
				}
				if e.Kind == harness.EventToolEnd {
					if started, ok := toolStarts[e.ToolCall.ID]; ok {
						e.Ms = time.Since(started).Milliseconds()
					}
					e.Text = toolDisplayFor(e.ToolCall)
					// An edit's tool result is its diff; hand the projector
					// a preview so the card opens as one.
					if e.ToolCall.Function.Name == "edit_file" && len(parseToolDiff(e.Output)) > 0 {
						e.Edit = true
						e.Diff = e.Output
					}
				}
				project(e)
			}
		},
	}

	// The transcript lives behind the Memory protocol
	// (spec/architecture.md): load it, seed it from the thread's visible
	// messages on the first turn, append this turn's prompt.
	history := h.transcriptFor(prompt)
	logAt := len(history)
	a.update(func() {
		if m := reply(th, at); m != nil {
			m.LogAt = logAt
		}
	})

	transcript, err := builtin.Run(ctx, cfg, history)
	close(done)
	flush()
	if turn.Memory != nil {
		turn.Memory.StoreTranscript(turn.MemoryKey, transcript)
	}
	if err != nil && ctx.Err() == nil && !errors.Is(err, builtin.ErrTurnLimit) {
		// The turn limit already announced itself as a notice.
		return err
	}
	return nil
}

// connectMCP starts every snapshotted server, skipping the ones that
// fail (they would only produce tool errors).
func (a *app) connectMCP(ctx context.Context, servers []builtin.MCPServer) []*builtin.ServerClient {
	var clients []*builtin.ServerClient
	for _, s := range servers {
		if c, err := builtin.StartServer(ctx, s); err == nil {
			clients = append(clients, c)
		}
	}
	return clients
}

// builtinSystemPrompt describes the agent, its project and its skills.
func builtinSystemPrompt(workdir string, skills *builtin.SkillSet) string {
	var b strings.Builder
	b.WriteString("You are Codex, a coding agent embedded in a desktop app. ")
	b.WriteString("You work inside the project directory " + workdir + ". ")
	b.WriteString("Use the tools to read and change files, run shell commands and search. ")
	b.WriteString("Prefer edit_file for precise changes and bash for everything else; ")
	b.WriteString("keep commands non-interactive and check your work with tests or builds when they exist.")
	b.WriteString(skills.PromptSection())
	return b.String()
}

// parseToolDiff converts an edit_file result ("+ line" / "- line") into
// colored diff lines for the diff card.
func parseToolDiff(out string) []DiffLine {
	var lines []DiffLine
	for _, l := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if l == "" {
			continue
		}
		switch l[0] {
		case '+', '-', ' ':
			lines = append(lines, DiffLine{Kind: l[0], Text: strings.TrimPrefix(l[1:], " ")})
		}
	}
	return lines
}

// seedChatLog builds a fresh transcript from the thread's visible
// messages plus the new prompt: system, prior user/assistant texts,
// then the prompt. Tool round-trips from earlier app sessions are not
// carried over. The history it walks is the copy taken on the main thread
// at construction, never the live thread.
func (h builtinHarness) seedChatLog(prompt string) []harness.ChatMessage {
	prior := h.priorMessages
	msgs := []harness.ChatMessage{{
		Role: "system",
		Content: builtinSystemPrompt(h.turn.Workdir, builtin.DiscoverSkills(h.turn.Workdir)) +
			"\n\nAnswer in the user's language. When you have the result, summarise what you did and stop; do not call tools without a reason.",
	}}
	for _, m := range prior {
		switch m.Role {
		case "user":
			msgs = append(msgs, harness.ChatMessage{Role: "user", Content: m.Text})
		case "assistant":
			if strings.TrimSpace(m.Text) != "" {
				msgs = append(msgs, harness.ChatMessage{Role: "assistant", Content: m.Text})
			}
		}
	}
	if prompt != "" {
		msgs = append(msgs, harness.ChatMessage{Role: "user", Content: prompt})
	}
	return msgs
}

// transcriptFor resolves the turn's starting transcript through the
// Memory protocol, seeding from the thread's visible messages when the
// store is empty.
func (h builtinHarness) transcriptFor(prompt string) []harness.ChatMessage {
	history := []harness.ChatMessage(nil)
	if h.turn.Memory != nil {
		history = h.turn.Memory.LoadTranscript(h.turn.MemoryKey)
	}
	if len(history) == 0 {
		return h.seedChatLog(prompt)
	}
	if prompt != "" {
		history = append(history, harness.ChatMessage{Role: "user", Content: prompt})
	}
	return history
}
