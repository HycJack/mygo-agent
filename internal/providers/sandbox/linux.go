//go:build linux

package sandbox

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"mygo-agent/internal/harness"
)

// sandboxedCommand wraps argv in bubblewrap mount namespaces, following
// agent-foundation's envd recipe (spec/sandbox.md): unshared pid/ipc/uts,
// a new session that dies with the parent, all capabilities dropped, the
// host filesystem read-only with credential directories masked out,
// read-write grants for the workdir and scratch, and the network
// namespace unshared (deny).
// canonical resolves a grant path: grants enter profiles as real paths,
// never through a symlink the child could sway.
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

func (sandbox) Command(ctx context.Context, b harness.Boundary, name string, arg ...string) (*exec.Cmd, func(), error) {
	bwrap, err := exec.LookPath("bwrap")
	if err != nil {
		return nil, nil, fmt.Errorf("sandboxed execution needs bubblewrap (bwrap) on PATH on Linux; install it, or switch the approval mode to Full Access to run unsandboxed")
	}
	b.Workdir, b.Scratch = canonical(b.Workdir), canonical(b.Scratch)
	if _, err := os.Stat(b.Workdir); err != nil {
		return nil, nil, fmt.Errorf("sandbox: the workdir grant %s does not exist", b.Workdir)
	}
	argv := []string{
		bwrap,
		"--unshare-pid", "--unshare-ipc", "--unshare-uts",
		"--new-session", "--die-with-parent",
		"--cap-drop", "ALL",
		// Network deny: an empty network namespace with no interfaces.
		"--unshare-net",
		// The whole host filesystem read-only, so toolchains work from
		// wherever they are installed; grants below are mounted
		// read-write over it.
		"--ro-bind", "/", "/",
		"--proc", "/proc",
		"--dev", "/dev",
	}
	// Mask the credential stores: an empty tmpfs over each directory, a
	// /dev/null bind over each bare file (.netrc) — a tmpfs needs a
	// directory to mount on, and a bind mount of the null device reads
	// as empty. A store that does not exist needs no mask, and bwrap
	// fails on a mount point that is not there.
	for _, p := range credentialStores() {
		st, err := os.Stat(p)
		if err != nil {
			continue
		}
		if st.IsDir() {
			argv = append(argv, "--tmpfs", p)
		} else {
			argv = append(argv, "--ro-bind", "/dev/null", p)
		}
	}
	for _, grant := range []string{b.Workdir, b.Scratch} {
		if grant == "" {
			continue
		}
		argv = append(argv, "--bind", grant, grant)
	}
	argv = append(argv, "--", name)
	argv = append(argv, arg...)
	// The payload's environment comes from the command's Env
	// (HOME/TMPDIR/PATH into the scratch dir), set by the caller.
	return exec.CommandContext(ctx, argv[0], argv[1:]...), nil, nil
}

// sandbox is the bubblewrap-backed provider.
type sandbox struct{}

// New returns the platform's sandbox provider.
func New() harness.Sandbox { return sandbox{} }
