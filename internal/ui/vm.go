// Package ui holds the app's views: ViewModels the host fills from its
// state, the Actions interfaces the views call back through, and the
// views themselves — home, the composer, the sidebar, the workspace
// panel, the thread transcript with its block cards, the file viewer,
// the manage-providers dialog and the window chrome.
//
// Import rule (spec/architecture.md): internal/ui imports mygo's
// toolkit, internal/components and internal/harness value types —
// never internal/app or providers.
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
	CanInterject  bool     // a routed relay is running: a non-empty draft sends as an interjection
	Mentions      []string // @-mention candidates while one is being typed; empty hides the popup
	MentionSel    int      // the highlighted candidate row
	FocusComposer bool     // consumed once: the view clears it after focusing
	Mode          int      // 0 read-only, 1 agent, 2 full
	Effort        int      // 0 low, 1 medium, 2 high
	Model         string
	ProviderID    string
	Providers     []ProviderVM // the model picker's rows

	// The agent the composer shows and new tasks bind (spec/agents.md):
	// the current thread's binding on a thread, the active selection on
	// the home screen.
	AgentID   string
	AgentName string
	Agents    []AgentVM // the agent picker's rows

	ModelMenu    bool // the picker popover's open state
	AgentMenu    bool // the agent popover's open state
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

// AgentVM is one agent's row in the picker and on the home launcher.
type AgentVM struct {
	ID    string
	Name  string
	Emoji string
	Sub   string // backend · model, as resolved for display
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
	// SetAgent binds the agent: new tasks start with it, and on a thread
	// the thread rebinds from the next turn (spec/agents.md).
	SetAgent(id string)
	// NewGroup creates a group profile and opens its form — the visible
	// entry to the relay.
	NewGroup()
	// SaveConfig persists settings after an edit.
	SaveConfig()
}

// Composer renders the input box: the draft area and the row with the
// approval mode, the model picker and send/stop. Enter sends, Shift+Enter
// (via the box's ancestors) breaks the line — same behavior as the host's
// own composer before it moved here.
//
// The horizontal inset is 24, not the 44 the message column uses, and the
// box is allowed wider than that column: a prompt is a single line of text
// you are about to write, while a reply is prose meant to be read, and
// holding both to one measure made the composer the narrower of the two for
// no reason. The cap keeps it a reading-width field on a wide display
// rather than a full-bleed bar.
//
// FillWidth and MaxWidth together are what bound it: MaxWidth alone does
// nothing, because a column with no width of its own is sized by its
// contents, and a child at FillWidth resolves that cycle by taking the
// parent — so the cap never applies. With FillWidth the column claims the
// space, the cap trims it, and Auto margins centre what is left.
func Composer(c *ui.Context, vm *ViewModel, acts Actions) {
	t := c.Theme()
	ui.Column(c).FillWidth().Padding(12, 24, 20).
		MaxWidth(composerMaxWidth).Margin(0, ui.Auto).Children(func() {
		box := ui.Column(c).FillWidth().Padding(10, 14).Gap(8).Radius(16).
			Background(vm.Pal.Card).Border(1, vm.Pal.Border)
		if vm.Running {
			box.Border(1, t.Accent.Alpha(0.5))
		}
		box.Children(func() {
			ta := ui.TextAreaBase(c, &vm.Draft).
				Placeholder("Plan, code, edit anything — @name to call on a panel member").FontSize(14).MinHeight(44)
			if vm.FocusComposer {
				ta.AutoFocus()
				vm.FocusComposer = false
			}
			if len(vm.Mentions) > 0 {
				mentionPopup(c, vm)
			}
			composerRow(c, vm, acts)
		})
		// Enter sends while the composer has the focus — unless the
		// mention popup is open, where Enter completes the highlighted
		// candidate instead (Tab does too; Esc closes without picking).
		if len(vm.Mentions) > 0 {
			if box.Shortcut(0, ui.KeyEnter) || box.Shortcut(0, ui.KeyTab) {
				completeMention(vm, vm.MentionSel)
				return
			}
			if box.Shortcut(0, ui.KeyEscape) {
				vm.Mentions = nil
				return
			}
			if box.Shortcut(0, ui.KeyDown) {
				vm.MentionSel = min(vm.MentionSel+1, len(vm.Mentions)-1)
			}
			if box.Shortcut(0, ui.KeyUp) {
				vm.MentionSel = max(vm.MentionSel-1, 0)
			}
		}
		if box.Shortcut(0, ui.KeyEnter) {
			acts.Send()
		}
		if c.Shortcut(ui.Cmd, ui.KeyEnter) {
			acts.Send()
		}
	})
}

// mentionPopup is the @-mention candidate row: one pill per member,
// highlighted selection, click to complete. The pills sit between the
// text area and the action row, inside the composer's card.
func mentionPopup(c *ui.Context, vm *ViewModel) {
	t := c.Theme()
	ui.Row(c).Gap(6).Wrap().Children(func() {
		for i, name := range vm.Mentions {
			pill := ui.ButtonBase(c).Label("@"+name).Padding(4, 10).Radius(8).
				Cursor(ui.CursorPointer).FontSize(12.5)
			picked := i == vm.MentionSel
			if picked {
				pill.Background(t.Accent.Alpha(0.16)).Border(1, t.Accent.Alpha(0.5))
			} else {
				pill.Background(vm.Pal.Hover)
				if pill.Hovered() {
					pill.Background(t.Accent.Alpha(0.10))
					vm.MentionSel = i
				}
			}
			if pill.Clicked() {
				completeMention(vm, i)
			}
			pill.Children(func() {
				ui.Text(c, "@"+name).FontSize(12.5).TextColor(t.Text)
			})
		}
	})
}

// completeMention replaces the draft's trailing "@query" with the
// picked "@name " and closes the popup. The draft is the view-owned
// binding: the edit lands on it and syncVM mirrors it into host state.
func completeMention(vm *ViewModel, i int) {
	if i < 0 || i >= len(vm.Mentions) {
		return
	}
	vm.Draft = ApplyMention(vm.Draft, vm.Mentions[i])
	vm.Mentions = nil
	vm.MentionSel = 0
}

// composerMaxWidth is how wide the composer may grow. It sits between the
// message column's 880 and the window, so the input reads as the page's
// widest element without becoming a banner across a wide display.
const composerMaxWidth = 1080

// composerRow is the bottom line: approval mode, agent, model, send/stop.
func composerRow(c *ui.Context, vm *ViewModel, acts Actions) {
	t := c.Theme()
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		Segments(c, vm.Mode, ModeNames, acts.SetMode, vm.Pal)
		ui.Spacer(c)
		agentButton(c, vm, acts)
		modelButton(c, vm, acts)
		send := ui.ButtonBase(c).Label("Send").Tooltip("Send (Enter)").Size(30, 30).Radius(999).Center()
		if vm.Running {
			// A routed relay takes the user's words mid-flight: a
			// non-empty draft sends as an interjection, an empty one
			// keeps the stop button (spec/relay-router.md).
			if vm.CanInterject && strings.TrimSpace(vm.Draft) != "" {
				send.Label("Interject").Tooltip("Send into the relay (Enter)")
				send.Background(t.Accent)
				if send.Hovered() {
					send.Background(t.AccentHover)
				}
				if send.Clicked() {
					acts.Send()
				}
				send.Children(func() { ui.Icon(c, IconArrowUp).FontSize(17).TextColor(t.AccentText) })
				return
			}
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
		// Popover's panel already paints the look; more here would read
		// as a second border inside it.
		ui.Column(c).Width(320).Padding(4).Children(func() {
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
				ui.Icon(c, IconGear).FontSize(13).TextColor(t.TextMuted)
				ui.Text(c, "Settings…").FontSize(12).Grow(1)
			})
			Segments(c, vm.Effort, EffortNames, acts.SetEffort, vm.Pal)
			if vm.Effort != prev {
				acts.SaveConfig()
			}
		})
	})
}

// agentButton opens the agent picker: the configured agents, then the
// manager. The selection binds new tasks; on a thread it rebinds the
// thread from its next turn (spec/agents.md).
func agentButton(c *ui.Context, vm *ViewModel, acts Actions) {
	t := c.Theme()
	ab := ui.ButtonBase(c).Tooltip("Agent").Gap(6).Padding(4, 8).Radius(8).Cursor(ui.CursorPointer)
	if ab.Hovered() || vm.AgentMenu {
		ab.Background(vm.Pal.CardHover)
	}
	if ab.Clicked() {
		vm.AgentMenu = !vm.AgentMenu
	}
	ab.Children(func() {
		ui.Icon(c, IconBot).FontSize(12).TextColor(vm.Pal.TextMuted)
		ui.Text(c, vm.AgentName).FontSize(11.5).TextColor(t.TextMuted).SingleLine()
		chev := ui.Icon(c, IconChevDown).FontSize(12).TextColor(vm.Pal.TextMuted)
		if vm.AgentMenu {
			chev.Rotate(180)
		}
	})
	ui.Popover(c, ab, &vm.AgentMenu, func() {
		ui.Column(c).Width(280).Padding(4).Children(func() {
			ui.Text(c, "AGENT").FontSize(10).FontWeight(600).TextColor(vm.Pal.TextMuted).
				Padding(6, 10, 2).LetterSpacing(0.6)
			ui.Scroll(c).MaxHeight(280).Children(func() {
				ui.Column(c).FillWidth().Children(func() {
					for i := range vm.Agents {
						ag := &vm.Agents[i]
						active := ag.ID == vm.AgentID
						row := ui.ButtonBase(c).Padding(6, 10).Radius(7).Gap(8).Cursor(ui.CursorPointer)
						if row.Hovered() {
							row.Background(vm.Pal.CardHover)
						}
						if row.Clicked() {
							acts.SetAgent(ag.ID)
							vm.AgentMenu = false
						}
						row.Children(func() {
							ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
								ui.Text(c, strings.TrimSpace(ag.Emoji+" "+ag.Name)).FontSize(12).SingleLine()
								ui.Text(c, ag.Sub).FontSize(10).TextColor(vm.Pal.TextMuted).SingleLine()
							})
							if active {
								ui.Icon(c, IconCheck).FontSize(13).TextColor(t.Text)
							}
						})
					}
				})
			})
			ui.Box(c).Height(1).Margin(4, 6).Background(vm.Pal.Border)
			mng := ui.ButtonBase(c).Padding(7, 10).Radius(7).Gap(8).Cursor(ui.CursorPointer)
			if mng.Hovered() {
				mng.Background(vm.Pal.CardHover)
			}
			if mng.Clicked() {
				vm.AgentMenu = false
				acts.OpenSettings(vm.AgentID)
			}
			mng.Children(func() {
				ui.Icon(c, IconGear).FontSize(13).TextColor(t.TextMuted)
				ui.Text(c, "Manage agents…").FontSize(12).Grow(1)
			})
		})
	})
}
