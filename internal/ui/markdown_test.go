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

// TestMarkdownNestedListNests proves a nested list item keeps its
// indent: trimming every line used to flatten the whole list into one
// level.
func TestMarkdownNestedListNests(t *testing.T) {
	cache := NewMdCache()
	src := "- outer item\n  - inner item\n    - deepest item\n"
	tt := ui.NewTester(func(c *ui.Context) {
		Markdown(c, cache, "m1", src, true, CodexPalette())
	}, 600, 400)
	for _, want := range []string{"outer item", "inner item", "deepest item"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %v", want, tt.Texts())
		}
	}
	// Each level steps in further, so the three items cannot share a
	// left edge — which is what a flat render gave them.
	top, ok := tt.Find("outer item")
	if !ok {
		t.Fatalf("outer item not rendered: %v", tt.Texts())
	}
	deep, ok := tt.Find("deepest item")
	if !ok {
		t.Fatalf("deepest item not rendered: %v", tt.Texts())
	}
	if deep.X <= top.X {
		t.Fatalf("nested item did not step in: outer x=%v deepest x=%v", top.X, deep.X)
	}
}

// TestMarkdownIndentedCodeBlock proves a four-space-indented run renders
// as code, not as prose, and that a nested list item's own indented text
// is still a list item.
func TestMarkdownIndentedCodeBlock(t *testing.T) {
	cache := NewMdCache()
	src := "intro\n\n    indented := 1\n    fmt.Println(indented)\n\nafter\n"
	tt := ui.NewTester(func(c *ui.Context) {
		Markdown(c, cache, "m1", src, true, CodexPalette())
	}, 600, 400)
	for _, want := range []string{"indented := 1", "fmt.Println(indented)", "after"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %v", want, tt.Texts())
		}
	}
	// The code run renders as a card, which is where the "Copy code"
	// button of every code block lives; a prose run has none.
	if !tt.HasText("Copy code") {
		t.Fatalf("indented code block rendered as prose: %v", tt.Texts())
	}
}

// TestMarkdownIndentedNestedListStaysList proves an item indented four
// columns inside a list is the item's own text, not a code block.
func TestMarkdownIndentedNestedListStaysList(t *testing.T) {
	cache := NewMdCache()
	tt := ui.NewTester(func(c *ui.Context) {
		Markdown(c, cache, "m1", "- outer\n  - inner\n    still the inner item\n", true, CodexPalette())
	}, 600, 400)
	if !tt.HasText("still the inner item") {
		t.Fatalf("missing nested text: %v", tt.Texts())
	}
	if tt.HasText("Copy code") {
		t.Fatalf("a list item's indented text became a code block: %v", tt.Texts())
	}
}

// TestNumberedItemNeedsSeparatorSpace proves "3.14 is pi" is prose: a
// digit run followed by a dot is only a list item when the dot opens
// the text.
func TestNumberedItemNeedsSeparatorSpace(t *testing.T) {
	for _, tc := range []struct {
		line, num, rest string
	}{
		{"3.14 is pi", "", ""},
		{"1. first item", "1", "first item"},
		{"12) second item", "12", "second item"},
		{"version 2.0 shipped", "", ""},
		{"42.", "", ""},
		{"7.\t tabbed", "7", "tabbed"},
	} {
		num, rest := numberedItem(tc.line)
		if num != tc.num || rest != tc.rest {
			t.Errorf("numberedItem(%q) = (%q, %q), want (%q, %q)", tc.line, num, rest, tc.num, tc.rest)
		}
	}
}

// TestStreamingFenceIsBounded proves an unterminated fence renders as
// the finished code card, in the same height-capped scrolling box, so
// a long streaming block cannot grow the reply without limit.
func TestStreamingFenceIsBounded(t *testing.T) {
	cache := NewMdCache()
	var open, closed *ui.Tester
	open = ui.NewTester(func(c *ui.Context) {
		Markdown(c, cache, "m1", "```go\n"+strings.Repeat("line of streaming code\n", 400), false, CodexPalette())
	}, 700, 900)
	closed = ui.NewTester(func(c *ui.Context) {
		Markdown(c, cache, "m2", "```go\n"+strings.Repeat("line of streaming code\n", 400)+"```\n", true, CodexPalette())
	}, 700, 900)

	if !open.HasText("go") {
		t.Fatalf("the streaming fence has no code-card header: %v", open.Texts()[:min(len(open.Texts()), 8)])
	}
	if !open.HasText("Copy code") {
		t.Fatal("the streaming fence is not rendered as a code card")
	}
	// Both the open and the closed fence are capped, so neither grows
	// with the length of its content: the card is a fraction of the
	// content's natural height.
	if got := codeCardHeight(t, open); got <= 0 || got >= 900 {
		t.Fatalf("streaming fence height %v is not capped under the 900pt window", got)
	}
	if openCard, closedCard := codeCardHeight(t, open), codeCardHeight(t, closed); openCard != closedCard {
		t.Fatalf("streaming fence height %v differs from the finished card %v", openCard, closedCard)
	}
}

// codeCardHeight is the height of the code card's scrolled code box.
func codeCardHeight(t *testing.T, tt *ui.Tester) float32 {
	t.Helper()
	r, ok := tt.Find("Copy code")
	if !ok {
		t.Fatal("no code card")
	}
	return r.H
}
