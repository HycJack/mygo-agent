package main

// The example's tests: the pure data helpers run anywhere; the render
// test draws every page in memory with ui.NewTester, asserts on what it
// shows, and — when MYGO_UI_SHOTS names a directory — writes PNGs of
// each page for a visual audit, the way the agent app screenshots its
// own surfaces.

import (
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestFilterOrders(t *testing.T) {
	orders := makeOrders(200)
	if len(orders) != 200 {
		t.Fatalf("makeOrders: got %d orders, want 200", len(orders))
	}
	got := filterOrders(orders, "ada", "all")
	for _, o := range got {
		if !containsLower(o.Customer, "ada") && !containsLower(o.ID, "ada") {
			t.Fatalf("filterOrders(ada): kept %v %v", o.ID, o.Customer)
		}
	}
	byStatus := filterOrders(orders, "", "paid")
	for _, o := range byStatus {
		if o.Status != "paid" {
			t.Fatalf("filterOrders(paid): kept a %s order", o.Status)
		}
	}
	if n := len(filterOrders(orders, "zzz-no-match", "all")); n != 0 {
		t.Fatalf("filterOrders(nomatch): got %d rows, want 0", n)
	}
}

func TestSortOrders(t *testing.T) {
	orders := []Order{
		{ID: "1", Customer: "b", Amount: 20, At: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
		{ID: "2", Customer: "A", Amount: 30, At: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)},
		{ID: "3", Customer: "c", Amount: 10, At: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)},
	}
	got := sortOrders(orders, "Amount", false)
	if got[0].ID != "3" || got[2].ID != "2" {
		t.Fatalf("sortOrders(Amount): got %s, %s, %s", got[0].ID, got[1].ID, got[2].ID)
	}
	got = sortOrders(orders, "Customer", false)
	if got[0].Customer != "A" {
		t.Fatalf("sortOrders(Customer): first is %q, want A", got[0].Customer)
	}
	got = sortOrders(orders, "Date", true)
	if got[0].ID != "2" {
		t.Fatalf("sortOrders(Date desc): first is %s, want 2", got[0].ID)
	}
	got = sortOrders(orders, "nonsense", false)
	if got[0].ID != "1" {
		t.Fatal("sortOrders(unknown): reordered the rows")
	}
}

func TestSeries(t *testing.T) {
	for _, theRange := range rangeNames {
		s, label := seriesFor(theRange)
		if len(s) == 0 || label == "" {
			t.Fatalf("seriesFor(%s): empty series", theRange)
		}
		for _, p := range s {
			if p.Value <= 0 {
				t.Fatalf("seriesFor(%s): %s is %v", theRange, p.Label, p.Value)
			}
		}
	}
	if a, _ := seriesFor("24h"); len(a) != 24 {
		t.Fatalf("seriesFor(24h): %d points, want 24", len(a))
	}
}

func TestFormatHelpers(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{formatMoney(1234567), "$1,234,567"},
		{formatMoney(999), "$999"},
		{formatMoney(-4200), "$-4,200"},
		{formatCompact(1234), "1.2k"},
		{formatCompact(3.4e6), "3.4M"},
		{formatCompact(812), "812"},
	}
	for _, tc := range cases {
		if tc.in != tc.want {
			t.Errorf("got %q, want %q", tc.in, tc.want)
		}
	}
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	rel := func(d time.Duration) string { return relativeTime(now.Add(-d), now) }
	if rel(30*time.Second) != "just now" {
		t.Errorf("relativeTime(now): %q", rel(30*time.Second))
	}
	if rel(12*time.Minute) != "12m ago" {
		t.Errorf("relativeTime(12m): %q", rel(12*time.Minute))
	}
	if rel(5*time.Hour) != "5h ago" {
		t.Errorf("relativeTime(5h): %q", rel(5*time.Hour))
	}
	if rel(24*time.Hour+time.Minute) != "yesterday" {
		t.Errorf("relativeTime(1d): %q", rel(24*time.Hour+time.Minute))
	}
}

func TestReorderShelf(t *testing.T) {
	shelf := []string{"a", "b", "c", "d"}
	got := reorderShelf(shelf, []int{0}, 3) // move a before index 3
	if got[2] != "a" || got[3] != "d" || len(got) != 4 {
		t.Fatalf("reorderShelf: %v", got)
	}
}

func TestFilterEvents(t *testing.T) {
	events := makeEvents(1000)
	if got := filterEvents(events, ""); len(got) != 1000 {
		t.Fatalf("filterEvents(empty): %d, want 1000", len(got))
	}
	if got := filterEvents(events, "deploy"); len(got) == 0 {
		t.Fatal("filterEvents(deploy): nothing matched")
	}
}

// TestRender walks every page in memory and checks that its headline
// content is there; with MYGO_UI_SHOTS set it also writes each page's
// PNG next to the test.
func TestRender(t *testing.T) {
	dir := os.Getenv("MYGO_UI_SHOTS")
	for _, page := range pages {
		d := newDashboard()
		d.router.Push(pagePath(page))
		tt := ui.NewTester(d.view, 1240, 800)
		tt.Frame()
		assertVisible(t, page, tt)
		if dir != "" {
			writeShot(t, tt, dir, "dashboard-"+lower(page))
		}
	}

	// Navigation: clicking the sidebar's Data item lands on /data.
	d := newDashboard()
	tt := ui.NewTester(d.view, 1240, 800)
	tt.Frame()
	if err := tt.Click("Data"); err != nil {
		t.Fatalf("click the Data item: %v", err)
	}
	tt.Frame()
	if d.router.Path() != pagePath("Data") {
		t.Fatalf("after clicking Data the router is at %s", d.router.Path())
	}
	if !tt.HasText("Orders") {
		t.Fatal("the Data page does not show the orders table")
	}
}

// assertVisible checks the page's own headline text.
func assertVisible(t *testing.T, page string, tt *ui.Tester) {
	t.Helper()
	seen := map[string]bool{
		"Overview": tt.HasText("Revenue") && tt.HasText("Traffic sources"),
		"Data":     tt.HasText("Orders") && tt.HasText("Regions"),
		"Controls": tt.HasText("Profile") && tt.HasText("Ranges"),
		"Overlays": tt.HasText("Board") && tt.HasText("Motion"),
		"Settings": tt.HasText("Workspace") && tt.HasText("Danger zone"),
	}[page]
	if !seen {
		t.Errorf("%s: expected content is missing (texts: %v)", page, tt.Texts())
	}
}

func writeShot(t *testing.T, tt *ui.Tester, dir, name string) {
	t.Helper()
	img := tt.Image()
	if img == nil {
		t.Fatalf("%s: no frame rendered", name)
	}
	f, err := os.Create(filepath.Join(dir, name+".png"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

func containsLower(s, sub string) bool {
	return len(s) >= len(sub) && strings.Contains(strings.ToLower(s), strings.ToLower(sub))
}
