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

// TestMarkdownListItemSelectsAndCopies: a bullet's text is selectable —
// a drag over the item copies it (marker excluded).
func TestMarkdownListItemSelectsAndCopies(t *testing.T) {
	cache := NewMdCache()
	tt := ui.NewTester(func(c *ui.Context) {
		Markdown(c, cache, "m1", "- retry the flaky case twice", true, CodexPalette())
	}, 600, 120)
	tt.Frame()
	r, ok := tt.Find("retry the flaky case twice")
	if !ok {
		t.Fatalf("the item text did not render; visible: %v", tt.Texts())
	}
	tt.Press(r.X+2, r.Y+8)
	tt.Move(r.X+r.W-2, r.Y+8)
	tt.Release(r.X+r.W-2, r.Y+8)
	tt.Key(ui.Cmd, ui.KeyC)
	if got := tt.Clipboard(); got != "retry the flaky case twice" {
		t.Fatalf("a drag over the list item copied %q", got)
	}
}

// TestMarkdownTableCellSelectsAndCopies: table cells are selectable —
// the header's weight rides the spans form.
func TestMarkdownTableCellSelectsAndCopies(t *testing.T) {
	const src = "| Plan | Status |\n| --- | --- |\n| ship it | green |"
	cache := NewMdCache()
	tt := ui.NewTester(func(c *ui.Context) {
		Markdown(c, cache, "m1", src, true, CodexPalette())
	}, 600, 160)
	tt.Frame()
	r, ok := tt.Find("ship it")
	if !ok {
		t.Fatalf("the cell did not render; visible: %v", tt.Texts())
	}
	tt.Press(r.X+1, r.Y+8)
	tt.Move(r.X+r.W-1, r.Y+8)
	tt.Release(r.X+r.W-1, r.Y+8)
	tt.Key(ui.Cmd, ui.KeyC)
	if got := tt.Clipboard(); got != "ship it" {
		t.Fatalf("a drag over the cell copied %q", got)
	}
}

// TestMarkdownCrossParagraphSelects: consecutive paragraphs (and the
// blank line between them) are one selectable element — a drag from the
// first into the second copies both.
func TestMarkdownCrossParagraphSelects(t *testing.T) {
	const src = "first paragraph of the plan\n\nsecond paragraph of the plan"
	cache := NewMdCache()
	tt := ui.NewTester(func(c *ui.Context) {
		Markdown(c, cache, "m1", src, true, CodexPalette())
	}, 600, 200)
	tt.Frame()
	// Drag from the first paragraph's line into the second (blank line
	// between them: one line-height apart plus one).
	tt.Press(2, 10)
	tt.Move(380, 54)
	tt.Release(380, 54)
	tt.Key(ui.Cmd, ui.KeyC)
	got := tt.Clipboard()
	if !strings.Contains(got, "first paragraph") || !strings.Contains(got, "second paragraph") {
		t.Fatalf("a drag across the paragraphs copied %q; visible: %v", got, tt.Texts())
	}
}

// TestMarkdownLastParagraphSelects: a complete message's LAST paragraph
// flushes into the parts path too (forMsg's complete tail) — before
// this rode the live tail's element form and could not be selected at
// all. One line, one drag, one copy.
func TestMarkdownLastParagraphSelects(t *testing.T) {
	const src = "single line answer with no trailing newline"
	cache := NewMdCache()
	tt := ui.NewTester(func(c *ui.Context) {
		Markdown(c, cache, "m1", src, true, CodexPalette())
	}, 600, 120)
	tt.Frame()
	tt.Press(2, 10)
	tt.Move(430, 10)
	tt.Release(430, 10)
	tt.Key(ui.Cmd, ui.KeyC)
	if got := tt.Clipboard(); got != src {
		t.Fatalf("a drag over the last paragraph copied %q; visible: %v", got, tt.Texts())
	}
}
