package main

// The MujicaUI pages (github.com/ZacharyZhang-NY/MujicaUI): a
// court-gothic component library for MyGo that this example embeds to
// show its pieces working inside a real dashboard. core.Use re-themes
// the whole window for as long as one of these pages renders — wine
// accent, serif headings — and every other page resets this app's own
// theme on its frame, so the two designs coexist per route.

import (
	"github.com/egoist/mygo/ui"

	"github.com/ZacharyZhang-NY/MujicaUI/core"
	"github.com/ZacharyZhang-NY/MujicaUI/theme"
)

// mujicaMode maps the dashboard's theme toggle onto MujicaUI's modes.
func mujicaMode(dark bool) core.Mode {
	if dark {
		return core.Dark
	}
	return core.Light
}

// mujicaPage applies the library's theme for this frame and opens the
// page with its heading; body stacks the cards beneath it.
func (d *dashboard) mujicaPage(c *ui.Context, title, subtitle string, body func()) {
	core.Use(c, core.Settings{Mode: mujicaMode(d.dark), Density: theme.Default, Locale: core.ZhCN})
	k := core.Tokens(c)
	ui.Column(c).Fill().Gap(14).Children(func() {
		ui.Column(c).Gap(2).Children(func() {
			ui.Text(c, title).FontSize(20).FontWeight(700)
			ui.Text(c, subtitle).FontSize(12.5).TextColor(k.TextMuted)
		})
		body()
	})
}

// mujicaCard frames one component demo in the library's own surface.
// No Grow: the page sits in the shell's scroll, and a growing child of
// a scroll's content collapses the layout.
func mujicaCard(c *ui.Context, title, caption string, body func()) *ui.Element {
	k := core.Tokens(c)
	return ui.Column(c).Padding(14).Gap(8).MinWidth(0).
		Radius(10).Background(k.Surface).Border(1, k.Border).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, title).FontSize(13).FontWeight(600)
		})
		if caption != "" {
			ui.Text(c, caption).FontSize(11.5).TextColor(k.TextMuted)
		}
		body()
	})
}
