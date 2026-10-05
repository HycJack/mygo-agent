package app

import (
	uipkg "mygo-agent/internal/ui"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// The sidebar renders in internal/ui over a ViewModel snapshot
// (spec/architecture.md); this file is the bridge: snapshot assembly,
// the Actions implementation, and the sync of transient view state.

// sidebarViewModel snapshots the rail's render input.
func (a *app) sidebarViewModel() *uipkg.SidebarVM {
	vm := &uipkg.SidebarVM{
		Version:       a.version,
		Workdir:       a.workdir,
		ActiveProject: a.activeProject,
		Search:        a.search,
		Current:       a.current,
		Backend:       a.backend,
		BackendName:   a.backendLabel(),
		CodexFound:    a.codexPath != "",
		BackendMenu:   a.backendMenu,
		ProjectMenu:   a.projectMenu,
		HoverRow:      a.hoverRow,
		Pal:           a.pal,
	}
	for i := range a.projects {
		p := &a.projects[i]
		vm.Projects = append(vm.Projects, uipkg.ProjectVM{ID: p.ID, Path: p.Path})
	}
	for _, th := range a.threads {
		if th.ProjectID != a.activeProject {
			continue
		}
		t := uipkg.ThreadVM{ID: th.ID, Title: th.Title, Updated: th.Updated,
			Running: th.ID == a.current && a.running}
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
	a.backendMenu = vm.BackendMenu
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

func (h sidebarActions) SetBackend(kind string) {
	h.a.backend = kind
	h.a.saveConfig()
}

func (h sidebarActions) SwitchProject(id string) { h.a.switchProject(id) }
func (h sidebarActions) RemoveProject(id string) { h.a.removeProject(id) }
func (h sidebarActions) PickProjectDir()         { h.a.pickProjectDir() }

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
