//go:build linux

package agent

import (
	"context"
	"fmt"
	"os"
	"os/exec"
)

// sandboxedCommand wraps argv in bubblewrap mount namespaces, following
// agent-foundation's envd recipe (spec/sandbox.md): unshared pid/ipc/uts,
// a new session that dies with the parent, all capabilities dropped, a
// cleared environment, read-only system roots, read-write grants for the
// workdir and scratch, and the network namespace unshared (deny).
func sandboxedCommand(ctx context.Context, b boundary, name string, arg ...string) (*exec.Cmd, error) {
	bwrap, err := exec.LookPath("bwrap")
	if err != nil {
		return nil, fmt.Errorf("sandboxed execution needs bubblewrap (bwrap) on PATH on Linux; install it, or switch the approval mode to Full Access to run unsandboxed")
	}
	argv := []string{
		bwrap,
		"--unshare-pid", "--unshare-ipc", "--unshare-uts",
		"--new-session", "--die-with-parent",
		"--cap-drop", "ALL",
		"--clearenv",
		// Network deny: an empty network namespace with no interfaces.
		"--unshare-net",
	}
	// Read-only system roots: the compiler toolchain and its data.
	for _, ro := range []string{"/usr", "/bin", "/sbin", "/lib", "/lib64", "/etc/ssl", "/etc/passwd", "/etc/group", "/etc/localtime", "/etc/resolv.conf"} {
		if pathExists(ro) {
			argv = append(argv, "--ro-bind", ro, ro)
		}
	}
	argv = append(argv,
		"--proc", "/proc",
		"--dev", "/dev",
		"--tmpfs", "/tmp",
	)
	// The grants, canonicalized by the caller: workdir and scratch
	// read-write at their own paths.
	for _, grant := range []string{b.Workdir, b.Scratch} {
		if grant == "" {
			continue
		}
		argv = append(argv, "--bind", grant, grant)
	}
	argv = append(argv, "--", name)
	argv = append(argv, arg...)
	// --clearenv wiped everything; the child's HOME/TMPDIR/PATH arrive as
	// the environment of the bwrap process itself, which passes them
	// through to the payload below "--".
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Env = []string{}
	return cmd, nil
}

func pathExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && (st.IsDir() || st.Mode().IsRegular())
}
