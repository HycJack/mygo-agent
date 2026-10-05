package cli

import (
	"strings"
	"testing"
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
	if !strings.HasSuffix(got, "… output truncated …") {
		t.Fatalf("a long output was not marked as truncated: %q", got)
	}
	if len(got) >= len(long) {
		t.Fatalf("a long output was not shortened: %d >= %d", len(got), len(long))
	}
}
