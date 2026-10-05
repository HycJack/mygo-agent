package ui

import (
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"
)

// WorkspaceVM is the render input for the right panel: the file tree of
// the workdir and the git working tree's changes. The tree is lazy — the
// view asks the host for one directory level at a time through
// WorkspaceActions.ListDir — and Expanded is a plain snapshot of which
// directories are open; the view never writes it, it reports the toggle
// through WorkspaceActions.ToggleDir.
type WorkspaceVM struct {
	Workdir string
	Width   float32

	GitFiles []ChangeVM
	GitErr   string

	Expanded map[string]bool

	Pal Palette
}

// ChangeVM is one path of `git status --porcelain`.
type ChangeVM struct {
	Code, Path string
}

// FileNode is one entry of a directory listing.
type FileNode struct {
	Name, Path string
	Dir        bool
}

// WorkspaceActions is what the panel calls back for.
type WorkspaceActions interface {
	// Refresh re-reads the tree caches and the git status.
	Refresh()
	// Hide closes the panel.
	Hide()
	// OpenFile shows a file in the viewer.
	OpenFile(path string)
	// OpenChange shows a change's diff in the viewer.
	OpenChange(ch ChangeVM)
	// ListDir returns one directory level, directories first.
	ListDir(dir string) []FileNode
	// ToggleDir opens a closed directory or closes an open one. The tree
	// reports the toggle here instead of writing the snapshot's Expanded
	// map, so the host stays the one home for it (spec/architecture.md).
	ToggleDir(path string)
}

// skippedDirs never help when browsing a workspace.
var skippedDirs = map[string]bool{
	".git": true, "node_modules": true, "target": true, "dist": true,
	".next": true, "__pycache__": true, ".venv": true, "venv": true,
	".gradle": true, ".idea": true, ".cache": true, "build": true,
}

// Workspace renders the right panel: the title strip, the file tree and
// the git changes. Width and placement stay with the host.
func Workspace(c *ui.Context, vm *WorkspaceVM, acts WorkspaceActions) {
	t := c.Theme()
	ui.Column(c).Width(vm.Width).Shrink(0).Background(vm.Pal.SidebarBG).Children(func() {
		// The strip that drags the window.
		ui.Row(c).Height(max(c.TitleBar().Height, 40)).PaddingX(12).DragWindow().AlignItems(ui.Center).Gap(6).Children(func() {
			ui.Icon(c, IconFolder).FontSize(13).TextColor(vm.Pal.TextMuted)
			ui.Text(c, filepath.Base(vm.Workdir)).SingleLine().FontSize(12).FontWeight(600).TextColor(t.Text).Grow(1)
			rb := ui.ButtonBase(c).Label("Refresh workspace").Tooltip("Refresh").Size(22, 22).Radius(6).Center()
			if rb.Hovered() {
				rb.Background(vm.Pal.Hover)
			}
			if rb.Clicked() {
				acts.Refresh()
			}
			rb.Children(func() { ui.Icon(c, IconRefresh).FontSize(12).TextColor(vm.Pal.TextMuted) })
			hide := ui.ButtonBase(c).Label("Hide workspace (⌘E)").Tooltip("Hide").Size(22, 22).Radius(6).Center()
			if hide.Hovered() {
				hide.Background(vm.Pal.Hover)
			}
			if hide.Clicked() {
				acts.Hide()
			}
			hide.Children(func() { ui.Icon(c, IconPanelRight).FontSize(12).TextColor(vm.Pal.TextMuted) })
		})
		ui.Scroll(c).Grow(1).Padding(2, 6, 12).Children(func() {
			treeSection(c, vm, acts)
			ui.Box(c).Height(10)
			changesSection(c, vm, acts)
		})
	})
}

// treeSection is the file tree of the workspace.
func treeSection(c *ui.Context, vm *WorkspaceVM, acts WorkspaceActions) {
	ui.Text(c, "FILES").FontSize(10.5).FontWeight(600).TextColor(vm.Pal.TextMuted).
		Padding(8, 8, 4).LetterSpacing(0.6)
	ui.Tree(c, func() {
		treeItems(c, vm, acts, vm.Workdir)
	})
}

// treeItems builds one directory level of the tree, recursing into the
// directories the user expanded.
func treeItems(c *ui.Context, vm *WorkspaceVM, acts WorkspaceActions, dir string) {
	nodes := acts.ListDir(dir)
	for _, node := range nodes {
		if node.Dir {
			// The tree item binds a local copy so the disclosure arrow
			// can flip it mid-frame; the change is reported, not
			// written back, so a zero-value VM with no Expanded map is
			// safe.
			open := vm.Expanded[node.Path]
			openPtr := &open
			ui.TreeItem(c, node.Name, openPtr, func() {
				treeItems(c, vm, acts, node.Path)
			}).Clicked()
			if *openPtr != vm.Expanded[node.Path] {
				acts.ToggleDir(node.Path)
			}
			continue
		}
		if ui.TreeItem(c, node.Name, nil, nil).Clicked() {
			acts.OpenFile(node.Path)
		}
	}
}

// changesSection lists the working tree's changes from git status.
func changesSection(c *ui.Context, vm *WorkspaceVM, acts WorkspaceActions) {
	t := c.Theme()
	n := len(vm.GitFiles)
	ui.Row(c).Padding(8, 8, 4).Gap(6).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "CHANGES").FontSize(10.5).FontWeight(600).TextColor(vm.Pal.TextMuted).LetterSpacing(0.6)
		if n > 0 {
			ui.Textf(c, "%d", n).FontSize(10).Padding(1, 5).Radius(8).
				Background(vm.Pal.Card).TextColor(t.TextMuted)
		}
		ui.Spacer(c)
		if vm.GitErr != "" {
			ui.Icon(c, IconAlert).FontSize(11).TextColor(vm.Pal.Warning).Tooltip(vm.GitErr)
		}
	})
	if n == 0 {
		msg := "Clean working tree."
		if vm.GitErr != "" {
			msg = "Not a git repository."
		}
		ui.Text(c, msg).FontSize(11.5).TextColor(vm.Pal.TextMuted).Padding(2, 8, 6)
		return
	}
	for _, ch := range vm.GitFiles {
		ch := ch
		row := ui.ButtonBase(c).Padding(3, 8).Radius(6).Gap(7).Cursor(ui.CursorPointer)
		if row.Hovered() {
			row.Background(vm.Pal.Hover)
		}
		if row.Clicked() {
			acts.OpenChange(ch)
		}
		row.Children(func() {
			ui.Text(c, ch.Code).Font("monospace").FontSize(10.5).FontWeight(600).Width(18).TextColor(changeColor(ch, vm.Pal))
			ui.Text(c, ch.Path).SingleLine().FontSize(12).Grow(1).MinWidth(0)
		})
	}
}

// changeColor colors the porcelain code of a change.
func changeColor(ch ChangeVM, pal Palette) ui.Color {
	switch {
	case strings.HasPrefix(ch.Code, "?"):
		return pal.TextMuted
	case strings.Contains(ch.Code, "D"):
		return pal.Danger
	case strings.Contains(ch.Code, "A"):
		return pal.Success
	default:
		return pal.Warning
	}
}
