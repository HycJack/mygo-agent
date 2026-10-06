package app

// The delegate tool (spec/agents.md, P6 multi-agent): one hop from the
// running loop to another configured agent. The sub-run is a real loop
// pass over the target's resolved profile, seeded fresh — it cannot see
// the parent conversation — under the parent turn's mode, which is the
// inherited approval starting point; an ask the sub-run raises lands on
// the parent's OnApproval, so the user answers every card themselves.
// One hop only: the sub-run's tools are the registry, never delegate.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"mygo-agent/internal/harness"
	"mygo-agent/internal/harness/builtin"
	"mygo-agent/internal/harness/cli"
)

// delegateTarget is one delegate candidate, fully resolved: everything
// the tool's Execute needs, snapshotted on the main thread
// (spec/architecture.md, threading).
type delegateTarget struct {
	id   string
	name string
	ov   agentOverlay
}

// delegateTool is built per turn from the turn's own snapshot.
func (h builtinHarness) delegateTool() builtin.Tool {
	return builtin.Tool{
		Name: "delegate",
		Description: "Delegate a sub-task to another configured agent and return its final answer. " +
			"The sub-agent runs with its own profile (model, tools, permissions) and cannot see this conversation; " +
			"use it for a piece of work that needs a different specialist. The agent's name is in the system prompt's agent list.",
		Actions: []harness.Action{harness.ActionDelegate},
		Parameters: json.RawMessage(`{"type":"object","properties":` +
			`{"agent":{"type":"string","description":"The agent to delegate to."},` +
			`"task":{"type":"string","description":"The complete, self-contained sub-task. The sub-agent sees nothing else."}},` +
			`"required":["agent","task"]}`),
		Execute: h.delegateExecute,
	}
}

// delegateExecute runs the sub-task synchronously: the sub-run's text
// becomes the tool result, bounded like any other tool output.
func (h builtinHarness) delegateExecute(ctx context.Context, args string) (string, error) {
	var in struct {
		Agent string `json:"agent"`
		Task  string `json:"task"`
	}
	if err := json.Unmarshal([]byte(args), &in); err != nil {
		return "", err
	}
	in.Task = strings.TrimSpace(in.Task)
	if in.Task == "" {
		return "", errors.New("task is required")
	}
	var target *delegateTarget
	for i := range h.delegates {
		if strings.EqualFold(h.delegates[i].name, in.Agent) || h.delegates[i].id == in.Agent {
			target = &h.delegates[i]
			break
		}
	}
	if target == nil {
		return "", fmt.Errorf("no delegate agent named %q", in.Agent)
	}
	ov := target.ov
	if ov.endpoint == nil {
		return "", fmt.Errorf("the agent %q has no provider with a base URL configured", target.name)
	}

	// The seed: the target's system head — the built-in prompt, the
	// skills its selection kept, its own instructions — then the task.
	skills := builtin.DiscoverSkills(h.turn.Workdir).Select(ov.skills)
	sys := builtinSystemPrompt(h.turn.Workdir, skills)
	if ov.systemPrompt != "" {
		sys += "\n\n" + ov.systemPrompt
	}
	history := []harness.ChatMessage{
		{Role: "system", Content: sys + seedTail},
		{Role: "user", Content: in.Task},
	}

	// The target's own tools: its registry minus its disabled names, and
	// its MCP subset spawned for the sub-run and closed with it.
	tools := builtin.Tools(h.turn.Workdir, skills, builtin.ToolOptions{
		Sandbox:       h.turn.Sandbox,
		ConfineWrites: h.turn.Mode != harness.ModeFull,
		Enabled:       ov.toolEnabled,
	})
	clients := h.a.connectMCP(ctx, ov.mcpServers)
	for _, c := range clients {
		defer c.Close()
		if mt, err := c.ListTools(ctx); err == nil {
			tools = append(tools, mt...)
		}
	}

	cfg := builtin.LoopConfig{
		BaseURL: ov.endpoint.BaseURL, APIKey: ov.endpoint.APIKey,
		Model: ov.model, Wire: ov.endpoint.Wire,
		ContextWindow:   ov.endpoint.ContextWindow,
		ReasoningEffort: effortLabel(ov.effort),
		Tools:           tools,
		Policy:          harness.Policy{Mode: h.turn.Mode, Rules: ov.rules},
		OnApproval:      h.turn.OnApproval,
		ApprovalTimeout: h.approvalLimit,
		MaxTurns:        ov.maxTurns,
		MaxMessages:     ov.maxTurns*6 + 12,
	}
	final, err := builtin.Run(ctx, cfg, history)
	answer := lastAssistantText(final)
	if err != nil && answer == "" {
		if errors.Is(err, context.Canceled) {
			return "", err // the parent turn is stopping; let it stop
		}
		return "", fmt.Errorf("delegate %s: %v", target.name, err)
	}
	if strings.TrimSpace(answer) == "" {
		return "", fmt.Errorf("delegate %s returned no answer", target.name)
	}
	return cli.TrimOutput(answer, 32<<10), nil
}

// effortLabel names the reasoning effort the loop expects.
func effortLabel(effort int) string {
	switch effort {
	case 0:
		return "low"
	case 2:
		return "high"
	}
	return "medium"
}

// lastAssistantText reads the final answer off a finished transcript.
func lastAssistantText(history []harness.ChatMessage) string {
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role != "assistant" {
			continue
		}
		if s, ok := history[i].Content.(string); ok {
			return s
		}
	}
	return ""
}
