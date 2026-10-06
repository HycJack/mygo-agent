// Package pi adapts the pi coding agent CLI in its JSON mode
// (spec/cli-backends.md): one fresh `pi -p --mode json` per turn, events
// streamed as JSONL on stdout. The session id the stream reports is
// emitted as the run's session and passed back with --session-id, so a
// task resumes pi's own session. Extensions are skipped (-ne): a broken
// local extension would otherwise block every run. pi executes its own
// tools with its own permissions; the host's approval cards do not cover
// it — read-only mode maps to a read-only tool allowlist, nothing more.
package pi

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"mygo-agent/internal/harness"
	"mygo-agent/internal/harness/cli"
)

// Harness runs one turn against the pi binary at Bin.
type Harness struct{ Bin string }

// New builds a pi harness for the binary at bin.
func New(bin string) *Harness { return &Harness{Bin: bin} }

// Kind implements harness.Harness.
func (h *Harness) Kind() string { return "pi" }

// Run drives one turn to completion. It returns nil for a clean turn or
// a stopped one; a non-nil error's message is the failure text for the
// reply card.
func (h *Harness) Run(ctx context.Context, turn harness.Turn, emit func(harness.Event)) error {
	// pi exposes one lever per mode, so an unknown value would run with
	// the wrong permission. Turn.Mode is a bare int with no clamp
	// anywhere, and the repo has no recover() to catch a bad index, so
	// fail loudly instead of guessing.
	switch turn.Mode {
	case harness.ModeReadOnly, harness.ModeAgent, harness.ModeFull:
	default:
		return fmt.Errorf("pi: unknown approval mode %d", int(turn.Mode))
	}

	prompt := turn.Prompt
	sessionID := turn.SessionID
	if sessionID == "" {
		sessionID = newUUID()
	}
	args := []string{"-p", "--mode", "json", "-ne", "--session-id", sessionID,
		"--thinking", effortLevel(turn.Effort)}
	if turn.Mode == harness.ModeReadOnly {
		// pi has no permission modes; keep the read tool only.
		args = append(args, "--tools", "read")
	}
	// The prompt rides argv after --, as pi's usage prescribes.
	args = append(args, "--", prompt)

	cmd := exec.CommandContext(ctx, h.Bin, args...)
	cmd.Dir = turn.Workdir
	cli.ProcGroupAttr(cmd)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("pi: %s", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("pi: %s", err)
	}

	e := emit
	if e == nil {
		e = func(harness.Event) {}
	}
	r := &run{turn: turn, emit: e}
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 64*1024), 8<<20)
	for sc.Scan() {
		r.handle(strings.TrimSpace(sc.Text()))
		if r.settled {
			break // agent_settled: the turn is done; reap the CLI
		}
	}
	// pi exits on its own; it has no resident stdin to close, but the
	// bounded group teardown is the same guard every adapter uses.
	waitErr, killed := cli.Reap(cmd, nil)
	// A cancelled turn is only a clean stop when the CLI had actually got
	// on with it. A run that produced nothing and was then cut off — a
	// CLI that hung before its first line, or died without saying why —
	// otherwise reports itself as finished, leaving the user an empty reply
	// and no reason for it.
	if ctx.Err() != nil {
		if r.sawEvent {
			return nil // stopped by the user; keep whatever arrived
		}
		return fmt.Errorf("pi: stopped before it answered — %s", cli.Trunc(piReason(stderr, waitErr, killed), 400))
	}
	if !killed && (!r.sawEvent || waitErr != nil) {
		tail := strings.TrimSpace(stderr.String())
		if tail == "" && waitErr != nil {
			tail = waitErr.Error()
		}
		if tail != "" {
			return fmt.Errorf("pi: %s", cli.Trunc(tail, 400))
		}
	}
	if r.tokens > 0 {
		r.emit(harness.Event{Kind: harness.EventNote, Text: fmt.Sprintf(
			"Done · %d tokens · $%.4f · session %s", r.tokens, r.cost, cli.ShortSession(sessionID)),
			Tokens: int64(r.tokens), CostUSD: r.cost})
	}
	return nil
}

// piReason is the best available account of a run that produced nothing:
// the CLI's own words if it left any, then the wait error, then what is
// actually known. The last is not decoration — a CLI that hangs and gets
// killed has no stderr and often no wait error either, and "it produced
// no output" is the fact that has to reach the user.
func piReason(stderr strings.Builder, waitErr error, killed bool) string {
	if tail := strings.TrimSpace(stderr.String()); tail != "" {
		return tail
	}
	if waitErr != nil {
		return waitErr.Error()
	}
	if killed {
		return "it was still running and had to be stopped"
	}
	return "it exited without answering"
}

// effortLevels are pi's --thinking levels, indexed by Turn.Effort.
var effortLevels = []string{"off", "minimal", "medium", "high"}

// effortLevel maps an effort to a --thinking level. An out-of-range value
// must still land somewhere — indexing the table directly would panic the
// dispatch goroutine, and nothing in the repo recovers.
func effortLevel(effort int) string {
	if effort >= 0 && effort < len(effortLevels) {
		return effortLevels[effort]
	}
	return effortLevels[2] // the same level a clamped effort would give
}

// run carries one pi run's state between events.
type run struct {
	turn     harness.Turn
	emit     func(harness.Event)
	sawEvent bool
	settled  bool

	sawDelta bool            // the streaming message already emitted its text
	thinking strings.Builder // thinking deltas, one reasoning card at end
	tokens   int
	cost     float64
}

// handle applies one JSONL event of pi's --mode json stream.
func (r *run) handle(line string) {
	if line == "" {
		return
	}
	var ev struct {
		Type string `json:"type"`
		// session
		ID string `json:"id"`
		// message_update
		AssistantMessageEvent *struct {
			Type    string `json:"type"`
			Delta   string `json:"delta"`
			Content *struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"assistantMessageEvent"`
		Message *piMessage `json:"message"`
		// auto_retry_start
		Attempt      int    `json:"attempt"`
		MaxAttempts  int    `json:"maxAttempts"`
		ErrorMessage string `json:"errorMessage"`
		// turn_end
		ToolResults []piMessage `json:"toolResults"`
	}
	if json.Unmarshal([]byte(line), &ev) != nil {
		return
	}
	r.sawEvent = true

	switch ev.Type {
	case "session":
		if ev.ID != "" {
			r.emit(harness.Event{Kind: harness.EventSession, SessionID: ev.ID})
		}
	case "message_update":
		ae := ev.AssistantMessageEvent
		if ae == nil {
			return
		}
		switch ae.Type {
		case "text_delta":
			r.sawDelta = true
			r.emit(harness.Event{Kind: harness.EventText, TextDelta: ae.Delta})
		case "thinking_delta":
			r.thinking.WriteString(ae.Delta)
		}
	case "message_end":
		if ev.Message == nil {
			return
		}
		r.messageEnd(ev.Message)
	case "turn_end":
		for _, res := range ev.ToolResults {
			if res.Role != "toolResult" {
				continue
			}
			exit := 0
			if res.IsError {
				exit = 1
			}
			r.emit(harness.Event{
				Kind:     harness.EventToolEnd,
				ToolCall: harness.ToolCall{ID: res.ToolCallID},
				Output:   cli.TrimOutput(res.text(), 16<<10),
				Exit:     exit,
			})
		}
	case "auto_retry_start":
		r.emit(harness.Event{Kind: harness.EventNote, Text: fmt.Sprintf(
			"pi retrying (attempt %d/%d): %s", ev.Attempt, ev.MaxAttempts, ev.ErrorMessage)})
	case "agent_settled":
		r.settled = true
	}
}

// messageEnd projects one completed message: the reply text (only when
// no delta streamed it — the wire repeats it whole), reasoning, tool
// calls, errors and usage.
func (r *run) messageEnd(m *piMessage) {
	if m.Role == "user" || m.Role == "toolResult" {
		return
	}
	if m.ErrorMessage != "" || m.StopReason == "error" {
		r.emit(harness.Event{Kind: harness.EventError, Err: m.ErrorMessage})
	}
	if r.thinking.Len() > 0 {
		r.emit(harness.Event{Kind: harness.EventReasoning, Text: strings.TrimSpace(r.thinking.String())})
		r.thinking.Reset()
	}
	for _, blk := range m.Content {
		switch blk.Type {
		case "text":
			if blk.Text != "" && !r.sawDelta {
				// No delta streamed this message: the wire only tells
				// the whole text here, so emit it once.
				r.emit(harness.Event{Kind: harness.EventText, TextDelta: blk.Text})
			}
		case "toolCall":
			r.emit(harness.Event{
				Kind:     harness.EventToolStart,
				ToolCall: harness.ToolCall{ID: blk.ID},
				Text:     cli.Trunc(blk.Name+" "+cli.ShortArgs(blk.Arguments), 160),
			})
		}
	}
	r.sawDelta = false
	if m.Usage.TotalTokens > 0 {
		r.tokens = m.Usage.TotalTokens
	}
	r.cost += m.Usage.Cost.Total
}

// piMessage mirrors one pi message on the JSON wire.
type piMessage struct {
	Role    string `json:"role"`
	Content []struct {
		Type      string          `json:"type"`
		Text      string          `json:"text"`
		ID        string          `json:"id"`
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	} `json:"content"`
	ToolCallID   string `json:"toolCallId"`
	ToolName     string `json:"toolName"`
	IsError      bool   `json:"isError"`
	StopReason   string `json:"stopReason"`
	ErrorMessage string `json:"errorMessage"`
	Usage        struct {
		TotalTokens int `json:"totalTokens"`
		Cost        struct {
			Total float64 `json:"total"`
		} `json:"cost"`
	} `json:"usage"`
}

// text flattens a toolResult message's content blocks.
func (m *piMessage) text() string {
	var b strings.Builder
	for _, blk := range m.Content {
		if blk.Text != "" {
			b.WriteString(blk.Text)
			b.WriteString("\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// newUUID returns a random RFC 4122 v4 UUID string, the shape pi's
// --session-id expects.
func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
