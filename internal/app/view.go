package app

import (
	uipkg "mygo-agent/internal/ui"

	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
)

// view builds the whole window: the sidebar of tasks and the workspace
// panel beside the thread of the chosen task — or the file viewer in its
// place — with the composer below and the terminal docked at the bottom.
func (a *app) view(c *ui.Context) {
	// In production the window serializes frames against update() on the
	// main thread; headless runs (tests) have no such discipline, so the
	// app lock stands in for it.
	if a.win == nil {
		a.mu.Lock()
		defer a.mu.Unlock()
	}
	c.SetTheme(a.theme)
	a.uiCtx = c
	tb := c.TitleBar()
	top := max(tb.Height, 16)

	if c.Shortcut(ui.Cmd, ui.KeyN) {
		a.newTask()
	}
	if c.Shortcut(ui.Cmd, ui.KeyT) {
		a.toggleTerminal(c)
	}
	if c.Shortcut(ui.Cmd, ui.KeyB) {
		a.navOpen = !a.navOpen
	}
	if c.Shortcut(ui.Cmd, ui.KeyE) {
		a.wsOpen = !a.wsOpen
		if a.wsOpen {
			a.refreshGit()
		}
	}
	if c.Shortcut(0, ui.KeyEscape) {
		// Overlays take Escape first while open; then the viewer gives
		// way, and finally a running agent stops.
		switch {
		case a.viewerOpen:
			a.viewerOpen = false
		case a.running:
			a.stop()
		}
	}

	ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
		if a.navOpen {
			a.renderSidebar(c, top)
		}
		ui.Column(c).Grow(1).MinWidth(0).Background(a.pal.Bg).Children(func() {
			a.header(c, tb)
			// The terminal docks above the bottom edge; the viewer, the
			// thread, or both share the rest.
			body := ui.Column(c).Grow(1).MinHeight(0)
			if a.termOpen {
				body.Children(func() {
					a.mainPane(c)
					a.termDock(c)
				})
			} else {
				body.Children(func() {
					a.mainPane(c)
				})
			}
		})
		if a.wsOpen {
			a.renderWorkspace(c)
		}
	})

	a.overlays(c)
}

// iconToggle is a header icon button with a pressed look while active.
func (a *app) iconToggle(c *ui.Context, tip string, active bool, ic *ui.SVG, fn func()) {
	b := ui.ButtonBase(c).Label(tip).Tooltip(tip).Size(28, 28).Radius(7).Center().Cursor(ui.CursorPointer)
	if active || b.Hovered() {
		b.Background(a.pal.Hover)
	}
	if b.Clicked() {
		fn()
	}
	b.Children(func() {
		col := a.pal.TextMuted
		if active {
			col = a.pal.Text
		}
		ui.Icon(c, ic).FontSize(15).TextColor(col)
	})
}

// mainPane is the header's body: the file viewer while one is open, else
// the home screen or the thread.
func (a *app) mainPane(c *ui.Context) {
	if a.viewerIsOpen() {
		a.viewerPane(c)
		return
	}
	ui.Column(c).Grow(1).MinHeight(0).Children(func() {
		if th := a.currentThread(); th == nil {
			a.home(c)
		} else {
			a.messages(c, th)
			a.composer(c)
		}
	})
}

// header is the custom title bar of the main column: the panel toggles,
// the task's title with its model and mode, and the actions on the right.
func (a *app) header(c *ui.Context, tb ui.TitleBar) {
	th := a.currentThread()
	left := float32(16)
	if !a.navOpen {
		left = tb.Left + 12 // clear the window controls
	}
	ui.Row(c).Height(max(tb.Height, 48)).Padding(0, tb.Right+12, 0, left).DragWindow().AlignItems(ui.Center).Gap(10).Children(func() {
		// The tasks sidebar toggle.
		a.iconToggle(c, "Toggle sidebar (⌘B)", a.navOpen, icPanelLeft, func() { a.navOpen = !a.navOpen })
		ui.Box(c).Width(1).Height(18).Background(a.pal.Border)
		ui.Icon(c, icMessage).FontSize(15).TextColor(a.pal.TextMuted)
		title := "New task"
		if th != nil && th.Title != "" {
			title = th.Title
		}
		ui.Text(c, title).SingleLine().FontWeight(600).FontSize(13).Grow(1).MinWidth(0)
		if th != nil {
			mode := []string{"Read only", "Agent", "Full access"}[a.mode]
			ui.Textf(c, "%s · %s · %s", a.backendLabel(), a.model, mode).FontSize(11).TextColor(a.pal.TextMuted).SingleLine().MinWidth(0)
			if n := changedFiles(th); n > 0 {
				badge := ui.Row(c).Gap(4).AlignItems(ui.Center).Padding(2, 8).Radius(999).Background(a.pal.Card).
					Border(1, a.pal.Border).Children(func() {
					ui.Icon(c, icGitBranch).FontSize(11).TextColor(a.pal.TextMuted)
					ui.Textf(c, "%d changed", n).FontSize(11).TextColor(a.pal.TextMuted).SingleLine()
				})
				badge.Tooltip("Files changed in this task")
			}
		}
		ui.Spacer(c)
		if a.running {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Padding(2, 8).Radius(999).Background(a.pal.Card).Children(func() {
				ui.Spinner(c)
				ui.Text(c, "Working").FontSize(11).TextColor(a.pal.TextMuted)
			})
		}
		// While the workspace panel is closed, its toggle lives here at
		// the window's top-right corner; open, it moves onto the panel.
		if !a.wsOpen {
			a.iconToggle(c, "Show workspace (⌘E)", false, icPanelRight, func() {
				a.wsOpen = true
				a.refreshGit()
			})
		}
		// Terminal toggle.
		termBtn := ui.ButtonBase(c).Label("Toggle terminal").Tooltip("Toggle terminal (⌘T)").
			Size(28, 28).Radius(7).Center().Cursor(ui.CursorPointer)
		if termBtn.Hovered() {
			termBtn.Background(a.pal.Hover)
		}
		if termBtn.Clicked() {
			a.toggleTerminal(c)
		}
		termBtn.Children(func() {
			col := a.pal.TextMuted
			if a.termOpen {
				col = a.pal.Text
			}
			ui.Icon(c, icTerminal).FontSize(15).TextColor(col)
		})
		more := ui.ButtonBase(c).Label("Task actions").Tooltip("Task actions").
			Size(28, 28).Radius(7).Center().Cursor(ui.CursorPointer)
		if more.Hovered() || a.threadMenu {
			more.Background(a.pal.Hover)
		}
		if more.Clicked() {
			a.threadMenu = !a.threadMenu
		}
		more.Children(func() { ui.Icon(c, icMore).FontSize(15).TextColor(a.pal.TextMuted) })
		ui.Popover(c, more, &a.threadMenu, func() {
			closeMenu := func() { a.threadMenu = false }
			ui.Column(c).Width(210).Padding(4).Radius(10).Background(a.pal.Card).
				Border(1, a.pal.Border).Shadow(0, 8, 24, 0, ui.RGBA(0, 0, 0, 0.4)).Children(func() {
				if th != nil && uipkg.MenuItem(c, "Rename task", false, a.pal) {
					closeMenu()
					a.renaming, a.renameID, a.renameDraft = true, th.ID, th.Title
				}
				if th != nil && uipkg.MenuItem(c, "Export as Markdown", false, a.pal) {
					closeMenu()
					a.exportMarkdown(c, th)
				}
				ui.Box(c).Height(1).Margin(4, 6).Background(a.pal.Border)
				if th != nil && uipkg.MenuItem(c, "Delete task", false, a.pal) {
					closeMenu()
					a.deleteThread(c, th.ID)
				}
			})
		})
	})
}

// home is what a new task starts from: the shared view in internal/ui
// over a ViewModel snapshot, with the app's own suggestion chips.
func (a *app) home(c *ui.Context) {
	vm := a.homeViewModel()
	uipkg.Home(c, vm, homeActions{a: a}, []string{
		"Explain what this project does",
		"Find and fix a failing test",
		"Write a migration guide",
		"Review the latest diff",
	})
	a.syncVM() // keystrokes and consumed focus flag land in host state
}

// termDock is the embedded terminal at the bottom of the window, a real
// shell run by the terminal plugin's Ghostty emulator.
func (a *app) termDock(c *ui.Context) {
	ui.Column(c).Height(a.termHeight).Shrink(0).Background(ui.Hex("#0a0a0d")).
		BorderWidth(1, 0, 0, 0).BorderColor(a.pal.Border).Children(func() {
		if a.term != nil {
			terminal.View(c, a.term).Fill()
		}
	})
}

func (a *app) toggleTerminal(c *ui.Context) {
	a.termOpen = !a.termOpen
	if a.termOpen && !a.openTerminal() && c != nil {
		c.Toast("Could not start the terminal in " + filepath.Base(a.workdir))
	}
}

func (a *app) newTask() {
	a.current = ""
	a.setDraft("")
	a.focusComposer = true
}

func (a *app) backendLabel() string {
	switch a.backend {
	case "codex":
		return "Codex CLI"
	case "builtin":
		return "Built-in agent"
	default:
		return "Demo agent"
	}
}

// overlays are the dialogs drawn above everything while open: renaming
// a task, and managing the model providers.
func (a *app) overlays(c *ui.Context) {
	ui.Modal(c, &a.renaming, func() {
		ui.Column(c).Width(380).Padding(20).Gap(14).Radius(12).Background(a.pal.Card).
			Border(1, a.pal.Border).Shadow(0, 12, 32, 0, ui.RGBA(0, 0, 0, 0.5)).Children(func() {
			ui.Text(c, "Rename task").FontSize(15).Bold()
			ui.TextInput(c, &a.renameDraft).Label("Title").AutoFocus()
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, "Cancel").Clicked() {
					a.renaming = false
				}
				if ui.PrimaryButton(c, "Rename").Clicked() {
					a.applyRename()
				}
			})
		})
	})
	a.settingsModal(c)
}

func (a *app) applyRename() {
	if th := a.byID(a.renameID); th != nil {
		th.Title = strings.TrimSpace(a.renameDraft)
		th.Updated = time.Now()
		a.saveThread(th)
	}
	a.renaming = false
}

// exportMarkdown writes the thread to a Markdown file in Downloads.
func (a *app) exportMarkdown(c *ui.Context, th *Thread) {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", th.Title)
	for _, m := range th.Messages {
		if m.Role == "user" {
			fmt.Fprintf(&b, "## You\n\n%s\n\n", m.Text)
		} else {
			fmt.Fprintf(&b, "## Codex\n\n%s\n\n", m.Text)
			for _, bl := range m.Blocks {
				switch bl.Type {
				case "command":
					fmt.Fprintf(&b, "```\n$ %s\n%s\n```\n\n", bl.Text, bl.Output)
				case "diff":
					fmt.Fprintf(&b, "**%s** (+%d −%d)\n\n```diff\n", bl.File, bl.Add, bl.Del)
					for _, l := range bl.Lines {
						b.WriteString(string(l.Kind) + " " + l.Text + "\n")
					}
					b.WriteString("```\n\n")
				}
			}
		}
	}
	home, _ := os.UserHomeDir()
	path := filepath.Join(home, "Downloads", fmt.Sprintf("codex-%s.md", th.ID))
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		c.Toast("Export failed: " + err.Error())
		return
	}
	c.Toast("Exported to " + path)
}
