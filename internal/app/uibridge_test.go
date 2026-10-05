package app

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// TestHomeComposerAcceptsTyping is the regression test for the
// per-frame ViewModel rebuild: keystrokes land in the bound draft,
// survive the next frame, and flow to host state. (The text area's
// content is not a ui.Text node, so the assertions read the draft, not
// the screen.)
func TestHomeComposerAcceptsTyping(t *testing.T) {
	a := newTestApp(t)
	tt := ui.NewTester(a.view, 1240, 800)
	tt.SetFocused(true)
	tt.Type("fix the bug")

	if a.draft != "fix the bug" {
		t.Fatalf("keystrokes did not reach host state: %q", a.draft)
	}
	tt.Frame() // the next frame must not drop the text
	if a.vm == nil || a.vm.Draft != "fix the bug" {
		t.Fatalf("typed text lost across frames: %+v", a.vm)
	}
	if a.draft != "fix the bug" {
		t.Fatalf("host draft drifted: %q", a.draft)
	}
}

// TestSidebarSearchAcceptsTyping proves the same fix for the rail's
// search field.
func TestSidebarSearchAcceptsTyping(t *testing.T) {
	a := newTestApp(t)
	now := mkTime()
	a.threads = []*Thread{
		{ID: "t1", ProjectID: "default", Title: "Fix the parser", Updated: now},
		{ID: "t2", ProjectID: "default", Title: "Write the docs", Updated: now},
	}
	tt := ui.NewTester(a.view, 1240, 800)

	// The search field binds to the persistent rail ViewModel; simulate
	// the keystrokes landing there and verify the frame applies the
	// filter.
	tt.Frame() // ensure the rail VM exists
	if a.sidebarVM == nil {
		t.Fatal("the sidebar view model was not built")
	}
	a.sidebarVM.Search = "parser"
	tt.Frame()

	if a.search != "parser" {
		t.Fatalf("search keystrokes did not reach host state: %q", a.search)
	}
	if tt.HasText("Write the docs") {
		t.Fatal("the filter did not hide the unrelated task")
	}
}

// TestSendClearsTheComposer proves the host clearing the draft
// propagates into the persistent ViewModel, or the sent text would
// stick in the input.
func TestSendClearsTheComposer(t *testing.T) {
	a := newTestApp(t)
	tt := ui.NewTester(a.view, 1240, 800)

	tt.Type("explain this")
	tt.Frame()
	if a.draft != "explain this" {
		t.Fatalf("draft not synced: %q", a.draft)
	}
	if err := tt.Click("Send"); err != nil {
		// Enter also sends.
		tt.TypeKey(0, ui.KeyEnter, "\n")
	}
	waitUntil(t, tt, func() bool { return !a.running })
	tt.Frame()

	if a.draft != "" {
		t.Fatalf("send did not clear the host draft: %q", a.draft)
	}
	if a.vm != nil && a.vm.Draft != "" {
		t.Fatalf("send did not clear the composer text: %q", a.vm.Draft)
	}
}

func mkTime() time.Time { return time.Now() }
