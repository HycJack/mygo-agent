package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"mygo-agent/internal/agent"
)

// runBuiltin drives the built-in agent loop against the selected
// provider: streaming replies, tool calls as cards, skills and MCP
// tools, all in-process.
func (a *app) runBuiltin(th *Thread, prompt string, at int) {
	ctx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel
	defer cancel()

	p := a.provider()
	if p == nil || p.BaseURL == "" || p.APIKey == "" {
		a.finish(th, at, "The built-in agent needs a provider with a base URL and an API key. Open the model picker → Manage providers & models…, fill them in, then pick a model from that provider.")
		return
	}

	skills := agent.DiscoverSkills(a.workdir)
	tools := agent.Tools(a.workdir, skills)
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
		wire = agent.WireChat
	}
	cfg := agent.LoopConfig{
		BaseURL:         p.BaseURL,
		APIKey:          p.APIKey,
		Model:           a.model,
		Wire:            wire,
		ReasoningEffort: []string{"low", "medium", "high"}[a.effort],
		SystemPrompt:    builtinSystemPrompt(a.workdir, skills),
		Tools:           tools,
		MaxTurns:        a.maxTurns,
		OnEvent: func(e agent.Event) {
			switch e.Kind {
			case "text":
				mu.Lock()
				pending.WriteString(e.TextDelta)
				mu.Unlock()
			case "tool_start":
				flush()
				toolStarts[e.ToolCall.ID] = time.Now()
				disp := toolDisplay(e.ToolCall)
				isEdit := e.ToolCall.Function.Name == "edit_file"
				a.update(func() {
					if m := reply(th, at); m != nil {
						if isEdit {
							m.Blocks = append(m.Blocks, Block{Type: "command", Text: disp, Running: true, Exit: -1, Edit: true})
						} else {
							m.Blocks = append(m.Blocks, Block{Type: "command", Text: disp, Running: true, Exit: -1})
						}
					}
				})
			case "tool_end":
				flush()
				var ms int64
				if started, ok := toolStarts[e.ToolCall.ID]; ok {
					ms = time.Since(started).Milliseconds()
				}
				a.update(func() {
					m := reply(th, at)
					if m == nil {
						return
					}
					bi := -1
					for i := len(m.Blocks) - 1; i >= 0; i-- {
						if m.Blocks[i].Type == "command" && m.Blocks[i].Running {
							bi = i
							break
						}
					}
					if bi < 0 {
						return
					}
					b := &m.Blocks[bi]
					b.Running = false
					b.Exit = e.Exit
					b.Ms = ms
					b.Output = agent.TrimOutput(e.Output, 16<<10)
					// Collapsed by default; diffs stay open, they are the
					// change itself.
					b.Open = b.Edit
					if b.Edit {
						// An edit renders as a colored diff card.
						lines := parseToolDiff(e.Output)
						if len(lines) > 0 {
							b.Type = "diff"
							b.Text = ""
							b.Lines = lines
							b.Add, b.Del = 0, 0
							for _, l := range lines {
								switch l.Kind {
								case '+':
									b.Add++
								case '-':
									b.Del++
								}
							}
							b.File = toolDisplayPath(e.ToolCall)
						}
					}
				})
			case "error":
				flush()
				a.update(func() {
					if m := reply(th, at); m != nil {
						m.Blocks = append(m.Blocks, Block{Type: "error", Text: e.Err})
					}
				})
			case "notice":
				flush()
				a.update(func() {
					if m := reply(th, at); m != nil {
						m.Blocks = append(m.Blocks, Block{Type: "reasoning", Text: e.Err})
					}
				})
			}
		},
	}

	history := a.builtinHistory(th, prompt, at)
	_, err := agent.Run(ctx, cfg, history)
	close(done)
	flush()
	errText := ""
	if err != nil && ctx.Err() == nil && !errors.Is(err, agent.ErrTurnLimit) {
		// The turn limit already announced itself as a notice.
		errText = err.Error()
	}
	a.finish(th, at, errText)
}

// connectMCP starts every configured server, skipping the ones that
// fail (they would only produce tool errors).
func (a *app) connectMCP(ctx context.Context) []*agent.ServerClient {
	var clients []*agent.ServerClient
	for _, s := range a.mcpServers {
		if c, err := agent.StartServer(ctx, s); err == nil {
			clients = append(clients, c)
		}
	}
	return clients
}

// builtinHistory builds the chat transcript: the system prompt, the
// thread's earlier turns, then the new prompt.
func (a *app) builtinHistory(th *Thread, prompt string, at int) []agent.ChatMessage {
	skills := agent.DiscoverSkills(a.workdir)
	msgs := []agent.ChatMessage{{
		Role: "system",
		Content: builtinSystemPrompt(a.workdir, skills) +
			"\n\nAnswer in the user's language. " +
			"When you have the result, summarise what you did and stop; do not call tools without a reason.",
	}}
	for i, m := range th.Messages {
		if i >= at {
			break
		}
		switch m.Role {
		case "user":
			msgs = append(msgs, agent.ChatMessage{Role: "user", Content: m.Text})
		case "assistant":
			if strings.TrimSpace(m.Text) != "" {
				msgs = append(msgs, agent.ChatMessage{Role: "assistant", Content: m.Text})
			}
		}
	}
	return msgs
}

// builtinSystemPrompt describes the agent, its project and its skills.
func builtinSystemPrompt(workdir string, skills *agent.SkillSet) string {
	var b strings.Builder
	b.WriteString("You are Codex, a coding agent embedded in a desktop app. ")
	b.WriteString("You work inside the project directory " + workdir + ". ")
	b.WriteString("Use the tools to read and change files, run shell commands and search. ")
	b.WriteString("Prefer edit_file for precise changes and bash for everything else; ")
	b.WriteString("keep commands non-interactive and check your work with tests or builds when they exist.")
	b.WriteString(skills.PromptSection())
	return b.String()
}

// toolDisplay renders a tool call as the one-line summary of its card.
func toolDisplay(call agent.ToolCall) string {
	var in map[string]any
	_ = json.Unmarshal([]byte(call.Function.Arguments), &in)
	get := func(k string) string {
		if s, ok := in[k].(string); ok {
			return s
		}
		return ""
	}
	short := func(s string, n int) string {
		if len(s) > n {
			return s[:n] + "…"
		}
		return s
	}
	switch call.Function.Name {
	case "bash":
		return "$ " + short(get("command"), 200)
	case "read_file":
		return "read_file " + get("path")
	case "edit_file":
		return "edit_file " + get("path")
	case "list_files":
		return "list_files " + get("path")
	case "grep":
		return "grep " + short(get("pattern"), 120)
	case "read_skill":
		return "read_skill " + get("name")
	default:
		if rest, ok := strings.CutPrefix(call.Function.Name, "mcp_"); ok {
			return rest + " " + short(call.Function.Arguments, 160)
		}
		return call.Function.Name + " " + short(call.Function.Arguments, 160)
	}
}

// toolDisplayPath pulls the path argument of a file tool call.
func toolDisplayPath(call agent.ToolCall) string {
	var in map[string]any
	_ = json.Unmarshal([]byte(call.Function.Arguments), &in)
	if s, ok := in["path"].(string); ok {
		return s
	}
	return ""
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
