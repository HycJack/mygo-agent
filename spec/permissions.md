# Permissions

The permission model decides, for every prepared tool call, whether it runs,
is denied, or waits for an approval. It is the *only* gate the built-in agent
has on the host — there is no outer container — so it must actually gate:
the approval-mode selector must change what the built-in backend can do, not
only the flags handed to external CLIs.

The design follows agent-foundation's shape (closed action catalog, selector
rules, three permission modes) at desktop scale.

## Action catalog

Actions are a closed set. A tool declares what it does; the policy decides
whether that is allowed. Adding an action is a spec change.

| Action | Meaning | Tools |
| --- | --- | --- |
| `file.read` | Read files and directories, search contents | read_file, list_files, grep |
| `file.write` | Create, modify or delete files | edit_file |
| `shell.exec` | Run a process on the host | bash |
| `skill.read` | Load bundled project skills | read_skill |
| `mcp.call` | Call a tool on a configured MCP server | mcp_* |

Rules:

- Every tool declares at least one action; a tool without a declared action is
  a bug and fails closed (denied).
- A tool's action set is trusted host code, never model-authored JSON.
- MCP tools always declare exactly `mcp.call`. The policy cannot know what a
  foreign server does, so `mcp.call` is treated as unbounded: in read-only
  mode it is denied, in agent mode it asks.

## The credential denylist

`file.read` and `skill.read` are allow in all three modes, and an absolute
path passes straight through the path join, so the gate alone would let the
model read `~/.aws/credentials` in read-only mode and carry it out in the next
request. The file tools therefore refuse a fixed set of credential stores:

```
~/.ssh   ~/.aws   ~/.gnupg   ~/.kube   ~/.docker   ~/.config/gh   ~/.netrc
```

- It is a **deny list on the tool**, not a mode: it applies in every mode,
  including full access. A sandbox boundary is a convenience; a credential
  store is a secret.
- The sandbox masks the same list for shell commands (sandbox.md): what
  is a secret to the model as a file is a secret through a shell too.
  The price is honest and known — a credential CLI that authenticates
  from its own store (`gh`, say) will not authenticate inside `agent`
  mode; that is the boundary working, and Full Access is the way around
  it.
- Both the literal and the symlink-resolved form of each store are compared,
  so a symlink or a `..` segment is not a way around it. Resolution is
  best-effort — a path that does not exist cannot be resolved — which is why
  both forms are kept rather than only the resolved one.
- A refusal is a settled tool result the model can read, naming the reason.
  It is never a crash and never a silent empty success.
- Writes are not the concern here: `ConfineWrites` already confines them to
  the workspace in agent mode.

## Permission modes and defaults

`Mode` is the three-level selector: `read-only` < `agent` < `full`. Defaults
per action:

| Action | read-only | agent | full |
| --- | --- | --- | --- |
| `file.read` | allow | allow | allow |
| `skill.read` | allow | allow | allow |
| `file.write` | deny | allow | allow |
| `shell.exec` | deny | allow (sandboxed, see sandbox.md) | allow (unsandboxed) |
| `mcp.call` | deny | ask | allow |

- In `agent` mode `file.write` is additionally confined to the workspace by
  the file tools themselves (writes outside the workdir are rejected); in
  `full` mode they are not.
- The mode also selects the shell execution boundary: `agent` runs sandboxed,
  `full` runs unsandboxed. Permission and boundary are decided together by the
  mode but are separate mechanisms.

## Selector rules

Host configuration (config.json `permissions.rules`) overrides mode defaults
per tool:

```json
"permissions": { "rules": { "bash": "ask", "mcp_github_*": "allow" } }
```

- Selectors match tool names: exact match wins, then the longest prefix
  ending in `*`, then the bare `*`, then the mode default.
- Values are `allow`, `deny`, `ask` — case- and space-insensitive; any
  other value fails closed to `deny`. `ask` without an approval handler
  is a denial (approvals.md).
- Rules widen and narrow alike: a rule may grant `mcp_x_*: allow` in agent
  mode or force `bash: ask` in full mode. There is no rule the UI applies
  that config cannot express — interactive decisions never write back to the
  rules.

## The gate

- The gate runs after argument parsing and before execution, exactly once per
  prepared call.
- Denial produces a tool result the model can read — a bounded message naming
  the tool and the reason — never a silent no-op and never a crash.
- Resolution order: selector rule → mode default by action.
- Unknown tool names (no matching Tool) fail as unknown, before the gate.

## Invariants

1. Mode `read-only` denies every action not listed as `allow` above, for every
   backend. A backend that cannot enforce its mode's denials must say so in
   its result rather than run anyway.
2. Permission state is recomputed per call from mode + rules; nothing a call
   did changes the permissions of a later call.
3. Model output never changes mode, rules, or the sandbox boundary.
4. A denial is a settled result: the loop records it and moves on; it is not
   retried by the harness.
