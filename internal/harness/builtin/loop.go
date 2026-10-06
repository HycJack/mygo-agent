package builtin

import (
	"mygo-agent/internal/harness"
	"mygo-agent/internal/harness/cli"

	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// The approval deadline is shared with the other adapters, so it lives in
// the protocol root (spec/approvals.md).
const defaultApprovalTimeout = harness.DefaultApprovalTimeout

// LoopConfig configures one run of the agent loop. The zero Policy is
// read-only: a caller that sets no policy fails closed.
type LoopConfig struct {
	BaseURL          string
	APIKey           string
	Model            string
	Wire             string // WireChat (default) or WireResponses
	ReasoningEffort  string // responses API only
	SystemPrompt     string
	Tools            []Tool
	MaxTurns         int
	MaxMessages      int // compact the transcript past this many messages
	ToolOutputBudget int // bytes of tool results the transcript keeps; 0 = default

	// ContextWindow is the provider's window in tokens. Zero keeps the
	// count-based compaction alone; a declared window arms the
	// watermark — near the top of it the transcript is summarised by
	// the model itself instead of dropped.
	ContextWindow int

	OnEvent func(Event)

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

	// The provider's own count of the last request's prompt tokens: the
	// honest size of what the next request would be, plus this round's
	// additions. Seeded from a bytes estimate so a turn whose transcript
	// already sits over the window compacts on its first round instead
	// of failing it; a real count replaces the estimate at the first
	// answer.
	lastPromptTokens := estimateTokens(history)

	for range cfg.MaxTurns {
		if ctx.Err() != nil {
			return messages, ctx.Err()
		}
		// Elision runs before the watermark: what the summariser is
		// about to read should be the trimmed results, not this round's
		// fresh full ones.
		messages = ElideToolResults(messages, cfg.ToolOutputBudget)
		// The watermark: past four fifths of a declared window, the
		// transcript is summarised by the model itself rather than
		// dropped. On failure the count backstop below still runs.
		if cfg.ContextWindow > 0 && lastPromptTokens*5 >= cfg.ContextWindow*4 {
			if out, before, after, ok := autoCompact(ctx, cfg, messages, lastPromptTokens); ok {
				messages = out
				emit(Event{Kind: EventNote, Text: cli.CompactedNotice("builtin", before, after)})
			}
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
			ContextWindow:   cfg.ContextWindow,
		}
		res, err := streamChat(ctx, scfg, func(delta string) {
			emit(Event{Kind: EventText, TextDelta: delta})
		})
		if err != nil {
			if ctx.Err() != nil {
				return messages, ctx.Err()
			}
			emit(Event{Kind: EventError, Err: err.Error()})
			return messages, err
		}
		if res.PromptTokens > 0 {
			lastPromptTokens = res.PromptTokens
		}
		if res.Content != "" {
			messages = append(messages, ChatMessage{Role: "assistant", Content: res.Content})
		} else if len(res.ToolCalls) > 0 {
			// A pure tool-call turn: the spec wants content absent.
			messages = append(messages, ChatMessage{Role: "assistant", Content: nil})
		}
		if len(res.ToolCalls) == 0 {
			emit(Event{Kind: EventDone})
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
			emit(Event{Kind: EventToolStart, ToolCall: call})
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
			emit(Event{Kind: EventToolEnd, ToolCall: call, Output: out, Exit: exit})
			messages = append(messages, ChatMessage{
				Role:       "tool",
				Content:    out,
				ToolCallID: call.ID,
			})
		}
	}
	emit(Event{Kind: EventNote, Text: fmt.Sprintf(
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
	actx, cancel := harness.ApprovalContext(ctx, timeout)
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
		reason := harness.ApprovalReason(actx)
		return &DenialError{Tool: t.Name, Reason: reason}
	}
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

// DefaultToolOutputBudget is the transcript's byte budget for tool
// results: roughly 30k tokens of output a task may consult, before the
// oldest results start giving way.
const DefaultToolOutputBudget = 128 << 10

// ElideToolResults keeps the transcript's tool results inside a byte
// budget by replacing the oldest ones with a one-line placeholder — the
// layer between per-result trimming (every tool bounds what it emits)
// and dropping whole turns (CompactHistory). A 32 KB output a task asked
// for ten rounds ago has been read and acted on; what it costs from here
// on is context, not information.
//
// The most recent messages are never touched: the model may still be
// working from them, and an elided result it asked for one round ago is
// a wall where a fact should be. Their protection is absolute — when the
// window alone outweighs the budget, the budget yields rather than the
// window opening. Pairing stays intact — the message and its
// tool_call_id remain, only the body shrinks — so both wire formats keep
// a valid exchange.
//
// The elision is part of the model-facing transcript and persists with
// it: what the screen shows comes from the events, which keep their own
// trimmed copies at emit time.
func ElideToolResults(msgs []ChatMessage, budget int) []ChatMessage {
	if budget <= 0 {
		budget = DefaultToolOutputBudget
	}
	const keepRecent = 10 // the current exchange and a margin
	var held int
	for i := range msgs {
		if msgs[i].Role == "tool" {
			held += len(toolContent(msgs[i]))
		}
	}
	if held <= budget {
		return msgs
	}
	limit := len(msgs) - keepRecent
	for i := 0; i < limit && held > budget; i++ {
		m := &msgs[i]
		body, ok := m.Content.(string)
		if m.Role != "tool" || !ok || body == "" {
			// Tool results are strings in this loop; a content the
			// wire sent as something else is not ours to reshape.
			continue
		}
		held -= len(body)
		m.Content = fmt.Sprintf("[output elided: %d bytes]", len(body))
		held += len(toolContent(*m))
	}
	return msgs
}

// toolContent reports the bytes a message's content occupies when
// Content is a string, which every tool result in this loop is.
func toolContent(m ChatMessage) string {
	s, _ := m.Content.(string)
	return s
}

// CompactedPrefix marks the summary message autoCompact leaves behind:
// the one seam a regenerate can trust after the log has been re-indexed.
const CompactedPrefix = "[context compacted"

// compactTailSize is how many recent messages a summarised transcript
// keeps verbatim: the current exchange and enough of the stretch before
// it for the model to land back on its feet. It matches ElideToolResults'
// keepRecent, so "recent" means the same thing to both layers — a fresh
// summary's tail is never placeholder-ized in the same round.
const compactTailSize = 10

// summarizerPrompt asks for a continuation summary, written for another
// instance of the same agent — the summary replaces everything it stands
// in for, so completeness is the instruction that matters.
const summarizerPrompt = `You summarise a coding-agent transcript so the work can continue in a fresh context. Write for another instance of the same agent: the user's goal, the decisions made, the files changed and the commands run with their outcomes, the current state, and what remains. Be factual and complete — your summary replaces everything it stands in for.`

// autoCompact is the summarising pass the watermark arms — Claude Code's
// auto-compaction shape: summarise, don't drop, so a task that outgrew
// its window keeps its goal and history in condensed form. The system
// head and the recent tail survive verbatim; everything between becomes
// one user-role summary message.
//
// It returns the new transcript with before/after prompt-token estimates
// for the compaction note. ok=false means the summarising call failed or
// had too little to work with; the caller falls back to dropping turns
// (CompactHistory) — a failed compaction must never fail the turn.
func autoCompact(ctx context.Context, cfg LoopConfig, msgs []ChatMessage, lastPromptTokens int) (out []ChatMessage, before, after int, ok bool) {
	head := 0
	for head < len(msgs) && msgs[head].Role == "system" {
		head++
	}
	tailStart := len(msgs) - compactTailSize
	if tailStart < head {
		tailStart = head
	}
	// The one boundary that matters: the kept window must not start on a
	// tool result. A result whose call is summarised away is an invalid
	// exchange on both wires — the next request would be refused, and
	// the broken transcript is the one that gets stored. Starting on an
	// assistant message (with or without calls) is fine: its results
	// follow it into the tail. Walking to a user turn instead would
	// strand the single-prompt task — the long agentic run this pass
	// exists for — unable to compact at all.
	middle := msgs[head:tailStart]
	if len(middle) < 4 {
		// Too little to be worth a call; the count backstop handles it.
		return nil, 0, 0, false
	}
	var b strings.Builder
	for _, m := range middle {
		text, _ := m.Content.(string)
		switch m.Role {
		case "user":
			b.WriteString("user: " + text + "\n")
		case "assistant":
			for _, tc := range m.ToolCalls {
				// The call's arguments are what the summary's facts are
				// made of — which command ran, which file changed. The
				// result alone never says. Redact before truncating, as
				// everywhere an argument reaches a durable surface.
				b.WriteString("assistant called " + tc.Function.Name + " " +
					cli.Trunc(cli.Redact(tc.Function.Arguments), 200) + "\n")
			}
			if text != "" {
				b.WriteString("assistant: " + text + "\n")
			}
		case "tool":
			b.WriteString("tool result: " + text + "\n")
		}
	}
	// The summarising call must itself fit the window: cap the input at
	// three quarters of it (bytes run about four to a token) and keep
	// head and tail of the middle, so an over-cap input degrades to a
	// summary of what it saw instead of a doomed request.
	text := b.String()
	if capBytes := cfg.ContextWindow * 3; capBytes > 0 && len(text) > capBytes {
		text = cli.TrimOutput(text, capBytes)
	}
	scfg := StreamConfig{
		BaseURL:       cfg.BaseURL,
		APIKey:        cfg.APIKey,
		Model:         cfg.Model,
		Wire:          cfg.Wire,
		ContextWindow: cfg.ContextWindow,
		Messages: []ChatMessage{
			{Role: "system", Content: summarizerPrompt},
			{Role: "user", Content: text},
		},
	}
	res, err := streamChat(ctx, scfg, func(string) {})
	if err != nil || strings.TrimSpace(res.Content) == "" {
		return nil, 0, 0, false
	}
	out = append(out, msgs[:head]...)
	out = append(out, ChatMessage{Role: "user", Content: CompactedPrefix +
		" — the earlier conversation is summarised below; the recent messages follow verbatim]\n\n" +
		strings.TrimSpace(res.Content)})
	out = append(out, msgs[tailStart:]...)
	return out, lastPromptTokens, estimateTokens(out), true
}

// estimateTokens is the bytes-to-tokens shorthand the compaction note
// uses for the after side: the before side is the provider's own count,
// and the two origins show — the after is an estimate, the before a
// measurement.
func estimateTokens(msgs []ChatMessage) int {
	var n int
	for _, m := range msgs {
		n += len(toolContent(m))
	}
	return n / 4
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
