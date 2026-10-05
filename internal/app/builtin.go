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
func (a *app) runBuiltin(ctx context.Context, th *Thread, turn harness.Turn, emit func(harness.Event)) error {
	prompt, at := turn.Prompt, -1
	for i := range th.Messages {
		if th.Messages[i].Running {
			at = i
		}
	}
	if at < 0 {
		at = len(th.Messages) - 1
	}

	p := a.provider()
	if p == nil || p.BaseURL == "" || p.APIKey == "" {
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
	mcpClients := a.connectMCP(ctx)
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
	flush := func() {
		mu.Lock()
		s := pending.String()
		pending.Reset()
		mu.Unlock()
		if s == "" {
			return
		}
		a.update(func() {
			if m := reply(th, at); m != nil {
				m.Text += s
			}
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
		ApprovalTimeout: a.approvalTimeout,
		MaxTurns:        turn.MaxTurns,
		MaxMessages:     turn.MaxTurns*6 + 12,
		OnEvent: func(e harness.Event) {
			switch e.Kind {
			case harness.EventText:
				mu.Lock()
				pending.WriteString(e.TextDelta)
				mu.Unlock()
			case harness.EventToolStart, harness.EventToolEnd, harness.EventError, harness.EventNote:
				// Tool cards and reports go through the shared projector;
				// the text stays on its 40ms batching ticker above.
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
	history := a.transcriptFor(turn, th, at, prompt)
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

// connectMCP starts every configured server, skipping the ones that
// fail (they would only produce tool errors).
func (a *app) connectMCP(ctx context.Context) []*builtin.ServerClient {
	var clients []*builtin.ServerClient
	for _, s := range a.effectiveMCPServers() {
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
// carried over.
func (a *app) seedChatLog(th *Thread, at int, prompt string) []harness.ChatMessage {
	msgs := []harness.ChatMessage{{
		Role: "system",
		Content: builtinSystemPrompt(a.workdir, builtin.DiscoverSkills(a.workdir)) +
			"\n\nAnswer in the user's language. When you have the result, summarise what you did and stop; do not call tools without a reason.",
	}}
	for i, m := range th.Messages {
		if i >= at-1 {
			break
		}
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
func (a *app) transcriptFor(turn harness.Turn, th *Thread, at int, prompt string) []harness.ChatMessage {
	history := []harness.ChatMessage(nil)
	if turn.Memory != nil {
		history = turn.Memory.LoadTranscript(turn.MemoryKey)
	}
	if history == nil {
		history = a.seedChatLog(th, at, prompt)
	} else if prompt != "" {
		history = append(history, harness.ChatMessage{Role: "user", Content: prompt})
	}
	return history
}
