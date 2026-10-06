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

// dispatch hands a turn to the thread's own agent in the background.
// The host owns the run's context (stopThread cancels it) and its end:
// adapters stream events and return, they never touch thread state.
func (a *app) dispatch(th *Thread, prompt string, at int) {
	a.dispatchParticipant(th, prompt, at, nil)
}

// dispatchParticipant is dispatch with an explicit agent — a panel
// relay's member (spec/agents.md); nil means the thread's own agent.
func (a *app) dispatchParticipant(th *Thread, prompt string, at int, ag *Agent) {
	plan := a.planTurnFor(th, prompt, ag)
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

// agentOverlay is one agent profile resolved over the app-level
// defaults: the arithmetic planTurn applies to a thread and the delegate
// tool applies to a sub-run (spec/agents.md). An empty field on the
// profile inherits the app's selection; a set one overrides it.
type agentOverlay struct {
	backend      string
	providerID   string
	model        string
	mode         int
	effort       int
	maxTurns     int
	rules        harness.Rules
	endpoint     *harness.Endpoint
	mcpServers   []builtin.MCPServer
	toolEnabled  map[string]bool
	skills       harness.SkillSelection
	systemPrompt string
}

// resolveAgent computes the overlay for one configured agent. Nil
// resolves to the pure app defaults.
func (a *app) resolveAgent(ag *Agent) agentOverlay {
	ov := agentOverlay{
		backend: a.backend, providerID: a.providerID, model: a.model,
		mode: a.mode, effort: a.effort, maxTurns: a.maxTurns,
		rules: a.permRules,
	}
	if ag == nil {
		return ov
	}
	if ag.Backend != "" {
		ov.backend = ag.Backend
	}
	if ag.Provider != "" {
		ov.providerID = ag.Provider
	}
	if ag.Model != "" {
		ov.model = ag.Model
	}
	if ag.Mode != nil {
		ov.mode = *ag.Mode
	}
	if ag.Effort != nil {
		ov.effort = *ag.Effort
	}
	if ag.MaxTurns > 0 {
		ov.maxTurns = ag.MaxTurns
	}
	// The agent's permission rules layer over the global ones: the agent
	// is the more specific grant and wins (spec/permissions.md).
	if len(ag.Tools.Rules) > 0 {
		rules := make(harness.Rules, len(a.permRules)+len(ag.Tools.Rules))
		maps.Copy(rules, a.permRules)
		for sel, perm := range ag.Tools.Rules {
			rules[sel] = harness.PermissionFromConfig(perm)
		}
		ov.rules = rules
	}
	// The resolved provider rides the Endpoint: the built-in loop reads
	// its connection from there, the codex adapter maps it onto the
	// model_providers override. A CLI provider (no base URL) leaves it
	// nil — the CLI's own sign-in applies.
	if p := a.providerByID(ov.providerID); p != nil && p.BaseURL != "" {
		ov.endpoint = &harness.Endpoint{
			ID: p.ID, Name: p.Name, BaseURL: p.BaseURL, APIKey: p.APIKey,
			Wire: p.Wire, ContextWindow: p.ContextWindow,
		}
	}
	// MCP: the agent names a subset of the effective servers; an empty
	// list mounts all of them (spec/agents.md).
	ov.mcpServers = a.effectiveMCPServers()
	if len(ag.MCPServers) > 0 {
		ov.mcpServers = slices.DeleteFunc(ov.mcpServers, func(s builtin.MCPServer) bool {
			return !slices.Contains(ag.MCPServers, s.Name)
		})
	}
	// Tools: the disabled registry names become explicit "off" entries;
	// absent stays on.
	if len(ag.Tools.Disabled) > 0 {
		ov.toolEnabled = make(map[string]bool, len(ag.Tools.Disabled))
		for _, name := range ag.Tools.Disabled {
			ov.toolEnabled[name] = false
		}
	}
	// The allow/deny lists select; every other mode discovers as usual.
	if ag.Skills.Mode == "custom" || len(ag.Skills.Allow) > 0 || len(ag.Skills.Deny) > 0 {
		ov.skills = harness.SkillSelection{Allow: ag.Skills.Allow, Deny: ag.Skills.Deny}
	}
	ov.systemPrompt = ag.SystemPrompt
	return ov
}

// panelFor resolves an agent's group relay: the configured member
// names in order, resolved to live profiles. Unknown names and the
// agent itself are skipped (spec/agents.md).
func (a *app) panelFor(ag *Agent) []*Agent {
	if ag == nil || len(ag.Panel) == 0 {
		return nil
	}
	var out []*Agent
	for _, name := range ag.Panel {
		member := a.agentByName(name)
		if member == nil || member.ID == ag.ID {
			continue
		}
		out = append(out, member)
	}
	return out
}

// planTurn assembles the turn from the thread's agent over the
// app-level defaults. Everything is snapshotted here, on the main
// thread — the snapshot, not a live reference: config may change
// mid-run, and a run that changed its endpoint under itself would be a
// different agent than the one the approval cards were about.
func (a *app) planTurn(th *Thread, prompt string) turnPlan {
	return a.planTurnFor(th, prompt, nil)
}

// planTurnFor is planTurn with an explicit agent: a panel participant's
// turn resolves that participant's profile, not the thread's binding.
// nil resolves the thread's own agent.
func (a *app) planTurnFor(th *Thread, prompt string, ag *Agent) turnPlan {
	if ag == nil {
		ag = a.agentFor(th)
		// A thread from before the agents block picks up its binding here;
		// the next save persists it (spec/data.md, agent_id).
		if th.AgentID == "" && ag != nil {
			th.AgentID = ag.ID
		}
	}
	ov := a.resolveAgent(ag)
	// Clamp at the boundary: mode and effort index arrays and flag lists
	// inside the adapters, and there is no recover() anywhere in the app.
	mode := clampMode(ov.mode)
	effort := clampInt(ov.effort, 0, 2)
	var sb harness.Sandbox
	if harness.Mode(mode) == harness.ModeAgent {
		sb = sandboxProvider()
	}
	key := harness.MemoryKey(th.ProjectID, th.ID)
	turn := harness.Turn{
		Prompt:   prompt,
		Workdir:  a.workdir,
		Mode:     harness.Mode(mode),
		Rules:    ov.rules,
		Model:    ov.model,
		Effort:   effort,
		MaxTurns: ov.maxTurns,
		SessionID: map[string]string{
			"codex": th.CodexID, "claude": th.ClaudeID, "pi": th.PiID,
		}[ov.backend],
		Sandbox:      sb,
		Memory:       threadMemory{a: a},
		MemoryKey:    key,
		Endpoint:     ov.endpoint,
		MCPServers:   ov.mcpServers,
		ToolEnabled:  ov.toolEnabled,
		Skills:       ov.skills,
		SystemPrompt: ov.systemPrompt,
	}
	return turnPlan{turn: turn, backend: ov.backend}
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

	// delegates are the other configured agents, fully resolved here on
	// the main thread (spec/agents.md, P6): the delegate tool's Execute
	// runs on the loop goroutine and must not touch live state.
	delegates []delegateTarget

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
	// The delegate candidates: everyone but this thread's own agent.
	// Read-only denies the action, so none are offered there.
	if turn.Mode != harness.ModeReadOnly && len(a.agents) > 1 {
		for i := range a.agents {
			if a.agents[i].ID == th.AgentID {
				continue
			}
			h.delegates = append(h.delegates, delegateTarget{
				id: a.agents[i].ID, name: a.agents[i].Name,
				ov: a.resolveAgent(&a.agents[i]),
			})
		}
	}
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
