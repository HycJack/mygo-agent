package app

import (
	"context"
	"errors"
	"time"

	"mygo-agent/internal/harness"
	"mygo-agent/internal/harness/builtin"
	"mygo-agent/internal/harness/claude"
	"mygo-agent/internal/harness/codex"
	"mygo-agent/internal/harness/pi"
	"mygo-agent/internal/providers/sandbox"
)

// sandboxProvider returns the platform sandbox for agent mode.
func sandboxProvider() harness.Sandbox { return sandbox.New() }

// The harness adapters (spec/architecture.md). The CLI ones live in
// internal/harness/{claude,codex,pi}; this file binds them to the host:
// it snapshots the turn, streams events into the projector, owns the
// run's context and settles the turn when an adapter returns.

// newHarness builds the adapter for the configured backend, bound to one
// turn of one thread.
func (a *app) newHarness(th *Thread, turn harness.Turn) harness.Harness {
	switch a.backend {
	case "builtin":
		return newBuiltinHarness(a, th, turn)
	case "claude":
		if a.claudePath != "" {
			return claude.New(a.claudePath)
		}
	case "codex":
		if a.codexPath != "" {
			return codex.New(a.codexPath)
		}
	case "pi":
		if a.piPath != "" {
			return pi.New(a.piPath)
		}
	}
	// Unknown backend or a CLI that is not installed: the built-in agent
	// always runs. It fails visibly when no provider is configured.
	return newBuiltinHarness(a, th, turn)
}

// dispatch hands a turn to the selected harness in the background. The
// host owns the run's context (stop() cancels it) and its end: adapters
// stream events and return, they never touch thread state themselves.
func (a *app) dispatch(th *Thread, prompt string, at int) {
	turn := a.turnFor(th, prompt)
	turn.OnApproval = func(ctx context.Context, req harness.ApprovalRequest) harness.ApprovalDecision {
		return a.waitForApproval(ctx, th, at, req)
	}
	// A prompt naming a path outside the workspace asks on the same card
	// every other permission does: a CLI's path boundary is decided below
	// the tool-permission layer, so without this the user never sees the
	// question and the model just hears that nothing was granted.
	turn.OnOutsideDir = func(ctx context.Context, req harness.OutsideDirRequest) bool {
		return a.waitForOutsideDirs(ctx, th, at, req)
	}
	h := a.newHarness(th, turn)
	ctx, cancel := context.WithCancel(context.Background())
	a.setCancel(cancel)
	go func() {
		err := h.Run(ctx, turn, func(ev harness.Event) { a.applyEvent(th, at, h.Kind(), ev) })
		a.finish(th, at, turnErrText(err))
	}()
}

// turnErrText maps an adapter's error to the reply card's text: a
// stopped run keeps whatever arrived, a failed one shows the reason.
func turnErrText(err error) string {
	if err == nil || errors.Is(err, context.Canceled) {
		return ""
	}
	return err.Error()
}

// turnFor snapshots everything a harness needs from the Host (the
// snapshot, not a live reference: config may change mid-run).
func (a *app) turnFor(th *Thread, prompt string) harness.Turn {
	// Clamp at the boundary: mode and effort index arrays and flag lists
	// inside the adapters, and there is no recover() anywhere in the app.
	mode := harness.Mode(clampMode(a.mode))
	var sb harness.Sandbox
	if mode == harness.ModeAgent {
		sb = sandboxProvider()
	}
	key := harness.MemoryKey(th.ProjectID, th.ID)
	turn := harness.Turn{
		Prompt:   prompt,
		Workdir:  a.workdir,
		Mode:     mode,
		Rules:    a.permRules,
		Model:    a.model,
		Effort:   clampInt(a.effort, 0, 2),
		MaxTurns: a.maxTurns,
		SessionID: map[string]string{
			"codex": th.CodexID, "claude": th.ClaudeID, "pi": th.PiID,
		}[a.backend],
		Sandbox:   sb,
		Memory:    threadMemory{a: a},
		MemoryKey: key,
	}
	if p := a.provider(); p != nil && p.ID != "codex" && p.BaseURL != "" {
		turn.Endpoint = &harness.Endpoint{ID: p.ID, Name: p.Name, BaseURL: p.BaseURL, APIKey: p.APIKey, Wire: p.Wire}
	}
	return turn
}

// builtinHarness binds the built-in loop to one turn of one thread.
//
// The config it needs is snapshotted here, on the main thread, rather
// than read from the live app while the turn runs: the user can change
// the provider or the MCP list mid-turn, and a run that changed its
// endpoint under itself would be a different agent than the one the
// approval cards were about.
type builtinHarness struct {
	a    *app
	th   *Thread
	turn harness.Turn

	provider      Provider
	mcpServers    []builtin.MCPServer
	approvalLimit time.Duration

	// at is the index of the running reply this turn writes into, and
	// priorMessages is the visible history to seed a fresh transcript
	// from. Both are captured here, on the main thread, because the turn
	// runs on another goroutine that must not read the live thread.
	at            int
	priorMessages []Message
}

func newBuiltinHarness(a *app, th *Thread, turn harness.Turn) builtinHarness {
	h := builtinHarness{a: a, th: th, turn: turn, at: -1}
	// A nil provider is kept as the zero value: runBuiltin reports the
	// "configure a provider" error rather than dereferencing it.
	if p := a.provider(); p != nil {
		h.provider = *p
	}
	h.mcpServers = a.effectiveMCPServers()
	h.approvalLimit = a.approvalTimeout
	for i := range th.Messages {
		if th.Messages[i].Running {
			h.at = i
		}
	}
	if h.at < 0 {
		h.at = len(th.Messages) - 1
	}
	// Everything before this turn's own user message: the seed log is
	// built from that history and then the prompt is appended, so the
	// message at at-1 must be excluded or the prompt lands twice. The
	// texts are copied because the main thread keeps editing the thread.
	if n := max(h.at-1, 0); n > 0 {
		h.priorMessages = make([]Message, n)
		copy(h.priorMessages, th.Messages[:n])
	}
	return h
}

func (h builtinHarness) Kind() string { return "builtin" }

func (h builtinHarness) Run(ctx context.Context, turn harness.Turn, emit func(harness.Event)) error {
	return h.runBuiltin(ctx, emit)
}

// runBackend is the test shim: dispatches like send does — running flag
// included — without touching the composer.
func runBackend(a *app, th *Thread, prompt string, at int) {
	a.update(func() { a.running, a.runningID = true, th.ID })
	a.dispatch(th, prompt, at)
}
