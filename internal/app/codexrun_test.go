package app

import (
	"strings"
	"testing"
)

// TestCodexRunFeed replays a recorded codex exec session and asserts
// the thread state it produces.
func TestCodexRunFeed(t *testing.T) {
	a := newTestApp(t)
	th := &Thread{ID: "t1", ProjectID: "default"}
	th.Messages = []Message{{ID: "m0", Role: "assistant", Running: true}}
	a.threads = append(a.threads, th)

	r := &codexRun{a: a, th: th, at: 0, blocks: map[string]int{}}
	r.handle(`{"type":"thread.started","thread_id":"sess-42"}`)
	r.handle(`{"type":"assistant","message":{"content":[{"type":"text","text":"ignored legacy shape"}]}}`)
	r.handle(`{"type":"item.started","item":{"id":"item_0","type":"command_execution","command":"go test ./...","status":"in_progress"}}`)
	r.handle(`{"type":"item.updated","item":{"id":"item_0","type":"command_execution","command":"go test ./...","status":"in_progress","aggregated_output":"running…"}}`)
	r.handle(`{"type":"item.completed","item":{"id":"item_0","type":"command_execution","command":"go test ./...","status":"completed","exit_code":0,"aggregated_output":"ok"}}`)
	r.handle(`{"type":"item.completed","item":{"id":"item_1","type":"file_change","changes":[{"path":"main.go","kind":"modify"}],"diff":"--- a\n+++ b\n@@ -1 +1 @@\n-old\n+new"}}`)
	r.handle(`{"type":"item.completed","item":{"id":"item_2","type":"agent_message","text":"All green."}}`)
	r.handle(`{"type":"turn.completed","usage":{"input_tokens":10}}`)

	if th.CodexID != "sess-42" {
		t.Fatalf("session id %q", th.CodexID)
	}
	m := &th.Messages[0]
	if !strings.Contains(m.Text, "All green.") {
		t.Fatalf("reply text %q", m.Text)
	}
	var cmd, diff *Block
	for i := range m.Blocks {
		switch m.Blocks[i].Type {
		case "command":
			cmd = &m.Blocks[i]
		case "diff":
			diff = &m.Blocks[i]
		}
	}
	if cmd == nil || cmd.Text != "go test ./..." || cmd.Output != "ok" || cmd.Exit != 0 || cmd.Running {
		t.Fatalf("command card: %+v", cmd)
	}
	if diff == nil || diff.File != "main.go" || diff.Add != 1 || diff.Del != 1 {
		t.Fatalf("diff card: %+v", diff)
	}
}

func TestCodexRunErrors(t *testing.T) {
	a := newTestApp(t)
	th := &Thread{ID: "t1", ProjectID: "default"}
	th.Messages = []Message{{ID: "m0", Role: "assistant", Running: true}}
	a.threads = append(a.threads, th)

	r := &codexRun{a: a, th: th, at: 0, blocks: map[string]int{}}
	r.handle(`{"type":"error","message":"quota exhausted"}`)
	if len(th.Messages[0].Blocks) != 1 || th.Messages[0].Blocks[0].Type != "error" ||
		!strings.Contains(th.Messages[0].Blocks[0].Text, "quota") {
		t.Fatalf("error block missing: %+v", th.Messages[0].Blocks)
	}
	// Garbage lines are skipped without effect.
	r.handle("not json")
	r.handle("")
	if len(th.Messages[0].Blocks) != 1 {
		t.Fatal("garbage lines changed the state")
	}
}
