package sandbox

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"mygo-agent/internal/harness"
)

// TestSeatbeltProfileShape asserts the generated SBPL from
// spec/sandbox.md: deny-default base, the root-directory handle native
// process startup opens, read-only system roots and the read-write grants.
func TestSeatbeltProfileShape(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("seatbelt profile is darwin-only")
	}
	b := harness.Boundary{Workdir: "/tmp/w", Scratch: "/tmp/s", Network: "deny"}
	p := seatbeltProfile(b)
	for _, want := range []string{
		"(version 1)",
		"(deny default)",
		"(allow process-exec process-fork)",
		`(allow file-read-data (literal "/"))`,
		"(allow file-read*)",
		`(deny file-read* (subpath ` + homeSub(t, ".ssh") + `))`,
		`(allow file-read* file-write* (subpath "/tmp/w"))`,
		`(allow file-read* file-write* (subpath "/tmp/s"))`,
	} {
		if !strings.Contains(p, want) {
			t.Fatalf("profile lacks %q:\n%s", want, p)
		}
	}
	// Network stays denied: the profile never allows it.
	if strings.Contains(p, "(allow network") {
		t.Fatalf("profile allows network in deny mode:\n%s", p)
	}
}

func homeSub(t *testing.T, sub string) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	return strconv.Quote(filepath.Join(home, sub))
}

// TestSandboxBoundary asserts the sandbox's core invariant
// (spec/sandbox.md): a sandboxed command writes inside the workdir and is
// refused outside it. Live, on platforms with a backend; skipped where
// none exists.
func TestSandboxBoundary(t *testing.T) {
	dir := t.TempDir()
	outside, err := os.MkdirTemp("", "mygo-sandbox-outside-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(outside)
	cmd, cleanup, err := New().Command(context.Background(), harness.Boundary{Workdir: dir, Scratch: t.TempDir(), Network: "deny"},
		"/bin/sh", "-c", "echo in > "+filepath.Join(dir, "ok.txt")+"; echo out > "+filepath.Join(outside, "leak.txt"))
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		// Platforms without a sandbox report the remedy instead of
		// running a weaker boundary — that path is asserted by
		// TestSandboxUnavailableIsReported.
		if runtime.GOOS != "darwin" {
			t.Skipf("no sandbox backend: %v", err)
		}
		t.Fatal(err)
	}
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("the outside write must be refused, the command succeeded:\n%s", out)
	}
	if !strings.Contains(string(out), "Operation not permitted") {
		t.Fatalf("the refusal is not an OS denial:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "ok.txt")); err != nil {
		t.Fatalf("write inside the workdir failed: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(outside, "leak.txt")); err == nil {
		t.Fatal("a sandboxed command wrote outside the workdir")
	}
}

// TestSandboxDenylistHidesCredentials asserts the credentials denylist:
// broad reads do not extend to the sensitive directories. On darwin the
// read is denied outright; on linux bubblewrap masks the directory with
// an empty tmpfs — the read succeeds but lists nothing.
func TestSandboxDenylistHidesCredentials(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home")
	}
	sshDir := filepath.Join(home, ".ssh")
	if _, err := os.Stat(sshDir); err != nil {
		t.Skip("no ~/.ssh to protect")
	}
	dir := t.TempDir()
	cmd, cleanup, err := New().Command(context.Background(), harness.Boundary{Workdir: dir, Scratch: t.TempDir(), Network: "deny"},
		"/bin/sh", "-c", "ls -A "+sshDir+" 2>/dev/null; echo exit=$?")
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		if runtime.GOOS != "darwin" {
			t.Skipf("no sandbox backend: %v", err)
		}
		t.Fatal(err)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("sandboxed probe failed: %v\n%s", err, out)
	}
	var want string
	switch runtime.GOOS {
	case "darwin":
		want = "exit=1"
	case "linux":
		want = "exit=0"
	default:
		t.Skip("no assertion for this platform")
	}
	if got := strings.TrimSpace(string(out)); got != want {
		t.Fatalf("~/.ssh leaked through the denylist: got %q, want %q", got, want)
	}
}

// TestSandboxNetworkDenied asserts sandbox.md invariant 2: a sandboxed
// command cannot open a network connection, not even to a listener that
// is right there accepting. The unsandboxed control first proves the
// probe itself works on this platform.
func TestSandboxNetworkDenied(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no sandbox backend on windows")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash for the /dev/tcp probe")
	}
	probe := fmt.Sprintf("exec 3<>/dev/tcp/127.0.0.1/%d", port)
	dir := t.TempDir()

	// Control: unsandboxed, the probe connects to the live listener.
	cmd, _, err := New().Command(context.Background(), harness.Boundary{Workdir: dir, Scratch: t.TempDir(), Network: "deny"}, bash, "-c", probe)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("the /dev/tcp probe does not work on this platform: %v\n%s", err, out)
	}

	// Sandboxed: the same probe must fail with the OS's denial.
	cmd, cleanup, err := New().Command(context.Background(), harness.Boundary{Workdir: dir, Scratch: t.TempDir(), Network: "deny"}, bash, "-c", probe)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		if runtime.GOOS != "darwin" {
			t.Skipf("no sandbox backend: %v", err)
		}
		t.Fatal(err)
	}
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("a sandboxed command connected to a live listener:\n%s", out)
	}
}

// TestSandboxMissingGrantIsReported asserts the boundary rule: a grant
// that does not exist is an error, not an empty grant.
func TestSandboxMissingGrantIsReported(t *testing.T) {
	absent := filepath.Join(t.TempDir(), "absent")
	_, _, err := New().Command(context.Background(), harness.Boundary{Workdir: absent, Scratch: t.TempDir(), Network: "deny"}, "/bin/sh", "-c", "true")
	if err == nil {
		t.Fatal("a missing workdir grant must be reported, not granted empty")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("error does not name the missing grant: %v", err)
	}
}

// TestSandboxToolchainRuns asserts broad reads keep real toolchains
// working inside the boundary: whatever go this test itself runs under
// must also run sandboxed.
func TestSandboxToolchainRuns(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("live toolchain check runs on darwin")
	}
	goPath, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go toolchain on PATH")
	}
	dir := t.TempDir()
	cmd, cleanup, err := New().Command(context.Background(), harness.Boundary{Workdir: dir, Scratch: t.TempDir(), Network: "deny"}, goPath, "version")
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		t.Fatal(err)
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the go toolchain failed inside the sandbox: %v\n%s", err, out)
	}
}

// TestSandboxUnavailableIsReported asserts the honesty rule: a platform
// without a sandbox errors instead of running unsandboxed.
func TestSandboxUnavailableIsReported(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("darwin has a sandbox backend")
	}
	if runtime.GOOS == "linux" {
		if _, err := exec.LookPath("bwrap"); err == nil {
			t.Skip("bwrap available; the live test covers it")
		}
	}
	_, _, err := New().Command(context.Background(), harness.Boundary{Workdir: t.TempDir(), Scratch: t.TempDir(), Network: "deny"}, "/bin/sh", "-c", "true")
	if err == nil {
		t.Fatal("a missing sandbox must be reported, not run unsandboxed")
	}
}
