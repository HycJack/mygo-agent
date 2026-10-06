package app

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"mygo-agent/internal/harness"
)

// waitTurn waits for the running reply at index at to finish — the real
// completion signal, unlike the run registry, which dispatch populates. Reads
// go through a.update so they are serialized with the run's writes.
func waitTurn(t *testing.T, a *app, th *Thread, at int) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		done := false
		a.update(func() {
			done = at < len(th.Messages) && !th.Messages[at].Running
		})
		if done {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timed out waiting for the turn to finish")
}

// pendingApprovalID waits for a pending approval card on the reply and
// returns its id, polling through a.update like the run's own writes.
func pendingApprovalID(t *testing.T, a *app, th *Thread) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		id := ""
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
	t.Fatal("no pending approval card appeared")
	return ""
}

// resolve routes a decision through a.update, as the main thread would.
func resolve(a *app, callID string, d harness.ApprovalDecision) {
	a.update(func() { a.resolveApproval(callID, d) })
}

// pendingApprovals counts the unresolved approvals under the same lock.
func pendingApprovals(a *app) int {
	n := 0
	a.update(func() { n = len(a.approvals) })
	return n
}

// TestBuiltinReadOnlyDeniesShell proves the mode gates the built-in
// backend (spec/permissions.md): a read-only run denies the shell tool
// with a denial the model can read, and the denial lands in the ChatLog
// the next turn sees.
func TestBuiltinReadOnlyDeniesShell(t *testing.T) {
	dir := t.TempDir()
	a, _ := builtinFixture(t, dir)
	a.mode = 0
	th := &Thread{ID: "t1", ProjectID: "p1"}
	th.Messages = []Message{{ID: "m0", Role: "assistant", Running: true}}
	a.threads = append(a.threads, th)
	a.current = "t1"

	go runBackend(a, th, "run the tool", 0)
	waitTurn(t, a, th, 0)

	denied := false
	toolMsg := ""
	a.update(func() {
		for _, b := range th.Messages[0].Blocks {
			if b.Type == "command" && b.Exit == 1 && strings.Contains(b.Output, "permission denied") {
				denied = true
			}
		}
		for _, c := range th.ChatLog {
			if c.Role == "tool" {
				toolMsg = fmt.Sprint(c.Content)
			}
		}
	})
	if !denied {
		t.Fatalf("read-only did not deny the shell: %+v", th.Messages[0].Blocks)
	}
	if !strings.Contains(toolMsg, "permission denied") {
		t.Fatalf("the denial is not in the transcript: %q", toolMsg)
	}
}

// TestBuiltinApprovalFlow drives one ask → approve → execute round trip
// through the real card: the run suspends on a pending approval card, the
// Allow once button resolves it, the command runs, the card settles.
func TestBuiltinApprovalFlow(t *testing.T) {
	dir := t.TempDir()
	a, _ := builtinFixture(t, dir)
	a.mode = 2 // full access; the rule alone forces the ask
	a.permRules = harness.Rules{"bash": harness.PermAsk}
	th := &Thread{ID: "t1", ProjectID: "p1"}
	th.Messages = []Message{{ID: "m0", Role: "assistant", Running: true}}
	a.threads = append(a.threads, th)
	a.current = "t1"

	go runBackend(a, th, "run the tool", 0)

	// The approval card appears and the run waits on it.
	id := pendingApprovalID(t, a, th)
	if pendingApprovals(a) != 1 {
		t.Fatalf("pending approvals: %d, want 1", pendingApprovals(a))
	}
	resolve(a, id, harness.ApprovalDecision{Approved: true})
	waitTurn(t, a, th, 0)

	ran, stuck := false, false
	a.update(func() {
		for _, b := range th.Messages[0].Blocks {
			if b.Type == "command" && b.Exit == 0 && strings.Contains(b.Output, "hello-from-tool") {
				ran = true
			}
			if b.Type == "approval" && b.Running {
				stuck = true
			}
		}
	})
	if stuck {
		t.Fatal("the approval card never settled")
	}
	if !ran {
		t.Fatalf("the approved command did not run: %+v", th.Messages[0].Blocks)
	}
	if pendingApprovals(a) != 0 {
		t.Fatalf("approvals not cleaned up: %d", pendingApprovals(a))
	}
}

// TestBuiltinApprovalDenyHalts proves a denial settles as the tool result
// and the loop keeps going: the model sees why and the turn completes.
func TestBuiltinApprovalDenyHalts(t *testing.T) {
	dir := t.TempDir()
	a, _ := builtinFixture(t, dir)
	a.mode = 2
	a.permRules = harness.Rules{"bash": harness.PermAsk}
	th := &Thread{ID: "t1", ProjectID: "p1"}
	th.Messages = []Message{{ID: "m0", Role: "assistant", Running: true}}
	a.threads = append(a.threads, th)
	a.current = "t1"

	go runBackend(a, th, "run the tool", 0)

	id := pendingApprovalID(t, a, th)
	resolve(a, id, harness.ApprovalDecision{Reason: "not this time"})
	waitTurn(t, a, th, 0)

	denied := false
	a.update(func() {
		for _, b := range th.Messages[0].Blocks {
			if b.Type == "command" && b.Exit == 1 && strings.Contains(b.Output, "not this time") {
				denied = true
			}
		}
	})
	if !denied {
		t.Fatalf("the denial is not the tool result: %+v", th.Messages[0].Blocks)
	}
}

// TestBuiltinApprovalTimeoutSettlesCard proves the timeout path records
// itself on the card (spec/approvals.md): the loop denies after its
// deadline and the card reads "approval timed out" instead of staying
// pending or claiming the user allowed anything.
func TestBuiltinApprovalTimeoutSettlesCard(t *testing.T) {
	dir := t.TempDir()
	a, _ := builtinFixture(t, dir)
	a.mode = 2
	a.permRules = harness.Rules{"bash": harness.PermAsk}
	th := &Thread{ID: "t1", ProjectID: "p1"}
	th.Messages = []Message{{ID: "m0", Role: "assistant", Running: true}}
	a.threads = append(a.threads, th)
	a.current = "t1"
	a.approvalTimeout = 50 * time.Millisecond

	go runBackend(a, th, "run the tool", 0)
	waitTurn(t, a, th, 0)

	timedOut, stuck := false, false
	a.update(func() {
		for _, b := range th.Messages[0].Blocks {
			if b.Type == "command" && b.Exit == 1 && strings.Contains(b.Output, "approval timed out") {
				timedOut = true
			}
			if b.Type == "approval" && b.Running {
				stuck = true
			}
			if b.Type == "approval" && b.Output != "approval timed out" {
				t.Fatalf("the card records %q, want \"approval timed out\"", b.Output)
			}
		}
	})
	if stuck {
		t.Fatal("the approval card is still pending after the timeout")
	}
	if !timedOut {
		t.Fatalf("the timeout denial is not the tool result: %+v", th.Messages[0].Blocks)
	}
	if pendingApprovals(a) != 0 {
		t.Fatalf("approvals not cleaned up: %d", pendingApprovals(a))
	}
}
