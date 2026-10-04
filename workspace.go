package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

// skippedDirs are directories that never help when browsing a workspace.
var skippedDirs = map[string]bool{
	".git": true, "node_modules": true, "target": true, "dist": true,
	".next": true, "__pycache__": true, ".venv": true, "venv": true,
	".gradle": true, ".idea": true, ".cache": true, "build": true,
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

// workspace is the panel on the right of the window: the file tree of
// the workdir, and the git working tree's changes.
func (a *app) workspace(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Width(240).Shrink(0).Background(a.pal.SidebarBG).
		BorderWidth(0, 0, 0, 1).BorderColor(a.pal.Border).Children(func() {
		// The strip that drags the window.
		ui.Row(c).Height(max(c.TitleBar().Height, 40)).PaddingX(12).DragWindow().AlignItems(ui.Center).Gap(6).Children(func() {
			ui.Icon(c, icFolder).FontSize(13).TextColor(a.pal.TextMuted)
			ui.Text(c, filepath.Base(a.workdir)).SingleLine().FontSize(12).FontWeight(600).TextColor(t.Text).Grow(1)
			rb := ui.ButtonBase(c).Label("Refresh workspace").Tooltip("Refresh").Size(22, 22).Radius(6).Center()
			if rb.Hovered() {
				rb.Background(a.pal.Hover)
			}
			if rb.Clicked() {
				a.dirCache = map[string][]fsNode{}
				a.refreshGit()
			}
			rb.Children(func() { ui.Icon(c, icRefresh).FontSize(12).TextColor(a.pal.TextMuted) })
			a.iconToggle(c, "Hide workspace (⌘E)", true, icPanelRight, func() { a.wsOpen = false })
		})
		ui.Scroll(c).Grow(1).Padding(2, 6, 12).Children(func() {
			a.treeSection(c)
			ui.Box(c).Height(10)
			a.changesSection(c)
		})
	})
}

// treeSection is the file tree of the workspace.
func (a *app) treeSection(c *ui.Context) {
	ui.Text(c, "FILES").FontSize(10.5).FontWeight(600).TextColor(a.pal.TextMuted).
		Padding(8, 8, 4).LetterSpacing(0.6)
	ui.Tree(c, func() {
		a.treeItems(c, a.workdir, 0)
	})
}

// treeItems builds one directory level of the tree, recursing into the
// directories the user opened.
func (a *app) treeItems(c *ui.Context, dir string, depth int) {
	for _, node := range a.listDir(dir) {
		node := node
		if node.Dir {
			open := a.dirs[node.Path]
			openPtr := &open
			ui.TreeItem(c, node.Name, openPtr, func() {
				a.treeItems(c, node.Path, depth+1)
			}).Clicked()
			a.dirs[node.Path] = open
			continue
		}
		if ui.TreeItem(c, node.Name, nil, nil).Clicked() {
			a.openFile(node.Path)
		}
	}
}

// changesSection lists the working tree's changes from git status.
func (a *app) changesSection(c *ui.Context) {
	t := c.Theme()
	n := len(a.gitFiles)
	ui.Row(c).Padding(8, 8, 4).Gap(6).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "CHANGES").FontSize(10.5).FontWeight(600).TextColor(a.pal.TextMuted).LetterSpacing(0.6)
		if n > 0 {
			ui.Textf(c, "%d", n).FontSize(10).Padding(1, 5).Radius(8).
				Background(a.pal.Card).TextColor(t.TextMuted)
		}
		ui.Spacer(c)
		if a.gitErr != "" {
			ui.Icon(c, icAlert).FontSize(11).TextColor(a.pal.Warning).Tooltip(a.gitErr)
		}
	})
	if n == 0 {
		msg := "Clean working tree."
		if a.gitErr != "" {
			msg = "Not a git repository."
		}
		ui.Text(c, msg).FontSize(11.5).TextColor(a.pal.TextMuted).Padding(2, 8, 6)
		return
	}
	for _, ch := range a.gitFiles {
		ch := ch
		row := ui.ButtonBase(c).Padding(3, 8).Radius(6).Gap(7).Cursor(ui.CursorPointer)
		if row.Hovered() {
			row.Background(a.pal.Hover)
		}
		if row.Clicked() {
			a.openGitDiff(ch)
		}
		row.Children(func() {
			ui.Text(c, ch.Code).Font("monospace").FontSize(10.5).FontWeight(600).Width(18).TextColor(a.changeColor(ch.Code))
			ui.Text(c, ch.Path).SingleLine().FontSize(12).Grow(1).MinWidth(0)
		})
	}
}

// changeLabel maps the porcelain code to the letter shown in the list;
// "??" (untracked) would read as a question mark, so it becomes "A".
func changeLabel(code string) string {
	if strings.HasPrefix(code, "?") {
		return "A"
	}
	return code
}

// changeMeaning names a porcelain code, for the hover tooltip.
func changeMeaning(code string) string {
	switch {
	case strings.HasPrefix(code, "?"):
		return "untracked (new file)"
	case strings.Contains(code, "A"):
		return "added"
	case strings.Contains(code, "D"):
		return "deleted"
	case strings.Contains(code, "R"):
		return "renamed"
	case strings.Contains(code, "M"):
		return "modified"
	default:
		return code
	}
}

// changeColor colors the porcelain code of a change.
func (a *app) changeColor(code string) ui.Color {
	switch {
	case strings.HasPrefix(code, "?"):
		return a.pal.TextMuted
	case strings.Contains(code, "D"):
		return a.pal.Danger
	case strings.Contains(code, "A"):
		return a.pal.Success
	default:
		return a.pal.Warning
	}
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
				a.update(func() { a.viewer.Err = truncTitle("git diff: "+err.Error(), 200); a.viewer.Loading = false })
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
		lines := parseUnifiedDiff(text)
		add, del := diffStats(lines)
		a.update(func() {
			a.viewer.Lines = lines
			a.viewer.Raw = text
			a.viewer.Sub = sprintStats(add, del)
			a.viewer.Note = note
			a.viewer.Loading = false
		})
	}()
}
