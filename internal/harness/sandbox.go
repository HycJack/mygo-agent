package harness

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
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

// shellCommand builds the command for one shell tool invocation. Without
// a Sandbox (full access) it is a plain child process; with one, the
// scratch directory is created here — the provider owns the boundary,
// the harness owns the child's environment and the cleanup order.
func shellCommand(ctx context.Context, o ToolOptions, workdir, name string, arg ...string) (*exec.Cmd, func(), error) {
	if o.Sandbox == nil {
		cmd := exec.CommandContext(ctx, name, arg...)
		cmd.Dir = workdir
		return cmd, nil, nil
	}
	scratch, err := os.MkdirTemp("", "mygo-sandbox-")
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() { os.RemoveAll(scratch) }
	if err := os.MkdirAll(filepath.Join(scratch, "tmp"), 0o700); err != nil {
		cleanup()
		return nil, nil, err
	}
	cmd, wrapCleanup, err := o.Sandbox.Command(ctx, Boundary{
		Workdir: canonical(workdir),
		Scratch: scratch,
		Network: "deny",
	}, name, arg...)
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	if wrapCleanup != nil {
		inner := cleanup
		cleanup = func() { wrapCleanup(); inner() }
	}
	cmd.Dir = workdir
	// The child sees a minimal environment pointing at the boundary's
	// writable places, so caches and temp files land inside the grants.
	cmd.Env = []string{
		"HOME=" + scratch,
		"TMPDIR=" + filepath.Join(scratch, "tmp"),
		"PATH=" + os.Getenv("PATH"),
	}
	return cmd, cleanup, nil
}

// canonical absolutizes and resolves a grant path: grants enter profiles
// as real paths, never through a symlink the child could sway.
func canonical(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	return abs
}
