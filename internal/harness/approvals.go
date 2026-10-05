package harness

import (
	"context"
	"errors"
	"time"
)

// The approval deadline, shared by every adapter (spec/approvals.md).
// It lives in the protocol root rather than in one adapter so the Host
// settles a timed-out card the same way whichever backend asked: expiry
// is a denial, never a card left pending.

// DefaultApprovalTimeout bounds every wait on the host. Ten minutes is
// long enough for a human to notice an ask and short enough that an
// unattended run cannot sit on a card forever.
const DefaultApprovalTimeout = 10 * time.Minute

// ErrApprovalTimedOut is the cause an approval's context carries when
// its deadline expires. The Host reads the cause to tell "approval timed
// out" from "the run was cancelled" when the context fires either way;
// the denial itself spells the same reason back to the model.
var ErrApprovalTimedOut = errors.New("approval timed out")

// ApprovalContext derives the context one approval wait runs under. A
// non-positive timeout falls back to DefaultApprovalTimeout. Expiry sets
// ErrApprovalTimedOut as the cause so a Host can settle the card
// truthfully instead of guessing from a bare context error.
func ApprovalContext(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		timeout = DefaultApprovalTimeout
	}
	return context.WithTimeoutCause(ctx, timeout, ErrApprovalTimedOut)
}

// ApprovalReason reports the settled reason for a wait that ended because
// its context fired: a timeout, or a cancellation of some other origin —
// a stopped run, or a parent deadline the adapter inherited.
func ApprovalReason(ctx context.Context) string {
	if errors.Is(context.Cause(ctx), ErrApprovalTimedOut) {
		return "approval timed out"
	}
	return "cancelled"
}
