package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestWorkspaceToggleDirThroughAction proves the panel's expansion runs
// through the action rather than through a live host map: ToggleDir
// opens and closes the dir, the next frame's snapshot carries it, and
// that snapshot is a copy the view cannot write host state through.
// The tree only ever worked because the view could reach a.dirs; with a
// copy, a silent write into vm.Expanded would show up here.
func TestWorkspaceToggleDirThroughAction(t *testing.T) {
	a := newTestApp(t)
	sub := filepath.Join(a.workdir, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	a.dirs = map[string]bool{}
	a.dirCache = map[string][]fsNode{}
	acts := workspaceActions{a: a}

	acts.ToggleDir(sub)
	if !a.dirs[sub] {
		t.Fatalf("ToggleDir did not open %q: %+v", sub, a.dirs)
	}
	vm := a.workspaceViewModel()
	if !vm.Expanded[sub] {
		t.Fatalf("the next frame's snapshot lost the expansion: %+v", vm.Expanded)
	}

	// The snapshot is a copy, not the host's map: writing it must leave
	// host state alone, and a nil host map must not panic.
	vm.Expanded[sub] = false
	vm.Expanded["not-a-dir"] = true
	if !a.dirs[sub] || a.dirs["not-a-dir"] {
		t.Fatalf("the view wrote host state through the snapshot: %+v", a.dirs)
	}
	delete(vm.Expanded, sub)

	acts.ToggleDir(sub)
	if a.dirs[sub] {
		t.Fatalf("ToggleDir did not close %q: %+v", sub, a.dirs)
	}
	if a.workspaceViewModel().Expanded[sub] {
		t.Fatal("the next frame's snapshot still shows the dir open")
	}
}

// TestWorkspaceTreeClickReachesHostState drives the real panel: clicking
// a directory's disclosure arrow goes through ToggleDir into a.dirs and
// back out in the next frame's snapshot.
func TestWorkspaceTreeClickReachesHostState(t *testing.T) {
	a := newTestApp(t)
	sub := filepath.Join(a.workdir, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	a.dirs = map[string]bool{}
	a.dirCache = map[string][]fsNode{}
	a.wsOpen = true

	tt := ui.NewTester(a.view, 1240, 800)
	tt.Frame()
	tt.Frame()

	// Find returns the item's label rect; the disclosure arrow sits just
	// to its left, inside the row.
	r, ok := tt.Find("sub")
	if !ok {
		t.Fatalf("the tree does not show %q: %v", "sub", tt.Texts())
	}
	tt.ClickAt(r.X-10, r.Y+r.H/2)
	tt.Frame()

	if !a.dirs[sub] {
		t.Fatalf("the disclosure click did not open %q in host state: %+v", sub, a.dirs)
	}
	if !a.workspaceViewModel().Expanded[sub] {
		t.Fatal("the next frame's snapshot does not show the dir open")
	}
	// Its level now lists the dir's contents.
	if !tt.HasText("Clean working tree.") && !tt.HasText("CHANGES") {
		t.Fatalf("the panel stopped rendering: %v", tt.Texts())
	}

	tt.ClickAt(r.X-10, r.Y+r.H/2)
	tt.Frame()
	if a.dirs[sub] {
		t.Fatalf("the disclosure click did not close %q: %+v", sub, a.dirs)
	}
}
