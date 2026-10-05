package app

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	uipkg "mygo-agent/internal/ui"

	"mygo-agent/internal/harness"
	"mygo-agent/internal/harness/cli"

	"github.com/egoist/mygo/ui"
)

// skippedDirs are directories that never help when browsing a workspace.
var skippedDirs = map[string]bool{
	".git": true, "node_modules": true, "target": true, "dist": true,
	".next": true, "__pycache__": true, ".venv": true, "venv": true,
	".gradle": true, ".idea": true, ".cache": true, "build": true,
}

// The workspace panel renders in internal/ui over a ViewModel snapshot
// (spec/architecture.md); this file is the bridge: the snapshot, the
// Actions implementation, and the host-side data work (lazy dir listing
// with its cache, git status, git diffs for the viewer).

// workspaceViewModel snapshots the panel's render input. Expanded
// aliases the host's dir map, so the view's toggles land in host state.
func (a *app) workspaceViewModel() *uipkg.WorkspaceVM {
	vm := &uipkg.WorkspaceVM{
		Workdir:  a.workdir,
		GitErr:   a.gitErr,
		Expanded: a.dirs,
		Pal:      a.pal,
	}
	for _, ch := range a.gitFiles {
		vm.GitFiles = append(vm.GitFiles, uipkg.ChangeVM{Code: ch.Code, Path: ch.Path})
	}
	return vm
}

// workspaceActions adapts *app to ui.WorkspaceActions.
type workspaceActions struct{ a *app }

func (h workspaceActions) Refresh() {
	h.a.dirCache = map[string][]fsNode{}
	h.a.refreshGit()
}

func (h workspaceActions) Hide()                { h.a.wsOpen = false }
func (h workspaceActions) OpenFile(path string) { h.a.openFile(path) }
func (h workspaceActions) OpenChange(ch uipkg.ChangeVM) {
	h.a.openGitDiff(gitChange{Code: ch.Code, Path: ch.Path})
}

func (h workspaceActions) ListDir(dir string) []uipkg.FileNode {
	nodes := h.a.listDir(dir)
	out := make([]uipkg.FileNode, len(nodes))
	for i, n := range nodes {
		out[i] = uipkg.FileNode{Name: n.Name, Path: n.Path, Dir: n.Dir}
	}
	return out
}

// renderWorkspace assembles the snapshot and renders the panel.
func (a *app) renderWorkspace(c *ui.Context) {
	uipkg.Workspace(c, a.workspaceViewModel(), workspaceActions{a: a})
}

// listDir lists dir's entries for the tree, directories first, cached
// from one readdir per directory.
func (a *app) listDir(dir string) []fsNode {
	if nodes, ok := a.dirCache[dir]; ok {
		return nodes
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		a.dirCache[dir] = nil
		return nil
	}
	var nodes []fsNode
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() && skippedDirs[name] {
			continue
		}
		if name == ".DS_Store" {
			continue
		}
		nodes = append(nodes, fsNode{Name: name, Path: filepath.Join(dir, name), Dir: e.IsDir()})
		if len(nodes) >= 500 {
			break
		}
	}
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].Dir != nodes[j].Dir {
			return nodes[i].Dir
		}
		return strings.ToLower(nodes[i].Name) < strings.ToLower(nodes[j].Name)
	})
	a.dirCache[dir] = nodes
	return nodes
}

// refreshGit re-reads `git status --porcelain` in the background, scoped
// to the repository that contains the workdir (the workdir may be well
// inside it), so paths and diffs address the root.
func (a *app) refreshGit() {
	if _, err := exec.LookPath("git"); err != nil {
		a.gitErr = "git not found"
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		root, err := exec.CommandContext(ctx, "git", "-C", a.workdir, "rev-parse", "--show-toplevel").Output()
		if err != nil {
			a.update(func() {
				a.gitErr = "not a git repository"
				a.gitFiles = nil
			})
			return
		}
		repo := strings.TrimSpace(string(root))
		out, err := exec.CommandContext(ctx, "git", "-C", repo, "status", "--porcelain").Output()
		a.update(func() {
			if err != nil {
				a.gitErr = "not a git repository"
				a.gitFiles = nil
				return
			}
			a.gitRoot = repo
			a.gitErr = ""
			a.gitFiles = parsePorcelain(string(out))
		})
	}()
}

// parsePorcelain reads the lines of git status --porcelain.
func parsePorcelain(out string) []gitChange {
	var changes []gitChange
	for _, line := range strings.Split(out, "\n") {
		if len(line) < 4 {
			continue
		}
		code := strings.TrimSpace(line[:2])
		path := line[3:]
		if i := strings.LastIndex(path, " -> "); i >= 0 {
			path = path[i+4:]
		}
		changes = append(changes, gitChange{Code: code, Path: strings.TrimSpace(path)})
	}
	return changes
}

// gitRepo resolves the repository root in the caller's goroutine, for
// the paths the background refresh has not stored yet.
func gitRepo(workdir string) string {
	out, err := exec.Command("git", "-C", workdir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// openGitDiff shows a change as a diff in the viewer, loading it in the
// background: `git diff` for tracked files, the content itself for
// untracked ones.
func (a *app) openGitDiff(ch gitChange) {
	a.viewer = viewerState{
		Path: "git:" + ch.Path, Kind: "diff", Title: ch.Path, Loading: true,
	}
	a.viewerOpen = true
	code := ch.Code
	go func() {
		repo := a.gitRoot
		if repo == "" {
			repo = gitRepo(a.workdir)
		}
		if repo == "" {
			a.update(func() {
				a.viewer.Err = "not a git repository"
				a.viewer.Loading = false
			})
			return
		}
		var text, note string
		if strings.HasPrefix(code, "?") {
			data, err := os.ReadFile(filepath.Join(repo, ch.Path))
			if err != nil {
				a.update(func() { a.viewer.Err = err.Error(); a.viewer.Loading = false })
				return
			}
			lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
			if len(lines) > 3000 {
				lines = lines[:3000]
				note = "showing the first 3,000 lines of the new file"
			}
			var b bytes.Buffer
			for _, l := range lines {
				b.WriteString("+" + l + "\n")
			}
			text = b.String()
		} else {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			out, err := exec.CommandContext(ctx, "git", "-C", repo, "diff", "--", ch.Path).Output()
			cancel()
			if err != nil {
				a.update(func() { a.viewer.Err = cli.Trunc("git diff: "+err.Error(), 200); a.viewer.Loading = false })
				return
			}
			text = string(out)
			if strings.TrimSpace(text) == "" {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				out, err := exec.CommandContext(ctx, "git", "-C", repo, "diff", "--cached", "--", ch.Path).Output()
				cancel()
				if err == nil {
					text = string(out)
				}
			}
			if strings.TrimSpace(text) == "" {
				text = "@@ no unstaged or staged diff for " + ch.Path + "\n"
			}
		}
		lines := harness.ParseUnifiedDiff(text)
		add, del := harness.DiffStats(lines)
		a.update(func() {
			a.viewer.Lines = lines
			a.viewer.Raw = text
			a.viewer.Sub = sprintStats(add, del)
			a.viewer.Note = note
			a.viewer.Loading = false
		})
	}()
}
