package ui

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// A folded run is a claim the view makes about what happened: how many,
// how long, whether anything failed. A claim that is off by a word is
// worse than no claim, so these pin the header text itself.

// stubActs records what the view asked for, so a click can be asserted.
type stubActs struct {
	toggled []int
}

func (s *stubActs) ToggleBlock(_ string, bi int) { s.toggled = append(s.toggled, bi) }
func (s *stubActs) Resend(string, string)        {}
func (s *stubActs) Regenerate()                  {}
func (s *stubActs) ResolveApproval(string, bool) {}

// renderItems draws the ordered pieces of one turn and returns the tester,
// so a test can read what the view actually put on screen. open decides
// whether a group arrives already expanded, the way a host that already
// has the members' Open set would hand it over.
func renderItems(t *testing.T, acts TranscriptActions, items []ItemVM, open bool) *ui.Tester {
	t.Helper()
	vm := &TranscriptVM{
		Messages: []MessageVM{{ID: "m1", Role: "assistant", Items: items}},
		Md:       NewMdCache(),
		Pal:      CodexPalette(),
	}
	return ui.NewTester(func(c *ui.Context) {
		for i := range items {
			it := &items[i]
			switch it.Kind {
			case ItemGroup:
				it.Open = open
				itemGroup(c, vm, acts, &vm.Messages[0], it)
			case ItemBlock:
				block(c, vm, acts, &vm.Messages[0], it.Blocks[0], it.At)
			}
		}
	}, 900, 500)
}

// TestCommandGroupHeaderCountsAndTotals: the header is the whole reason a
// run folds, so it has to carry the count, the failures and the summed
// duration — and the count must never lose its number.
func TestCommandGroupHeaderCountsAndTotals(t *testing.T) {
	items := []ItemVM{{
		Kind: ItemGroup, Type: "command", Ms: 3940, Failed: 1,
		Blocks: []BlockVM{
			{Type: "command", Text: "$ ls", Exit: 0},
			{Type: "command", Text: "$ go test", Exit: 1},
			{Type: "command", Text: "$ git diff", Exit: 0},
		},
	}}
	tt := renderItems(t, &stubActs{}, items, false)
	for _, want := range []string{"3 commands", "1 failed", "3.9s"} {
		if !tt.HasText(want) {
			t.Fatalf("header is missing %q: %v", want, tt.Texts())
		}
	}
	// A single failure must keep its "1". plural() drops the number when
	// one and many are spelled the same, so this reads "· failed" — true
	// of the shape, wrong as a claim about the turn.
	if tt.HasText("failed") && !tt.HasText("1 failed") {
		t.Fatalf("the failure count lost its number: %v", tt.Texts())
	}
}

// TestCommandGroupHeaderWithNoFailuresSaysNothing: a clean run must not
// carry an empty failure clause.
func TestCommandGroupHeaderWithNoFailuresSaysNothing(t *testing.T) {
	items := []ItemVM{{
		Kind: ItemGroup, Type: "command", Ms: 250,
		Blocks: []BlockVM{
			{Type: "command", Text: "$ ls", Exit: 0},
			{Type: "command", Text: "$ pwd", Exit: 0},
		},
	}}
	tt := renderItems(t, &stubActs{}, items, false)
	if !tt.HasText("2 commands") {
		t.Fatalf("missing the count: %v", tt.Texts())
	}
	if tt.HasText("failed") {
		t.Fatalf("a clean run reported a failure: %v", tt.Texts())
	}
}

// TestDiffGroupHeaderNamesTheFileAndTallies: a diff run folds only while
// the file matches, so the header can name it — and the add/del it shows
// must be the run's total, not the first card's.
func TestDiffGroupHeaderNamesTheFileAndTallies(t *testing.T) {
	items := []ItemVM{{
		Kind: ItemGroup, Type: "diff",
		Blocks: []BlockVM{
			{Type: "diff", File: "internal/app/itemize.go", Add: 3, Del: 1},
			{Type: "diff", File: "internal/app/itemize.go", Add: 2, Del: 5},
		},
	}}
	tt := renderItems(t, &stubActs{}, items, false)
	for _, want := range []string{"itemize.go", "+5", "−6"} {
		if !tt.HasText(want) {
			t.Fatalf("header is missing %q: %v", want, tt.Texts())
		}
	}
}

// TestGroupHeaderForAnEmptyRunDoesNotPanic: the host only ever builds a
// group with two or more members, but a header that indexes its first
// member before checking is one refactor away from a crash, and this
// package has no recover anywhere.
func TestGroupHeaderForAnEmptyRunDoesNotPanic(t *testing.T) {
	items := []ItemVM{{Kind: ItemGroup, Type: "diff"}}
	tt := renderItems(t, &stubActs{}, items, false)
	if tt == nil {
		t.Fatal("the header did not render")
	}
}

// TestFoldedGroupHidesItsCards: folding has to actually hide the cards,
// or the header is a label over the same wall of rows it replaced.
func TestFoldedGroupHidesItsCards(t *testing.T) {
	items := []ItemVM{{
		Kind: ItemGroup, Type: "command", At: 0,
		Blocks: []BlockVM{
			{Type: "command", Text: "$ rm -rf /", Exit: 0, Output: "gone"},
			{Type: "command", Text: "$ echo hi", Exit: 0, Output: "hi"},
		},
	}}
	folded := renderItems(t, &stubActs{}, items, false)
	if folded.HasText("rm -rf /") || folded.HasText("echo hi") {
		t.Fatalf("a folded run still shows its cards: %v", folded.Texts())
	}
	open := renderItems(t, &stubActs{}, items, true)
	if !open.HasText("rm -rf /") || !open.HasText("echo hi") {
		t.Fatalf("an expanded run hides its cards: %v", open.Texts())
	}
}

// TestGroupToggleIsAddressedByItsFirstMember: the group owns no state, so
// the only thing the view can send is the position of the run's first
// card. Addressing it by an index into the folded Blocks would make the
// host open a different set of cards than the one the user clicked.
func TestGroupToggleIsAddressedByItsFirstMember(t *testing.T) {
	acts := &stubActs{}
	items := []ItemVM{{
		Kind: ItemGroup, Type: "command", At: 4,
		Blocks: []BlockVM{
			{Type: "command", Text: "$ a"},
			{Type: "command", Text: "$ b"},
		},
	}}
	tt := renderItems(t, acts, items, false)
	if err := tt.Click("2 commands"); err != nil {
		t.Fatalf("clicking the group header: %v", err)
	}
	if len(acts.toggled) != 1 || acts.toggled[0] != 4 {
		t.Fatalf("the header asked for %v, want exactly one toggle at the first member's position 4", acts.toggled)
	}
}

// TestReasoningAndNoteHeadersReadDifferently: reasoning is the agent
// thinking and note is the harness reporting. They fold on the same rule
// but must not be labelled the same way, or the user cannot tell whose
// voice they are reading.
func TestReasoningAndNoteHeadersReadDifferently(t *testing.T) {
	think := renderItems(t, &stubActs{}, []ItemVM{{
		Kind: ItemGroup, Type: "reasoning",
		Blocks: []BlockVM{{Type: "reasoning", Text: "a"}, {Type: "reasoning", Text: "b"}},
	}}, false)
	note := renderItems(t, &stubActs{}, []ItemVM{{
		Kind: ItemGroup, Type: "note",
		Blocks: []BlockVM{{Type: "note", Text: "a"}, {Type: "note", Text: "b"}},
	}}, false)
	if !think.HasText("Thinking") {
		t.Fatalf("a reasoning run lost its label: %v", think.Texts())
	}
	if !note.HasText("2 notes") {
		t.Fatalf("a note run lost its count: %v", note.Texts())
	}
	if think.HasText("2 notes") || note.HasText("Thinking") {
		t.Fatal("reasoning and note render the same header, so the two voices are indistinguishable")
	}
}

// TestTwoProseStretchesGetTheirOwnCacheEntry: the markdown cache is
// incremental — each entry is fed only the text appended since the last
// frame, so a long streaming reply costs O(delta) instead of re-parsing
// the whole document every frame. That only holds if each stretch of
// prose has its OWN entry.
//
// The cache does not break visibly when two share one: forMsg notices the
// new source is not a prefix of the old and re-parses from scratch. So
// the cost is invisible and the reply goes quadratic exactly as the
// incremental design exists to prevent. What is asserted here is the
// entry count, which is the thing that actually has to hold.
func TestTwoProseStretchesGetTheirOwnCacheEntry(t *testing.T) {
	items := []ItemVM{
		{Kind: ItemText, At: 0, Text: "**alpha** paragraph"},
		{Kind: ItemGroup, Type: "command", At: 1,
			Blocks: []BlockVM{{Type: "command", Text: "$ ls"}, {Type: "command", Text: "$ pwd"}}},
		{Kind: ItemText, At: 3, Text: "**beta** paragraph"},
	}
	vm := &TranscriptVM{
		Messages: []MessageVM{{ID: "m1", Role: "assistant", Items: items}},
		Md:       NewMdCache(),
		Pal:      CodexPalette(),
	}
	tt := ui.NewTester(func(c *ui.Context) {
		m := &vm.Messages[0]
		for i := range m.Items {
			item(c, vm, &stubActs{}, m, &m.Items[i])
		}
	}, 900, 500)

	// Both paragraphs are on screen...
	for _, want := range []string{"alpha", "beta"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q: %v", want, tt.Texts())
		}
	}
	// ...and they parsed independently, which is what keeps the parse
	// incremental. One entry means the second one re-parsed from scratch
	// on every frame, for the whole life of the message.
	if got := len(vm.Md.states); got != 2 {
		t.Fatalf("the message's two prose stretches share %d cache entries, want 2", got)
	}
}

// TestProseCacheEntryIsExtendedNotRebuilt pins the property the cache
// exists for: a second frame over a grown reply appends to the entry
// rather than parsing the document again.
func TestProseCacheEntryIsExtendedNotRebuilt(t *testing.T) {
	vm := &TranscriptVM{
		Messages: []MessageVM{{ID: "m1", Role: "assistant", Items: []ItemVM{
			{Kind: ItemText, At: 0, Text: "first "},
		}}},
		Md:  NewMdCache(),
		Pal: CodexPalette(),
	}
	draw := func() {
		ui.NewTester(func(c *ui.Context) {
			item(c, vm, &stubActs{}, &vm.Messages[0], &vm.Messages[0].Items[0])
		}, 900, 500)
	}
	draw()
	// The streaming case: the reply grew by one word.
	vm.Messages[0].Items[0].Text += "second"
	draw()

	st := vm.Md.states["msg:m1:0"]
	if st == nil {
		t.Fatalf("no cache entry for the prose block: %v", keysOf(vm.Md.states))
	}
	if st.text != "first second" {
		t.Fatalf("the entry holds %q, want the whole reply", st.text)
	}
	// One entry, holding the whole thing: extended, not replaced.
	if got := len(vm.Md.states); got != 1 {
		t.Fatalf("a growing reply produced %d cache entries, want 1", got)
	}
}

func keysOf(m map[string]*mdState) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestPluralCountsAboveOne: the helper behind every header count.
func TestPluralCountsAboveOne(t *testing.T) {
	cases := map[string]string{
		plural(1, "command", "commands"): "command",
		plural(2, "command", "commands"): "2 commands",
		plural(0, "command", "commands"): "0 commands",
	}
	for got, want := range cases {
		if got != want {
			t.Fatalf("plural gave %q, want %q", got, want)
		}
	}
	if strings.TrimSpace(plural(3, "note", "notes")) != "3 notes" {
		t.Fatalf("plural(3) = %q", plural(3, "note", "notes"))
	}
}

// TestReasoningCardIsCollapsedUntilClicked pins the thinking card's
// contract: the thought is context for the reply, not the reply, so a
// card arrives folded — the first line on screen, the body not — and the
// click asks the host to toggle that block's position, the way a
// command's output does.
func TestReasoningCardIsCollapsedUntilClicked(t *testing.T) {
	item := ItemVM{Kind: ItemBlock, Type: "reasoning", At: 7,
		Blocks: []BlockVM{{Type: "reasoning", Text: "checking the parser tests first\nthen the fixtures"}}}
	acts := &stubActs{}
	tt := renderItems(t, acts, []ItemVM{item}, false)

	if tt.HasText("then the fixtures") {
		t.Fatalf("the thought's body is on screen while folded: %v", tt.Texts())
	}
	if !tt.HasText("checking the parser tests first") {
		t.Fatalf("the folded card lost its preview line: %v", tt.Texts())
	}
	if err := tt.Click("checking the parser tests first"); err != nil {
		t.Fatal(err)
	}
	if len(acts.toggled) != 1 || acts.toggled[0] != 7 {
		t.Fatalf("the click toggled %v, want the block's own position [7]", acts.toggled)
	}

	// Expanded, the body is the point: the full text is on screen.
	item.Blocks[0].Open = true
	tt = renderItems(t, acts, []ItemVM{item}, false)
	if !tt.HasText("then the fixtures") {
		t.Fatalf("an expanded thinking card lost its body: %v", tt.Texts())
	}
}

// TestReasoningRunExpandsIntoFoldedMembers pins the two-layer fold: a
// reasoning run's header opens the run, and the members it reveals are
// themselves folded previews — the run's open state is the members', not
// a license for pages of thought to pour onto the screen.
func TestReasoningRunExpandsIntoFoldedMembers(t *testing.T) {
	items := []ItemVM{{
		Kind: ItemGroup, Type: "reasoning",
		Blocks: []BlockVM{
			{Type: "reasoning", Text: "first thought\nwith detail"},
			{Type: "reasoning", Text: "second thought\nwith detail"},
		},
	}}
	acts := &stubActs{}
	tt := renderItems(t, acts, items, true)

	if tt.HasText("with detail") {
		t.Fatalf("an expanded run poured its members' bodies out: %v", tt.Texts())
	}
	for _, want := range []string{"first thought", "second thought"} {
		if !tt.HasText(want) {
			t.Fatalf("an expanded run lost a member preview %q: %v", want, tt.Texts())
		}
	}
}
