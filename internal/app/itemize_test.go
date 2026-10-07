package app

import (
	"fmt"
	"strings"
	"testing"

	"mygo-agent/internal/harness"
	uipkg "mygo-agent/internal/ui"
)

// A turn has to read the way it happened: prose, a tool call, more prose.
// Before the ordered sequence the Host kept text in one string and cards
// in another, so every turn rendered as all-cards-then-all-prose. These
// tests pin the order, and the runs that fold.

// turnApp returns an app holding one running assistant message, ready for
// the projector.
func turnApp(t *testing.T) (*app, *Thread) {
	t.Helper()
	a := newTestApp(t)
	th := &Thread{ID: "t1", ProjectID: "default"}
	th.Messages = []Message{{ID: "m0", Role: "assistant", Running: true}}
	a.threads = []*Thread{th}
	a.current = "t1"
	return a, th
}

// diffEvent is a file change, the way a backend reports an edit it made
// itself rather than a tool call preview.
func diffEvent(file, diff string) harness.Event {
	return harness.Event{Kind: harness.EventFileChange, File: file, Diff: diff}
}

func say(th *Thread, delta string) {
	th.Messages[0].Blocks[len(th.Messages[0].Blocks)-1].Text += delta
}

func types(m Message) string {
	var out []string
	for _, b := range m.Blocks {
		out = append(out, b.Type)
	}
	return strings.Join(out, ",")
}

// TestProseAndCardsInterleave is the regression: the order the agent
// produced must be the order it renders in.
func TestProseAndCardsInterleave(t *testing.T) {
	a, th := turnApp(t)
	at := 0
	ev := func(k, delta, text string) harness.Event {
		e := harness.Event{Kind: k, TextDelta: delta, Text: text}
		return e
	}
	// "let me look" → a command → "found it" → an edit → "done".
	a.applyEvent(th, at, "codex", ev(harness.EventText, "let me look", ""))
	a.applyEvent(th, at, "codex", cmdEvent("c1", "ls"))
	a.applyEvent(th, at, "codex", ev(harness.EventText, "found it", ""))
	a.applyEvent(th, at, "codex", diffEvent("main.go", "@@ -1 +1 @@\n-a\n+b"))
	a.applyEvent(th, at, "codex", ev(harness.EventText, "done", ""))

	if got := types(th.Messages[0]); got != "text,command,text,diff,text" {
		t.Fatalf("block order %q, want text,command,text,diff,text", got)
	}
	// Each stretch of prose is its own block, not one blob.
	m := &th.Messages[0]
	if m.Blocks[0].Text != "let me look" || m.Blocks[2].Text != "found it" || m.Blocks[4].Text != "done" {
		t.Fatalf("the prose stretches were not kept apart: %+v", m.Blocks)
	}
	// The aggregate stays a readable document: a card interrupting the
	// prose leaves a paragraph break, so Copy hands over something a human
	// would have written.
	if m.Text != "let me look\n\nfound it\n\ndone" {
		t.Fatalf("aggregate %q", m.Text)
	}
}

// TestAdjacentProseIsOneParagraph proves the other half: two text events
// with nothing between them are one paragraph. A gateway that emits an
// assistant message per content block would otherwise turn one sentence
// into two, and a chunked stream into a hundred.
func TestAdjacentProseIsOneParagraph(t *testing.T) {
	a, th := turnApp(t)
	for _, d := range []string{"hello ", "wor", "ld"} {
		a.applyEvent(th, 0, "claude", harness.Event{Kind: harness.EventText, TextDelta: d})
	}
	if got := types(th.Messages[0]); got != "text" {
		t.Fatalf("block order %q, want a single text block", got)
	}
	if th.Messages[0].Text != "hello world" {
		t.Fatalf("aggregate %q, want the three deltas joined with nothing between", th.Messages[0].Text)
	}
}

// TestInterleavingSurvivesARoundTrip proves the order is persisted, not
// only held in memory: a turn reloaded from its thread file still reads
// in the order it happened.
func TestInterleavingSurvivesARoundTrip(t *testing.T) {
	a, th := turnApp(t)
	a.threadsDir = threadsLayout{root: t.TempDir()}
	a.applyEvent(th, 0, "codex", harness.Event{Kind: harness.EventText, TextDelta: "before"})
	a.applyEvent(th, 0, "codex", cmdEvent("c1", "ls"))
	a.applyEvent(th, 0, "codex", harness.Event{Kind: harness.EventText, TextDelta: "after"})
	a.saveThread(th)

	b := newTestApp(t)
	b.threadsDir = a.threadsDir
	b.loadThreads()
	if len(b.threads) != 1 {
		t.Fatalf("loaded %d threads, want 1", len(b.threads))
	}
	if got := types(b.threads[0].Messages[0]); got != "text,command,text" {
		t.Fatalf("reloaded order %q", got)
	}
}

// TestItemizeGroupsARunOfCommands is the folding half: a twenty-command
// turn is one row, not twenty.
func TestItemizeGroupsARunOfCommands(t *testing.T) {
	m := &Message{Blocks: []Block{
		{Type: blockCommand, Text: "ls", Ms: 10, Exit: 0},
		{Type: blockCommand, Text: "go test ./...", Ms: 900, Exit: 0},
		{Type: blockCommand, Text: "git diff", Ms: 30, Exit: 1},
	}}
	items := itemize(m)
	if len(items) != 1 || items[0].Kind != uipkg.ItemGroup {
		t.Fatalf("items %+v, want one group", items)
	}
	if len(items[0].Blocks) != 3 {
		t.Fatalf("group holds %d blocks, want 3", len(items[0].Blocks))
	}
	if items[0].Ms != 940 {
		t.Fatalf("group duration %d, want the sum 940", items[0].Ms)
	}
	if items[0].Failed != 1 {
		t.Fatalf("group failures %d, want 1", items[0].Failed)
	}
	if items[0].At != 0 {
		t.Fatalf("group address %d, want 0", items[0].At)
	}
}

// TestItemizeKeepsASingleCardPlain: one command then an answer is the
// common shape and must look the way it always has.
func TestItemizeKeepsASingleCardPlain(t *testing.T) {
	m := &Message{Blocks: []Block{
		{Type: blockCommand, Text: "ls", Exit: 0},
		{Type: blockText, Text: "three files"},
	}}
	items := itemize(m)
	if len(items) != 2 {
		t.Fatalf("items %+v, want two", items)
	}
	if items[0].Kind != uipkg.ItemBlock || items[1].Kind != uipkg.ItemText {
		t.Fatalf("items %+v, want a plain card then text", items)
	}
}

// TestItemizeFoldsAKindBehindOneHeader: two runs of commands with a
// sentence between them are ONE group — the work-log interleaving is
// exactly what buried the prose under collapsed rows before. The prose
// keeps its place; the kind stands behind one header.
func TestItemizeFoldsAKindBehindOneHeader(t *testing.T) {
	m := &Message{Blocks: []Block{
		{Type: blockCommand, Text: "ls"},
		{Type: blockCommand, Text: "cat a"},
		{Type: blockText, Text: "now the tests"},
		{Type: blockCommand, Text: "go test"},
		{Type: blockCommand, Text: "go vet"},
	}}
	items := itemize(m)
	if len(items) != 2 {
		t.Fatalf("items %+v, want group, text", items)
	}
	if items[0].Kind != uipkg.ItemGroup || items[1].Kind != uipkg.ItemText {
		t.Fatalf("items %+v, want group, text", items)
	}
	if len(items[0].Blocks) != 4 || items[0].At != 0 {
		t.Fatalf("the group holds %d members at %d, want all four at 0", len(items[0].Blocks), items[0].At)
	}
}

// TestItemizeNeverGroupsErrorsOrApprovals: an error says something
// different every time and an approval binds exactly one call, so neither
// may be folded into a run.
func TestItemizeNeverGroupsErrorsOrApprovals(t *testing.T) {
	m := &Message{Blocks: []Block{
		{Type: blockError, Text: "e1"},
		{Type: blockError, Text: "e2"},
		{Type: blockApproval, Text: "a1", ApprovalID: "1"},
		{Type: blockApproval, Text: "a2", ApprovalID: "2"},
	}}
	items := itemize(m)
	if len(items) != 4 {
		t.Fatalf("items %+v, want every error and approval on its own", items)
	}
	for i, it := range items {
		if it.Kind != uipkg.ItemBlock {
			t.Fatalf("item %d is a %s, want a plain block", i, it.Kind)
		}
	}
}

// TestItemizeGroupsDiffsOnlyPerFile: two edits to one file are one story;
// edits to two files are two facts.
func TestItemizeGroupsDiffsOnlyPerFile(t *testing.T) {
	m := &Message{Blocks: []Block{
		{Type: blockDiff, File: "a.go", Add: 1, Del: 0},
		{Type: blockDiff, File: "a.go", Add: 2, Del: 1},
		{Type: blockDiff, File: "b.go", Add: 3, Del: 0},
	}}
	items := itemize(m)
	if len(items) != 2 {
		t.Fatalf("items %+v, want one group for a.go and one for b.go", items)
	}
	if len(items[0].Blocks) != 2 || items[0].At != 0 {
		t.Fatalf("first group %+v", items[0])
	}
	if items[1].At != 2 {
		t.Fatalf("the b.go card is addressed at %d, want 2", items[1].At)
	}
}

// TestItemizeFoldsAnOldThreadForward: history written before the ordered
// sequence exists has its prose only in Message.Text. It must still
// render, not go blank.
func TestItemizeFoldsAnOldThreadForward(t *testing.T) {
	m := &Message{Text: "an answer from before", Blocks: nil}
	items := itemize(m)
	if len(items) != 1 || items[0].Kind != uipkg.ItemText {
		t.Fatalf("items %+v, want one synthesized text item", items)
	}
	if items[0].Text != "an answer from before" {
		t.Fatalf("item text %q", items[0].Text)
	}
	// A message with neither prose nor cards has nothing to synthesize.
	if got := itemize(&Message{}); len(got) != 0 {
		t.Fatalf("an empty message produced %+v", got)
	}
}

// TestToggleBlockOpensAWholeRun: a group has no state of its own — the
// host flips every member from the group's address, so a group survives a
// reload with the thread.
func TestToggleBlockOpensAWholeRun(t *testing.T) {
	a, th := turnApp(t)
	a.threadsDir = threadsLayout{root: t.TempDir()}
	for i, cmd := range []string{"ls", "cat a", "go test"} {
		a.applyEvent(th, 0, "codex", cmdEvent(fmt.Sprintf("c%d", i), cmd))
	}
	h := transcriptActions{a: a, th: th}
	h.ToggleBlock(th.Messages[0].ID, 0)
	for i, b := range th.Messages[0].Blocks {
		if !b.Open {
			t.Fatalf("block %d stayed closed after the group was opened", i)
		}
	}
	h.ToggleBlock(th.Messages[0].ID, 0)
	for i, b := range th.Messages[0].Blocks {
		if b.Open {
			t.Fatalf("block %d stayed open after the group was closed", i)
		}
	}
	// The state has to persist, since a group owns no state of its own.
	a.saveThread(th)
	reloaded := newTestApp(t)
	reloaded.threadsDir = a.threadsDir
	reloaded.loadThreads()
	if len(reloaded.threads) != 1 || reloaded.threads[0].Messages[0].Blocks[0].Open {
		t.Fatalf("the folded state did not survive: %+v", reloaded.threads)
	}
}

// TestToggleBlockSpansTheKind: a kind folds across the prose, so one
// click opens every command of the message — the folded group a click
// comes from stands for all of them, wherever they sit.
func TestToggleBlockSpansTheKind(t *testing.T) {
	a, th := turnApp(t)
	a.applyEvent(th, 0, "codex", cmdEvent("a", "ls"))
	a.applyEvent(th, 0, "codex", cmdEvent("b", "cat a"))
	a.applyEvent(th, 0, "codex", harness.Event{Kind: harness.EventText, TextDelta: "now the tests"})
	a.applyEvent(th, 0, "codex", cmdEvent("c", "go test"))

	h := transcriptActions{a: a, th: th}
	h.ToggleBlock(th.Messages[0].ID, 0)
	if !th.Messages[0].Blocks[0].Open || !th.Messages[0].Blocks[1].Open || !th.Messages[0].Blocks[3].Open {
		t.Fatalf("the kind did not open as one: %+v", th.Messages[0].Blocks)
	}
	// And the kind closes as one.
	h.ToggleBlock(th.Messages[0].ID, 3)
	for i, b := range th.Messages[0].Blocks {
		if b.Type == blockCommand && b.Open {
			t.Fatalf("command %d stayed open", i)
		}
	}
}

// TestToggleBlockStopsAtADifferentFile: the other boundary. Two edits to
// one file fold; the first edit to another file does not ride along.
//
// The two a.go cards arrive already expanded, so the first toggle CLOSES
// the run. b.go starts expanded too, which is the tell: if the scan had
// crossed the file boundary it would have closed b.go as well.
func TestToggleBlockStopsAtADifferentFile(t *testing.T) {
	a, th := turnApp(t)
	a.applyEvent(th, 0, "codex", diffEvent("a.go", "@@ -1 +1 @@\n-a\n+b"))
	a.applyEvent(th, 0, "codex", diffEvent("a.go", "@@ -5 +5 @@\n-c\n+d"))
	a.applyEvent(th, 0, "codex", diffEvent("b.go", "@@ -1 +1 @@\n-x\n+y"))

	h := transcriptActions{a: a, th: th}
	h.ToggleBlock(th.Messages[0].ID, 0)
	if th.Messages[0].Blocks[0].Open || th.Messages[0].Blocks[1].Open {
		t.Fatalf("the a.go run did not close: %+v", th.Messages[0].Blocks)
	}
	if !th.Messages[0].Blocks[2].Open {
		t.Fatal("the toggle crossed to b.go, so the two files are really one run")
	}
	// And it reopens as a unit, leaving b.go alone again.
	h.ToggleBlock(th.Messages[0].ID, 0)
	if !th.Messages[0].Blocks[0].Open || !th.Messages[0].Blocks[1].Open || !th.Messages[0].Blocks[2].Open {
		t.Fatalf("the run did not reopen as a unit: %+v", th.Messages[0].Blocks)
	}
}

// TestToggleBlockOnASingleCardIsUnchanged: an ungroupable card still
// toggles on its own.
func TestToggleBlockOnASingleCardIsUnchanged(t *testing.T) {
	a, th := turnApp(t)
	a.applyEvent(th, 0, "codex", harness.Event{Kind: harness.EventError, Err: "boom"})
	a.applyEvent(th, 0, "codex", harness.Event{Kind: harness.EventError, Err: "bang"})
	h := transcriptActions{a: a, th: th}
	h.ToggleBlock(th.Messages[0].ID, 0)
	if !th.Messages[0].Blocks[0].Open || th.Messages[0].Blocks[1].Open {
		t.Fatalf("errors %+v, want only the first one open", th.Messages[0].Blocks)
	}
	// Out of range is a no-op, not a panic: the view can address a card
	// from a frame whose snapshot has moved on.
	h.ToggleBlock(th.Messages[0].ID, 99)
	h.ToggleBlock("nope", 0)
}

// TestViewStampMovesWithTheOrderedSequence: the transcript cache reuses
// a snapshot when the stamp is unchanged, so a streaming reply that lands
// in a text block has to invalidate it.
func TestViewStampMovesWithTheOrderedSequence(t *testing.T) {
	th := &Thread{Messages: []Message{{
		Blocks: []Block{{Type: blockText, Text: "par"}},
	}}}
	before := th.viewStamp()
	say(th, "tial")
	if th.viewStamp() == before {
		t.Fatal("appending to a text block left the stamp unchanged, so the transcript cache would show a stale reply")
	}
	// A new stretch of prose must also move it.
	th2 := &Thread{Messages: []Message{{Blocks: []Block{
		{Type: blockText, Text: "a"},
		{Type: blockCommand},
		{Type: blockText, Text: "b"},
	}}}}
	before2 := th2.viewStamp()
	th2.Messages[0].Blocks[2].Text = "bb"
	if th2.viewStamp() == before2 {
		t.Fatal("extending the second prose stretch left the stamp unchanged")
	}
}

// TestTranscriptVMBuildsItems proves the bridge hands the view the ordered
// sequence, and that the flattened cards still address the host's blocks.
func TestTranscriptVMBuildsItems(t *testing.T) {
	a, th := turnApp(t)
	a.applyEvent(th, 0, "codex", harness.Event{Kind: harness.EventText, TextDelta: "one"})
	a.applyEvent(th, 0, "codex", cmdEvent("c1", "ls"))
	a.applyEvent(th, 0, "codex", cmdEvent("c2", "pwd"))
	a.applyEvent(th, 0, "codex", harness.Event{Kind: harness.EventText, TextDelta: "two"})

	vm := a.transcriptVM(th)
	m := vm.Messages[0]
	if len(m.Items) != 3 {
		t.Fatalf("items %+v, want text, group, text", m.Items)
	}
	if m.Items[0].Kind != uipkg.ItemText || m.Items[1].Kind != uipkg.ItemGroup || m.Items[2].Kind != uipkg.ItemText {
		t.Fatalf("items %+v", m.Items)
	}
	if len(m.Blocks) != 2 {
		t.Fatalf("flattened cards %d, want the two commands", len(m.Blocks))
	}
	// A change must produce a new snapshot rather than a reused one.
	a.update(func() { th.Messages[0].Blocks[0].Text += "!" })
	if got := a.transcriptVM(th).Messages[0].Items[0].Text; got != "one!" {
		t.Fatalf("the reused snapshot is stale: %q", got)
	}
}

// TestItemizeFoldsAKindAcrossTheProse: a member's work-log interleaves
// prose with commands and thinking; each kind folds into ONE group at
// its first member, and the prose keeps its place.
func TestItemizeFoldsAKindAcrossTheProse(t *testing.T) {
	m := &Message{Blocks: []Block{
		{Type: blockNote, Text: "→ 开发"},
		{Type: blockText, Text: "first"},
		{Type: blockCommand, Text: "$ a"},
		{Type: blockReasoning, Text: "think"},
		{Type: blockText, Text: "second"},
		{Type: blockCommand, Text: "$ b"},
		{Type: blockCommand, Text: "$ c"},
		{Type: blockReasoning, Text: "think more"},
	}}
	items := itemize(m)
	var kinds []string
	for _, it := range items {
		kinds = append(kinds, fmt.Sprintf("%s/%s@%d(n=%d)", it.Kind, it.Type, it.At, len(it.Blocks)))
	}
	// note@0 inline, text@1, commands group@2 (3 members), reasoning
	// group@3 (2 members), text@4.
	want := []string{"block/note@0(n=1)", "text/@1(n=0)", "group/command@2(n=3)", "group/reasoning@3(n=2)", "text/@4(n=0)"}
	if strings.Join(kinds, "|") != strings.Join(want, "|") {
		t.Fatalf("items = %v, want %v", kinds, want)
	}
	// The command group's members carry their true indices.
	for _, it := range items {
		if it.Kind == uipkg.ItemGroup && it.Type == blockCommand {
			if len(it.Ats) != 3 || it.Ats[0] != 2 || it.Ats[2] != 6 {
				t.Fatalf("command group ats = %v", it.Ats)
			}
		}
	}
}
