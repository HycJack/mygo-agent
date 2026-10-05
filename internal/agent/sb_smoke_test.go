package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSeatbeltSmoke(t *testing.T) {
	if _, err := os.Stat("/usr/bin/sandbox-exec"); err != nil {
		t.Skip("no sandbox-exec")
	}
	dir := t.TempDir()
	o := ToolOptions{Sandbox: true}
	cmd, cleanup, err := shellCommand(context.Background(), o, dir, "/bin/sh", "-c", "echo inside > "+filepath.Join(dir, "ok.txt")+"; echo hello")
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.CombinedOutput()
	t.Logf("write-in-workdir: out=%q err=%v", string(out), err)
	if err != nil {
		t.Fatalf("write inside workdir failed: %v\n%s", err, out)
	}

	home, _ := os.UserHomeDir()
	target := filepath.Join(home, ".mygo-sandbox-smoke")
	cmd2, cleanup2, err := shellCommand(context.Background(), o, dir, "/bin/sh", "-c", "echo pwned > "+target)
	if cleanup2 != nil {
		defer cleanup2()
	}
	if err != nil {
		t.Fatal(err)
	}
	out2, err2 := cmd2.CombinedOutput()
	t.Logf("write-outside: out=%q err=%v", string(out2), err2)
	if err2 == nil {
		os.Remove(target)
		t.Fatalf("write OUTSIDE workdir was allowed!\n%s", out2)
	}
	os.Remove(target)

	// network deny: a connect attempt should fail
	cmd3, cleanup3, err := shellCommand(context.Background(), o, dir, "/bin/sh", "-c", "nc -z -w 2 127.0.0.1 9 >/dev/null 2>&1; echo nc-exit=$?")
	if cleanup3 != nil {
		defer cleanup3()
	}
	if err != nil {
		t.Fatal(err)
	}
	out3, _ := cmd3.CombinedOutput()
	t.Logf("net-deny: %s", string(out3))
}
