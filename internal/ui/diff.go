package ui

import (
	"fmt"

	"mygo-agent/internal/harness"

	"github.com/egoist/mygo/ui"
)

// diffLineRow renders one line of a diff or of a viewed text file: the
// row tinted by its kind, the line number for plain text, and the
// word-level change highlighted, as godiff does.
// DiffLineRow renders one line of a diff or of a viewed text file: the
// row tinted by its kind, the line number for plain text, and the
// word-level change highlighted.
func DiffLineRow(c *ui.Context, l harness.DiffLine, nums, wrap bool, pal Palette) {
	t := c.Theme()
	row := ui.Row(c).MinHeight(19).Padding(0, 12).AlignItems(ui.Start)
	switch l.Kind {
	case '+':
		row.Background(pal.DiffAddBG)
	case '-':
		row.Background(pal.DiffDelBG)
	case '@':
		row.Background(pal.Card)
	}
	row.Children(func() {
		if nums {
			num := ""
			if l.Kind != '@' {
				num = fmt.Sprint(l.Number)
			}
			ui.Text(c, num).Font("monospace").FontSize(11).Width(46).
				TextColor(pal.TextMuted).TextAlign(ui.End).Margin(0, 10, 0, 0)
		}
		if l.Kind == '@' {
			ui.Text(c, l.Text).Font("monospace").FontSize(11.5).TextColor(t.TextMuted)
			return
		}
		content := ui.Row(c).Grow(1).MinWidth(0)
		if wrap {
			content.Wrap()
		}
		content.Children(func() {
			if l.MarkHi > l.MarkLo {
				r := []rune(l.Text)
				lo, hi := l.MarkLo, l.MarkHi
				if lo > len(r) {
					lo = len(r)
				}
				if hi > len(r) {
					hi = len(r)
				}
				strong := pal.DiffAddMark
				if l.Kind == '-' {
					strong = pal.DiffDelMark
				}
				ui.RichText(c,
					ui.Span{Text: string(r[:lo])},
					ui.Span{Text: string(r[lo:hi]), Background: strong},
					ui.Span{Text: string(r[hi:])},
				).Font("monospace").FontSize(12)
				return
			}
			txt := ui.Text(c, l.Text).Font("monospace").FontSize(12)
			if !wrap {
				txt.NoWrap().SingleLine()
			}
			switch l.Kind {
			case '+':
				txt.TextColor(pal.DiffAddText)
			case '-':
				txt.TextColor(pal.DiffDelText)
			}
		})
	})
}
