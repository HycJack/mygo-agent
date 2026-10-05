package app

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// Layout contracts the user notices by looking, not by crashing: how wide
// the input is, whether the splitter can be dragged, and that dragging it
// stops at its bounds. Each was changed recently, and each is the kind of
// thing that regresses silently — the app still builds, still runs, and
// the box is quietly the wrong width.

// composerSpan measures the composer's content width from two anchors in
// its own row: the first mode segment at the left, Send at the right.
// Find reports window coordinates, and this pair is in the same element,
// so the difference is the row's width and needs no assumption about
// where the pane starts or how the box is centred.
func composerSpan(tt *ui.Tester) (float32, bool) {
	left, ok := tt.Find("Agent")
	if !ok {
		return 0, false
	}
	right, ok := tt.Find("Send")
	if !ok {
		return 0, false
	}
	// The box's own padding is 14 on each side.
	return (right.X + right.W) - left.X + 28, true
}

func composerFixture(t *testing.T) *app {
	t.Helper()
	a := newTestApp(t)
	now := time.Now()
	th := &Thread{ID: "t1", ProjectID: "default", Title: "Layout", Updated: now}
	th.Messages = append(th.Messages, Message{
		ID: "m0", Role: "assistant", Text: "a reply", At: now,
	})
	a.threads = append(a.threads, th)
	a.current = "t1"
	return a
}

// TestComposerIsWiderThanTheMessageColumn pins the width decision: the
// input is for a line you are about to write, the column above it is
// prose meant to be read, and holding both to one measure made the input
// the narrower of the two for no reason.
func TestComposerIsWiderThanTheMessageColumn(t *testing.T) {
	a := composerFixture(t)
	tt := ui.NewTester(a.view, 2400, 800)
	tt.Frame()
	tt.Frame()

	got, ok := composerSpan(tt)
	if !ok {
		t.Fatal("the composer is not on screen")
	}
	// The message column is capped at 880 (internal/ui/thread.go).
	if got <= 880 {
		t.Fatalf("composer is %.0f wide, no wider than the 880 message column", got)
	}
	if got > float32(composerCapForTest) {
		t.Fatalf("composer is %.0f wide, past its cap of %d", got, composerCapForTest)
	}
}

// composerCapForTest mirrors uipkg.composerMaxWidth. The host cannot read
// the unexported constant, and restating it here is the assertion: if the
// cap moves, this test says so rather than passing either way.
const composerCapForTest = 1080

// TestComposerStopsGrowingOnAWideWindow: a cap that is only ever reached
// is not a cap. On a very wide display the box has to stop, or the input
// becomes a banner across the screen.
func TestComposerStopsGrowingOnAWideWindow(t *testing.T) {
	a := composerFixture(t)
	var widths []float32
	for _, w := range []int{1600, 2400, 3200} {
		tt := ui.NewTester(a.view, w, 800)
		tt.Frame()
		tt.Frame()
		got, ok := composerSpan(tt)
		if !ok {
			t.Fatalf("the composer is not on screen at %d", w)
		}
		widths = append(widths, got)
	}
	// Past the cap, a wider window must not make the box wider.
	if widths[1] > widths[0]+2 || widths[2] > widths[1]+2 {
		t.Fatalf("the composer grew with the window: %v", widths)
	}
}

// TestDividerDragResizesTheSidebar is the splitter's whole purpose: press
// on it, drag, and the panel gives way.
func TestDividerDragResizesTheSidebar(t *testing.T) {
	a := newTestApp(t)
	a.navOpen = true
	before := a.navWidth
	tt := ui.NewTester(a.view, 1440, 800)
	tt.Frame()
	tt.Frame()

	// The divider is the 5 DIP strip that starts at the sidebar's
	// trailing edge; pressing 2 DIPs to its left lands in the sidebar
	// itself, which does not drag.
	divX, divY := before+1, float32(400)
	tt.Move(divX, divY)
	tt.Press(divX, divY)
	tt.Move(divX+60, divY)
	tt.Frame()
	tt.Release(divX+60, divY)
	tt.Frame()

	if a.navWidth == before {
		t.Fatalf("dragging the divider right by 60 left the sidebar at %.0f", a.navWidth)
	}
	if a.navWidth < before {
		t.Fatalf("the sidebar shrank to %.0f when dragged right, from %.0f", a.navWidth, before)
	}
}

// TestDividerStaysInsideItsBounds: a splitter dragged off the edge used
// to leave the panel at a negative or absurd width, which is how a layout
// ends up with an invisible sidebar.
func TestDividerStaysInsideItsBounds(t *testing.T) {
	a := newTestApp(t)
	a.navOpen = true
	tt := ui.NewTester(a.view, 1440, 800)
	tt.Frame()
	tt.Frame()

	divX, divY := a.navWidth+1, float32(400)
	tt.Move(divX, divY)
	tt.Press(divX, divY)
	tt.Move(divX+5000, divY)
	tt.Frame()
	tt.Release(divX+5000, divY)
	tt.Frame()
	if a.navWidth > 520 {
		t.Fatalf("the sidebar grew to %.0f, past its 520 bound", a.navWidth)
	}

	tt.Move(a.navWidth+1, divY)
	tt.Press(a.navWidth+1, divY)
	tt.Move(0, divY)
	tt.Frame()
	tt.Release(0, divY)
	tt.Frame()
	if a.navWidth < 180 {
		t.Fatalf("the sidebar shrank to %.0f, under its 180 bound", a.navWidth)
	}
}
