package ui

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestMarkdownParagraphSelectsAndCopies pins the copy path end to end:
// a rendered paragraph (bold span included) is one selectable element —
// a drag across it copies the whole line. The paragraph rides
// constructor spans, whose selectable editor receives the press
// (a Children-built RichText's spans swallow it — the upstream report
// is cmd/richtext-repro).
func TestMarkdownParagraphSelectsAndCopies(t *testing.T) {
	const src = "Order **12345** shipped yesterday"
	cache := NewMdCache()
	tt := ui.NewTester(func(c *ui.Context) {
		Markdown(c, cache, "m1", src, true, CodexPalette())
	}, 600, 200)
	tt.Frame()

	// The paragraph sits at the top of the frame; drag across its
	// content width on the first line.
	y := float32(10)
	tt.Press(2, y)
	tt.Move(232, y)
	tt.Release(232, y)
	tt.Key(ui.Cmd, ui.KeyC)
	if got := tt.Clipboard(); got != "Order 12345 shipped yesterday" {
		t.Fatalf("a drag across the paragraph copied %q; visible: %v", got, tt.Texts())
	}
}

// TestMarkdownMultiLineParagraphSelectsAcrossLines: consecutive
// paragraph lines ride one selectable element — a drag from the first
// line into the second copies both.
func TestMarkdownMultiLineParagraphSelectsAcrossLines(t *testing.T) {
	const src = "first line of the answer\nsecond line of the answer"
	cache := NewMdCache()
	tt := ui.NewTester(func(c *ui.Context) {
		Markdown(c, cache, "m1", src, true, CodexPalette())
	}, 600, 200)
	tt.Frame()

	// Drag from the first line into the second.
	tt.Press(2, 10)
	tt.Move(380, 34)
	tt.Release(400, 34)
	tt.Key(ui.Cmd, ui.KeyC)
	got := tt.Clipboard()
	if !strings.Contains(got, "first line") || !strings.Contains(got, "second line") {
		t.Fatalf("a drag across the paragraph copied %q; visible: %v", got, tt.Texts())
	}
}
