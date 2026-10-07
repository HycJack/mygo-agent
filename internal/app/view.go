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

	"slices"
)

// The window frame assembles the shared views from internal/ui around
// host state (spec/architecture.md): shortcuts, the sidebar/workspace
// toggles, the terminal dock, and the bridges that fill ViewModels.

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
	if c.Shortcut(ui.Cmd, ui.KeyComma) {
		a.settingsOpen = true
	}
	if c.Shortcut(0, ui.KeyEscape) {
		// Overlays take Escape first while open; then the viewer gives
		// way, and finally a running agent stops.
		switch {
		case a.viewerOpen:
			a.viewerOpen = false
		case a.currentRunning():
			a.stopThread(a.current)
		}
	}

	ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
		if a.navOpen {
			a.renderSidebar(c, top)
			a.divider(c, &a.navWidth, 1)
		}
		ui.Column(c).Grow(1).MinWidth(0).Background(a.pal.Bg).Children(func() {
			a.renderHeader(c, tb)
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
			a.divider(c, &a.wsWidth, -1)
			a.renderWorkspace(c)
		}
	})

	a.overlays(c)
}

// Divider drag bounds, DIP.
const (
	dividerMin = 180.0
	dividerMax = 520.0
)

// divider is the draggable splitter between the panels: dragging it
// resizes the neighbouring panel (sign decides which side yields). The
// panels' own borders moved here, so the line you see is the handle.
//
// The bounds are the constants above rather than parameters, and they are
// not named min/max: those would shadow the builtins the clamp below is
// written with, and a function that calls itself as min(w, lo) is a
// runtime blowup, not a compile error one reader can see.
func (a *app) divider(c *ui.Context, width *float32, sign float32) {
	div := ui.Column(c).Width(5).Justify(ui.Center)
	div.Cursor(ui.CursorResizeEW)
	if dx, _, ok := div.Dragged(); ok {
		w := *width + sign*dx
		*width = min(max(w, dividerMin), dividerMax)
	}
	line := a.pal.Border
	if div.Hovered() {
		line = a.pal.Hover
	}
	div.Children(func() {
		ui.Box(c).Width(1).Grow(1).Background(line)
	})
}

// mainPane is the header's body: the file viewer while one is open, else
// the home screen or the thread.
func (a *app) mainPane(c *ui.Context) {
	if a.viewerIsOpen() {
		a.renderViewer(c)
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

// renderHeader assembles the header snapshot and renders the shared
// header bar.
func (a *app) renderHeader(c *ui.Context, tb ui.TitleBar) {
	th := a.currentThread()
	vm := &uipkg.HeaderVM{
		Title:    "New task",
		Running:  a.currentRunning(),
		NavOpen:  a.navOpen,
		WsOpen:   a.wsOpen,
		TermOpen: a.termOpen,
		MenuOpen: a.threadMenu,
		Pal:      a.pal,
	}
	if th != nil {
		vm.HasTask = true
		if th.Title != "" {
			vm.Title = th.Title
		}
		// The meta line names who answers: the bound agent when the task
		// has one worth naming, then backend, model and mode.
		vm.Meta = a.threadMeta(th)
		vm.Changed = changedFiles(th)
	}
	uipkg.Header(c, tb, vm, headerActions{a: a, th: th})
	a.threadMenu = vm.MenuOpen // the popover's binding
}

// headerActions adapts *app to ui.HeaderActions for the open task.
type headerActions struct {
	a  *app
	th *Thread
}

func (h headerActions) ToggleNav() { h.a.navOpen = !h.a.navOpen }

func (h headerActions) ToggleWorkspace() {
	h.a.wsOpen = true
	h.a.refreshGit()
}

func (h headerActions) ToggleTerminal() { h.a.toggleTerminal(h.a.uiCtx) }

func (h headerActions) Rename() {
	if h.th == nil {
		return
	}
	h.a.renaming, h.a.renameID, h.a.renameDraft = true, h.th.ID, h.th.Title
}

func (h headerActions) Export() {
	if h.th != nil {
		h.a.exportMarkdown(h.a.uiCtx, h.th)
	}
}

func (h headerActions) Trace() {
	if h.th != nil {
		h.a.openTrace(h.th)
	}
}

// threadMeta is the header's "who answers" line: the bound agent when
// the task has one worth naming, then the resolved backend, model and
// mode — the same resolution a turn will use (spec/agents.md).
func (a *app) threadMeta(th *Thread) string {
	meta := ""
	if ag := a.agentFor(th); ag != nil {
		ov := a.resolveAgent(ag)
		if ag.Name != "Default" {
			meta = strings.TrimSpace(ag.Emoji+" "+ag.Name) + " · "
		}
		mode := uipkg.ModeLabel(min(max(ov.mode, 0), 2))
		return meta + fmt.Sprintf("%s · %s · %s", backendLabels(ov.backend), ov.model, mode)
	}
	return meta + fmt.Sprintf("%s · %s · %s", a.backendLabel(), a.model, uipkg.ModeLabel(a.mode))
}

// backendLabels names a backend key for display.
func backendLabels(backend string) string {
	switch backend {
	case "claude":
		return "Claude Code"
	case "codex":
		return "Codex CLI"
	case "pi":
		return "Pi"
	}
	return "Built-in agent"
}

func (h headerActions) Delete() {
	if h.th != nil {
		h.a.deleteThread(h.a.uiCtx, h.th.ID)
	}
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
	case "claude":
		return "Claude Code"
	case "pi":
		return "Pi"
	default:
		return "Built-in agent"
	}
}

// overlays are the dialogs drawn above everything while open: renaming
// a task, and managing the model providers.
func (a *app) overlays(c *ui.Context) {
	vm := &uipkg.RenameVM{Open: a.renaming, Title: a.renameDraft}
	uipkg.RenameDialog(c, vm, renameActions{a: a})

	// The new-group dialog, rendered above whatever surface opened it.
	a.groupDialogModal(c)
	a.renaming, a.renameDraft = vm.Open, vm.Title
	a.settingsModal(c)
}

// renameActions adapts *app to ui.RenameActions.
type renameActions struct{ a *app }

func (h renameActions) Apply(title string) {
	if th := h.a.byID(h.a.renameID); th != nil {
		th.Title = strings.TrimSpace(title)
		th.Updated = time.Now()
		h.a.saveThread(th)
	}
	h.a.renaming = false
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

// groupDialogModal renders the new-group dialog over the current
// surface and mirrors its bindings back (spec/agents.md).
func (a *app) groupDialogModal(c *ui.Context) {
	if os.Getenv("CLICK_PROBE") == "2" {
		ag := a.agentByID(a.settingsSel)
		t := c.Theme()
		ui.Modal(c, &a.groupDrafting, func() {
			ui.Column(c).Width(420).Gap(14).Children(func() {
				enabled := !slices.Contains(ag.Tools.Disabled, "read_file")
				row := ui.ButtonBase(c).FillWidth().Padding(8, 12).Radius(8).Gap(10).
					AlignItems(ui.Center).Cursor(ui.CursorPointer)
				if row.Clicked() {
					println("PROBE-ROW-FLIP")
				}
				row.Children(func() {
					box := ui.Box(c).Size(16, 16).Radius(4).Center().Shrink(0)
					if enabled {
						box.Background(t.Accent)
					} else {
						box.Background(t.Background).Border(1, t.Border)
					}
					ui.Text(c, "read_file").FontSize(12.5)
				})
				ui.Button(c, "plain-button")
			})
		})
		return
	}
	if os.Getenv("CLICK_PROBE") != "" {
		state := map[string]string{}
		ui.Modal(c, &a.groupDrafting, func() {
			ui.Column(c).Width(420).Gap(14).Children(func() {
				if ui.Button(c, "BTN-A").Clicked() {
					state["A"] = "hit"
				}
				pill := ui.ButtonBase(c).Padding(4, 10).Radius(999)
				pillHit := false
				if pill.Clicked() {
					pillHit = true
				}
				pill.Children(func() { ui.Text(c, "BTN-B").FontSize(12.5) })
				if pillHit {
					state["B"] = "hit"
				}
				nested := ui.Column(c).Gap(6).Children(func() {
					row := ui.ButtonBase(c).FillWidth().Padding(8, 12).Radius(8).Gap(10).
						AlignItems(ui.Center).Cursor(ui.CursorPointer)
					rowHit := false
					if row.Clicked() {
						rowHit = true
					}
					row.Children(func() { ui.Text(c, "BTN-C").FontSize(12.5) })
					if rowHit {
						state["C"] = "hit"
					}
				})
				_ = nested
			})
		})
		for k, v := range state {
			println("CLICK-PROBE", k, v)
		}
		return
	}
	if !a.groupDrafting {
		return
	}
	vm := &uipkg.GroupVM{Open: true, Name: a.groupDraftName, Pal: a.pal}
	for i := range a.agents {
		ag := &a.agents[i]
		vm.Members = append(vm.Members, uipkg.GroupMemberVM{
			ID: ag.ID, Name: ag.Name, Emoji: ag.Emoji, Checked: a.groupDraftOn[ag.ID],
		})
	}
	if len(vm.Members) == 0 {
		vm.Err = "no agents to invite — create agent profiles in settings first"
	} else if len(a.groupDraftOn) == 0 {
		vm.Err = "pick at least one member"
	}
	vm.CanStart = len(a.groupDraftOn) > 0
	uipkg.GroupDialog(c, vm, groupActions{a: a})
	// Mirror the name only. The checkboxes reach the host through
	// ToggleMember the moment they flip — mirroring the frame's stale
	// Checked flags here would wipe the very tick the user just made.
	a.groupDraftName = vm.Name
}

// groupActions adapts *app to ui.GroupActions.
type groupActions struct{ a *app }

func (h groupActions) ToggleMember(id string, on bool) {
	if h.a.groupDraftOn == nil {
		h.a.groupDraftOn = map[string]bool{}
	}
	if on {
		h.a.groupDraftOn[id] = true
	} else {
		delete(h.a.groupDraftOn, id)
	}
}

// Start creates the group profile and a thread bound to it: the chat
// begins here, with the composer focused (spec/agents.md).
func (h groupActions) Start() { h.a.startGroupChat() }

func (h groupActions) Cancel() {
	h.a.groupDrafting = false
	clear(h.a.groupDraftOn)
}

// startGroupChat is the new-group dialog's Start: create the group
// profile (panel = the picked members) and a thread bound to it, and
// land in that thread with the composer focused (spec/agents.md).
func (a *app) startGroupChat() {
	members := make([]string, 0, len(a.groupDraftOn))
	for i := range a.agents {
		if a.groupDraftOn[a.agents[i].ID] {
			members = append(members, a.agents[i].Name)
		}
	}
	if len(members) == 0 {
		return
	}
	name := strings.TrimSpace(a.groupDraftName)
	if name == "" {
		name = "Group"
	}
	ag := Agent{ID: "ag-" + uid(), Name: name, Panel: members}
	a.agents = append(a.agents, ag)
	a.activeAgent = ag.ID
	a.groupDrafting = false
	clear(a.groupDraftOn)
	a.saveConfig()
	a.createThread()
	a.focusComposer = true
}

// openGroupDraft opens the new-group dialog with a clean draft.
func (a *app) openGroupDraft() {
	a.groupDrafting = true
	a.groupDraftName = "Group"
	if a.groupDraftOn == nil {
		a.groupDraftOn = map[string]bool{}
	}
	clear(a.groupDraftOn)
}
