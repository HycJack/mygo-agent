package main

// The Mujica Charts page: one card per chart type in the library's
// chart package, packed two per row (wide charts span the row), then a
// closing card with flagship widgets from the wider library. Everything
// here is styled through MujicaUI tokens — mujicaPage's core.Use has
// already re-themed the frame — so the dashboard's own palette is
// deliberately left unused.

import (
	"fmt"
	"math"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/ZacharyZhang-NY/MujicaUI/chart"
	"github.com/ZacharyZhang-NY/MujicaUI/code"
	"github.com/ZacharyZhang-NY/MujicaUI/core"
	"github.com/ZacharyZhang-NY/MujicaUI/data"
	"github.com/ZacharyZhang-NY/MujicaUI/messaging"
)

// mujicaChartsState is the page's demo state. Charts keep paths,
// selections, windows and viewers in it; ui.Local ties it to the window
// so it survives across frames. Zero values must be live states, so the
// init below seeds the ones that need a non-zero start.
type mujicaChartsState struct {
	donut     bool                    // donut card's hole toggle
	gauge     float64                 // gauge value, 0..100
	picked    []chart.ScatterKey      // scatter selection
	tree      []string                // treemap drill-down path
	sun       []string                // sunburst drill-down path
	net       chart.NetworkGraphState // network pan/zoom layout
	filters   map[string][2]float64   // parallel-coordinate axis filters
	year      int                     // calendar heatmap year
	market    chart.MarketView        // candlestick visible window
	mtype     chart.ChartType         // candlestick series shape
	brush     [2]float64              // range-brush window
	attempts  int                     // empty-state retry clicks
	mailItems []messaging.MailItem    // mail list rows, mutated in place
	mails     data.ListState[string]  // mail list selection state
	opened    string                  // last opened mail id
	viewer    code.CodeViewerState    // code viewer scroll/selection
}

// mujicaChartsTree is the drill-down tree the treemap and sunburst share.
func mujicaChartsTree() chart.ChartNode {
	return chart.ChartNode{Name: "Services", Children: []chart.ChartNode{
		{Name: "API", Children: []chart.ChartNode{{Name: "REST", Value: 30}, {Name: "gRPC", Value: 14}}},
		{Name: "Storage", Value: 25},
		{Name: "Web", Children: []chart.ChartNode{{Name: "Dash", Value: 12}, {Name: "Docs", Value: 8}}},
		{Name: "Batch", Value: 18},
	}}
}

// mujicaChartsCandles builds 60 deterministic daily candles; times must
// strictly increase and high/low must bracket open/close.
func mujicaChartsCandles() []chart.Candle {
	t0 := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	candles := make([]chart.Candle, 60)
	for i := range candles {
		open := 100 + 8*math.Sin(float64(i)/5)
		close := 100 + 8*math.Sin(float64(i)/5+0.7)
		candles[i] = chart.Candle{
			Time: t0.Add(time.Duration(i) * 24 * time.Hour),
			Open: open, High: max(open, close) + 1.5, Low: min(open, close) - 1.5, Close: close,
			Volume: float64(900 + 37*i),
		}
	}
	return candles
}

// mujicaChartsPage shows the chart package's catalog and a taste of the
// domain packages. `pal` is ignored on purpose: components read their
// colors from the tokens core.Use installed for this frame.
func (d *dashboard) mujicaChartsPage(c *ui.Context, pal Palette) {
	d.mujicaPage(c, "Mujica Charts", "The chart package's catalog, then flagship widgets from the wider library.", func() {
		s := ui.Local(c.Root(), "mcharts-state", func() mujicaChartsState {
			return mujicaChartsState{
				donut: true, gauge: 68, year: 2026, brush: [2]float64{20, 70},
				filters: map[string][2]float64{},
				mailItems: []messaging.MailItem{
					{ID: "1", From: "Sakiko", Subject: "Second act", Snippet: "The score is attached.", Unread: true, Attachments: 2},
					{ID: "2", From: "Uika", Subject: "Costumes", Snippet: "Fittings on Thursday.", Starred: true},
					{ID: "3", From: "Umiri", Subject: "Venue", Snippet: "The hall is booked."},
				},
			}
		})

		// Small charts pack two per row; wide charts call ColumnSpan(2)
		// on their card. No card grows: the page sits in the shell's
		// scroll, and a growing child of scroll content collapses it.
		ui.Grid(c).Columns(2).Gap(12).Children(func() {
			// Line: three series; the built-in legend, axes and tooltip
			// come from the default options.
			mujicaCard(c, "Line", "Three series with the built-in legend, axes and tooltip.", func() {
				chart.LineChart(c, []chart.ChartSeries{
					{Name: "Requests", Values: []float64{3, 5, 4, 6, 8, 7, 9, 11}},
					{Name: "Errors", Values: []float64{2, 3, 5, 4, 6, 9, 8, 7}},
					{Name: "Timeouts", Values: []float64{1, 2, 2, 3, 2, 4, 5, 4}},
				}, chart.LineChartOptions{Frame: chart.ChartFrame{Label: "Requests per hour", Height: 170}})
			})

			// Area: stacked bands over the same shape of data.
			mujicaCard(c, "Area", "Stacked bands; Baseline moves the common floor.", func() {
				chart.AreaChart(c, []chart.ChartSeries{
					{Name: "Matins", Values: []float64{3, 5, 4, 6, 8, 7, 9, 11}},
					{Name: "Vespers", Values: []float64{2, 3, 5, 4, 6, 9, 8, 7}},
				}, chart.AreaChartOptions{Frame: chart.ChartFrame{Label: "Attendance", Height: 170}, Stacked: true})
			})

			// Bar: grouped columns, one per category, with value labels.
			mujicaCard(c, "Bar", "Grouped columns with labels; negative values dip below zero.", func() {
				chart.BarChart(c, []string{"Nave", "Choir", "Crypt", "Bells"}, []chart.ChartSeries{
					{Name: "Gold", Values: []float64{4, -2, 3, 5}},
					{Name: "Silver", Values: []float64{2, 5, 1, -1}},
				}, chart.BarChartOptions{Frame: chart.ChartFrame{Label: "Tithes", Height: 170}, Labels: true})
			})

			// Pie: plain wedges; legend picks up the slice labels.
			mujicaCard(c, "Pie", "Wedges sized by value; MinAngle keeps slivers readable.", func() {
				chart.PieChart(c, []chart.ChartSlice{
					{Label: "Wax", Value: 50}, {Label: "Oil", Value: 30}, {Label: "Myrrh", Value: 12}, {Label: "Salt", Value: 4},
				}, chart.PieChartOptions{Frame: chart.ChartFrame{Label: "Stores", Height: 170}})
			})

			// Donut: a pie with a hole; the toggle shrinks it back.
			mujicaCard(c, "Donut", "PieChart with the hole on; the check box turns it back into a pie.", func() {
				ui.Checkbox(c, &s.donut, "Hole")
				chart.PieChart(c, []chart.ChartSlice{
					{Label: "Go", Value: 46}, {Label: "Rust", Value: 27}, {Label: "TS", Value: 18}, {Label: "Other", Value: 9},
				}, chart.PieChartOptions{Frame: chart.ChartFrame{Label: "Languages", Height: 160}, Donut: s.donut})
			})

			// Gauge: the slider drives the needle through toned segments.
			mujicaCard(c, "Gauge", "Segmented arc; drag the slider to move the needle.", func() {
				ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
					ui.StepSlider(c, &s.gauge, 0, 100, 1).Width(140)
					ui.Text(c, fmt.Sprintf("%.0f%%", s.gauge))
				})
				chart.Gauge(c, s.gauge, chart.GaugeOptions{
					Frame: chart.ChartFrame{Label: "Nave fullness", Height: 130}, Max: 100, Unit: "%",
					Segments: []chart.GaugeSegment{
						{To: 60, Label: "Calm", Tone: chart.GaugeGood},
						{To: 85, Label: "Busy", Tone: chart.GaugeWarning},
						{To: 100, Label: "Crowded", Tone: chart.GaugeDanger},
					},
				})
			})

			// Radar: two overlapping polygons over five axes.
			mujicaCard(c, "Radar", "Two series over five scored axes.", func() {
				chart.RadarChart(c, []string{"Voice", "Latin", "Chant", "Organ", "Bells"}, []chart.ChartSeries{
					{Name: "Agnes", Values: []float64{8, 6, 9, 4, 7}},
					{Name: "Clare", Values: []float64{5, 9, 6, 8, 3}},
				}, chart.RadarChartOptions{Frame: chart.ChartFrame{Label: "Skills", Height: 180}})
			})

			// Scatter: sized bubbles in two groups; click or drag-box to
			// select points.
			mujicaCard(c, "Scatter", "Bubbled points in two groups; click or drag a box to select.", func() {
				chart.ScatterChart(c, &s.picked, []chart.ScatterGroup{
					{Name: "Monks", Points: []chart.ScatterPoint{{X: 1, Y: 2, Size: 4}, {X: 3, Y: 5, Size: 9}, {X: 6, Y: 4, Size: 1}, {X: 7, Y: 8, Size: 6}}},
					{Name: "Nuns", Points: []chart.ScatterPoint{{X: 2, Y: 7, Size: 16}, {X: 8, Y: 9, Size: 2}, {X: 5, Y: 2, Size: 8}}},
				}, chart.ScatterChartOptions{Frame: chart.ChartFrame{Label: "Choir", Height: 180}, Bubbles: true})
				ui.Text(c, fmt.Sprintf("%d selected", len(s.picked)))
			})

			// Histogram: binned counts over a deterministic sample.
			mujicaCard(c, "Histogram", "Twelve bins over a deterministic sample.", func() {
				sample := make([]float64, 240)
				for i := range sample {
					sample[i] = 50 + 12*math.Sin(float64(i)*1.7)*math.Cos(float64(i)*0.31)
				}
				chart.Histogram(c, sample, chart.HistogramOptions{Frame: chart.ChartFrame{Label: "Ages", Height: 170}, Bins: 12})
			})

			// Box plot: median, quartiles and an outlier per group.
			mujicaCard(c, "Box plot", "Quartiles per group; the Choir sample carries an outlier.", func() {
				chart.BoxPlot(c, mujicaChartsGroups(), chart.BoxPlotOptions{Frame: chart.ChartFrame{Label: "Hymns", Height: 170}})
			})

			// Violin: the same groups as the box plot, smoothed.
			mujicaCard(c, "Violin", "The box-plot groups smoothed into violins.", func() {
				chart.ViolinPlot(c, mujicaChartsGroups(), chart.ViolinPlotOptions{Frame: chart.ChartFrame{Label: "Hymn violins", Height: 170}})
			})

			// Density: overlaid kernel densities per group.
			mujicaCard(c, "Density", "Kernel density per group; legend keys hide a group.", func() {
				chart.DensityPlot(c, mujicaChartsGroups(), chart.DensityPlotOptions{Frame: chart.ChartFrame{Label: "Hymn density", Height: 170}})
			})

			// Waterfall: drops and rises with a subtotal and a total.
			mujicaCard(c, "Waterfall", "Steps with a running subtotal and a final total.", func() {
				chart.WaterfallChart(c, []chart.WaterfallStep{
					{Label: "Tithes", Value: 12}, {Label: "Repairs", Value: -5}, {Label: "Q1", Kind: chart.WaterfallSubtotal},
					{Label: "Alms", Value: 4}, {Label: "Bread", Value: -2}, {Label: "Year", Kind: chart.WaterfallTotal},
				}, chart.WaterfallChartOptions{Frame: chart.ChartFrame{Label: "Ledger", Height: 170}})
			})

			// Funnel: narrowing stages of a pipeline.
			mujicaCard(c, "Funnel", "Narrowing conversion stages.", func() {
				chart.FunnelChart(c, []chart.ChartSlice{
					{Label: "Visitors", Value: 1000}, {Label: "Novices", Value: 400}, {Label: "Vows", Value: 100}, {Label: "Priors", Value: 12},
				}, chart.FunnelChartOptions{Frame: chart.ChartFrame{Label: "Order", Height: 170}})
			})

			// Bullet: a measure against bands and a target mark.
			mujicaCard(c, "Bullet", "One measure, three qualitative bands and a target tick.", func() {
				target := 250.0
				chart.BulletChart(c, 270, chart.BulletChartOptions{
					Frame: chart.ChartFrame{Label: "Alms", Height: 100}, Target: &target, Bands: []float64{150, 225, 300},
				})
			})

			// Sparkline: line, bar and area kinds of one tiny series.
			mujicaCard(c, "Sparkline", "Line, bar and area kinds of one tiny series.", func() {
				vals := []float64{3, 5, 4, 7, 6, 9, 8, 11}
				ui.Row(c).Gap(24).Children(func() {
					for _, kind := range []chart.SparklineKind{chart.SparklineLine, chart.SparklineBar, chart.SparklineArea} {
						chart.Sparkline(c, vals, chart.SparklineOptions{Frame: chart.ChartFrame{Label: "Bells", Height: 48}, Kind: kind})
					}
				})
			})

			// Spark bar: signed mini columns around a zero line.
			mujicaCard(c, "Spark bar", "Signed mini columns around the zero line.", func() {
				chart.SparkBar(c, []float64{3, -2, 4, 1, -3}, chart.SparkBarOptions{Frame: chart.ChartFrame{Label: "Alms", Height: 64}, BarWidth: 10})
			})

			// Trend indicator: signed deltas with arrow and tone.
			mujicaCard(c, "Trend", "Signed deltas; the note anchors the comparison.", func() {
				ui.Row(c).Gap(24).Wrap().Children(func() {
					chart.TrendIndicator(c, 12.5, chart.TrendIndicatorOptions{Precision: 1, Unit: "%", Note: "vs last week"})
					chart.TrendIndicator(c, -3, chart.TrendIndicatorOptions{})
					chart.TrendIndicator(c, 0, chart.TrendIndicatorOptions{})
				})
			})

			// Heatmap: a small matrix with row and column labels.
			mujicaCard(c, "Heatmap", "Four days by five offices; cells shade by value.", func() {
				rows, cols := []string{"Mon", "Tue", "Wed", "Thu"}, []string{"Matins", "Lauds", "Terce", "Vespers", "Compline"}
				values := make([][]float64, len(rows))
				for r := range rows {
					values[r] = make([]float64, len(cols))
					for col := range cols {
						values[r][col] = float64((r*7 + col*5) % 11)
					}
				}
				chart.HeatmapChart(c, rows, cols, values, chart.HeatmapChartOptions{Frame: chart.ChartFrame{Label: "Hours", Height: 170}})
			})

			// Chord: flows between pairs of a small closed group.
			mujicaCard(c, "Chord", "Flows between three services, as a matrix.", func() {
				chart.ChordDiagram(c, []string{"API", "Web", "Batch"}, [][]float64{{0, 5, 2}, {4, 0, 1}, {1, 2, 0}},
					chart.ChordDiagramOptions{Frame: chart.ChartFrame{Label: "Traffic", Height: 190}})
			})

			// Treemap: drill into a branch, Backspace steps back out.
			mujicaCard(c, "Treemap", "Area by value; click drills in, Backspace steps back.", func() {
				chart.Treemap(c, &s.tree, mujicaChartsTree(), chart.TreemapOptions{Frame: chart.ChartFrame{Label: "Rooms", Height: 180}})
			})

			// Sunburst: the same tree as rings.
			mujicaCard(c, "Sunburst", "The treemap's tree as rings, two deep.", func() {
				chart.Sunburst(c, &s.sun, mujicaChartsTree(), chart.SunburstOptions{Frame: chart.ChartFrame{Label: "Rooms", Height: 190}, Depth: 2})
			})

			// Brush: a draggable range whose track previews a series.
			mujicaCard(c, "Brush", "Drag either handle to pick a window of the preview series.", func() {
				chart.ChartBrush(c, &s.brush, chart.ChartBrushOptions{Label: "Window", Max: 100, Values: []float64{1, 4, 2, 7, 3, 8, 4, 9}})
				ui.Text(c, fmt.Sprintf("Window %.0f-%.0f", s.brush[0], s.brush[1]))
			})

			// Empty state: the placeholder charts show while loading or
			// failed, with a working retry.
			mujicaCard(c, "Chart states", "Loading and failed placeholders; retry counts clicks.", func() {
				chart.ChartEmptyState(c, data.DataLoading, chart.ChartEmptyStateOptions{Height: 80})
				if chart.ChartEmptyState(c, data.DataFailed, chart.ChartEmptyStateOptions{Height: 80, Error: "The archive did not answer"}).Changed() {
					s.attempts++
				}
				ui.Text(c, fmt.Sprintf("Retries: %d", s.attempts))
			})

			// Candlestick: the market chart with a type switcher; pan
			// and wheel zoom the visible window.
			mujicaCard(c, "Candlestick", "OHLC candles with pan and zoom; the switcher flips the series shape.", func() {
				chart.ChartTypeSwitcher(c, &s.mtype, chart.ChartTypeSwitcherOptions{})
				chart.CandlestickChart(c, mujicaChartsCandles(), &s.market,
					chart.CandlestickOptions{Frame: chart.ChartFrame{Label: "ACME daily", Height: 250}, Type: s.mtype})
			}).ColumnSpan(2)

			// Sankey: money (or traffic) from sources through a hub.
			mujicaCard(c, "Sankey", "Flows from sources through a hub; hovering a node lights its paths.", func() {
				chart.SankeyChart(c,
					[]chart.SankeyNode{
						{ID: "signups", Label: "Signups"}, {ID: "referrals", Label: "Referrals"},
						{ID: "active", Label: "Active"}, {ID: "churn", Label: "Churn"}, {ID: "paid", Label: "Paid"},
					},
					[]chart.SankeyLink{
						{From: "signups", To: "active", Value: 60}, {From: "referrals", To: "active", Value: 30},
						{From: "active", To: "paid", Value: 50}, {From: "active", To: "churn", Value: 40},
					},
					chart.SankeyChartOptions{Frame: chart.ChartFrame{Label: "Pipeline", Height: 220}})
			}).ColumnSpan(2)

			// Network: a small forced graph; drag nodes, wheel zooms.
			mujicaCard(c, "Network", "A five-node service map under a force layout; drag nodes or the background.", func() {
				chart.NetworkGraph(c, &s.net,
					[]chart.GraphNode{
						{ID: "api", Label: "API", X: 0, Y: 0}, {ID: "db", Label: "DB", X: 10, Y: 0},
						{ID: "cache", Label: "Cache", X: 10, Y: 10}, {ID: "worker", Label: "Worker", X: 0, Y: 10},
						{ID: "queue", Label: "Queue", X: 5, Y: 5},
					},
					[]chart.GraphEdge{
						{From: "api", To: "db"}, {From: "api", To: "cache"},
						{From: "api", To: "queue"}, {From: "queue", To: "worker"}, {From: "worker", To: "db"},
					},
					chart.NetworkGraphOptions{Frame: chart.ChartFrame{Label: "House", Height: 260}, Force: true})
			}).ColumnSpan(2)

			// Parallel coordinates: one axis per measure; drag on an
			// axis to filter rows.
			mujicaCard(c, "Parallel coordinates", "Four singers across three measures; drag on an axis to filter.", func() {
				chart.ParallelCoordinates(c, &s.filters, []string{"Age", "Hymns", "Vigils"},
					[][]float64{{20, 3, 9}, {35, 8, 2}, {50, 5, 5}, {65, 1, 4}},
					chart.ParallelCoordinatesOptions{
						Frame: chart.ChartFrame{Label: "Choir", Height: 230},
						Names: []string{"Agnes", "Clare", "Hild", "Bede"},
					})
			}).ColumnSpan(2)

			// Calendar heatmap: a year of daily counts; the arrow
			// buttons page between years.
			mujicaCard(c, "Calendar heatmap", "A year of daily activity; the arrows page between years.", func() {
				values := map[core.Date]float64{}
				day := core.Date{Year: s.year, Month: time.January, Day: 1}
				for i := range 366 {
					if (i*37)%5 != 0 {
						values[day.AddDays(i)] = float64((i * 13) % 9)
					}
				}
				chart.CalendarHeatmap(c, &s.year, values, chart.CalendarHeatmapOptions{Frame: chart.ChartFrame{Label: "Prayers", Height: 150}})
			}).ColumnSpan(2)

			// From the wider library: three flagship widgets from the
			// domain packages, stacked.
			k := core.Tokens(c)
			mujicaCard(c, "From the wider library", "A chat message, a mail pair and a code viewer from the domain packages.", func() {
				heading := func(t string) {
					ui.Text(c, t).FontSize(11.5).FontWeight(600).TextColor(k.TextMuted)
				}

				// messaging.ChatMessage wraps chat.MessageBubble with
				// author, time and own/right-hand styling.
				heading("Messaging · chat message")
				at := time.Date(2026, 10, 5, 21, 7, 0, 0, time.Local)
				messaging.ChatMessage(c, messaging.ChatEntry{
					ID: "mcharts-m1", Author: "Sakiko", Time: at,
					Text: "The second act starts at nine; the score is in the mail below.",
				}, messaging.ChatMessageOptions{})
				messaging.ChatMessage(c, messaging.ChatEntry{
					ID: "mcharts-m2", Author: "You", Time: at.Add(time.Minute), Text: "Bring the masks.", Own: true,
				}, messaging.ChatMessageOptions{Continued: true})

				// Mail list + reader: selecting or opening a row marks
				// it read; the star toggles in place.
				heading("Messaging · mail list and reader")
				ui.Row(c).Gap(12).AlignItems(ui.Start).Children(func() {
					r := messaging.MailList(c, &s.mails, s.mailItems, messaging.MailListOptions{Height: 190})
					r.Element.Width(280).Shrink(0)
					if r.Opened != "" {
						s.opened = r.Opened
						for i := range s.mailItems {
							if s.mailItems[i].ID == r.Opened {
								s.mailItems[i].Unread = false
								break
							}
						}
					}
					if r.Starred != "" {
						for i := range s.mailItems {
							if s.mailItems[i].ID == r.Starred {
								s.mailItems[i].Starred = !s.mailItems[i].Starred
								break
							}
						}
					}
					messaging.MailReader(c, messaging.Mail{
						From: "Sakiko Togawa", To: []string{"Uika Misumi"}, Cc: []string{"Umiri Yahata"},
						Subject: "Second act", Date: at, Format: messaging.MailMarkdown,
						Body:        "**Rehearsal** at nine. Fittings are on Thursday — see the [venue map](https://example.com).",
						Attachments: []messaging.MailAttachment{{Name: "score.pdf", Size: 2048}},
					})
				})
				ui.Text(c, "Opened: "+s.opened)

				// code.CodeViewer: read-only highlighted source with
				// selection and folding.
				heading("Code · viewer")
				code.CodeViewer(c, []string{
					"func Seal(decree string) int {",
					"\tif decree == \"\" {",
					"\t\treturn 0",
					"\t}",
					"\treturn len(decree)",
					"}",
				}, &s.viewer, code.CodeViewerOptions{Language: "go", Label: "Seal"}).Element.Height(140)
			}).ColumnSpan(2)
		})
	})
}

// mujicaChartsGroups is the sample the box plot, violin and density
// cards share; the Choir group carries an outlier on purpose.
func mujicaChartsGroups() []chart.BoxGroup {
	return []chart.BoxGroup{
		{Name: "Choir", Values: []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 24}},
		{Name: "Nave", Values: []float64{4, 5, 5, 6, 7, 8}},
		{Name: "Crypt", Values: []float64{12, 14, 15, 18, 22, 25}},
	}
}
