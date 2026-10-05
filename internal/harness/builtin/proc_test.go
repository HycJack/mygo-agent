//go:build !windows

package builtin

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestBashStopKillsGrandchildHoldingStdout covers the pair of failures a
// child without a process group leaves behind. The background grandchild
// inherits stdout, so killing only `bash -c` both orphans it and keeps
// the output pipe open: the call would block long after the context is
// gone. The marker file says whether the grandchild outlived the stop.
func TestBashStopKillsGrandchildHoldingStdout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the bash tool drives powershell on windows")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("no bash on PATH")
	}
	marker := filepath.Join(t.TempDir(), "alive")
	script := "while true; do echo x >> " + marker + "; sleep 0.05; done & " +
		"echo started; sleep 60"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var bash Tool
	for _, tl := range Tools(t.TempDir(), nil) {
		if tl.Name == "bash" {
			bash = tl
		}
	}
	type result struct {
		out string
		err error
	}
	done := make(chan result, 1)
	go func() {
		out, err := bash.Execute(ctx, `{"command":"`+script+`"}`)
		done <- result{out, err}
	}()
	time.Sleep(300 * time.Millisecond) // let the grandchild start
	cancel()

	select {
	case got := <-done:
		if got.err == nil {
			t.Fatal("a cancelled command must fail")
		}
		if !strings.Contains(got.out, "outcome is unknown") {
			t.Fatalf("the cancelled result must still report an unknown outcome: %q", got.out)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the call did not return: the output pipe is still held open")
	}

	// The grandchild is dead if the file stops growing.
	first := fileSize(t, marker)
	time.Sleep(500 * time.Millisecond)
	if grew := fileSize(t, marker) - first; grew > 0 {
		t.Fatalf("a grandchild outlived the stop: the marker grew by %d bytes", grew)
	}
}

func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return st.Size()
}
