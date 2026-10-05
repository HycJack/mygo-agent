# Architecture

The app is four layers joined by protocols, in the shape of agent-foundation
(a13n): a Harness runs turns, Providers supply isolation and memory, the Host
owns state and policy, and the UI renders host state. Every boundary is one
interface in one file with one owning spec section. The harness list is the
replaceable part: builtin, codex, claude and demo differ only in their
adapter; the Host never branches on a harness's internals.

```
┌────────────────────────────────────────────────────────────┐
│ UI (views)          renders host state; owns no policy      │
│   ▲ reads state                                    │
│ HOST (app)          state, threads, config, dispatch,       │
│   ▲ harness.Event          approvals, persistence          │
│   │ harness.Turn                                   │
│ HARNESS (internal/harness)   one protocol, four adapters:   │
│   │    builtin loop · codex app-server · claude control ·   │
│   ▼    demo                                                 │
│ PROVIDERS (internal/providers)                              │
│      sandbox (seatbelt/bwrap/none) · memory (host json)     │
└────────────────────────────────────────────────────────────┘
```

## Protocol map

| Boundary | Protocol code | Owned by |
| --- | --- | --- |
| Host → Harness | `harness.Harness` + `Turn` + `Event` (harness/harness.go) | architecture.md, cli-backends.md |
| Harness → Sandbox | `harness.Sandbox` (harness/sandbox.go) | sandbox.md |
| Harness → Memory | `harness.Memory` (harness/memory.go) | architecture.md |
| Host → config | `config.Config` | permissions.md |
| Host → persistence | threads.json via `writeFileAtomic` | architecture.md |

## Layers and import rules (machine-checked)

- `internal/harness` imports **neither** `internal/app`/UI **nor**
  `internal/providers`. The harness owns the loop, the tool set, the
  permission gate, the approval wait and the stream wire clients; it knows
  nothing about threads, windows or files-on-disk beyond its workdir.
- `internal/providers/*` may import `internal/harness` (for the interface
  and value types) and nothing else inside the repo.
- `internal/app` (Host + UI) may import everything; it is the assembly end.
- `internal/ui` holds the shared view code: ViewModels the host fills,
  the Actions interface the views call back through, the palette/theme,
  the icon set, the markdown renderer (with its incremental MdCache) and
  the diff-line renderer. It imports mygo's toolkit and
  `internal/harness` value types — never `internal/app` or providers.
  Migrated: Home, Composer, Sidebar (with the GroupThreads/RelTime pure
  helpers), Workspace, Markdown, DiffLineRow. The thread's block cards
  and the viewer pane still render inline in the host — their state
  (Block, viewerState) changes mid-turn and mid-load; they migrate when
  a second UI surface needs them, over the same ViewModel pattern.
- `internal/components` stays UI-toolkit-level: it imports no internal
  package.
- `scripts/check-deps.sh` enforces the arrows and runs in CI. A rule break is
  a spec break: change the spec in the same commit or fix the code.

## State ownership

| State | Owner | Persisted by |
| --- | --- | --- |
| Threads, messages, blocks | Host | threads.json (atomic write) |
| Providers, rules, mode, backend | Host | config.json (atomic write, 0600) |
| Harness transcripts | Memory provider | through the Host's store |
| Sandbox scratch dirs | Sandbox provider | removed at command end |
| Approval decisions | Host | never persisted (approvals.md) |

One home each: the mode is read from `app.mode` and derived everywhere; the
permission rules are read from config once; a harness's session id
(`CodexID`/`ClaudeID`) is written by the Host when the harness reports it.

## The Harness protocol

A harness is one turn of work. Instances are per-turn (OAC's model: a fresh
session per prompt), so a harness struct may close over its turn's thread
position and the Host's approval callback.

```go
type Harness interface {
    Kind() string // "builtin" | "codex" | "claude" | "demo"
    // Run drives one turn, emitting normalized Events until the turn
    // settles. It returns the final transcript (builtin only; others nil).
    Run(ctx context.Context, turn Turn, emit func(Event)) ([]ChatMessage, error)
}
```

- `Turn` carries everything a harness needs and nothing about Thread/UI:
  prompt, workdir, mode, rules, model, effort, max turns, session id to
  resume, the approval callback, and the initial transcript for memory.
- `Event` is the normalized stream every adapter emits (kinds: text,
  tool_start, tool_end, reasoning, file_change, session, note, error,
  done). The Host's single projector turns events into message blocks —
  one mapping, not one per backend.
- Errors: a harness returns an error only for a failed turn; a stopped run
  is `context.Canceled` and the Host keeps what arrived. A denial or
  approval timeout is a settled tool result inside the stream, never an
  error (approvals.md).

## What stayed out (deliberately)

- mygo's immediate-mode framework renders from live state with host
  callbacks; the shared views in `internal/ui` render from ViewModel
  snapshots and report through Actions, and the host syncs the snapshot's
  transient bits (draft, menu state) back after each frame. The thread
  view — whose state changes under the renderer mid-turn — migrates last,
  when a second UI surface (headless CLI, remote) justifies it.
- No remote/stream transport yet. When one appears, it consumes the same
  `Event` stream the projector does (a13n's AG-UI adapter is the model:
  standard events where they fit, one CUSTOM kind otherwise).
