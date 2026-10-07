package app

import (
	"encoding/json"
	"strings"

	"mygo-agent/internal/harness"
	"mygo-agent/internal/harness/cli"
)

// applyEvent is the Host's projector (spec/architecture.md): the one
// place a harness event becomes message state. Every backend emits the
// same normalized stream; nothing else writes blocks during a turn.
func (a *app) applyEvent(th *Thread, at int, kind string, ev harness.Event) {
	a.update(func() { a.projectEvent(th, at, kind, ev) })
}

// projectEvent is applyEvent's body, for callers that already hold the
// main-thread lock. The built-in backend batches text on its own 40ms
// ticker and must reach the same projector without nesting update() —
// which in a headless run would take the same non-reentrant mutex twice.
func (a *app) projectEvent(th *Thread, at int, kind string, ev harness.Event) {
	m := reply(th, at)
	if m == nil {
		return
	}
	// A settled turn accepts no more events. The Host calls finish the
	// instant Run returns, so an adapter whose reader goroutine outlives
	// it would otherwise write into a message already marked done and
	// re-saved. Cheap, and it makes the rule hold for every adapter
	// rather than trusting each one to stop on time.
	if !m.Running {
		return
	}
	// The trace rides the projector: the one writer, every backend's
	// events in one format (spec/agents.md, tracing).
	if te, ok := traceHarnessEvent(ev); ok {
		a.appendTrace(th, te)
	}
	switch ev.Kind {
	case harness.EventText:
		if ev.TextDelta == "" {
			return
		}
		// The prose joins the ORDERED sequence, not a separate field
		// rendered afterwards. An agent that says "let me look", runs a
		// tool, then says "found it" has to read in that order; keeping
		// text in one string made interleaving impossible and every
		// turn rendered as all-cards-then-all-prose.
		//
		// A new stretch of prose starts only after a CARD. Two text
		// events with nothing between them are one paragraph — a gateway
		// that emits an assistant message per content block would
		// otherwise turn one sentence into two, and a chunked stream into
		// a hundred.
		n := len(m.Blocks)
		if n == 0 || m.Blocks[n-1].Type == blockText {
			if n == 0 {
				m.Blocks = append(m.Blocks, Block{Type: blockText})
				n = 1
			}
			m.Blocks[n-1].Text += ev.TextDelta
			m.Text += ev.TextDelta
			return
		}
		// The aggregate is what the copy button hands over and what
		// reseeds the built-in transcript, so a card interrupting the
		// prose has to leave a paragraph break in it — two fragments run
		// together would be neither readable nor a valid document.
		m.Text = strings.TrimRight(m.Text, "\n") + "\n\n" + ev.TextDelta
		m.Blocks = append(m.Blocks, Block{Type: blockText, Text: ev.TextDelta})
	case harness.EventSession:
		if ev.SessionID == "" {
			return
		}
		switch kind {
		case "codex":
			th.CodexID = ev.SessionID
		case "claude":
			th.ClaudeID = ev.SessionID
		case "pi":
			th.PiID = ev.SessionID
		}
		a.saveThread(th)
	case harness.EventToolStart:
		// The card title is redacted HERE, once, rather than in each
		// adapter: every backend puts its own text in Event.Text, and
		// the card is persisted into the thread file. Redacting at the
		// producers meant whichever one was missed was the one that
		// leaked.
		b := Block{Type: blockCommand, Text: cli.Redact(ev.Text), Running: true, Exit: -1, ToolID: ev.ToolCall.ID}
		if ev.Edit {
			b.Edit = true
		}
		if ev.Diff != "" {
			// An edit preview: the diff is the card.
			b.Type = blockDiff
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
		// Only a diff card counts. Bumping unconditionally made the
		// header's "N changed" grow with every bash call, and because
		// the bump also cleared the stale flag the wrong number could
		// never self-heal.
		if b.Type == blockDiff {
			th.noteDiffBlock()
		}
		m.turnTools++
	case harness.EventToolEnd:
		// Match the card a tool_start opened: by ToolID when the
		// harness carries one, else the newest running command card.
		bi := -1
		for i := len(m.Blocks) - 1; i >= 0; i-- {
			b := &m.Blocks[i]
			if (b.Type != blockCommand && b.Type != blockDiff) || !b.Running {
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
		if b.Type == blockCommand {
			b.Output = cli.TrimOutput(ev.Output, 16<<10)
			// A multi-line command is its own deliverable — a heredoc
			// file-write carries the content in the command text, and a
			// relay member's writes are the work the user wants to see —
			// so it opens on finish instead of hiding behind one
			// truncated title line.
			b.Open = b.Edit || strings.Contains(b.Text, "\n")
		}
		// A diff card already shows what changed; leave it be.
	case harness.EventFileChange:
		b := Block{Type: blockDiff, Open: true}
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
		th.noteDiffBlock()
	case harness.EventReasoning:
		if ev.Text != "" {
			m.Blocks = append(m.Blocks, Block{Type: blockReasoning, Text: ev.Text})
		}
	case harness.EventNote:
		// A note is the harness reporting on the turn (a turn-limit
		// notice, a decline); it is not the agent thinking. Keeping
		// them apart is what lets the view fold one and not the other.
		if ev.Text != "" {
			m.Blocks = append(m.Blocks, Block{Type: blockNote, Text: ev.Text})
		}
		m.turnTokens += ev.Tokens
		m.turnCost += ev.CostUSD
	case harness.EventError:
		if ev.Err != "" {
			m.Blocks = append(m.Blocks, Block{Type: blockError, Text: ev.Err})
		}
	}
}

// toolDisplayFor renders a tool call as the one-line card title. The
// text is redacted: a card is persisted into the thread file, and a
// command line is the most likely place for a token to appear. Redaction
// belongs in the truncating helper, not only in the keyed reader — an MCP
// tool's arguments are free-form and never pass through get().
func toolDisplayFor(call harness.ToolCall) string {
	var in map[string]any
	_ = json.Unmarshal([]byte(call.Function.Arguments), &in)
	get := func(k string) string {
		if s, ok := in[k].(string); ok {
			return cli.Redact(s)
		}
		return ""
	}
	// Redact before truncating: a cut can otherwise leave the head of a
	// secret on the card.
	short := func(s string, n int) string {
		return cli.Trunc(cli.Redact(s), n)
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
