package app

import (
	"context"
	"errors"

	"mygo-agent/internal/harness"

	"github.com/egoist/mygo/ui"
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
		if errors.Is(context.Cause(ctx), harness.ErrApprovalTimedOut) {
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

// blockApproval is the approval card: while pending, the redacted summary
// with Allow once / Deny buttons; after the decision, its outcome.
// Approvals bind one call (spec/approvals.md): there is deliberately no
// "always allow" here — durable authority is the permissions config.
func (a *app) blockApproval(c *ui.Context, b *Block) {
	t := c.Theme()
	card := ui.Column(c).Padding(9, 12).Radius(8).Gap(6).AlignItems(ui.Start).
		Border(1, a.pal.Warning.Alpha(0.5)).Background(a.pal.Warning.Alpha(0.06))
	card.Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Start).Children(func() {
			ui.Icon(c, icAlert).FontSize(14).TextColor(a.pal.Warning)
			ui.Text(c, b.Text).Font("monospace").FontSize(12).Grow(1).MinWidth(0).TextColor(t.Text)
			if b.Running {
				ui.Spinner(c)
			}
		})
		if b.Running {
			ui.Row(c).Gap(8).Children(func() {
				allow := ui.ButtonBase(c).Label("Allow once").Tooltip("Allow this one call").
					Padding(5, 12).Radius(7).Border(1, a.pal.Success).Cursor(ui.CursorPointer)
				if allow.Hovered() {
					allow.Background(a.pal.Success.Alpha(0.12))
				}
				if allow.Clicked() {
					a.resolveApproval(b.ApprovalID, harness.ApprovalDecision{Approved: true})
				}
				allow.Children(func() {
					ui.Icon(c, icCheck).FontSize(12).TextColor(a.pal.Success)
					ui.Text(c, "Allow once").FontSize(12).TextColor(a.pal.Success)
				})
				deny := ui.ButtonBase(c).Label("Deny").Tooltip("Deny this call").
					Padding(5, 12).Radius(7).Border(1, a.pal.Border).Cursor(ui.CursorPointer)
				if deny.Hovered() {
					deny.Background(a.pal.Hover)
				}
				if deny.Clicked() {
					a.resolveApproval(b.ApprovalID, harness.ApprovalDecision{Reason: "the user denied this call"})
				}
				deny.Children(func() {
					ui.Icon(c, icX).FontSize(12).TextColor(a.pal.TextMuted)
					ui.Text(c, "Deny").FontSize(12).TextColor(t.Text)
				})
			})
			return
		}
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			switch b.Exit {
			case 0:
				ui.Icon(c, icCheck).FontSize(12).TextColor(a.pal.Success)
				ui.Text(c, "Allowed").FontSize(11.5).TextColor(a.pal.TextMuted)
			default:
				ui.Icon(c, icX).FontSize(12).TextColor(a.pal.Danger)
				ui.Text(c, "Denied").FontSize(11.5).TextColor(a.pal.TextMuted)
				if b.Output != "" {
					ui.Text(c, b.Output).FontSize(11.5).TextColor(a.pal.TextMuted).Grow(1).MinWidth(0).SingleLine()
				}
			}
		})
	})
}
