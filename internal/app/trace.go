package app

// The thread trace (spec/agents.md, P3 tracing): one append-only JSONL
// file per thread, beside its thread file. The projector is the single
// writer — every backend's events pass through it, so four adapters get
// one trace format for free — and finish adds one turn summary line.
//
// The trace is derived, auditable data: it records what happened and is
// never read back into state. Text and reasoning deltas stay out (they
// are the prose, already persisted in the thread file); the trace is
// the tool/usage/note skeleton of the turn.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mygo-agent/internal/harness"
	"mygo-agent/internal/harness/cli"
)

// traceEvent is one line of a thread's trace file.
type traceEvent struct {
	At      time.Time `json:"ts"`
	Kind    string    `json:"kind"`
	Tool    string    `json:"tool,omitempty"`
	ToolID  string    `json:"tool_id,omitempty"`
	Ms      int64     `json:"ms,omitempty"`
	Tokens  int64     `json:"tokens,omitempty"`
	CostUSD float64   `json:"cost_usd,omitempty"`
	Summary string    `json:"summary,omitempty"`
	Failed  bool      `json:"failed,omitempty"`
}

// eventFile is a thread's trace path: beside the thread file, under the
// same project partition and the same filename rules (spec/data.md).
func (l threadsLayout) eventFile(th *Thread) (string, bool) {
	if l.root == "" || !safeName(th.ProjectID) || !safeName(th.ID) {
		return "", false
	}
	return filepath.Join(l.root, th.ProjectID, th.ID+".events.jsonl"), true
}

// appendTrace writes one line. Errors are swallowed on purpose: the
// trace is best-effort derived data, and a failed append must never
// fail the turn that produced the event.
func (a *app) appendTrace(th *Thread, te traceEvent) {
	if th.dropped {
		return
	}
	path, ok := a.threadsDir.eventFile(th)
	if !ok {
		return
	}
	data, err := json.Marshal(te)
	if err != nil {
		return
	}
	// The project directory is created by the thread's own save, but an
	// event can beat it to the file (the first event of a turn lands
	// before finish's first save in a test drive); make the parent.
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	_, _ = f.Write(append(data, '\n'))
	_ = f.Close()
}

// traceHarnessEvent shapes one harness event into a trace line. Text
// and reasoning deltas return false: recording every delta would be the
// thread file with extra steps.
func traceHarnessEvent(ev harness.Event) (traceEvent, bool) {
	te := traceEvent{At: time.Now(), Kind: ev.Kind}
	switch ev.Kind {
	case harness.EventToolStart:
		te.Tool = ev.ToolCall.Function.Name
		te.ToolID = ev.ToolCall.ID
		te.Summary = toolDisplayFor(ev.ToolCall)
	case harness.EventToolEnd:
		te.Tool = ev.ToolCall.Function.Name
		te.ToolID = ev.ToolCall.ID
		te.Ms = ev.Ms
		te.Failed = ev.Exit != 0
		te.Summary = cli.Trunc(cli.Redact(ev.Output), 200)
	case harness.EventFileChange:
		te.Tool = "file_change"
		te.Summary = cli.Trunc(ev.File, 200)
	case harness.EventSession:
		te.Summary = ev.SessionID
	case harness.EventNote:
		te.Summary = cli.Trunc(cli.Redact(ev.Text), 300)
		te.Tokens = ev.Tokens
		te.CostUSD = ev.CostUSD
	case harness.EventError:
		te.Summary = cli.Trunc(ev.Err, 300)
		te.Failed = true
	default:
		return te, false
	}
	return te, true
}

// traceTurn writes the turn's summary line: the aggregate of what the
// turn's events carried, one line per turn. The per-message
// accumulators are consumed and cleared here.
func (a *app) traceTurn(th *Thread, at int, errText string) {
	m := reply(th, at)
	if m == nil {
		return
	}
	te := traceEvent{
		At:      time.Now(),
		Kind:    "turn",
		Ms:      time.Since(m.At).Milliseconds(),
		Tokens:  m.turnTokens,
		CostUSD: m.turnCost,
		Failed:  errText != "",
	}
	te.Summary = fmt.Sprintf("%d tool calls", m.turnTools)
	if te.Failed {
		te.Summary = cli.Trunc(errText, 200)
	}
	if te.Tokens > 0 {
		te.Summary = fmt.Sprintf("%s · %d tokens", te.Summary, te.Tokens)
	}
	m.turnTokens, m.turnCost, m.turnTools = 0, 0, 0
	a.appendTrace(th, te)
}

// openTrace shows a thread's trace in the file viewer: one formatted
// line per recorded event, oldest first — the turn as it happened.
func (a *app) openTrace(th *Thread) {
	path, ok := a.threadsDir.eventFile(th)
	if !ok {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		a.showViewerError(path, "no trace yet for this task.")
		return
	}
	var b strings.Builder
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var te traceEvent
		if json.Unmarshal([]byte(line), &te) != nil {
			continue
		}
		head := te.At.Local().Format("15:04:05")
		if te.Ms > 0 {
			head += fmt.Sprintf(" %6dms", te.Ms)
		}
		detail := te.Summary
		if te.Tool != "" {
			detail = te.Tool + " " + detail
		}
		if te.Tokens > 0 {
			detail = fmt.Sprintf("%s · %d tokens", detail, te.Tokens)
		}
		if te.CostUSD > 0 {
			detail = fmt.Sprintf("%s · $%.4f", detail, te.CostUSD)
		}
		mark := " "
		if te.Failed {
			mark = "!"
		}
		fmt.Fprintf(&b, "%s %s %-9s %s\n", head, mark, te.Kind, strings.TrimSpace(detail))
	}
	text := b.String()
	if text == "" {
		text = "(empty trace)"
	}
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	a.viewer = viewerState{
		Path: "trace:" + th.ID, Kind: "text",
		Title: "Trace — " + th.Title, Sub: fmt.Sprintf("%d events", len(lines)),
		Text: text, Lines: makeLines(lines), Raw: text,
	}
	a.viewerOpen = true
}
