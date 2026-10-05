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
    "created": …, "updated": …, "codex_id": "…", "claude_id": "…"
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
- `version` is the schema version; the current version is 1; every
  write stamps it; a file from a newer schema is refused (see Load
  failures).

## config.json — version 1

```json
{ "version": 1, "projects": […], "providers": […], … }
```

- Strict decode: unknown fields are errors. A typo in a hand-edited
  config (`"permisions"`) fails loudly, not silently zeroed.
- Mode 0600 — provider API keys live there.

## Load failures

| Failure | Behavior |
| --- | --- |
| `version` greater than the binary knows | The file is **renamed** with an `.unsupported` suffix and skipped; the bytes are preserved. Never silently misparse a newer schema. |
| Strict decode error (typo, corruption) | Renamed with an `.invalid` suffix, skipped; the reason surfaces in the settings dialog. Per-thread files fail alone: the rest of the tree still loads. |
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
3. Load-failure reasons stay visible (settings dialog) until the next
   successful save clears them.
4. Writes are atomic (temp file + rename) per file; config.json keeps
   mode 0600, thread files 0600.
5. Deleting a thread removes exactly its file; the undo toast restores
   the file from the in-memory bytes the deletion held.
