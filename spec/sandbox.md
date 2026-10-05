# Sandbox

The sandbox is the local execution boundary for the shell tool in `agent`
mode. It exists because the built-in agent runs on the user's real machine:
read-only's denial of `shell.exec` and full access's unsandboxed execution
bracket a middle ground — *the agent may run commands, but only inside a
workspace-scoped boundary*.

The model follows agent-foundation's `a13n-envd` execution boundary:
path grants plus an egress mode, enforced with the platform's native
primitive. It is deliberately small: one function that wraps one command.

## Boundary

```go
type Boundary struct {
    Workdir string   // the project directory: read-write
    Scratch string   // a fresh per-run temp dir: read-write, HOME and TMPDIR
    Network string   // "deny" (agent mode) or "inherit" (not used yet)
}
```

- Grants are exact directories. `Workdir` is read-write; system roots
  (`/System`, `/usr`, `/bin`, `/sbin`, `/lib*`, `/etc`) are read-only;
  `Scratch` is read-write and becomes `HOME` and `TMPDIR` of the child so
  caches and temp files land inside the boundary.
- **The working directory is a default, not a boundary.** Containment comes
  from the grants, not from `cmd.Dir`.
- **No per-command weakening.** The boundary is fixed for the mode; there is
  no flag, tool argument or model request that narrows or widens it. A
  different boundary requires a different mode (or a spec change).
- Paths are canonicalized before they enter a profile. A grant that does not
  exist is an error, not an empty grant.

## Platform backends

| Platform | Primitive | Network | Status |
| --- | --- | --- | --- |
| macOS | Seatbelt (`/usr/bin/sandbox-exec`) with a generated `(deny default)` profile | `(deny network*)` | supported |
| Linux | bubblewrap (`bwrap`) when on PATH: mount namespaces, `--cap-drop ALL`, `--clearenv`, `--die-with-parent` | `--unshare-net` | supported when `bwrap` exists |
| Windows | — | — | unsupported, reported |

Rules:

- Each backend builds from the same `Boundary`; backends differ only in the
  primitive. There is no shared "mostly sandboxed" path.
- **Honesty rule:** a backend that cannot run returns a typed error naming
  the missing primitive and the remedy (install bubblewrap / use full
  access). The shell tool surfaces that error as the tool result. It never
  falls back to unsandboxed execution inside `agent` mode — an unavailable
  sandbox degrades the *feature*, not the guarantee.
- `full` mode does not use the sandbox at all; that is the contract, not an
  optimization.

## macOS profile (normative shape)

Generated SBPL, launched as `sandbox-exec -p <profile> -- <argv>`:

- `(version 1)` header, `(deny default)` base.
- Allowed unconditionally: `process-exec*`, `process-fork`, `signal` to
  same-sandbox processes, `sysctl-read*`, `file-read-metadata`,
  `file-read*` on `/dev/null`, `/dev/zero`, `/dev/random`, `/dev/urandom`,
  `/var/empty`, and read-only access to the system roots.
- Per grant: `file-read*` (+ `file-write*` for read-write grants) on the
  canonicalized subpath.
- `Network: deny` adds nothing (default deny); `inherit` would add
  `(allow network*)` and the DNS mach-lookups — reserved for a future mode
  and not generated today.
- The profile is built from the `Boundary` values only; command text, tool
  arguments and model output never appear in it.

## Failure and cleanup

- The wrapper is a process: sandboxed commands are killed by cancelling the
  context like any other command (the app's process-group stop applies).
- A sandboxed command that was killed or timed out has an **unknown
  outcome**: its result says so instead of reporting a clean failure.
- `Scratch` directories are removed when the run finishes.

## Invariants

1. In `agent` mode a shell command that writes outside `Workdir`/`Scratch`
   fails, on every supported platform, with the OS's denial — this is the
   property the sandbox tests assert.
2. Egress is denied in `agent` mode; a command that opens a network
   connection fails the same way.
3. The sandbox never grants more than `full` mode and never less than
   read-only mode's total denial of `shell.exec`.
4. Wrapping is not authorization: the permission gate (permissions.md) runs
   first; the sandbox bounds what an *allowed* command may touch.
