package app

// One journey through the app's main surface, driven the way a user
// drives it: pick an agent from the home launcher, send a message, read
// the attributed reply, open the task's trace, open settings from the
// header gear, close everything again. The pieces have their own tests;
// this one pins that they work together.

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestE2EJourney(t *testing.T) {
	a, srv, bodies := panelFixture(t, "alpha")
	defer srv.Close()
	a.agents = []Agent{
		{ID: "default", Name: "Default"},
		{ID: "ag-review", Name: "Reviewer", Emoji: "🔍"},
		{ID: "ag-team", Name: "Team", Panel: []string{"Reviewer"}},
	}
	a.current = ""
	tt := ui.NewTester(a.view, 1440, 900)
	tt.Frame()

	// Home: pick the Reviewer card — the composer autofocuses.
	if err := tt.Click("Reviewer"); err != nil {
		t.Fatalf("pick the reviewer card: %v", err)
	}
	if a.activeAgent != "ag-review" {
		t.Fatalf("active agent = %q", a.activeAgent)
	}
	a.setDraft("review the retry logic")
	tt.Frame()
	if err := tt.Click("Send"); err != nil {
		t.Fatalf("send: %v", err)
	}
	if len(a.threads) != 1 {
		t.Fatalf("the send did not create a task")
	}
	th := a.threads[0]
	if th.AgentID != "ag-review" {
		t.Fatalf("the task bound %q", th.AgentID)
	}
	waitUntil(t, tt, func() bool {
		done := false
		a.update(func() { done = !a.isRunning(th.ID) && len(th.Messages) > 1 && !th.Messages[1].Running })
		return done
	})
	if th.Messages[1].Text != "alpha" || th.Messages[1].AgentID != "ag-review" {
		t.Fatalf("reply: %q by %q", th.Messages[1].Text, th.Messages[1].AgentID)
	}
	// The header names who answers.
	if !tt.HasText("Reviewer") {
		t.Fatal("the header meta does not name the bound agent")
	}

	// The task menu's trace lands in the viewer.
	if err := tt.Click("Task actions"); err != nil {
		t.Fatalf("open the task menu: %v", err)
	}
	tt.Frame()
	if err := tt.Click("View trace"); err != nil {
		t.Fatalf("open the trace: %v", err)
	}
	tt.Frame()
	if !a.viewerIsOpen() || a.viewer.Kind != "text" {
		t.Fatalf("the trace viewer did not open: %+v", a.viewer)
	}
	if !strings.Contains(a.viewer.Raw, "turn") || !strings.Contains(a.viewer.Raw, "tool calls") {
		t.Fatalf("the trace does not show the turn summary: %q", a.viewer.Raw)
	}

	// Escape gives the viewer way.
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if a.viewerIsOpen() {
		t.Fatal("escape did not close the viewer")
	}

	// Settings from the header gear; the agents half is in there.
	if err := tt.Click("Settings"); err != nil {
		t.Fatalf("open settings: %v", err)
	}
	tt.Frame()
	if !a.settingsOpen {
		t.Fatal("the gear did not open settings")
	}
	// Select the panel agent in the rail — its resolved subtitle is
	// unique on screen — and its form shows the relay editor.
	if err := tt.Click("panel · 1 agents"); err != nil {
		t.Fatalf("select the team agent: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Panel members (group relay)") {
		t.Fatal("the agent form's panel editor is missing")
	}

	// ⌘, works from anywhere too — and Escape closes.
	tt.Key(ui.Cmd, ui.KeyComma)
	tt.Frame()
	if !a.settingsOpen {
		t.Fatal("cmd+comma did not open settings")
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if a.settingsOpen {
		t.Fatal("escape did not close settings")
	}

	// The whole journey produced exactly one provider call — a solo
	// agent, no relay.
	if len(*bodies) != 1 {
		t.Fatalf("provider calls = %d", len(*bodies))
	}
}
