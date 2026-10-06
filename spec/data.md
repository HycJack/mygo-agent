# Data: persistence formats

The app's durable data lives in two places: config.json (settings) and a
per-thread directory tree (conversations). Both are JSON, written
atomically (temp file + rename), and versioned. The rules here keep
hand-edited files and future schema changes from silently destroying
data, and they follow the shape the mainstream coding agents use — one
file per session (Claude Code: `~/.claude/projects/<project>/<session>.jsonl`;
Codex CLI: `~/.codex/sessions/<date>/rollout-<id>.jsonl`) — adapted to
this app's snapshot model.

## Threads: one file per thread

```
<configDir>/codex-go/threads/<projectID>/<threadID>.json
```

```json
{
  "version": 1,
  "meta": {
    "id": "…", "project_id": "…", "title": "…",
    "created": …, "updated": …, "codex_id": "…", "claude_id": "…",
    "pi_id": "…", "agent_id": "…"
  },
  "messages": [ … ],
  "chat_log": [ … ]
}
```

- **One thread, one file.** A turn's save rewrites one small file, not
  every conversation the app has ever had. Deleting a task deletes one
  file. One corrupt file loses one task, never the history.
- Files carry mode 0600: thread content includes code snippets.
- `<projectID>` partitions the tree by project; `<threadID>` is the
  app's hex uid. Both are validated filename-safe before use.
- `agent_id` binds the thread to a configured agent (spec/agents.md).
  Empty resolves to the default agent; the resolution is stamped back
  on the next save, so a thread from before the agents block picks up
  its binding on first use — never a rewrite on load alone.
- `version` is the schema version; the current version is 1; every
  write stamps it; a file from a newer schema is refused (see Load
  failures).

## config.json — version 2

```json
{ "version": 2, "projects": […], "providers": […],
  "agents": […], "default_agent": "…", … }
```

- Strict decode: unknown fields are errors. A typo in a hand-edited
  config (`"permisions"`) fails loudly, not silently zeroed.
- Mode 0600 — provider API keys live there.
- `agents` are the configured agent profiles and `default_agent` names
  the one new tasks bind to when nothing else chose (spec/agents.md).
- **Version 1 files still load.** The host folds a config with no
  agents into a single **Default** agent — an empty profile inherits
  the app-level selection, so the fold is the identity and an old
  config behaves exactly as it did — and the next save stamps
  version 2. A file from version 3 is refused like any newer schema.

## Load failures

| Failure | Behavior |
| --- | --- |
| `version` greater than the binary knows | The file is **renamed** with an `.unsupported` suffix and skipped; the bytes are preserved. Never silently misparse a newer schema. |
| Strict decode error (typo, corruption) | Renamed with an `.invalid` suffix, skipped; the reason is recorded on the host for the session. Per-thread files fail alone: the rest of the tree still loads. |
| File missing | Defaults; no rename, no notice. |

## Legacy migration

The pre-directory single-file `threads.json` loads once as an upgrade
contract: its threads are imported into the tree, and the original file
is renamed `threads.json.migrated` — preserved, never overwritten or
deleted by the app.

## Invariants

1. **A save never destroys data that failed to load.** Failed files are
   renamed out of the way first; after that, saving is safe. There is no
   path where one bad byte wipes a task history — and with per-thread
   files, a bad byte cannot even touch the other tasks.
2. A quarantine rename happens at most once per file per launch; if the
   target name exists, a counter suffix is added.
3. Load-failure reasons are recorded for the session and cleared at the
   next successful save.
4. Writes are atomic (temp file + rename) per file; config.json keeps
   mode 0600, thread files 0600.
5. Deleting a thread removes exactly its file; a running thread is
   stopped first, and the deletion marks the thread *dropped* so a late
   event from the dying run can never re-save it. The undo toast
   restores the file from the in-memory bytes the deletion held; only
   undo clears the dropped flag.
