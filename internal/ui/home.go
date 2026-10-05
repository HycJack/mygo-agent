package ui

import "github.com/egoist/mygo/ui"

// Home is the new-task screen: the greeting, the composer in the middle
// and a few suggested prompts. It renders from the ViewModel and talks
// back only through Actions.
func Home(c *ui.Context, vm *ViewModel, acts Actions, suggestions []string) {
	t := c.Theme()
	ui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).Fill().Justify(ui.Center).AlignItems(ui.Center).Gap(22).Padding(24).
			MaxWidth(860).Margin(0, ui.Auto).Children(func() {
			logo := ui.Box(c).Size(56, 56).Radius(16).Background(vm.Pal.Card).Border(1, vm.Pal.Border).Center()
			logo.Children(func() {
				ui.Icon(c, IconSparkles).FontSize(26).TextColor(vm.Pal.Text)
			})
			ui.Text(c, "What are we coding next?").FontSize(25).Bold()
			ui.Text(c, "Codex runs in the background while you keep working.").FontSize(13).TextColor(t.TextMuted)
			Composer(c, vm, acts)
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
