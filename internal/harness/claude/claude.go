// Package claude adapts the Claude Code CLI (spec/cli-backends.md): the
// prompt is one input line on the stream-json wire, events become the
// harness's normalized events, and `can_use_tool` control requests
// surface through the turn's approval callback. One fresh CLI per turn;
// session resume goes through --resume.
package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"mygo-agent/internal/harness"
	"mygo-agent/internal/harness/cli"
)

// Harness runs one turn against the claude binary at Bin.
type Harness struct {
	Bin string
	// ApprovalTimeout bounds each approval wait; zero means the shared
	// harness.DefaultApprovalTimeout. Tests shorten it.
	ApprovalTimeout time.Duration
}

// New builds a claude harness for the binary at bin.
func New(bin string) *Harness { return &Harness{Bin: bin} }

// Kind implements harness.Harness.
func (h *Harness) Kind() string { return "claude" }

// Run drives one turn to completion. It returns nil for a clean turn or
// a stopped one (whatever arrived stays emitted); a non-nil error's
// message is the failure text for the reply card.
func (h *Harness) Run(ctx context.Context, turn harness.Turn, emit func(harness.Event)) error {
	prompt := turn.Prompt

	// Mode mapping: read-only and agent run in default mode where the
	// CLI asks before every mutating tool — the app denies, or forwards
	// to a card, per mode. Full access bypasses asking entirely.
	perm := "default"
	if turn.Mode == harness.ModeFull {
		perm = "bypassPermissions"
	}
	args := []string{"-p", "--output-format", "stream-json", "--verbose",
		"--input-format", "stream-json", "--include-partial-messages",
		"--permission-mode", perm, "--model", turn.Model}
	if turn.SessionID != "" {
		args = append(args, "--resume", turn.SessionID)
	}
	// The agent's own system prompt rides the CLI's append flag: the
	// CLI's base prompt stays, the agent's instructions join after it
	// (spec/agents.md, the capability map).
	if turn.SystemPrompt != "" {
		args = append(args, "--append-system-prompt", turn.SystemPrompt)
	}
	// The turn's MCP servers map onto the CLI's own config file: stdio
	// servers spawn as the CLI's children, HTTP ones by URL. The file is
	// deleted when the turn ends. A server that cannot be expressed is
	// skipped rather than fatal — the mapping is best-effort, and the
	// model discovers the absent tool and says so.
	if len(turn.MCPServers) > 0 {
		if path, err := writeMCPConfig(turn.MCPServers); err == nil {
			args = append(args, "--mcp-config", path)
			defer os.Remove(path)
		}
	}
	// A prompt that names a path outside the workspace asks for it here,
	// once, and the answer goes on the command line as --add-dir. It has
	// to be asked before the spawn: the grant is the CLI's allow list,
	// not a decision it can be told about midway. Read-only and agent
	// both run in default permission mode, and the CLI's directory
	// boundary blocks reads as much as writes — without the grant even a
	// read-only run fails silently, which is the failure this card
	// exists to prevent. Full access (bypassPermissions) has no boundary
	// to raise and is never asked.
	if perm == "default" {
		args = append(args, outsideDirArgs(ctx, turn)...)
	}

	cmd := exec.CommandContext(ctx, h.Bin, args...)
	cmd.Dir = turn.Workdir
	cli.ProcGroupAttr(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("claude: %s", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("claude: %s", err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("claude: %s", err)
	}

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
		return fmt.Errorf("claude: %s", err)
	}

	e := emit
	if e == nil {
		e = func(harness.Event) {}
	}
	r := &run{turn: turn, ctx: ctx, stdin: stdin, mode: turn.Mode, emit: e,
		approvalTimeout: h.ApprovalTimeout, cards: map[string]int{}}
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 64*1024), 8<<20)
	for sc.Scan() {
		r.handle(strings.TrimSpace(sc.Text()))
		// The result line ends the turn, but the CLI stays alive waiting
		// for the next input message: check after every line and close
		// stdin ourselves — a check before Scan would block here forever
		// and the turn would never settle.
		if r.sawResult {
			break
		}
	}
	waitErr, killed := cli.Reap(cmd, stdin)

	// A cancelled turn is only a clean stop when the CLI had actually got
	// on with it. A run that produced nothing at all and was then cut off
	// — a CLI that hung before its first line, or died without saying why
	// — reports itself as a finished turn otherwise, and the user is left
	// with an empty reply and no reason for it. The events that did arrive
	// still stand; what is missing is said out loud.
	if ctx.Err() != nil {
		if r.sawEvent {
			return nil // stopped by the user; keep whatever arrived
		}
		return fmt.Errorf("claude: stopped before it answered — %s", cli.Trunc(claudeReason(stderr, waitErr, killed), 400))
	}
	if r.sawEvent && (waitErr == nil || killed) {
		// A CLI we had to stop after it already settled is not a failed
		// turn: the events it sent stand.
		return nil
	}
	tail := strings.TrimSpace(stderr.String())
	if tail == "" && waitErr != nil {
		tail = waitErr.Error()
	}
	if tail == "" {
		return nil
	}
	return fmt.Errorf("claude: %s", tail)
}

// claudeReason is the best available account of a run that produced
// nothing: the CLI's own words if it left any, then the wait error, then
// what is actually known. The last is not decoration — a CLI that hangs
// and is killed has no stderr and often no wait error either, and "the
// CLI produced no output" is the fact that has to reach the user.
func claudeReason(stderr strings.Builder, waitErr error, killed bool) string {
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

// run carries one Claude Code run's state between events.
type run struct {
	turn      harness.Turn
	ctx       context.Context
	stdin     io.WriteCloser
	mode      harness.Mode
	emit      func(harness.Event)
	cards     map[string]int // tool_use id -> block index
	sawEvent  bool
	sawResult bool
	// streamed is the reply text the partial-message deltas have already
	// emitted, so the complete assistant message that follows them only
	// contributes its un-streamed suffix (the CLI warns the partial
	// stream may duplicate the message).
	streamed strings.Builder

	// approvalTimeout bounds one approval wait; zero uses the shared
	// default (harness.ApprovalContext).
	approvalTimeout time.Duration

	// wrote records control responses when there is no CLI to write to
	// (unit tests); production always has stdin.
	wrote []map[string]any
}

// controlRequest answers one CLI control request (spec/cli-backends.md):
// can_use_tool decisions ride the turn's approval callback; every other
// server-initiated subtype gets an error reply so the CLI never stalls
// on an unanswered request.
func (r *run) controlRequest(line string) {
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
	decision := harness.ApprovalDecision{Reason: "no approval handler is configured"}
	if r.turn.OnApproval != nil {
		// Every approval wait is bounded (spec/approvals.md): a card on a
		// live run must never sit unanswered. Expiry settles the call as
		// a denial inside the stream — not a failed turn — and the reason
		// tells the model (and the Host's card) why.
		actx, cancel := harness.ApprovalContext(r.ctx, r.approvalTimeout)
		decision = r.turn.OnApproval(actx, harness.ApprovalRequest{
			Call:    call,
			Summary: harness.ApprovalSummary(call),
			Reason:  "claude asks to use this tool",
		})
		if actx.Err() != nil {
			// A decision that arrives after the deadline is not a decision.
			decision = harness.ApprovalDecision{Reason: harness.ApprovalReason(actx)}
		}
		cancel()
	}
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
// a run by hand.
func (r *run) send(ev harness.Event) {
	if r.emit != nil {
		r.emit(ev)
	}
}

// writeControl writes one control_response line to the CLI's stdin.
func (r *run) writeControl(response map[string]any) {
	if r.stdin == nil {
		r.wrote = append(r.wrote, response)
		return
	}
	line, _ := json.Marshal(map[string]any{"type": "control_response", "response": response})
	_, _ = r.stdin.Write(append(line, '\n'))
}

// handle applies one stream-json line.
func (r *run) handle(line string) {
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
		// compact_metadata is snake_case, like the rest of claude's
		// stream-json wire. Reading it as `compactMetadata` decodes to
		// nothing and silently costs the note its counts.
		CompactMetadata *struct {
			Trigger    string `json:"trigger"`
			PreTokens  int    `json:"pre_tokens"`
			PostTokens int    `json:"post_tokens"`
		} `json:"compact_metadata"`
		Message *struct {
			Content []block `json:"content"`
		} `json:"message"`
		ResultText string  `json:"result"`
		IsError    bool    `json:"is_error"`
		Duration   float64 `json:"duration_ms"`
		Cost       float64 `json:"total_cost_usd"`
		// The turn's token totals, when the CLI reports them; the note
		// carries their sum the way pi's note carries totalTokens.
		Usage *struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
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
		// claude summarises its own context when the conversation
		// outgrows the window, and says so on this wire as a system
		// frame with subtype compact_boundary. Without it the only
		// symptom is the model quietly forgetting an earlier
		// instruction. A frame with no metadata still counts: the
		// event happened, the counts are a bonus.
		if ev.Subtype == "compact_boundary" {
			var before, after int
			if ev.CompactMetadata != nil {
				before, after = ev.CompactMetadata.PreTokens, ev.CompactMetadata.PostTokens
			}
			r.send(harness.Event{
				Kind: harness.EventNote,
				Text: cli.CompactedNotice("claude", before, after),
			})
		}

	case "stream_event":
		// With --include-partial-messages the CLI wraps its inner
		// Anthropic stream in these frames: text arrives as deltas as it
		// is generated, instead of one whole assistant message at the
		// end. The complete assistant message still follows — the reply
		// below emits only the suffix the deltas have not covered.
		var se struct {
			Event struct {
				Type  string `json:"type"`
				Delta struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"delta"`
			} `json:"event"`
		}
		if json.Unmarshal([]byte(line), &se) != nil {
			return
		}
		if se.Event.Type == "content_block_delta" && se.Event.Delta.Type == "text_delta" && se.Event.Delta.Text != "" {
			r.streamed.WriteString(se.Event.Delta.Text)
			r.send(harness.Event{Kind: harness.EventText, TextDelta: se.Event.Delta.Text})
		}
	case "assistant":
		if ev.Message == nil {
			return
		}
		for _, blk := range ev.Message.Content {
			switch blk.Type {
			case "text":
				// Deduplicate against the streamed deltas: the CLI warns
				// that partial messages may duplicate the assistant
				// message, so only the un-streamed suffix goes out.
				whole := r.streamed.String()
				text := blk.Text
				if text == "" {
					continue
				}
				if len(text) >= len(whole) {
					if suffix, ok := strings.CutPrefix(text, whole); ok {
						text = suffix
					} else if whole != "" {
						text = "" // diverged; trust the deltas already sent
					}
				} else if strings.HasPrefix(whole, text) {
					text = ""
				}
				r.streamed.Reset()
				if text != "" {
					r.send(harness.Event{Kind: harness.EventText, TextDelta: text})
				}
			case "tool_use":
				r.streamed.Reset()
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
		verb := "Done"
		if ev.IsError || ev.Subtype != "success" {
			verb = "Stopped"
		}
		text := fmt.Sprintf("%s in %.1fs · $%.4f", verb, ev.Duration/1000, ev.Cost)
		var tokens int64
		if ev.Usage != nil {
			tokens = int64(ev.Usage.InputTokens + ev.Usage.OutputTokens)
			if tokens > 0 {
				text += fmt.Sprintf(" · %d tokens", tokens)
			}
		}
		r.send(harness.Event{Kind: harness.EventNote,
			Text:   text + " · session " + cli.ShortSession(r.turn.SessionID),
			Tokens: tokens, CostUSD: ev.Cost})
	}
}

// toolUse emits the tool_start event for a tool the model invoked. Edits
// carry a diff preview computed from the call's own old/new strings.
func (r *run) toolUse(id, name string, rawInput json.RawMessage) {
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
		disp = "$ " + cli.Trunc(in.Command, 200)
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
		disp = "grep " + cli.Trunc(in.Pattern, 120)
	case "Glob":
		disp = "glob " + cli.Trunc(in.Pattern, 120)
	default:
		disp = cli.Trunc(name+" "+cli.ShortArgs(rawInput), 160)
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
func (r *run) toolResult(toolUseID string, rawContent json.RawMessage, isError bool) {
	exit := 0
	if isError {
		exit = 1
	}
	r.send(harness.Event{
		Kind:     harness.EventToolEnd,
		ToolCall: harness.ToolCall{ID: toolUseID},
		Output:   cli.TrimOutput(contentText(rawContent), 16<<10),
		Exit:     exit,
	})
}

// block mirrors one content block of a Claude Code message.
type block struct {
	Type       string          `json:"type"`
	Text       string          `json:"text"`
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	RawInput   json.RawMessage `json:"input"`
	ToolUseID  string          `json:"tool_use_id"`
	RawContent json.RawMessage `json:"content"`
	IsError    bool            `json:"is_error"`
}

// contentText flattens a tool result's content — a string, or a list of
// text blocks — into plain text.
func contentText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []block
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

// outsideDirArgs asks once about every directory the prompt pointed at
// outside the workspace, and returns the --add-dir flags for the ones
// that were granted.
//
// One question for the whole prompt, not one per path: a prompt naming
// five files in three directories is a single decision, and asking five
// times trains the user to click through without reading. A refusal is
// final and silent — the run stays confined, and the CLI's own message
// ("you haven't granted it yet") is what the model sees, which is the
// same thing it would have said unasked.
func outsideDirArgs(ctx context.Context, turn harness.Turn) []string {
	// A run with nowhere to ask runs confined. That is the safe reading
	// of a boundary it cannot raise, and it keeps this function safe to
	// call on any turn rather than only where Run has already checked.
	if turn.OnOutsideDir == nil {
		return nil
	}
	dirs := harness.DirsOutsideWorkdir(turn.Prompt, turn.Workdir)
	if len(dirs) == 0 {
		return nil
	}
	if !turn.OnOutsideDir(ctx, harness.OutsideDirRequest{Workdir: turn.Workdir, Dirs: dirs}) {
		return nil
	}
	// --add-dir takes directories only and repeats per directory, so a
	// grant that is a file path is useless on its own. The scan already
	// narrows to directories, which is what makes this list well-formed.
	args := make([]string, 0, len(dirs)*2)
	for _, d := range dirs {
		args = append(args, "--add-dir", d)
	}
	return args
}

// writeMCPConfig writes the turn's MCP servers as the CLI's --mcp-config
// file (spec/agents.md, the capability map): stdio servers keep their
// command, URL servers map to the http type. Best-effort by contract —
// the caller skips the flag when the write fails.
func writeMCPConfig(servers []harness.MCPServer) (string, error) {
	cfg := struct {
		MCPServers map[string]any `json:"mcpServers"`
	}{MCPServers: map[string]any{}}
	for _, s := range servers {
		if s.URL != "" {
			cfg.MCPServers[s.Name] = map[string]any{"type": "http", "url": s.URL}
			continue
		}
		if s.Command == "" {
			continue
		}
		srv := map[string]any{"command": s.Command}
		if len(s.Args) > 0 {
			srv["args"] = s.Args
		}
		if len(s.Env) > 0 {
			srv["env"] = s.Env
		}
		cfg.MCPServers[s.Name] = srv
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", err
	}
	f, err := os.CreateTemp("", "mygo-mcp-*.json")
	if err != nil {
		return "", err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), f.Close()
}
