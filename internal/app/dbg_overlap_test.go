package app

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestDebugReasoningOverlap(t *testing.T) {
	dir := os.Getenv("MYGO_UI_SHOTS")
	if dir == "" {
		t.Skip("MYGO_UI_SHOTS not set")
	}
	a := shotApp(t)
	long := strings.Repeat("The extraction stopped mid-line because the heredoc body ended before the closing brace was reached, so the parser saw an unterminated block. ", 6)
	now := time.Now()
	th := &Thread{ID: "t-ov", ProjectID: "default", Title: "overlap", Created: now, Updated: now}
	th.Messages = []Message{
		{ID: "u0", Role: "user", Text: "go", At: now},
		{ID: "m1", Role: "assistant", AgentID: "ag-m1", At: now,
			Blocks: []Block{
				{Type: blockReasoning, Open: true, Text: long},
				{Type: blockText, Text: "这是后续的结论文字，如果思考内容溢出，这一行会和它重叠。"},
			}},
	}
	a.threads = append(a.threads, th)
	a.current = "t-ov"
	tt := ui.NewTester(a.view, 1440, 900)
	tt.Frame()
	tt.Frame()
	writeShot(t, tt, dir, "dbg-overlap")
}
