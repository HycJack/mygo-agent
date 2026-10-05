package app

import (
	"encoding/json"
	"strings"

	"mygo-agent/internal/harness"
)

// applyEvent is the Host's projector (spec/architecture.md): the one
// place a harness event becomes message state. Every backend emits the
// same normalized stream; nothing else writes blocks during a turn.
func (a *app) applyEvent(th *Thread, at int, kind string, ev harness.Event) {
	a.update(func() {
		m := reply(th, at)
		if m == nil {
			return
		}
		switch ev.Kind {
		case harness.EventText:
			if ev.TextDelta != "" {
				m.Text += ev.TextDelta
			}
		case harness.EventSession:
			if ev.SessionID == "" {
				return
			}
			switch kind {
			case "codex":
				th.CodexID = ev.SessionID
			case "claude":
				th.ClaudeID = ev.SessionID
			}
			a.saveThread(th)
		case harness.EventToolStart:
			b := Block{Type: "command", Text: ev.Text, Running: true, Exit: -1, ToolID: ev.ToolCall.ID}
			if ev.Edit {
				b.Edit = true
			}
			if ev.Diff != "" {
				// An edit preview: the diff is the card.
				b.Type = "diff"
				b.File = ev.File
				b.Lines = parseToolDiff(ev.Diff)
				for _, l := range b.Lines {
					switch l.Kind {
					case '+':
						b.Add++
					case '-':
						b.Del++
					}
				}
			}
			m.Blocks = append(m.Blocks, b)
		case harness.EventToolEnd:
			// Match the card a tool_start opened: by ToolID when the
			// harness carries one, else the newest running command card.
			bi := -1
			for i := len(m.Blocks) - 1; i >= 0; i-- {
				b := &m.Blocks[i]
				if (b.Type != "command" && b.Type != "diff") || !b.Running {
					continue
				}
				if ev.ToolCall.ID != "" && b.ToolID == ev.ToolCall.ID {
					bi = i
					break
				}
				if ev.ToolCall.ID == "" {
					bi = i
					break
				}
				if bi < 0 {
					bi = i // remember the newest as a fallback
				}
			}
			if bi < 0 {
				return
			}
			b := &m.Blocks[bi]
			b.Running = false
			b.Exit = ev.Exit
			b.Ms = ev.Ms
			if b.Type == "command" {
				b.Output = harness.TrimOutput(ev.Output, 16<<10)
				b.Open = b.Edit
			}
			// A diff card already shows what changed; leave it be.
		case harness.EventFileChange:
			b := Block{Type: "diff", Open: true}
			if ev.File != "" {
				b.File = ev.File
			} else if len(ev.Paths) > 0 {
				b.File = strings.Join(ev.Paths, ", ")
			}
			if ev.Diff != "" {
				b.Lines = harness.ParseUnifiedDiff(ev.Diff)
				for _, l := range b.Lines {
					switch l.Kind {
					case '+':
						b.Add++
					case '-':
						b.Del++
					}
				}
			}
			m.Blocks = append(m.Blocks, b)
		case harness.EventReasoning, harness.EventNote:
			if ev.Text != "" {
				m.Blocks = append(m.Blocks, Block{Type: "reasoning", Text: ev.Text})
			}
		case harness.EventError:
			if ev.Err != "" {
				m.Blocks = append(m.Blocks, Block{Type: "error", Text: ev.Err})
			}
		}
	})
}

// toolDisplayFor renders a tool call as the one-line card title.
func toolDisplayFor(call harness.ToolCall) string {
	var in map[string]any
	_ = json.Unmarshal([]byte(call.Function.Arguments), &in)
	get := func(k string) string {
		if s, ok := in[k].(string); ok {
			return s
		}
		return ""
	}
	short := func(s string, n int) string {
		if len(s) > n {
			return s[:n] + "…"
		}
		return s
	}
	switch call.Function.Name {
	case "bash":
		return "$ " + short(get("command"), 200)
	case "read_file":
		return "read_file " + get("path")
	case "edit_file":
		return "edit_file " + get("path")
	case "list_files":
		return "list_files " + get("path")
	case "grep":
		return "grep " + short(get("pattern"), 120)
	case "read_skill":
		return "read_skill " + get("name")
	default:
		if rest, ok := strings.CutPrefix(call.Function.Name, "mcp_"); ok {
			return rest + " " + short(call.Function.Arguments, 160)
		}
		return call.Function.Name + " " + short(call.Function.Arguments, 160)
	}
}
