package app

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"slices"
	"strings"

	"mygo-agent/internal/agent"
)

// runClaude drives the Claude Code CLI (`claude -p`) in the background:
// its stream-json events become the thread's text, tool cards and
// diffs, and the CLI's session id is kept so later turns resume it.
func (a *app) runClaude(th *Thread, prompt string, at int) {
	ctx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel
	defer cancel()

	// The approval mode maps onto Claude Code's permission modes:
	// read-only analyses, edits applied without asking, or full access.
	perm := []string{"plan", "acceptEdits", "bypassPermissions"}[a.mode]
	args := []string{"-p", "--output-format", "stream-json", "--verbose",
		"--permission-mode", perm, "--model", a.model}
	if th.ClaudeID != "" {
		args = append(args, "--resume", th.ClaudeID)
	}
	args = append(args, prompt)

	cmd := exec.CommandContext(ctx, a.claudePath, args...)
	cmd.Dir = a.workdir
	procGroupAttr(cmd)
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
	a.claudePid = cmd.Process.Pid

	r := &claudeRun{a: a, th: th, at: at, cards: map[string]int{}}
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 64*1024), 8<<20)
	for sc.Scan() {
		r.handle(strings.TrimSpace(sc.Text()))
	}
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
	a        *app
	th       *Thread
	at       int
	cards    map[string]int // tool_use id -> block index
	sawEvent bool

	pending strings.Builder
}

// flush pushes batched text deltas onto the thread.
func (r *claudeRun) flush() {
	if r.pending.Len() == 0 {
		return
	}
	s := r.pending.String()
	r.pending.Reset()
	r.a.update(func() {
		if m := reply(r.th, r.at); m != nil {
			m.Text += s
		}
	})
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
	case "system":
		if ev.Subtype == "init" && ev.SessionID != "" {
			id := ev.SessionID
			r.a.update(func() { r.th.ClaudeID = id })
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
				r.a.update(func() {
					if m := reply(r.th, r.at); m != nil {
						m.Text += blk.Text
					}
				})
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
		note := fmt.Sprintf("%s in %.1fs · $%.4f · session %s", verb, ev.Duration/1000, ev.Cost, shortSession(r.th.ClaudeID))
		r.a.update(func() {
			if m := reply(r.th, r.at); m != nil {
				m.Blocks = append(m.Blocks, Block{Type: "reasoning", Text: note})
			}
		})
	}
}

// toolUse adds a card for a tool the model invoked. Edits render as a
// diff right away, computed from the call's own old/new strings.
func (r *claudeRun) toolUse(id, name string, rawInput json.RawMessage) {
	var in struct {
		Command  string `json:"command"`
		FilePath string `json:"file_path"`
		Pattern  string `json:"pattern"`
		Old      string `json:"old_string"`
		New      string `json:"new_string"`
		Content  string `json:"content"`
		Query    string `json:"query"`
	}
	_ = json.Unmarshal(rawInput, &in)

	disp := name
	isEdit := false
	var lines []DiffLine
	switch name {
	case "Bash":
		disp = "$ " + truncTitle(in.Command, 200)
	case "Read":
		disp = "read " + in.FilePath
	case "Edit":
		disp, isEdit = "edit "+in.FilePath, true
		lines = agent.UnifiedDiff(in.Old, in.New)
	case "Write":
		disp, isEdit = "write "+in.FilePath, true
		old := ""
		if data, err := os.ReadFile(in.FilePath); err == nil {
			old = string(data)
		}
		lines = agent.UnifiedDiff(old, in.Content)
	case "Grep":
		disp = "grep " + truncTitle(in.Pattern, 120)
	case "Glob":
		disp = "glob " + truncTitle(in.Pattern, 120)
	default:
		disp = truncTitle(name+" "+shortArgs(rawInput), 160)
	}

	r.a.update(func() {
		m := reply(r.th, r.at)
		if m == nil {
			return
		}
		b := Block{Type: "command", Text: disp, Running: true, Exit: -1}
		if isEdit {
			b.Edit = true
			if len(lines) > 0 {
				b.Type = "diff"
				b.File = in.FilePath
				b.Lines = lines
				for _, l := range lines {
					switch l.Kind {
					case '+':
						b.Add++
					case '-':
						b.Del++
					}
				}
			}
		}
		m.Blocks = append(m.Blocks, b)
		r.cards[id] = len(m.Blocks) - 1
	})
}

// toolResult fills the matching card with the tool's output.
func (r *claudeRun) toolResult(toolUseID string, rawContent json.RawMessage, isError bool) {
	r.a.update(func() {
		m := reply(r.th, r.at)
		if m == nil {
			return
		}
		bi, ok := r.cards[toolUseID]
		if !ok || bi >= len(m.Blocks) {
			return
		}
		b := &m.Blocks[bi]
		b.Running = false
		b.Exit = 0
		if isError {
			b.Exit = 1
		}
		b.Output = agent.TrimOutput(claudeContentText(rawContent), 16<<10)
		if b.Edit {
			// The diff card already shows what changed.
			b.Output = ""
		}
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
