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
	// CanStart is computed by the host from its own draft state — the
	// members' Checked flags in the snapshot are stale the moment a
	// checkbox flips (the flip reaches the host through ToggleMember).
	CanStart bool
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
			_ = t
			ui.Column(c).Gap(6).Children(func() {
				for i := range vm.Members {
					m := &vm.Members[i]
					// ui.Button (not a bare ButtonBase row): inside a
					// Modal only the toolkit's own Button helper reliably
					// receives clicks in this nested context.
					label := m.Name
					if m.Checked {
						label = "✓ " + label
					}
					if ui.Button(c, label).Clicked() {
						acts.ToggleMember(m.ID, !m.Checked)
					}
				}
			})
			if vm.Err != "" {
				ui.Text(c, vm.Err).FontSize(11.5).TextColor(t.Warning)
			}
			ui.Row(c).Gap(8).Justify(ui.End).AlignItems(ui.Center).Children(func() {
				if vm.Err != "" {
					ui.Text(c, vm.Err).FontSize(11.5).TextColor(t.Warning).Grow(1)
				}
				start := ui.Button(c, "Start chat")
				if !vm.CanStart {
					start.Disabled(true).Opacity(0.4)
				} else {
					start.Background(t.Accent)
				}
				if start.Clicked() {
					acts.Start()
				}
			})
		})
	})
}
