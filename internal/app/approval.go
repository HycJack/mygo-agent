package app

import (
	"context"
	"errors"

	"mygo-agent/internal/harness"
	"mygo-agent/internal/harness/builtin"
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
		reason := "cancelled"
		if errors.Is(context.Cause(ctx), builtin.ErrApprovalTimedOut) {
			reason = "approval timed out"
		}
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
