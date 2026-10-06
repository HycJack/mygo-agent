package ui

// The new-group dialog: creating a group chat happens before the
// chatting does — name it, tick the members, start — and lands in the
// fresh group thread with the composer focused (spec/agents.md).

import (
	"github.com/egoist/mygo/ui"
)

// GroupMemberVM is one agent row of the member list.
type GroupMemberVM struct {
	ID      string
	Name    string
	Emoji   string
	Checked bool
}

// GroupVM is the render input of the new-group dialog. Name and the
// members' Checked flags are bindings the host mirrors back after the
// frame.
type GroupVM struct {
	Open    bool
	Name    string
	Members []GroupMemberVM
	// Err says why Start would not take (no members picked, say).
	Err string

	Pal Palette
}

// GroupActions is what the new-group dialog calls back for.
type GroupActions interface {
	// ToggleMember flips one agent's membership.
	ToggleMember(id string, on bool)
	// Start creates the group and opens its thread.
	Start()
	// Cancel closes the dialog.
	Cancel()
}

// GroupDialog is the new-group-chat modal.
func GroupDialog(c *ui.Context, vm *GroupVM, acts GroupActions) {
	t := c.Theme()
	ui.Modal(c, &vm.Open, func() {
		ui.Column(c).Width(420).Gap(14).Children(func() {
			ui.Text(c, "New group chat").FontSize(15).Bold()
			ui.Text(c, "The members answer in order, each seeing the earlier replies in the conversation.").FontSize(12).TextColor(t.TextMuted)
			ui.TextInput(c, &vm.Name).Label("Name").AutoFocus()
			ui.Text(c, "MEMBERS").FontSize(10.5).FontWeight(600).TextColor(t.TextMuted).
				Margin(4, 0, 0).LetterSpacing(0.6)
			if len(vm.Members) == 0 {
				ui.Text(c, "No agents to invite — create agent profiles in settings first.").FontSize(12).TextColor(t.TextMuted)
			}
			ui.Column(c).Gap(6).Children(func() {
				for i := range vm.Members {
					m := &vm.Members[i]
					row := ui.Row(c).Gap(10).AlignItems(ui.Center).Padding(8, 12).Radius(8).
						Background(vm.Pal.Card).Border(1, vm.Pal.Border)
					row.Children(func() {
						// The name rides the checkbox label: the whole
						// name is the click target, not the 16px box.
						checked := m.Checked
						was := checked
						ui.Checkbox(c, &checked, m.Name).FontSize(12.5)
						if checked != was {
							acts.ToggleMember(m.ID, checked)
						}
						ui.Text(c, m.Emoji).FontSize(13)
					})
				}
			})
			if vm.Err != "" {
				ui.Text(c, vm.Err).FontSize(11.5).TextColor(t.Warning)
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, "Cancel").Clicked() {
					acts.Cancel()
				}
				start := ui.Button(c, "Start chat")
				if len(vm.Members) > 0 {
					start.Background(t.Accent)
				} else {
					start.Disabled(true).Opacity(0.4)
				}
				if start.Clicked() {
					acts.Start()
				}
			})
		})
	})
}
