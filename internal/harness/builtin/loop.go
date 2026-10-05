package builtin

import (
	"mygo-agent/internal/harness/cli"

	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// defaultApprovalTimeout bounds every wait on the host: expiry denies.
const defaultApprovalTimeout = 10 * time.Minute

// ErrApprovalTimedOut is the cause an approval's context carries when
// ApprovalTimeout expires. The host reads it to settle what it is showing
// for the request — the denial itself spells the same reason.
var ErrApprovalTimedOut = errors.New("approval timed out")

// LoopConfig configures one run of the agent loop. The zero Policy is
// read-only: a caller that sets no policy fails closed.
type LoopConfig struct {
	BaseURL         string
	APIKey          string
	Model           string
	Wire            string // WireChat (default) or WireResponses
	ReasoningEffort string // responses API only
	SystemPrompt    string
	Tools           []Tool
	MaxTurns        int
	MaxMessages     int // compact the transcript past this many messages
	OnEvent         func(Event)

	// Policy gates every tool call before execution. The zero value is
	// read-only (the strictest mode).
	Policy Policy

	// OnApproval decides calls whose permission resolves to ask. The loop
	// calls it with a context that expires when the loop stops waiting —
	// the ApprovalTimeout deadline or run cancellation — and may block
	// until that context is done; a nil handler denies ask calls up front.
	OnApproval      func(ctx context.Context, req ApprovalRequest) ApprovalDecision
	ApprovalTimeout time.Duration // default 10 minutes; expiry denies
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
		messages = CompactHistory(messages, cfg.MaxMessages)
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
			out, execErr := runGatedTool(ctx, cfg, call)
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

// runGatedTool resolves the call's permission (spec/permissions.md) and
// executes only allowed calls; denied and unanswered calls settle as a
// DenialError the model can read.
func runGatedTool(ctx context.Context, cfg LoopConfig, call ToolCall) (string, error) {
	for _, t := range cfg.Tools {
		if t.Name != call.Function.Name {
			continue
		}
		args, err := parseToolArgs(call.Function.Arguments)
		if err != nil {
			return "", err
		}
		switch cfg.Policy.Resolve(t.Name, t.Actions) {
		case PermDeny:
			return "", &DenialError{Tool: t.Name}
		case PermAsk:
			if err := askApproval(ctx, cfg, t, call); err != nil {
				return "", err
			}
		}
		return t.Execute(ctx, args)
	}
	return "", errors.New("unknown tool " + call.Function.Name)
}

// askApproval waits for the host's decision on one prepared call. The
// host sees the same context the loop waits on: when the loop stops
// waiting — timeout or run cancellation — that context expires on the
// host's side too, so it settles whatever it is showing for the request
// instead of leaving it pending. Timeout, cancellation and a missing
// handler all land in the same place: a settled denial (spec/approvals.md).
func askApproval(ctx context.Context, cfg LoopConfig, t Tool, call ToolCall) error {
	req := ApprovalRequest{
		Call:    call,
		Summary: ApprovalSummary(call),
		Reason:  "the permission policy asks for this tool",
	}
	if cfg.OnApproval == nil {
		return &DenialError{Tool: t.Name, Reason: "no approval handler is configured"}
	}
	timeout := cfg.ApprovalTimeout
	if timeout <= 0 {
		timeout = defaultApprovalTimeout
	}
	// Expiry sets the cause, so the host can tell "approval timed out"
	// from a cancelled run when the context fires there as well.
	actx, cancel := context.WithTimeoutCause(ctx, timeout, ErrApprovalTimedOut)
	defer cancel()
	type answer struct{ d ApprovalDecision }
	ch := make(chan answer, 1)
	go func() { ch <- answer{cfg.OnApproval(actx, req)} }()
	select {
	case a := <-ch:
		if a.d.Approved {
			return nil
		}
		return &DenialError{Tool: t.Name, Reason: a.d.Reason}
	case <-actx.Done():
		if errors.Is(context.Cause(actx), ErrApprovalTimedOut) {
			return &DenialError{Tool: t.Name, Reason: ErrApprovalTimedOut.Error()}
		}
		return &DenialError{Tool: t.Name, Reason: "cancelled before approval"}
	}
}

// executeTool finds the tool by name and runs it with its JSON args.
// Weak models often emit near-JSON (code fences, trailing commas,
// single quotes, smart quotes), so the arguments go through a repair
// pass before the tool sees them. It performs no permission check; the
// loop gates through runGatedTool.
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
	return "", fmt.Errorf("tool arguments are not valid JSON (model wrote: %s)", cli.Trunc(args, 200))
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

// Args decodes a tool call's arguments into a struct.
func Args(call ToolCall, into any) error {
	return json.Unmarshal([]byte(call.Function.Arguments), into)
}

// CompactHistory folds old tool exchanges once the transcript outgrows
// maxMessages: the system prompt, the first user message and the most
// recent turns survive, everything between is dropped at a clean turn
// boundary (a user message). Long tasks keep running instead of
// overflowing the model's context window.
func CompactHistory(msgs []ChatMessage, maxMessages int) []ChatMessage {
	if maxMessages <= 0 || len(msgs) <= maxMessages {
		return msgs
	}
	head := 0
	for head < len(msgs) && msgs[head].Role == "system" {
		head++
	}
	keepFrom := len(msgs) - (maxMessages - head)
	// The kept window must start on a user turn, never on a tool
	// result whose call it answers.
	for keepFrom > head && msgs[keepFrom].Role != "user" {
		keepFrom--
	}
	if keepFrom <= head {
		return msgs
	}
	out := make([]ChatMessage, 0, maxMessages)
	out = append(out, msgs[:head]...)
	out = append(out, msgs[keepFrom:]...)
	return out
}
