//go:build !windows

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestCodexStopKillsProcessGroup mirrors the Claude Code stop test for
// the codex backend: a fake codex CLI records its pid and a
// grandchild's and streams forever; after a.stop() nothing of the
// group may survive.
func TestCodexStopKillsProcessGroup(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-codex.sh")
	body := `#!/bin/bash
echo $$ > "{{dir}}/claude.pid"
sleep 300 &
echo $! > "{{dir}}/child.pid"
echo '{"type":"thread.started","thread_id":"sess-1"}'
while true; do
  echo '{"type":"turn.started"}'
  sleep 0.1
done
`
	body = strings.ReplaceAll(body, "{{dir}}", dir)
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	a := newTestApp(t)
	a.codexPath = script
	th := &Thread{ID: "t1", ProjectID: "default"}
	th.Messages = []Message{{ID: "m0", Role: "assistant", Running: true}}
	a.threads = append(a.threads, th)

	go a.runCodex(th, "hello", 0)

	waitFor(t, 10*time.Second, func() bool {

		return a.codexPid != 0 && !th.Messages[0].Running == false
	})
	_ = th

	claudePid := a.codexPid
	if claudePid == 0 || !alive(claudePid) {
		t.Fatalf("codex process %d is not running", claudePid)
	}
	childPid := readIntFile(t, filepath.Join(dir, "child.pid"))
	if !alive(childPid) {
		t.Fatal("the grandchild died on its own before the stop")
	}

	a.stop()

	waitFor(t, 8*time.Second, func() bool { return !a.running && !alive(claudePid) })
	if alive(claudePid) {
		t.Fatal("codex survived the stop")
	}
	if alive(childPid) {
		t.Fatal("the tool process spawned by codex survived the stop")
	}
	if th.Messages[0].Running {
		t.Fatal("the reply is still marked running")
	}
}
