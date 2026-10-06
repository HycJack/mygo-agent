package main

// The Data page: the table, the ten-thousand-row list, the tree, the
// grid view and the outline table — MyGo's data widgets over mock
// dashboard data.

import (
	"fmt"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"
)

func (d *dashboard) dataPage(c *ui.Context, pal Palette) {
	PageTitle(c, pal, "Data", "The toolkit's data surfaces over mock orders and events; every widget here is virtualized.")

	all := makeOrders(200)
	orders := sortOrders(filterOrders(all, d.orderQuery, d.orderStatus), d.orderSort.Column, d.orderSort.Descending)

	// The orders table and its filter row.
	Card(c, pal, "Orders", fmt.Sprintf("%d of %d shown", len(orders), len(all)), func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.SearchField(c, &d.orderQuery).Placeholder("Filter orders…").Label("Filter").Width(220)
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "Status").FontSize(12).TextColor(pal.TextMuted)
				ui.Select(c, &d.orderStatus, append([]string{"all"}, Statuses...)).Width(130)
			})
			ui.Spacer(c)
			if ui.Button(c, "Export selected").Disabled(d.orderChosen.Len() == 0).Clicked() {
				var ids []string
				for id := range d.orderChosen.All() {
					ids = append(ids, id)
				}
				c.ToastAction(fmt.Sprintf("Exported %d orders", len(ids)), "Clear selection", func() {
					d.orderChosen.Clear()
				})
			}
		})
		d.orderAt = min(d.orderAt, max(len(orders)-1, 0))
		d.orderTable.Key = func(i int) any { return orders[i].ID }
		d.orderTable.Label = func(i int) string { return orders[i].Customer }
		d.orderTable.Selected = &d.orderAt
		d.orderTable.Selection = &d.orderChosen
		d.orderTable.Sort = &d.orderSort
		cols := []ui.TableColumn{
			{Title: "Order", Width: 96},
			{Title: "Customer", Sortable: true},
			{Title: "Region", Width: 84, Sortable: true},
			{Title: "Amount", Width: 100, Align: ui.End, Sortable: true},
			{Title: "Status", Width: 96, Sortable: true},
			{Title: "Date", Width: 110, Sortable: true},
		}
		tbl := ui.Table(c, &d.orderTable, cols, len(orders), func(row, col int) {
			o := &orders[row]
			switch col {
			case 0:
				ui.Text(c, o.ID).Font("monospace").FontSize(12).SingleLine()
			case 1:
				ui.EditableText(c, &o.Customer)
			case 2:
				ui.Text(c, o.Region).SingleLine()
			case 3:
				ui.Text(c, formatMoney(o.Amount)).TextAlign(ui.End).Font("monospace").FontSize(12)
			case 4:
				Pill(c, pal, o.Status, statusColor(pal, o.Status))
			case 5:
				ui.Text(c, o.At.Format("Jan 2, 15:04")).FontSize(12).TextColor(pal.TextMuted).SingleLine()
			}
		}).Height(300)
		if tbl.Submitted() && d.orderAt < len(orders) {
			c.Toast("Opened " + orders[d.orderAt].ID)
		}
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Textf(c, "%d selected; Enter opens a row, the top search filters, a header click sorts.", d.orderChosen.Len()).
				FontSize(11).TextColor(pal.TextMuted).Grow(1)
			if ui.Button(c, "Copy order ID").Disabled(d.orderAt >= len(orders)).Clicked() {
				mygoClipboard(orders[d.orderAt].ID)
				c.Toast("Order ID copied")
			}
		})
	})

	// The events list: ten thousand rows, only those in view are built;
	// the top bar's search filters it too.
	Card(c, pal, "Events", "10,000 rows, built only as they show", func() {
		if d.eventQuery != "" {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Textf(c, "Filtering on “%s” (top bar)", d.eventQuery).FontSize(12).TextColor(pal.TextMuted).Grow(1)
				if ui.Button(c, "Clear").Clicked() {
					d.eventQuery = ""
				}
			})
		}
		events := filterEvents(makeEvents(10000), d.eventQuery)
		d.eventAt = min(d.eventAt, max(len(events)-1, 0))
		d.eventRows.Key = func(i int) any { return events[i].Seq }
		d.eventRows.Selected = &d.eventAt
		list := ui.List(c, &d.eventRows, len(events), func(i int) {
			e := events[i]
			ui.Row(c).Height(30).PaddingX(10).Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Box(c).Size(8, 8).Radius(4).Background(statusColor(pal, e.Level)).Shrink(0)
				ui.Text(c, e.Kind).Width(64).FontSize(12).TextColor(pal.TextMuted).SingleLine()
				ui.Text(c, e.What).FontSize(12.5).SingleLine().Grow(1).MinWidth(0)
				ui.Text(c, relativeTime(e.At, d.now)).FontSize(11).TextColor(pal.TextMuted).SingleLine()
			}).ContextMenu(func(m *ui.Menu) {
				if m.Item("Copy event").Chosen() {
					mygoClipboard(fmt.Sprintf("#%d %s", e.Seq, e.What))
					c.Toast("Event copied")
				}
				if m.Item("Mark read").Chosen() {
					c.Toast("Marked read")
				}
			})
		}).Height(260).Border(1, pal.Border).Radius(8).Padding(4)
		if list.Shortcut(0, ui.KeyEnter) && d.eventAt < len(events) {
			c.Toast(fmt.Sprintf("Event #%d", events[d.eventAt].Seq))
		}
	})

	// The storage tree beside the product grid.
	ui.Row(c).Gap(14).AlignItems(ui.Start).Children(func() {
		Card(c, pal, "Files", "workspace storage", func() {
			item := func(path, label string, children func()) {
				var open *bool
				if children != nil {
					o := d.treeOpen[path]
					open = &o
					defer func() { d.treeOpen[path] = o }()
				}
				if ui.TreeItem(c, label, open, children).Selected(d.treeLeaf == path).Clicked() {
					d.treeLeaf = path
				}
			}
			ui.Tree(c, func() {
				item("europe", "europe (2.1 GB)", func() {
					item("europe/berlin", "berlin (1.4 GB)", func() {
						item("europe/berlin/exports", "exports/", nil)
						item("europe/berlin/dumps", "dumps/", nil)
					})
					item("europe/paris", "paris (0.7 GB)", nil)
				})
				item("americas", "americas (3.8 GB)", func() {
					item("americas/new-york", "new-york (2.9 GB)", nil)
					item("americas/sao-paulo", "sao-paulo (0.9 GB)", nil)
				})
				item("backups.tar.gz", "backups.tar.gz (812 MB)", nil)
			})
			ui.Row(c).Gap(6).AlignItems(ui.Center).Margin(8, 0, 0, 0).Children(func() {
				ui.Icon(c, IconDatabase).FontSize(13).TextColor(pal.TextMuted)
				ui.Textf(c, "Selected: %s", d.treeLeaf).FontSize(11).TextColor(pal.TextMuted).SingleLine()
			})
		}).Width(260).Shrink(0)
		Card(c, pal, "Products", "drag to reorder the shelf", func() {
			if d.shelf == nil {
				d.shelf = []string{"Starter", "Team", "Business", "Enterprise", "Seats add-on", "Storage add-on"}
			}
			d.gridState.Key = func(i int) any { return d.shelf[i] }
			d.gridState.Selected = &d.gridAt
			d.gridState.Selection = &d.gridChosen
			d.gridState.Label = func(i int) string { return d.shelf[i] }
			d.gridState.Reorder = func(items []int, to int) {
				d.shelf = reorderShelf(d.shelf, items, to)
			}
			ui.GridView(c, &d.gridState, len(d.shelf), 108, 76, func(i int) {
				ui.Column(c).Grow(1).Margin(6).Padding(8).Radius(8).Gap(6).
					Background(pal.Bg).Border(1, pal.Border).Justify(ui.Center).AlignItems(ui.Center).Children(func() {
					ui.Icon(c, IconPackage).FontSize(16).TextColor(pal.Series[i%len(pal.Series)])
					ui.Text(c, d.shelf[i]).FontSize(11).SingleLine()
				})
			}).Height(180)
			ui.Textf(c, "%d chosen; drag a tile onto another to move it.", d.gridChosen.Len()).FontSize(11).TextColor(pal.TextMuted)
		}).Grow(1)
	})

	// The regions outline: regions and their stores, built as they show.
	Card(c, pal, "Regions", "an outline table: every region opens onto its stores", func() {
		cols := []ui.TableColumn{
			{Title: "Name", Sortable: true},
			{Title: "Stores", Width: 90, Align: ui.End},
			{Title: "Revenue", Width: 120, Align: ui.End},
		}
		ui.OutlineTable(c, &d.outline, cols, outlineRegions(),
			func(item string) []string {
				if strings.HasPrefix(item, "Region ") {
					return outlineStores(item)
				}
				return nil
			},
			func(item string, col int) {
				switch {
				case col == 0:
					ui.Text(c, item).SingleLine()
				case strings.Contains(item, "/store-"):
					// A store row: no count of its own; revenue by its index.
					if col == 1 {
						ui.Text(c, "—").TextAlign(ui.End).TextColor(pal.TextMuted)
					} else {
						i := int(item[len(item)-1] - '0')
						ui.Text(c, formatMoney(18400-float64(i)*1240)).TextAlign(ui.End).Font("monospace").FontSize(12)
					}
				default:
					// A region row: its four stores and their total.
					if col == 1 {
						ui.Text(c, "4").TextAlign(ui.End)
					} else {
						ui.Text(c, formatMoney(float64(len(item))*2670)).TextAlign(ui.End).Font("monospace").FontSize(12)
					}
				}
			},
		).Height(240)
		ui.Text(c, "The arrows open and close the row chosen; type-ahead jumps by name.").FontSize(11).TextColor(pal.TextMuted)
	})
}

// reorderShelf moves the chosen items before index to, the way
// GridState.Reorder asks.
func reorderShelf(shelf []string, items []int, to int) []string {
	moved := make([]string, 0, len(items))
	rest := make([]string, 0, len(shelf))
	at := -1
	for i, v := range shelf {
		if i == to {
			at = len(rest)
		}
		if slices.Contains(items, i) {
			moved = append(moved, v)
		} else {
			rest = append(rest, v)
		}
	}
	if at < 0 {
		at = len(rest)
	}
	return slices.Insert(rest, at, moved...)
}

// outlineRegions names the outline's top rows.
func outlineRegions() []string {
	var out []string
	for _, r := range Regions {
		out = append(out, "Region "+r)
	}
	return out
}

// outlineStores names a region's children.
func outlineStores(region string) []string {
	code := strings.TrimPrefix(region, "Region ")
	out := make([]string, 4)
	for i := range out {
		out[i] = fmt.Sprintf("%s/store-%s-%d", region, strings.ToLower(code), i+1)
	}
	return out
}
