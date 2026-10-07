package ui

import (
	"strings"

	"github.com/egoist/mygo/ui"
)

// mdCacheMax is how many messages' parses are kept live. Past it the
// cache gives an entry a second chance rather than dropping every live
// parse at once.
const mdCacheMax = 256

// MdCache holds the incrementally-parsed markdown of many messages; the
// host keeps one for the app's lifetime.
type MdCache struct {
	states map[string]*mdState
}

// NewMdCache returns an empty cache.
func NewMdCache() *MdCache { return &MdCache{states: map[string]*mdState{}} }

// markdownRenderer renders markdown with one palette.
type markdownRenderer struct{ pal Palette }

// Markdown is parsed incrementally: each message carries an mdState
// that is fed only the text appended since the last frame, so a long
// streaming reply costs O(delta) per frame instead of re-parsing the
// whole document (which would be quadratic over the reply length).

// Markdown renders a reply by its id, using and updating the cached
// parse state in cache.
func Markdown(c *ui.Context, cache *MdCache, id, src string, complete bool, pal Palette) {
	mdr := markdownRenderer{pal: pal}
	st := cache.forMsg(id, src, complete)
	t := c.Theme()
	for _, p := range st.parts {
		mdr.renderFencePart(c, p, t)
	}
	mdr.renderLive(c, st, t)
}

// mdState is the incrementally-parsed markdown of one message.
type mdState struct {
	text    string // the source consumed so far
	pending string // the incomplete trailing line
	inCode  bool
	lang    string
	parts   []fencePart
	cur     fencePart // the part being streamed
	live    bool      // rendered since the last eviction sweep
}

// forMsg returns the cached state for a message, extending it when the
// text grew by appending (the streaming case) and re-parsing from
// scratch when it was replaced.
func (m *MdCache) forMsg(id, src string, complete bool) *mdState {
	if m.states == nil {
		m.states = map[string]*mdState{}
	}
	st := m.states[id]
	if st == nil || !strings.HasPrefix(src, st.text) {
		st = &mdState{}
		m.states[id] = st
	}
	if src != st.text {
		st.feed(st.text, src)
		st.text = src
	}
	if complete && st.pending != "" {
		// The stream ended without a trailing newline: the pending
		// line is a real line.
		st.line(st.pending + "\n")
		st.pending = ""
	}
	if complete {
		// A complete message renders entirely through the parts path
		// (the selectable spans form): whatever is still in the live
		// tail — the message's last paragraph, usually — flushes into a
		// part here. renderLive is for streaming frames only.
		st.flush()
	}
	st.live = true
	if len(m.states) > mdCacheMax {
		m.evict()
	}
	return st
}

// evict frees room by dropping the parses not rendered since the last
// sweep, giving the rest a second chance. Clearing the whole map here
// threw away every live parse at once, so a long thread paid for the
// cache being full by re-parsing its whole conversation.
func (m *MdCache) evict() {
	for id, s := range m.states {
		if len(m.states) <= mdCacheMax {
			return
		}
		if s.live {
			s.live = false
			continue
		}
		delete(m.states, id)
	}
}

// feed consumes the new source suffix line by line, keeping the
// incomplete trailing line as pending.
func (st *mdState) feed(old, src string) {
	chunk := st.pending + src[len(old):]
	lines := strings.Split(chunk, "\n")
	complete := lines[:len(lines)-1]
	for _, line := range complete {
		st.line(line + "\n")
	}
	st.pending = lines[len(lines)-1]
}

// line advances the parse state by one complete line.
func (st *mdState) line(line string) {
	trimmed := strings.TrimSpace(line)
	switch {
	case st.inCode && strings.HasPrefix(strings.TrimSpace(line), "```"):
		st.flush()
		st.inCode = false
		st.lang = ""
	case st.inCode:
		st.cur.text += line
	case strings.HasPrefix(trimmed, "```"):
		st.flush()
		st.inCode = true
		st.lang = strings.TrimPrefix(trimmed, "```")
	case trimmed == "":
		// A blank line stays inside the prose part (its own newline):
		// splitting parts here would split the selectable text at every
		// paragraph, and a drag could not cross paragraphs. Code fences
		// still split above.
		st.cur.text += line
	default:
		st.cur.text += line
	}
}

// flush closes the part being streamed.
func (st *mdState) flush() {
	if st.cur.text == "" {
		return
	}
	st.parts = append(st.parts, fencePart{
		code: st.inCode,
		lang: st.lang,
		text: strings.TrimRight(st.cur.text, "\n"),
	})
	st.cur = fencePart{code: st.inCode, lang: st.lang}
}

// renderFencePart renders one completed part: a code card or a block
// of prose lines with the full markdown dispatch.
func (md markdownRenderer) renderFencePart(c *ui.Context, p fencePart, t *ui.Theme) {
	if p.code {
		md.codeCard(c, p)
		return
	}
	md.renderLines(c, p.text, t)
}

// renderLines is the markdown line dispatch: indented code blocks,
// tables, headings, lists, quotes, rules and paragraphs with inline
// formatting. The leading indent is kept rather than trimmed, because
// it is the only thing that says how deep a list item sits and whether
// a run of lines is an indented code block.
func (md markdownRenderer) renderLines(c *ui.Context, text string, t *ui.Theme) {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	// The content indent of the list being read, -1 outside one: a line
	// indented past it is that item's own text, so a nested item stays
	// a list instead of turning into an indented code block.
	listBase := -1
	for li := 0; li < len(lines); li++ {
		line := lines[li]
		ind := indentOf(line)
		trimmed := strings.TrimSpace(line)

		// Four columns of indent outside a list item is an indented code
		// block. Collect the whole run so it renders as one card.
		if trimmed != "" && ind >= 4 && (listBase < 0 || ind < listBase) {
			run := []string{trimmed}
			for li+1 < len(lines) {
				next := lines[li+1]
				if strings.TrimSpace(next) == "" || indentOf(next) < 4 {
					break
				}
				li++
				run = append(run, strings.TrimSpace(lines[li]))
			}
			md.codeCard(c, fencePart{text: strings.Join(run, "\n")})
			continue
		}

		switch {
		case trimmed == "":
			continue
		case strings.HasPrefix(trimmed, "|") && li+1 < len(lines) &&
			isTableSeparator(strings.TrimSpace(lines[li+1])):
			run := []string{trimmed}
			li++
			run = append(run, strings.TrimSpace(lines[li]))
			for li+1 < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[li+1]), "|") {
				li++
				run = append(run, strings.TrimSpace(lines[li]))
			}
			md.tableBlock(c, run)
		case trimmed == "---" || trimmed == "***" || trimmed == "___":
			ui.Divider(c).MarginY(6)
		case strings.HasPrefix(trimmed, "### "):
			ui.Text(c, strings.TrimPrefix(trimmed, "### ")).FontSize(14).Bold().Selectable()
		case strings.HasPrefix(trimmed, "## "):
			ui.Text(c, strings.TrimPrefix(trimmed, "## ")).FontSize(15.5).Bold().Selectable()
		case strings.HasPrefix(trimmed, "# "):
			ui.Text(c, strings.TrimPrefix(trimmed, "# ")).FontSize(17).Bold().Selectable()
		case strings.HasPrefix(trimmed, "> "):
			ui.Row(c).Gap(8).AlignItems(ui.Start).Children(func() {
				ui.Box(c).Width(2).Background(t.Border)
				md.inlineSelectable(c, strings.TrimPrefix(trimmed, "> "), t,
					func(rt *ui.Element) { rt.FontSize(14).LineHeight(1.6).TextColor(t.TextMuted) })
			})
		case strings.HasPrefix(trimmed, "- "), strings.HasPrefix(trimmed, "* "):
			openList(&listBase, ind)
			ui.Row(c).Gap(8).AlignItems(ui.Start).Margin(0, 0, 0, depthIndent(ind)).Children(func() {
				ui.Text(c, "•").FontSize(14).LineHeight(1.6).TextColor(md.pal.TextMuted)
				md.inlineSelectable(c, strings.TrimPrefix(strings.TrimPrefix(trimmed, "- "), "* "), t,
					func(rt *ui.Element) { rt.Grow(1).MinWidth(0).FontSize(14).LineHeight(1.6) })
			})
		default:
			if n, rest := numberedItem(trimmed); n != "" {
				openList(&listBase, ind)
				ui.Row(c).Gap(8).AlignItems(ui.Start).Margin(0, 0, 0, depthIndent(ind)).Children(func() {
					ui.Text(c, n+".").Font("monospace").FontSize(12).TextColor(md.pal.TextMuted).
						Width(22).TextAlign(ui.End)
					md.inlineSelectable(c, rest, t,
						func(rt *ui.Element) { rt.Grow(1).MinWidth(0).FontSize(14).LineHeight(1.6) })
				})
				continue
			}
			// One selectable element per PROSE RUN, not per line or per
			// paragraph: selection lives inside a single text element,
			// and per-line/per-paragraph fragments made a drag stop at
			// every break — message content read as unselectable. A
			// prose run is every paragraph plus the blank lines between
			// them, up to the next block kind (heading, list, quote,
			// table, rule, code). Link-free runs ride constructor spans
			// (the selectable form); a run with links keeps the element
			// form so the links stay clickable.
			para := []string{trimmed}
			for li+1 < len(lines) {
				next := lines[li+1]
				if strings.TrimSpace(next) == "" {
					// The blank line rides with the run when prose
					// resumes after it; otherwise it ends the run.
					if li+2 < len(lines) && paragraphContinues(lines[li+2]) {
						para = append(para, "", strings.TrimSpace(lines[li+2]))
						li += 2
						continue
					}
					break
				}
				if !paragraphContinues(next) {
					break
				}
				para = append(para, strings.TrimSpace(next))
				li++
			}
			if paragraphHasLink(para) {
				ui.RichText(c).FontSize(14).LineHeight(1.6).Selectable().Children(func() {
					for pi, pl := range para {
						if pi > 0 {
							md.renderInline(c, "\n", t)
						}
						md.renderInline(c, pl, t)
					}
				})
				continue
			}
			var spans []ui.Span
			for pi, pl := range para {
				if pi > 0 {
					spans = append(spans, ui.Span{Text: "\n"})
				}
				spans = append(spans, md.inlineSpans(pl, t)...)
			}
			ui.RichText(c, spans...).FontSize(14).LineHeight(1.6).Selectable()
		}
	}
}

// paragraphContinues reports whether a line is still plain paragraph
// prose — a run of such lines renders as one selectable paragraph. Every
// other block kind (heading, list item, quote, table, rule, indented
// code, a table's separator neighbor) ends the paragraph.
func paragraphContinues(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}
	switch {
	case strings.HasPrefix(trimmed, "#"), strings.HasPrefix(trimmed, "> "),
		strings.HasPrefix(trimmed, "- "), strings.HasPrefix(trimmed, "* "),
		trimmed == "---", trimmed == "***", trimmed == "___",
		strings.HasPrefix(trimmed, "|"):
		return false
	}
	if ind := indentOf(line); ind >= 4 {
		return false // an indented code block
	}
	if n, _ := numberedItem(trimmed); n != "" {
		return false
	}
	return true
}

// indentOf is the width of a line's leading indent, a tab counting as
// the four columns markdown measures it as.
func indentOf(line string) int {
	n := 0
	for _, r := range line {
		switch r {
		case ' ':
			n++
		case '\t':
			n += 4
		default:
			return n
		}
	}
	return n
}

// depthIndent is how far in a list item at indent ind sits, two columns
// of indent per level.
func depthIndent(ind int) float32 { return float32(ind/2) * 18 }

// openList notes the content indent of the list an item at indent ind
// belongs to, so the lines inside it are read as its text.
func openList(listBase *int, ind int) {
	if *listBase < 0 || ind < *listBase {
		*listBase = ind + 2
	}
}

// numberedItem recognises "12. text" list lines and splits them. The
// separator has to open the item — "3.14 is pi" is prose, not the item
// "3." followed by "14 is pi".
func numberedItem(line string) (num, rest string) {
	i := strings.IndexAny(line, ".)")
	if i <= 0 || i > 4 {
		return "", ""
	}
	if i+1 >= len(line) || (line[i+1] != ' ' && line[i+1] != '\t') {
		return "", ""
	}
	for _, r := range line[:i] {
		if r < '0' || r > '9' {
			return "", ""
		}
	}
	return line[:i], strings.TrimLeft(line[i+1:], " \t")
}

// isTableSeparator reports whether a line is the |---|---| divider of a
// markdown table.
func isTableSeparator(line string) bool {
	if !strings.Contains(line, "-") {
		return false
	}
	for _, r := range line {
		switch r {
		case '|', '-', ':', ' ':
		default:
			return false
		}
	}
	return true
}

// tableBlock renders a markdown table as a grid: a bold header over a
// subtle rule, body rows with hairline separators, and inline
// formatting inside every cell.
func (md markdownRenderer) tableBlock(c *ui.Context, run []string) {
	t := c.Theme()
	rows := make([][]string, 0, len(run))
	for _, line := range run {
		if isTableSeparator(line) {
			continue
		}
		rows = append(rows, splitTableRow(line))
	}
	if len(rows) == 0 {
		return
	}
	n := 0
	for _, r := range rows {
		if len(r) > n {
			n = len(r)
		}
	}
	if n == 0 {
		return
	}
	grid := ui.Grid(c).Columns(n).GapX(14).GapY(0)
	grid.Children(func() {
		total := len(rows)
		for ri, r := range rows {
			last := ri == total-1
			for ci := 0; ci < n; ci++ {
				text := ""
				if ci < len(r) {
					text = r[ci]
				}
				cell := ui.Column(c).Padding(5, 2).MinWidth(0)
				if ri == 0 {
					// The header row: bold text over a solid rule.
					cell.Padding(2, 2, 6, 2).BorderWidth(0, 0, 1, 0).BorderColor(md.pal.Border)
				} else if !last {
					cell.BorderWidth(0, 0, 1, 0).BorderColor(md.pal.Border)
				}
				weight := 400
				if ri == 0 {
					weight = 600
				}
				cell.Children(func() {
					md.inlineSelectable(c, text, t, func(rt *ui.Element) { rt.FontWeight(weight) })
				})
			}
		}
	})
}

// splitTableRow splits one "| a | b |" line into its cells.
func splitTableRow(line string) []string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "|")
	line = strings.TrimSuffix(line, "|")
	parts := strings.Split(line, "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

// renderInline emits one line's inline formatting — `code`, **bold**,
// ~~struck~~, [text](url) — as the children of a RichText paragraph.
// inlineSpans renders one line's inline markdown as constructor spans —
// the RichText form whose selectable editor actually receives presses
// (a RichText built from Children elements has its presses swallowed by
// them, spec/relay-lessons.md §8.5). Links stay element-form and keep
// their click; a line carrying one must render through renderInline.
// inlineSelectable renders one line's inline markdown as a selectable
// text: constructor spans when the line carries no link (the form
// whose selectable editor receives presses), the element form when it
// does — a link is a clickable element and cannot be a span. style
// tweaks the element (size, weight, indent-filling) in either form.
func (md markdownRenderer) inlineSelectable(c *ui.Context, line string, t *ui.Theme, style func(*ui.Element)) {
	if paragraphHasLink([]string{line}) {
		rt := ui.RichText(c).Selectable().Children(func() {
			md.renderInline(c, line, t)
		})
		if style != nil {
			style(rt)
		}
		return
	}
	rt := ui.RichText(c, md.inlineSpans(line, t)...).Selectable()
	if style != nil {
		style(rt)
	}
}

func (md markdownRenderer) inlineSpans(line string, t *ui.Theme) []ui.Span {
	var out []ui.Span
	plain := &strings.Builder{}
	flush := func() {
		if plain.Len() > 0 {
			out = append(out, ui.Span{Text: plain.String()})
			plain.Reset()
		}
	}
	for i := 0; i < len(line); {
		switch {
		case line[i] == '`':
			if end := strings.IndexByte(line[i+1:], '`'); end >= 0 {
				flush()
				out = append(out, ui.Span{Text: line[i+1 : i+1+end],
					Font: "monospace", Size: 11.5, Background: t.Surface})
				i += end + 2
				continue
			}
			plain.WriteByte(line[i])
			i++
		case strings.HasPrefix(line[i:], "**"):
			if end := strings.Index(line[i+2:], "**"); end >= 0 {
				flush()
				out = append(out, ui.Span{Text: line[i+2 : i+2+end], Weight: 700})
				i += end + 4
				continue
			}
			plain.WriteByte(line[i])
			i++
		case strings.HasPrefix(line[i:], "~~"):
			if end := strings.Index(line[i+2:], "~~"); end >= 0 {
				flush()
				out = append(out, ui.Span{Text: line[i+2 : i+2+end],
					Strikethrough: true, Color: t.TextMuted})
				i += end + 4
				continue
			}
			plain.WriteByte(line[i])
			i++
		default:
			plain.WriteByte(line[i])
			i++
		}
	}
	flush()
	return out
}

// paragraphHasLink reports whether the paragraph carries a markdown
// link: links are clickable elements and must render through
// renderInline, at the cost of drag-selection.
func paragraphHasLink(lines []string) bool {
	for _, l := range lines {
		if strings.Contains(l, "](") && strings.Contains(l, "[") {
			return true
		}
	}
	return false
}

func (md markdownRenderer) renderInline(c *ui.Context, line string, t *ui.Theme) {
	var plain strings.Builder
	flush := func() {
		if plain.Len() > 0 {
			ui.Text(c, plain.String())
			plain.Reset()
		}
	}
	for i := 0; i < len(line); {
		switch {
		case line[i] == '`':
			if end := strings.IndexByte(line[i+1:], '`'); end >= 0 {
				flush()
				ui.Text(c, line[i+1:i+1+end]).Font("monospace").FontSize(11.5).
					TextBackground(t.Surface)
				i += end + 2
				continue
			}
			plain.WriteByte(line[i])
			i++
		case strings.HasPrefix(line[i:], "**"):
			if end := strings.Index(line[i+2:], "**"); end >= 0 {
				flush()
				ui.Text(c, line[i+2:i+2+end]).FontWeight(700)
				i += end + 4
				continue
			}
			plain.WriteByte(line[i])
			i++
		case strings.HasPrefix(line[i:], "~~"):
			if end := strings.Index(line[i+2:], "~~"); end >= 0 {
				flush()
				ui.Text(c, line[i+2:i+2+end]).Strikethrough().TextColor(t.TextMuted)
				i += end + 4
				continue
			}
			plain.WriteByte(line[i])
			i++
		case line[i] == '[':
			if end := strings.Index(line[i:], "]("); end > 0 {
				after := strings.Index(line[i+end:], ")")
				if after > 0 {
					label := line[i+1 : i+end]
					url := line[i+end+2 : i+end+after]
					flush()
					ui.Link(c, label, url)
					i += end + after + 1
					continue
				}
			}
			plain.WriteByte(line[i])
			i++
		default:
			plain.WriteByte(line[i])
			i++
		}
	}
	flush()
}

// fencePart is one run of text between code fences.
type fencePart struct {
	code bool
	lang string
	text string
}

// codeCard is a fenced block: the language, a copy button, and the code.
func (md markdownRenderer) codeCard(c *ui.Context, p fencePart) {
	t := c.Theme()
	ui.Column(c).Radius(8).Background(md.pal.CodeBG).Border(1, md.pal.Border).Clip().Children(func() {
		ui.Row(c).Padding(5, 10).Gap(8).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(md.pal.Border).Children(func() {
			lang := p.lang
			if lang == "" {
				lang = "code"
			}
			ui.Text(c, lang).Font("monospace").FontSize(11).TextColor(t.TextMuted).Grow(1)
			cp := ui.ButtonBase(c).Label("Copy code").Tooltip("Copy").Size(22, 22).Radius(5).Center()
			if cp.Hovered() {
				cp.Background(md.pal.CardHover)
			}
			if cp.Clicked() {
				c.WriteClipboard(p.text)
				c.Toast("Copied")
			}
			cp.Children(func() { ui.Icon(c, IconCopy).FontSize(12).TextColor(t.TextMuted) })
		})
		ui.ScrollBoth(c).MaxHeight(360).Children(func() {
			ui.Text(c, strings.TrimRight(p.text, "\n")).Font("monospace").FontSize(12).Selectable().
				Padding(10, 12).NoWrap()
		})
	})
}

// renderLive draws the part being streamed: an open fence as the same
// card a closed one is, height cap included, so a streaming block looks
// like the finished one and cannot grow the reply without limit; a
// prose tail as blocks, its trailing partial line as inline text.
func (md markdownRenderer) renderLive(c *ui.Context, st *mdState, t *ui.Theme) {
	live := st.cur.text + st.pending
	if live == "" {
		return
	}
	if st.inCode {
		md.codeCard(c, fencePart{code: true, lang: st.lang, text: live})
		return
	}
	// The complete lines go through the block dispatch as one run, so a
	// table, a list or an indented code block still in the tail is read
	// as a block; only the trailing partial line — the one still
	// arriving — renders as inline text.
	lines := strings.Split(live, "\n")
	body := strings.Join(lines[:max(0, len(lines)-1)], "\n")
	ui.Column(c).Gap(6).Children(func() {
		if strings.TrimSpace(body) != "" {
			md.renderLines(c, body, t)
		}
		last := strings.TrimSpace(lines[len(lines)-1])
		if last == "" {
			return
		}
		ui.RichText(c).FontSize(14).LineHeight(1.6).Selectable().Children(func() {
			md.renderInline(c, last, t)
		})
	})
}
