package app

import (
	"context"
	"errors"

	"mygo-agent/internal/harness"
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
		return builtinHarness{a: a, th: th, turn: turn}
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
	return builtinHarness{a: a, th: th, turn: turn}
}

// dispatch hands a turn to the selected harness in the background. The
// host owns the run's context (stop() cancels it) and its end: adapters
// stream events and return, they never touch thread state themselves.
func (a *app) dispatch(th *Thread, prompt string, at int) {
	turn := a.turnFor(th, prompt)
	turn.OnApproval = func(ctx context.Context, req harness.ApprovalRequest) harness.ApprovalDecision {
		return a.waitForApproval(ctx, th, at, req)
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
	mode := harness.Mode(a.mode)
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
		Effort:   a.effort,
		MaxTurns: a.maxTurns,
		SessionID: map[string]string{
			"codex": th.CodexID, "claude": th.ClaudeID, "pi": th.PiID,
		}[a.backend],
		Sandbox:   sb,
		Memory:    threadMemory{a: a},
		MemoryKey: key,
	}
	if p := a.provider(); p != nil && p.ID != "codex" && p.BaseURL != "" {
		turn.Endpoint = &harness.Endpoint{ID: p.ID, Name: p.Name, BaseURL: p.BaseURL, APIKey: p.APIKey}
	}
	return turn
}

type builtinHarness struct {
	a    *app
	th   *Thread
	turn harness.Turn
}

func (h builtinHarness) Kind() string { return "builtin" }

func (h builtinHarness) Run(ctx context.Context, turn harness.Turn, emit func(harness.Event)) error {
	return h.a.runBuiltin(ctx, h.th, turn, emit)
}

// runBackend is the test shim: dispatches like send does — running flag
// included — without touching the composer.
func runBackend(a *app, th *Thread, prompt string, at int) {
	a.update(func() { a.running = true })
	a.dispatch(th, prompt, at)
}
