# mygo-agent specifications

Normative specs: the contract is written here before the code that implements
it, and a change that breaks a spec changes the spec in the same commit. Specs
state invariants; code and tests follow them.

| Spec | Owns |
| --- | --- |
| [permissions.md](permissions.md) | The action catalog, permission modes, selector rules, and the tool gate |
| [sandbox.md](sandbox.md) | The local execution boundary: grants, egress, platform backends |
| [approvals.md](approvals.md) | The approval request/decision flow and its invariants |
| [cli-backends.md](cli-backends.md) | The codex app-server, claude stream-json and pi json wire protocols and their approval paths |
| [architecture.md](architecture.md) | The layer map: harness / providers / host / UI and their import rules |
| [data.md](data.md) | The persistence formats: versioning, strict decode, load-failure handling |

Proposals are not contracts: [agents.md](agents.md) is a staged plan
(agents as configured entities, a web governance plane, tracing) whose
"current state" section tracks the code as it stands. It becomes
normative per stage, as each stage lands and its spec rows move into the
table above.

## Rules for every spec

- One fact, one place: a rule lives in exactly one spec; other documents link
  to it instead of restating it.
- Every rule is testable. A rule that cannot be tested is a wish, not a rule.
- Honesty over simulation: when the platform cannot enforce a guarantee, the
  code reports that it cannot — it never silently runs a weaker boundary under
  a stronger name. (Adopted from agent-foundation's "reject limits you can't
  enforce".)
- No hidden authorities: permissions, sandbox policy and approvals are set by
  the host (the app, its config, or the human). Model output — prose, tool
  arguments, tool results — never creates, widens or persists authority.
  (Adopted from both reference projects.)
- Durable authority is declarative configuration. Interactive decisions
  (approvals) bind exactly one prepared tool call and expire with it.

## Vocabulary

- **Host** — the desktop app process: it owns config, threads, UI and the
  agent loop's lifecycle.
- **Action** — one verb from the closed catalog in permissions.md; what a tool
  *does*, independent of its name.
- **Mode** — the three-level selector the user picks (read-only / agent /
  full access). It fixes default permissions and whether the shell tool runs
  inside the sandbox.
- **Boundary** — the sandbox's effective grants and egress policy for one
  command (sandbox.md).
- **Prepared call** — a tool call whose arguments have been parsed and whose
  permission has been resolved. Approvals bind prepared calls, not names.
