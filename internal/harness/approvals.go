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

// OutsideDirRequest is one question about reaching past the workspace.
//
// It is not a tool approval. A CLI's own path boundary is decided inside
// the CLI, below its tool-permission layer, so the usual can_use_tool
// channel never sees it: the CLI refuses the call and the model hears
// about it second-hand. Asking the user is the Host's job, and it is
// asked once per turn with every directory the prompt pointed at, so a
// prompt naming five paths is one card and not five.
//
// The dirs are the roots the adapter is being asked to add, already
// narrowed to what the prompt actually mentioned and not already inside
// the workspace.
type OutsideDirRequest struct {
	// Workdir is the workspace the run is confined to.
	Workdir string
	// Dirs are the roots outside it that the prompt referred to.
	Dirs []string
}

// OnOutsideDir decides whether a run may reach the directories outside
// its workspace that its prompt named. It runs before the CLI is
// spawned, because granting means adding them to the CLI's own allow
// list — a decision that has to exist on the command line, not midway
// through a turn.
//
// nil means no: an adapter with nowhere to ask runs confined to the
// workspace, which is the safe reading of a boundary it cannot raise.
type OnOutsideDir func(ctx context.Context, req OutsideDirRequest) bool
