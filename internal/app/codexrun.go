package app

import (
	"encoding/json"
	"strings"

	"mygo-agent/internal/agent"
)

// codexRun carries one codex exec run's state between stream-json
// events. handle applies one line; it is a method so tests can feed
// recorded sessions and assert the thread state.
type codexRun struct {
	a        *app
	th       *Thread
	at       int
	blocks   map[string]int // event item id -> block index
	sawEvent bool
}

// handle parses and applies one stream-json line.
func (r *codexRun) handle(line string) {
	if line == "" {
		return
	}
	var ev codexEvent
	if json.Unmarshal([]byte(line), &ev) != nil {
		return
	}
	r.sawEvent = true
	switch {
	case ev.Type == "thread.started" && ev.ThreadID != "":
		id := ev.ThreadID
		r.a.update(func() {
			r.th.CodexID = id
			r.a.save()
		})
	case strings.HasSuffix(ev.Type, ".delta") && ev.Delta != "":
		delta := ev.Delta
		r.a.update(func() {
			if m := reply(r.th, r.at); m != nil {
				m.Text += delta
			}
		})
	case ev.Item != nil:
		r.codexItem(ev)
	case ev.Type == "error" || ev.Type == "turn.failed":
		msg := ev.Message
		if msg == "" {
			msg = "The agent failed."
		}
		r.a.update(func() {
			if m := reply(r.th, r.at); m != nil {
				m.Blocks = append(m.Blocks, Block{Type: "error", Text: msg})
			}
		})
	case ev.Type == "turn.completed" || ev.Type == "thread.completed":
		// The reply is complete; keep reading for more events.
	}
}

// codexItem maps one item.started/updated/completed event to a card.
func (r *codexRun) codexItem(ev codexEvent) {
	id := ev.Item.ID
	it := ev.Item
	th, at := r.th, r.at
	blocks := r.blocks
	switch it.Type {
	case "command_execution":
		r.a.update(func() {
			m := reply(th, at)
			if m == nil {
				return
			}
			bi, ok := blocks[id]
			if !ok {
				m.Blocks = append(m.Blocks, Block{Type: "command", Text: it.Command, Running: true, Exit: -1})
				blocks[id] = len(m.Blocks) - 1
				bi = blocks[id]
			}
			b := &m.Blocks[bi]
			b.Text = it.Command
			if it.AggregatedOutput != "" {
				b.Output = it.AggregatedOutput
			}
			if it.ExitCode != nil {
				b.Exit = *it.ExitCode
			}
			if it.Status != "in_progress" {
				b.Running = false
				if b.Exit == -1 {
					b.Exit = 0
				}
				b.Open = strings.TrimSpace(b.Output) != ""
			}
		})
	case "file_change":
		r.a.update(func() {
			m := reply(th, at)
			if m == nil {
				return
			}
			bi, ok := blocks[id]
			if !ok {
				m.Blocks = append(m.Blocks, Block{Type: "diff"})
				blocks[id] = len(m.Blocks) - 1
				bi = blocks[id]
			}
			b := &m.Blocks[bi]
			b.Type = "diff"
			b.Open = true
			if len(it.Changes) > 0 {
				var paths []string
				for _, ch := range it.Changes {
					paths = append(paths, ch.Path)
				}
				b.File = strings.Join(paths, ", ")
			}
			if it.Diff != "" {
				b.Lines = agent.ParseUnifiedDiff(it.Diff)
				for _, l := range b.Lines {
					switch l.Kind {
					case '+':
						b.Add++
					case '-':
						b.Del++
					}
				}
			}
		})
	case "agent_message":
		text := it.Text
		r.a.update(func() {
			if m := reply(th, at); m != nil && text != "" {
				m.Text = text
			}
		})
	case "reasoning":
		text := it.Text
		if text == "" && len(it.Summary) > 0 {
			text = strings.Join(it.Summary, " ")
		}
		if text == "" {
			return
		}
		r.a.update(func() {
			if m := reply(th, at); m != nil {
				m.Blocks = append(m.Blocks, Block{Type: "reasoning", Text: text})
			}
		})
	case "error":
		text := it.Text
		r.a.update(func() {
			if m := reply(th, at); m != nil && text != "" {
				m.Blocks = append(m.Blocks, Block{Type: "error", Text: text})
			}
		})
	}
}
