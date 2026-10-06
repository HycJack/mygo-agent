package main

// The dashboard's own building blocks, abstracted from the agent's
// internal/ui patterns: the card frame (gallery + thread view), the KPI
// card with its delta pill, the header icon toggle (header.go) and the
// small segmented control over one integer (widgets.go). They take the
// palette as a value, the way the agent's widgets take its Palette.

import (
	"fmt"
	"math"

	"github.com/egoist/mygo/ui"
)

// Card frames a section of a page: the padded, bordered, slightly
// elevated surface every dashboard panel sits in. It does not grow:
// inside a scroll's content a growing element collapses the layout, so
// a card that shares a row's width takes Grow from its caller, where the
// row's definite measure makes grow mean something.
func Card(c *ui.Context, pal Palette, title, subtitle string, body func()) *ui.Element {
	return ui.Column(c).Padding(16).Gap(10).MinWidth(0).
		Radius(10).Background(pal.Card).Border(1, pal.Border).
		Shadow(0, 1, 3, 0, ui.RGBA(0, 0, 0, 0.10)).Children(func() {
		if title != "" {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, title).FontSize(14).FontWeight(600)
				if subtitle != "" {
					ui.Text(c, subtitle).FontSize(12).TextColor(pal.TextMuted)
				}
			})
		}
		body()
	})
}

// StatCard is one KPI tile: the label, the big number, a delta pill and
// a sparkline of the last samples.
func StatCard(c *ui.Context, pal Palette, label, value, delta string, up bool, ic *ui.SVG, samples []float64, series ui.Color) *ui.Element {
	return ui.Column(c).Padding(16).Gap(8).Grow(1).Basis(0).MinWidth(150).
		Radius(10).Background(pal.Card).Border(1, pal.Border).
		Shadow(0, 1, 3, 0, ui.RGBA(0, 0, 0, 0.10)).Children(func() {
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, ic).FontSize(14).TextColor(pal.TextMuted)
			ui.Text(c, label).FontSize(12).TextColor(pal.TextMuted)
		})
		ui.Text(c, value).FontSize(24).FontWeight(700)
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			DeltaPill(c, pal, delta, up)
			ui.Text(c, "vs last period").FontSize(11).TextColor(pal.TextMuted)
		})
		sparkline(c, samples, series).Height(36).Margin(4, 0, 0, 0).Grow(1)
	})
}

// DeltaPill is the little +4.6% / −1.2% badge of a KPI card.
func DeltaPill(c *ui.Context, pal Palette, delta string, up bool) {
	col := pal.Success
	ic := IconTrendUp
	if !up {
		col, ic = pal.Danger, IconTrendDown
	}
	ui.Row(c).Gap(3).AlignItems(ui.Center).Padding(1, 6).Radius(999).
		Background(col.Alpha(0.15)).Children(func() {
		ui.Icon(c, ic).FontSize(11).TextColor(col)
		ui.Text(c, delta).FontSize(11).FontWeight(600).TextColor(col)
	})
}

// Pill is a status badge: the order's paid / pending / refunded, an
// event's level, a member's role.
func Pill(c *ui.Context, pal Palette, text string, col ui.Color) *ui.Element {
	return ui.Row(c).Padding(1, 8).Radius(999).Background(col.Alpha(0.15)).
		Children(func() { ui.Text(c, text).FontSize(11).FontWeight(600).TextColor(col) })
}

// statusColor maps an order status or event level to its signal color.
func statusColor(pal Palette, s string) ui.Color {
	switch s {
	case "paid", "info", "deploy":
		return pal.Success
	case "pending", "warn":
		return pal.Warning
	case "refunded", "error":
		return pal.Danger
	default:
		return pal.TextMuted
	}
}

// labeledSwitch draws a switch with its text beside it: the widget's own
// Label names it to the screen reader, it does not render it.
func labeledSwitch(c *ui.Context, pal Palette, on *bool, label string) {
	ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Switch(c, on)
		ui.Text(c, label).FontSize(12.5)
	})
}

// iconToggle is the header's 28×28 icon button, from the agent's
// header.go: quiet until hovered or active.
func iconToggle(c *ui.Context, pal Palette, tip string, active bool, ic *ui.SVG, fn func()) *ui.Element {
	b := ui.ButtonBase(c).Label(tip).Tooltip(tip).Size(28, 28).Radius(7).Center().Cursor(ui.CursorPointer)
	if active || b.Hovered() {
		b.Background(pal.Hover)
	}
	if b.Clicked() {
		fn()
	}
	b.Children(func() {
		col := pal.TextMuted
		if active {
			col = pal.Text
		}
		ui.Icon(c, ic).FontSize(15).TextColor(col)
	})
	return b
}

// Segments is the composer's small segmented control over one integer
// choice (the agent's widgets.go): a value in, a click reported through
// set.
func Segments(c *ui.Context, pal Palette, value int, names []string, set func(int)) *ui.Element {
	chosen := value
	seg := ui.SegmentedBase(c, &chosen, len(names))
	seg.Track.Padding(2).Radius(8).Background(pal.Bg).Border(1, pal.Border).Children(func() {
		for i, name := range names {
			i, name := i, name
			s := seg.Segment(i).Padding(3, 10).Radius(6)
			on := i == value
			if on {
				s.Background(pal.CardHover)
			}
			s.Children(func() {
				col := pal.TextMuted
				if on {
					col = pal.Text
				}
				ui.Text(c, name).FontSize(11).FontWeight(600).TextColor(col)
			})
		}
	})
	if chosen != value {
		set(chosen)
	}
	return seg.Track
}

// PageTitle is the heading a page opens with, plus its one-line
// description.
func PageTitle(c *ui.Context, pal Palette, title, subtitle string) {
	ui.Column(c).Gap(2).Children(func() {
		ui.Text(c, title).FontSize(20).FontWeight(700)
		ui.Text(c, subtitle).FontSize(12.5).TextColor(pal.TextMuted)
	})
}

// KV is one label/value line of a detail panel.
func KV(c *ui.Context, pal Palette, label, value string) {
	ui.Row(c).Gap(12).Children(func() {
		ui.Text(c, label).Width(90).FontSize(12).TextColor(pal.TextMuted)
		ui.Text(c, value).FontSize(12).SingleLine().Grow(1)
	})
}

// rowOf is the KPI row's helper: four tiles that share the width evenly.
func kpiRow(c *ui.Context, cards func()) {
	ui.Row(c).Gap(14).AlignItems(ui.Stretch).Children(cards)
}

// sparkline draws the last samples as one stroked line, the gallery's
// live-data drawing, in a color of the caller's choosing.
func sparkline(c *ui.Context, samples []float64, col ui.Color) *ui.Element {
	return ui.Box(c).Draw(func(p *ui.Painter, r ui.Rect) {
		if len(samples) < 2 {
			return
		}
		lo, hi := samples[0], samples[0]
		for _, v := range samples {
			lo, hi = min(lo, v), max(hi, v)
		}
		span := max(hi-lo, 1e-9)
		var path ui.Path
		for i, v := range samples {
			x := r.X + 2 + float32(i)/float32(len(samples)-1)*(r.W-4)
			y := r.Y + r.H - 2 - float32((v-lo)/span)*(r.H-4)
			if i == 0 {
				path.MoveTo(x, y)
			} else {
				path.LineTo(x, y)
			}
		}
		p.StrokePath(&path, 1.5, col)
	})
}

// areaChart draws a labeled time series: horizontal gridlines, a smooth
// curve over a gradient fill, and the last point marked. It is custom
// painting, the way the gallery's Drawing page does it.
func areaChart(c *ui.Context, pal Palette, data []NamedValue, col ui.Color) *ui.Element {
	return ui.Box(c).Grow(1).Height(220).Draw(func(p *ui.Painter, r ui.Rect) {
		if len(data) < 2 {
			return
		}
		padL, padR, padT, padB := float32(44), float32(8), float32(10), float32(22)
		plot := ui.Rect{X: r.X + padL, Y: r.Y + padT, W: r.W - padL - padR, H: r.H - padT - padB}
		hi := data[0].Value
		for _, d := range data {
			hi = max(hi, d.Value)
		}
		hi = niceCeil(hi)
		// Gridlines and their labels at 0, ¼, ½, ¾, 1 of the range.
		for i := range 5 {
			f := float32(i) / 4
			y := plot.Y + plot.H - f*plot.H
			p.Fill(ui.Rect{X: plot.X, Y: y, W: plot.W, H: 1}, pal.Border, 0)
			p.Text(r.X, y-5, formatCompact(float64(hi)*float64(f)), 10, pal.TextMuted)
		}
		at := func(i int) (float32, float32) {
			x := plot.X + float32(i)/float32(len(data)-1)*plot.W
			v := float32(data[i].Value / hi)
			y := plot.Y + plot.H - v*plot.H
			return x, y
		}
		// The fill under the curve, fading out downward.
		var fill ui.Path
		x0, y0 := at(0)
		fill.MoveTo(x0, plot.Y+plot.H)
		fill.LineTo(x0, y0)
		for i := 1; i < len(data); i++ {
			px, py := at(i - 1)
			x, y := at(i)
			fill.QuadTo((px+x)/2, (py+y)/2, x, y)
		}
		fill.LineTo(plot.X+plot.W, plot.Y+plot.H)
		fill.Close()
		p.FillPathGradient(&fill, ui.LinearGradient{From: col.Alpha(0.35), To: col.Alpha(0.02), Angle: 90})
		// The curve itself.
		var line ui.Path
		line.MoveTo(x0, y0)
		for i := 1; i < len(data); i++ {
			px, py := at(i - 1)
			x, y := at(i)
			line.QuadTo((px+x)/2, (py+y)/2, x, y)
		}
		p.StrokePath(&line, 2, col)
		// The last point.
		xl, yl := at(len(data) - 1)
		var dot ui.Path
		dot.Circle(xl, yl, 3.5)
		p.FillPath(&dot, col)
		// The x labels: a few, spread over the width.
		step := (len(data) + 5) / 6
		for i := 0; i < len(data); i += step {
			x, _ := at(i)
			w := float32(len(data[i].Label)) * 6
			p.Text(min(x-w/2, plot.X+plot.W-w), r.Y+r.H-14, data[i].Label, 10, pal.TextMuted)
		}
	})
}

// barChart draws one rounded bar per value with its label and value
// under it; the tallest bar carries the accent color.
func barChart(c *ui.Context, pal Palette, data []NamedValue) *ui.Element {
	return ui.Box(c).Grow(1).Height(220).Draw(func(p *ui.Painter, r ui.Rect) {
		if len(data) == 0 {
			return
		}
		padL, padB := float32(44), float32(34)
		plot := ui.Rect{X: r.X + padL, Y: r.Y + 10, W: r.W - padL - 8, H: r.H - 10 - padB}
		hi := niceCeil(data[0].Value)
		for _, d := range data {
			hi = max(hi, d.Value)
		}
		hi = niceCeil(hi)
		for i := range 5 {
			f := float32(i) / 4
			y := plot.Y + plot.H - f*plot.H
			p.Fill(ui.Rect{X: plot.X, Y: y, W: plot.W, H: 1}, pal.Border, 0)
			p.Text(r.X, y-5, formatCompact(float64(hi)*float64(f)), 10, pal.TextMuted)
		}
		slot := plot.W / float32(len(data))
		bw := min(slot*0.6, 34)
		best := 0
		for i, d := range data {
			if d.Value > data[best].Value {
				best = i
			}
		}
		for i, d := range data {
			h := float32(d.Value/hi) * plot.H
			x := plot.X + float32(i)*slot + (slot-bw)/2
			col := pal.Series[0].Alpha(0.45)
			if i == best {
				col = pal.Series[0]
			}
			p.Fill(ui.Rect{X: x, Y: plot.Y + plot.H - h, W: bw, H: h}, col, 5)
			label := formatCompact(d.Value)
			p.Text(x+bw/2-float32(len(label))*3, plot.Y+plot.H-h-13, label, 10, pal.TextMuted)
			p.Text(x+bw/2-float32(len(d.Label))*3, plot.Y+plot.H+10, d.Label, 10, pal.TextMuted)
		}
	})
}

// donut draws the traffic sources as a ring of arcs with the total in
// the middle; the legend beside it is the caller's, so it can carry
// interactive rows.
func donut(c *ui.Context, pal Palette, data []NamedValue) *ui.Element {
	return ui.Box(c).Size(150, 150).Margin(0, ui.Auto).Draw(func(p *ui.Painter, r ui.Rect) {
		total := 0.0
		for _, d := range data {
			total += d.Value
		}
		cx, cy, radius, width := r.X+r.W/2, r.Y+r.H/2, min(r.W, r.H)/2-8, float32(18)
		from := -float32(math.Pi) / 2
		for i, d := range data {
			span := float32(d.Value/total) * 2 * math.Pi
			arc := func(a0, a1 float32) *ui.Path {
				var pa ui.Path
				const step = 0.08
				pa.MoveTo(cx+radius*cos(a0), cy+radius*sin(a0))
				for a := a0 + step; a < a1; a += step {
					pa.LineTo(cx+radius*cos(a), cy+radius*sin(a))
				}
				pa.LineTo(cx+radius*cos(a1), cy+radius*sin(a1))
				return &pa
			}
			p.StrokePath(arc(from, from+span), width, pal.Series[i%len(pal.Series)])
			from += span
		}
		label := formatCompact(total * 1000)
		p.Text(cx-float32(len(label))*6, cy+2, label, 20, pal.Text)
		sub := "visits"
		p.Text(cx-float32(len(sub))*3, cy+20, sub, 11, pal.TextMuted)
	})
}

// niceCeil rounds a chart's top value up to a round number, so the
// gridline labels read like 5k, 10k, 20k — not 4,731.
func niceCeil(v float64) float64 {
	if v <= 0 {
		return 1
	}
	mag := math.Pow(10, math.Floor(math.Log10(v)))
	for _, m := range []float64{1, 2, 2.5, 5, 10} {
		if v <= m*mag {
			return m * mag
		}
	}
	return 10 * mag
}

func cos(a float32) float32 { return float32(math.Cos(float64(a))) }
func sin(a float32) float32 { return float32(math.Sin(float64(a))) }

// formatPct renders a share of a total as a percent string.
func formatPct(part, total float64) string {
	if total == 0 {
		return "0%"
	}
	return fmt.Sprintf("%.0f%%", part/total*100)
}
