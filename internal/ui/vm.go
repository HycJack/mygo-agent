// Package ui holds view code shared across app surfaces: ViewModels the
// host fills from its state, the Actions interface the views call back
// through, and the views themselves (home and the composer today; the
// thread, sidebar and workspace migrate as they stabilize).
//
// Import rule (spec/architecture.md): internal/ui imports mygo's toolkit
// and internal/harness value types — never internal/app or providers.
package ui

import (
	"strings"

	"github.com/egoist/mygo/ui"
)

// ViewModel is the render input for the shared views: a snapshot of the
// state they show, plain data the host rebuilds per frame. Nothing here
// can reach back into host state.
type ViewModel struct {
	// Composer state.
	Draft         string
	Running       bool
	FocusComposer bool // consumed once: the view clears it after focusing
	Mode          int  // 0 read-only, 1 agent, 2 full
	Effort        int  // 0 low, 1 medium, 2 high
	Model         string
	ProviderID    string
	Providers     []ProviderVM // the model picker's rows

	ModelMenu    bool // the picker popover's open state
	SettingsSel  string
	SettingsOpen bool

	Pal Palette
}

// ProviderVM is one provider's group in the model picker.
type ProviderVM struct {
	ID     string
	Name   string
	Models []string
}

// Actions is the narrow surface the shared views call back through. The
// host implements it; the views never touch host state directly.
type Actions interface {
	// Send dispatches the draft; Stop cancels the running turn.
	Send()
	Stop()
	// SetDraft edits the composer text.
	SetDraft(string)
	// SetMode sets the approval mode (0/1/2); SetEffort the effort.
	SetMode(int)
	SetEffort(int)
	// PickModel selects a provider/model pair; OpenSettings opens the
	// provider manager at a provider ("" keeps the current selection).
	PickModel(providerID, model string)
	OpenSettings(providerID string)
	// SaveConfig persists settings after an edit.
	SaveConfig()
}

// Composer renders the input box: the draft area and the row with the
// approval mode, the model picker and send/stop. Enter sends, Shift+Enter
// (via the box's ancestors) breaks the line — same behavior as the host's
// own composer before it moved here.
func Composer(c *ui.Context, vm *ViewModel, acts Actions) {
	t := c.Theme()
	ui.Column(c).Padding(12, 44, 20).Children(func() {
		box := ui.Column(c).FillWidth().Padding(10, 14).Gap(8).Radius(16).
			Background(vm.Pal.Card).Border(1, vm.Pal.Border)
		if vm.Running {
			box.Border(1, t.Accent.Alpha(0.5))
		}
		box.Children(func() {
			ta := ui.TextAreaBase(c, &vm.Draft).Placeholder("Plan, code, edit anything").FontSize(14).MinHeight(44)
			if vm.FocusComposer {
				ta.AutoFocus()
				vm.FocusComposer = false
			}
			composerRow(c, vm, acts)
		})
		// Enter sends while the composer has the focus.
		if box.Shortcut(0, ui.KeyEnter) {
			acts.Send()
		}
		if c.Shortcut(ui.Cmd, ui.KeyEnter) {
			acts.Send()
		}
	})
}

// composerRow is the bottom line: approval mode, model, send/stop.
func composerRow(c *ui.Context, vm *ViewModel, acts Actions) {
	t := c.Theme()
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		Segments(c, vm.Mode, ModeNames, acts.SetMode, vm.Pal)
		ui.Spacer(c)
		modelButton(c, vm, acts)
		send := ui.ButtonBase(c).Label("Send").Tooltip("Send (Enter)").Size(30, 30).Radius(999).Center()
		if vm.Running {
			send.Label("Stop").Tooltip("Stop")
			send.Background(t.Text)
			if send.Clicked() {
				acts.Stop()
			}
			send.Children(func() { ui.Icon(c, IconStop).FontSize(14).TextColor(vm.Pal.AccentSub) })
			return
		}
		empty := strings.TrimSpace(vm.Draft) == ""
		send.Background(t.Accent)
		if empty {
			send.Disabled(true).Opacity(0.4)
		} else if send.Hovered() {
			send.Background(t.AccentHover)
		}
		if send.Clicked() {
			acts.Send()
		}
		send.Children(func() { ui.Icon(c, IconArrowUp).FontSize(17).TextColor(t.AccentText) })
	})
}

// modelButton opens the model popover grouped by provider, with the
// reasoning-effort segments beneath it.
func modelButton(c *ui.Context, vm *ViewModel, acts Actions) {
	t := c.Theme()
	prev := vm.Effort
	mb := ui.ButtonBase(c).Label("Model").Tooltip("Model and effort").Gap(6).Padding(4, 8).Radius(8).Cursor(ui.CursorPointer)
	if mb.Hovered() || vm.ModelMenu {
		mb.Background(vm.Pal.CardHover)
	}
	if mb.Clicked() {
		vm.ModelMenu = !vm.ModelMenu
	}
	mb.Children(func() {
		ui.Icon(c, IconSparkles).FontSize(12).TextColor(vm.Pal.TextMuted)
		ui.Text(c, vm.Model).Font("monospace").FontSize(11.5).TextColor(t.TextMuted).SingleLine()
		chev := ui.Icon(c, IconChevDown).FontSize(12).TextColor(vm.Pal.TextMuted)
		if vm.ModelMenu {
			chev.Rotate(180)
		}
	})
	ui.Popover(c, mb, &vm.ModelMenu, func() {
		ui.Column(c).Width(320).Padding(4).Radius(12).Background(vm.Pal.Card).
			Border(1, vm.Pal.Border).Shadow(0, 8, 24, 0, ui.RGBA(0, 0, 0, 0.4)).Children(func() {
			ui.Row(c).Padding(6, 10, 2).Children(func() {
				ui.Text(c, "PROVIDER / MODEL").FontSize(10).FontWeight(600).TextColor(vm.Pal.TextMuted).LetterSpacing(0.6).Grow(1)
				ui.Text(c, "EFFORT").FontSize(10).FontWeight(600).TextColor(vm.Pal.TextMuted).LetterSpacing(0.6)
			})
			ui.Scroll(c).MaxHeight(320).Children(func() {
				ui.Column(c).FillWidth().Children(func() {
					for pi := range vm.Providers {
						p := &vm.Providers[pi]
						ui.Textf(c, "%s", p.Name).FontSize(10).FontWeight(600).TextColor(vm.Pal.TextMuted).
							Padding(8, 10, 2).LetterSpacing(0.4)
						for _, m := range p.Models {
							model, pid := m, p.ID
							active := vm.ProviderID == pid && vm.Model == model
							row := ui.ButtonBase(c).Padding(6, 10).Radius(7).Gap(8).Cursor(ui.CursorPointer)
							if row.Hovered() {
								row.Background(vm.Pal.CardHover)
							}
							if row.Clicked() {
								acts.PickModel(pid, model)
								vm.ModelMenu = false
							}
							row.Children(func() {
								ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
									ui.Text(c, model).Font("monospace").FontSize(12).SingleLine()
									ui.Text(c, p.Name).FontSize(10).TextColor(vm.Pal.TextMuted).SingleLine()
								})
								if active {
									ui.Icon(c, IconCheck).FontSize(13).TextColor(t.Text)
								}
							})
						}
					}
				})
			})
			ui.Box(c).Height(1).Margin(4, 6).Background(vm.Pal.Border)
			mng := ui.ButtonBase(c).Padding(7, 10).Radius(7).Gap(8).Cursor(ui.CursorPointer)
			if mng.Hovered() {
				mng.Background(vm.Pal.CardHover)
			}
			if mng.Clicked() {
				vm.ModelMenu = false
				acts.OpenSettings(vm.SettingsSel)
			}
			mng.Children(func() {
				ui.Icon(c, IconSliders).FontSize(13).TextColor(t.TextMuted)
				ui.Text(c, "Manage providers & models…").FontSize(12).Grow(1)
			})
			Segments(c, vm.Effort, EffortNames, acts.SetEffort, vm.Pal)
			if vm.Effort != prev {
				acts.SaveConfig()
			}
		})
	})
}
