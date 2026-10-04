package main

import (
	"fmt"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestRailJump(t *testing.T) {
	a := newTestApp(t)
	now := time.Now()
	th := &Thread{ID: "t1", ProjectID: "default", Title: "Rail test", Updated: now}
	for i := 0; i < 12; i++ {
		th.Messages = append(th.Messages, Message{
			ID: fmt.Sprintf("m%d", i), Role: "assistant", Text: fmt.Sprintf("reply number %d", i), At: now,
		})
	}
	a.threads = append(a.threads, th)
	a.current = "t1"

	tt := ui.NewTester(a.view, 1240, 320) // short window: the thread overflows
	tt.Frame()
	tt.Frame()

	st := a.listState("t1")
	if !st.AtEnd() {
		t.Fatal("a fresh chat should follow its end")
	}
	// Clicking a rail dot jumps to that message: the list stops
	// following the end once the user jumps away.
	if err := tt.Click("Jump to: reply number 0"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if st.AtEnd() {
		t.Fatal("the jump did not take effect")
	}
}
