package main

import (
	"strings"
	"testing"
)

func TestClaudeLineHandling(t *testing.T) {
	a := newTestApp(t)
	th := &Thread{ID: "t1", ProjectID: "default"}
	th.Messages = []Message{{ID: "m0", Role: "assistant", Running: true}}
	a.threads = append(a.threads, th)

	r := &claudeRun{a: a, th: th, at: 0, cards: map[string]int{}}

	r.handle(`{"type":"system","subtype":"init","session_id":"sess-abc123"}`)
	if th.ClaudeID != "sess-abc123" {
		t.Fatalf("session id %q", th.ClaudeID)
	}

	r.handle(`{"type":"assistant","message":{"content":[{"type":"text","text":"Hello "}]}}`)
	r.handle(`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"tu1","name":"Edit","input":{"file_path":"a.go","old_string":"alpha\n","new_string":"beta\n"}}]}}`)
	r.handle(`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"tu1","content":"file edited"}]}}`)

	m := &th.Messages[0]
	if !strings.HasPrefix(m.Text, "Hello ") {
		t.Fatalf("text %q", m.Text)
	}
	if len(m.Blocks) != 1 {
		t.Fatalf("blocks %d", len(m.Blocks))
	}
	b := m.Blocks[0]
	if b.Type != "diff" || b.File != "a.go" || b.Add != 1 || b.Del != 1 {
		t.Fatalf("edit card: %+v", b)
	}
	if b.Running {
		t.Fatal("the edit card is still running")
	}
}

func TestClaudeResultNote(t *testing.T) {
	a := newTestApp(t)
	th := &Thread{ID: "t1", ProjectID: "default", ClaudeID: "sess-abc123"}
	th.Messages = []Message{{ID: "m0", Role: "assistant", Running: true}}
	a.threads = append(a.threads, th)

	r := &claudeRun{a: a, th: th, at: 0, cards: map[string]int{}}
	r.handle(`{"type":"result","subtype":"success","is_error":false,"duration_ms":12000,"total_cost_usd":0.0042,"result":"done"}`)

	m := &th.Messages[0]
	if len(m.Blocks) != 1 || !strings.Contains(m.Blocks[0].Text, "$0.0042") ||
		!strings.Contains(m.Blocks[0].Text, "sess-ab") {
		t.Fatalf("result note: %+v", m.Blocks)
	}
}
