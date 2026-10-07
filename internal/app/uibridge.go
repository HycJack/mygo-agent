package app

import (
	"fmt"
	"strings"

	uipkg "mygo-agent/internal/ui"

	"github.com/egoist/mygo/ui"
)

// The bridge between the Host and the shared views in internal/ui
// (spec/architecture.md): the app fills ViewModels from its state and
// implements Actions; the views never touch app state directly.

// md returns the shared markdown cache, building it on first use.
func (a *app) md() *uipkg.MdCache {
	if a.mdCache == nil {
		a.mdCache = uipkg.NewMdCache()
	}
	return a.mdCache
}

// homeViewModel returns the persistent ViewModel both composers bind
// to, refreshing the host-owned fields in place. vm.Draft is the single
// binding target — the text area writes keystrokes into it and syncVM
// mirrors it into a.draft at frame end — so typing survives rebuilds
// (the per-frame-rebuild bug) and the home and thread composers share
// one draft.
func (a *app) homeViewModel() *uipkg.ViewModel {
	if a.vm == nil {
		a.vm = &uipkg.ViewModel{}
	}
	vm := a.vm
	vm.Running = a.currentRunning()
	vm.CanInterject = false
	if vm.Running {
		if th := a.currentThread(); th != nil && a.groupQueue[th.ID] != nil &&
			a.relayRouteMode(th) == "router" {
			vm.CanInterject = true
		}
	}
	vm.FocusComposer = a.focusComposer
	vm.Mode = a.mode
	vm.Effort = a.effort
	vm.SettingsSel = a.settingsSel
	vm.SettingsOpen = a.settingsOpen
	vm.ModelMenu = a.modelMenu
	vm.AgentMenu = a.agentMenu
	vm.Pal = a.pal
	// The agent the composer shows: the current thread's binding on a
	// thread, the active selection on the home screen (spec/agents.md).
	agID := a.activeAgentID()
	if th := a.currentThread(); th != nil && th.AgentID != "" {
		agID = th.AgentID
	}
	vm.AgentID = agID
	vm.AgentName = agID
	if ag := a.agentByID(agID); ag != nil {
		vm.AgentName = strings.TrimSpace(ag.Emoji + " " + ag.Name)
	}
	// The model line names what the next turn will actually run: the
	// bound agent's override when it has one (spec/agents.md).
	// The model line resolves thread > agent > app — the same order
	// planTurn uses — and the picker groups by the backend that order
	// resolves to (spec/agents.md).
	th := a.currentThread()
	backend, providerID, model := a.backend, a.providerID, a.model
	if ag := a.agentByID(agID); ag != nil {
		ov := a.resolveAgent(ag)
		backend, providerID, model = ov.backend, ov.providerID, ov.model
	}
	if th != nil {
		if th.Provider != "" {
			providerID = th.Provider
		}
		if th.Model != "" {
			model = th.Model
		}
	}
	vm.Model, vm.ProviderID = model, providerID
	// The picker groups by provider, showing every configured endpoint's
	// models first — builtin runs them in process and codex runs them
	// through its model_providers override — then the effective CLI
	// backend's own table when there is one (spec/agents.md).
	vm.Providers = vm.Providers[:0]
	for i := range a.providers {
		p := &a.providers[i]
		if p.BaseURL == "" {
			continue // a CLI pseudo-provider, not a real endpoint
		}
		vm.Providers = append(vm.Providers, uipkg.ProviderVM{ID: p.ID, Name: p.Name, Models: p.Models})
	}
	switch backend {
	case "codex":
		vm.Providers = append(vm.Providers, uipkg.ProviderVM{ID: "codex", Name: "Codex CLI", Models: defaultModels})
	case "claude":
		vm.Providers = append(vm.Providers, uipkg.ProviderVM{ID: "claude", Name: "Claude Code", Models: claudeModels})
	}
	vm.Agents = vm.Agents[:0]
	for i := range a.agents {
		ag := &a.agents[i]
		vm.Agents = append(vm.Agents, uipkg.AgentVM{ID: ag.ID, Name: ag.Name, Emoji: ag.Emoji, Sub: a.agentSub(ag)})
	}
	return vm
}

// agentSub is the picker row's subtitle: the backend and model the
// agent resolves to, which is the app's when the profile leaves them
// empty.
func (a *app) agentSub(ag *Agent) string {
	if n := len(a.panelFor(ag)); n > 0 {
		return fmt.Sprintf("panel · %d agents", n)
	}
	backend, model := ag.Backend, ag.Model
	if backend == "" {
		backend = a.backend
	}
	if model == "" {
		model = a.model
	}
	return backend + " · " + model
}

// uiVM publishes the snapshot to the bridge and returns the same pointer:
// views mutate the snapshot's transient bits (FocusComposer, ModelMenu)
// in place, and syncVM copies them back to host state before the next
// snapshot is built.
func (a *app) uiVM(vm *uipkg.ViewModel) *uipkg.ViewModel { a.vm = vm; return vm }

// syncVM mirrors the view-owned bits of the ViewModel into host state;
// called after the frame that rendered it. One direction only: vm.Draft
// is the binding the text area writes into.
func (a *app) syncVM() {
	if a.vm == nil {
		return
	}
	a.focusComposer = a.vm.FocusComposer
	a.modelMenu = a.vm.ModelMenu
	a.agentMenu = a.vm.AgentMenu
	a.draft = a.vm.Draft
}

// setDraft sets the composer text everywhere: the bound ViewModel for
// the current frame, the host mirror for the logic.
func (a *app) setDraft(s string) {
	if a.vm != nil {
		a.vm.Draft = s
	}
	a.draft = s
}

// homeActions adapts *app to ui.Actions for the home surface.
type homeActions struct{ a *app }

func (h homeActions) Send() { h.a.send() }
func (h homeActions) Stop() { h.a.stopThread(h.a.current) }

// SetDraft fills the composer (the suggestion chips): the bound
// ViewModel immediately, the host mirror for the frame.
func (h homeActions) SetDraft(s string) { h.a.setDraft(s) }
func (h homeActions) SetMode(m int)     { h.a.mode = clampMode(m); h.a.saveConfig() }
func (h homeActions) SetEffort(e int) {
	h.a.effort = clampInt(e, 0, 2)
	h.a.saveConfig()
}

// PickModel writes the model the way the composer displays it: on the
// current thread (spec/agents.md — the override lives with the task,
// not the agent profile), or onto the app defaults from the home
// screen. A CLI backend id as the provider means the CLI's own model
// table was picked, which also selects that backend.
func (h homeActions) PickModel(pid, m string) {
	switch pid {
	case "codex", "claude", "pi":
		h.a.backend = pid
		h.a.model = m
		if th := h.a.currentThread(); th != nil {
			th.Provider, th.Model = "", m
			h.a.saveThread(th)
		}
		h.a.saveConfig()
		return
	}
	if th := h.a.currentThread(); th != nil {
		th.Provider, th.Model = pid, m
		h.a.saveThread(th)
		return
	}
	h.a.providerID, h.a.model = pid, m
	h.a.saveConfig()
}

// SetAgent binds the agent: the composer's selection now, and the
// current thread's binding from its next turn. A profile with its own
// default approval mode seeds the mode the way starting a task with it
// would (spec/agents.md).
func (h homeActions) SetAgent(id string) { h.a.setActiveAgent(id) }

// NewGroup opens the new-group-chat dialog: name it, tick the members,
// start — the chat begins before any settings visit (spec/agents.md).
func (h homeActions) NewGroup() { h.a.openGroupDraft() }

// setActiveAgent is the host half of the picker and the home launcher.
func (a *app) setActiveAgent(id string) {
	ag := a.agentByID(id)
	if ag == nil {
		return
	}
	a.activeAgent = ag.ID
	if ag.Mode != nil {
		a.mode = clampMode(*ag.Mode)
	}
	if th := a.currentThread(); th != nil && th.AgentID != ag.ID {
		th.AgentID = ag.ID
		a.saveThread(th)
	}
}
func (h homeActions) OpenSettings(sel string) {
	if sel != "" {
		h.a.settingsSel = sel
	} else if h.a.settingsSel == "" {
		h.a.settingsSel = h.a.providerID
	}
	h.a.settingsOpen = true
}
func (h homeActions) SaveConfig() { h.a.saveConfig() }

var _ = ui.KeyEnter // keep the toolkit import while remaining views render inline
