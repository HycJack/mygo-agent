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
    Scratch string   // a fresh temp dir per shell call: read-write;
                     // the child's HOME (its TMPDIR is a tmp/ subdir)
    Network string   // "deny" (agent mode) or "inherit" (not used yet)
}
```

- Grants are exact directories. `Workdir` is read-write; reads are broad
  (the whole host filesystem) so toolchains work from wherever they are
  installed — **except a fixed credentials denylist** (`~/.ssh`,
  `~/.aws`, `~/.gnupg`, `~/.kube`, `~/.docker`), which stays unread.
  This is the codex *workspace-write* contract: read the disk, write the
  workspace, no network. `Scratch` is read-write, becomes the child's
  `HOME`, and holds its `TMPDIR` (a `tmp/` subdirectory), so caches and
  temp files land inside the boundary; it is fresh for every shell
  invocation and removed when the command ends. The file tools refuse
  the same credential list (permissions.md).
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
| macOS | Seatbelt (`/usr/bin/sandbox-exec`) with a generated `(deny default)` profile | `(deny network*)` by default | supported |
| Linux | bubblewrap (`bwrap`) when on PATH: PID/IPC/UTS/network namespaces, `--new-session`, `--ro-bind / /`, `--proc /proc`, `--dev /dev`, credential dirs masked by tmpfs, `--cap-drop ALL`, `--die-with-parent` | `--unshare-net` | supported when `bwrap` exists |
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
- Allowed unconditionally: `process-exec`, `process-fork`, `signal` to
  same-sandbox processes, `process-info*` (same-sandbox), `sysctl-read`,
  `file-read-metadata`, the root directory handle (`file-read-data` on
  `/`, so path lookups resolve), and `file-read*`/`file-write*` on
  `/dev/null`, `/dev/zero`, `/dev/random`, `/dev/urandom`.
- Broad `file-read*` so toolchains run from anywhere, with the fixed
  credentials denylist denied over it (more specific filters win).
- Per grant: `file-read*` + `file-write*` on the canonicalized subpath.
- `Network: deny` adds nothing (default deny); `inherit` would add
  `(allow network*)` and the DNS mach-lookups — reserved for a future mode
  and not generated today.
- The profile is built from the `Boundary` values only; command text, tool
  arguments and model output never appear in it.

## Failure and cleanup

- The wrapper is a process: sandboxed commands are killed by cancelling the
  context like any other command (the app's process-group stop applies).
  That stop is a **process-group** kill, and it must be wired into the shell
  tool's command, not merely written: a direct-child kill leaves the command's
  own children running, and — because they still hold the output pipe — leaves
  the tool call blocked in `Wait` long after its deadline. `WaitDelay` bounds
  that pipe; both halves of the guard belong together.
- A sandboxed command that was killed or timed out has an **unknown
  outcome**: its result says so instead of reporting a clean failure.
- The `Scratch` directory is removed when the command ends — a fresh one
  is made for every shell call, not one per run.

The credential denylist here masks the same stores for a *shell* command
that the file tools refuse (permissions.md) — one list, two enforcement
points, because a mode that denies `shell.exec` still allows `file.read`.
On Linux a bare file (`~/.netrc`) is masked by binding `/dev/null` over
it; a tmpfs needs a directory to mount on, and a store that does not
exist needs no mask at all.

## Invariants

1. In `agent` mode a shell command that writes outside `Workdir`/`Scratch`
   fails, on every supported platform, with the OS's denial — asserted
   live on macOS; on Linux the same grants are enforced by construction
   (read-only root bind, per-grant binds, tmpfs masks, `--unshare-net`).
2. Egress is denied in `agent` mode; a command that opens a network
   connection fails the same way.
3. The sandbox never grants more than `full` mode and never less than
   read-only mode's total denial of `shell.exec`.
4. Wrapping is not authorization: the permission gate (permissions.md) runs
   first; the sandbox bounds what an *allowed* command may touch.
