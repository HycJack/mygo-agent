package ui

import "github.com/egoist/mygo/ui"

// The window chrome renders here over ViewModel snapshots
// (spec/architecture.md): the header bar above the main column, its task
// menu, and the rename dialog. The host assembles the frame around them.

// HeaderVM is the render input of the main column's header. MenuOpen is
// a binding the popover writes and the host syncs back after the frame.
type HeaderVM struct {
	Title, Meta string // the task's title; backend · model · mode
	Changed     int    // files changed in this task
	Running     bool   // shows the working pill
	HasTask     bool   // a task is open (menu and meta need one)

	NavOpen, WsOpen, TermOpen, MenuOpen bool

	Pal Palette
}

// HeaderActions is what the header calls back for.
type HeaderActions interface {
	ToggleNav()
	ToggleWorkspace()
	ToggleTerminal()
	Rename()
	// Export writes the thread out as Markdown.
	Export()
	// Trace opens the thread's event trace in the viewer.
	Trace()
	// Delete removes the task (the undo toast is the way back).
	Delete()
	// OpenSettings opens the settings dialog (the gear and ⌘,).
	OpenSettings()
}

// Header is the custom title bar of the main column: the panel toggles,
// the task's title with its model and mode, and the actions on the
// right. It drags the window.
func Header(c *ui.Context, tb ui.TitleBar, vm *HeaderVM, acts HeaderActions) {
	left := float32(16)
	if !vm.NavOpen {
		left = tb.Left + 12 // clear the window controls
	}
	ui.Row(c).Height(max(tb.Height, 48)).Padding(0, tb.Right+12, 0, left).DragWindow().AlignItems(ui.Center).Gap(10).Children(func() {
		// The tasks sidebar toggle.
		iconToggle(c, vm.Pal, "Toggle sidebar (⌘B)", vm.NavOpen, IconPanelLeft, acts.ToggleNav)
		ui.Box(c).Width(1).Height(18).Background(vm.Pal.Border)
		ui.Icon(c, IconMessage).FontSize(15).TextColor(vm.Pal.TextMuted)
		ui.Text(c, vm.Title).SingleLine().FontWeight(600).FontSize(13).Grow(1).MinWidth(0)
		if vm.HasTask {
			ui.Text(c, vm.Meta).FontSize(11).TextColor(vm.Pal.TextMuted).SingleLine().MinWidth(0)
			if vm.Changed > 0 {
				badge := ui.Row(c).Gap(4).AlignItems(ui.Center).Padding(2, 8).Radius(999).Background(vm.Pal.Card).
					Border(1, vm.Pal.Border).Children(func() {
					ui.Icon(c, IconGitBranch).FontSize(11).TextColor(vm.Pal.TextMuted)
					ui.Textf(c, "%d changed", vm.Changed).FontSize(11).TextColor(vm.Pal.TextMuted).SingleLine()
				})
				badge.Tooltip("Files changed in this task")
			}
		}
		ui.Spacer(c)
		if vm.Running {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Padding(2, 8).Radius(999).Background(vm.Pal.Card).Children(func() {
				ui.Spinner(c)
				ui.Text(c, "Working").FontSize(11).TextColor(vm.Pal.TextMuted)
			})
		}
		// While the workspace panel is closed, its toggle lives here at
		// the window's top-right corner; open, it moves onto the panel.
		if !vm.WsOpen {
			iconToggle(c, vm.Pal, "Show workspace (⌘E)", false, IconPanelRight, acts.ToggleWorkspace)
		}
		// Terminal toggle.
		termBtn := ui.ButtonBase(c).Label("Toggle terminal").Tooltip("Toggle terminal (⌘T)").
			Size(28, 28).Radius(7).Center().Cursor(ui.CursorPointer)
		if termBtn.Hovered() {
			termBtn.Background(vm.Pal.Hover)
		}
		if termBtn.Clicked() {
			acts.ToggleTerminal()
		}
		termBtn.Children(func() {
			col := vm.Pal.TextMuted
			if vm.TermOpen {
				col = vm.Pal.Text
			}
			ui.Icon(c, IconTerminal).FontSize(15).TextColor(col)
		})
		// Settings: always one click away (the gear and ⌘,).
		gear := ui.ButtonBase(c).Label("Settings").Tooltip("Settings (⌘,)").
			Size(28, 28).Radius(7).Center().Cursor(ui.CursorPointer)
		if gear.Hovered() {
			gear.Background(vm.Pal.Hover)
		}
		if gear.Clicked() {
			acts.OpenSettings()
		}
		gear.Children(func() {
			ui.Icon(c, IconSliders).FontSize(15).TextColor(vm.Pal.TextMuted)
		})
		if vm.HasTask {
			taskMenu(c, vm, acts)
		}
	})
}

// taskMenu is the ··· popover of an open task: rename, export, delete.
func taskMenu(c *ui.Context, vm *HeaderVM, acts HeaderActions) {
	more := ui.ButtonBase(c).Label("Task actions").Tooltip("Task actions").
		Size(28, 28).Radius(7).Center().Cursor(ui.CursorPointer)
	if more.Hovered() || vm.MenuOpen {
		more.Background(vm.Pal.Hover)
	}
	if more.Clicked() {
		vm.MenuOpen = !vm.MenuOpen
	}
	more.Children(func() { ui.Icon(c, IconMore).FontSize(15).TextColor(vm.Pal.TextMuted) })
	ui.Popover(c, more, &vm.MenuOpen, func() {
		closeMenu := func() { vm.MenuOpen = false }
		// Popover's panel already paints the look; more here would read
		// as a second border inside it.
		ui.Column(c).Width(210).Padding(4).Children(func() {
			if MenuItem(c, "Rename task", false, vm.Pal) {
				closeMenu()
				acts.Rename()
			}
			if MenuItem(c, "View trace", false, vm.Pal) {
				closeMenu()
				acts.Trace()
			}
			if MenuItem(c, "Export as Markdown", false, vm.Pal) {
				closeMenu()
				acts.Export()
			}
			ui.Box(c).Height(1).Margin(4, 6).Background(vm.Pal.Border)
			if MenuItem(c, "Delete task", false, vm.Pal) {
				closeMenu()
				acts.Delete()
			}
		})
	})
}

// iconToggle is a header icon button with a pressed look while active.
func iconToggle(c *ui.Context, pal Palette, tip string, active bool, ic *ui.SVG, fn func()) {
	b := ui.ButtonBase(c).Label(tip).Tooltip(tip).Size(28, 28).Radius(7).Center().Cursor(ui.CursorPointer)
	if active || b.Hovered() {
		b.Background(pal.Hover)
	}
	if b.Clicked() {
		fn()
	}
	b.Children(func() {
		col := pal.TextMuted
		if active {
			col = pal.Text
		}
		ui.Icon(c, ic).FontSize(15).TextColor(col)
	})
}

// RenameVM is the render input of the rename dialog. Title is the
// binding the text input writes; the host syncs it back after the frame.
type RenameVM struct {
	Open  bool
	Title string
}

// RenameActions is what the rename dialog calls back for.
type RenameActions interface {
	Apply(title string) // renames the task to title
}

// RenameDialog is the rename task modal.
func RenameDialog(c *ui.Context, vm *RenameVM, acts RenameActions) {
	ui.Modal(c, &vm.Open, func() {
		// Modal's panel already paints the look; more here would read as
		// a second border inside it.
		ui.Column(c).Width(380).Gap(14).Children(func() {
			ui.Text(c, "Rename task").FontSize(15).Bold()
			ui.TextInput(c, &vm.Title).Label("Title").AutoFocus()
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, "Cancel").Clicked() {
					vm.Open = false
				}
				if ui.PrimaryButton(c, "Rename").Clicked() {
					vm.Open = false
					acts.Apply(vm.Title)
				}
			})
		})
	})
}
