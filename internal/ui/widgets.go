package ui

import "github.com/egoist/mygo/ui"

// ModeNames / EffortNames label the composer's two segmented controls.
var (
	ModeNames   = []string{"Read Only", "Agent", "Full Access"}
	EffortNames = []string{"Low", "Medium", "High"}
)

// ModeLabel is the long form of a mode's name, for display next to a
// task (the header's meta line).
func ModeLabel(mode int) string {
	names := []string{"Read only", "Agent", "Full access"}
	return names[min(mode, 2)]
}

// Segments is a small segmented control over one integer choice, shared
// by the mode selector and the effort selector. The choice arrives as a
// value (the ViewModel is a snapshot); a click reports back through set.
func Segments(c *ui.Context, value int, names []string, set func(int), pal Palette) {
	t := c.Theme()
	chosen := value
	seg := ui.SegmentedBase(c, &chosen, len(names))
	seg.Track.Padding(2).Radius(8).Background(pal.Bg).Border(1, pal.Border).Children(func() {
		for i, name := range names {
			i, name := i, name
			s := seg.Segment(i).Padding(3, 8).Radius(6)
			on := i == value
			if on {
				s.Background(pal.CardHover)
			}
			s.Children(func() {
				col := t.TextMuted
				if on {
					col = t.Text
				}
				ui.Text(c, name).FontSize(11).TextColor(col)
			})
		}
	})
	if chosen != value {
		set(chosen)
	}
}
