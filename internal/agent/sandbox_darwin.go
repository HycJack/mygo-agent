//go:build darwin

package agent

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// systemReadOnlyRoots are the canonical paths a compiler toolchain and its
// runtime need to read: the system volume, the toolchain, and the timezone
// database behind /etc/localtime. Everything else on disk stays unread.
var systemReadOnlyRoots = []string{
	"/System",
	"/usr/bin",
	"/usr/lib",
	"/usr/libexec",
	"/bin",
	"/sbin",
	"/private/var/db/timezone",
	"/private/etc",
	"/etc",
}

// sandboxedCommand wraps argv in a Seatbelt profile launched with
// /usr/bin/sandbox-exec. The profile shape follows agent-foundation's
// envd worker confinement (spec/sandbox.md): (deny default), process
// allowances, the root-directory handle native process startup opens,
// read-only system roots, read-write grants for the workdir and scratch,
// network denied. Only Boundary values enter the profile — never command
// text or tool arguments.
func sandboxedCommand(ctx context.Context, b boundary, name string, arg ...string) (*exec.Cmd, error) {
	if _, err := exec.LookPath("sandbox-exec"); err != nil {
		return nil, fmt.Errorf("sandboxed execution needs /usr/bin/sandbox-exec on macOS, which was not found; switch the approval mode to Full Access to run unsandboxed")
	}
	argv := append([]string{"-p", seatbeltProfile(b), "--", name}, arg...)
	return exec.CommandContext(ctx, "/usr/bin/sandbox-exec", argv...), nil
}

// seatbeltProfile renders the SBPL for one boundary.
func seatbeltProfile(b boundary) string {
	var sb strings.Builder
	sb.WriteString("(version 1)\n(deny default)\n")
	sb.WriteString("(allow process-exec process-fork)\n")
	sb.WriteString("(allow signal (target same-sandbox))\n")
	sb.WriteString("(allow process-info* (target same-sandbox))\n")
	sb.WriteString("(allow sysctl-read)\n")
	// Native process startup opens the root directory itself; this
	// permits that handle, not recursive access to ungranted contents.
	sb.WriteString("(allow file-read-data (literal \"/\"))\n")
	// Metadata (stat) of anything: path resolution needs it, and it
	// carries no file contents.
	sb.WriteString("(allow file-read-metadata)\n")
	for _, dev := range []string{"/dev/null", "/dev/zero", "/dev/random", "/dev/urandom"} {
		fmt.Fprintf(&sb, "(allow file-read* file-write* (literal %q))\n", dev)
	}
	for _, root := range systemReadOnlyRoots {
		fmt.Fprintf(&sb, "(allow file-read* (subpath %q))\n", canonical(root))
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
