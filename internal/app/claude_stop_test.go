//go:build !windows

package app

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// alive reports whether a process still responds to signal 0.
func alive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}

func waitFor(t *testing.T, timeout time.Duration, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if done() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("timed out")
}

// readIntFile polls until the file holds a pid.
func readIntFile(t *testing.T, path string) int {
	t.Helper()
	waitFor(t, 5*time.Second, func() bool {
		data, err := os.ReadFile(path)
		return err == nil && strings.TrimSpace(string(data)) != ""
	})
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// TestClaudeStopKillsProcessGroup proves the stop button really kills
// the claude CLI and the tool processes it spawned: a fake claude
// script records its own pid and a grandchild's, streams forever, and
// after a.stop() nothing of the group may survive.
func TestClaudeStopKillsProcessGroup(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-claude.sh")
	body := `#!/bin/bash
echo $$ > "{{dir}}/claude.pid"
sleep 300 &
echo $! > "{{dir}}/child.pid"
echo '{"type":"system","subtype":"init","session_id":"sess-1"}'
while true; do
  echo '{"type":"assistant","message":{"content":[{"type":"text","text":"tick "}]}}'
  sleep 0.1
done
`
	body = strings.ReplaceAll(body, "{{dir}}", dir)
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	a := newTestApp(t)
	a.backend = "claude"
	a.claudePath = script
	th := &Thread{ID: "t1", ProjectID: "default"}
	th.Messages = []Message{{ID: "m0", Role: "assistant", Running: true}}
	a.threads = append(a.threads, th)

	go runBackend(a, th, "hello", 0)

	// The fake claude is streaming.
	waitFor(t, 10*time.Second, func() bool {
		ticked := false
		a.update(func() { ticked = strings.Contains(th.Messages[0].Text, "tick") })
		return ticked
	})

	// The fake claude records its own pid; the host no longer tracks it.
	claudePid := readIntFile(t, filepath.Join(dir, "claude.pid"))
	if !alive(claudePid) {
		t.Fatalf("claude process %d is not running", claudePid)
	}
	childPid := readIntFile(t, filepath.Join(dir, "child.pid"))
	if !alive(childPid) {
		t.Fatal("the grandchild died on its own before the stop")
	}

	// The stop button.
	a.stop()

	waitFor(t, 8*time.Second, func() bool {
		stopped := false
		a.update(func() { stopped = !a.running })
		return stopped && !alive(claudePid)
	})
	if alive(claudePid) {
		t.Fatal("claude survived the stop")
	}
	if alive(childPid) {
		t.Fatal("the tool process spawned by claude survived the stop")
	}
	stillRunning := false
	a.update(func() { stillRunning = th.Messages[0].Running })
	if stillRunning {
		t.Fatal("the reply is still marked running")
	}
}
