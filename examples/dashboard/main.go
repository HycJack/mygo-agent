// Dashboard tours the components MyGo's UI toolkit ships, arranged the
// way a product dashboard would use them — the dark look, the cards and
// the shell are abstracted from this repository's own agent UI. It shows
// the app shell (a router-driven sidebar that folds to an icon rail, an
// activity panel that slides away on the right), KPI cards and
// Painter-drawn charts, virtualized lists and sortable tables, every
// form control, overlays (dialogs, popovers, toasts, drag & drop) and a
// settings page, with live data pushed from a goroutine. Light and dark
// themes swap at runtime through Context.SetTheme.
//
//	go run ./examples/dashboard
package main

import (
	"fmt"
	"log"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// pages names the router's pages in order; Cmd+1…9 switch among the
// first nine (the tenth has no shortcut). The five Mujica pages embed
// github.com/ZacharyZhang-NY/MujicaUI and showcase its components under
// the library's own theme.
var pages = []string{
	"Overview", "Data", "Controls", "Overlays", "Settings",
	"Mujica Forms", "Mujica Data", "Mujica Feedback", "Mujica Charts",
	"Mujica Agent",
}

// pageIcons names each page's sidebar icon, lucide.dev.
var pageIcons = map[string]*ui.SVG{
	"Overview":        IconDashboard,
	"Data":            IconDatabase,
	"Controls":        IconSliders,
	"Overlays":        IconLayers,
	"Settings":        IconGear,
	"Mujica Forms":    IconPenLine,
	"Mujica Data":     IconTable,
	"Mujica Feedback": IconBellRing,
	"Mujica Charts":   IconChartSpline,
	"Mujica Agent":    IconBot,
}

func pagePath(page string) string {
	// Spaces would make routes read like two segments; routes dash them.
	return "/" + strings.ReplaceAll(lower(page), " ", "-")
}

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

	dark      bool // the theme toggle
	navOpen   bool // the left sidebar: full or the icon rail
	inspector bool // the right activity panel: open or folded away
	live      []float64
	now       time.Time
	notifs    int
	notes     int
	inbox     int
	crumb     int
	csvFiles  []string

	// Session
	loggedIn bool
	user     string // the signed-in identity's email
	jwt      string // the token the (mock) auth server issued
	welcome  string // the toast the shell shows on its first frame

	// Login form
	loginEmail, loginPassword string
	authBusy                  string // which flow is connecting, if any
	authError                 string

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
	sections            [4]bool

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
		navOpen:     true,
		inspector:   true,
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
		sections:    [4]bool{true, false, false, false},
		split:       180,
		wsName:      "Acme Inc",
		wsURL:       "acme.analytics.app",
	}
	d.live = liveSeed()
	return d
}

// view is the whole UI: the login screen until a session lands, then
// the shell — a collapsible sidebar on each side of the routed pages —
// with the palette applied through the theme.
func (d *dashboard) view(c *ui.Context) {
	pal := d.palette()
	c.SetTheme(pal.Theme())
	if !d.loggedIn {
		d.loginView(c, pal)
		return
	}
	if d.welcome != "" {
		msg := d.welcome
		d.welcome = ""
		c.Toast(msg)
	}
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
					case "Mujica Forms":
						r.Title("Mujica Forms")
						d.mujicaFormsPage(c, pal)
					case "Mujica Data":
						r.Title("Mujica Data")
						d.mujicaDataPage(c, pal)
					case "Mujica Feedback":
						r.Title("Mujica Feedback")
						d.mujicaStatePage(c, pal)
					case "Mujica Charts":
						r.Title("Mujica Charts")
						d.mujicaChartsPage(c, pal)
					case "Mujica Agent":
						r.Title("Mujica Agent")
						d.mujicaAgentPage(c, pal)
					default:
						r.Title("Overview")
						d.overviewPage(c, pal)
					}
				})
			})
		})
		d.inspectorPanel(c, pal)
	})
	// ⌘1…5 (Ctrl elsewhere) switch pages; ⌘B folds the sidebar, ⌘J the
	// activity panel.
	for i, p := range pages {
		if c.Shortcut(ui.Cmd, ui.Key1+ui.Key(i)) {
			d.router.Push(pagePath(p))
		}
	}
	if c.Shortcut(ui.Cmd, ui.KeyB) {
		d.navOpen = !d.navOpen
	}
	if c.Shortcut(ui.Cmd, ui.KeyJ) {
		d.inspector = !d.inspector
	}
}

// sidebarWidthsFull and sidebarWidthRail are the sidebar's two widths;
// the transition between them is animated.
const (
	sidebarWidthFull = 212
	sidebarWidthRail = 58
	panelWidth       = 292
	slideDuration    = 200 * time.Millisecond
)

// sidebar is the left navigation: the full rail (brand, sections,
// badges, user footer) or a 58px column of icons. The width animates
// between the two — Size transition — while the content swaps to its
// shape and ClipX keeps the slide clean.
func (d *dashboard) sidebar(c *ui.Context, pal Palette) {
	w := float32(sidebarWidthRail)
	if d.navOpen {
		w = sidebarWidthFull
	}
	body := ui.Column(c).Width(w).Shrink(0).PaddingY(12).Gap(8).Background(pal.SidebarBG).
		ClipX().Transition(ui.ElementTransition{Size: true, Duration: slideDuration, Ease: ui.EaseInOut})
	body.Children(func() {
		if d.navOpen {
			d.sidebarFull(c, pal)
			return
		}
		d.sidebarRail(c, pal)
	})
}

// sidebarFull is the expanded sidebar: brand row with its collapse
// button, the sections, and the user footer.
func (d *dashboard) sidebarFull(c *ui.Context, pal Palette) {
	ui.Row(c).Gap(9).AlignItems(ui.Center).Padding(4, 6, 4, 10).Children(func() {
		ui.Box(c).Size(26, 26).Radius(8).Background(pal.Series[0]).Center().Children(func() {
			ui.Icon(c, IconZap).FontSize(15).TextColor(ui.RGB(255, 255, 255))
		})
		ui.Column(c).Gap(0).Grow(1).MinWidth(0).Children(func() {
			ui.Text(c, "Acme Analytics").FontSize(13).FontWeight(700).SingleLine()
			ui.Text(c, "workspace: acme-inc").FontSize(10.5).TextColor(pal.TextMuted).SingleLine()
		})
		iconToggle(c, pal, "Collapse sidebar (⌘B)", false, IconPanelLeftClose, func() { d.navOpen = false })
	})
	page := pageOf(d.router.Path())
	if ui.Sidebar(c, &page, func() {
		ui.SidebarSection(c, "Analytics", &d.sections[0], func() {
			ui.SidebarItem(c, "Overview", pageIcons["Overview"], "Overview")
			ui.SidebarItem(c, "Data", pageIcons["Data"], "Data").Children(func() { ui.Badge(c, "10k") })
		})
		ui.SidebarSection(c, "Toolkit", &d.sections[1], func() {
			ui.SidebarItem(c, "Controls", pageIcons["Controls"], "Controls")
			ui.SidebarItem(c, "Overlays", pageIcons["Overlays"], "Overlays")
		})
		ui.SidebarSection(c, "Workspace", &d.sections[2], func() {
			ui.SidebarItem(c, "Settings", pageIcons["Settings"], "Settings").Children(func() {
				if d.inbox > 0 {
					ui.Textf(c, "%d", d.inbox).FontSize(10.5).TextColor(pal.TextMuted)
				}
			})
		})
		ui.SidebarSection(c, "MujicaUI", &d.sections[3], func() {
			ui.SidebarItem(c, "Mujica Forms", pageIcons["Mujica Forms"], "Mujica Forms")
			ui.SidebarItem(c, "Mujica Data", pageIcons["Mujica Data"], "Mujica Data")
			ui.SidebarItem(c, "Mujica Feedback", pageIcons["Mujica Feedback"], "Mujica Feedback")
			ui.SidebarItem(c, "Mujica Charts", pageIcons["Mujica Charts"], "Mujica Charts")
			ui.SidebarItem(c, "Mujica Agent", pageIcons["Mujica Agent"], "Mujica Agent")
		})
	}).Grow(1).MinHeight(0).Label("Pages").Changed() {
		d.router.Push(pagePath(page))
	}
	d.userFooter(c, pal)
}

// sidebarRail is the collapsed sidebar: the brand (a click expands),
// the pages as bare icons with tooltips and dot badges, the avatar.
func (d *dashboard) sidebarRail(c *ui.Context, pal Palette) {
	page := pageOf(d.router.Path())
	brand := ui.ButtonBase(c).Label("Expand sidebar").Tooltip("Expand sidebar (⌘B)").
		Size(36, 36).Radius(9).Center().Margin(0, ui.Auto).Cursor(ui.CursorPointer)
	brand.Background(pal.Series[0])
	if brand.Clicked() {
		d.navOpen = true
	}
	brand.Children(func() { ui.Icon(c, IconZap).FontSize(17).TextColor(ui.RGB(255, 255, 255)) })
	for _, p := range pages {
		p := p
		active := p == page
		b := ui.ButtonBase(c).Key("rail-"+p).Label(p).Tooltip(p).Size(36, 36).Radius(9).
			Center().Margin(0, ui.Auto).Cursor(ui.CursorPointer)
		if active {
			b.Background(pal.Sel)
		} else if b.Hovered() {
			b.Background(pal.Hover)
		}
		if b.Clicked() {
			d.router.Push(pagePath(p))
		}
		b.Children(func() {
			ui.Box(c).Children(func() {
				col := pal.TextMuted
				if active {
					col = pal.Text
				}
				ui.Icon(c, pageIcons[p]).FontSize(17).TextColor(col)
				// A dot stands in for the badge the label used to carry.
				if (p == "Data") || (p == "Settings" && d.inbox > 0) {
					ui.Box(c).Size(7, 7).Radius(4).Background(pal.Series[1]).
						Border(1.5, pal.SidebarBG).Attach(ui.AnchorTopRight, ui.AnchorTopRight)
				}
			})
		})
	}
	ui.Box(c).Grow(1)
	expand := ui.ButtonBase(c).Label("Expand sidebar").Tooltip("Expand sidebar (⌘B)").
		Size(36, 36).Radius(9).Center().Margin(0, ui.Auto).Cursor(ui.CursorPointer)
	if expand.Hovered() {
		expand.Background(pal.Hover)
	}
	if expand.Clicked() {
		d.navOpen = true
	}
	expand.Children(func() { ui.Icon(c, IconChevronsRight).FontSize(16).TextColor(pal.TextMuted) })
	ui.Row(c).Margin(0, ui.Auto).Children(func() {
		ui.Avatar(c, "Ada Lovelace", nil)
	})
}

// userFooter is the sidebar's account row: avatar, name, and a context
// menu with the session's actions — copy the JWT, the profile, sign
// out (back to the login screen).
func (d *dashboard) userFooter(c *ui.Context, pal Palette) {
	email := d.user
	if email == "" {
		email = "ada@acme.dev"
	}
	foot := ui.Row(c).Gap(8).AlignItems(ui.Center).Padding(6, 8).Radius(8).Cursor(ui.CursorPointer)
	if foot.Hovered() {
		foot.Background(pal.Hover)
	}
	foot.ContextMenu(func(m *ui.Menu) {
		if m.Item("Copy JWT").Chosen() {
			mygoClipboard(d.jwt)
			c.Toast("JWT copied to the clipboard")
		}
		if m.Item("Profile").Chosen() {
			d.router.Push(pagePath("Settings"))
		}
		if m.Item("Sign out").Chosen() {
			d.loggedIn = false
			d.user, d.jwt = "", ""
			d.loginPassword = ""
			c.Toast("Signed out")
		}
	})
	foot.Children(func() {
		ui.Avatar(c, "Ada Lovelace", nil)
		ui.Column(c).Gap(0).Grow(1).MinWidth(0).Children(func() {
			ui.Text(c, "Ada Lovelace").FontSize(12).FontWeight(600).SingleLine()
			ui.Text(c, email).FontSize(10.5).TextColor(pal.TextMuted).SingleLine()
		})
		ui.Icon(c, IconMore).FontSize(14).TextColor(pal.TextMuted)
	})
}

// inspectorPanel is the right-hand activity panel: live requests, the
// recent activity feed and the team, over every page. Folding it slides
// the column to nothing; the border rides the open state.
func (d *dashboard) inspectorPanel(c *ui.Context, pal Palette) {
	w := float32(0)
	if d.inspector {
		w = panelWidth
	}
	panel := ui.Column(c).Width(w).Shrink(0).Background(pal.SidebarBG).
		ClipX().Transition(ui.ElementTransition{Size: true, Duration: slideDuration, Ease: ui.EaseInOut})
	if d.inspector {
		panel = panel.BorderWidth(0, 0, 0, 1).BorderColor(pal.Border)
	}
	panel.Children(func() {
		if !d.inspector {
			return
		}
		ui.Row(c).Padding(12, 8, 8, 14).Gap(7).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, IconActivity).FontSize(14).TextColor(pal.TextMuted)
			ui.Text(c, "Activity").FontSize(13).FontWeight(600)
			ui.Row(c).Gap(5).AlignItems(ui.Center).Padding(1, 7).Radius(999).
				Background(pal.Success.Alpha(0.14)).Children(func() {
				ui.Box(c).Size(6, 6).Radius(3).Background(pal.Success)
				ui.Text(c, "live").FontSize(10).FontWeight(600).TextColor(pal.Success)
			})
			ui.Spacer(c)
			iconToggle(c, pal, "Hide panel (⌘J)", false, IconPanelRightClose, func() { d.inspector = false })
		})
		ui.Column(c).Padding(0, 10, 12).Gap(10).Grow(1).MinHeight(0).Children(func() {
			// Live requests.
			ui.Column(c).Padding(12).Gap(8).Radius(10).Shrink(0).Background(pal.Card).Border(1, pal.Border).Children(func() {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "Requests / s").FontSize(12).TextColor(pal.TextMuted).Grow(1)
					ui.Text(c, formatCompact(d.live[len(d.live)-1])).FontSize(15).FontWeight(700)
				})
				sparkline(c, d.live, pal.Series[1]).Height(40).Grow(1)
				ui.Textf(c, "peak %s · floor %s", formatCompact(maxOf(d.live)), formatCompact(minOf(d.live))).
					FontSize(10.5).TextColor(pal.TextMuted)
			})
			// The feed takes what height is left.
			ui.Column(c).Padding(12).Gap(8).Radius(10).Grow(1).MinHeight(0).Background(pal.Card).Border(1, pal.Border).Children(func() {
				ui.Text(c, "Recent activity").FontSize(13).FontWeight(600)
				d.activityList(c, pal, 0)
			})
			// Team online.
			ui.Column(c).Padding(12).Gap(8).Radius(10).Shrink(0).Background(pal.Card).Border(1, pal.Border).Children(func() {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "Team online").FontSize(13).FontWeight(600).Grow(1)
					ui.Textf(c, "%d of %d", 5, len(team())).FontSize(11).TextColor(pal.TextMuted)
				})
				ui.Row(c).Gap(6).Wrap().Children(func() {
					for i, m := range team() {
						if i == 5 {
							break
						}
						ui.Box(c).Children(func() {
							ui.Avatar(c, m.Name, nil).Tooltip(m.Name + " · " + m.Role)
							ui.Box(c).Size(9, 9).Radius(5).Background(pal.Success).
								Border(2, pal.Card).Attach(ui.AnchorBottomRight, ui.AnchorBottomRight)
						})
					}
				})
			})
		})
	})
}

// topbar is the header above the pages: the two panel toggles at its
// edges, history, breadcrumbs, the global search, and the actions —
// refresh, notifications, theme, export.
func (d *dashboard) topbar(c *ui.Context, pal Palette) {
	ui.Toolbar(c, func() {
		iconToggle(c, pal, "Toggle sidebar (⌘B)", d.navOpen, IconPanelLeft, func() { d.navOpen = !d.navOpen })
		ui.Box(c).Width(1).Height(18).Background(pal.Border)
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
		iconToggle(c, pal, "Toggle activity panel (⌘J)", d.inspector, IconPanelRight, func() { d.inspector = !d.inspector })
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

// activityList builds the Recent activity feed: one virtualized row per
// event, with an avatar, the text, a level pill and a relative time. A
// fixed height fills it out; zero grows it into what height is left.
func (d *dashboard) activityList(c *ui.Context, pal Palette, fixed float32) *ui.Element {
	events := makeEvents(40)
	labels := map[string]string{"deploy": "deploy", "alert": "alert", "signup": "signup", "payment": "billing"}
	d.feedRows.Key = func(i int) any { return events[i].Seq }
	list := ui.List(c, &d.feedRows, len(events), func(i int) {
		e := events[i]
		ui.Row(c).Key(e.Seq).Padding(7, 4).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Avatar(c, eventActor(e.Seq), nil)
			ui.Column(c).Gap(0).Grow(1).MinWidth(0).Children(func() {
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					ui.Text(c, labels[e.Kind]).FontSize(11).FontWeight(700).TextColor(pal.TextMuted)
					ui.Text(c, e.What).FontSize(12.5).SingleLine().Grow(1).MinWidth(0)
				})
				ui.Text(c, relativeTime(e.At, d.now)).FontSize(10.5).TextColor(pal.TextMuted)
			})
			Pill(c, pal, e.Level, statusColor(pal, e.Level))
		}).ContextMenu(func(m *ui.Menu) {
			if m.Item("Copy details").Chosen() {
				mygoClipboard(fmt.Sprintf("%s: %s", e.Kind, e.What))
			}
			if m.Item("Copy time").Chosen() {
				mygoClipboard(e.At.Format(time.RFC3339))
			}
		})
	}).Padding(4).Gap(0)
	if fixed > 0 {
		return list.Height(fixed)
	}
	return list.Grow(1).MinHeight(0)
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
