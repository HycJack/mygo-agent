package main

// The Overview page: KPI cards, the charts, the activity feed and the
// live card. Everything the eye lands on first.

import (
	"github.com/egoist/mygo/ui"
)

func (d *dashboard) overviewPage(c *ui.Context, pal Palette) {
	PageTitle(c, pal, "Good morning, Ada", d.now.Format("Monday, January 2 · here is what happened while you were away."))

	// The KPI row: four tiles, one sparkline each.
	series, _ := seriesFor(rangeNames[d.theRange])
	values := make([]float64, len(series))
	for i, s := range series {
		values[i] = s.Value
	}
	total := sum(values)
	orders := makeOrders(60)
	var paid float64
	for _, o := range orders {
		if o.Status == "paid" {
			paid += o.Amount
		}
	}
	kpiRow(c, func() {
		StatCard(c, pal, "Revenue", formatMoney(paid), "+12.4%", true, IconDollar, scale(values, paid/total), pal.Series[0])
		StatCard(c, pal, "Requests", formatCompact(total), "+8.1%", true, IconActivity, values, pal.Series[1])
		StatCard(c, pal, "New users", formatCompact(total/37), "+3.2%", true, IconUsers, scale(values, 1.0/41), pal.Series[3])
		StatCard(c, pal, "Conversion", "3.9%", "-0.4%", false, IconPercent, scale(values, 0.9/total), pal.Series[2])
	})

	// Traffic and its sources: the drawn area chart beside the donut and
	// its legend of Meter rows.
	ui.Row(c).Gap(14).AlignItems(ui.Stretch).Children(func() {
		Card(c, pal, seriesLabel(d.theRange), "updates when the range changes", func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, formatCompact(total)).FontSize(22).FontWeight(700)
				DeltaPill(c, pal, "+8.1%", true)
				ui.Spacer(c)
				Segments(c, pal, d.theRange, rangeNames, func(i int) { d.theRange = i })
			})
			areaChart(c, pal, series, pal.Series[0])
		}).Grow(3)
		Card(c, pal, "Traffic sources", "last "+rangeNames[d.theRange], func() {
			sources := trafficSources()
			donut(c, pal, sources)
			ui.Column(c).Gap(8).Margin(10, 0, 0, 0).Children(func() {
				total := 100.0
				for i, s := range sources {
					ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Box(c).Size(9, 9).Radius(4).Background(pal.Series[i%len(pal.Series)]).Shrink(0)
						ui.Text(c, s.Label).FontSize(12).Grow(1).MinWidth(0).SingleLine()
						ui.Text(c, formatPct(s.Value, total)).FontSize(12).TextColor(pal.TextMuted)
					})
					ui.Meter(c, s.Value, 0, 100, nil).Height(5).Label(s.Label)
				}
			})
		}).Grow(1).MaxWidth(340)
	})

	// Revenue by weekday, with the system health and the import zone
	// stacked beside it.
	ui.Row(c).Gap(14).AlignItems(ui.Start).Children(func() {
		Card(c, pal, "Revenue by day", "this week", func() {
			barChart(c, pal, revenueByDay())
		}).Grow(3)
		ui.Column(c).Grow(1).MaxWidth(340).Gap(14).Children(func() {
			Card(c, pal, "System health", "live", func() {
				d.healthCards(c, pal)
			})
			Card(c, pal, "Import", "", func() {
				zone := ui.Column(c).MinHeight(84).Padding(10).Radius(8).Gap(4).
					Justify(ui.Center).AlignItems(ui.Center).Background(pal.Bg).
					Border(1, pal.Border).BorderStyle(ui.BorderDashed)
				if files := zone.DroppedFiles(); files != nil {
					d.csvFiles = files
				}
				if zone.FileDragOver() {
					zone.Border(2, pal.Series[0])
				}
				zone.Children(func() {
					ui.Icon(c, IconInbox).FontSize(18).TextColor(pal.TextMuted)
					ui.Text(c, "Drop a CSV to import").FontSize(12).TextColor(pal.TextMuted)
					for i, f := range d.csvFiles {
						if i == 2 {
							ui.Textf(c, "and %d more…", len(d.csvFiles)-i).FontSize(11).TextColor(pal.TextMuted)
							break
						}
						ui.Text(c, baseName(f)).FontSize(11)
					}
				})
				if len(d.csvFiles) > 0 {
					if ui.Button(c, "Clear").Clicked() {
						d.csvFiles = nil
					}
				}
			})
		})
	})
}

// healthCards renders the three live meters the goroutine nudges.
func (d *dashboard) healthCards(c *ui.Context, pal Palette) {
	names := []string{"CPU", "Memory", "Disk"}
	icons := []*ui.SVG{IconZap, IconServer, IconDatabase}
	levels := &ui.MeterLevels{Warning: 70, Critical: 88}
	ui.Column(c).Gap(12).Children(func() {
		for i, n := range names {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, icons[i]).FontSize(13).TextColor(pal.TextMuted)
				ui.Text(c, n).FontSize(12).Grow(1)
				ui.Textf(c, "%.0f%%", d.health[i]).FontSize(12).TextColor(pal.TextMuted)
			})
			ui.Meter(c, d.health[i], 0, 100, levels).Height(6).Label(n)
		}
		ui.Row(c).Gap(8).AlignItems(ui.Center).Margin(6, 0, 0, 0).Children(func() {
			ui.Icon(c, IconClock).FontSize(13).TextColor(pal.TextMuted)
			ui.Text(c, "Uptime").FontSize(12).Grow(1)
			ui.Text(c, "41d 12h").FontSize(12).FontWeight(600)
		})
	})
}

// scale returns samples scaled to fit a KPI card's sparkline shape.
func scale(values []float64, k float64) []float64 {
	out := make([]float64, len(values))
	for i, v := range values {
		out[i] = v * k
	}
	return out
}

func maxOf(values []float64) float64 {
	m := values[0]
	for _, v := range values {
		m = max(m, v)
	}
	return m
}

func minOf(values []float64) float64 {
	m := values[0]
	for _, v := range values {
		m = min(m, v)
	}
	return m
}

// eventActor picks a stable name for an event's avatar.
func eventActor(seq int) string {
	actors := []string{"Ada Lovelace", "Grace Hopper", "Alan Turing", "Radia Perlman", "Ken Thompson"}
	return actors[seq%len(actors)]
}

// seriesLabel names the traffic card per range.
func seriesLabel(theRange int) string {
	_, label := seriesFor(rangeNames[theRange])
	return label
}

// baseName trims a dropped file's path.
func baseName(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			return path[i+1:]
		}
	}
	return path
}
