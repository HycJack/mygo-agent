package main

import (
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// sidebar is the left rail: the wordmark, the new-task button, the search
// field, the tasks grouped by when they were updated, and the backend
// picker at the bottom.
func (a *app) sidebar(c *ui.Context, top float32) {
	t := c.Theme()
	ui.Column(c).Width(264).Shrink(0).Background(a.pal.SidebarBG).
		BorderWidth(0, 1, 0, 0).BorderColor(a.pal.Border).Children(func() {
		// The strip the window controls sit over, which drags the window.
		ui.Row(c).Height(top + 2).PaddingX(16).DragWindow().AlignItems(ui.Center).Gap(8).Children(func() {
			logo := ui.Box(c).Size(20, 20).Radius(6).Background(a.pal.Text).Center()
			logo.Children(func() { ui.Icon(c, icSparkles).FontSize(12).TextColor(a.pal.AccentSub) })
			ui.Text(c, "Codex").FontSize(13).Bold()
			ui.Spacer(c)
			ui.Text(c, "Go").FontSize(10).TextColor(a.pal.TextMuted)
		})
		// The project switcher.
		ui.Column(c).Padding(6, 10, 0).Children(func() {
			a.projectSwitcher(c)
		})
		// New task and search.
		ui.Column(c).Padding(6, 10, 8).Gap(8).Children(func() {
			nb := ui.ButtonBase(c).Padding(7, 10).Radius(8).Gap(8).Cursor(ui.CursorPointer)
			if nb.Hovered() {
				nb.Background(a.pal.Hover)
			}
			if nb.Clicked() {
				a.newTask()
			}
			nb.Children(func() {
				ui.Icon(c, icPlus).FontSize(15).TextColor(t.Text)
				ui.Text(c, "New task").FontSize(13).Grow(1)
				ui.Text(c, "⌘N").FontSize(11).TextColor(a.pal.TextMuted)
			})
			ui.SearchField(c, &a.search).Placeholder("Search tasks…")
		})
		// The tasks, grouped by date.
		ui.Scroll(c).Grow(1).Padding(2, 8, 12).Gap(2).Children(func() {
			a.threadList(c)
		})
		// The backend picker.
		ui.Column(c).Padding(10).BorderWidth(1, 0, 0, 0).BorderColor(a.pal.Border).Children(func() {
			pick := ui.ButtonBase(c).Padding(6, 8).Radius(8).Gap(8).Cursor(ui.CursorPointer)
			if pick.Hovered() {
				pick.Background(a.pal.Hover)
			}
			if pick.Clicked() {
				a.backendMenu = !a.backendMenu
			}
			pick.Children(func() {
				dot := a.pal.Success
				if a.backend == "codex" {
					dot = a.pal.Text
				}
				ui.Icon(c, icDot).FontSize(9).TextColor(dot)
				ui.Textf(c, "%s", a.backendLabel()).FontSize(12).Grow(1)
				chev := ui.Icon(c, icChevDown).FontSize(12).TextColor(a.pal.TextMuted)
				if a.backendMenu {
					chev.Rotate(180)
				}
			})
			ui.Popover(c, pick, &a.backendMenu, func() {
				closeMenu := func() { a.backendMenu = false }
				ui.Column(c).Width(250).Padding(4).Radius(10).Background(a.pal.Card).
					Border(1, a.pal.Border).Shadow(0, 8, 24, 0, ui.RGBA(0, 0, 0, 0.4)).Children(func() {
					if a.menuItem(c, "Built-in agent — runs in the app", a.backend == "builtin") {
						closeMenu()
						a.backend = "builtin"
						a.saveConfig()
					}
					if a.menuItem(c, "Demo agent — built-in, no account", a.backend == "demo") {
						closeMenu()
						a.backend = "demo"
						a.saveConfig()
					}
					label := "Codex CLI — runs the codex binary"
					if a.codexPath == "" {
						label = "Codex CLI — not found in PATH"
					}
					if a.menuItemDisabled(c, label, a.backend == "codex", a.codexPath == "") {
						closeMenu()
						a.backend = "codex"
						a.saveConfig()
					}
				})
			})
		})
	})
}

// threadList builds the groups and rows of the task list.
func (a *app) threadList(c *ui.Context) {
	groups := a.visibleThreads()
	for _, g := range groups {
		ui.Text(c, g.title).FontSize(11).FontWeight(600).TextColor(a.pal.TextMuted).
			Padding(10, 8, 4).LetterSpacing(0.4)
		for _, th := range g.threads {
			a.threadRow(c, th)
		}
	}
	if len(groups) == 0 {
		msg := "No tasks match."
		if strings.TrimSpace(a.search) == "" {
			msg = "No tasks yet — start one above."
		}
		ui.Text(c, msg).FontSize(12).TextColor(a.pal.TextMuted).Padding(8)
	}
}

// threadRow is one task in the sidebar: its title, its age, and, on
// hover, a delete button; right-click renames or deletes it.
func (a *app) threadRow(c *ui.Context, th *Thread) {
	row := ui.ButtonBase(c).Key(th.ID).Fill().Padding(6, 8).Radius(8).Gap(8).Cursor(ui.CursorPointer)
	chosen := th.ID == a.current
	if chosen {
		row.Background(a.pal.Sel)
	} else if row.Hovered() {
		row.Background(a.pal.Hover)
	}
	if row.Clicked() {
		a.current = th.ID
		a.focusComposer = true
		if a.win != nil {
			title := th.Title
			if title == "" {
				title = "New task"
			}
			a.win.SetTitle("Codex — " + title)
		}
	}
	row.ContextMenu(func(m *ui.Menu) {
		if m.Item("Rename").Chosen() {
			a.renaming, a.renameID, a.renameDraft = true, th.ID, th.Title
		}
		m.Separator()
		if m.Item("Delete").Chosen() {
			a.deleteThread(c, th.ID)
		}
	})
	row.Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			title := th.Title
			if title == "" {
				title = "New task"
			}
			ui.Text(c, title).SingleLine().FontSize(13)
			sub := relTime(th.Updated)
			if th.CodexID != "" {
				sub += " · codex"
			}
			ui.Text(c, sub).SingleLine().FontSize(11).TextColor(a.pal.TextMuted)
		})
		// The delete button exists while the row is hovered — and for as
		// long as the pointer stays on the button itself: a hover-gated
		// build would remove it between a click's press and release.
		if row.Hovered() {
			a.hoverRow = th.ID
		}
		if a.hoverRow == th.ID || row.Hovered() {
			del := ui.ButtonBase(c).Label("Delete task").Tooltip("Delete").Size(24, 24).Radius(6).Center()
			if del.Hovered() {
				del.Background(a.pal.CardHover)
				a.hoverRow = th.ID
			} else if !row.Hovered() {
				a.hoverRow = ""
			}
			if del.Clicked() {
				a.deleteThread(c, th.ID)
			}
			del.Children(func() { ui.Icon(c, icTrash).FontSize(13).TextColor(a.pal.TextMuted) })
		} else if chosen && a.running {
			ui.Spinner(c)
		}
	})
}

// projectSwitcher is the button at the top of the sidebar: the active
// project's name and path, opening a picker of the projects and an
// "Add project…" entry backed by the native folder dialog.
func (a *app) projectSwitcher(c *ui.Context) {
	t := c.Theme()
	sw := ui.ButtonBase(c).Fill().Padding(7, 10).Radius(8).Gap(8).Cursor(ui.CursorPointer)
	if sw.Hovered() || a.projectMenu {
		sw.Background(a.pal.Hover)
	}
	if sw.Clicked() {
		a.projectMenu = !a.projectMenu
	}
	sw.Children(func() {
		ui.Icon(c, icFolder).FontSize(14).TextColor(t.Text)
		ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
			ui.Text(c, filepath.Base(a.workdir)).SingleLine().FontSize(12.5).FontWeight(600)
			ui.Text(c, a.workdir).SingleLine().FontSize(10.5).TextColor(a.pal.TextMuted)
		})
		chev := ui.Icon(c, icChevDown).FontSize(12).TextColor(a.pal.TextMuted)
		if a.projectMenu {
			chev.Rotate(180)
		}
	})
	ui.Popover(c, sw, &a.projectMenu, func() {
		ui.Column(c).Width(300).Padding(4).Radius(12).Background(a.pal.Card).
			Border(1, a.pal.Border).Shadow(0, 8, 24, 0, ui.RGBA(0, 0, 0, 0.4)).Children(func() {
			ui.Text(c, "PROJECTS").FontSize(10).FontWeight(600).TextColor(a.pal.TextMuted).
				Padding(6, 10, 2).LetterSpacing(0.6)
			for i := range a.projects {
				p := a.projects[i]
				active := p.ID == a.activeProject
				row := ui.ButtonBase(c).Padding(6, 10).Radius(7).Gap(8).Cursor(ui.CursorPointer)
				if row.Hovered() {
					row.Background(a.pal.CardHover)
				}
				if row.Clicked() {
					a.projectMenu = false
					a.switchProject(p.ID)
				}
				row.ContextMenu(func(m *ui.Menu) {
					if m.Item("Remove project").Chosen() {
						a.removeProject(p.ID)
					}
				})
				row.Children(func() {
					ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
						ui.Text(c, filepath.Base(p.Path)).SingleLine().FontSize(12.5)
						ui.Text(c, p.Path).SingleLine().FontSize(10.5).TextColor(a.pal.TextMuted)
					})
					if active {
						ui.Icon(c, icCheck).FontSize(13).TextColor(t.Text)
					}
				})
			}
			ui.Box(c).Height(1).Margin(4, 6).Background(a.pal.Border)
			add := ui.ButtonBase(c).Padding(7, 10).Radius(7).Gap(8).Cursor(ui.CursorPointer)
			if add.Hovered() {
				add.Background(a.pal.CardHover)
			}
			if add.Clicked() {
				a.pickProjectDir()
			}
			add.Children(func() {
				ui.Icon(c, icPlus).FontSize(13).TextColor(t.TextMuted)
				ui.Text(c, "Add project…").FontSize(12.5).Grow(1)
			})
		})
	})
}

// pickProjectDir opens the native folder dialog and adds what it picks.
func (a *app) pickProjectDir() {
	if a.pickingDir {
		return
	}
	a.pickingDir = true
	a.projectMenu = false
	win := a.win
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:            win,
			Title:             "Choose a project folder",
			Directory:         true,
			CreateDirectories: true,
			DefaultPath:       a.workdir,
		})
		a.update(func() {
			a.pickingDir = false
			if err != nil || len(paths) == 0 {
				return
			}
			a.addProjectPath(paths[0])
		})
	}()
}
