# CLI backends: native approval protocols

The codex and Claude Code backends do not map the approval mode onto CLI
permission flags alone; they speak the CLIs' native approval protocols so a
pending tool decision reaches this app's approval cards (approvals.md). The
reference implementations are OpenAgentCore's codex adapter
(`apps/daemon/internal/agent/codex/`) and its Claude SDK adapter.

## Mode mapping (normative)

| Mode | codex (app-server) | claude CLI | pi CLI |
| --- | --- | --- | --- |
| read-only | `sandbox: "read-only"`, `approvalPolicy: "never"` | `--permission-mode default`; the app **denies every** `can_use_tool`; directories the prompt names outside the workspace are still asked about once and passed as `--add-dir` (approvals.md) | `--tools read` (allowlist keeps the read tool only) |
| agent | `sandbox: "workspace-write"`, `approvalPolicy: "on-request"` | `--permission-mode default`; every `can_use_tool` forwards to an approval card, and directories the prompt names outside the workspace are asked about once and passed as `--add-dir` (approvals.md) | pi defaults |
| full | `sandbox: "danger-full-access"`, `approvalPolicy: "never"` | `--permission-mode bypassPermissions` | pi defaults |

In codex agent mode the sandbox is the boundary; codex sends approval
requests when the model wants to *escalate beyond* the sandbox. In claude
agent mode there is no engine sandbox, so the approval card is the boundary —
the same role the built-in agent's cards play.

## codex app-server protocol

Custom endpoints ride `Turn.Endpoint` and spawn as a `model_providers.<id>`
config override with the key via `MYGO_PROVIDER_<id>_API_KEY` (the id keeps
its case; anything outside `[A-Za-z0-9]` hex-escapes, so distinct ids cannot
collide). `wire_api` follows the provider's declared wire — empty means
`"responses"`. A provider declaring `"chat"` fails before spawn: newer codex
CLIs refuse `wire_api="chat"` outright (openai/codex discussion 7782).

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
- `-c model_reasoning_effort=<low|medium|high>` follows `Turn.Effort`
  (default `medium`) and is always passed.
- The thread id arrives in the `thread/start` result (`thread.id`) and again
  in the `thread/started` notification; it is persisted as the thread's
  `CodexID` for resume.
- Handled notifications: `turn/completed` and `turn/failed` settle the
  turn; `item/started` and `item/completed` carry the item model;
  `item/agentMessage/delta`, `item/reasoning/textDelta` and
  `item/reasoning/summaryTextDelta` stream prose and reasoning; `error`
  fails the turn; `thread/tokenUsage/updated` feeds the closing note.
  Item shapes match the exec `--json` item model and map to the same
  cards. Every other frame is ignored today — including `thread/started`
  (the id is read from the `thread/start` result), `turn/started` and
  `item/commandExecution/outputDelta`.
- The turn closes with a note ("Done · N tokens") from its own usage
  frame when `turn/completed` carries one, else from the running
  `thread/tokenUsage/updated` total; a server that reports no usage
  stays silent. Usage frames are read under either spelling — camelCase
  on the app-server, snake_case on exec `--json`.
- A context compaction arrives as an **item** of type `contextCompaction`,
  announced twice (`item/started`, `item/completed`), and is reported as a
  note on the completed frame only. The codex binary also contains a
  `thread/compacted` method name, but the app-server does not send it —
  measured against 0.153.4, a `thread/compact/start` at 420k tokens
  returns `{}` and no such frame ever arrives. Do not bind to the
  notification; the item type is what fires.
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

- The agent profile's extras ride two flags (spec/agents.md): the system
  prompt as `--append-system-prompt`, and the turn's MCP servers as a
  `--mcp-config` file — stdio servers keep their command, URL servers
  map to the http type — that is deleted when the turn ends. A server
  the format cannot express is skipped, not fatal.
- `--include-partial-messages` turns on per-delta streaming: text
  arrives as wrapped `content_block_delta` frames while it is being
  generated, and the reply emits those deltas live. The complete
  assistant message that follows contributes only its un-streamed
  suffix — the CLI warns the partial stream may duplicate it, so the
  adapter deduplicates against what the deltas already covered (the
  stream resets at every tool_use).

- The session id arrives in the `system`/`init` event (`session_id`), as
  today.
- A context compaction arrives as a `system` frame with
  `subtype: "compact_boundary"`, carrying `compact_metadata`:
  `{"trigger":"manual"|…, "pre_tokens":N, "post_tokens":N,
  "cumulative_dropped_tokens":N, "duration_ms":N}`. The metadata is
  **snake_case** — reading it as `compactMetadata` decodes to nothing and
  silently drops the counts. A boundary frame with no metadata still means
  the context was folded and is still reported.
- Permission requests arrive as control requests:
  `{"type":"control_request","request_id":…,"request":{"subtype":
  "can_use_tool","tool_name":…,"input":{…}}}`. The app replies on stdin:
  - allow: `{"type":"control_response","response":{"subtype":"success",
    "request_id":…,"response":{"behavior":"allow","updatedInput":<input>}}}`
    — `updatedInput` echoes the received input object.
  - deny: `…{"behavior":"deny","message":<reason>}`.
- Any other server-initiated control subtype is answered with
  `{"subtype":"error","request_id":…,"error":"unsupported by mygo"}` —
  never left hanging: an unanswered control request stalls the CLI turn
  forever.
- In read-only mode every `can_use_tool` is denied with a fixed message
  naming the mode (the model reads it as the tool result).
- The `result` event closes the turn; its `duration_ms`,
  `total_cost_usd` and `usage` become the closing note ("Done in 12.3s ·
  $0.0042 · 127 tokens · session …" — the token count only when the CLI
  reported usage).

## pi JSON mode protocol

Spawn `pi -p --mode json -ne --session-id <id> [--thinking <level>] [--tools read] -- <prompt>`;
one fresh process per turn; JSONL events on stdout.

- `-ne` skips extension discovery: a broken local extension must not block
  the app's runs. `-ne` also means pi extensions are out of scope here.
- **Session**: the app generates a UUID for a new thread and always
  passes `--session-id`; the stream's `{"type":"session","id"}` is
  stored as `threadMeta.pi_id`, so later turns resume pi's own session
  file.
- **Thinking**: `--thinking` maps `Turn.Effort` 0/1/2 to
  `off`/`minimal`/`medium`/`high` (out of range falls back to `medium`).
- **Model/provider**: pi resolves models from its own configuration
  (`~/.pi/agent/settings.json`, models.json, auth.json). The app passes
  no `--model` today — pi's default applies; a per-agent model mapping
  is future work (spec/agents.md §4.5).
- **Events** (the ones the projector consumes): `session`;
  `message_update` with `assistantMessageEvent.type` `text_delta` /
  `thinking_delta` (streamed; the deltas are the text); `message_end`
  whose assistant message repeats the text **whole** (emit only when no
  delta streamed it) and carries `content` blocks `text` / `toolCall`
  `{id,name,arguments}`, `usage.totalTokens`, `usage.cost.total`,
  `errorMessage`; `turn_end` with `toolResults` — messages with
  `role:"toolResult"`, `toolCallId`, `toolName`, `content`, `isError`;
  `auto_retry_start`; `agent_settled` — the turn is over. The turn
  closes with a note ("Done · N tokens · $… · session …") when the
  stream reported usage.
- **Settle**: break the read loop on `agent_settled` and reap the
  process; the CLI may keep its streams open (the claude lesson). codex
  app-server is a resident server: close stdin when the turn settles or
  `Wait` blocks forever after a completed turn (the same lesson, second
  sighting).
- **Approvals**: pi executes its tools with its own permissions; the
  app's approval cards do not cover pi. Read-only mode therefore maps to
  a tool allowlist, the only lever pi exposes.
- **Context compaction is not observable on this wire** (measured against
  pi 1.0.3). The full `--mode json` vocabulary is `session`, `agent_start`,
  `turn_start`, `message_start`, `message_update`, `message_end`,
  `turn_end`, `agent_end`, `agent_settled` — no compaction event. pi does
  have `session_compact` / `session_before_compact` /
  `session_compact_failed`, but they are emitted through the extension
  runner and only reach a subscriber when an extension handler is
  registered; this app runs `-ne`, so nothing subscribes. `compaction_start`
  / `compaction_end` exist as internal agent events but are consumed by
  pi's TUI, not by the print-mode JSON writer. `/compact` in `-p` mode is
  passed to the model as literal text, not handled as a command. Reaching
  it would mean switching pi to `--mode rpc` (a full event transport, not
  a note), which is future work.

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

## Process reaping

Every adapter that starts a CLI shares one guard, `cli.Reap`: close stdin
(the thing that makes a resident CLI exit), wait a bounded grace, then a
process-group SIGTERM and finally a leader kill so `Wait` always returns.
The grace starts only after the CLI itself announced the turn was over, so
it cannot cut short a turn that is still producing output, and a group
teardown is reported as cleanup rather than as a failed turn. `WaitDelay`
alone is not enough: it only engages once the context is already done, so a
lingering CLI would hold the turn forever.

An MCP server child is reaped too — nothing else waits on it, so without an
explicit `Wait` every server a turn starts lingers as a <defunct> process.
