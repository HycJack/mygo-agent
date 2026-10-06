package main

// The dashboard's mock data model and its pure helpers. Nothing here
// touches the UI, so it unit-tests without a window — the same split the
// agent's ui package draws between rendering and ViewModel code.

import (
	"fmt"
	"math"
	"math/rand/v2"
	"strconv"
	"strings"
	"time"
)

// Order is one row of the orders table.
type Order struct {
	ID       string
	Customer string
	Region   string // "NA", "EU", "APAC" or "LATAM"
	Amount   float64
	Status   string // "paid", "pending" or "refunded"
	At       time.Time
}

// Statuses and Regions name an order's two categorical fields.
var Statuses = []string{"paid", "pending", "refunded"}

var Regions = []string{"NA", "EU", "APAC", "LATAM"}

var customers = []string{
	"Ada Lovelace", "Grace Hopper", "Alan Turing", "Margaret Hamilton",
	"Edsger Dijkstra", "Barbara Liskov", "Donald Knuth", "Radia Perlman",
	"Ken Thompson", "Frances Allen", "Linus Torvalds", "Anita Borg",
}

// makeOrders generates n plausible orders from a fixed seed, so every
// launch shows the same dashboard.
func makeOrders(n int) []Order {
	r := rand.New(rand.NewPCG(42, 7))
	orders := make([]Order, n)
	start := time.Now().AddDate(0, 0, -21)
	for i := range orders {
		orders[i] = Order{
			ID:       fmt.Sprintf("ORD-%04d", 1000+i),
			Customer: customers[r.IntN(len(customers))],
			Region:   Regions[r.IntN(len(Regions))],
			Amount:   math.Round(float64(40+r.IntN(1960))*100) / 100,
			Status:   Statuses[r.IntN(10)/4], // weights paid > pending > refunded
			At:       start.Add(time.Duration(r.IntN(21*24)) * time.Hour),
		}
	}
	return orders
}

// filterOrders keeps the orders whose customer, ID or region matches the
// search and whose status matches the chosen filter ("all" keeps all).
func filterOrders(orders []Order, search, status string) []Order {
	q := strings.ToLower(strings.TrimSpace(search))
	var out []Order
	for _, o := range orders {
		if status != "all" && o.Status != status {
			continue
		}
		if q != "" &&
			!strings.Contains(strings.ToLower(o.Customer), q) &&
			!strings.Contains(strings.ToLower(o.ID), q) &&
			!strings.Contains(strings.ToLower(o.Region), q) {
			continue
		}
		out = append(out, o)
	}
	return out
}

// sortOrders orders the rows by the table's sort column; an unknown
// column keeps the input order.
func sortOrders(orders []Order, col string, descending bool) []Order {
	out := append([]Order(nil), orders...)
	var less func(a, b Order) int
	switch col {
	case "Customer":
		less = func(a, b Order) int { return strings.Compare(strings.ToLower(a.Customer), strings.ToLower(b.Customer)) }
	case "Region":
		less = func(a, b Order) int { return strings.Compare(a.Region, b.Region) }
	case "Amount":
		less = func(a, b Order) int { return cmpFloat(a.Amount, b.Amount) }
	case "Date":
		less = func(a, b Order) int { return a.At.Compare(b.At) }
	}
	if less == nil {
		return out
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && less(out[j], out[j-1]) < 0; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	if descending {
		for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
			out[i], out[j] = out[j], out[i]
		}
	}
	return out
}

func cmpFloat(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

// sum is the total of a slice, for KPI cards and chart labels.
func sum(values []float64) float64 {
	var t float64
	for _, v := range values {
		t += v
	}
	return t
}

// NamedValue is one labeled bar of the bar chart, or one segment of the
// donut.
type NamedValue struct {
	Label string
	Value float64
}

// seriesFor returns the traffic series of a range: hourly points for a
// day, daily for a week, weekly for a quarter. Deterministic per range.
func seriesFor(theRange string) (series []NamedValue, label string) {
	r := rand.New(rand.NewPCG(9, uint64(len(theRange))))
	base := 400 + float64(len(theRange))*37
	switch theRange {
	case "24h":
		label = "Requests per hour"
		for h := range 24 {
			diurnal := 1 + 0.55*math.Sin((float64(h)-4)/24*2*math.Pi)
			series = append(series, NamedValue{fmt.Sprintf("%02d:00", h), base * diurnal * (0.85 + 0.3*r.Float64())})
		}
	case "7d":
		label = "Requests per day"
		for d, day := range []string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"} {
			week := 1.0
			if d >= 5 {
				week = 0.55
			}
			series = append(series, NamedValue{day, base * 20 * week * (0.85 + 0.3*r.Float64())})
		}
	default: // "30d"
		label = "Requests per week"
		for w := range 12 {
			series = append(series, NamedValue{fmt.Sprintf("W%d", w+1), base * 90 * (0.8 + 0.4*r.Float64())})
		}
	}
	return series, label
}

// revenueByDay returns one bar per weekday of the last week.
func revenueByDay() []NamedValue {
	r := rand.New(rand.NewPCG(5, 5))
	var out []NamedValue
	for _, day := range []string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"} {
		week := 1.0
		if day == "Sat" || day == "Sun" {
			week = 0.6
		}
		out = append(out, NamedValue{day, 5200 * week * (0.8 + 0.4*r.Float64())})
	}
	return out
}

// trafficSources names where the visitors came from, for the donut.
func trafficSources() []NamedValue {
	return []NamedValue{
		{"Organic search", 46},
		{"Direct", 24},
		{"Referral", 17},
		{"Social", 13},
	}
}

// member is one row of the team workload card.
type member struct {
	Name  string
	Role  string
	Load  float64 // 0..1
	Tasks int
	Seen  time.Time
}

func team() []member {
	r := rand.New(rand.NewPCG(3, 11))
	roles := []string{"Design", "Backend", "Frontend", "Data", "Support"}
	var out []member
	for i, name := range customers[:6] {
		out = append(out, member{
			Name:  name,
			Role:  roles[i%len(roles)],
			Load:  0.25 + 0.7*r.Float64(),
			Tasks: 2 + r.IntN(18),
			Seen:  time.Now().Add(-time.Duration(r.IntN(600)) * time.Minute),
		})
	}
	return out
}

// Event is one row of the events list, which shows off the virtualized
// list with ten thousand of them.
type Event struct {
	Seq   int
	Kind  string // "deploy", "alert", "signup", "payment"
	What  string
	At    time.Time
	Level string // "info", "warn", "error"
}

var eventKinds = []struct {
	kind  string
	whats []string
	level string
}{
	{"deploy", []string{"api@2.4.1 rolled out", "web@1.19.0 canary at 10%", "worker@0.8.2 rolled back"}, "info"},
	{"alert", []string{"p95 latency above 400ms", "error rate 2.1% on checkout", "disk 82% on db-3"}, "warn"},
	{"signup", []string{"new workspace acme-inc", "new user from invite", "trial started: globex"}, "info"},
	{"payment", []string{"invoice #8121 settled", "charge failed, card declined", "refund issued for #8097"}, "error"},
	{"deploy", []string{"edge config updated", "schema migration 0042 applied"}, "info"},
	{"alert", []string{"queue depth above 10k", "cert renews in 72h"}, "warn"},
}

// makeEvents generates n events, one every few minutes walking backwards
// from now, so their timestamps read like a live feed.
func makeEvents(n int) []Event {
	r := rand.New(rand.NewPCG(21, 3))
	events := make([]Event, n)
	at := time.Now()
	for i := range events {
		k := eventKinds[r.IntN(len(eventKinds))]
		events[i] = Event{
			Seq:   n - i,
			Kind:  k.kind,
			What:  k.whats[r.IntN(len(k.whats))],
			At:    at,
			Level: k.level,
		}
		at = at.Add(-time.Duration(2+r.IntN(9)) * time.Minute)
	}
	return events
}

// filterEvents matches the list's search against the kind and the text.
func filterEvents(events []Event, search string) []Event {
	q := strings.ToLower(strings.TrimSpace(search))
	if q == "" {
		return events
	}
	var out []Event
	for _, e := range events {
		if strings.Contains(strings.ToLower(e.Kind), q) || strings.Contains(strings.ToLower(e.What), q) {
			out = append(out, e)
		}
	}
	return out
}

// formatMoney renders an amount as whole dollars: $12,480.
func formatMoney(v float64) string {
	n := int64(math.Round(v))
	neg, s := n < 0, strconv.FormatInt(n, 10)
	if neg {
		s = s[1:]
	}
	var parts []string
	for len(s) > 3 {
		parts = append([]string{s[len(s)-3:]}, parts...)
		s = s[:len(s)-3]
	}
	parts = append([]string{s}, parts...)
	sign := ""
	if neg {
		sign = "-"
	}
	return "$" + sign + strings.Join(parts, ",")
}

// formatCompact renders a count the way dashboards do: 1.2k, 3.4M.
func formatCompact(v float64) string {
	switch {
	case v >= 1e6:
		return trimZero(v/1e6) + "M"
	case v >= 1e3:
		return trimZero(v/1e3) + "k"
	default:
		return strconv.Itoa(int(v))
	}
}

func trimZero(v float64) string {
	s := strconv.FormatFloat(v, 'f', 1, 64)
	return strings.TrimSuffix(s, ".0")
}

// relativeTime renders a timestamp against now, the way an activity feed
// does: "just now", "12m ago", "3h ago", then a date.
func relativeTime(t, now time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + "m ago"
	case d < 24*time.Hour:
		return strconv.Itoa(int(d.Hours())) + "h ago"
	case d < 48*time.Hour:
		return "yesterday"
	default:
		return t.Format("Jan 2")
	}
}
