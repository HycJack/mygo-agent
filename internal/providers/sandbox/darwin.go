//go:build darwin

package sandbox

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"mygo-agent/internal/harness"
)

// New returns the platform's sandbox provider.
func New() harness.Sandbox { return sandbox{} }

type sandbox struct{}

// Command wraps argv in a Seatbelt profile launched with
// /usr/bin/sandbox-exec (the harness.Sandbox protocol). The profile shape follows the codex workspace-
// write contract and agent-foundation's envd worker confinement
// (spec/sandbox.md): (deny default), process allowances, the
// root-directory handle native process startup opens, broad reads with a
// credentials denylist, read-write grants for the workdir and scratch,
// network denied. Only Boundary values and the fixed denylist enter the
// profile — never command text or tool arguments.
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
	if _, err := exec.LookPath("sandbox-exec"); err != nil {
		return nil, nil, fmt.Errorf("sandboxed execution needs /usr/bin/sandbox-exec on macOS, which was not found; switch the approval mode to Full Access to run unsandboxed")
	}
	b.Workdir, b.Scratch = canonical(b.Workdir), canonical(b.Scratch)
	if _, err := os.Stat(b.Workdir); err != nil {
		return nil, nil, fmt.Errorf("sandbox: the workdir grant %s does not exist", b.Workdir)
	}
	argv := append([]string{"-p", seatbeltProfile(b), "--", name}, arg...)
	return exec.CommandContext(ctx, "/usr/bin/sandbox-exec", argv...), nil, nil
}

// seatbeltProfile renders the SBPL for one boundary.
func seatbeltProfile(b harness.Boundary) string {
	var sb strings.Builder
	sb.WriteString("(version 1)\n(deny default)\n")
	sb.WriteString("(allow process-exec process-fork)\n")
	sb.WriteString("(allow signal (target same-sandbox))\n")
	sb.WriteString("(allow process-info* (target same-sandbox))\n")
	sb.WriteString("(allow sysctl-read)\n")
	// Native process startup opens the root directory itself; this
	// permits that handle, not recursive access to ungranted contents.
	sb.WriteString("(allow file-read-data (literal \"/\"))\n")
	sb.WriteString("(allow file-read-metadata)\n")
	for _, dev := range []string{"/dev/null", "/dev/zero", "/dev/random", "/dev/urandom"} {
		fmt.Fprintf(&sb, "(allow file-read* file-write* (literal %q))\n", dev)
	}
	// Reads are broad so toolchains work from wherever they are
	// installed; the credentials denylist is more specific and wins.
	sb.WriteString("(allow file-read*)\n")
	for _, p := range credentialStores() {
		fmt.Fprintf(&sb, "(deny file-read* (subpath %q))\n", p)
		fmt.Fprintf(&sb, "(deny file-read-metadata (subpath %q))\n", p)
	}
	for _, grant := range []string{b.Workdir, b.Scratch} {
		if grant == "" {
			continue
		}
		fmt.Fprintf(&sb, "(allow file-read* file-write* (subpath %q))\n", grant)
	}
	// Network stays denied under (deny default): nothing to add for the
	// deny egress mode. The inherit mode is reserved (spec/sandbox.md).
	return sb.String()
}
