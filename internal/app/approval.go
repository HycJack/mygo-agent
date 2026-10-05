package app

import (
	"context"
	"fmt"
	"strings"

	"mygo-agent/internal/harness"
)

// waitForApproval implements LoopConfig.OnApproval (spec/approvals.md):
// it appends a pending approval card to the running reply and blocks
// until the user decides or the loop stops waiting — the loop's context
// expires on timeout or run cancellation, and its cause settles the card
// truthfully. Every path settles the card.
func (a *app) waitForApproval(ctx context.Context, th *Thread, at int, req harness.ApprovalRequest) harness.ApprovalDecision {
	ch := make(chan harness.ApprovalDecision, 1)
	a.update(func() {
		if m := reply(th, at); m != nil {
			m.Blocks = append(m.Blocks, Block{
				Type:       "approval",
				Text:       req.Summary,
				Running:    true,
				Exit:       -1,
				ApprovalID: req.Call.ID,
			})
			a.approvals[req.Call.ID] = ch
			return
		}
		ch <- harness.ApprovalDecision{Reason: "the thread is gone"}
	})
	select {
	case d := <-ch:
		a.update(func() { a.settleApproval(th, at, req.Call.ID, d) })
		return d
	case <-ctx.Done():
		// The deadline is shared by every adapter (harness.ApprovalContext),
		// so the cause tells a timeout from a cancelled run truthfully.
		reason := harness.ApprovalReason(ctx)
		a.update(func() {
			delete(a.approvals, req.Call.ID)
			a.settleApproval(th, at, req.Call.ID, harness.ApprovalDecision{Reason: reason})
		})
		return harness.ApprovalDecision{Reason: reason}
	}
}

// settleApproval records the decision on the card. Called on the main
// thread through update.
func (a *app) settleApproval(th *Thread, at int, callID string, d harness.ApprovalDecision) {
	m := reply(th, at)
	if m == nil {
		return
	}
	for i := range m.Blocks {
		b := &m.Blocks[i]
		if b.Type == "approval" && b.ApprovalID == callID {
			b.Running = false
			b.Exit = 1
			if d.Approved {
				b.Exit = 0
			} else {
				b.Output = d.Reason
			}
			return
		}
	}
}

// resolveApproval delivers a button click to the waiting call. Runs on
// the main thread; the channel is buffered so the send never blocks.
func (a *app) resolveApproval(callID string, d harness.ApprovalDecision) {
	if ch, ok := a.approvals[callID]; ok {
		delete(a.approvals, callID)
		ch <- d
	}
}

// waitForOutsideDirs answers the one question a turn raises about the
// directories its prompt reached for outside the workspace
// (spec/approvals.md). It rides the ordinary approval card, because it
// is the same decision with the same consequences: the user either
// lets this run reach a directory or it does not.
//
// The card names every directory at once. One prompt naming five files
// in three directories is one thing to decide, and a card per path would
// be five clicks that teach the user to stop reading them. A refusal is
// not a partial grant — the run stays exactly as confined as it was,
// which is what "no" has to mean when the grant is on a command line.
func (a *app) waitForOutsideDirs(ctx context.Context, th *Thread, at int, req harness.OutsideDirRequest) bool {
	dirs := append([]string(nil), req.Dirs...)
	call := harness.ToolCall{ID: "outside-dirs"}
	call.Function.Name = "access"
	call.Function.Arguments = `{"path":"` + strings.Join(dirs, ", ") + `"}`
	decision := a.waitForApproval(ctx, th, at, harness.ApprovalRequest{
		Call: call,
		Summary: "read outside the workspace: " +
			strings.Join(shortenAll(dirs, 3), ", "),
		Reason: "this prompt refers to paths outside the workspace",
	})
	return decision.Approved
}

// shortenAll names at most n directories and counts the rest, so a
// prompt pointing at a dozen trees does not produce a card nobody reads
// — the count is the honest signal that the ask is larger than usual.
func shortenAll(dirs []string, n int) []string {
	if len(dirs) <= n {
		return dirs
	}
	out := append([]string(nil), dirs[:n]...)
	return append(out, fmt.Sprintf("and %d more", len(dirs)-n))
}
