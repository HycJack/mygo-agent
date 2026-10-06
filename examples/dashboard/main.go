// Dashboard tours the components MyGo's UI toolkit ships, arranged the
// way a product dashboard would use them — the dark look, the cards and
// the shell are abstracted from this repository's own agent UI. It shows
// the app shell (a router-driven sidebar), KPI cards and Painter-drawn
// charts, virtualized lists and sortable tables, every form control,
// overlays (dialogs, popovers, toasts, drag & drop) and a settings page,
// with live data pushed from a goroutine. Light and dark themes swap at
// runtime through Context.SetTheme.
//
//	go run ./examples/dashboard
package main

import (
	"log"
	"math/rand/v2"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// pages names the router's pages in order; Cmd+1…5 switch among them.
var pages = []string{"Overview", "Data", "Controls", "Overlays", "Settings"}

func pagePath(page string) string { return "/" + lower(page) }

func pageOf(path string) string {
	for _, p := range pages {
		if path == pagePath(p) {
			return p
		}
	}
	return "Overview"
}

func lower(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 'a' - 'A'
		}
	}
	return string(b)
}

// dashboard is the whole app state. The view is a function of it; every
// event handler mutates it and the next frame shows the result.
type dashboard struct {
	win    *mygo.Window
	router *ui.Router

	dark     bool // the theme toggle
	live     []float64
	now      time.Time
	notifs   int
	notes    int
	inbox    int
	crumb    int
	csvFiles []string

	// Overview
	theRange int // 24h / 7d / 30d, an index into rangeNames
	health   [3]float64

	// Data
	orderQuery  string
	orderStatus string
	orderSort   ui.SortOrder
	orderTable  ui.ListState // the orders table's state
	orderAt     int
	orderChosen ui.Selection[string]
	eventQuery  string
	eventRows   ui.ListState
	feedRows    ui.ListState // the Overview activity feed's own list state
	eventAt     int
	treeOpen    map[string]bool
	treeLeaf    string
	shelf       []string
	gridState   ui.GridState
	gridAt      int
	gridChosen  ui.Selection[string]
	outline     ui.OutlineState[string]
	outlineAt   int

	// Controls
	name, email, bio    string
	news                bool
	plan                string
	role                string
	region              string
	city                string
	font                string
	tags                []string
	notifyMail          bool
	notifyDigest        bool
	notifyMentions      bool
	regionNA, regionEU  bool
	budget, quality     float64
	priceLow, priceHigh float64
	copies              float64
	retention           float64
	meeting             time.Time
	tint                ui.Color
	starts              int
	prefTab             int
	sections            [3]bool

	// Settings
	split       float32
	settingsTab int
	wsName      string
	wsURL       string
	advanced    bool
	advanced2   bool

	// Overlays
	dialog, modal, alert, menu bool
	tasks                      []task
}

// rangeNames labels the Overview's range switcher.
var rangeNames = []string{"24h", "7d", "30d"}

// task is one card of the kanban board on the Overlays page.
type task struct {
	name string
	col  int
}

// palette returns the palette of the theme in effect.
func (d *dashboard) palette() Palette {
	if d.dark {
		return CodexPalette()
	}
	return DaylightPalette()
}

func newDashboard() *dashboard {
	d := &dashboard{
		router:      ui.NewRouter("/overview"),
		dark:        true,
		now:         time.Now(),
		notifs:      3,
		notes:       7,
		inbox:       12,
		theRange:    1,
		health:      [3]float64{62, 41, 78},
		orderStatus: "all",
		treeOpen:    map[string]bool{"europe": true},
		treeLeaf:    "europe/berlin",
		name:        "Ada Lovelace",
		email:       "ada@acme.dev",
		role:        "Admin",
		plan:        "Pro",
		region:      "EU",
		font:        "Inter",
		tags:        []string{"go", "native"},
		budget:      3200,
		quality:     75,
		priceLow:    120,
		priceHigh:   380,
		copies:      1,
		retention:   90,
		meeting:     time.Date(2026, 10, 15, 9, 30, 0, 0, time.Local),
		tint:        ui.Hex("#58a6ff"),
		starts:      4,
		sections:    [3]bool{true},
		split:       180,
		wsName:      "Acme Inc",
		wsURL:       "acme.analytics.app",
	}
	d.live = liveSeed()
	return d
}

// view is the whole UI: the shell — sidebar, top bar, routed pages —
// with the palette applied through the theme.
func (d *dashboard) view(c *ui.Context) {
	pal := d.palette()
	c.SetTheme(pal.Theme())
	ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
		d.sidebar(c, pal)
		ui.Column(c).Grow(1).MinWidth(0).Children(func() {
			d.topbar(c, pal)
			d.router.View(c, func(r *ui.Route) {
				ui.Scroll(c).Grow(1).Padding(16, 24, 28).Gap(16).Children(func() {
					switch pageOf(r.Path()) {
					case "Data":
						r.Title("Data")
						d.dataPage(c, pal)
					case "Controls":
						r.Title("Controls")
						d.controlsPage(c, pal)
					case "Overlays":
						r.Title("Overlays")
						d.overlaysPage(c, pal)
					case "Settings":
						r.Title("Settings")
						d.settingsPage(c, pal)
					default:
						r.Title("Overview")
						d.overviewPage(c, pal)
					}
				})
			})
		})
	})
	// ⌘1…5 (Ctrl elsewhere) switch pages.
	for i, p := range pages {
		if c.Shortcut(ui.Cmd, ui.Key1+ui.Key(i)) {
			d.router.Push(pagePath(p))
		}
	}
}

// sidebar is the app's navigation rail: brand, two sections of pages and
// the user footer.
func (d *dashboard) sidebar(c *ui.Context, pal Palette) {
	ui.Column(c).Width(210).Shrink(0).Padding(14, 10).Gap(10).Background(pal.SidebarBG).Children(func() {
		ui.Row(c).Gap(9).AlignItems(ui.Center).Padding(4, 8).Children(func() {
			ui.Box(c).Size(26, 26).Radius(8).Background(pal.Series[0]).Center().Children(func() {
				ui.Icon(c, IconZap).FontSize(15).TextColor(ui.RGB(255, 255, 255))
			})
			ui.Column(c).Gap(0).Children(func() {
				ui.Text(c, "Acme Analytics").FontSize(13).FontWeight(700).SingleLine()
				ui.Text(c, "workspace: acme-inc").FontSize(10.5).TextColor(pal.TextMuted).SingleLine()
			})
		})
		page := pageOf(d.router.Path())
		if ui.Sidebar(c, &page, func() {
			ui.SidebarSection(c, "Analytics", &d.sections[0], func() {
				ui.SidebarItem(c, "Overview", IconDashboard, "Overview")
				ui.SidebarItem(c, "Data", IconDatabase, "Data").Children(func() { ui.Badge(c, "10k") })
			})
			ui.SidebarSection(c, "Toolkit", &d.sections[1], func() {
				ui.SidebarItem(c, "Controls", IconSliders, "Controls")
				ui.SidebarItem(c, "Overlays", IconLayers, "Overlays")
			})
			ui.SidebarSection(c, "Workspace", &d.sections[2], func() {
				ui.SidebarItem(c, "Settings", IconGear, "Settings").Children(func() {
					if d.inbox > 0 {
						ui.Textf(c, "%d", d.inbox).FontSize(10.5).TextColor(pal.TextMuted)
					}
				})
			})
		}).Grow(1).MinHeight(0).Label("Pages").Changed() {
			d.router.Push(pagePath(page))
		}
		// The user footer: avatar, name, and a context menu of account
		// actions.
		foot := ui.Row(c).Gap(8).AlignItems(ui.Center).Padding(6, 8).Radius(8).Cursor(ui.CursorPointer)
		if foot.Hovered() {
			foot.Background(pal.Hover)
		}
		foot.ContextMenu(func(m *ui.Menu) {
			if m.Item("Profile").Chosen() {
				d.router.Push(pagePath("Settings"))
			}
			if m.Item("Sign out").Chosen() {
				c.Toast("Signed out (not really)")
			}
		})
		foot.Children(func() {
			ui.Avatar(c, "Ada Lovelace", nil)
			ui.Column(c).Gap(0).Grow(1).MinWidth(0).Children(func() {
				ui.Text(c, "Ada Lovelace").FontSize(12).FontWeight(600).SingleLine()
				ui.Text(c, "ada@acme.dev").FontSize(10.5).TextColor(pal.TextMuted).SingleLine()
			})
			ui.Icon(c, IconMore).FontSize(14).TextColor(pal.TextMuted)
		})
	})
}

// topbar is the header above the pages: history, breadcrumbs, the global
// search, and the right-hand actions — refresh, notifications, theme.
func (d *dashboard) topbar(c *ui.Context, pal Palette) {
	ui.Toolbar(c, func() {
		ui.BackButton(c, d.router)
		ui.ForwardButton(c, d.router)
		// A crumb click on the workspace name goes back to Overview.
		if ui.Breadcrumbs(c, []string{"Acme", pageOf(d.router.Path())}, &d.crumb).Label("Path").Changed() {
			d.router.Push(pagePath("Overview"))
		}
		ui.Spacer(c)
		ui.SearchField(c, &d.eventQuery).Placeholder("Search events…").Label("Search").Width(210)
		iconToggle(c, pal, "Refresh data", false, IconRefresh, func() {
			d.live = liveSeed()
			c.Toast("Data refreshed")
		})
		bell := ui.ButtonBase(c).Label("Notifications").Tooltip("Notifications").Size(28, 28).Radius(7).Center().Cursor(ui.CursorPointer)
		if bell.Hovered() {
			bell.Background(pal.Hover)
		}
		if bell.Clicked() {
			d.notifs = 0
			c.Toast("3 notifications marked read")
		}
		bell.Children(func() {
			ui.Box(c).Children(func() {
				ui.Icon(c, IconBell).FontSize(15).TextColor(pal.TextMuted)
				if d.notifs > 0 {
					ui.Textf(c, "%d", d.notifs).FontSize(9).FontWeight(700).Padding(0, 4).Radius(8).
						Background(pal.Danger).TextColor(ui.RGB(255, 255, 255)).
						Attach(ui.AnchorTopRight, ui.AnchorCenter).Top(-4)
				}
			})
		})
		iconToggle(c, pal, "Toggle theme", d.dark, IconMoon, func() { d.dark = !d.dark })
		ui.MenuButton(c, "Export", func(m *ui.Menu) {
			for _, as := range []string{"CSV", "JSON", "PDF"} {
				if m.Item("As " + as).Chosen() {
					c.Toast("Report exported as " + as)
				}
			}
		})
	}).Label("Top bar").Padding(8, 20, 0)
}

// liveSeed starts the requests-per-second samples the Overview's live
// card draws, one pushed every second by the goroutine in main.
func liveSeed() []float64 {
	r := rand.New(rand.NewPCG(1, 2))
	samples := make([]float64, 60)
	for i := range samples {
		samples[i] = 400 + 180*r.Float64()
	}
	return samples
}

// mygoClipboard copies to the system clipboard from context-menu actions.
func mygoClipboard(s string) {
	mygo.Clipboard.WriteText(s)
}

func main() {
	d := newDashboard()
	mygo.App.WhenReady(func() {
		d.win = mygo.NewWindow(mygo.WindowOptions{
			Title:    "Acme Analytics — MyGo Dashboard",
			Width:    1240,
			Height:   800,
			MinWidth: 980, MinHeight: 620,
			StateKey: "dashboard-example",
			Content:  ui.View(d.view),
		})
		// Live data: a sample a second and the clock, pushed with
		// Window.Update so the view reads them on the next frame.
		go func() {
			r := rand.New(rand.NewPCG(13, 31))
			for {
				time.Sleep(time.Until(time.Now().Truncate(time.Second).Add(time.Second)))
				v := d.live[len(d.live)-1]
				v = max(120, min(900, v+220*(r.Float64()-0.5)))
				d.win.Update(func() {
					d.now = time.Now()
					d.live = append(d.live[1:], v)
					for i := range d.health {
						d.health[i] = min(100, max(2, d.health[i]+(r.Float64()-0.5)*4))
					}
				})
			}
		}()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
