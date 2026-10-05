package app

import (
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// Markdown is parsed incrementally: each message carries an mdState
// that is fed only the text appended since the last frame, so a long
// streaming reply costs O(delta) per frame instead of re-parsing the
// whole document (which would be quadratic over the reply length).

// markdown renders a reply by its id, using and updating the cached
// parse state.
func (a *app) markdown(c *ui.Context, id, src string, complete bool) {
	st := a.mdFor(id, src, complete)
	t := c.Theme()
	for _, p := range st.parts {
		a.renderFencePart(c, p, t)
	}
	a.renderLive(c, st, t)
}

// mdState is the incrementally-parsed markdown of one message.
type mdState struct {
	text    string // the source consumed so far
	pending string // the incomplete trailing line
	inCode  bool
	lang    string
	parts   []fencePart
	cur     fencePart // the part being streamed
}

// mdFor returns the cached state for a message, extending it when the
// text grew by appending (the streaming case) and re-parsing from
// scratch when it was replaced.
func (a *app) mdFor(id, src string, complete bool) *mdState {
	if a.mdStates == nil {
		a.mdStates = map[string]*mdState{}
	}
	st := a.mdStates[id]
	if st == nil || !strings.HasPrefix(src, st.text) {
		st = &mdState{}
		a.mdStates[id] = st
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
	if len(a.mdStates) > 256 {
		clear(a.mdStates)
		a.mdStates[id] = st
	}
	return st
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
		st.flush() // blank lines end a prose part
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

// renderMD draws every completed part and the live tail.
func (a *app) renderMD(c *ui.Context, st *mdState, t *ui.Theme) {
	for _, p := range st.parts {
		a.renderFencePart(c, p, t)
	}
	live := st.cur.text + st.pending
	if live == "" {
		return
	}
	ui.Column(c).Gap(6).Children(func() {
		lines := strings.Split(live, "\n")
		body := strings.Join(lines[:max(len(lines)-1, 0)], "\n")
		if body != "" {
			a.renderLines(c, body, t)
		}
		// The trailing line may be incomplete mid-stream: render it as
		// inline text only.
		last := lines[len(lines)-1]
		if strings.TrimSpace(last) == "" {
			return
		}
		if st.inCode {
			ui.Text(c, last).Font("monospace").FontSize(12)
			return
		}
		ui.RichText(c).FontSize(14).LineHeight(1.6).Children(func() {
			a.renderInline(c, strings.TrimLeft(last, " "), t)
		})
	})
}

// renderFencePart renders one completed part: a code card or a block
// of prose lines with the full markdown dispatch.
func (a *app) renderFencePart(c *ui.Context, p fencePart, t *ui.Theme) {
	if p.code {
		a.codeCard(c, p)
		return
	}
	a.renderLines(c, p.text, t)
}

// renderLines is the markdown line dispatch: tables, headings, lists,
// quotes, rules and paragraphs with inline formatting.
func (a *app) renderLines(c *ui.Context, text string, t *ui.Theme) {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	for li := 0; li < len(lines); li++ {
		line := strings.TrimLeft(lines[li], " ")
		trimmed := strings.TrimSpace(line)
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
			a.tableBlock(c, run)
		case trimmed == "---" || trimmed == "***" || trimmed == "___":
			ui.Divider(c).MarginY(6)
		case strings.HasPrefix(trimmed, "### "):
			ui.Text(c, strings.TrimPrefix(trimmed, "### ")).FontSize(14).Bold()
		case strings.HasPrefix(trimmed, "## "):
			ui.Text(c, strings.TrimPrefix(trimmed, "## ")).FontSize(15.5).Bold()
		case strings.HasPrefix(trimmed, "# "):
			ui.Text(c, strings.TrimPrefix(trimmed, "# ")).FontSize(17).Bold()
		case strings.HasPrefix(trimmed, "> "):
			ui.Row(c).Gap(8).AlignItems(ui.Start).Children(func() {
				ui.Box(c).Width(2).Background(t.Border)
				ui.RichText(c).FontSize(14).LineHeight(1.6).Children(func() {
					a.renderInline(c, strings.TrimPrefix(trimmed, "> "), t)
				}).TextColor(t.TextMuted)
			})
		case strings.HasPrefix(trimmed, "- "), strings.HasPrefix(trimmed, "* "):
			ui.Row(c).Gap(8).AlignItems(ui.Start).Children(func() {
				ui.Text(c, "•").FontSize(14).LineHeight(1.6).TextColor(a.pal.TextMuted)
				ui.RichText(c).Grow(1).MinWidth(0).FontSize(14).LineHeight(1.6).Children(func() {
					a.renderInline(c, strings.TrimPrefix(strings.TrimPrefix(trimmed, "- "), "* "), t)
				})
			})
		default:
			if n, rest := numberedItem(trimmed); n != "" {
				ui.Row(c).Gap(8).AlignItems(ui.Start).Children(func() {
					ui.Text(c, n+".").Font("monospace").FontSize(12).TextColor(a.pal.TextMuted).
						Width(22).TextAlign(ui.End)
					ui.RichText(c).Grow(1).MinWidth(0).FontSize(14).LineHeight(1.6).Children(func() {
						a.renderInline(c, rest, t)
					})
				})
				continue
			}
			ui.RichText(c).FontSize(14).LineHeight(1.6).Children(func() {
				a.renderInline(c, trimmed, t)
			})
		}
	}
}

// numberedItem recognises "12. text" list lines and splits them.
func numberedItem(line string) (num, rest string) {
	i := strings.IndexAny(line, ".)")
	if i <= 0 || i > 4 {
		return "", ""
	}
	for _, r := range line[:i] {
		if r < '0' || r > '9' {
			return "", ""
		}
	}
	return line[:i], strings.TrimLeft(line[i+1:], " ")
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
func (a *app) tableBlock(c *ui.Context, run []string) {
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
					cell.Padding(2, 2, 6, 2).BorderWidth(0, 0, 1, 0).BorderColor(a.pal.Border)
				} else if !last {
					cell.BorderWidth(0, 0, 1, 0).BorderColor(a.pal.Border)
				}
				weight := 400
				if ri == 0 {
					weight = 600
				}
				cell.Children(func() {
					ui.RichText(c).Children(func() {
						a.renderInline(c, text, t)
					}).FontWeight(weight)
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
func (a *app) renderInline(c *ui.Context, line string, t *ui.Theme) {
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

// splitFences splits src into prose and fenced code parts (used by the
// incremental parser's callers and tests).
func splitFences(src string) []fencePart {
	var parts []fencePart
	var buf []string
	inCode := false
	lang := ""
	flush := func() {
		if len(buf) > 0 {
			parts = append(parts, fencePart{code: inCode, lang: lang, text: strings.Join(buf, "\n")})
			buf = nil
		}
	}
	for line := range strings.SplitSeq(src, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			flush()
			if inCode {
				inCode = false
				lang = ""
			} else {
				inCode = true
				lang = strings.TrimPrefix(trimmed, "```")
			}
			continue
		}
		buf = append(buf, line)
	}
	flush()
	return parts
}

// codeCard is a fenced block: the language, a copy button, and the code.
func (a *app) codeCard(c *ui.Context, p fencePart) {
	t := c.Theme()
	ui.Column(c).Radius(8).Background(a.pal.CodeBG).Border(1, a.pal.Border).Clip().Children(func() {
		ui.Row(c).Padding(5, 10).Gap(8).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(a.pal.Border).Children(func() {
			lang := p.lang
			if lang == "" {
				lang = "code"
			}
			ui.Text(c, lang).Font("monospace").FontSize(11).TextColor(t.TextMuted).Grow(1)
			cp := ui.ButtonBase(c).Label("Copy code").Tooltip("Copy").Size(22, 22).Radius(5).Center()
			if cp.Hovered() {
				cp.Background(a.pal.CardHover)
			}
			if cp.Clicked() {
				mygo.Clipboard.WriteText(p.text)
				c.Toast("Copied")
			}
			cp.Children(func() { ui.Icon(c, icCopy).FontSize(12).TextColor(t.TextMuted) })
		})
		ui.ScrollBoth(c).MaxHeight(360).Children(func() {
			ui.Text(c, strings.TrimRight(p.text, "\n")).Font("monospace").FontSize(12).
				Padding(10, 12).NoWrap()
		})
	})
}

// renderLive draws the part being streamed: its complete lines get the
// markdown dispatch and the trailing partial line renders as inline
// text.
func (a *app) renderLive(c *ui.Context, st *mdState, t *ui.Theme) {
	live := st.cur.text + st.pending
	if live == "" {
		return
	}
	ui.Column(c).Gap(6).Children(func() {
		lines := strings.Split(live, "\n")
		for li, line := range lines {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" {
				continue
			}
			if st.inCode {
				ui.Text(c, line).Font("monospace").FontSize(12)
				continue
			}
			if li == len(lines)-1 {
				ui.RichText(c).FontSize(14).LineHeight(1.6).Children(func() {
					a.renderInline(c, trimmed, t)
				})
				continue
			}
			a.renderLines(c, line, t)
		}
	})
}
