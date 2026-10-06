package app

import (
	"context"
	"errors"
	"maps"
	"slices"
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

// newHarness builds the adapter for the turn's backend, bound to one
// turn of one thread. The backend is the turn plan's resolution — the
// thread's agent's choice, else the app's default — never read from
// live state here.
func (a *app) newHarness(backend string, th *Thread, turn harness.Turn) harness.Harness {
	switch backend {
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
	plan := a.planTurn(th, prompt)
	turn := plan.turn
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
	h := a.newHarness(plan.backend, th, turn)
	ctx, cancel := context.WithCancel(context.Background())
	a.runStart(th.ID, cancel)
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

// turnPlan is one dispatch's snapshot: the turn and the backend that
// will run it (spec/agents.md — the thread's agent assembles it).
type turnPlan struct {
	turn    harness.Turn
	backend string
}

// planTurn assembles the turn from the thread's agent over the
// app-level defaults: an empty field on the agent inherits the app's
// selection, a set one overrides it (spec/agents.md). Everything is
// snapshotted here, on the main thread — the snapshot, not a live
// reference: config may change mid-run, and a run that changed its
// endpoint under itself would be a different agent than the one the
// approval cards were about.
func (a *app) planTurn(th *Thread, prompt string) turnPlan {
	ag := a.agentFor(th)
	// A thread from before the agents block picks up its binding here;
	// the next save persists it (spec/data.md, agent_id).
	if th.AgentID == "" && ag != nil {
		th.AgentID = ag.ID
	}
	backend, providerID, model := a.backend, a.providerID, a.model
	mode, effort, maxTurns := a.mode, a.effort, a.maxTurns
	if ag != nil {
		if ag.Backend != "" {
			backend = ag.Backend
		}
		if ag.Provider != "" {
			providerID = ag.Provider
		}
		if ag.Model != "" {
			model = ag.Model
		}
		if ag.Mode != nil {
			mode = *ag.Mode
		}
		if ag.Effort != nil {
			effort = *ag.Effort
		}
		if ag.MaxTurns > 0 {
			maxTurns = ag.MaxTurns
		}
	}
	// Clamp at the boundary: mode and effort index arrays and flag lists
	// inside the adapters, and there is no recover() anywhere in the app.
	mode = clampMode(mode)
	effort = clampInt(effort, 0, 2)
	var sb harness.Sandbox
	if harness.Mode(mode) == harness.ModeAgent {
		sb = sandboxProvider()
	}
	key := harness.MemoryKey(th.ProjectID, th.ID)
	turn := harness.Turn{
		Prompt:   prompt,
		Workdir:  a.workdir,
		Mode:     harness.Mode(mode),
		Model:    model,
		Effort:   effort,
		MaxTurns: maxTurns,
		SessionID: map[string]string{
			"codex": th.CodexID, "claude": th.ClaudeID, "pi": th.PiID,
		}[backend],
		Sandbox:   sb,
		Memory:    threadMemory{a: a},
		MemoryKey: key,
	}
	// The agent's permission rules layer over the global ones: the agent
	// is the more specific grant and wins (spec/permissions.md).
	rules := a.permRules
	if ag != nil && len(ag.Tools.Rules) > 0 {
		rules = make(harness.Rules, len(a.permRules)+len(ag.Tools.Rules))
		maps.Copy(rules, a.permRules)
		for sel, perm := range ag.Tools.Rules {
			rules[sel] = harness.PermissionFromConfig(perm)
		}
	}
	turn.Rules = rules
	// The resolved provider rides the Endpoint: the built-in loop reads
	// its connection from there, the codex adapter maps it onto the
	// model_providers override. A CLI provider (no base URL) leaves it
	// nil — the CLI's own sign-in applies.
	if p := a.providerByID(providerID); p != nil && p.BaseURL != "" {
		turn.Endpoint = &harness.Endpoint{
			ID: p.ID, Name: p.Name, BaseURL: p.BaseURL, APIKey: p.APIKey,
			Wire: p.Wire, ContextWindow: p.ContextWindow,
		}
	}
	if ag != nil {
		// MCP: the agent names a subset of the effective servers; an
		// empty list mounts all of them (spec/agents.md).
		servers := a.effectiveMCPServers()
		if len(ag.MCPServers) > 0 {
			servers = slices.DeleteFunc(servers, func(s builtin.MCPServer) bool {
				return !slices.Contains(ag.MCPServers, s.Name)
			})
		}
		turn.MCPServers = servers
		// Tools: the disabled registry names become explicit "off"
		// entries; absent stays on.
		if len(ag.Tools.Disabled) > 0 {
			turn.ToolEnabled = make(map[string]bool, len(ag.Tools.Disabled))
			for _, name := range ag.Tools.Disabled {
				turn.ToolEnabled[name] = false
			}
		}
		// Skills: the custom mode applies the allow/deny lists; every
		// other mode discovers as usual.
		if ag.Skills.Mode == "custom" {
			turn.Skills = harness.SkillSelection{Allow: ag.Skills.Allow, Deny: ag.Skills.Deny}
		}
		turn.SystemPrompt = ag.SystemPrompt
	}
	return turnPlan{turn: turn, backend: backend}
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
	// The provider and the MCP list come off the turn, not off the live
	// app: planTurn resolved them from the thread's agent, and a run
	// that changed its endpoint under itself would be a different agent
	// than the one the approval cards were about. A nil Endpoint is kept
	// as the zero provider: runBuiltin reports the "configure a
	// provider" error rather than dereferencing it.
	if turn.Endpoint != nil {
		h.provider = Provider{
			ID: turn.Endpoint.ID, Name: turn.Endpoint.Name,
			BaseURL: turn.Endpoint.BaseURL, APIKey: turn.Endpoint.APIKey,
			Wire: turn.Endpoint.Wire, ContextWindow: turn.Endpoint.ContextWindow,
		}
	}
	h.mcpServers = turn.MCPServers
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

// runBackend is the test shim: dispatches like send does. The run
// registers itself in dispatch — there is no separate flag to set.
func runBackend(a *app, th *Thread, prompt string, at int) {
	a.dispatch(th, prompt, at)
}
