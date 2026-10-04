# MyGo Agent

A Codex-style desktop AI coding agent built with [MyGo](https://github.com/egoist/mygo)
— GPU-drawn native UI, no webview, no HTML, no JavaScript.

![screenshot](screenshot.png)

## Highlights

- **Projects** — switch working directories from the sidebar; tasks,
  the file tree, the git panel and the terminal are scoped per project.
- **Four agent backends**, switchable from the bottom-left menu:
  - *Built-in agent* — an in-process loop (pi-style): streaming
    OpenAI-compatible calls, local tools (`bash`, `read_file`,
    `edit_file`, `list_files`, `grep`, `read_skill`), markdown replies
    with tables and diff cards.
  - *Codex CLI* — runs `codex exec --json`, resuming sessions.
  - *Claude Code* — runs `claude -p --output-format stream-json`,
    resuming sessions; Edit/Write calls render as diffs.
  - *Demo agent* — no account needed.
- **Providers, ZCode-style** — per-vendor base URL, API key, model list
  and wire API (chat completions or the Responses API that the codex
  and OpenAI models use). Presets for OpenAI, DeepSeek, OpenRouter and
  Ollama.
- **Skills** (Agent Skills spec) — `SKILL.md` directories in
  `.agents/skills/` (project or home); advertised by name and
  description, loaded on demand via `read_skill`.
- **MCP servers** — stdio Model Context Protocol clients configured in
  settings, plus the project's `.mcp.json` (Claude Code / pi
  convention). Tools join the agent's set as `mcp_<server>_<tool>`.
- **UI** — task sidebar with date groups and search, file tree + git
  changes panel, file viewer (text / image / markdown / diffs), an
  anchor rail that previews and jumps between messages, message actions
  (copy / resend / regenerate), a full-width composer with approval
  modes, and an embedded Ghostty terminal.

## Layout

```
main.go               entry, window, version, packaging hooks
model.go              app state, threads, persistence, projects
view.go sidebar.go    shell: header, panels, task list
thread.go composer.go message rendering, mini-markdown, composer
viewer.go workspace.go file viewer, file tree, git status/diff
rail.go               the message anchor rail
diff.go builtin.go    word-level diffs, the built-in backend runner
claude.go             the Claude Code CLI backend
settings.go           providers, API keys, models, MCP servers
theme.go              the dark Codex palette and icons
internal/agent        the agent loop: streaming clients (chat +
                      responses), tools, skills, MCP, diffs
internal/config       persisted configuration
internal/components   reusable UI pieces (anchor rail, formatters)
```

## Build

Requires Go 1.27+ (the toolchain downloads itself). No cgo, no npm.

```bash
make build     # ./mygo-agent for this machine
make run       # go run .
make test      # all packages
make release   # dist/ for darwin/linux/windows (amd64+arm64),
               # macOS .app bundles and checksums
```

`--version` / `-v` prints the build version.

## Packaging

`make release` cross-compiles every target into `dist/`:

```
mygo-agent-darwin-amd64         mygo-agent-windows-amd64.exe
mygo-agent-darwin-arm64         mygo-agent-windows-arm64.exe
mygo-agent-linux-amd64          mygo-agent-linux-arm64
MyGoAgent-darwin-*.app.zip      checksums.txt
```

macOS bundles need codesigning/notarization for distribution outside
your machine (`codesign --deep --force --sign -` for local ad-hoc).
Pushing a `v*` tag runs the release workflow and attaches `dist/*` to
a GitHub release.

## Documentation

- [README.zh-CN.md](README.zh-CN.md) — 中文说明
