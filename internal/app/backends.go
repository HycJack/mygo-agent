package app

import (
	"context"

	"mygo-agent/internal/harness"
	"mygo-agent/internal/providers/sandbox"
)

// sandboxProvider returns the platform sandbox for agent mode.
func sandboxProvider() harness.Sandbox { return sandbox.New() }

// The four harness adapters (spec/architecture.md). Each wraps one
// per-turn runner; they consume the Turn and emit normalized Events, and
// the Host dispatches by Kind without knowing their internals.

// newHarness builds the adapter for the configured backend, bound to one
// turn of one thread.
func (a *app) newHarness(th *Thread, at int, turn harness.Turn) harness.Harness {
	switch a.backend {
	case "builtin":
		return builtinHarness{a: a, th: th, at: at}
	case "claude":
		if a.claudePath != "" {
			return claudeHarness{a: a, th: th, at: at}
		}
	case "codex":
		if a.codexPath != "" {
			return codexHarness{a: a, th: th, at: at}
		}
	}
	return demoHarness{a: a, th: th, at: at}
}

// dispatch hands a turn to the selected harness in the background.
func (a *app) dispatch(th *Thread, prompt string, at int) {
	turn := a.turnFor(th, prompt)
	h := a.newHarness(th, at, turn)
	go func() {
		err := h.Run(context.Background(), turn, func(ev harness.Event) {
			a.applyEvent(th, at, h.Kind(), ev)
		})
		_ = err // the adapters settle their own turns via a.finish
	}()
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
	return harness.Turn{
		Prompt:   prompt,
		Workdir:  a.workdir,
		Mode:     mode,
		Rules:    a.permRules,
		Model:    a.model,
		Effort:   a.effort,
		MaxTurns: a.maxTurns,
		SessionID: map[string]string{
			"codex": th.CodexID, "claude": th.ClaudeID,
		}[a.backend],
		Sandbox:   sb,
		Memory:    threadMemory{a: a},
		MemoryKey: key,
	}
}

type builtinHarness struct {
	a  *app
	th *Thread
	at int
}

func (h builtinHarness) Kind() string { return "builtin" }

func (h builtinHarness) Run(ctx context.Context, turn harness.Turn, emit func(harness.Event)) error {
	h.a.runBuiltin(ctx, h.th, turn, emit)
	return nil
}

type claudeHarness struct {
	a  *app
	th *Thread
	at int
}

func (h claudeHarness) Kind() string { return "claude" }

func (h claudeHarness) Run(ctx context.Context, turn harness.Turn, emit func(harness.Event)) error {
	h.a.runClaude(ctx, h.th, turn, emit)
	return nil
}

type codexHarness struct {
	a  *app
	th *Thread
	at int
}

func (h codexHarness) Kind() string { return "codex" }

func (h codexHarness) Run(ctx context.Context, turn harness.Turn, emit func(harness.Event)) error {
	h.a.runCodex(ctx, h.th, turn, emit)
	return nil
}

type demoHarness struct {
	a  *app
	th *Thread
	at int
}

func (h demoHarness) Kind() string { return "demo" }

func (h demoHarness) Run(ctx context.Context, turn harness.Turn, emit func(harness.Event)) error {
	h.a.runDemo(ctx, h.th, turn, emit)
	return nil
}

// runBackend is the test shim: builds the turn from the app and runs the
// selected backend to completion.
func runBackend(a *app, th *Thread, prompt string, at int) {
	turn := a.turnFor(th, prompt)
	turn.SessionID = map[string]string{"codex": th.CodexID, "claude": th.ClaudeID}[a.backend]
	switch a.backend {
	case "codex":
		go a.runCodex(context.Background(), th, turn, func(ev harness.Event) { a.applyEvent(th, at, "codex", ev) })
	case "claude":
		go a.runClaude(context.Background(), th, turn, func(ev harness.Event) { a.applyEvent(th, at, "claude", ev) })
	case "builtin":
		go a.runBuiltin(context.Background(), th, turn, func(ev harness.Event) { a.applyEvent(th, at, "builtin", ev) })
	default:
		go a.runDemo(context.Background(), th, turn, func(ev harness.Event) { a.applyEvent(th, at, "demo", ev) })
	}
}
