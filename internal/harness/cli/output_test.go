package cli

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

// A command's output reaches a card as the child wrote it, and on
// Windows that ends in CRLF. Trimming only the newline left a carriage
// return inside the stored text: an invisible-but-present character on
// screen, and a mismatch for anything comparing the card's words to the
// command's. These pin the platform difference at the one place every
// adapter's output passes through.
func TestTrimOutputDropsTheCarriageReturn(t *testing.T) {
	cases := []struct{ in, want string }{
		{"hello\n", "hello"},
		{"hello\r\n", "hello"},     // Windows
		{"hello\r\n\r\n", "hello"}, // two trailing lines
		{"a\r\nb\r\n", "a\r\nb"},   // only the trailing one
		{"", ""},
		{"\r\n", ""},
		{"no newline", "no newline"},
	}
	for _, c := range cases {
		if got := TrimOutput(c.in, 1<<20); got != c.want {
			t.Errorf("TrimOutput(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestTrimOutputStillCaps(t *testing.T) {
	long := ""
	for range 100 {
		long += "0123456789"
	}
	got := TrimOutput(long, 20)
	if !strings.Contains(got, "output truncated") {
		t.Fatalf("a long output was not marked as truncated: %q", got)
	}
	if len(got) >= len(long) {
		t.Fatalf("a long output was not shortened: %d >= %d", len(got), len(long))
	}
}

// TestTrimOutputKeepsBothEnds pins the shape: a command's errors live at
// the end of its output and a file's structure at its start, so the cut
// keeps both ends around the marker instead of only the head.
func TestTrimOutputKeepsBothEnds(t *testing.T) {
	var b strings.Builder
	for i := range 3000 {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	long := b.String()
	got := TrimOutput(long, 600)
	// The trailing newline the trim removes first is not part of the tail.
	src := strings.TrimRight(long, "\r\n")
	head := src[:300]
	tail := src[len(src)-200:]
	if !strings.HasPrefix(got, head) {
		t.Fatalf("the head of the output was lost: %q", got[:60])
	}
	if !strings.HasSuffix(got, tail) {
		t.Fatalf("the tail of the output was lost — a failing run's last words: %q", got[len(got)-60:])
	}
	if !strings.Contains(got, "output truncated") {
		t.Fatalf("the cut was not marked: %q", got)
	}
}

// TestTrimOutputDoesNotSplitARune pins the cut boundary: a multibyte
// line survives whole, in the head and in the tail.
func TestTrimOutputDoesNotSplitARune(t *testing.T) {
	long := strings.Repeat("中文", 1000) // 6000 bytes, no ASCII anywhere
	got := TrimOutput(long, 500)
	if !utf8.ValidString(got) {
		t.Fatal("the trimmed output is not valid UTF-8 — a rune was split")
	}
	if !strings.HasPrefix(long, got[:strings.Index(got, "\n…")]) {
		t.Fatal("the head does not match the source")
	}
}

// A compaction note is read once, mid-task, and the one thing it must
// say is that the earlier turns still exist. The counts are a bonus, so
// they are only present when the backend reported them.
func TestCompactedNoticeSaysTheTurnsAreNotLost(t *testing.T) {
	full := CompactedNotice("claude", 25876, 5253)
	if !strings.Contains(full, "claude") || !strings.Contains(full, "not lost") {
		t.Fatalf("note %q", full)
	}
	if !strings.Contains(full, "25.9k") || !strings.Contains(full, "5.3k") {
		t.Fatalf("note %q lost the counts", full)
	}

	bare := CompactedNotice("codex", 0, 0)
	if strings.Contains(bare, "→") || strings.Contains(bare, "0") {
		t.Fatalf("a note with no counts invented them: %q", bare)
	}
	if !strings.Contains(bare, "not lost") {
		t.Fatalf("note %q", bare)
	}
}

func TestCompactTokensStaysReadable(t *testing.T) {
	cases := map[int]string{
		0: "0", 512: "512", 999: "999",
		1000: "1.0k", 5253: "5.3k", 25876: "25.9k",
		134208: "134k", 2_500_000: "2.5M",
	}
	for in, want := range cases {
		if got := CompactTokens(in); got != want {
			t.Errorf("CompactTokens(%d) = %q, want %q", in, got, want)
		}
	}
}
