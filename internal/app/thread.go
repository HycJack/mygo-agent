package app

import (
	"strings"

	"mygo-agent/internal/components"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// railPreview boils a message down to a short preview for the rail.
func railPreview(m *Message) string {
	if s := strings.TrimSpace(m.Text); s != "" {
		return s
	}
	for _, b := range m.Blocks {
		switch b.Type {
		case "command":
			return "$ " + b.Text
		case "diff":
			return "edited " + b.File
		case "error":
			return b.Text
		case "reasoning":
			return b.Text
		}
	}
	return "message"
}

// messages is the conversation: a virtualized list that follows its end
// as the agent replies, like a chat.
func (a *app) messages(c *ui.Context, th *Thread) {
	st := a.listState(th.ID)
	st.Key = func(i int) any { return th.Messages[i].ID }
	st.FollowEnd = true
	ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
		// The anchor rail: one dash per message, hover previews it,
		// click jumps to it.
		items := make([]components.RailItem, len(th.Messages))
		first, _ := st.Visible()
		for i := range th.Messages {
			m := &th.Messages[i]
			items[i] = components.RailItem{ID: m.ID, Preview: railPreview(m), Active: i == first}
		}
		components.AnchorRail(c, items, components.Colors{
			Active:    a.pal.Text,
			TextMuted: a.pal.TextMuted,
			Border:    a.pal.Border,
			Surface:   a.pal.Card,
			Text:      a.pal.Text,
		}, func(it components.RailItem, index int) {
			st.ScrollTo(index, ui.Start)
		})
		ui.List(c, st, len(th.Messages), func(i int) {
			a.messageRow(c, th, i)
		}).Grow(1).MinHeight(0).Justify(ui.End).Gap(20).Padding(24, 44, 16)
	})
}

// messageRow draws one turn: the user's prompt in a bubble, or the
// assistant's cards and reply — each with its hover actions (copy,
// resend / regenerate) and time, the way ZCode does.
func (a *app) messageRow(c *ui.Context, th *Thread, i int) {
	m := &th.Messages[i]
	if row := ui.Row(c).Key(m.ID).Fill(); row.Hovered() {
		a.hoverMsg = m.ID
	}
	showActions := a.hoverMsg == m.ID

	if m.Role == "user" {
		ui.Row(c).AlignItems(ui.Center).Children(func() {
			if showActions {
				a.messageActions(c, m, false)
			}
			bubble := ui.Box(c).MaxWidthPercent(72).Padding(9, 14).Radius(14).
				Background(a.pal.UserBubble)
			bubble.Margin(0, 0, 0, ui.Auto)
			bubble.Children(func() {
				ui.Text(c, m.Text).Selectable()
			})
		})
		return
	}
	t := c.Theme()
	isLast := !a.running && len(th.Messages) > 0 && th.Messages[len(th.Messages)-1].ID == m.ID
	ui.Row(c).Gap(10).AlignItems(ui.Start).Children(func() {
		avatar := ui.Box(c).Size(26, 26).Radius(7).Background(a.pal.Card).Border(1, a.pal.Border).Center()
		avatar.Children(func() { ui.Icon(c, icSparkles).FontSize(14).TextColor(t.Text) })
		ui.Column(c).Grow(1).MinWidth(0).Gap(6).Children(func() {
			// Tool calls sit in a tight group, one quiet row each.
			if len(m.Blocks) > 0 {
				ui.Column(c).Gap(2).Children(func() {
					for bi := range m.Blocks {
						a.block(c, &m.Blocks[bi])
					}
				})
			}
			if m.Text != "" {
				a.markdown(c, m.Text)
			}
			if m.Running && m.Text == "" && len(m.Blocks) == 0 {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Spinner(c)
					ui.Text(c, "Working…").FontSize(12).TextColor(t.TextMuted)
				})
			}
			if showActions {
				a.messageActions(c, m, isLast)
			}
		})
	})
}

// messageActions is the hover row under (or beside) a message: its time
// and its copy / resend / regenerate buttons.
func (a *app) messageActions(c *ui.Context, m *Message, canRegenerate bool) {
	t := c.Theme()
	ui.Row(c).Gap(2).AlignItems(ui.Center).Children(func() {
		stamp := m.At.Format("15:04")
		ui.Text(c, stamp).FontSize(10.5).TextColor(a.pal.TextMuted).Tooltip(m.At.Format("2006-01-02 15:04:05"))
		ui.Box(c).Width(4)
		a.msgAction(c, "Copy", icCopy, func() {
			mygo.Clipboard.WriteText(m.Text)
			c.Toast("Copied")
		})
		if m.Role == "user" {
			a.msgAction(c, "Resend", icArrowUp, func() {
				a.resend(currentThreadOf(a, m), m.Text)
			})
		} else if canRegenerate {
			a.msgAction(c, "Regenerate", icRefresh, func() {
				a.regenerate(currentThreadOf(a, m))
			})
		}
		_ = t
	})
}

// currentThreadOf finds the thread a message belongs to.
func currentThreadOf(a *app, m *Message) *Thread {
	for _, th := range a.threads {
		for i := range th.Messages {
			if th.Messages[i].ID == m.ID {
				return th
			}
		}
	}
	return nil
}

// msgAction is one small ghost icon button of a message's action row.
func (a *app) msgAction(c *ui.Context, tip string, ic *ui.SVG, fn func()) {
	b := ui.ButtonBase(c).Label(tip).Tooltip(tip).Size(22, 22).Radius(6).Center()
	if b.Hovered() {
		b.Background(a.pal.Hover)
	}
	if b.Clicked() {
		fn()
	}
	b.Children(func() { ui.Icon(c, ic).FontSize(12).TextColor(a.pal.TextMuted) })
}

// block draws one card of an assistant message.
func (a *app) block(c *ui.Context, b *Block) {
	switch b.Type {
	case "command":
		a.blockCommand(c, b)
	case "diff":
		a.blockDiff(c, b)
	case "error":
		a.blockError(c, b)
	case "reasoning":
		a.blockReasoning(c, b)
	}
}

// blockCommand is one tool call as a quiet row: status, the command,
// its duration, and its output folded away until clicked — many rows
// stack tightly, the way ZCode lays tool calls out.
func (a *app) blockCommand(c *ui.Context, b *Block) {
	t := c.Theme()
	head := ui.Row(c).MinHeight(22).Padding(0, 8).Gap(8).AlignItems(ui.Center).Radius(6).Cursor(ui.CursorPointer)
	if head.Hovered() || b.Open {
		head.Background(a.pal.Hover)
	}
	if head.Clicked() {
		b.Open = !b.Open
	}
	head.Children(func() {
		switch {
		case b.Running:
			ui.Spinner(c)
		case b.Exit == 0:
			ui.Icon(c, icCheck).FontSize(12).TextColor(a.pal.Success)
		default:
			ui.Icon(c, icX).FontSize(12).TextColor(a.pal.Danger)
		}
		ui.Text(c, "$ "+b.Text).Font("monospace").FontSize(12).Grow(1).MinWidth(0).SingleLine()
		if b.Ms > 0 {
			ui.Text(c, components.Duration(b.Ms)).FontSize(10.5).TextColor(a.pal.TextMuted)
		}
		chev := ui.Icon(c, icChevDown).FontSize(12).TextColor(a.pal.TextMuted)
		if b.Open {
			chev.Rotate(180)
		}
	})
	if b.Open {
		out := b.Output
		if strings.TrimSpace(out) == "" {
			out = "(no output)"
		}
		ui.Column(c).Padding(0, 28, 6).Children(func() {
			ui.ScrollBoth(c).MaxHeight(260).Radius(6).Background(a.pal.CodeBG).Children(func() {
				ui.Text(c, strings.TrimRight(out, "\n")).Font("monospace").FontSize(11.5).
					Padding(8, 10).NoWrap().TextColor(t.Text)
			})
		})
	}
}

// blockDiff is a card for a file the agent changed: the path, the counts,
// and the unified diff colored by line.
func (a *app) blockDiff(c *ui.Context, b *Block) {
	ui.Column(c).Radius(8).Background(a.pal.CodeBG).Border(1, a.pal.Border).Clip().Children(func() {
		head := ui.Row(c).Padding(7, 10).Gap(8).AlignItems(ui.Center).Cursor(ui.CursorPointer)
		if head.Clicked() {
			b.Open = !b.Open
		}
		head.Children(func() {
			ui.Icon(c, icFileCode).FontSize(13).TextColor(a.pal.TextMuted)
			ui.Text(c, b.File).Font("monospace").FontSize(12).Grow(1).MinWidth(0).SingleLine()
			if b.Add > 0 {
				ui.Textf(c, "+%d", b.Add).Font("monospace").FontSize(11).TextColor(a.pal.DiffAddText)
			}
			if b.Del > 0 {
				ui.Textf(c, "−%d", b.Del).Font("monospace").FontSize(11).TextColor(a.pal.DiffDelText)
			}
			chev := ui.Icon(c, icChevDown).FontSize(13).TextColor(a.pal.TextMuted)
			if b.Open {
				chev.Rotate(180)
			}
		})
		if b.Open && len(b.Lines) > 0 {
			ui.Scroll(c).MaxHeight(320).Children(func() {
				ui.Column(c).FillWidth().PaddingY(4).Children(func() {
					for _, l := range b.Lines {
						a.diffLineRow(c, l, false, false)
					}
				})
			})
		}
	})
}

// blockError is a card for something that went wrong.
func (a *app) blockError(c *ui.Context, b *Block) {
	t := c.Theme()
	ui.Row(c).Padding(9, 12).Gap(8).Radius(8).AlignItems(ui.Start).
		Background(a.pal.Danger.Alpha(0.10)).Border(1, a.pal.Danger.Alpha(0.35)).Children(func() {
		ui.Icon(c, icAlert).FontSize(14).TextColor(a.pal.Danger)
		ui.Text(c, b.Text).FontSize(12.5).TextColor(t.Text).Grow(1).MinWidth(0)
	})
}

// blockReasoning is a quiet note about how the agent thought.
func (a *app) blockReasoning(c *ui.Context, b *Block) {
	t := c.Theme()
	ui.Row(c).Padding(2, 0).Gap(8).Children(func() {
		ui.Box(c).Width(2).MinHeight(16).Radius(1).Background(a.pal.Border)
		ui.Text(c, b.Text).Italic().FontSize(12).TextColor(t.TextMuted).Grow(1).MinWidth(0)
	})
}

// markdown renders a reply: fenced code blocks become code cards,
// tables become grids, and the rest is paragraphs with `code`,
// **bold**, ~~strikethrough~~, [links](…), headings, lists, quotes and
// rules.
func (a *app) markdown(c *ui.Context, src string) {
	t := c.Theme()
	for _, part := range splitFences(src) {
		if part.code {
			a.codeCard(c, part)
			continue
		}
		ui.Column(c).Gap(6).Children(func() {
			lines := strings.Split(strings.TrimRight(part.text, "\n"), "\n")
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
					ui.Row(c).Gap(8).Children(func() {
						ui.Box(c).Width(2).Background(a.pal.Border)
						ui.RichText(c).FontSize(14).LineHeight(1.6).Children(func() {
							a.renderInline(c, strings.TrimPrefix(trimmed, "> "), t)
						}).TextColor(t.TextMuted)
					})
				case strings.HasPrefix(trimmed, "- "), strings.HasPrefix(trimmed, "* "):
					ui.Row(c).Gap(0).Children(func() {
						ui.Text(c, "•  ").FontSize(14).TextColor(a.pal.TextMuted)
						ui.RichText(c).FontSize(14).LineHeight(1.6).Children(func() {
							a.renderInline(c, strings.TrimPrefix(strings.TrimPrefix(trimmed, "- "), "* "), t)
						})
					})
				default:
					if n, rest := numberedItem(trimmed); n != "" {
						ui.Row(c).Gap(0).Children(func() {
							ui.Text(c, n+"  ").Font("monospace").FontSize(12).TextColor(a.pal.TextMuted)
							ui.RichText(c).FontSize(14).LineHeight(1.6).Children(func() {
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
		})
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
	gap := float32(0)
	_ = gap
	grid := ui.Grid(c).Columns(n).GapX(14).GapY(0)
	cell := func(text string, header bool, lastRow bool) {
		cell := ui.Column(c).Padding(5, 2).MinWidth(0)
		if header || !lastRow {
			cell.BorderWidth(0, 0, 1, 0).BorderColor(a.pal.Border)
			if header {
				cell.Padding(2, 2, 6)
			}
		}
		cell.Children(func() {
			if header {
				ui.RichText(c).Children(func() {
					a.renderInline(c, text, t)
				}).FontWeight(600)
				return
			}
			ui.RichText(c).Children(func() {
				a.renderInline(c, text, t)
			})
		})
	}
	grid.Children(func() {
		total := len(rows)
		for ri, r := range rows {
			last := ri == total-1
			for ci := 0; ci < n; ci++ {
				text := ""
				if ci < len(r) {
					text = r[ci]
				}
				cell(text, ri == 0, last)
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
	text := func(s string) *ui.Element {
		if s == "" {
			return nil
		}
		return ui.Text(c, s)
	}
	var plain strings.Builder
	flush := func() {
		if plain.Len() > 0 {
			text(plain.String())
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

// splitFences splits src into prose and fenced code parts.
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
	for _, line := range strings.Split(src, "\n") {
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
