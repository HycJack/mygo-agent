package ui

import "github.com/egoist/mygo/ui"

// Home is the new-task screen: the greeting, the composer in the middle
// and a few suggested prompts. It renders from the ViewModel and talks
// back only through Actions.
//
// The panel is centred in the window, not stacked under the title bar, and
// that is what the outer growing column is for. This used to be a Scroll,
// which cannot do it: a scroll container sizes its content by the content
// and never stretches it to the viewport, so the panel's own
// Justify(Center) had no surplus to distribute and the panel sat at the
// top of an otherwise empty window. A Scroll bought nothing here anyway —
// the panel is a fixed, short composition that never overflowed on any
// window worth having — and when the window is shorter than the panel both
// anchor to the top with the panel's padding intact.
func Home(c *ui.Context, vm *ViewModel, acts Actions, suggestions []string) {
	t := c.Theme()
	ui.Column(c).Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).Fill().Justify(ui.Center).AlignItems(ui.Center).Gap(22).Padding(24).
			MaxWidth(homeMaxWidth).Margin(0, ui.Auto).Children(func() {
			logo := ui.Box(c).Size(56, 56).Radius(16).Background(vm.Pal.Card).Border(1, vm.Pal.Border).Center()
			logo.Children(func() {
				ui.Icon(c, IconSparkles).FontSize(26).TextColor(vm.Pal.Text)
			})
			ui.Text(c, "What are we coding next?").FontSize(25).Bold()
			ui.Text(c, "Your agent runs in the background while you keep working.").FontSize(13).TextColor(t.TextMuted)
			Composer(c, vm, acts)
			// The agent launcher (spec/agents.md): one card per configured
			// agent, the chosen one highlighted. Picking one binds it to
			// the next task — typing straight into the composer still
			// works and binds whatever the picker last chose.
			if len(vm.Agents) > 0 {
				ui.Row(c).Gap(8).Wrap().Justify(ui.Center).Children(func() {
					for i := range vm.Agents {
						ag := &vm.Agents[i]
						active := ag.ID == vm.AgentID
						card := ui.ButtonBase(c).Padding(8, 14).Radius(12).Gap(8).Cursor(ui.CursorPointer)
						if active {
							card.Background(vm.Pal.Sel).Border(1, vm.Pal.Border)
						} else {
							card.Border(1, vm.Pal.Border)
						}
						if card.Hovered() {
							card.Background(vm.Pal.Hover)
						}
						if card.Clicked() {
							acts.SetAgent(ag.ID)
							vm.FocusComposer = true
						}
						card.Children(func() {
							if ag.Emoji != "" {
								ui.Text(c, ag.Emoji).FontSize(15)
							}
							ui.Column(c).Gap(1).Children(func() {
								ui.Text(c, ag.Name).FontSize(12.5).SingleLine()
								ui.Text(c, ag.Sub).FontSize(10).TextColor(t.TextMuted).SingleLine()
							})
						})
					}
				})
			}
			ui.Row(c).Gap(8).Wrap().Justify(ui.Center).Children(func() {
				for _, prompt := range suggestions {
					prompt := prompt
					chip := ui.ButtonBase(c).Padding(6, 12).Radius(999).Border(1, vm.Pal.Border).
						Cursor(ui.CursorPointer)
					if chip.Hovered() {
						chip.Background(vm.Pal.Hover)
					}
					if chip.Clicked() {
						acts.SetDraft(prompt)
						vm.FocusComposer = true
					}
					chip.Children(func() {
						ui.Text(c, prompt).FontSize(12).TextColor(t.TextMuted)
					})
				}
			})
		})
	})
}

// homeMaxWidth caps the home panel, and it is deliberately narrower than
// the composer's own 1080. The two are the same Composer, but they sit in
// different frames: on a thread the box is a bar under a conversation and
// may run wide, while here it is a centred panel with a logo above it and
// suggestion chips below. At the thread's width it stopped reading as a
// dialog and spanned the screen, so it gets a panel's measure instead —
// which also leaves it narrower than the 880 message column, the way a
// welcome panel should sit against the conversation that follows it.
const homeMaxWidth = 760
