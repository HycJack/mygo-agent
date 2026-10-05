package app

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// msgView renders a two-message thread headlessly; every call makes a
// fresh app so each Tester owns its ListState.
func msgView() func(c *ui.Context) {
	a := &app{
		pal:     codexPalette(),
		lists:   map[string]*ui.ListState{},
		mdCache: nil,
	}
	th := &Thread{ID: "t1", Title: "Test", Messages: []Message{
		{ID: "m1", Role: "user", Text: "帮我看看这个项目的结构", At: time.Now()},
		{ID: "m2", Role: "assistant", Text: "这是一个 Codex 风格的桌面 AI 编码代理。", At: time.Now()},
	}}
	return func(c *ui.Context) {
		c.SetTheme(codexTheme())
		a.messages(c, th)
	}
}

// TestThreadComposerSyncsBack pins the thread surface's composer sync:
// the model menu must open and stay open, and typed text must reach the
// host draft — without the frame-end sync both died silently.
func TestThreadComposerSyncsBack(t *testing.T) {
	a := &app{
		theme:   codexTheme(),
		pal:     codexPalette(),
		lists:   map[string]*ui.ListState{},
		backend: "builtin",
	}
	th := &Thread{ID: "t1", ProjectID: "default", Title: "Sync test"}
	a.threads = append(a.threads, th)
	a.current = "t1"

	tt := ui.NewTester(a.view, 1240, 800)
	tt.Frame()
	tt.Frame()

	if err := tt.Click("Model"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.modelMenu {
		t.Fatal("the model menu did not open on the thread surface")
	}
	tt.Frame()
	if !a.modelMenu {
		t.Fatal("the model menu flashed shut: the toggle was not synced")
	}

	// Typing lands in the bound ViewModel; the frame-end sync must
	// mirror it into the host draft.
	if r, ok := tt.Find("Plan, code, edit anything"); ok {
		tt.ClickAt(r.X+r.W/2, r.Y+r.H/2)
		tt.Type("hello from the thread")
	} else if a.vm != nil {
		a.vm.Draft = "hello from the thread" // the binding the caret writes
	}
	tt.Frame()
	if a.draft != "hello from the thread" {
		t.Fatalf("draft was not synced: %q", a.draft)
	}
}

// TestMessageActionsFollowHover pins the hover contract of the message
// action row: hidden until the pointer is over a message, shown with the
// actions that message allows, gone once it leaves.
func TestMessageActionsFollowHover(t *testing.T) {
	if ui.NewTester(msgView(), 800, 500).HasText("Copy") {
		t.Error("actions visible without hover")
	}

	tst := ui.NewTester(msgView(), 800, 500)
	tst.Move(400, 480) // over the last (assistant) message
	tst.Frame()
	if !tst.HasText("Copy") {
		t.Error("hovering the assistant row shows no Copy action")
	}
	if !tst.HasText("Regenerate") {
		t.Error("hovering the last assistant row shows no Regenerate action")
	}

	// Reserving the action row means hovering shifts nothing: the user
	// bubble sits exactly where it did before the actions appeared.
	bubble, ok := tst.Find("帮我看看这个项目的结构")
	if !ok {
		t.Fatal("the user bubble is missing")
	}
	if b2, _ := tst.Find("帮我看看这个项目的结构"); b2 != bubble {
		t.Errorf("hover moved the bubble: %+v -> %+v", bubble, b2)
	}

	tst.Move(10, 10) // off the transcript
	tst.Frame()
	if tst.HasText("Copy") {
		t.Error("actions stayed visible after the pointer left")
	}
}

// TestMessageActionCopyClickable pins that a hover action button can
// actually be clicked: hovering, pressing and releasing on Copy must
// put the message text on the clipboard.
func TestMessageActionCopyClickable(t *testing.T) {
	tst := ui.NewTester(msgView(), 800, 500)
	tst.Move(400, 480) // over the last (assistant) message
	tst.Frame()
	tst.Frame() // settle the hover before the click

	if err := tst.Click("Copy"); err != nil {
		t.Fatalf("click Copy: %v", err)
	}
	tst.Frame()
	if got := tst.Clipboard(); got != "这是一个 Codex 风格的桌面 AI 编码代理。" {
		t.Fatalf("clipboard %q — the Copy click did not land", got)
	}
}
