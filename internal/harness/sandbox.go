package agent

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// boundary is the effective execution boundary for one sandboxed command
// (spec/sandbox.md): the workdir read-write, a fresh scratch directory
// read-write, system roots read-only, network denied.
type boundary struct {
	Workdir string
	Scratch string
	Network string // "deny" — "inherit" is reserved for a future mode
}

// shellCommand builds the command for one shell tool invocation. In full
// access it is a plain child process; with ToolOptions.Sandbox it runs
// inside the boundary.
//
// The returned cleanup removes the scratch directory and must run after
// the command finishes. A platform without a sandbox returns an error
// that names the remedy — never a silently weaker boundary.
func shellCommand(ctx context.Context, o ToolOptions, workdir, name string, arg ...string) (*exec.Cmd, func(), error) {
	if !o.Sandbox {
		cmd := exec.CommandContext(ctx, name, arg...)
		cmd.Dir = workdir
		return cmd, nil, nil
	}
	scratch, err := os.MkdirTemp("", "mygo-sandbox-")
	if err != nil {
		return nil, nil, fmt.Errorf("sandbox: scratch directory: %w", err)
	}
	cleanup := func() { os.RemoveAll(scratch) }
	if err := os.MkdirAll(filepath.Join(scratch, "tmp"), 0o700); err != nil {
		cleanup()
		return nil, nil, err
	}
	b := boundary{Workdir: canonical(workdir), Scratch: scratch, Network: "deny"}
	cmd, err := sandboxedCommand(ctx, b, name, arg...)
	if err != nil {
		cleanup()
		return nil, nil, err
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
