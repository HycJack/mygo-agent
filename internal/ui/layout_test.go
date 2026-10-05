package ui

import (
	"fmt"
	"math"
	"testing"

	"github.com/egoist/mygo/ui"
)

// These two are geometry claims about what the user sees, and neither is
// visible in the code that produces it: the rail's dashes line up on
// their left edge only if the bar is anchored rather than centred, and
// the home panel stops growing only if its cap is the one the panel
// obeys. Both are measured off a rendered frame instead, because a test
// that restates the constant proves nothing about the pixels.

// stubAppActs implements Actions so a shared view can be rendered with
// no host behind it.
type stubAppActs struct{}

func (*stubAppActs) Send()                    {}
func (*stubAppActs) Stop()                    {}
func (*stubAppActs) SetDraft(string)          {}
func (*stubAppActs) SetMode(int)              {}
func (*stubAppActs) SetEffort(int)            {}
func (*stubAppActs) PickModel(string, string) {}
func (*stubAppActs) OpenSettings(string)      {}
func (*stubAppActs) SaveConfig()              {}

// paintedRun is one row's painted extent: where it starts, and how wide.
type paintedRun struct{ at, x, w int }

// dashBands finds the rail's painted rows: the bands of dashHit rows that
// carry ink, each reported as where its ink starts and how wide it is.
// The rail's column is painted the same colour as the frame around it, so
// "ink" is simply "not the background".
func dashBands(t *testing.T, items []RailItem) []paintedRun {
	t.Helper()
	pal := CodexPalette()
	colors := Colors{Text: pal.Text, TextMuted: pal.TextMuted, Border: pal.Border,
		Surface: pal.Card, Active: pal.Text}
	h := dashHit * len(items)
	// The column carries the background itself and fills the window, so
	// every row outside a dash is the background and cannot be read as a
	// dash that happens to be wider than the rail.
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Background(pal.Bg).Children(func() {
			ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
				AnchorRail(c, items, colors, nil)
			})
		})
	}, 40, h)
	img := tt.Image()
	bg := img.RGBAAt(1, 1)

	var bands []paintedRun
	for band := range len(items) {
		run := paintedRun{at: band, x: -1}
		for y := band * dashHit; y < (band+1)*dashHit && y < img.Rect.Dy(); y++ {
			for x := 0; x < img.Rect.Dx(); x++ {
				if img.RGBAAt(x, y) == bg {
					continue
				}
				if run.x < 0 {
					run.x = x
				}
				run.w = x - run.x + 1
			}
		}
		if run.x >= 0 {
			bands = append(bands, run)
		}
	}
	if len(bands) != len(items) {
		t.Fatalf("painted %d of %d dashes: %+v", len(bands), len(items), bands)
	}
	return bands
}

// TestAnchorRailDashesShareOneLeftEdge: the rail is a ruler, and a ruler
// whose marks slide sideways as they change length is not a ruler. Every
// dash must start at the same x no matter how long it is — the wave grows
// and shrinks from that one edge.
func TestAnchorRailDashesShareOneLeftEdge(t *testing.T) {
	items := make([]RailItem, 7)
	for i := range items {
		items[i] = RailItem{
			ID:      fmt.Sprintf("m%d", i),
			Preview: fmt.Sprintf("reply %d", i),
			Active:  i == 0,
		}
	}
	bands := dashBands(t, items)

	for i, b := range bands {
		if b.x != bands[0].x {
			t.Errorf("dash %d starts at x=%d, but the rail's edge is x=%d: "+
				"the bar is centred, so its left edge moves with its width", i, b.x, bands[0].x)
		}
	}
}

// TestAnchorRailTapersOnBothSidesOfTheReader: the wave falls off toward
// both sides of the dash the reader is on. It used to fall off only to
// the right, which left every dash behind the reader the same length —
// a flat block that said nothing about how far back they were, on the
// side of the rail the reader had just come from.
func TestAnchorRailTapersOnBothSidesOfTheReader(t *testing.T) {
	const active = 5 // far enough in to have a left side with room on it
	items := make([]RailItem, 11)
	for i := range items {
		items[i] = RailItem{
			ID:      fmt.Sprintf("m%d", i),
			Preview: fmt.Sprintf("reply %d", i),
			Active:  i == active,
		}
	}
	bands := dashBands(t, items)
	peak := bands[active]

	// The falloff reaches dashFall dashes either side and then stops: a
	// dash inside it is shorter than the one nearer the reader, and a
	// dash beyond it sits at the base length. Both sides, or the side
	// the reader just came from is a flat block.
	side := func(name string, at func(k int) int, n int) {
		for k := 1; k <= n; k++ {
			cur := bands[at(k)]
			if prev := bands[at(k-1)]; k <= int(dashFall) {
				if cur.w >= prev.w {
					t.Errorf("%s: dash %d is %dpx and dash %d nearer the reader is %dpx: "+
						"the wave does not taper — %+v", name, at(k), cur.w, at(k-1), prev.w, bands)
				}
			} else if int(cur.w) != int(dashMin) {
				t.Errorf("%s: dash %d is %dpx, %d out past the %v-dash falloff, so it should "+
					"sit at the %v base — %+v", name, at(k), cur.w, k, dashFall, dashMin, bands)
			}
		}
	}
	side("behind the reader", func(k int) int { return active - k }, active)
	side("ahead of the reader", func(k int) int { return active + k }, len(bands)-1-active)
	for i, b := range bands {
		if i != active && b.w > peak.w {
			t.Errorf("dash %d is %dpx, longer than the reader's %dpx: %+v", i, b.w, peak.w, bands)
			break
		}
	}
}

// TestAnchorRailDashesHoldTheirEdgeWhileTheWaveGlides: the left edge is
// a property of the rail, not of one settled frame. The widths ease
// toward their targets over several frames, so a rail that only lined up
// once the animation had finished would drift for the whole glide.
func TestAnchorRailDashesHoldTheirEdgeWhileTheWaveGlides(t *testing.T) {
	items := make([]RailItem, 5)
	for i := range items {
		items[i] = RailItem{ID: fmt.Sprintf("m%d", i), Preview: fmt.Sprintf("reply %d", i)}
	}
	pal := CodexPalette()
	colors := Colors{Text: pal.Text, TextMuted: pal.TextMuted, Border: pal.Border,
		Surface: pal.Card, Active: pal.Text}
	h := dashHit * len(items)
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Background(pal.Bg).Children(func() {
			ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
				AnchorRail(c, items, colors, nil)
			})
		})
	}, 40, h)
	img := tt.Image()
	bg := img.RGBAAt(1, 1)

	// The wave starts at zero width and eases in, so the early frames are
	// the ones where a centred bar would be caught mid-drift.
	first := -1
	for i := 1; i < 6; i++ {
		tt.SetSize(40+i, h)
		img = tt.Image()
		for band := range len(items) {
			x := -1
			for y := band * dashHit; y < (band+1)*dashHit && y < img.Rect.Dy(); y++ {
				for px := 0; px < img.Rect.Dx(); px++ {
					if img.RGBAAt(px, y) == bg {
						continue
					}
					if x < 0 {
						x = px
					}
				}
			}
			if x < 0 {
				continue
			}
			if first < 0 {
				first = x
			}
			if x != first {
				t.Fatalf("frame %d, dash %d starts at x=%d but the rail's left edge is x=%d",
					i, band, x, first)
			}
		}
	}
}

// TestHomePanelStopsAtItsCap: the home panel must not grow with the
// window. A cap that a wide display could push past is no cap at all, so
// the widest painted row is measured on a narrow window and on a wide one
// and has to come out the same.
func TestHomePanelStopsAtItsCap(t *testing.T) {
	pal := CodexPalette()
	width := func(winW int) int {
		t.Helper()
		vm := &ViewModel{Pal: pal, Mode: 1, Model: "gpt-5", ProviderID: "openai",
			Providers: []ProviderVM{{ID: "openai", Name: "OpenAI", Models: []string{"gpt-5"}}}}
		tt := ui.NewTester(func(c *ui.Context) {
			Home(c, vm, &stubAppActs{}, []string{"Fix the parser", "Write the docs"})
		}, winW, 900)
		img := tt.Image()
		bg := img.RGBAAt(2, 2)
		best := 0
		for y := range img.Rect.Dy() {
			lo, hi := -1, -1
			for x := range img.Rect.Dx() {
				if img.RGBAAt(x, y) == bg {
					continue
				}
				if lo < 0 {
					lo = x
				}
				hi = x
			}
			if lo >= 0 && hi-lo+1 > best {
				best = hi - lo + 1
			}
		}
		if best == 0 {
			t.Fatalf("window %d painted nothing at all", winW)
		}
		return best
	}

	narrow, wide := width(1400), width(2200)
	if narrow != wide {
		t.Fatalf("the panel is %dpx wide on a 1400 window and %dpx on a 2200 one: "+
			"it is taking its width from the window, not from its cap", narrow, wide)
	}
	// A cap that the panel may reach is a panel that has become a bar.
	// 880 is the thread's message column, and the home panel is meant to
	// sit inside that measure rather than run past it: it is a centred
	// dialog, not the input at the foot of a conversation. Measured off
	// the frame, so a constant raised past this fails here.
	const messageColumn = 880
	if narrow > messageColumn {
		t.Errorf("the home panel paints %dpx, wider than the %dpx conversation column: "+
			"it has stopped reading as a dialog", narrow, messageColumn)
	}
}

// TestHomePanelIsCentredInTheWindow: a welcome panel is centred, so the
// window above it and the window below it are the same height. Nothing in
// the layout says "centred" — a container that sizes its content by the
// content hands the panel no surplus to be centred in, and it ends up
// pinned under the title bar with the rest of the window empty — so it
// has to be measured off the frame.
//
// Only windows the panel comfortably fits are measured. Shorter than that
// the panel anchors to its top padding and lets the bottom go, which is
// the one behaviour worth keeping when the composer would otherwise land
// off-screen, and both a scroll container and a growing column do it.
func TestHomePanelIsCentredInTheWindow(t *testing.T) {
	pal := CodexPalette()
	for _, winH := range []int{820, 620, 520} {
		vm := &ViewModel{Pal: pal, Mode: 1, Model: "gpt-5", ProviderID: "openai",
			Providers: []ProviderVM{{ID: "openai", Name: "OpenAI", Models: []string{"gpt-5"}}}}
		tt := ui.NewTester(func(c *ui.Context) {
			Home(c, vm, &stubAppActs{}, []string{"Fix the parser", "Write the release notes"})
		}, 1440, winH)
		img := tt.Image()
		bg := img.RGBAAt(2, 2)
		top, bot := -1, -1
		for y := range img.Rect.Dy() {
			for x := range img.Rect.Dx() {
				if img.RGBAAt(x, y) == bg {
					continue
				}
				if top < 0 {
					top = y
				}
				bot = y
			}
		}
		if top < 0 {
			t.Fatalf("window %d painted nothing at all", winH)
		}
		above, below := top, img.Rect.Dy()-1-bot
		if math.Abs(float64(above-below)) > 2 {
			t.Errorf("window %d: %dpx above the panel and %dpx below it (painted y %d..%d) — "+
				"it is not centred", winH, above, below, top, bot)
		}
	}
}

// railW is the width the rail takes beside a thread.
const railW = 30

// peakDash returns the index of the rail's longest dash: the wave centres
// on the dash the reader is on, so the widest one is where the rail says
// they are. Each dash is its own run of inked rows — the bar is 3px tall
// in a 14px row, so the runs do not touch — and the rail is centred
// vertically, so the runs are found by scanning rather than by assuming
// which row the first dash starts on.
func peakDash(t *testing.T, tt *ui.Tester) int {
	t.Helper()
	img := tt.Image()
	// The view paints its own background across the whole frame, so the
	// corner is a safe sample of it.
	bg := img.RGBAAt(0, 0)
	rows := make([]int, img.Rect.Dy())
	for y := range rows {
		lo, hi := -1, -1
		for x := range railW {
			if img.RGBAAt(x, y) == bg {
				continue
			}
			if lo < 0 {
				lo = x
			}
			hi = x
		}
		if lo >= 0 {
			rows[y] = hi - lo + 1
		}
	}
	best, bestW, idx := -1, 0, -1
	for y := 0; y < len(rows); y++ {
		if rows[y] == 0 {
			continue
		}
		w := 0
		for y < len(rows) && rows[y] > 0 {
			if rows[y] > w {
				w = rows[y]
			}
			y++
		}
		idx++
		if w > bestW {
			best, bestW = idx, w
		}
	}
	return best
}

// TestAnchorRailPointsAtTheMessageTheReaderIsOn: the rail's peak is where
// the reader is, and at the end of a transcript that is the last message
// — not the top row of the viewport. The list settles its tail
// End-aligned, so a jump to the last message leaves the rows above it at
// the top, and lighting that top row pointed the rail at an earlier
// message than the one just jumped to, while the last one sat right there
// on screen with its dash dark.
func TestAnchorRailPointsAtTheMessageTheReaderIsOn(t *testing.T) {
	const n = 12
	st := &ui.ListState{}
	vm := &TranscriptVM{List: st, Md: NewMdCache(), Pal: CodexPalette()}
	for i := range n {
		vm.Messages = append(vm.Messages, MessageVM{
			ID:   fmt.Sprintf("m%d", i),
			Role: "user",
			Items: []ItemVM{{Kind: ItemText,
				Text: fmt.Sprintf("prompt number %d, long enough to make this row tall", i)}},
		})
	}
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(CodexTheme())
		ui.Column(c).Fill().Background(vm.Pal.Bg).Children(func() {
			Transcript(c, vm, &stubActs{})
		})
	}, 900, 420)
	tt.Frame()

	// The contract: the rail's peak is the row the list says the reader
	// is on — the last row at the end of the transcript, the first row
	// anywhere else. Which row that is for a given scroll is the
	// toolkit's business, not this test's, so it is read rather than
	// hard-coded; the one thing pinned outright is that at the end the
	// peak is the last message.
	for _, c := range []struct {
		name   string
		atEnd  bool
		scroll func()
	}{
		{"opened at the end", true, nil},
		{"scrolled into the middle", false, func() { st.ScrollTo(5, ui.Start) }},
		{"back at the end", true, func() { st.ScrollToEnd() }},
	} {
		if c.scroll != nil {
			c.scroll()
			tt.Frame()
			tt.Frame()
		}
		first, last := st.Visible()
		got := peakDash(t, tt)
		want := first
		if c.atEnd {
			want = last
		}
		if got != want {
			t.Errorf("%s: the rail's peak is dash %d, want %d (first=%d last=%d atEnd=%v)",
				c.name, got, want, first, last, st.AtEnd())
		}
		if c.atEnd {
			if got != n-1 {
				t.Errorf("%s: the rail's peak is dash %d, want %d (the last message)",
					c.name, got, n-1)
			}
			// The bug: at the end the two differ, and lighting the top
			// row pointed at a message several turns back.
			if first == last {
				t.Fatalf("%s: first and last are both %d, so this case cannot tell the "+
					"reading position from the top row — make the transcript taller",
					c.name, first)
			}
		}
	}
}
