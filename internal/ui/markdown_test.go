package ui

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestMdStateIncrementalFeed proves the parser's streaming contract:
// feeding the same document in two appends yields the same parts as
// parsing it whole.
func TestMdStateIncrementalFeed(t *testing.T) {
	src := "| Field | Type |\n|-------|------|\n| attempts | `atomic.Int32` |\n| deadline | time.Time |\n\n> quoted note\n\n---\n\n1. first item\n2. second item\n"

	whole := &mdState{}
	whole.feed("", src)
	whole.line(whole.pending + "\n")

	// Feed in two chunks split at a paragraph boundary, like a stream
	// would arrive.
	split := strings.Index(src, "\n\n") + 2
	incr := &mdState{}
	incr.feed("", src[:split])
	incr.line(incr.pending + "\n")
	incr.feed(src[:split], src)
	incr.line(incr.pending + "\n")

	if len(whole.parts) == 0 {
		t.Fatal("no parts parsed")
	}
	if len(whole.parts) != len(incr.parts) {
		t.Fatalf("incremental parse diverged: %d parts vs %d", len(incr.parts), len(whole.parts))
	}
	for i := range whole.parts {
		if whole.parts[i].text != incr.parts[i].text || whole.parts[i].code != incr.parts[i].code {
			t.Fatalf("part %d diverged: %+v vs %+v", i, incr.parts[i], whole.parts[i])
		}
	}
}

// TestMdStateFences proves code fences open and close across feeds and
// keep their language tag.
func TestMdStateFences(t *testing.T) {
	st := &mdState{}
	st.feed("", "```go\nx := 1\n```\nplain\n")
	st.line(st.pending + "\n")
	if len(st.parts) != 2 {
		t.Fatalf("parts: %+v", st.parts)
	}
	if !st.parts[0].code || st.parts[0].lang != "go" || !strings.Contains(st.parts[0].text, "x := 1") {
		t.Fatalf("code part: %+v", st.parts[0])
	}
	if st.parts[1].code || st.parts[1].text != "plain" {
		t.Fatalf("prose part: %+v", st.parts[1])
	}
}

// TestMarkdownRendersWithoutRawMarkers proves the rendered output shows
// content, not markdown syntax. Runs against the real renderer.
func TestMarkdownRendersWithoutRawMarkers(t *testing.T) {
	cache := NewMdCache()
	tt := ui.NewTester(func(c *ui.Context) {
		Markdown(c, cache, "m1",
			"| Field | Type |\n|-------|------|\n| attempts | `atomic.Int32` |\n\n**bold** and `code`\n\n```go\nx := 1\n```\n",
			true, CodexPalette())
	}, 600, 400)
	for _, want := range []string{"Field", "attempts", "atomic.Int32", "bold", "code", "x := 1"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %v", want, tt.Texts())
		}
	}
	for _, gone := range []string{"| Field |", "**", "```"} {
		if tt.HasText(gone) {
			t.Fatalf("raw markdown leaked: %q in %v", gone, tt.Texts())
		}
	}
}
