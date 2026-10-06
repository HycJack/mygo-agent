package app

import (
	"strings"

	uipkg "mygo-agent/internal/ui"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// The sidebar renders in internal/ui over a ViewModel snapshot
// (spec/architecture.md); this file is the bridge: snapshot assembly,
// the Actions implementation, and the sync of transient view state.

// sidebarViewModel returns the persistent rail ViewModel, refreshing
// the host-owned fields in place. vm.Search is the single binding
// target — the search field writes keystrokes into it and syncSidebar
// mirrors it into a.search at frame end.
func (a *app) sidebarViewModel() *uipkg.SidebarVM {
	if a.sidebarVM == nil {
		a.sidebarVM = &uipkg.SidebarVM{}
	}
	vm := a.sidebarVM
	vm.Version = a.version
	vm.Workdir = a.workdir
	vm.ActiveProject = a.activeProject
	vm.Current = a.current
	// The agent switcher's state: the composer's picker, the home
	// launcher and this rail bind one selection (spec/agents.md).
	vm.AgentID = a.activeAgentID()
	vm.AgentName = vm.AgentID
	if ag := a.agentByID(vm.AgentID); ag != nil {
		vm.AgentName = strings.TrimSpace(ag.Emoji + " " + ag.Name)
	}
	vm.Agents = vm.Agents[:0]
	for i := range a.agents {
		ag := &a.agents[i]
		vm.Agents = append(vm.Agents, uipkg.AgentVM{ID: ag.ID, Name: ag.Name, Emoji: ag.Emoji, Sub: a.agentSub(ag)})
	}
	vm.AgentMenu = a.agentMenu
	vm.ProjectMenu = a.projectMenu
	vm.HoverRow = a.hoverRow
	vm.Width = a.navWidth
	vm.Pal = a.pal
	vm.Projects = vm.Projects[:0]
	for i := range a.projects {
		p := &a.projects[i]
		vm.Projects = append(vm.Projects, uipkg.ProjectVM{ID: p.ID, Path: p.Path})
	}
	vm.Threads = vm.Threads[:0]
	for _, th := range a.threads {
		if th.ProjectID != a.activeProject {
			continue
		}
		t := uipkg.ThreadVM{ID: th.ID, Title: th.Title, Updated: th.Updated,
			Running: a.isRunning(th.ID)}
		if a.search != "" {
			t.Search = th.searchHaystack()
		}
		if ag := a.agentFor(th); ag != nil {
			t.AgentEmoji = ag.Emoji
		}
		if th.CodexID != "" {
			t.BackendTag = "codex"
		}
		vm.Threads = append(vm.Threads, t)
	}
	return vm
}

// syncSidebar copies the rail's transient view state back to the host.
func (a *app) syncSidebar(vm *uipkg.SidebarVM) {
	a.search = vm.Search
	a.agentMenu = vm.AgentMenu
	a.projectMenu = vm.ProjectMenu
	a.hoverRow = vm.HoverRow
}

// sidebarActions adapts *app to ui.SidebarActions.
type sidebarActions struct{ a *app }

func (h sidebarActions) NewTask() {
	h.a.newTask()
	h.a.winTitle()
}

func (h sidebarActions) OpenThread(id string) {
	h.a.current = id
	h.a.focusComposer = true
	h.a.winTitle()
}

func (h sidebarActions) DeleteThread(id string) {
	// The rail's context menu runs on the main thread; the deletion path
	// wants a ui.Context for its undo toast, so it re-enters through the
	// deferred update the same way the inline delete button did.
	if c := h.a.uiCtx; c != nil {
		h.a.deleteThread(c, id)
	}
}

func (h sidebarActions) RenameThread(id string) {
	th := h.a.byID(id)
	if th == nil {
		return
	}
	h.a.renaming, h.a.renameID, h.a.renameDraft = true, th.ID, th.Title
}

func (h sidebarActions) SetAgent(id string) { h.a.setActiveAgent(id) }

func (h sidebarActions) SwitchProject(id string) { h.a.switchProject(id) }
func (h sidebarActions) RemoveProject(id string) { h.a.removeProject(id) }
func (h sidebarActions) PickProjectDir()         { h.a.pickProjectDir() }
func (h sidebarActions) OpenSettings()           { h.a.settingsOpen = true }

// renderSidebar assembles the snapshot, renders, and syncs back.
func (a *app) renderSidebar(c *ui.Context, top float32) {
	vm := a.sidebarViewModel()
	uipkg.Sidebar(c, vm, sidebarActions{a: a}, top)
	a.syncSidebar(vm)
}

// winTitle refreshes the window title from the current thread.
func (a *app) winTitle() {
	if a.win == nil {
		return
	}
	title := "New task"
	if th := a.currentThread(); th != nil && th.Title != "" {
		title = th.Title
	}
	a.win.SetTitle("Codex — " + title)
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
