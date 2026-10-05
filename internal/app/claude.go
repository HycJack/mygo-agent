package app

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"slices"
	"strings"

	"mygo-agent/internal/harness"
)

// runClaude drives the Claude Code CLI over the bidirectional
// stream-json protocol (spec/cli-backends.md): the prompt is one input
// line, events become the thread's cards, and `can_use_tool` control
// requests surface as approval cards whose decision is written back.
// One fresh CLI per turn; session resume goes through --resume.
func (a *app) runClaude(ctx context.Context, th *Thread, turn harness.Turn, emit func(harness.Event)) {
	prompt, at := turn.Prompt, -1
	for i := range th.Messages {
		if th.Messages[i].Running {
			at = i
		}
	}
	if at < 0 {
		at = len(th.Messages) - 1
	}
	ctx, cancel := a.adoptCancel(ctx)
	defer cancel()

	// Mode mapping: read-only and agent run in default mode where the
	// CLI asks before every mutating tool — the app denies, or forwards
	// to a card, per mode. Full access bypasses asking entirely.
	mode := turn.Mode
	perm := "default"
	if mode == harness.ModeFull {
		perm = "bypassPermissions"
	}
	args := []string{"-p", "--output-format", "stream-json", "--verbose",
		"--input-format", "stream-json",
		"--permission-mode", perm, "--model", turn.Model}
	if turn.SessionID != "" {
		args = append(args, "--resume", turn.SessionID)
	}

	cmd := exec.CommandContext(ctx, a.claudePath, args...)
	cmd.Dir = a.workdir
	procGroupAttr(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		a.finish(th, at, "claude: "+err.Error())
		return
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		a.finish(th, at, "claude: "+err.Error())
		return
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		a.finish(th, at, "claude: "+err.Error())
		return
	}
	a.update(func() { a.claudePid = cmd.Process.Pid })

	// The prompt: one user message on the input stream.
	msg, _ := json.Marshal(map[string]any{
		"type": "user",
		"message": map[string]any{
			"role":    "user",
			"content": []map[string]any{{"type": "text", "text": prompt}},
		},
	})
	if _, err := stdin.Write(append(msg, '\n')); err != nil {
		stdin.Close()
		a.finish(th, at, "claude: "+err.Error())
		return
	}

	e := emit
	if e == nil {
		e = func(harness.Event) {}
	}
	r := &claudeRun{a: a, th: th, at: at, cards: map[string]int{},
		ctx: ctx, stdin: stdin, mode: mode, emit: e}
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 64*1024), 8<<20)
	for sc.Scan() {
		if r.sawResult {
			break // the turn is done; closing stdin ends the CLI
		}
		r.handle(strings.TrimSpace(sc.Text()))
	}
	stdin.Close()
	waitErr := cmd.Wait()

	errText := ""
	if ctx.Err() != nil {
		// Stopped by the user; keep whatever arrived.
	} else if !r.sawEvent || waitErr != nil {
		tail := strings.TrimSpace(stderr.String())
		if tail == "" && waitErr != nil {
			tail = waitErr.Error()
		}
		if tail != "" {
			errText = truncTitle("claude: "+tail, 400)
		}
	}
	r.flush()
	a.finish(th, at, errText)
}

// claudeRun carries one Claude Code run's state between events.
type claudeRun struct {
	a         *app
	th        *Thread
	at        int
	cards     map[string]int // tool_use id -> block index
	sawEvent  bool
	sawResult bool

	// Control-protocol state (spec/cli-backends.md).
	ctx   context.Context
	stdin io.WriteCloser
	mode  harness.Mode
	emit  func(harness.Event)

	pending strings.Builder
}

// controlRequest answers one CLI control request (spec/cli-backends.md):
// can_use_tool decisions ride the shared approval cards; every other
// server-initiated subtype gets an error reply so the CLI never stalls
// on an unanswered request.
func (r *claudeRun) controlRequest(line string) {
	var req struct {
		RequestID string `json:"request_id"`
		Request   struct {
			Subtype  string          `json:"subtype"`
			ToolName string          `json:"tool_name"`
			Input    json.RawMessage `json:"input"`
		} `json:"request"`
	}
	if json.Unmarshal([]byte(line), &req) != nil {
		return
	}
	if req.Request.Subtype != "can_use_tool" {
		r.writeControl(map[string]any{
			"subtype": "error", "request_id": req.RequestID, "error": "unsupported by mygo",
		})
		return
	}
	// Read-only: the mode denies every mutating tool the CLI asks about;
	// the denial names the mode so the model can stop trying.
	if r.mode == harness.ModeReadOnly {
		r.writeControl(map[string]any{
			"subtype": "success", "request_id": req.RequestID,
			"response": map[string]any{
				"behavior": "deny",
				"message":  "read-only mode: the " + req.Request.ToolName + " tool is not available; ask the user to change the approval mode",
			},
		})
		return
	}
	call := harness.ToolCall{ID: req.RequestID}
	call.Function.Name = req.Request.ToolName
	call.Function.Arguments = string(req.Request.Input)
	decision := r.a.waitForApproval(r.ctx, r.th, r.at, harness.ApprovalRequest{
		Call:    call,
		Summary: harness.ApprovalSummary(call),
		Reason:  "claude asks to use this tool",
	})
	if decision.Approved {
		var input any
		_ = json.Unmarshal(req.Request.Input, &input)
		if input == nil {
			input = map[string]any{}
		}
		r.writeControl(map[string]any{
			"subtype": "success", "request_id": req.RequestID,
			"response": map[string]any{"behavior": "allow", "updatedInput": input},
		})
		return
	}
	reason := decision.Reason
	if reason == "" {
		reason = "the user denied this call"
	}
	r.writeControl(map[string]any{
		"subtype": "success", "request_id": req.RequestID,
		"response": map[string]any{"behavior": "deny", "message": reason},
	})
}

// send routes one event to the projector; nil-safe for tests that build
// a claudeRun by hand.
func (r *claudeRun) send(ev harness.Event) {
	if r.emit != nil {
		r.emit(ev)
	}
}

// writeControl writes one control_response line to the CLI's stdin.
func (r *claudeRun) writeControl(response map[string]any) {
	line, _ := json.Marshal(map[string]any{"type": "control_response", "response": response})
	_, _ = r.stdin.Write(append(line, '\n'))
}

// flush pushes batched text deltas onto the thread.
func (r *claudeRun) flush() {
	if r.pending.Len() == 0 {
		return
	}
	s := r.pending.String()
	r.pending.Reset()
	if s != "" {
		r.send(harness.Event{Kind: harness.EventText, TextDelta: s})
	}
}

// handle applies one stream-json line.
func (r *claudeRun) handle(line string) {
	if line == "" {
		return
	}
	r.sawEvent = true
	// The result event carries everything at the top level; "result"
	// itself is the final text as a string.
	var ev struct {
		Type      string `json:"type"`
		Subtype   string `json:"subtype"`
		SessionID string `json:"session_id"`
		Message   *struct {
			Content []claudeBlock `json:"content"`
		} `json:"message"`
		ResultText string  `json:"result"`
		IsError    bool    `json:"is_error"`
		Duration   float64 `json:"duration_ms"`
		Cost       float64 `json:"total_cost_usd"`
	}
	if json.Unmarshal([]byte(line), &ev) != nil {
		return
	}

	switch ev.Type {
	case "control_request":
		r.controlRequest(line)
		return
	case "result":
		r.sawResult = true
	}

	switch ev.Type {
	case "system":
		if ev.Subtype == "init" && ev.SessionID != "" {
			r.send(harness.Event{Kind: harness.EventSession, SessionID: ev.SessionID})
		}

	case "assistant":
		if ev.Message == nil {
			return
		}
		for _, blk := range ev.Message.Content {
			switch blk.Type {
			case "text":
				if blk.Text == "" {
					continue
				}
				r.flush()
				r.send(harness.Event{Kind: harness.EventText, TextDelta: blk.Text})
			case "tool_use":
				r.flush()
				r.toolUse(blk.ID, blk.Name, blk.RawInput)
			}
		}

	case "user":
		if ev.Message == nil {
			return
		}
		for _, blk := range ev.Message.Content {
			if blk.Type == "tool_result" {
				r.toolResult(blk.ToolUseID, blk.RawContent, blk.IsError)
			}
		}

	case "result":
		r.flush()
		verb := "Done"
		if ev.IsError || ev.Subtype != "success" {
			verb = "Stopped"
		}
		r.send(harness.Event{Kind: harness.EventNote, Text: fmt.Sprintf(
			"%s in %.1fs · $%.4f · session %s", verb, ev.Duration/1000, ev.Cost, shortSession(r.th.ClaudeID))})
	}
}

// toolUse emits the tool_start event for a tool the model invoked. Edits
// carry a diff preview computed from the call's own old/new strings.
func (r *claudeRun) toolUse(id, name string, rawInput json.RawMessage) {
	var in struct {
		Command  string `json:"command"`
		FilePath string `json:"file_path"`
		Pattern  string `json:"pattern"`
		Old      string `json:"old_string"`
		New      string `json:"new_string"`
		Content  string `json:"content"`
	}
	_ = json.Unmarshal(rawInput, &in)

	disp := name
	isEdit := false
	var lines []harness.DiffLine
	file := ""
	switch name {
	case "Bash":
		disp = "$ " + truncTitle(in.Command, 200)
	case "Read":
		disp, file = "read "+in.FilePath, in.FilePath
	case "Edit":
		disp, file, isEdit = "edit "+in.FilePath, in.FilePath, true
		lines = harness.UnifiedDiff(in.Old, in.New)
	case "Write":
		disp, file, isEdit = "write "+in.FilePath, in.FilePath, true
		old := ""
		if data, err := os.ReadFile(in.FilePath); err == nil {
			old = string(data)
		}
		lines = harness.UnifiedDiff(old, in.Content)
	case "Grep":
		disp = "grep " + truncTitle(in.Pattern, 120)
	case "Glob":
		disp = "glob " + truncTitle(in.Pattern, 120)
	default:
		disp = truncTitle(name+" "+shortArgs(rawInput), 160)
	}

	ev := harness.Event{
		Kind:     harness.EventToolStart,
		ToolCall: harness.ToolCall{ID: id},
		Text:     disp,
		Edit:     isEdit,
		File:     file,
	}
	if isEdit && len(lines) > 0 {
		var b strings.Builder
		for _, l := range lines {
			b.WriteByte(l.Kind)
			b.WriteByte(' ')
			b.WriteString(l.Text)
			b.WriteByte('\n')
		}
		ev.Diff = b.String()
	}
	r.send(ev)
}

// toolResult emits the tool_end event for a completed tool call.
func (r *claudeRun) toolResult(toolUseID string, rawContent json.RawMessage, isError bool) {
	exit := 0
	if isError {
		exit = 1
	}
	r.send(harness.Event{
		Kind:     harness.EventToolEnd,
		ToolCall: harness.ToolCall{ID: toolUseID},
		Output:   harness.TrimOutput(claudeContentText(rawContent), 16<<10),
		Exit:     exit,
	})
}

// claudeBlock mirrors one content block of a Claude Code message.
type claudeBlock struct {
	Type       string          `json:"type"`
	Text       string          `json:"text"`
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	RawInput   json.RawMessage `json:"input"`
	ToolUseID  string          `json:"tool_use_id"`
	RawContent json.RawMessage `json:"content"`
	IsError    bool            `json:"is_error"`
}

// claudeContentText flattens a tool result's content — a string, or a
// list of text blocks — into plain text.
func claudeContentText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []claudeBlock
	if json.Unmarshal(raw, &blocks) == nil {
		var b strings.Builder
		for _, blk := range blocks {
			if blk.Type == "text" {
				b.WriteString(blk.Text)
				b.WriteString("\n")
			}
		}
		return strings.TrimRight(b.String(), "\n")
	}
	return string(raw)
}

// shortSession trims a session id for the summary note.
func shortSession(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// shortArgs summarizes a raw JSON argument object for a card title.
func shortArgs(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var in map[string]any
	if json.Unmarshal(raw, &in) != nil {
		s := string(raw)
		if len(s) > 120 {
			return s[:120] + "…"
		}
		return s
	}
	keys := slices.Sorted(maps.Keys(in))
	var parts []string
	for _, k := range keys {
		if s, ok := in[k].(string); ok {
			parts = append(parts, s)
		}
	}
	joined := strings.Join(parts, " ")
	if len(joined) > 120 {
		joined = joined[:120] + "…"
	}
	return joined
}
