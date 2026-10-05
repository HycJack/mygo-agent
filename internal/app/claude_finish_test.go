//go:build !windows

package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestClaudeFinishAfterResult pins the end-of-turn contract: the CLI
// sends the result line and then stays alive waiting for the next input
// message, so the host must close stdin itself. A runner that waits for
// the CLI to blink first never calls finish — the spinner would spin
// and the send button stay a stop button forever.
func TestClaudeFinishAfterResult(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-claude.sh")
	body := `#!/bin/bash
echo '{"type":"system","subtype":"init","session_id":"sess-1"}'
echo '{"type":"assistant","message":{"content":[{"type":"text","text":"ok"}]}}'
echo '{"type":"result","subtype":"success","is_error":false,"duration_ms":12,"total_cost_usd":0.01}'
# The real CLI keeps stdout open and waits for the next input message;
# it leaves only when its stdin closes.
while read -r _; do :; done
`
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

	waitFor(t, 10*time.Second, func() bool {
		done := false
		a.update(func() { done = !a.running && !th.Messages[0].Running })
		return done
	})
	a.update(func() {
		if th.Messages[0].Text != "ok" {
			t.Fatalf("reply text %q", th.Messages[0].Text)
		}
	})
}
