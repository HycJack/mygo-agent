//go:build !windows

package claude

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"mygo-agent/internal/harness"
)

// TestLiveClaudeCompactionProbe drives a real claude through a context
// compaction and checks the note reaches the host event stream.
//
// The synthetic test proves the frame is mapped correctly; only this
// proves the frame is still on the wire. Everything about it is the
// CLI's to change — `compact_boundary` is a system subtype claude chose,
// and `compact_metadata` is snake_case on a stream where the neighbouring
// fields are not. A rename upstream would leave the unit test green and
// the transcript silent, which is the failure this catches.
//
// Opt-in, and it costs a real turn: it reads a file, then compacts.
//
//	LIVE_CLAUDE_COMPACT=1 go test ./internal/harness/claude -run LiveClaudeCompaction
func TestLiveClaudeCompactionProbe(t *testing.T) {
	if os.Getenv("LIVE_CLAUDE_COMPACT") == "" {
		t.Skip("set LIVE_CLAUDE_COMPACT=1 to run a real compaction (costs a turn)")
	}
	h := New("claude")
	var session string
	sawCompaction := false

	turn := func(prompt, sid string, timeout time.Duration) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		err := h.Run(ctx, harness.Turn{
			Prompt: prompt, Workdir: "/Users/yicaohuang/mygo-agent",
			Mode: harness.ModeAgent, Effort: 1, SessionID: sid,
			OnApproval: func(context.Context, harness.ApprovalRequest) harness.ApprovalDecision {
				return harness.ApprovalDecision{Approved: true}
			},
			OnOutsideDir: func(context.Context, harness.OutsideDirRequest) bool { return true },
		}, func(ev harness.Event) {
			switch ev.Kind {
			case harness.EventSession:
				session = ev.SessionID
				t.Logf("session %s", ev.SessionID)
			case harness.EventNote:
				t.Logf("note: %s", ev.Text)
				if strings.Contains(ev.Text, "compacted") {
					sawCompaction = true
				}
			}
		})
		if err != nil {
			t.Fatalf("turn %q: %v", prompt, err)
		}
	}

	turn("Read internal/harness/paths.go in full with the Read tool, then reply OK.",
		"", 150*time.Second)
	// /compact is a real command in print mode: an empty session answers
	// "No messages to compact", which is why the first turn has to run.
	turn("/compact", session, 210*time.Second)

	if !sawCompaction {
		t.Fatal("a real compaction produced no note: claude may have moved " +
			"or renamed the compact_boundary frame (spec/cli-backends.md)")
	}
}
