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

- **Every adapter shares one deadline.** `harness.ApprovalContext(ctx,
  timeout)` (harness/approvals.go) derives the context an approval waits
  under, and `harness.ApprovalReason(ctx)` names how it ended. The
  builtin loop, codex and claude all go through it, so a card behaves the
  same whichever backend asked — and the Host reads one cause instead of
  reaching into an adapter package to recognise it. The deadline is
  `harness.DefaultApprovalTimeout` (10 minutes) unless the host overrides
  it. An adapter that passed the bare run context could leave a card
  waiting forever while the run stayed alive; that is the bug this shared
  helper exists to prevent. Every adapter routes its wait through it,
  including codex.
- Expiry is a denial with reason "approval timed out"; the callback's
  context expires at the same instant with `ErrApprovalTimedOut` as its
  cause, so the host settles its card instead of leaving it pending.
- Run cancellation (stop button) denies any pending request with reason
  "cancelled"; it never leaves a request waiting on a dead run.
- A host with no `OnApproval` handler denies `ask` calls up front with
  "no approval handler" — `ask` never silently allows. pi is the
  documented exception: it runs its tools under its own permissions and
  exposes no callback, so it has no host approval cards at all
  (cli-backends.md).

## Redaction

`cli.Redact` masks credential-shaped text before it reaches a card, a
transcript or a log. It matches a header or assignment whose key names a
secret (`Authorization:`, `--api-key`, `API_KEY=`, …) plus the
well-known literal token formats, and keeps the key so the ask stays
readable.

- **Redact before truncate, never after.** A cut can hide the tail of a
  secret behind the ellipsis, and a card is persisted into the thread
  file — not just drawn once.
- The rules stay narrow on purpose. A "anything long" heuristic would
  redact ordinary paths and hashes and make the ask unreadable, which is
  its own safety problem: a user who cannot read the call cannot judge
  it.
- For a literal token format the vendor marker (`sk-`, `AKIA`, `xoxb-`)
  is kept and the body masked — for these formats the "prefix" is the
  credential, so keeping the match would defeat the point.

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
