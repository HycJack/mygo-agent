//go:build !windows

package pi

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mygo-agent/internal/harness"
	"mygo-agent/internal/harness/cli"
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

// TestLingerAfterSettledReturns drives a fake CLI that settles and then
// sleeps forever, ignoring its streams. The reap is bounded, so Run
// returns instead of holding the turn — the claude lesson, second
// sighting (spec/cli-backends.md).
func TestLingerAfterSettledReturns(t *testing.T) {
	old := cli.ReapGrace
	cli.ReapGrace = 200 * time.Millisecond
	defer func() { cli.ReapGrace = old }()

	dir := t.TempDir()
	script := filepath.Join(dir, "fake-pi.sh")
	body := `#!/bin/bash
echo '{"type":"session","version":3,"id":"pi-sess-1"}'
echo '{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"hi"}],"usage":{"totalTokens":7,"cost":{"total":0.01}}}}'
echo '{"type":"agent_settled"}'
sleep 30
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	start := time.Now()
	go func() {
		done <- New(script).Run(context.Background(),
			harness.Turn{Workdir: dir, Mode: harness.ModeAgent, Effort: 1}, func(harness.Event) {})
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("run did not settle — the lingering CLI held the turn")
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("run took %s; the reap was not bounded", elapsed)
	}
}

// TestUnknownModeReturnsError pins that a mode pi cannot map to a
// permission fails loudly. Turn.Mode is a bare int with no clamp in the
// repo, and nothing recovers a panic, so an unknown value must not reach
// the flag mapping.
func TestUnknownModeReturnsError(t *testing.T) {
	// A binary that does not exist: Run must reject the turn before it
	// ever spawns anything.
	err := New(filepath.Join(t.TempDir(), "no-such-pi")).Run(context.Background(),
		harness.Turn{Workdir: t.TempDir(), Mode: harness.Mode(99)}, func(harness.Event) {})
	if err == nil || !strings.Contains(err.Error(), "mode") {
		t.Fatalf("err = %v, want an unknown mode error", err)
	}
}

// TestOutOfRangeEffortFallsBack pins that an effort outside the level
// table still maps to a level instead of indexing past the end and
// panicking the dispatch goroutine.
func TestOutOfRangeEffortFallsBack(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-pi.sh")
	body := `#!/bin/bash
echo "$@" > "{{dir}}/args.txt"
echo '{"type":"session","version":3,"id":"pi-sess-1"}'
echo '{"type":"agent_settled"}'
while read -r _; do :; done
`
	body = strings.ReplaceAll(body, "{{dir}}", dir)
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := New(script).Run(context.Background(),
		harness.Turn{Workdir: dir, Mode: harness.ModeAgent, Effort: 99}, func(harness.Event) {}); err != nil {
		t.Fatalf("run: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "args.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "--thinking medium") {
		t.Fatalf("out-of-range effort not mapped to a level: %s", data)
	}
}

// TestSilentCLIIsNotACleanTurn pins the failure mode where a CLI produces
// nothing at all and is then cut off: a hang before its first line, a
// crash that left no stderr, a binary that never started properly. All
// three look identical on the wire, and reporting them as a finished turn
// leaves the user an empty reply with no reason for it — the one outcome
// they cannot act on. The run has to say that it stopped without answering.
func TestSilentCLIIsNotACleanTurn(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "silent-pi.sh")
	// Reads its input, then hangs: no session, no events, no stderr.
	if err := os.WriteFile(script, []byte("#!/bin/bash\nIFS= read -r _\nsleep 300\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := New(script).Run(ctx, harness.Turn{Workdir: dir}, func(harness.Event) {})
	if err == nil {
		t.Fatal("a CLI that produced nothing reported a clean turn")
	}
	msg := err.Error()
	if !strings.Contains(msg, "stopped before it answered") {
		t.Fatalf("the error does not say the CLI never answered: %q", msg)
	}
	if strings.Contains(msg, "pi:pi:") {
		t.Fatalf("the prefix is doubled: %q", msg)
	}
}

// TestReasonNamesWhatIsKnown checks the fallback chain: a killed run with
// no stderr and no wait error still has to say something true, rather
// than nothing at all.
func TestReasonNamesWhatIsKnown(t *testing.T) {
	var empty strings.Builder
	if got := piReason(empty, nil, true); got == "" {
		t.Fatal("a killed run with nothing on stderr produced no reason at all")
	}
	if got := piReason(empty, nil, false); got == "" {
		t.Fatal("a run that exited silently produced no reason at all")
	}
	if got := piReason(empty, os.ErrProcessDone, true); !strings.Contains(got, "process") {
		t.Fatalf("the wait error should win over the generic wording: %q", got)
	}
}
