package app

import (
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
	vm.Running = a.running
	vm.FocusComposer = a.focusComposer
	vm.Mode = a.mode
	vm.Effort = a.effort
	vm.Model = a.model
	vm.ProviderID = a.providerID
	vm.SettingsSel = a.settingsSel
	vm.SettingsOpen = a.settingsOpen
	vm.ModelMenu = a.modelMenu
	vm.Pal = a.pal
	vm.Providers = vm.Providers[:0]
	for i := range a.providers {
		p := &a.providers[i]
		vm.Providers = append(vm.Providers, uipkg.ProviderVM{ID: p.ID, Name: p.Name, Models: p.Models})
	}
	return vm
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
func (h homeActions) Stop() { h.a.stop() }

// SetDraft fills the composer (the suggestion chips): the bound
// ViewModel immediately, the host mirror for the frame.
func (h homeActions) SetDraft(s string) { h.a.setDraft(s) }
func (h homeActions) SetMode(m int)     { h.a.mode = clampMode(m); h.a.saveConfig() }
func (h homeActions) SetEffort(e int) {
	h.a.effort = clampInt(e, 0, 2)
	h.a.saveConfig()
}
func (h homeActions) PickModel(pid, m string) {
	h.a.providerID, h.a.model = pid, m
	h.a.saveConfig()
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
