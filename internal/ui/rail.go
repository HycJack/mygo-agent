package ui

import (
	"math"
	"strings"

	"github.com/egoist/mygo/ui"
)

// Colors are the theme colors the rail needs, mapped from the app's
// palette so they follow it.
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
	Active  bool   // the message at the top of the viewport right now
}

// The wave, in DIPs: the dash under the pointer is the longest, and the
// length falls off toward both sides — ZCode's message map.
const (
	dashMin   = 8.0  // the far ends of the wave
	dashMax   = 24.0 // the dash the wave centers on
	dashDecay = 0.5  // length falloff per step of distance
	dashBar   = 3.0  // the visual bar's height
	dashHit   = 14.0 // the clickable row's height
	dashEase  = 0.35 // per-frame easing toward the target width
)

// railLabel is one dash's "Jump to: …" text, cached beside the dash.
// The rail carries one dash per message, so formatting this string per
// frame allocated the whole transcript's worth of labels and tooltips
// on every repaint; the preview only changes when the message does, and
// the cache is keyed on exactly that.
type railLabel struct {
	preview string
	label   string
}

// AnchorRail is the slim ruler of dashes beside a thread, in the style
// of ZCode's message map: one dash per message; the dash under the
// pointer is the longest and its neighbours fall off like a wave, the
// wave centers on the message at the top of the viewport while the
// pointer is elsewhere, hover opens a preview card above the
// conversation, and a click calls onJump.
//
// Layout note for callers: place it in a row with AlignItems(Stretch)
// next to the scrollable content, never inside a Fill()ed element.
func AnchorRail(c *ui.Context, items []RailItem, colors Colors, onJump func(item RailItem, index int)) {
	if len(items) == 0 {
		ui.Spacer(c).Width(24)
		return
	}
	rail := ui.Column(c).Width(30).PaddingY(18).AlignItems(ui.Center)

	// The wave centers on the dash the pointer is on, else on the
	// viewport-top message. Hover is only known while a dash builds, so
	// the widths read last frame's center: a one-frame lag, invisible.
	center := ui.Local(rail, "center", func() int { return -1 })

	rail.Children(func() {
		hovered := -1
		animating := false
		for i := range items {
			i := i
			it := &items[i]
			row := ui.Row(c).Key(it.ID).Height(dashHit).AlignItems(ui.Center)
			row.Children(func() {
				lb := ui.Local(row, it.ID, func() railLabel { return railLabel{} })
				if lb.preview != it.Preview {
					lb.preview, lb.label = it.Preview, "Jump to: "+FirstLine(it.Preview)
				}

				d := math.Abs(float64(i - *center))
				target := dashMin + (dashMax-dashMin)*math.Pow(dashDecay, d)

				dash := ui.ButtonBase(c).Label(lb.label).Tooltip(lb.label).
					Size(26, dashHit).Radius(3).Center().Cursor(ui.CursorPointer)
				if dash.Hovered() {
					hovered = i
				}

				// The bar's width eases toward its wave target, so the
				// wave glides after the pointer instead of jumping.
				w := ui.Local(dash, "w", func() float32 { return 0 })
				delta := target - float64(*w)
				if math.Abs(delta) > 0.3 {
					*w += float32(delta * dashEase)
					animating = true
				} else {
					*w = float32(target)
				}

				// Color: the wave's peak is bright, its neighbours step
				// down; away from the rail the viewport-top dash leads.
				bar := colors.Border
				switch {
				case *center >= 0 && i == *center, hovered == -1 && it.Active:
					bar = colors.Text
				case math.Abs(d-1) < 0.5:
					bar = colors.TextMuted
				}
				dash.Children(func() {
					ui.Box(c).Size(*w, dashBar).Radius(1.5).Background(bar)
				})

				if dash.Clicked() && onJump != nil {
					onJump(*it, i)
				}
				if dash.Hovered() {
					// The preview paints in an overlay, above the
					// conversation — an absolute child of the rail would
					// land under the messages and turn unreadable.
					b := dash.Bounds()
					ui.Overlay(c, func() {
						ui.Box(c).Absolute().Left(b.X + b.W + 6).Top(b.Y - 6).Children(func() {
							previewCard(c, it.Preview, colors)
						})
					})
				}
			})
		}
		if hovered >= 0 {
			*center = hovered
		} else {
			for i := range items {
				if items[i].Active {
					*center = i
					break
				}
			}
		}
		if animating {
			c.AnimationFrame() // keep the wave gliding
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
		Background(colors.Surface).Border(1, colors.Border).Shadow(0, 12, 32, 0, ui.RGBA(0, 0, 0, 0.5)).
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
