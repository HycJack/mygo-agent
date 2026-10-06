package main

// The Overlays page: dialogs, popovers, menus, toasts, drag & drop and
// the motion system, in dashboard clothing.

import (
	"time"

	"github.com/egoist/mygo/ui"
)

func (d *dashboard) overlaysPage(c *ui.Context, pal Palette) {
	PageTitle(c, pal, "Overlays", "Dialogs, popovers, toasts, drag & drop and motion.")

	ui.Row(c).Gap(14).AlignItems(ui.Start).Children(func() {
		// Buttons and menus.
		Card(c, pal, "Buttons and menus", "", func() {
			ui.Row(c).Gap(8).Wrap().Children(func() {
				if ui.PrimaryButton(c, "New report").Clicked() {
					d.dialog = true
				}
				ui.Button(c, "Disabled").Disabled(true)
				ui.MenuButton(c, "Schedule ▾", func(m *ui.Menu) {
					for _, at := range []string{"Tomorrow 9:00", "Next Monday", "First of month"} {
						if m.Item(at).Chosen() {
							c.Toast("Scheduled: " + at)
						}
					}
				})
				pick := ui.Button(c, "View ▾")
				if pick.Clicked() {
					d.menu = !d.menu
				}
				ui.Popover(c, pick, &d.menu, func() {
					for _, item := range []string{"As table", "As cards", "As chart"} {
						entry := ui.Row(c).Key(item).Padding(6, 12).Radius(6).Width(150)
						if entry.Hovered() {
							entry.Background(pal.Accent).TextColor(ui.RGB(255, 255, 255))
						}
						if entry.Clicked() {
							d.menu = false
							c.Toast(item)
						}
						entry.Children(func() { ui.Text(c, item).FontSize(12.5) })
					}
				})
				ui.Button(c, "Hover me").Tooltip("Tooltips rest a moment before they show.")
				if ui.Button(c, "Delete a draft").Clicked() {
					d.notes--
					c.ToastAction("Draft deleted", "Undo", func() { d.notes++ })
				}
			})
			ui.Textf(c, "%d drafts left. The undo on the toast brings one back.", max(d.notes, 0)).FontSize(11.5).TextColor(pal.TextMuted)
		}).Grow(1)

		// The color tiles fade their backgrounds in and out as the pointer
		// comes and goes.
		Card(c, pal, "Motion", "hover the tiles", func() {
			ui.Row(c).Gap(10).Children(func() {
				for i := range 4 {
					col := pal.Series[i]
					tile := ui.Box(c).Key(i).Size(64, 48).Radius(8).Border(2, pal.Border)
					tile.Background(col.Alpha(0.15))
					if tile.Hovered() {
						tile.Background(col)
					}
					tile.Transition(ui.ElementTransition{Colors: true, Duration: 150 * time.Millisecond})
				}
			})
			ui.Text(c, "Backgrounds and borders fade where the pointer lands.").FontSize(11.5).TextColor(pal.TextMuted)
		}).Grow(1)
	})

	// The kanban board: tasks drag between the columns; a drop turns a
	// task of one column into one of another.
	Card(c, pal, "Board", "drag a card to another column; Escape gives up", func() {
		if d.tasks == nil {
			d.tasks = []task{
				{"Write the Q4 summary", 0},
				{"Fix the checkout alert", 0},
				{"Migrate the events table", 1},
				{"Review onboarding copy", 1},
				{"Ship the pricing page", 2},
			}
		}
		ui.Row(c).Gap(12).AlignItems(ui.Start).Children(func() {
			for col, title := range []string{"To do", "In progress", "Done"} {
				bin := ui.Column(c).Grow(1).Basis(0).Gap(6).Padding(10).Radius(8).MinHeight(150).
					Background(pal.Bg).Border(1, pal.Border).Label(title)
				if t, ok := ui.Drop[*task](bin); ok {
					t.col = col
				}
				if t, ok := ui.DragOver[*task](bin); ok && t.col != col {
					bin.Border(2, pal.Series[0])
				}
				bin.Children(func() {
					ui.Text(c, title).FontSize(12).FontWeight(600).TextColor(pal.TextMuted)
					for i := range d.tasks {
						t := &d.tasks[i]
						if t.col != col {
							continue
						}
						ui.Row(c).Key(t.name).Padding(8, 10).Radius(8).Gap(8).AlignItems(ui.Center).
							Background(pal.Card).Border(1, pal.Border).Cursor(ui.CursorPointer).Drag(t).
							Children(func() {
								ui.Box(c).Size(8, 8).Radius(4).Background(pal.Series[col]).Shrink(0)
								ui.Text(c, t.name).FontSize(12.5).Grow(1).MinWidth(0).SingleLine()
								ui.Avatar(c, eventActor(len(t.name)), nil).Size(20, 20)
							})
					}
				})
			}
		})
	})

	// The dialogs at the page's end: they render over everything.
	if ui.AlertDialog(c, &d.alert, "Delete “Old workspace”?", "All 14 dashboards in it go with it. You cannot undo this.", "Cancel", "Delete") == 1 {
		c.Toast("Workspace deleted")
	}
	ui.Modal(c, &d.dialog, func() {
		ui.Column(c).Gap(10).Children(func() {
			ui.Text(c, "New report").FontSize(17).FontWeight(700)
			ui.Text(c, "A modal centered over the window; click outside or press Escape to close.").FontSize(12.5).TextColor(pal.TextMuted)
			ui.Row(c).Gap(8).Justify(ui.End).Margin(6, 0, 0, 0).Children(func() {
				if ui.Button(c, "Cancel").Clicked() {
					d.dialog = false
				}
				if ui.PrimaryButton(c, "Create").Clicked() {
					d.dialog = false
					c.Toast("Report created")
				}
			})
		})
	})
	// The danger button that opens the alert.
	ui.Row(c).Gap(8).Children(func() {
		if ui.Button(c, "Delete workspace…").Clicked() {
			d.alert = true
		}
		ui.Text(c, "opens an alert dialog").FontSize(11.5).TextColor(pal.TextMuted)
	})
}
