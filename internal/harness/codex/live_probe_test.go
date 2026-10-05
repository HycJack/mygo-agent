package codex

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"mygo-agent/internal/harness"
)

func TestLiveCodexProbe(t *testing.T) {
	if os.Getenv("LIVE_CODEX") == "" {
		t.Skip("set LIVE_CODEX=1 to run a real turn")
	}
	dir, _ := os.MkdirTemp("", "codexprobe")
	h := New("codex")
	turn := harness.Turn{Prompt: "Reply with exactly: ok", Workdir: dir, Mode: harness.ModeAgent, Effort: 1}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	err := h.Run(ctx, turn, func(ev harness.Event) {
		switch ev.Kind {
		case "text":
			fmt.Print(ev.TextDelta)
		case "session":
			fmt.Printf("\n[session %s]\n", ev.SessionID)
		case "error":
			fmt.Printf("\n[ERROR %s]\n", ev.Err)
		default:
			fmt.Printf("[%s]\n", ev.Kind)
		}
	})
	fmt.Printf("\n=== Run returned: %v\n", err)
}
