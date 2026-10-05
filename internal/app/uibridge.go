package app

import (
	uipkg "mygo-agent/internal/ui"

	"github.com/egoist/mygo/ui"
)

// The bridge between the Host and the shared views in internal/ui
// (spec/architecture.md): the app fills ViewModels from its state and
// implements Actions; the views never touch app state directly.

// homeViewModel snapshots the state ui.Home and ui.Composer render.
func (a *app) homeViewModel() *uipkg.ViewModel {
	vm := &uipkg.ViewModel{
		Draft:         a.draft,
		Running:       a.running,
		FocusComposer: a.focusComposer,
		Mode:          a.mode,
		Effort:        a.effort,
		Model:         a.model,
		ProviderID:    a.providerID,
		SettingsSel:   a.settingsSel,
		SettingsOpen:  a.settingsOpen,
		Pal:           a.pal,
	}
	for i := range a.providers {
		p := &a.providers[i]
		vm.Providers = append(vm.Providers, uipkg.ProviderVM{ID: p.ID, Name: p.Name, Models: p.Models})
	}
	return a.uiVM(vm)
}

// uiVM publishes the snapshot to the bridge and returns the same pointer:
// views mutate the snapshot's transient bits (FocusComposer, ModelMenu)
// in place, and syncVM copies them back to host state before the next
// snapshot is built.
func (a *app) uiVM(vm *uipkg.ViewModel) *uipkg.ViewModel { a.vm = vm; return vm }

// syncVM copies the transient view-owned bits of the last snapshot back
// into host state.
func (a *app) syncVM() {
	if a.vm == nil {
		return
	}
	a.focusComposer = a.vm.FocusComposer
	a.modelMenu = a.vm.ModelMenu
}

// homeActions adapts *app to ui.Actions for the home surface.
type homeActions struct{ a *app }

func (h homeActions) Send()             { h.a.syncFromDraft(); h.a.send() }
func (h homeActions) Stop()             { h.a.stop() }
func (h homeActions) SetDraft(s string) { h.a.draft = s }
func (h homeActions) SetMode(m int)     { h.a.mode = m; h.a.saveConfig() }
func (h homeActions) SetEffort(e int)   { h.a.effort = e; h.a.saveConfig() }
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

// syncFromDraft copies the composer snapshot's draft back before send —
// the TextArea writes into the ViewModel's Draft field.
func (a *app) syncFromDraft() {
	if a.vm != nil {
		a.draft = a.vm.Draft
	}
}

var _ = ui.KeyEnter // keep the toolkit import while remaining views render inline
