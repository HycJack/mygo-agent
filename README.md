# MyGo Agent

A desktop AI-coding-agent app in the shape of OpenAI's Codex, built with
[MyGo](https://github.com/egoist/mygo)'s native UI: GPU-drawn widgets over
Metal — no webview, no HTML, no JavaScript.

![screenshot](screenshot.png)

## Run

```bash
go run .          # or: go build -o codex-app . && ./codex-app
go test ./...     # headless ui.Tester tests of the core flows
```

Requires Go 1.27+ (the toolchain downloads itself). macOS 13+; also builds
on Windows 10 1809+ and Linux glibc 2.28+.

## What it does

- **Projects** — the switcher at the top of the sidebar lists the
  working directories you have opened (native folder dialog to add one,
  right-click a row to remove it). Tasks, the file tree, the git panel
  and the docked terminal are all scoped to the active project.
- **Tasks sidebar** — search, date grouping (Today / Yesterday / Previous
  7 Days …), rename and delete with undo, ⌘N for a new task, ⌘B to
  collapse and expand the sidebar.
- **Workspace panel** (⌘E), docked on the right — a file tree of the
  working directory (lazily read, heavy directories skipped) and the git
  working tree's changes from `git status --porcelain`, refreshed after
  each agent run.
- **File viewer** — click any file in the tree: images render as
  bitmaps, Markdown renders, `.diff`/`.patch` files show colored, and
  everything else opens as code text with line numbers, a wrap toggle,
  and a copy button; Esc or "Chat" goes back to the thread.
- **Git diff viewer**, in the spirit of EGOIST's
  [godiff](https://github.com/egoist/godiff) — click a change in the
  panel to see `git diff` for it (staged diff as fallback, whole content
  as an added diff for untracked files), with hunk headers, row tints,
  and word-level highlighting of the part of each line that actually
  changed.
- **Thread view** — streaming replies, the user's prompt in a bubble, and
  tool cards: shell commands with live status and folded output, file
  patches as colored unified diffs, reasoning notes and errors.
- **Composer** — a full-width box like ZCode's, Enter sends,
  Shift+Enter breaks the line, approval mode (Read Only / Agent / Full
  Access), circular send button that becomes a stop button while the
  agent runs.
- **Built-in agent loop** (pi-style, in-process) — pick "Built-in
  agent" in the bottom-left menu and the app runs the loop itself:
  streaming chat completions against the selected provider, tool
  calls (bash, read_file, edit_file, list_files, grep, read_skill)
  executed locally, replies streaming into the thread with command and
  diff cards.
- **Skills** (Agent Skills spec) — directories with a SKILL.md
  (frontmatter `name:`/`description:`) in the project's
  `.agents/skills`, or `~/.agents/skills`, or `~/.codex-go/skills`.
  The system prompt advertises name and description only; the agent
  loads the full instructions through `read_skill` when the task
  matches, exactly as pi does.
- **MCP servers** — stdio Model Context Protocol clients configured in
  the settings dialog (name + command). At run time each server's
  tools join the agent's set as `mcp_<server>_<tool>` via
  initialize → tools/list → tools/call.
- **Providers and models**, ZCode-style — the sparkle button in the
  composer lists every provider's models with a check on the chosen
  pair, plus reasoning effort (Low / Medium / High). "Manage providers
  & models…" opens a dialog with one pane per provider: name, base URL,
  API key and its models (OpenAI-compatible endpoints; presets for
  OpenAI, DeepSeek, OpenRouter and Ollama). A custom provider runs
  through `codex exec` as a `model_providers.<id>` config override with
  the key passed via an env var; the built-in Codex CLI provider uses
  the CLI's own sign-in.
- **Embedded terminal** (⌘T) — a real shell via MyGo's terminal plugin
  (Ghostty's `libghostty-vt`), docked at the bottom.
- **Persistence** — plain JSON under `~/…/Application Support/codex-go/`:
  `threads.json` for the tasks, `config.json` for the projects,
  providers (incl. API keys, file mode 0600), model, effort and backend
  — written atomically (temp file + rename) and reloaded on launch.
  Legacy configs without providers migrate on first read.
- **Two agent backends**, picked from the bottom-left menu:
  - *Demo agent* — built in, no account: streams a plan, runs one real
    read-only command, and shows a sample patch.
  - *Codex CLI* — runs the real `codex exec --json` binary (if `codex` is
    in PATH and signed in), mapping its JSONL events to the same cards and
    resuming the session across turns.

## Screenshot mode

`CODEX_SHOT=<file.png>` captures the window to a PNG and exits;
`CODEX_SEED=1` fills in sample tasks; `CODEX_TERM=1` opens the terminal
at launch; `CODEX_NAV=0` starts with the sidebar collapsed;
`CODEX_VIEW=<path>` (or `git:<path>`) opens a file — or a git diff — in
the viewer.

```bash
CODEX_SEED=1 CODEX_VIEW="git:AGENTS.md" CODEX_SHOT=/tmp/codex.png go run .
```

## Layout

| File | Contents |
| --- | --- |
| `main.go` | entry, window, seeding, screenshot mode |
| `model.go` | tasks / messages / blocks, persistence, sidebar grouping |
| `theme.go` | the dark Codex palette and the icon set |
| `view.go` | root layout, header, home, terminal dock, menus |
| `sidebar.go` | task list |
| `workspace.go` | file tree, git status list, git diff loading |
| `viewer.go` | the file viewer and the shared diff-line renderer |
| `diff.go` | unified-diff parsing and word-level change marking |
| `thread.go` | messages, tool cards, the mini-Markdown renderer |
| `composer.go` | composer, mode segments, model select |
| `agent.go` | demo agent and `codex exec` streaming |
