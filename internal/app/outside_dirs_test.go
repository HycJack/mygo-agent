//go:build !windows

package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"mygo-agent/internal/harness"
)

// waitForCard polls for a pending approval card, the way a user waits to
// see one. The run is on another goroutine and holds the app lock while
// it appends, so the read goes through update.
func waitForCard(t *testing.T, a *app, th *Thread) string {
	t.Helper()
	var id string
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		a.update(func() {
			for _, b := range th.Messages[0].Blocks {
				if b.Type == "approval" && b.Running && b.ApprovalID != "" {
					id = b.ApprovalID
				}
			}
		})
		if id != "" {
			return id
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no approval card appeared")
	return ""
}

// The host side of the outside-workspace question: one card naming every
// directory, and a decision that settles it. The wait is by design — the
// adapter cannot spawn until the answer exists — so every case here
// drives it from another goroutine and answers the way a click does.

// A decision that arrives the way the UI delivers one: the card is
// pending, and resolveApproval settles it.
func TestOutsideDirsCardSettlesOnTheDecision(t *testing.T) {
	dir := t.TempDir()
	a, th := newCLITestApp(t, dir, "claude", writeScript(t, dir, "fake-claude", fakeClaude))
	th.Messages[0].Running = true

	decided := make(chan bool, 1)
	go func() {
		decided <- a.waitForOutsideDirs(context.Background(), th, 0, harness.OutsideDirRequest{
			Workdir: dir, Dirs: []string{"/tmp/one", "/tmp/two"},
		})
	}()

	id := waitForCard(t, a, th)
	// One card for both directories.
	var cards int
	a.update(func() {
		for _, b := range th.Messages[0].Blocks {
			if b.Type == "approval" {
				cards++
			}
		}
	})
	if cards != 1 {
		t.Fatalf("%d cards for two directories, want one", cards)
	}
	// The card names what is being asked about.
	a.update(func() {
		for _, b := range th.Messages[0].Blocks {
			if b.ApprovalID == id && !strings.Contains(b.Text, "/tmp/one") {
				t.Fatalf("the card does not name the directory: %q", b.Text)
			}
		}
	})
	resolve(a, id, harness.ApprovalDecision{Approved: true})
	select {
	case ok := <-decided:
		if !ok {
			t.Fatal("an allowed card reported a refusal")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the ask did not settle after the decision")
	}
}

// Many directories still make one card, and the card says how many it is
// holding back, so the user can see the ask is larger than usual.
func TestOutsideDirsCardShortensALongList(t *testing.T) {
	dir := t.TempDir()
	a, th := newCLITestApp(t, dir, "claude", writeScript(t, dir, "fake-claude", fakeClaude))
	th.Messages[0].Running = true

	go a.waitForOutsideDirs(context.Background(), th, 0, harness.OutsideDirRequest{
		Workdir: dir,
		Dirs:    []string{"/a", "/b", "/c", "/d", "/e", "/f"},
	})
	waitForCard(t, a, th) // the card is there before its text is read
	var text string
	a.update(func() {
		for _, b := range th.Messages[0].Blocks {
			if b.Type == "approval" && b.Running {
				text = b.Text
			}
		}
	})
	if !strings.Contains(text, "3 more") {
		t.Fatalf("card %q does not say how many directories it is holding back", text)
	}
}

// A refusal is final and total. The grant goes on a command line before
// the process starts, so a "no" has to leave the run exactly as confined
// as it was — not "everything but the last one", which is what a
// per-directory decision would tempt an implementation into.
func TestOutsideDirsRefusalGrantsNothing(t *testing.T) {
	dir := t.TempDir()
	a, th := newCLITestApp(t, dir, "claude", writeScript(t, dir, "fake-claude", fakeClaude))
	th.Messages[0].Running = true

	decided := make(chan bool, 1)
	go func() {
		decided <- a.waitForOutsideDirs(context.Background(), th, 0, harness.OutsideDirRequest{
			Workdir: dir, Dirs: []string{"/tmp/one", "/tmp/two"},
		})
	}()

	id := waitForCard(t, a, th)
	resolve(a, id, harness.ApprovalDecision{Reason: "not this project"})
	select {
	case ok := <-decided:
		if ok {
			t.Fatal("a refused ask reported approval")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the refusal did not settle the ask")
	}
	// The card records the refusal, so the thread says why the run could
	// not reach the directory.
	a.update(func() {
		for _, b := range th.Messages[0].Blocks {
			if b.ApprovalID == id && b.Running {
				t.Fatal("the card is still pending after a decision")
			}
			if b.ApprovalID == id && b.Output != "not this project" {
				t.Fatalf("the card records %q, want the refusal reason", b.Output)
			}
		}
	})
	// And nothing is left waiting behind it.
	if pendingApprovals(a) != 0 {
		t.Fatalf("%d approvals left pending after the refusal", pendingApprovals(a))
	}
}
