# Architecture

The app is four layers joined by protocols, in the shape of agent-foundation
(a13n): a Harness runs turns, Providers supply isolation and memory, the Host
owns state and policy, and the UI renders host state. Every boundary is one
interface in one file with one owning spec section. The harness list is the
replaceable part: builtin, codex, claude and pi differ only in
their adapter; the Host never branches on a harness's internals.

```
┌────────────────────────────────────────────────────────────┐
│ UI (internal/ui)    renders ViewModels; owns no policy      │
│   ▲ reads snapshots                                 │
│ HOST (internal/app)      state, threads, config, dispatch,  │
│   ▲ harness.Event          approvals, persistence           │
│   │ harness.Turn                                    │
│ HARNESS (internal/harness)   one protocol, four adapters:   │
│   │    builtin loop · codex app-server · claude control ·   │
│   ▼    pi json                                            │
│ PROVIDERS (internal/providers)                              │
│      sandbox (seatbelt/bwrap/none)                          │
└────────────────────────────────────────────────────────────┘
```

Transcripts are not a provider: `harness.Memory` is the protocol and the
Host implements it over the thread store (`internal/app/memory.go`),
because a transcript is thread state and already has one home.

## Protocol map

| Boundary | Protocol code | Owned by |
| --- | --- | --- |
| Host → Harness | `harness.Harness` + `Turn` + `Event` (harness/harness.go) | architecture.md, cli-backends.md |
| Harness → Sandbox | `harness.Sandbox` (harness/sandbox.go) | sandbox.md |
| Harness → Memory | `harness.Memory` (harness/memory.go) | architecture.md |
| Harness → approvals | `harness.ApprovalContext` (harness/approvals.go) | approvals.md |
| Host → config | `config.Config` | permissions.md, agents.md |
| Host → persistence | `threads/<projectID>/<threadID>.json` via `writeFileAtomic` | data.md |

## Layers and import rules (machine-checked)

- `internal/harness` imports **neither** `internal/app`/UI **nor**
  `internal/providers`. The harness owns the loop, the tool set, the
  permission gate, the approval wait and the stream wire clients; it knows
  nothing about threads, windows or files-on-disk beyond its workdir.
  The root package is the protocol and the shared value types only; the
  built-in agent (loop, tools, skills, MCP over stdio and streamable
  HTTP) lives in
  `internal/harness/builtin`, and the CLI adapters in their own
  subpackages — `internal/harness/claude` (stream-json control),
  `internal/harness/codex` (app-server JSON-RPC), `internal/harness/pi`
  (JSON mode) — plus `internal/harness/cli` for the pieces they share
  (process-group kill guard, output/argument helpers). The protocol
  root is held to the same standard: it may name only itself and
  `internal/harness/cli`. An
  adapter implements `Harness` and **returns** its failure: it streams
  events and never settles threads itself — the Host's dispatch owns the
  run's context and calls finish when Run returns. Approvals reach an
  adapter through `Turn.OnApproval` (the spec-promised callback on the
  Turn); codex's custom endpoint rides `Turn.Endpoint`. A rule break is
  a spec break; `scripts/check-deps.sh` checks the subpackages too.
- `internal/providers/*` may import `internal/harness` (for the interface
  and value types) and nothing else inside the repo.
- `internal/app` (Host + UI) may import everything; it is the assembly end.
- `internal/ui` holds the shared view code: ViewModels the host fills,
  the Actions interface the views call back through, the palette/theme,
  the icon set, the markdown renderer (with its incremental MdCache) and
  the diff-line renderer. It imports mygo's toolkit,
  `internal/harness` value types — never
  `internal/app` or providers. Migrated: Home, Composer, Sidebar (with
  the GroupThreads/RelTime pure helpers), Workspace, Markdown,
  DiffLineRow, the thread transcript with its block cards and approval
  card (Transcript), the file viewer (Viewer), the manage-providers
  dialog (Settings), and the window chrome (Header, RenameDialog). The
  host assembles the frame around them: shortcuts, panels, terminal dock,
  and one bridge per surface that fills the ViewModel and syncs the
  bindings (drafts, menus, toggles) back after each frame.
- `scripts/check-deps.sh` enforces the arrows and runs in CI. It names
  every harness and provider subpackage by glob, and a `go list` failure
  is a hard error rather than an empty answer — a check that silently
  passes on a build break is worse than no check. A rule break is
  a spec break: change the spec in the same commit or fix the code.

## State ownership

| State | Owner | Persisted by |
| --- | --- | --- |
| Threads, messages, blocks | Host | `threads/<projectID>/<threadID>.json`, one atomic write per turn (data.md) |
| Providers, rules, mode, backend, agents | Host | config.json (atomic write, 0600) |
| Harness transcripts | Host, via the `harness.Memory` protocol | the thread file's `chat_log` |
| Sandbox scratch dirs | the harness tool layer | removed when the command ends (sandbox.md) |
| Approval decisions | Host | never persisted (approvals.md) |

One home each: the mode is read from `app.mode` and derived everywhere; the
permission rules are read from config once; a harness's session id
(`CodexID`/`ClaudeID`/`PiID`) is written by the Host when the harness
reports it.

## Threading

The harness goroutine never touches host state directly. Every read and
write of a thread, the approvals map or a config value goes through
`app.update`, which marshals onto the main thread in a windowed run and
serializes on `app.mu` in headless tests. Two rules follow from that and
are load-bearing:

- **A turn reads a snapshot.** `turnFor` builds the `Turn` on the main
  thread, and the built-in harness captures its provider, MCP list and
  approval deadline when it is constructed. A turn must not change its
  endpoint or tool set underneath the approval cards the user is deciding
  on.
- **A settled turn accepts no more events.** `finish` marks the reply
  done; `applyEvent` drops anything that arrives afterwards. An adapter
  whose reader goroutine outlives its own `Run` therefore cannot write
  into a message that has already been saved.

## The Harness protocol

A harness is one turn of work. Instances are per-turn (OAC's model: a fresh
session per prompt), so a harness struct may close over its turn's thread
position and the Host's approval callback.

```go
type Harness interface {
    Kind() string // "builtin" | "codex" | "claude" | "pi"
    // Run drives one turn, emitting normalized Events until the turn
    // settles. It returns an error only for a failed turn.
    Run(ctx context.Context, turn Turn, emit func(Event)) error
}
```

`Run` returns only `error`: the transcript reaches the Host through
`Turn.Memory`, so the return value is reserved for a failed turn.

- `Turn` carries everything a harness needs and nothing about Thread/UI:
  prompt, workdir, mode, rules, model, effort, max turns, session id to
  resume, an endpoint override, the sandbox boundary, the MCP servers,
  tool and skill selections, an appended system prompt, memory (its key
  plus the initial transcript), and the approval and outside-directory
  callbacks.
- `Event` is the normalized stream every adapter emits (kinds: text,
  tool_start, tool_end, reasoning, file_change, session, note, error,
  done). The Host's single projector turns events into message blocks —
  one mapping, not one per backend.
- Errors: a harness returns an error only for a failed turn; a stopped run
  is `context.Canceled` and the Host keeps what arrived. A denial or
  approval timeout is a settled tool result inside the stream, never an
  error (approvals.md).

## Transcript order and folding

A turn has to read the way it happened: prose, a tool call, more prose, a
patch, a closing sentence. So `Message.Blocks` is a **single ordered
sequence** and prose lives in it as `text` blocks. Keeping text in
`Message.Text` and cards in `Message.Blocks` made interleaving
inexpressible — the view had nothing to interleave *with* — and every turn
rendered as all-cards-then-all-prose. `Message.Text` is now the
concatenation of the text blocks, kept for two readers that still need a
flat document: the copy button, and reseeding the built-in transcript.

Three rules follow, and each is load-bearing:

- **A new stretch of prose starts only after a card.** Two text events
  with nothing between them are one paragraph. The Claude gateway emits an
  assistant message per content block, so without this rule one sentence
  becomes two and a chunked stream becomes a hundred.
- **A card interrupting the prose leaves a paragraph break.** The
  aggregate is the copy button's content and the built-in transcript's
  seed, so two fragments run together would be neither readable nor a
  valid document.
- **The projector is the only writer, and it redacts.** Every backend
  puts its own text in `Event.Text` and the card is persisted into the
  thread file, so redaction happens once, in the projector. Redacting at
  the producers meant whichever adapter was missed was the one that
  leaked.

Runs of alike cards are then folded by `app.itemize`, a pure function in
the Host. Folding is **presentation only** — the persisted sequence is
untouched, so a card is still auditable, and a group's open state is just
the open state of its members, which is why no new persisted field was
needed and a group survives a reload with the thread.

| Folds into a run | Because |
| --- | --- |
| `command` | a twenty-command turn is one row; the header carries the count, the failures and the summed duration |
| `diff`, while the file matches | two edits to one file are one story; edits to two files are two facts |
| `reasoning` | the agent thinking, and nobody scrolls back for it |
| `note` | the harness narrating the turn |

Two kinds never fold:

- `error` — every one says something different and every one is
  actionable. Collapsing them hides the reason a turn failed.
- `approval` — a decision binds exactly one prepared call
  (approvals.md). A group header would have to claim to be something it
  is not.

A run of **one** keeps its plain card. "One command, then an answer" is
the common shape and must keep looking the way it always has; a header
that says "1 commands" is worse than no header. Prose never folds at all.
And a `reasoning` card is created closed: the thinking shows as one
preview line until the user opens it, because nobody scrolls back for it
even when it stays a single card.

## Tool output budgets

What a tool call costs the model's context is bounded at three layers,
each a backstop for the one above it:

- **At the exit.** Every tool bounds what it emits — bash and every MCP
  result at 32 KB (`TrimOutput`), `grep` at 200 matching lines,
  `list_files` at 200 entries, `read_file` at 400 lines (a range at
  2 MB). The trim keeps head **and tail**: a command's errors live at
  the end of its output and a file's structure at its start, so a cut
  that kept only the first half would drop exactly the half a failing
  run was about to show. MCP results are bounded where they enter, at
  the one place every server's output passes through — a server the app
  does not own has no trim of its own.
- **In the transcript.** `ElideToolResults` keeps the tool results
  inside a byte budget (128 KB) by replacing the oldest with a one-line
  placeholder, oldest first. A result ten rounds old has been read and
  acted on; what it costs from here on is context, not information. The
  most recent messages are never touched, and their protection is
  absolute — when the recent window alone outweighs the budget, the
  budget yields rather than the window opening. Elision is part of the
  model-facing transcript and persists with it; what the screen shows
  comes from the events, which keep their own trimmed copies.
- **At the watermark.** When a provider declares a context window
  (config `context_window`) and the last response's own prompt-token
  count passes four fifths of it, the transcript is **summarised, not
  dropped** — Claude Code's auto-compaction shape. The model writes the
  summary in one tool-less call over the oldest stretch; the system head
  and the most recent messages survive verbatim, and a note says the
  compaction happened. A failed summarising call falls back to dropping
  turns — a failed compaction must never fail the turn. Declaring no
  window keeps the count-based backstop alone. On the chat-completions
  wire the declared window does double duty: it is also what asks the
  provider for usage counts at all (`stream_options.include_usage`);
  the Responses wire reports tokens on its own.
- **By the turn.** `CompactHistory` drops whole turns past a message
  count, at a user-message boundary so no tool result loses the call
  that asked for it.

## What stayed out (deliberately)

- mygo's immediate-mode framework renders from live state with host
  callbacks; every view in `internal/ui` renders from ViewModel
  snapshots and report through Actions, and the host syncs the snapshot's
  transient bits (drafts, menu state, toggles) back after each frame.
  Blocks fold and approval decisions run through TranscriptActions, so
  the view never writes host state directly.
- No remote/stream transport yet. When one appears, it consumes the same
  `Event` stream the projector does (a13n's AG-UI adapter is the model:
  standard events where they fit, one CUSTOM kind otherwise).
