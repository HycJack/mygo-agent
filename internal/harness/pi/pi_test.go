package pi

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mygo-agent/internal/harness"
)

// TestRunFeed drives the JSON wire end to end with a fake CLI: deltas
// stream the reply text, the completed message repeats it whole (emitted
// once), tool calls become cards that the turn's toolResults settle, the
// session id is emitted, and Run returns even though the CLI lingers —
// closing time is the host's job, never the CLI's.
func TestRunFeed(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-pi.sh")
	body := `#!/bin/bash
echo '{"type":"session","version":3,"id":"pi-sess-1"}'
echo '{"type":"agent_start"}'
echo '{"type":"message_update","assistantMessageEvent":{"type":"thinking_delta","delta":"thinking…"},"message":{}}'
echo '{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"Hel"},"message":{}}'
echo '{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"lo"},"message":{}}'
echo '{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"Hello"},{"type":"toolCall","id":"tc1","name":"bash","arguments":{"command":"ls"}}],"usage":{"totalTokens":42,"cost":{"total":0.01}}}}'
echo '{"type":"turn_end","message":{},"toolResults":[{"role":"toolResult","toolCallId":"tc1","toolName":"bash","content":[{"type":"text","text":"file.txt"}]}]}'
echo '{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"again"}],"usage":{"totalTokens":60,"cost":{"total":0.02}}}}'
echo '{"type":"agent_settled"}'
# The real CLI keeps its streams open until reaped; the host breaks first.
while read -r _; do :; done
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	var evs []harness.Event
	h := New(script)
	err := h.Run(context.Background(), harness.Turn{Workdir: dir}, func(ev harness.Event) {
		evs = append(evs, ev)
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	text := ""
	for _, ev := range evs {
		if ev.Kind == harness.EventText {
			text += ev.TextDelta
		}
	}
	// Deltas streamed "Hel"+"lo"; the completed message repeated "Hello"
	// whole (emitted once); a second message arrived whole with no deltas.
	if text != "Helloagain" {
		t.Fatalf("reply text %q", text)
	}
	var session, reasoning, note bool
	for _, ev := range evs {
		switch {
		case ev.Kind == harness.EventSession && ev.SessionID == "pi-sess-1":
			session = true
		case ev.Kind == harness.EventReasoning && ev.Text == "thinking…":
			reasoning = true
		case ev.Kind == harness.EventNote && strings.Contains(ev.Text, "0.03"):
			note = true
		}
	}
	if !session || !reasoning || !note {
		t.Fatalf("missing events — session:%v reasoning:%v note:%v", session, reasoning, note)
	}
	var start, end bool
	for _, ev := range evs {
		if ev.Kind == harness.EventToolStart && ev.ToolCall.ID == "tc1" {
			start = true
		}
		if ev.Kind == harness.EventToolEnd && ev.ToolCall.ID == "tc1" && ev.Output == "file.txt" {
			end = true
		}
	}
	if !start || !end {
		t.Fatalf("tool events incomplete — start:%v end:%v", start, end)
	}
}

// TestResumePinsSession checks that a turn carrying a session id passes
// it back through --session-id.
func TestResumePinsSession(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-pi.sh")
	body := `#!/bin/bash
echo "$@" > "{{dir}}/args.txt"
echo '{"type":"session","version":3,"id":"whatever"}'
echo '{"type":"agent_settled"}'
while read -r _; do :; done
`
	body = strings.ReplaceAll(body, "{{dir}}", dir)
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		done <- New(script).Run(context.Background(),
			harness.Turn{Workdir: dir, SessionID: "existing-sess"}, func(harness.Event) {})
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("run did not settle")
	}
	data, err := os.ReadFile(filepath.Join(dir, "args.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "--session-id existing-sess") {
		t.Fatalf("resume id not passed: %s", data)
	}
}
