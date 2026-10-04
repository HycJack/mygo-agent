package main

import (
	"strings"

	"github.com/egoist/mygo/ui"
)

// RailItem is one entry of the message anchor rail.
type RailItem struct {
	ID      any    // stable identity across frames
	Preview string // a few lines of the message, shown in the hover card
	Kind    string // "user" for the user's own messages, else the agent's
	Active  bool   // the message at the top of the viewport right now
}

// MessageAnchorRail is the slim ruler of dashes beside a thread, in the
// style of ZCode's message map: one dash per message — longer for
// longer messages, bright for the one at the top of the viewport —
// whose hover opens a preview card beside it and whose click jumps to
// the message.
//
// Layout note for callers: place it in a row with AlignItems(Stretch)
// next to the scrollable content, never inside a Fill()ed element.
func MessageAnchorRail(c *ui.Context, items []RailItem, onJump func(item RailItem, index int)) {
	t := c.Theme()
	if len(items) == 0 {
		ui.Spacer(c).Width(24)
		return
	}
	rail := ui.Column(c).Width(30).PaddingY(18).Gap(7).AlignItems(ui.Center)
	rail.Children(func() {
		for i, it := range items {
			it := it
			ui.Row(c).Key(it.ID).AlignItems(ui.Center).Children(func() {
				label := "Jump to: " + firstLine(it.Preview)
				// The dash itself: longer messages draw longer dashes;
				// the message at the top of the viewport is brightest.
				dash := ui.Box(c).Height(3).Radius(2).Cursor(ui.CursorPointer).Label(label)
				switch {
				case it.Active:
					dash.Width(34).Background(t.Text)
				case it.Kind == "user":
					dash.Width(26).Background(t.TextMuted)
				default:
					dash.Width(18).Background(t.Border)
				}
				dash.Focusable()
				// The preview card opens while the dash is hovered and
				// closes when the pointer leaves; the open state rides
				// on the element itself.
				open := ui.Local(dash, "open", func() bool { return false })
				if dash.Hovered() && !*open {
					*open = true
				} else if !dash.Hovered() && *open {
					*open = false
				}
				if dash.Clicked() && onJump != nil {
					onJump(it, i)
				}
				if *open {
					// The card floats beside the dash, outside the
					// rail's width; nothing on the way clips it.
					ui.Box(c).Absolute().Left(34).Children(func() {
						railPreviewCard(c, it.Preview)
					})
				}
			})
		}
	})
}

// railPreviewCard draws the hover card: the message's first lines,
// capped so long replies stay a preview.
func railPreviewCard(c *ui.Context, preview string) {
	t := c.Theme()
	lines := strings.Split(preview, "\n")
	if len(lines) > 6 {
		lines = append(lines[:6], "…")
	}
	ui.Column(c).Width(380).Padding(12, 14).Radius(10).
		Background(t.Surface).Border(1, t.Border).Shadow(0, 8, 24, 0, ui.RGBA(0, 0, 0, 0.4)).
		Children(func() {
			ui.Column(c).Gap(6).Children(func() {
				for i, l := range lines {
					runes := []rune(l)
					if len(runes) > 240 {
						l = string(runes[:240]) + "…"
					}
					col := t.Text
					if i == len(lines)-1 && l == "…" {
						col = t.TextMuted
					}
					ui.Text(c, l).FontSize(12.5).LineHeight(1.5).TextColor(col).MaxLines(2)
				}
			})
		})
}

// firstLine is the first line of s.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// railPreview boils a message down to a short preview.
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
