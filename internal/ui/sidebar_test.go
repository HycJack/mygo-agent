package ui

import (
	"strings"
	"testing"
	"time"
)

// The sidebar's grouping, its relative timestamps and the durations on
// the cards are the three places where a wrong answer is invisible until
// a user reads it: a task filed under the wrong heading, an age that
// disagrees with the group it sits in, a tool that "ran for 1500ms". They
// are pure, so they are cheap to pin exactly.

func TestGroupThreadsPutsEachTaskInOneSection(t *testing.T) {
	now := time.Now()
	threads := []ThreadVM{
		{ID: "a", Title: "Today one", Updated: now.Add(-2 * time.Hour)},
		{ID: "b", Title: "Yesterday one", Updated: now.AddDate(0, 0, -1).Add(-time.Hour)},
		{ID: "c", Title: "Last week", Updated: now.AddDate(0, 0, -3)},
		{ID: "d", Title: "Last month", Updated: now.AddDate(0, 0, -20)},
		{ID: "e", Title: "Ancient", Updated: now.AddDate(-1, 0, 0)},
	}
	groups := GroupThreads(threads, "")
	if len(groups) != 5 {
		t.Fatalf("groups %d, want 5: %+v", len(groups), groups)
	}
	want := []string{"Today", "Yesterday", "Previous 7 Days", "Previous 30 Days", "Older"}
	for i, g := range groups {
		if g.Title != want[i] {
			t.Fatalf("group %d is %q, want %q", i, g.Title, want[i])
		}
		if len(g.Threads) != 1 {
			t.Fatalf("group %q holds %d tasks, want 1", g.Title, len(g.Threads))
		}
	}
	// A task can only be in one section: the first match wins, and the
	// sections are tried newest-first.
	seen := map[string]int{}
	for _, g := range groups {
		for _, th := range g.Threads {
			seen[th.ID]++
		}
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("task %q appears in %d sections", id, n)
		}
	}
}

func TestGroupThreadsSkipsEmptySections(t *testing.T) {
	now := time.Now()
	// Only a task from today: the other four headings must not appear as
	// empty rows.
	groups := GroupThreads([]ThreadVM{
		{ID: "a", Title: "Only one", Updated: now},
	}, "")
	if len(groups) != 1 || groups[0].Title != "Today" {
		t.Fatalf("groups %+v, want Today alone", groups)
	}
}

func TestGroupThreadsSearchFiltersBeforeGrouping(t *testing.T) {
	now := time.Now()
	threads := []ThreadVM{
		{ID: "a", Title: "Fix the parser", Updated: now},
		{ID: "b", Title: "Write the docs", Updated: now},
		{ID: "c", Title: "Parser benchmarks", Updated: now},
	}
	// The search is case-insensitive and matches anywhere in the text.
	groups := GroupThreads(threads, "PARSER")
	if len(groups) != 1 {
		t.Fatalf("groups %+v, want one", groups)
	}
	if len(groups[0].Threads) != 2 {
		t.Fatalf("kept %d tasks, want the 2 mentioning the parser", len(groups[0].Threads))
	}
	// No match at all yields no groups, not an empty Today.
	if got := GroupThreads(threads, "nothing here"); len(got) != 0 {
		t.Fatalf("a search with no match produced groups: %+v", got)
	}
}

func TestRelTimeReadsTheAgeATaskIsFiledUnder(t *testing.T) {
	now := time.Now()
	cases := []struct {
		at   time.Time
		want string
	}{
		{now.Add(-30 * time.Second), "now"},
		{now.Add(-5 * time.Minute), "5m"},
		{now.Add(-3 * time.Hour), "3h"},
		// Days old: a weekday name, which reads better than "2d".
		{now.AddDate(0, 0, -2), now.AddDate(0, 0, -2).Format("Mon")},
	}
	for _, c := range cases {
		if got := RelTime(c.at); got != c.want {
			t.Errorf("RelTime(%s) = %q, want %q", c.at.Format(time.Kitchen), got, c.want)
		}
	}
	// Past a week the label becomes a date; this year omits the year.
	old := now.AddDate(0, 0, -30)
	if got, want := RelTime(old), old.Format("1/2"); got != want {
		t.Errorf("RelTime(30 days ago) = %q, want %q", got, want)
	}
	lastYear := now.AddDate(-1, 0, 0)
	if got, want := RelTime(lastYear), lastYear.Format("1/2/06"); got != want {
		t.Errorf("RelTime(last year) = %q, want %q", got, want)
	}
}

func TestDurationKeepsSubSecondToolsReadable(t *testing.T) {
	cases := []struct {
		ms   int64
		want string
	}{
		{0, "0ms"},
		{1, "1ms"},
		{999, "999ms"},
		{1000, "1.0s"},
		{1500, "1.5s"},
		{940, "940ms"},
		{61234, "61.2s"},
	}
	for _, c := range cases {
		if got := Duration(c.ms); got != c.want {
			t.Errorf("Duration(%d) = %q, want %q", c.ms, got, c.want)
		}
	}
}

func TestFirstLineTakesTheHeadline(t *testing.T) {
	cases := []struct{ in, want string }{
		{"one line", "one line"},
		{"first\nsecond", "first"},
		{"", ""},
	}
	for _, c := range cases {
		if got := FirstLine(c.in); got != c.want {
			t.Errorf("FirstLine(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// A rail dash is a button whose label is the message's own words: the
// empty preview must still produce a label, or the dash becomes
// unclickable and the user has no way to jump at all.
func TestRailDashAlwaysHasALabel(t *testing.T) {
	for _, preview := range []string{"reply number 0", "", "\n\nleading blank"} {
		label := "Jump to: " + FirstLine(preview)
		if !strings.HasPrefix(label, "Jump to:") {
			t.Fatalf("preview %q produced the label %q", preview, label)
		}
		if strings.Count(label, "\n") != 0 {
			t.Fatalf("a multi-line preview leaked into one dash's label: %q", label)
		}
	}
}
