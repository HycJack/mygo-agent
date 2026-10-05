# CLI backends: native approval protocols

The codex and Claude Code backends do not map the approval mode onto CLI
permission flags alone; they speak the CLIs' native approval protocols so a
pending tool decision reaches this app's approval cards (approvals.md). The
reference implementations are OpenAgentCore's codex adapter
(`apps/daemon/internal/agent/codex/`) and its Claude SDK adapter.

## Mode mapping (normative)

| Mode | codex (app-server) | claude CLI |
| --- | --- | --- |
| read-only | `sandbox: "read-only"`, `approvalPolicy: "never"` | `--permission-mode default`; the app **denies every** `can_use_tool` |
| agent | `sandbox: "workspace-write"`, `approvalPolicy: "on-request"` | `--permission-mode default`; every `can_use_tool` forwards to an approval card |
| full | `sandbox: "danger-full-access"`, `approvalPolicy: "never"` | `--permission-mode bypassPermissions` |

In codex agent mode the sandbox is the boundary; codex sends approval
requests when the model wants to *escalate beyond* the sandbox. In claude
agent mode there is no engine sandbox, so the approval card is the boundary —
the same role the built-in agent's cards play.

## codex app-server protocol

Spawn `codex app-server --stdio`; one fresh process per turn; JSON-RPC 2.0
over NDJSON. Sequence: `initialize` (clientInfo, `capabilities.experimentalApi=true`)
→ `thread/start` or `thread/resume` (params below) → `turn/start`
(`threadId`, `input:[{type:"text",text}]`) → notifications stream → exit.

- `thread/start` params: `cwd`, `model`, `approvalPolicy` (bare string
  `"never"|"on-request"|"on-failure"|"untrusted"`), `sandbox` — the v0.141+
  kebab string `"read-only"|"workspace-write"|"danger-full-access"`. Sending
  the legacy `sandboxPolicy` object makes codex **silently fall back to
  read-only**; keep the kebab string.
- `thread/resume` params: `threadId`, `cwd`, `approvalPolicy`, `sandbox`.
- The thread id arrives in the `thread/start` result (`thread.id`) and again
  in the `thread/started` notification; it is persisted as the thread's
  `CodexID` for resume.
- Subscribed notifications: `thread/started`, `turn/started`,
  `turn/completed`, `turn/failed`, `item/started`, `item/completed`,
  `item/agentMessage/delta`, `item/reasoning/textDelta`,
  `item/reasoning/summaryTextDelta`, `item/commandExecution/outputDelta`,
  `thread/tokenUsage/updated`, `error`. Item shapes match the exec `--json`
  item model and map to the same cards.
- Approval server-requests (method, has `id`):
  - `item/commandExecution/requestApproval` — params carry `command`, `cwd`,
    `reason`, `itemId`. Reply `{"decision": "accept"|"decline"}`.
  - `item/fileChange/requestApproval` — params carry `grantRoot`, `reason`.
    Reply `{"decision": ...}`.
  - `item/permissions/requestApproval` — a request to widen the permission
    profile. This app replies `{"permissions":{},"scope":"turn"}` (a deny)
    and surfaces a note; granting profile escalation is not a card flow.
- Decisions ride the JSON-RPC **response** for the server-request's `id`.
  A pending request is answered exactly once: the user's decision, or
  `decline` after 10 minutes (approvals.md timeout rule), or `decline` when
  the run is cancelled. Handlers are registered before `turn/start`, so a
  request can never arrive unhandled.

## claude stream-json control protocol

Spawn `claude -p --output-format stream-json --verbose --input-format
stream-json` plus `--model` / `--resume <id>` as today. One turn per
process. The prompt is written as one input line:

`{"type":"user","message":{"role":"user","content":[{"type":"text","text":…}]}}`

stdin stays open until the `result` event (or an error) arrives, then closes.

- The session id arrives in the `system`/`init` event (`session_id`), as
  today.
- Permission requests arrive as control requests:
  `{"type":"control_request","request_id":…,"request":{"subtype":
  "can_use_tool","tool_name":…,"input":{…}}}`. The app replies on stdin:
  - allow: `{"type":"control_response","response":{"subtype":"success",
    "request_id":…,"response":{"behavior":"allow","updatedInput":<input>}}}`
    — `updatedInput` echoes the received input object.
  - deny: `…{"behavior":"deny","message":<reason>}`.
- Any other server-initiated control subtype is answered with
  `{"subtype":"error","request_id":…,"error":"unsupported"}` — never left
  hanging: an unanswered control request stalls the CLI turn forever.
- In read-only mode every `can_use_tool` is denied with a fixed message
  naming the mode (the model reads it as the tool result).

## Invariants (extending approvals.md)

1. The approval mode decides the CLI launch flags **and** the app's
   behavior on approval requests; the two can never disagree (the table
   above is generated from one Mode switch).
2. Every approval request is answered exactly once under every path:
   decision, timeout, cancellation. An unanswered request is a hung CLI.
3. CLI approvals reuse the built-in approval cards, the same 10-minute
   timeout, and the no-"always allow" rule.
4. The session id (`CodexID` / `ClaudeID`) is persisted before the turn's
   first output so a crash mid-turn still resumes.
