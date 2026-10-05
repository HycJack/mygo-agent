# Approvals

An approval is the host's yes/no on one prepared tool call whose permission
resolved to `ask`. The shape follows the two reference projects:

- From OpenAgentCore: a request/decision pair with a bounded timeout that
  **denies** on expiry — never approval by silence — and handlers always
  registered so the loop cannot hang waiting.
- From agent-foundation: decisions bind exactly one call; "approve once" is
  literal; durable authority lives in declarative rules (permissions.md),
  never in an interactive decision.

## Request

```go
type ApprovalRequest struct {
    Call    ToolCall // the prepared call being asked about
    Summary string   // bounded, redacted presentation: tool + target
    Reason  string   // why policy asked, from a fixed vocabulary
}
```

- The request travels from the agent loop to the host through a single
  callback (`OnApproval`), which receives a context that expires exactly
  when the loop stops waiting — the `ApprovalTimeout` deadline or run
  cancellation. The loop blocks on the answer; the host owns rendering.
  There is no second channel and no polling.
- The summary is derived by host code from the call's parsed arguments:
  command line, file path, or server/tool name — MCP calls append a
  bounded, single-line view of their arguments so the user is not
  approving blind. Credentials, bearer tokens and long payloads are
  redacted or bounded, never rendered wholesale.

## Decision

```go
type ApprovalDecision struct {
    Approved bool
    Reason   string // the denial reason shown to the model; empty when approved
}
```

- Outcomes: **allow this call once**, or **deny with a reason**. There is no
  "always allow" decision — that is what `permissions.rules` are for.
- A denial becomes the tool result: bounded text naming the tool and the
  reason (or "denied" when the reason is empty). The model can read it and
  choose differently; the harness does not retry.

## Timing

- The loop waits on `OnApproval` for at most `ApprovalTimeout` (default
  10 minutes). Expiry is a denial with reason "approval timed out"; the
  callback's context expires at the same instant with `ErrApprovalTimedOut`
  as its cause, so the host settles its card instead of leaving it
  pending.
- Run cancellation (stop button) denies any pending request with reason
  "cancelled"; it never leaves a request waiting on a dead run.
- A host with no `OnApproval` handler denies `ask` calls up front with
  "no approval handler" — `ask` never silently allows.

## Invariants

1. Model output cannot create an approval. Only the host's decision channel
   can, and it answers one call id.
2. An approval binds exactly one prepared call. A second call with the same
   arguments asks again; approval state is never reused, cached by
   similarity, or persisted.
3. Approval never widens anything but the gate: an approved call still runs
   under its mode's sandbox boundary, and file writes stay confined.
4. Timeout, cancellation and missing handler all land in the same place — a
   settled denial recorded as the tool result — so the loop always makes
   progress.
5. The pending request is visible to the user while it waits (the approval
   card) and its outcome stays in the thread transcript after the decision.
6. The card never outlives the loop's wait. When the callback's context
   expires the card records the cause — "approval timed out" or
   "cancelled" — and stops accepting decisions; a decision delivered at
   the same instant as the deadline may be dropped either way, never
   mis-recorded as an execution that did not happen.
