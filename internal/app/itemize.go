package app

import uipkg "mygo-agent/internal/ui"

// The transcript renders a turn in arrival order (spec/architecture.md):
// prose, a tool call, more prose, another tool call. The projector puts
// everything in one ordered block sequence for that; this file turns that
// sequence into the items the view draws, folding runs of the same kind of
// card into one group so a twenty-command turn is one row, not twenty.
//
// Grouping is PRESENTATION ONLY. The persisted sequence is untouched, so
// the transcript a card was read from is still the one on disk, and a
// group's open state is just the open state of its members.

// groupableBlocks are the kinds that fold into a run. Two kinds are
// deliberately absent:
//
//   - error: each one says something different and every one is
//     actionable, so collapsing them hides the reason a turn failed.
//   - approval: a decision binds exactly one prepared call
//     (spec/approvals.md), so a group would have no honest header.
func groupable(kind string) bool {
	switch kind {
	case blockCommand, blockDiff, blockReasoning, blockNote:
		return true
	}
	return false
}

// itemize turns one message's blocks into ordered render items. Text is
// always its own item; a run of two or more groupable blocks of the same
// kind becomes one group, and a run of one stays a plain card so the
// common "one command, then an answer" turn looks the way it always has.
//
// Diffs group only while they touch the SAME file: two edits to one file
// are one story, and one card with a combined count is more useful than
// two cards; edits to different files are separate facts.
func itemize(m *Message) []uipkg.ItemVM {
	var items []uipkg.ItemVM
	i := 0
	for i < len(m.Blocks) {
		b := &m.Blocks[i]
		if b.Type == blockText {
			// At is the block's position, which the view uses both to
			// address a card and to key this stretch of prose in the
			// markdown cache. Left at zero, every paragraph in a message
			// would share one cache entry and the second would render the
			// first's markdown.
			items = append(items, uipkg.ItemVM{Kind: uipkg.ItemText, Text: b.Text, At: i})
			i++
			continue
		}
		if !groupable(b.Type) {
			items = append(items, blockItem(i, b))
			i++
			continue
		}
		// Walk the run: same kind, and for a diff the same file.
		end := i + 1
		for end < len(m.Blocks) {
			n := &m.Blocks[end]
			if n.Type != b.Type {
				break
			}
			if b.Type == blockDiff && n.File != b.File {
				break
			}
			end++
		}
		if end-i == 1 {
			items = append(items, blockItem(i, b))
		} else {
			items = append(items, groupItem(m.Blocks[i:end], i))
		}
		i = end
	}
	// A thread written before the ordered sequence existed has its prose
	// only in Message.Text. Synthesize one trailing text item so history
	// still renders instead of going blank.
	if len(items) == 0 && m.Text != "" {
		items = append(items, uipkg.ItemVM{Kind: uipkg.ItemText, Text: m.Text})
	}
	return items
}

// blockItem is a single card, addressed by its index in the message's
// blocks so ToggleBlock can reach it.
func blockItem(bi int, b *Block) uipkg.ItemVM {
	return uipkg.ItemVM{
		Kind:   uipkg.ItemBlock,
		Type:   b.Type,
		Blocks: []uipkg.BlockVM{blockVM(*b)},
		Open:   b.Open,
		At:     bi,
	}
}

// groupItem is a folded run. At is the index of its first member, which is
// also the index ToggleBlock flips: the host opens or closes the whole run
// from that one position, so a group needs no state of its own and
// survives a reload with the thread.
func groupItem(run []Block, at int) uipkg.ItemVM {
	it := uipkg.ItemVM{
		Kind:   uipkg.ItemGroup,
		Type:   run[0].Type,
		Open:   run[0].Open,
		At:     at,
		Blocks: make([]uipkg.BlockVM, 0, len(run)),
	}
	for _, b := range run {
		it.Blocks = append(it.Blocks, blockVM(b))
		if b.Ms > 0 {
			it.Ms += b.Ms
		}
		if b.Type == blockCommand && b.Exit != 0 && !b.Running {
			it.Failed++
		}
	}
	return it
}

// blockVM copies one block into the view's shape.
func blockVM(b Block) uipkg.BlockVM {
	return uipkg.BlockVM{
		Type: b.Type, Text: b.Text, File: b.File, Output: b.Output,
		Add: b.Add, Del: b.Del, Lines: b.Lines,
		Exit: b.Exit, Ms: b.Ms, Open: b.Open, Edit: b.Edit, Running: b.Running,
		ToolID: b.ToolID, ApprovalID: b.ApprovalID,
	}
}
