package harness

import (
	"context"
	"os/exec"
)

// Boundary is the effective execution boundary for one sandboxed command
// (spec/sandbox.md): the workdir read-write, a fresh scratch directory
// read-write (it becomes the child's HOME and TMPDIR), network denied.
type Boundary struct {
	Workdir string
	Scratch string
	Network string // "deny" — "inherit" is reserved for a future mode
}

// Sandbox wraps one command invocation in a boundary. A platform without
// a backend returns an error naming the remedy — never a silently weaker
// boundary (the honesty rule in spec/sandbox.md). Implementations live in
// internal/providers/sandbox; the harness knows the protocol only.
type Sandbox interface {
	Command(ctx context.Context, b Boundary, name string, arg ...string) (*exec.Cmd, func(), error)
}
