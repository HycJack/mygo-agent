package main

// The Mujica Data page: a tour of MujicaUI's data, display, navigation
// and layout packages — one card per component. Everything here is
// styled through the library's own tokens (core.Tokens); the
// dashboard's palette is deliberately ignored. Demo state lives in
// ui.Local under "mdata-" keys, and elements are never held across
// frames.

import (
	"cmp"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/ZacharyZhang-NY/MujicaUI/core"
	"github.com/ZacharyZhang-NY/MujicaUI/data"
	"github.com/ZacharyZhang-NY/MujicaUI/display"
	"github.com/ZacharyZhang-NY/MujicaUI/icons"
	"github.com/ZacharyZhang-NY/MujicaUI/input"
	"github.com/ZacharyZhang-NY/MujicaUI/layout"
	"github.com/ZacharyZhang-NY/MujicaUI/navigation"
)

// mujicaDataPage tours the four structural packages of MujicaUI. The
// app palette arrives for signature parity with the other pages; the
// library's tokens own every color on this route.
func (d *dashboard) mujicaDataPage(c *ui.Context, pal Palette) {
	d.mujicaPage(c, "Mujica Data", "data, display, navigation and layout pieces, all drawn on library tokens", func() {
		mujicaDataDataGroup(c)
		mujicaDataDisplayGroup(c)
		mujicaDataNavigationGroup(c)
		mujicaDataLayoutGroup(c)
	})
}

// mujicaDataPair sets two small cards side by side; each keeps to half
// the page, so the fixed-size demos inside stay within their card.
func mujicaDataPair(c *ui.Context, left, right func()) {
	ui.Grid(c).Columns(2).Gap(12).Children(func() {
		left()
		right()
	})
}

// mujicaDataDataGroup covers the data package: Card, Collapsible,
// Accordion, Statistic, Timeline, DescriptionList, List, DataTable,
// Tree, TreeTable, ImageViewer and Carousel.
func mujicaDataDataGroup(c *ui.Context) {
	k := core.Tokens(c)

	// Card + Collapsible.
	mujicaDataPair(c, func() {
		mujicaCard(c, "Card", "A framed surface with title, decorated variant and footer action.", func() {
			data.Card(c, data.CardOptions{
				Title: "Nightly export", Decorated: true,
				Footer: func() {
					if input.Button(c, "Run now", input.ButtonOptions{Variant: input.Secondary}).Clicked() {
						c.Toast("Export queued")
					}
				},
			}, func() {
				ui.Text(c, "Orders, refunds and payouts, archived at 02:00 local time.")
			}).Width(300)
		})
	}, func() {
		type collState struct{ open bool }
		s := ui.Local(c.Root(), "mdata-collapsible", func() collState { return collState{} })
		mujicaCard(c, "Collapsible", "A single trigger; closed content is never built.", func() {
			if data.Collapsible(c, &s.open, data.CollapsibleOptions{Title: "Deployment log"}, func() {
				ui.Text(c, "12:01 build passed · 12:03 canary at 10% · 12:09 fully rolled out.")
			}).Changed() {
				c.Toast(fmt.Sprintf("log %s", map[bool]string{true: "opened", false: "closed"}[s.open]))
			}
		})
	})

	// Accordion + Statistic.
	mujicaDataPair(c, func() {
		type accState struct{ open []string }
		s := ui.Local(c.Root(), "mdata-accordion", func() accState { return accState{open: []string{"build"}} })
		mujicaCard(c, "Accordion", "Sections whose open IDs the caller owns; one starts open.", func() {
			r := data.Accordion(c, &s.open, []data.AccordionItem{
				{ID: "build", Title: "Build", Content: func() { ui.Text(c, "Compiled in 41s; 218 tests green.") }},
				{ID: "canary", Title: "Canary", Content: func() { ui.Text(c, "Error budget steady at 0.2%.") }},
				{ID: "rollback", Title: "Rollback (unused)", Disabled: true, Content: func() {}},
			}, data.AccordionOptions{})
			if r.Changed() {
				c.Toast(fmt.Sprintf("open sections: %v", s.open))
			}
		})
	}, func() {
		change := 12.5
		mujicaCard(c, "Statistic", "A headline number with unit and signed change.", func() {
			data.Statistic(c, 12840, data.StatisticOptions{
				Title: "Requests", Unit: "per hour", Precision: 0,
				Change: &change, ChangeUnit: "%", ChangeNote: "vs yesterday",
			})
			data.Statistic(c, 99.98, data.StatisticOptions{Title: "Uptime", Suffix: "%", Precision: 2})
		})
	})

	// Timeline + DescriptionList.
	mujicaDataPair(c, func() {
		mujicaCard(c, "Timeline", "Events down a spine; the marker carries the status.", func() {
			data.Timeline(c, []data.TimelineItem{
				{Time: "09:00", Title: "Release 2026.40 cut", Status: data.TimelineDone},
				{Time: "11:30", Title: "Canary promoted", Status: data.TimelineActive,
					Content: "Watching p99 latency for one more hour before full rollout."},
				{Time: "12:05", Title: "eu-west probe failed", Status: data.TimelineError},
				{Time: "12:10", Title: "Retrying probe", Status: data.TimelineWarning},
			})
		})
	}, func() {
		mujicaCard(c, "DescriptionList", "Label/value pairs in columns; values can copy themselves.", func() {
			if i, ok := data.DescriptionList(c, []data.DescriptionItem{
				{Label: "Cluster", Value: "prod-eu-west-1", Copyable: true},
				{Label: "Owner", Value: "platform"},
				{Label: "Version", Value: "2026.40-rc.3", Copyable: true},
				{Label: "Runbook", Value: "wiki/prod/rollout", Span: -1},
			}, data.DescriptionListOptions{Columns: 2}).Copied(); ok {
				c.Toast(fmt.Sprintf("copied item %d", i+1))
			}
		})
	})

	// List on its own row: it wants a fixed viewport to scroll in.
	stages := []string{"ingest", "enrich", "index", "score", "route", "notify", "digest", "archive", "replay"}
	type listState struct{ sel data.ListState[int] }
	ls := ui.Local(c.Root(), "mdata-list", func() listState { return listState{} })
	mujicaCard(c, "List", "A virtualized choice list; arrows move, Enter submits.", func() {
		v := data.List(c, &ls.sel, len(stages), data.ListOptions[int]{
			Key:   func(i int) int { return i },
			Label: func(i int) string { return stages[i] },
		}, func(i int) {
			ui.Text(c, fmt.Sprintf("%s · stage %d", stages[i], i+1)).SingleLine()
		})
		v.Element.Size(340, 170)
		if v.Changed() {
			id, _ := ls.sel.Selected()
			c.Toast("picked stage " + stages[id])
		}
		if v.Submitted() {
			id, _ := ls.sel.Selected()
			c.Toast("opened stage " + stages[id])
		}
	})

	// DataTable with a dozen cached rows; sorting stays local, Enter opens.
	type mdataOrder struct {
		ID, Region string
		Amount     float64
		Paid       bool
	}
	type tableState struct {
		rows  []mdataOrder
		table data.DataTableState[string]
	}
	ts := ui.Local(c.Root(), "mdata-table", func() tableState {
		rows := make([]mdataOrder, 12)
		for i := range rows {
			rows[i] = mdataOrder{
				ID: fmt.Sprintf("ORD-%03d", i+1), Region: [4]string{"eu", "us", "apac", "sa"}[i%4],
				Amount: 120 + float64(i*37%400), Paid: i%3 != 0,
			}
		}
		return tableState{rows: rows}
	})
	mujicaCard(c, "DataTable", "Sortable columns and row choice; a header click reorders locally.", func() {
		cols := []data.DataColumn[mdataOrder]{
			{ID: "id", Title: "Order", Width: 90, Sortable: true, Text: func(r mdataOrder) string { return r.ID },
				Compare: func(a, b mdataOrder) int { return cmp.Compare(a.ID, b.ID) }},
			{ID: "region", Title: "Region", Width: 90, Sortable: true, Text: func(r mdataOrder) string { return r.Region },
				Compare: func(a, b mdataOrder) int { return cmp.Compare(a.Region, b.Region) }},
			{ID: "amount", Title: "Amount", Width: 90, Align: ui.End, Sortable: true,
				Text:    func(r mdataOrder) string { return fmt.Sprintf("$%.2f", r.Amount) },
				Compare: func(a, b mdataOrder) int { return cmp.Compare(a.Amount, b.Amount) }},
			{ID: "status", Title: "Status", Width: 110,
				Text: func(r mdataOrder) string { return map[bool]string{true: "Paid", false: "Pending"}[r.Paid] },
				Cell: func(r mdataOrder) {
					dot, color := "● Paid", k.Success
					if !r.Paid {
						dot, color = "○ Pending", k.Warning
					}
					ui.Text(c, dot).TextColor(color).SingleLine()
				}},
		}
		v := data.DataTable(c, &ts.table, ts.rows, data.DataTableOptions[mdataOrder, string]{
			Columns: cols, Key: func(r mdataOrder) string { return r.ID },
		})
		v.Element.Size(520, 210)
		if v.SortChanged() {
			c.Toast("sorted by " + ts.table.Sort.Column)
		}
		if v.Changed() {
			id, _ := ts.table.Selected()
			c.Toast("selected " + id)
		}
		if v.Submitted() {
			id, _ := ts.table.Selected()
			c.Toast("opened " + id)
		}
	})

	// Tree and TreeTable share one small service map.
	services := map[string][]string{
		"prod": {"api", "web", "worker"}, "staging": {"api"},
		"api": nil, "web": nil, "worker": nil,
	}
	mujicaDataPair(c, func() {
		type treeState struct{ tree data.TreeState[string] }
		s := ui.Local(c.Root(), "mdata-tree", func() treeState { return treeState{} })
		mujicaCard(c, "Tree", "Hierarchy with expand, collapse and keyboard motion.", func() {
			v := data.Tree(c, &s.tree, data.TreeOptions[string]{
				Roots:    []string{"prod", "staging"},
				Children: func(n string) []string { return services[n] },
				Label:    func(n string) string { return n },
			})
			v.Element.Size(300, 160)
			if v.Changed() {
				n, _ := s.tree.Selected()
				c.Toast("tree: " + n)
			}
		})
	}, func() {
		type ttState struct{ table data.TreeTableState[string] }
		s := ui.Local(c.Root(), "mdata-treetable", func() ttState { return ttState{} })
		replicas := map[string]int{"prod": 3, "staging": 1, "api": 6, "web": 2, "worker": 4}
		mujicaCard(c, "TreeTable", "Tree rows plus sortable columns of their own.", func() {
			v := data.TreeTable(c, &s.table, data.TreeTableOptions[string]{
				Roots:    []string{"prod", "staging"},
				Children: func(n string) []string { return services[n] },
				Label:    func(n string) string { return n },
				Columns: []data.TreeColumn[string]{
					{ID: "name", Title: "Service", MinWidth: 120, Cell: func(n string) { ui.Text(c, n).SingleLine() },
						Compare: cmp.Compare[string]},
					{ID: "reps", Title: "Replicas", Width: 84, Align: ui.End,
						Cell:    func(n string) { ui.Text(c, fmt.Sprint(replicas[n])) },
						Compare: func(a, b string) int { return cmp.Compare(replicas[a], replicas[b]) }},
				},
			})
			v.Element.Size(360, 160)
			if v.Changed() {
				n, _ := s.table.Selected()
				c.Toast("row " + n)
			}
		})
	})

	// Three generated paintings shared by the viewer and the carousel.
	pics := ui.Local(c.Root(), "mdata-pics", func() []data.ViewerImage {
		art := func(bg, fg, path string) *ui.SVG {
			return ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="480" height="320"><rect width="480" height="320" fill="` +
				bg + `"/><path d="` + path + `" fill="` + fg + `"/></svg>`))
		}
		return []data.ViewerImage{
			{Source: art("#AD9164", "#7D2034", "M240 40 L380 160 L240 280 L100 160 Z"), Alt: "Rose window"},
			{Source: art("#211C1D", "#BDA172", "M80 280 L80 120 L240 40 L400 120 L400 280 Z"), Alt: "Chapel arch"},
			{Source: art("#F2EBDD", "#651729", "M60 60 H420 V100 H60 Z M60 140 H420 V180 H60 Z"), Alt: "Choir stalls"},
		}
	})
	mujicaDataPair(c, func() {
		type viewerState struct {
			open  bool
			index int
		}
		s := ui.Local(c.Root(), "mdata-viewer", func() viewerState { return viewerState{} })
		mujicaCard(c, "ImageViewer", "Thumbnails open a zoomable, pannable lightbox.", func() {
			ui.Row(c).Gap(10).Children(func() {
				for i, p := range *pics {
					thumb := ui.ButtonBase(c).Label("Open "+p.Alt).Radius(4).Border(1, k.Border).Clip()
					thumb.Children(func() { ui.Image(c, p.Source).Size(110, 72) })
					if thumb.Clicked() {
						s.open, s.index = true, i
					}
				}
			})
			if data.ImageViewer(c, &s.open, &s.index, *pics).Changed() {
				c.Toast("viewing " + (*pics)[s.index].Alt)
			}
		})
	}, func() {
		type carState struct{ index int }
		s := ui.Local(c.Root(), "mdata-carousel", func() carState { return carState{} })
		mujicaCard(c, "Carousel", "Slides step by arrow, dot — or on the autoplay timer.", func() {
			v := data.Carousel(c, &s.index, len(*pics), data.CarouselOptions{
				Label: "Windows", Autoplay: true, Loop: true, Interval: 4 * time.Second,
			}, func(i int) {
				ui.Image(c, (*pics)[i].Source).Grow(1).Label((*pics)[i].Alt)
			})
			v.Element.Size(340, 180)
			if v.Changed() {
				c.Toast("slide: " + (*pics)[s.index].Alt)
			}
		})
	})
}

// mujicaDataDisplayGroup covers the display package: Text (with its
// editable variant), Heading, Icon, Image, Avatar, AvatarGroup, Badge,
// Tag, Link and Kbd.
func mujicaDataDisplayGroup(c *ui.Context) {
	k := core.Tokens(c)

	// Text (with EditableText) + Heading.
	mujicaDataPair(c, func() {
		type txtState struct{ name string }
		s := ui.Local(c.Root(), "mdata-text", func() txtState { return txtState{name: "eu-ingest-7"} })
		mujicaCard(c, "Text", "Selectable copy, line clamping, and in-place editing.", func() {
			display.Text(c, "Velvet curtains fall over the long gallery. Every candle leans toward the open door while the quartet plays on beneath the chandeliers.",
				display.TextOptions{MaxLines: 2})
			display.Text(c, "Order ID: 4821-AX — select and copy me.", display.TextOptions{Selectable: true})
			e := display.EditableText(c, &s.name, display.EditableTextOptions{Label: "Node name"})
			if e.Changed() {
				c.Toast("renamed to " + s.name)
			}
		})
	}, func() {
		mujicaCard(c, "Heading", "Serif headings in three levels, with subtitle and rule.", func() {
			display.Heading(c, "The Grand Hall", display.HeadingOptions{
				Level: 2, Subtitle: "Ceremonies of the winter season", Divider: true,
			})
			display.Heading(c, "Level three", display.HeadingOptions{Level: 3})
		})
	})

	// Icon + Image, the latter showing its error state with retry.
	logo := ui.Local(c.Root(), "mdata-logo", func() *ui.SVG {
		return ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="240" height="160" viewBox="0 0 240 160">` +
			`<rect width="240" height="160" fill="#2B2126"/><path d="M120 40 L150 80 L120 120 L90 80 Z" fill="#7D2034"/>` +
			`<circle cx="120" cy="80" r="8" fill="#F2EBDD"/></svg>`))
	})
	mujicaDataPair(c, func() {
		mujicaCard(c, "Icon", "Named icons in semantic tones; a missing name falls back visibly.", func() {
			ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
				display.Icon(c, "star", display.IconOptions{Size: 20, Tone: display.IconAccent, Label: "Featured"})
				display.Icon(c, "circle-check", display.IconOptions{Size: 20, Tone: display.IconSuccess})
				display.Icon(c, "bell", display.IconOptions{Size: 20, Tone: display.IconWarning})
				display.Icon(c, "trash-2", display.IconOptions{Size: 20, Tone: display.IconDanger})
				display.Icon(c, "moon", display.IconOptions{Size: 20, Tone: display.IconMuted})
				display.Icon(c, "settings", display.IconOptions{Size: 20, Tone: display.IconMuted})
			})
		})
	}, func() {
		mujicaCard(c, "Image", "Bitmap or SVG, fit or crop; failures offer a retry.", func() {
			ui.Row(c).Gap(12).Children(func() {
				display.Image(c, *logo, display.ImageOptions{Width: 130, Height: 96, Crop: true, Alt: "Gothic emblem"})
				if display.Image(c, nil, display.ImageOptions{Width: 130, Height: 96, Err: errors.New("404 not found")}).Retry {
					c.Toast("retrying image load")
				}
			})
		})
	})

	// Avatar + AvatarGroup.
	people := []display.AvatarItem{
		{Name: "Sakiko Togawa"}, {Name: "Uika Misumi"}, {Name: "Mutsumi Wakaba"},
		{Name: "Umiri Yahata"}, {Name: "Nyamu Yutenji"},
	}
	mujicaDataPair(c, func() {
		mujicaCard(c, "Avatar", "Picture, or initials, or a default icon — in that order.", func() {
			ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
				display.Avatar(c, "Sakiko Togawa", display.AvatarOptions{Size: 40})
				display.Avatar(c, "Uika Misumi", display.AvatarOptions{Size: 32})
				display.Avatar(c, "若叶睦", display.AvatarOptions{Size: 28})
				display.Avatar(c, "", display.AvatarOptions{Size: 24})
			})
		})
	}, func() {
		mujicaCard(c, "AvatarGroup", "Overlapping avatars; +N unfolds the full roster.", func() {
			g := display.AvatarGroup(c, people, display.AvatarGroupOptions{Max: 3})
			if g.Toggled {
				c.Toast(fmt.Sprintf("roster %s", map[bool]string{true: "opened", false: "closed"}[g.Open]))
			}
		})
	})

	// Badge + Tag.
	mujicaDataPair(c, func() {
		mujicaCard(c, "Badge", "Counts or words inline, or pinned to a corner.", func() {
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "Inbox")
				display.Badge(c, display.BadgeOptions{Count: 12, Max: 99, Label: "Unread"}, nil)
				display.Badge(c, display.BadgeOptions{Text: "New", Tone: display.BadgeNeutral}, nil)
				display.Badge(c, display.BadgeOptions{
					Count: 3, Tone: display.BadgeDanger, Position: display.BadgeTopRight, Label: "Alerts",
				}, func() { display.Avatar(c, "Uika Misumi", display.AvatarOptions{Size: 36}) })
			})
		})
	}, func() {
		type tagState struct{ tags []string }
		s := ui.Local(c.Root(), "mdata-tags", func() tagState {
			return tagState{tags: []string{"gothic", "baroque", "rococo"}}
		})
		mujicaCard(c, "Tag", "Clickable and closable are separate affordances.", func() {
			ui.Row(c).Gap(8).Wrap().Children(func() {
				for i := 0; i < len(s.tags); i++ {
					t := s.tags[i]
					var v display.TagView
					ui.Box(c).Key(t).Children(func() {
						v = display.Tag(c, t, display.TagOptions{Icon: "star", Clickable: true, Closable: true})
					})
					if v.Clicked {
						c.Toast("filter " + t)
					}
					if v.Closed {
						s.tags = append(s.tags[:i:i], s.tags[i+1:]...)
						i--
						c.Toast("removed " + t)
					}
				}
			})
		})
	})

	// Link + Kbd.
	mujicaDataPair(c, func() {
		mujicaCard(c, "Link", "A URL opens outside; no URL makes it an in-app action.", func() {
			if display.Link(c, "Read the runbook", display.LinkOptions{URL: "https://example.com/runbook"}).Clicked() {
				c.Toast("runbook opened")
			}
			if display.Link(c, "Show the incident timeline", display.LinkOptions{}).Clicked() {
				c.Toast("timeline expanded")
			}
		})
	}, func() {
		mujicaCard(c, "Kbd", "Key caps render platform keys, Command or Ctrl.", func() {
			for _, combo := range [][]string{{"Mod", "K"}, {"Mod", "Shift", "P"}, {"Alt", "Up"}, {"Esc"}} {
				ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
					display.Kbd(c, combo...)
					ui.Text(c, strings.Join(combo, " + ")).FontSize(11.5).TextColor(k.TextMuted)
				})
			}
		})
	})
}

// mujicaDataNavigationGroup covers the navigation package: Tabs,
// SegmentedControl, Breadcrumb, Pagination, Steps, CommandPalette,
// Sidebar, Menubar, NavigationMenu and Toolbar.
func mujicaDataNavigationGroup(c *ui.Context) {
	k := core.Tokens(c)

	// Tabs + SegmentedControl.
	mujicaDataPair(c, func() {
		type tabState struct{ tab int }
		s := ui.Local(c.Root(), "mdata-tabs", func() tabState { return tabState{} })
		mujicaCard(c, "Tabs", "A tab list with panels; arrows move between tabs.", func() {
			if navigation.Tabs(c, &s.tab, []navigation.Tab{
				{Label: "Overview", Panel: func() { ui.Text(c, "Fleet at a glance: 14 up, 1 draining.") }},
				{Label: "Latency", Panel: func() { ui.Text(c, "p50 42ms · p99 190ms, flat for an hour.") }},
				{Label: "Errors", Panel: func() { ui.Text(c, "0.21% of requests, all in one region.") }},
			}, navigation.TabsOptions{Label: "Fleet views"}).Changed() {
				c.Toast(fmt.Sprintf("tab %d", s.tab+1))
			}
		})
	}, func() {
		type segState struct{ view int }
		s := ui.Local(c.Root(), "mdata-segmented", func() segState { return segState{} })
		mujicaCard(c, "SegmentedControl", "One Tab stop; arrow keys move the choice.", func() {
			if navigation.SegmentedControl(c, &s.view, []navigation.Segment{
				{Label: "Day"}, {Label: "Week"}, {Label: "Month"},
			}, navigation.SegmentedControlOptions{Equal: true, Label: "Range"}).Changed() {
				c.Toast(fmt.Sprintf("range %d", s.view+1))
			}
			ui.Text(c, "The chart of the chosen range goes here.").FontSize(11.5).TextColor(k.TextMuted)
		})
	})

	// Breadcrumb + Pagination.
	mujicaDataPair(c, func() {
		type crumbState struct{ depth int }
		s := ui.Local(c.Root(), "mdata-crumbs", func() crumbState { return crumbState{depth: 4} })
		mujicaCard(c, "Breadcrumb", "Long paths fold their middle into a menu.", func() {
			names := []string{"Home", "Cluster", "prod-eu-west-1", "Ingest", "Pools"}
			var path []navigation.BreadcrumbItem
			for i, n := range names[:s.depth] {
				it := navigation.BreadcrumbItem{Label: n}
				if i == 0 {
					it.Icon = icons.Must("house")
				}
				path = append(path, it)
			}
			if i, ok := navigation.Breadcrumb(c, path, navigation.BreadcrumbOptions{MaxItems: 3}).Chosen(); ok {
				s.depth = i + 1
				c.Toast("jumped to " + path[i].Label)
			}
		})
	}, func() {
		type pageState struct {
			page, size int
		}
		s := ui.Local(c.Root(), "mdata-pages", func() pageState { return pageState{page: 1, size: 20} })
		mujicaCard(c, "Pagination", "Page, size and jump field share one Tab stop.", func() {
			if navigation.Pagination(c, &s.page, &s.size, navigation.PaginationOptions{
				Total: 428, PageSizes: []int{10, 20, 50}, Jump: true,
			}).Changed() {
				c.Toast(fmt.Sprintf("page %d of %d", s.page, (428+s.size-1)/s.size))
			}
		})
	})

	// Steps + CommandPalette.
	mujicaDataPair(c, func() {
		type stepState struct{ current int }
		s := ui.Local(c.Root(), "mdata-steps", func() stepState { return stepState{current: 1} })
		mujicaCard(c, "Steps", "Done steps stay clickable; arrows walk the rail.", func() {
			steps := []navigation.Step{
				{Title: "Freeze", Description: "Deploys paused"},
				{Title: "Migrate", Description: "Schema v26"},
				{Title: "Roll out", Description: "10% canary"},
				{Title: "Unfreeze"},
			}
			for i := range steps {
				steps[i].Clickable = i < s.current
			}
			if navigation.Steps(c, &s.current, steps, navigation.StepsOptions{Label: "Release"}).Changed() {
				c.Toast("at " + steps[s.current].Title)
			}
		})
	}, func() {
		type palState struct{ open bool }
		s := ui.Local(c.Root(), "mdata-palette", func() palState { return palState{} })
		mujicaCard(c, "CommandPalette", "A searchable overlay; by button or the app shortcut.", func() {
			if input.Button(c, "Open commands", input.ButtonOptions{Icon: icons.Must("command")}).Clicked() {
				s.open = true
			}
			if id, ok := navigation.CommandPalette(c, &s.open, []navigation.Command{
				{ID: "drain", Label: "Drain node", Group: "Fleet"},
				{ID: "snapshot", Label: "Snapshot database", Group: "Fleet"},
				{ID: "theme", Label: "Toggle dark mode", Group: "View", Keywords: []string{"night"}, Icon: icons.Must("moon")},
			}, navigation.CommandPaletteOptions{}).Chosen(); ok {
				c.Toast("ran " + id)
			}
		})
	})

	// Sidebar + Menubar, each framed so their panels clip in place.
	mujicaDataPair(c, func() {
		type sideState struct{ sel string }
		s := ui.Local(c.Root(), "mdata-sidebar", func() sideState { return sideState{sel: "inbox"} })
		mujicaCard(c, "Sidebar", "A nested source list; sections fold, items show counts.", func() {
			ui.Column(c).Height(170).Border(1, k.Border).Radius(4).Clip().Children(func() {
				if navigation.Sidebar(c, &s.sel, []navigation.SidebarSection{
					{Title: "Operations", Items: []navigation.SidebarItem{
						{ID: "inbox", Label: "Alerts", Icon: icons.Must("inbox"), Count: 3},
						{ID: "starred", Label: "Starred", Icon: icons.Must("star")},
						{Label: "Fleet", Items: []navigation.SidebarItem{
							{ID: "prod", Label: "Production", Icon: icons.Must("folder")},
							{ID: "staging", Label: "Staging", Icon: icons.Must("folder")},
						}},
					}},
					{Title: "Settings", Items: []navigation.SidebarItem{
						{ID: "keys", Label: "API keys", Icon: icons.Must("settings")},
						{ID: "trash", Label: "Trash", Icon: icons.Must("trash-2")},
					}},
				}, navigation.SidebarOptions{Width: 190}).Changed() {
					c.Toast("sidebar: " + s.sel)
				}
			})
		})
	}, func() {
		type menuState struct {
			wrap, ruler bool
			zoom        string
		}
		s := ui.Local(c.Root(), "mdata-menubar", func() menuState { return menuState{wrap: true, zoom: "fit"} })
		mujicaCard(c, "Menubar", "Desktop menus with shortcuts, checks and radios.", func() {
			ui.Column(c).Height(170).Border(1, k.Border).Radius(4).Clip().Children(func() {
				if id, ok := navigation.Menubar(c, []navigation.MenubarMenu{
					{Label: "File", Items: []navigation.MenubarItem{
						{ID: "new", Label: "New dashboard", Shortcut: navigation.MenubarShortcut{Mods: ui.Cmd, Key: ui.KeyN}},
						{ID: "open", Label: "Open…", Shortcut: navigation.MenubarShortcut{Mods: ui.Cmd, Key: ui.KeyO}},
						{Kind: navigation.MenubarSeparator},
						{ID: "close", Label: "Close", Shortcut: navigation.MenubarShortcut{Mods: ui.Cmd, Key: ui.KeyW}},
					}},
					{Label: "View", Items: []navigation.MenubarItem{
						{ID: "wrap", Label: "Word wrap", Kind: navigation.MenubarCheck, Checked: s.wrap},
						{ID: "ruler", Label: "Ruler", Kind: navigation.MenubarCheck, Checked: s.ruler},
						{Kind: navigation.MenubarSeparator},
						{ID: "fit", Label: "Fit", Kind: navigation.MenubarRadio, Checked: s.zoom == "fit"},
						{ID: "actual", Label: "Actual size", Kind: navigation.MenubarRadio, Checked: s.zoom == "actual"},
					}},
				}, navigation.MenubarOptions{}).Chosen(); ok {
					switch id {
					case "wrap":
						s.wrap = !s.wrap
					case "ruler":
						s.ruler = !s.ruler
					case "fit", "actual":
						s.zoom = id
					}
					c.Toast("menu " + id)
				}
				ui.Text(c, fmt.Sprintf("wrap %v · ruler %v · zoom %s", s.wrap, s.ruler, s.zoom)).Padding(10)
			})
		})
	})

	// NavigationMenu + Toolbar.
	mujicaDataPair(c, func() {
		type navState struct {
			router *ui.Router
			last   string
		}
		s := ui.Local(c.Root(), "mdata-navmenu", func() navState {
			return navState{router: ui.NewRouter("/fleet"), last: "/fleet"}
		})
		mujicaCard(c, "NavigationMenu", "A horizontal menu driving an in-page router view.", func() {
			navigation.NavigationMenu(c, s.router, []navigation.NavigationLink{
				{Label: "Fleet", Path: "/fleet", Icon: icons.Must("house")},
				{Label: "Reports", Items: []navigation.NavigationLink{
					{Label: "Latency", Path: "/reports/latency", Description: "Percentiles over time"},
					{Label: "Errors", Path: "/reports/errors", Description: "Rates and budgets"},
				}},
			}, navigation.NavigationMenuOptions{})
			if p := s.router.Path(); p != s.last {
				s.last = p
				c.Toast("navigated to " + p)
			}
			ui.Column(c).Height(56).Border(1, k.Border).Radius(4).Children(func() {
				s.router.View(c, func(r *ui.Route) {
					r.Title(r.Path())
					ui.Text(c, "Page "+r.Path()).Padding(10)
				})
			})
		})
	}, func() {
		mujicaCard(c, "Toolbar", "One Tab stop of actions; overflow folds into a menu.", func() {
			navigation.Toolbar(c, navigation.ToolbarOptions{Label: "Format"}, func() {
				navigation.ToolbarGroup(c, func() {
					for _, name := range []string{"bold", "italic", "underline"} {
						if navigation.ToolbarButton(c, name, navigation.ToolbarButtonOptions{Icon: icons.Must(name)}).Clicked() {
							c.Toast(name + " applied")
						}
					}
				})
				navigation.ToolbarSeparator(c)
				if navigation.ToolbarButton(c, "Copy", navigation.ToolbarButtonOptions{Icon: icons.Must("copy"), ShowLabel: true}).Clicked() {
					mygoClipboard("toolbar copy")
					c.Toast("selection copied")
				}
			})
		})
	})
}

// mujicaDataLayoutGroup covers the layout package: TitleBar,
// StatusBar, Container, Stack, Grid, AspectRatio, Divider, ScrollArea,
// SplitPane and AppShell.
func mujicaDataLayoutGroup(c *ui.Context) {
	k := core.Tokens(c)

	// TitleBar + StatusBar.
	mujicaDataPair(c, func() {
		mujicaCard(c, "TitleBar", "A draggable title row with tool slots either side.", func() {
			ui.Column(c).Height(56).Border(1, k.Border).Radius(6).Clip().Children(func() {
				layout.TitleBar(c, "Ops console", layout.TitleBarOptions{
					Leading: func() {
						if input.Button(c, "", input.ButtonOptions{Variant: input.Ghost, Icon: icons.Must("panel-left"), Label: "Sidebar"}).Clicked() {
							c.Toast("sidebar toggle")
						}
					},
					Trailing: func() {
						if input.Button(c, "Share", input.ButtonOptions{Variant: input.Ghost}).Clicked() {
							c.Toast("dashboard shared")
						}
					},
				})
			})
		})
	}, func() {
		mujicaCard(c, "StatusBar", "Left and right items; narrow bars fold secondary ones.", func() {
			ui.Column(c).Width(360).Children(func() {
				if id := layout.StatusBar(c, layout.StatusBarOptions{
					Left: []layout.StatusItem{
						{ID: "branch", Text: "main"},
						{ID: "sync", Text: "Syncing", ShowProgress: true, Progress: 0.4},
						{ID: "lint", Text: "2 warnings", Tone: layout.StatusItemWarning, Action: true},
					},
					Right: []layout.StatusItem{
						{ID: "enc", Text: "UTF-8", Secondary: true},
						{ID: "bell", Label: "Notifications", Icon: icons.Must("bell"), Action: true},
					},
				}).Clicked; id != "" {
					c.Toast("status " + id)
				}
			})
		})
	})

	// Container + Stack.
	mujicaDataPair(c, func() {
		mujicaCard(c, "Container", "Caps a readable measure and can center it.", func() {
			layout.Container(c, layout.ContainerOptions{MaxWidth: 220, Center: true, Padding: layout.SpaceL}, func() {
				ui.Text(c, "Prose keeps a measure")
				ui.Text(c, "The column stops at 220 DIPs and sits in the middle.").TextColor(k.TextMuted)
			}).Background(k.Surface).Border(1, k.Border).Radius(6)
		})
	}, func() {
		mujicaCard(c, "Stack", "Row or column with density-scaled gap and alignment.", func() {
			layout.Stack(c, layout.StackOptions{
				Horizontal: true, Gap: layout.SpaceM, Align: layout.StackAlignCenter, Wrap: true,
			}, func() {
				for _, n := range []string{"Save", "Preview", "Cancel"} {
					if input.Button(c, n, input.ButtonOptions{Variant: input.Secondary}).Clicked() {
						c.Toast(n + " clicked")
					}
				}
			}).Padding(8)
		})
	})

	// Grid + AspectRatio.
	mujicaDataPair(c, func() {
		mujicaCard(c, "Grid", "Track sizing with spanning cells.", func() {
			cell := func(s string) {
				ui.Box(c).Center().Padding(6).Background(k.Surface).Border(1, k.Border).Radius(4).
					Children(func() { ui.Text(c, s).TextColor(k.TextMuted) })
			}
			layout.Grid(c, layout.GridOptions{Columns: []ui.Track{ui.Fixed(84), ui.Fr(1), ui.Fr(1)}, Gap: layout.SpaceS}, func() {
				layout.GridCell(c, layout.GridCellOptions{ColumnSpan: -1}, func() { cell("header spans all") })
				layout.GridCell(c, layout.GridCellOptions{RowSpan: 2}, func() { cell("row span 2") })
				cell("a")
				cell("b")
				cell("c")
			})
		})
	}, func() {
		mujicaCard(c, "AspectRatio", "Keeps a frame at a fixed width over height.", func() {
			ui.Box(c).Size(260, 170).Border(1, k.Border).Radius(6).Children(func() {
				layout.AspectRatio(c, layout.AspectRatioOptions{Ratio: 16.0 / 9}, func() {
					ui.Box(c).Fill().Center().Background(k.Selection).Children(func() {
						ui.Text(c, "16 : 9").TextColor(k.TextMuted)
					})
				})
			})
		})
	})

	// Divider + ScrollArea.
	mujicaDataPair(c, func() {
		mujicaCard(c, "Divider", "Horizontal or vertical rules, with label or ornament.", func() {
			ui.Text(c, "Overture")
			layout.Divider(c, layout.DividerOptions{Label: "Act II"})
			ui.Text(c, "Aria")
			layout.Divider(c, layout.DividerOptions{Ornament: true})
			ui.Row(c).Height(36).Gap(12).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "Left")
				layout.Divider(c, layout.DividerOptions{Vertical: true})
				ui.Text(c, "Right")
			})
		})
	}, func() {
		type scrollState struct {
			off   ui.ScrollState
			lastY float32
		}
		s := ui.Local(c.Root(), "mdata-scroll", func() scrollState { return scrollState{} })
		mujicaCard(c, "ScrollArea", "A focusable scroll region with keyboard paging.", func() {
			layout.ScrollArea(c, &s.off, layout.ScrollAreaOptions{Label: "Feed"}, func() {
				for i := range 18 {
					ui.Text(c, fmt.Sprintf("%02d  ledger line of the treasury, carried over", i+1)).SingleLine().Padding(3, 8)
				}
			}).Height(140).Background(k.Surface).Border(1, k.Border).Radius(6)
			if s.off.Y != s.lastY {
				s.lastY = s.off.Y
				c.Toast(fmt.Sprintf("scrolled to %.0f", s.off.Y))
			}
		})
	})

	// SplitPane: full width so both panes get room to drag.
	type splitState struct{ split layout.SplitPaneState }
	ss := ui.Local(c.Root(), "mdata-split", func() splitState {
		return splitState{split: layout.SplitPaneState{Size: 160}}
	})
	mujicaCard(c, "SplitPane", "Drag or keyboard the divider; Enter folds a collapsible pane.", func() {
		pane := func(title, body string, bg ui.Color) func() {
			return func() {
				ui.Column(c).Fill().Padding(10).Gap(4).Background(bg).Children(func() {
					ui.Text(c, title).FontSize(13).FontWeight(600)
					ui.Text(c, body).TextColor(core.Tokens(c).TextMuted)
				})
			}
		}
		ui.Column(c).Height(170).Border(1, k.Border).Radius(6).Clip().Children(func() {
			r := layout.SplitPane(c, &ss.split, layout.SplitPaneOptions{Collapsible: layout.SplitPaneFirst},
				pane("Files", "Drag me, or focus and use the arrows.", k.Surface),
				pane("Editor", "Enter folds the left pane.", k.Background))
			r.Element.Fill()
			if r.Resized {
				c.Toast(fmt.Sprintf("split at %.0f", ss.split.Size))
			}
			if r.Toggled {
				c.Toast(fmt.Sprintf("pane %s", map[bool]string{true: "folded", false: "unfolded"}[ss.split.Collapsed]))
			}
		})
	})

	// AppShell: the whole window chrome at a glance, in a fixed frame.
	type shellState struct{ collapsed bool }
	sh := ui.Local(c.Root(), "mdata-shell", func() shellState { return shellState{} })
	mujicaCard(c, "AppShell", "Title bar, folding sidebar, scrolling content and status bar.", func() {
		ui.Column(c).Height(230).Border(1, k.Border).Radius(6).Clip().Children(func() {
			r := layout.AppShell(c, &sh.collapsed, layout.AppShellOptions{
				SidebarWidth: 190,
				Sidebar: func() {
					for _, n := range []string{"Overview", "Alerts", "Fleet", "Reports", "Settings"} {
						ui.Text(c, n).Padding(6, 12)
					}
				},
				Content: func() {
					layout.Container(c, layout.ContainerOptions{}, func() {
						for i := range 14 {
							ui.Text(c, fmt.Sprintf("Entry %d of the operations record", i+1)).Padding(4, 0)
						}
					})
				},
				StatusBar: func() {
					layout.StatusBar(c, layout.StatusBarOptions{
						Left: []layout.StatusItem{{ID: "sync", Text: "Synced 12:04"}},
					})
				},
			})
			if r.Toggled {
				c.Toast(fmt.Sprintf("sidebar %s", map[bool]string{true: "collapsed", false: "expanded"}[sh.collapsed]))
			}
		})
	})
}
