package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Event is one thing the loop reports to the UI while running.
type Event struct {
	Kind      string // "text" | "tool_start" | "tool_end" | "error" | "done"
	TextDelta string // for "text"
	ToolCall  ToolCall
	Output    string // for "tool_end"
	Exit      int    // for "tool_end": 0 ok, 1 failed
	Err       string // for "error"
}

// LoopConfig configures one run of the agent loop.
type LoopConfig struct {
	BaseURL         string
	APIKey          string
	Model           string
	Wire            string // WireChat (default) or WireResponses
	ReasoningEffort string // responses API only
	SystemPrompt    string
	Tools           []Tool
	MaxTurns        int
	OnEvent         func(Event)
}

// ToolCallResult is what executing one tool call produced.
type ToolCallResult struct {
	ToolCall ToolCall
	Output   string
	IsError  bool
}

// ErrTurnLimit is returned when the loop stops because it hit the turn
// budget — a safety stop against a runaway agent, not a failure. The
// transcript is complete and the task continues on the next message.
var ErrTurnLimit = errors.New("turn limit reached")

// Run executes the agent loop until the model stops calling tools, the
// turn budget runs out, or the context is cancelled. It returns the
// final transcript including this turn's exchanges.
func Run(ctx context.Context, cfg LoopConfig, history []ChatMessage) ([]ChatMessage, error) {
	if cfg.MaxTurns <= 0 {
		cfg.MaxTurns = 25
	}
	emit := func(e Event) {
		if cfg.OnEvent != nil {
			cfg.OnEvent(e)
		}
	}
	messages := make([]ChatMessage, 0, len(history)+8)
	messages = append(messages, history...)

	for range cfg.MaxTurns {
		if ctx.Err() != nil {
			return messages, ctx.Err()
		}
		scfg := StreamConfig{
			BaseURL:         cfg.BaseURL,
			APIKey:          cfg.APIKey,
			Model:           cfg.Model,
			Wire:            cfg.Wire,
			Messages:        messages,
			Tools:           cfg.Tools,
			ReasoningEffort: cfg.ReasoningEffort,
		}
		res, err := streamChat(ctx, scfg, func(delta string) {
			emit(Event{Kind: "text", TextDelta: delta})
		})
		if err != nil {
			if ctx.Err() != nil {
				return messages, ctx.Err()
			}
			emit(Event{Kind: "error", Err: err.Error()})
			return messages, err
		}
		if res.Content != "" {
			messages = append(messages, ChatMessage{Role: "assistant", Content: res.Content})
		} else if len(res.ToolCalls) > 0 {
			// A pure tool-call turn: the spec wants content absent.
			messages = append(messages, ChatMessage{Role: "assistant", Content: nil})
		}
		if len(res.ToolCalls) == 0 {
			emit(Event{Kind: "done"})
			return messages, nil
		}
		// The assistant message that carries tool calls needs the full
		// list for the next request.
		if len(messages) > 0 && messages[len(messages)-1].Role == "assistant" {
			messages[len(messages)-1].ToolCalls = res.ToolCalls
		} else {
			messages = append(messages, ChatMessage{Role: "assistant", ToolCalls: res.ToolCalls})
		}
		for _, call := range res.ToolCalls {
			if ctx.Err() != nil {
				return messages, ctx.Err()
			}
			emit(Event{Kind: "tool_start", ToolCall: call})
			out, execErr := executeTool(ctx, cfg.Tools, call)
			exit := 0
			if execErr != nil {
				exit = 1
				if out == "" {
					out = execErr.Error()
				} else {
					out += "\n" + execErr.Error()
				}
			}
			emit(Event{Kind: "tool_end", ToolCall: call, Output: out, Exit: exit})
			messages = append(messages, ChatMessage{
				Role:       "tool",
				Content:    out,
				ToolCallID: call.ID,
			})
		}
	}
	emit(Event{Kind: "notice", Err: fmt.Sprintf(
		"Stopped after %d rounds of tool calls (the turn limit — raise max_turns in config.json). Send a message to keep going.",
		cfg.MaxTurns)})
	return messages, ErrTurnLimit
}

// executeTool finds the tool by name and runs it with its JSON args.
// Weak models often emit near-JSON (code fences, trailing commas,
// single quotes, smart quotes), so the arguments go through a repair
// pass before the tool sees them.
func executeTool(ctx context.Context, tools []Tool, call ToolCall) (string, error) {
	for _, t := range tools {
		if t.Name != call.Function.Name {
			continue
		}
		args, err := parseToolArgs(call.Function.Arguments)
		if err != nil {
			return "", err
		}
		return t.Execute(ctx, args)
	}
	return "", errors.New("unknown tool " + call.Function.Name)
}

// parseToolArgs cleans up a tool call's argument string and returns
// valid JSON, repairing the shapes weak models actually produce.
func parseToolArgs(raw string) (string, error) {
	args := strings.TrimSpace(raw)
	if args == "" {
		return "{}", nil
	}
	if json.Valid([]byte(args)) {
		return args, nil
	}
	for _, candidate := range repairJSON(args) {
		if json.Valid([]byte(candidate)) {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("tool arguments are not valid JSON (model wrote: %s)", trunc(args, 200))
}

// repairJSON lists the fix-ups tried in order.
func repairJSON(args string) []string {
	var out []string
	s := args
	// Strip a markdown code fence.
	if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```json")
		s = strings.TrimPrefix(s, "```")
		s = strings.TrimSuffix(strings.TrimSpace(s), "```")
		s = strings.TrimSpace(s)
	}
	// Smart quotes left by a chatty model.
	fixed := strings.NewReplacer("“", `"`, "”", `"`, "‘", "'", "’", "'").Replace(s)
	if fixed != s {
		out = append(out, fixed)
	}
	// Trailing commas before a closing brace or bracket.
	commaFixed := trailingComma.ReplaceAllString(s, "$1")
	out = append(out, commaFixed)
	if commaFixed != s {
		out = append(out, trailingComma.ReplaceAllString(fixed, "$1"))
	}
	// Single-quoted or backtick-quoted strings become double-quoted
	// ones. A crude pass, but it covers the shapes weak models emit.
	quote := strings.NewReplacer("'", `"`, "`", `"`)
	out = append(out, quote.Replace(s), quote.Replace(commaFixed))
	return out
}

var (
	trailingComma = regexp.MustCompile(`,(\s*[}\]])`)
)

func trunc(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// Args decodes a tool call's arguments into a struct.
func Args(call ToolCall, into any) error {
	return json.Unmarshal([]byte(call.Function.Arguments), into)
}

// TrimOutput caps a tool result so one command cannot flood the context.
func TrimOutput(s string, max int) string {
	s = strings.TrimRight(s, "\n")
	if len(s) <= max {
		return s
	}
	return s[:max] + "\n… output truncated …"
}
