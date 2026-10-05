package app

import (
	"encoding/json"
	"strings"

	"mygo-agent/internal/harness"
)

// codexRun maps codex events (exec --json lines or app-server items) onto
// the normalized harness.Event stream. It owns no thread state: the Host
// projector turns events into cards (spec/architecture.md).
type codexRun struct {
	emit      func(harness.Event)
	toolIDs   map[string]string           // exec item id -> tool-call id
	reasoning map[string]*strings.Builder // app-server: itemId -> delta buffer
	sawEvent  bool
}

func newCodexRun(emit func(harness.Event)) *codexRun {
	return &codexRun{emit: emit, toolIDs: map[string]string{}}
}

// handle parses and applies one exec --json stream-json line.
func (r *codexRun) handle(line string) {
	if line == "" {
		return
	}
	var ev codexEvent
	if json.Unmarshal([]byte(line), &ev) != nil {
		return
	}
	r.handleEvent(ev)
}

// handleEvent maps one parsed codex event to a normalized Event.
func (r *codexRun) handleEvent(ev codexEvent) {
	r.sawEvent = true
	switch {
	case ev.Type == "thread.started" && ev.ThreadID != "":
		r.emit(harness.Event{Kind: harness.EventSession, SessionID: ev.ThreadID})
	case strings.HasSuffix(ev.Type, ".delta") && ev.Delta != "":
		r.emit(harness.Event{Kind: harness.EventText, TextDelta: ev.Delta})
	case ev.Item != nil:
		r.item(*ev.Item)
	case ev.Type == "error" || ev.Type == "turn.failed":
		msg := ev.Message
		if msg == "" {
			msg = "The agent failed."
		}
		r.emit(harness.Event{Kind: harness.EventError, Err: msg})
	case ev.Type == "turn.completed" || ev.Type == "thread.completed":
		// The reply is complete; the transport decides when reading ends.
	}
}

// item maps one codex item (either wire's shape) onto events.
func (r *codexRun) item(it codexItem) {
	switch it.Type {
	case "command_execution":
		r.emit(harness.Event{
			Kind:     harness.EventToolStart,
			ToolCall: r.toolCall(it.ID, it.Command),
			Text:     "$ " + it.Command,
		})
		if it.AggregatedOutput != "" || it.ExitCode != nil || it.Status != "in_progress" {
			exit := 0
			if it.ExitCode != nil {
				exit = *it.ExitCode
			}
			r.emit(harness.Event{
				Kind:     harness.EventToolEnd,
				ToolCall: r.toolCall(it.ID, it.Command),
				Text:     "$ " + it.Command,
				Output:   it.AggregatedOutput,
				Exit:     exit,
			})
		}
	case "file_change":
		var paths []string
		for _, ch := range it.Changes {
			paths = append(paths, ch.Path)
		}
		r.emit(harness.Event{
			Kind:  harness.EventFileChange,
			File:  strings.Join(paths, ", "),
			Paths: paths,
			Diff:  it.Diff,
		})
	case "agent_message":
		if it.Text != "" {
			r.emit(harness.Event{Kind: harness.EventText, TextDelta: it.Text})
		}
	case "reasoning":
		text := it.Text
		if text == "" && len(it.Summary) > 0 {
			text = strings.Join(it.Summary, " ")
		}
		if text != "" {
			r.emit(harness.Event{Kind: harness.EventReasoning, Text: text})
		}
	case "error":
		if it.Text != "" {
			r.emit(harness.Event{Kind: harness.EventError, Err: it.Text})
		}
	}
}

// toolCall fabricates the stable identity the projector matches on: exec
// items have no native tool-call id, so the item id is reused.
func (r *codexRun) toolCall(itemID, command string) harness.ToolCall {
	var call harness.ToolCall
	call.ID = itemID
	call.Function.Name = "codex"
	call.Function.Arguments = command
	return call
}
