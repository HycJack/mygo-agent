// Package components holds reusable pieces of the app's UI, built on
// MyGo's toolkit and styled through the colors handed to them.
package components

import (
	"strings"

	"github.com/egoist/mygo/ui"
)

// Colors are the theme colors the components need, mapped from the
// app's palette so they follow it.
type Colors struct {
	Text      ui.Color
	TextMuted ui.Color
	Border    ui.Color
	Surface   ui.Color
	Active    ui.Color // the dash of the message at the top of the viewport
}

// RailItem is one entry of the message anchor rail.
type RailItem struct {
	ID      any    // stable identity across frames
	Preview string // a few lines of the message, shown in the hover card
	Kind    string // "user" for the user's own messages, else the agent's
	Active  bool   // the message at the top of the viewport right now
}

// AnchorRail is the slim ruler of dashes beside a thread, in the style
// of ZCode's message map: one dash per message — longer for longer
// messages, bright for the one at the top of the viewport — whose
// hover opens a preview card beside it and whose click calls onJump.
//
// Layout note for callers: place it in a row with AlignItems(Stretch)
// next to the scrollable content, never inside a Fill()ed element.
func AnchorRail(c *ui.Context, items []RailItem, colors Colors, onJump func(item RailItem, index int)) {
	if len(items) == 0 {
		ui.Spacer(c).Width(24)
		return
	}
	rail := ui.Column(c).Width(30).PaddingY(18).Gap(7).AlignItems(ui.Center)
	rail.Children(func() {
		for i, it := range items {
			it := it
			ui.Row(c).Key(it.ID).AlignItems(ui.Center).Children(func() {
				label := "Jump to: " + FirstLine(it.Preview)
				// The dash itself: longer messages draw longer dashes;
				// the message at the top of the viewport is brightest.
				dash := ui.Box(c).Height(3).Radius(2).Cursor(ui.CursorPointer).Label(label)
				switch {
				case it.Active:
					dash.Width(34).Background(colors.Active)
				case it.Kind == "user":
					dash.Width(26).Background(colors.TextMuted)
				default:
					dash.Width(18).Background(colors.Border)
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
						previewCard(c, it.Preview, colors)
					})
				}
			})
		}
	})
}

// previewCard draws the hover card: the message's first lines, capped
// so long replies stay a preview.
func previewCard(c *ui.Context, preview string, colors Colors) {
	lines := strings.Split(preview, "\n")
	if len(lines) > 6 {
		lines = append(lines[:6], "…")
	}
	ui.Column(c).Width(380).Padding(12, 14).Radius(10).
		Background(colors.Surface).Border(1, colors.Border).Shadow(0, 8, 24, 0, ui.RGBA(0, 0, 0, 0.4)).
		Children(func() {
			ui.Column(c).Gap(6).Children(func() {
				for i, l := range lines {
					runes := []rune(l)
					if len(runes) > 240 {
						l = string(runes[:240]) + "…"
					}
					col := colors.Text
					if i == len(lines)-1 && l == "…" {
						col = colors.TextMuted
					}
					ui.Text(c, l).FontSize(12.5).LineHeight(1.5).TextColor(col).MaxLines(2)
				}
			})
		})
}

// FirstLine is the first line of s.
func FirstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
