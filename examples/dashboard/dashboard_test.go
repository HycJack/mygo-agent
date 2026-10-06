package main

// The example's tests: the pure data helpers run anywhere; the render
// test draws every page in memory with ui.NewTester, asserts on what it
// shows, and — when MYGO_UI_SHOTS names a directory — writes PNGs of
// each page for a visual audit, the way the agent app screenshots its
// own surfaces.

import (
	"encoding/base64"
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
		d.loggedIn = true
		d.router.Push(pagePath(page))
		tt := ui.NewTester(d.view, 1240, 800)
		tt.Frame()
		assertVisible(t, page, tt)
		if dir != "" {
			writeShot(t, tt, dir, "dashboard-"+lower(page))
		}
	}

	// The shell's own states, for the visual audit: the login screen,
	// the default, the sidebar folded to its rail, and both folded.
	if dir != "" {
		writeShot(t, ui.NewTester(newDashboard().view, 1240, 800), dir, "login")

		d := newDashboard()
		d.loggedIn = true
		tt := ui.NewTester(d.view, 1240, 800)
		tt.Frame()
		writeShot(t, tt, dir, "shell-default")

		d = newDashboard()
		d.loggedIn = true
		d.navOpen = false
		tt = ui.NewTester(d.view, 1240, 800)
		tt.Frame()
		writeShot(t, tt, dir, "shell-sidebar-folded")

		d = newDashboard()
		d.loggedIn = true
		d.navOpen = false
		d.inspector = false
		tt = ui.NewTester(d.view, 1240, 800)
		tt.Frame()
		writeShot(t, tt, dir, "shell-both-folded")
	}

	// Navigation: clicking the sidebar's Data item lands on /data.
	nd := newDashboard()
	nd.loggedIn = true
	nav := ui.NewTester(nd.view, 1240, 800)
	nav.Frame()
	if err := nav.Click("Data"); err != nil {
		t.Fatalf("click the Data item: %v", err)
	}
	nav.Frame()
	if nd.router.Path() != pagePath("Data") {
		t.Fatalf("after clicking Data the router is at %s", nd.router.Path())
	}
	if !nav.HasText("Orders") {
		t.Fatal("the Data page does not show the orders table")
	}
}

// TestFoldedPanels walks the shell's collapse states: both panels open
// at first, ⌘B folds the sidebar to the icon rail, ⌘J folds the activity
// panel, and the rail still navigates. Every fold slides for
// slideDuration, so the test waits the animation out before it clicks.
func TestFoldedPanels(t *testing.T) {
	d := newDashboard()
	d.loggedIn = true
	tt := ui.NewTester(d.view, 1240, 800)
	tt.Frame()
	if !tt.HasText("Analytics") || !tt.HasText("Recent activity") {
		t.Fatal("at start the sidebar and the activity panel should both be open")
	}

	settle := func() {
		tt.Frame()
		time.Sleep(slideDuration + 60*time.Millisecond)
		tt.Frame()
	}

	tt.Key(ui.Cmd, ui.KeyB)
	settle()
	if tt.HasText("Analytics") {
		t.Fatal("⌘B did not fold the sidebar")
	}
	if !tt.HasText("Recent activity") {
		t.Fatal("⌘B folded the activity panel too")
	}
	if err := tt.Click("Controls"); err != nil {
		t.Fatalf("click the Controls rail icon: %v", err)
	}
	tt.Frame()
	if d.router.Path() != pagePath("Controls") {
		t.Fatalf("the rail did not navigate: %s", d.router.Path())
	}

	tt.Key(ui.Cmd, ui.KeyB)
	tt.Key(ui.Cmd, ui.KeyJ)
	settle()
	if tt.HasText("Recent activity") {
		t.Fatal("⌘J did not fold the activity panel")
	}
	if !tt.HasText("Analytics") {
		t.Fatal("⌘B unfolded the sidebar instead of ⌘J folding the panel")
	}

	// The Overview content survives both panels folded.
	tt.Key(ui.Cmd, ui.Key1)
	tt.Frame()
	if !tt.HasText("Revenue") {
		t.Fatal("the Overview content is missing with both panels folded")
	}
}

// assertVisible checks the page's own headline content.
func assertVisible(t *testing.T, page string, tt *ui.Tester) {
	t.Helper()
	seen := map[string]bool{
		"Overview":        tt.HasText("Revenue") && tt.HasText("Traffic sources"),
		"Data":            tt.HasText("Orders") && tt.HasText("Regions"),
		"Controls":        tt.HasText("Profile") && tt.HasText("Ranges"),
		"Overlays":        tt.HasText("Board") && tt.HasText("Motion"),
		"Settings":        tt.HasText("Workspace") && tt.HasText("Danger zone"),
		"Mujica Forms":    tt.HasText("TextInput") && tt.HasText("WeekPicker"),
		"Mujica Data":     tt.HasText("DataTable") && tt.HasText("Timeline"),
		"Mujica Feedback": tt.HasText("Alert") && tt.HasText("Drawer"),
		"Mujica Charts":   tt.HasText("Candlestick") && tt.HasText("From the wider library"),
		"Mujica Agent":    tt.HasText("Conversation") && tt.HasText("PromptComposer"),
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

// TestFakeJWT checks the demo token's shape: three dot-separated
// segments, the first two base64url JSON.
func TestFakeJWT(t *testing.T) {
	token := fakeJWT("ada@acme.dev")
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("fakeJWT: %d segments, want 3: %q", len(parts), token)
	}
	dec := func(s string) string {
		b, err := base64.RawURLEncoding.DecodeString(s)
		if err != nil {
			t.Fatalf("fakeJWT segment %q does not decode: %v", s, err)
		}
		return string(b)
	}
	if h := dec(parts[0]); !strings.Contains(h, `"alg":"HS256"`) {
		t.Errorf("header: %s", h)
	}
	if p := dec(parts[1]); !strings.Contains(p, `"sub":"ada@acme.dev"`) {
		t.Errorf("payload: %s", p)
	}
	if parts[2] == "" {
		t.Error("signature is empty")
	}
}

// TestLoginFlow walks the two ways in: the password form (with its
// validation), an OAuth button, and sign-out back to the card.
func TestLoginFlow(t *testing.T) {
	// The app opens on the login screen; the shell stays hidden.
	d := newDashboard()
	tt := ui.NewTester(d.view, 1240, 800)
	tt.Frame()
	if !tt.HasText("Welcome back") || !tt.HasText("Continue with GitHub") {
		t.Fatal("the app does not open on the login screen")
	}
	if tt.HasText("Revenue") {
		t.Fatal("the shell is visible before signing in")
	}

	// Bad credentials report inline and keep the card up. The tester
	// reaches an input by the geometry beside its field label, the way
	// MyGo's own form tests do.
	focusField := func(label string) {
		r, ok := tt.Find(label)
		if !ok {
			t.Fatalf("no %s field on the card", label)
		}
		tt.ClickAt(r.X+r.W+30, r.Y+r.H/2)
	}
	focusField("Email")
	tt.Type("ada@acme.dev")
	focusField("Password")
	tt.Type("abc")
	tt.Click("Sign in")
	tt.Frame()
	if !tt.HasText("at least 6 characters") {
		t.Fatal("a short password did not report inline")
	}
	if d.loggedIn {
		t.Fatal("a short password signed in")
	}

	// Good credentials land in the shell, token issued.
	focusField("Password")
	tt.Type("hunter02")
	tt.Click("Sign in")
	tt.Frame()
	if !d.loggedIn || d.user != "ada@acme.dev" {
		t.Fatal("the password form did not sign in")
	}
	if d.jwt == "" || len(strings.Split(d.jwt, ".")) != 3 {
		t.Fatalf("no JWT issued: %q", d.jwt)
	}
	if !tt.HasText("Good morning") {
		t.Fatal("the shell did not appear after signing in")
	}

	// An OAuth button signs in with its provider at once (headless: the
	// mock handshake lands synchronously).
	od := newDashboard()
	ot := ui.NewTester(od.view, 1240, 800)
	ot.Frame()
	if err := ot.Click("Continue with WeChat"); err != nil {
		t.Fatalf("click the WeChat button: %v", err)
	}
	ot.Frame()
	if !od.loggedIn || od.user != "ada@acme.dev" {
		t.Fatal("the OAuth button did not sign in")
	}
	if !ot.HasText("Good morning") {
		t.Fatal("the shell did not appear after the OAuth sign-in")
	}

	// Sign out returns to the card, password cleared, email kept.
	nd := newDashboard()
	nd.loggedIn = true
	nd.loginEmail = "kept@acme.dev"
	nd.loginPassword = "hunter02"
	nt := ui.NewTester(nd.view, 1240, 800)
	nt.Frame()
	// The footer menu is a context menu: right-click the account row.
	if err := nt.RightClick("Ada Lovelace"); err != nil {
		t.Fatalf("open the account menu: %v", err)
	}
	nt.ChooseMenuItem("Sign out")
	nt.Frame()
	if nd.loggedIn {
		t.Fatal("sign out kept the session")
	}
	if !nt.HasText("Welcome back") {
		t.Fatal("sign out did not return to the login card")
	}
	if nd.loginPassword != "" {
		t.Fatal("sign out kept the password")
	}
}
