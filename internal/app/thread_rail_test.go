package app

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestRailJump(t *testing.T) {
	a := newTestApp(t)
	now := time.Now()
	th := &Thread{ID: "t1", ProjectID: "default", Title: "Rail test", Updated: now}
	// The rail anchors the conversation's skeleton: every user message
	// plus each turn's last reply. Six user/reply pairs give six dashes.
	for i := 0; i < 6; i++ {
		th.Messages = append(th.Messages,
			Message{ID: fmt.Sprintf("u%d", i), Role: "user", Text: fmt.Sprintf("ask number %d", i), At: now},
			Message{ID: fmt.Sprintf("m%d", i), Role: "assistant", Text: fmt.Sprintf("reply number %d", i), At: now})
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

// TestRailAnchorsSkipMidTurnReplies: the rail anchors the skeleton —
// every user message and each turn's LAST reply. A relay member's
// mid-turn replies leave no dash, or the work-log buries the map.
func TestRailAnchorsSkipMidTurnReplies(t *testing.T) {
	a := newTestApp(t)
	now := time.Now()
	th := &Thread{ID: "t1", ProjectID: "default", Title: "Relay rail", Updated: now}
	th.Messages = []Message{
		{ID: "u0", Role: "user", Text: "ask", At: now},
		{ID: "m1", Role: "assistant", AgentID: "a", Text: "first member", At: now},
		{ID: "m2", Role: "assistant", AgentID: "b", Text: "second member", At: now},
		{ID: "m3", Role: "assistant", AgentID: "c", Text: "wrap-up", At: now},
		{ID: "u1", Role: "user", Text: "again", At: now},
		{ID: "m5", Role: "assistant", AgentID: "a", Text: "solo answer", At: now},
	}
	a.threads = append(a.threads, th)
	a.current = "t1"

	// The snapshot's items ARE what the rail renders from: assert the
	// anchor set, not the pixels.
	a.update(func() {
		snap := a.transcriptVM(th)
		var ids []string
		for i := range snap.Messages {
			m := &snap.Messages[i]
			if m.Role == "user" || i+1 == len(snap.Messages) || snap.Messages[i+1].Role == "user" {
				ids = append(ids, m.ID)
			}
		}
		if strings.Join(ids, ",") != "u0,m3,u1,m5" {
			t.Fatalf("anchors = %v, want u0,m3,u1,m5 (m1/m2 are mid-turn)", ids)
		}
	})
}
