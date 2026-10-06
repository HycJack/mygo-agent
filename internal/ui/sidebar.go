package ui

import (
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

// SidebarVM is the render input for the left rail: projects, the task
// list, the search and the backend picker. Threads arrive project-
// filtered; search filtering and date grouping happen here (pure code,
// unit-testable without a window).
type SidebarVM struct {
	Version, Workdir string

	Projects      []ProjectVM
	ActiveProject string

	Search  string
	Threads []ThreadVM
	Current string

	// The rail's agent switcher (the old backend switcher's slot): the
	// agent a new task binds, with the resolved backend-model subtitle
	// (spec/agents.md).
	AgentID   string
	AgentName string
	Agents    []AgentVM
	Width     float32

	// Transient view state; the host syncs it back after each frame.
	AgentMenu   bool
	ProjectMenu bool
	HoverRow    string

	Pal Palette
}

// ProjectVM is one entry of the project switcher.
type ProjectVM struct {
	ID, Path string
}

// ThreadVM is one task row.
type ThreadVM struct {
	ID, Title  string
	Updated    time.Time
	BackendTag string // non-empty appends " · <tag>" under the title
	Running    bool
	// AgentEmoji is the bound agent's emoji, prefixed to the title so a
	// task's owner is recognizable at a glance (spec/agents.md).
	AgentEmoji string
	// Search is the row's full-text haystack (title + messages), filled
	// by the host only while a search is active; empty matches on the
	// title alone.
	Search string
}

// SidebarActions is what the rail calls back for.
type SidebarActions interface {
	NewTask()
	OpenThread(id string)
	DeleteThread(id string)
	RenameThread(id string)
	SetAgent(id string)
	// NewGroup creates a group profile and opens its form (spec/agents.md).
	NewGroup()
	SwitchProject(id string)
	RemoveProject(id string)
	PickProjectDir()
	// OpenSettings opens the settings dialog (the rail's gear, or ⌘,).
	OpenSettings()
}

// ThreadGroup is one date section of the task list.
type ThreadGroup struct {
	Title   string
	Threads []ThreadVM
}

// GroupThreads filters threads by the search text and groups them by how
// recently they were updated: each thread goes in the first group that
// fits, so the groups partition the list. Newest first within a group.
func GroupThreads(threads []ThreadVM, search string) []ThreadGroup {
	q := strings.ToLower(strings.TrimSpace(search))
	names := []struct {
		key, title string
		is         func(time.Time) bool
	}{
		{"today", "Today", func(t time.Time) bool { return sameDay(t, time.Now()) }},
		{"yesterday", "Yesterday", func(t time.Time) bool { return sameDay(t, time.Now().AddDate(0, 0, -1)) }},
		{"week", "Previous 7 Days", func(t time.Time) bool { return time.Since(t) < 7*24*time.Hour }},
		{"month", "Previous 30 Days", func(t time.Time) bool { return time.Since(t) < 30*24*time.Hour }},
		{"older", "Older", func(time.Time) bool { return true }},
	}
	// Pointer-stable grouping: collect per key first, then emit in the
	// fixed section order (a slice of groups appended-to while holding
	// pointers into it would dangle after a realloc — the bug this
	// shape avoids).
	set := map[string][]ThreadVM{}
	for _, th := range threads {
		if q != "" && !threadMatches(th, q) {
			continue
		}
		for _, n := range names {
			if !n.is(th.Updated) {
				continue
			}
			set[n.key] = append(set[n.key], th)
			break
		}
	}
	var groups []ThreadGroup
	for _, n := range names {
		if len(set[n.key]) == 0 {
			continue
		}
		groups = append(groups, ThreadGroup{Title: n.title, Threads: set[n.key]})
	}
	return groups
}

func threadMatches(th ThreadVM, q string) bool {
	if strings.Contains(strings.ToLower(th.Title), q) {
		return true
	}
	// Full text (spec/agents.md, tracing): the host fills the haystack
	// only while a search is on, so the empty default costs nothing.
	return th.Search != "" && strings.Contains(strings.ToLower(th.Search), q)
}

func sameDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

// RelTime formats a timestamp the way the rail does: "now", "12m", "3h",
// the weekday within the last week, otherwise the date.
func RelTime(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + "m"
	case d < 24*time.Hour:
		return strconv.Itoa(int(d.Hours())) + "h"
	case d < 7*24*time.Hour:
		return t.Format("Mon")
	case t.Year() == time.Now().Year():
		return t.Format("1/2")
	default:
		return t.Format("1/2/06")
	}
}

// Sidebar renders the left rail. Width and placement stay with the host;
// this draws the whole column contents.
func Sidebar(c *ui.Context, vm *SidebarVM, acts SidebarActions, top float32) {
	t := c.Theme()
	ui.Column(c).Width(vm.Width).Shrink(0).Background(vm.Pal.SidebarBG).Children(func() {
		// The strip the window controls sit over, which drags the
		// window. No branding here: the macOS traffic lights own this
		// corner.
		ui.Row(c).Height(top + 2).PaddingX(16).DragWindow().AlignItems(ui.Center).Children(func() {
			ui.Spacer(c)
			ui.Text(c, vm.Version).FontSize(10).TextColor(vm.Pal.TextMuted)
		})
		ui.Column(c).Padding(6, 10, 0).Children(func() {
			projectSwitcher(c, vm, acts)
		})
		ui.Column(c).Padding(6, 10, 8).Gap(8).Children(func() {
			nb := ui.ButtonBase(c).Padding(7, 10).Radius(8).Gap(8).Cursor(ui.CursorPointer)
			if nb.Hovered() {
				nb.Background(vm.Pal.Hover)
			}
			if nb.Clicked() {
				acts.NewTask()
			}
			nb.Children(func() {
				ui.Icon(c, IconPlus).FontSize(15).TextColor(t.Text)
				ui.Text(c, "New task").FontSize(13).Grow(1)
				ui.Text(c, "⌘N").FontSize(11).TextColor(vm.Pal.TextMuted)
			})
			ui.SearchField(c, &vm.Search).Placeholder("Search tasks…")
		})
		ui.Scroll(c).Grow(1).Padding(2, 8, 12).Gap(2).Children(func() {
			threadList(c, vm, acts)
		})
		// The rail's bottom-left corner: the backend switcher, with
		// settings one click beside it (and ⌘, behind it).
		ui.Row(c).Padding(10).Gap(6).AlignItems(ui.Center).BorderWidth(1, 0, 0, 0).
			BorderColor(vm.Pal.Border).Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).Children(func() {
				agentPicker(c, vm, acts)
			})
			gear := ui.ButtonBase(c).Label("Settings").Tooltip("Settings (⌘,)").
				Size(28, 28).Radius(8).Center().Cursor(ui.CursorPointer)
			if gear.Hovered() {
				gear.Background(vm.Pal.Hover)
			}
			if gear.Clicked() {
				acts.OpenSettings()
			}
			gear.Children(func() {
				ui.Icon(c, IconGear).FontSize(15).TextColor(vm.Pal.TextMuted)
			})
		})
	})
}

// threadList builds the groups and rows of the task list.
func threadList(c *ui.Context, vm *SidebarVM, acts SidebarActions) {
	groups := GroupThreads(vm.Threads, vm.Search)
	for _, g := range groups {
		ui.Text(c, g.Title).FontSize(11).FontWeight(600).TextColor(vm.Pal.TextMuted).
			Padding(10, 8, 4).LetterSpacing(0.4)
		for _, th := range g.Threads {
			threadRow(c, vm, acts, th)
		}
	}
	if len(groups) == 0 {
		msg := "No tasks match."
		if strings.TrimSpace(vm.Search) == "" {
			msg = "No tasks yet — start one above."
		}
		ui.Text(c, msg).FontSize(12).TextColor(vm.Pal.TextMuted).Padding(8)
	}
}

// threadRow is one task: its title, its age and, on hover, a delete
// button; right-click renames or deletes it.
func threadRow(c *ui.Context, vm *SidebarVM, acts SidebarActions, th ThreadVM) {
	row := ui.ButtonBase(c).Key(th.ID).Fill().Padding(6, 8).Radius(8).Gap(8).Cursor(ui.CursorPointer)
	chosen := th.ID == vm.Current
	if chosen {
		row.Background(vm.Pal.Sel)
	} else if row.Hovered() {
		row.Background(vm.Pal.Hover)
	}
	if row.Clicked() {
		acts.OpenThread(th.ID)
	}
	row.ContextMenu(func(m *ui.Menu) {
		if m.Item("Rename").Chosen() {
			acts.RenameThread(th.ID)
		}
		m.Separator()
		if m.Item("Delete").Chosen() {
			acts.DeleteThread(th.ID)
		}
	})
	row.Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			title := th.Title
			if title == "" {
				title = "New task"
			}
			if th.AgentEmoji != "" {
				ui.Text(c, th.AgentEmoji).FontSize(12)
			}
			ui.Text(c, title).SingleLine().FontSize(13)
			sub := RelTime(th.Updated)
			if th.BackendTag != "" {
				sub += " · " + th.BackendTag
			}
			ui.Text(c, sub).SingleLine().FontSize(11).TextColor(vm.Pal.TextMuted)
		})
		// The delete button exists while the row is hovered — and for as
		// long as the pointer stays on the button itself: a hover-gated
		// build would remove it between a click's press and release.
		if row.Hovered() {
			vm.HoverRow = th.ID
		}
		if vm.HoverRow == th.ID || row.Hovered() {
			del := ui.ButtonBase(c).Label("Delete task").Tooltip("Delete").Size(24, 24).Radius(6).Center()
			if del.Hovered() {
				del.Background(vm.Pal.CardHover)
				vm.HoverRow = th.ID
			} else if !row.Hovered() {
				vm.HoverRow = ""
			}
			if del.Clicked() {
				acts.DeleteThread(th.ID)
			}
			del.Children(func() { ui.Icon(c, IconTrash).FontSize(13).TextColor(vm.Pal.TextMuted) })
		} else if chosen && th.Running {
			ui.Spinner(c)
		}
	})
}

// projectSwitcher is the button at the top of the rail: the active
// project's name and path, opening a picker of projects and the
// "Add project…" entry backed by the native folder dialog.
func projectSwitcher(c *ui.Context, vm *SidebarVM, acts SidebarActions) {
	t := c.Theme()
	sw := ui.ButtonBase(c).Fill().Padding(7, 10).Radius(8).Gap(8).Cursor(ui.CursorPointer)
	if sw.Hovered() || vm.ProjectMenu {
		sw.Background(vm.Pal.Hover)
	}
	if sw.Clicked() {
		vm.ProjectMenu = !vm.ProjectMenu
	}
	sw.Children(func() {
		ui.Icon(c, IconFolder).FontSize(14).TextColor(t.Text)
		ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
			ui.Text(c, filepath.Base(vm.Workdir)).SingleLine().FontSize(12.5).FontWeight(600)
			ui.Text(c, vm.Workdir).SingleLine().FontSize(10.5).TextColor(vm.Pal.TextMuted)
		})
		chev := ui.Icon(c, IconChevDown).FontSize(12).TextColor(vm.Pal.TextMuted)
		if vm.ProjectMenu {
			chev.Rotate(180)
		}
	})
	ui.Popover(c, sw, &vm.ProjectMenu, func() {
		// Popover's panel already paints the look; more here would read
		// as a second border inside it.
		ui.Column(c).Width(300).Padding(4).Children(func() {
			ui.Text(c, "PROJECTS").FontSize(10).FontWeight(600).TextColor(vm.Pal.TextMuted).
				Padding(6, 10, 2).LetterSpacing(0.6)
			for _, p := range vm.Projects {
				p := p
				active := p.ID == vm.ActiveProject
				row := ui.ButtonBase(c).Padding(6, 10).Radius(7).Gap(8).Cursor(ui.CursorPointer)
				if row.Hovered() {
					row.Background(vm.Pal.CardHover)
				}
				if row.Clicked() {
					vm.ProjectMenu = false
					acts.SwitchProject(p.ID)
				}
				row.ContextMenu(func(m *ui.Menu) {
					if m.Item("Remove project").Chosen() {
						acts.RemoveProject(p.ID)
					}
				})
				row.Children(func() {
					ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
						ui.Text(c, filepath.Base(p.Path)).SingleLine().FontSize(12.5)
						ui.Text(c, p.Path).SingleLine().FontSize(10.5).TextColor(vm.Pal.TextMuted)
					})
					if active {
						ui.Icon(c, IconCheck).FontSize(13).TextColor(t.Text)
					}
				})
			}
			ui.Box(c).Height(1).Margin(4, 6).Background(vm.Pal.Border)
			add := ui.ButtonBase(c).Padding(7, 10).Radius(7).Gap(8).Cursor(ui.CursorPointer)
			if add.Hovered() {
				add.Background(vm.Pal.CardHover)
			}
			if add.Clicked() {
				acts.PickProjectDir()
			}
			add.Children(func() {
				ui.Icon(c, IconPlus).FontSize(13).TextColor(t.TextMuted)
				ui.Text(c, "Add project…").FontSize(12.5).Grow(1)
			})
		})
	})
}

// agentPicker is the rail's bottom-left switcher: the agent a new task
// binds, showing each profile's resolved backend-model. It replaces the
// old backend switcher — the backend is an agent attribute now, and the
// composer's picker, the home launcher and this rail bind the same
// selection (spec/agents.md).
func agentPicker(c *ui.Context, vm *SidebarVM, acts SidebarActions) {
	pick := ui.ButtonBase(c).Padding(6, 8).Radius(8).Gap(8).Cursor(ui.CursorPointer)
	if pick.Hovered() {
		pick.Background(vm.Pal.Hover)
	}
	if pick.Clicked() {
		vm.AgentMenu = !vm.AgentMenu
	}
	pick.Children(func() {
		ui.Icon(c, IconBot).FontSize(14).TextColor(vm.Pal.TextMuted)
		ui.Text(c, vm.AgentName).SingleLine().FontSize(12).Grow(1)
		chev := ui.Icon(c, IconChevDown).FontSize(12).TextColor(vm.Pal.TextMuted)
		if vm.AgentMenu {
			chev.Rotate(180)
		}
	})
	ui.Popover(c, pick, &vm.AgentMenu, func() {
		closeMenu := func() { vm.AgentMenu = false }
		ui.Column(c).Width(260).Padding(4).Children(func() {
			ui.Text(c, "AGENT").FontSize(10).FontWeight(600).TextColor(vm.Pal.TextMuted).
				Padding(6, 10, 2).LetterSpacing(0.6)
			for i := range vm.Agents {
				ag := &vm.Agents[i]
				row := ui.ButtonBase(c).Fill().Padding(6, 10).Radius(7).Gap(8).Cursor(ui.CursorPointer)
				if row.Hovered() {
					row.Background(vm.Pal.CardHover)
				}
				if row.Clicked() {
					closeMenu()
					acts.SetAgent(ag.ID)
				}
				row.Children(func() {
					ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
						ui.Text(c, strings.TrimSpace(ag.Emoji+" "+ag.Name)).SingleLine().FontSize(12.5)
						ui.Text(c, ag.Sub).SingleLine().FontSize(10.5).TextColor(vm.Pal.TextMuted)
					})
					if ag.ID == vm.AgentID {
						ui.Icon(c, IconCheck).FontSize(13).TextColor(c.Theme().Text)
					}
				})
			}
			grp := ui.ButtonBase(c).Fill().Padding(6, 10).Radius(7).Gap(8).Cursor(ui.CursorPointer)
			if grp.Hovered() {
				grp.Background(vm.Pal.CardHover)
			}
			if grp.Clicked() {
				closeMenu()
				acts.NewGroup()
			}
			grp.Children(func() {
				ui.Icon(c, IconBot).FontSize(13).TextColor(c.Theme().TextMuted)
				ui.Text(c, "New group…").FontSize(12.5).Grow(1)
			})
			mng := ui.ButtonBase(c).Fill().Padding(6, 10).Radius(7).Gap(8).Cursor(ui.CursorPointer)
			if mng.Hovered() {
				mng.Background(vm.Pal.CardHover)
			}
			if mng.Clicked() {
				closeMenu()
				acts.OpenSettings()
			}
			mng.Children(func() {
				ui.Icon(c, IconGear).FontSize(13).TextColor(c.Theme().TextMuted)
				ui.Text(c, "Manage agents…").FontSize(12.5).Grow(1)
			})
		})
	})
}

// MenuItem is one row of the app's own popover menus.
func MenuItem(c *ui.Context, label string, checked bool, pal Palette) bool {
	t := c.Theme()
	it := ui.Row(c).Padding(6, 10).Radius(6).Gap(8).Cursor(ui.CursorPointer)
	if it.Hovered() {
		it.Background(pal.CardHover)
	}
	chosen := it.Clicked()
	it.Children(func() {
		ui.Text(c, label).FontSize(12.5).Grow(1).MinWidth(0)
		if checked {
			ui.Icon(c, IconCheck).FontSize(13).TextColor(t.Text)
		}
	})
	return chosen
}

// MenuItemDisabled is a menu row grayed out while its action is missing.
func MenuItemDisabled(c *ui.Context, label string, checked, disabled bool, pal Palette) bool {
	t := c.Theme()
	it := ui.Row(c).Padding(6, 10).Radius(6).Gap(8)
	if disabled {
		it.Disabled(true)
	} else {
		it.Cursor(ui.CursorPointer)
		if it.Hovered() {
			it.Background(pal.CardHover)
		}
	}
	chosen := it.Clicked()
	it.Children(func() {
		col := t.Text
		if disabled {
			col = t.TextMuted
		}
		ui.Text(c, label).FontSize(12.5).Grow(1).MinWidth(0).TextColor(col)
		if checked {
			ui.Icon(c, IconCheck).FontSize(13).TextColor(t.Text)
		}
	})
	return chosen && !disabled
}
