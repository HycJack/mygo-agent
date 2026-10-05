package codex

import (
	"encoding/json"
	"strings"

	"mygo-agent/internal/harness"
)

// run maps codex events (exec --json lines or app-server items) onto the
// normalized harness.Event stream. It owns no thread state: the Host
// projector turns events into cards (spec/architecture.md).
type run struct {
	turn      harness.Turn
	emit      func(harness.Event)
	toolIDs   map[string]string           // exec item id -> tool-call id
	reasoning map[string]*strings.Builder // app-server: itemId -> delta buffer
	deltas    map[string]bool             // app-server: message items already streamed as deltas
	sawEvent  bool
}

func newRun(turn harness.Turn, emit func(harness.Event)) *run {
	return &run{turn: turn, emit: emit, toolIDs: map[string]string{}}
}

// codexEvent is one JSONL line of codex exec --json or one app-server
// item; only the fields the mapping consumes are declared, everything
// else is ignored.
type codexEvent struct {
	Type     string     `json:"type"`
	ThreadID string     `json:"thread_id"`
	Message  string     `json:"message"`
	Delta    string     `json:"delta"`
	Item     *codexItem `json:"item"`
}

type codexItem struct {
	ID               string   `json:"id"`
	Type             string   `json:"type"`
	Command          string   `json:"command"`
	Status           string   `json:"status"`
	AggregatedOutput string   `json:"aggregated_output"`
	ExitCode         *int     `json:"exit_code"`
	Text             string   `json:"text"`
	Summary          []string `json:"summary"`
	Diff             string   `json:"diff"`
	Changes          []struct {
		Path string `json:"path"`
		Kind string `json:"kind"`
	} `json:"changes"`
}

// handle parses and applies one exec --json stream-json line.
func (r *run) handle(line string) {
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
func (r *run) handleEvent(ev codexEvent) {
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
func (r *run) item(it codexItem) {
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
		// On the app-server wire the text already arrived as deltas; the
		// completed item repeats it whole, and appending both doubles the
		// reply. Only emit the whole text when no delta streamed it.
		if it.Text != "" && !r.deltas[it.ID] {
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
func (r *run) toolCall(itemID, command string) harness.ToolCall {
	var call harness.ToolCall
	call.ID = itemID
	call.Function.Name = "codex"
	call.Function.Arguments = command
	return call
}

// notify streams one app-server notification into the run, reusing the
// item→event mapping through handleEvent.
func (r *run) notify(method string, params json.RawMessage) {
	r.sawEvent = true
	switch {
	case method == "item/agentMessage/delta":
		var p struct {
			ItemID string `json:"itemId"`
			Delta  string `json:"delta"`
		}
		if json.Unmarshal(params, &p) == nil && p.Delta != "" {
			if r.deltas == nil {
				r.deltas = map[string]bool{}
			}
			r.deltas[p.ItemID] = true
			r.emit(harness.Event{Kind: harness.EventText, TextDelta: p.Delta})
		}
	case method == "item/reasoning/textDelta" || method == "item/reasoning/summaryTextDelta":
		var p struct {
			ItemID string `json:"itemId"`
			Delta  string `json:"delta"`
		}
		if json.Unmarshal(params, &p) == nil && p.Delta != "" {
			if r.reasoning == nil {
				r.reasoning = map[string]*strings.Builder{}
			}
			b := r.reasoning[p.ItemID]
			if b == nil {
				b = &strings.Builder{}
				r.reasoning[p.ItemID] = b
			}
			b.WriteString(p.Delta)
		}
	case method == "item/started" || method == "item/completed":
		// The app-server item wire is camelCase, unlike exec --json's
		// snake_case codexItem tags; decode through the alias and map.
		var p struct {
			Item struct {
				ID               string   `json:"id"`
				Type             string   `json:"type"`
				Text             string   `json:"text"`
				Summary          []string `json:"summary"`
				Command          string   `json:"command"`
				Status           string   `json:"status"`
				ExitCode         *int     `json:"exitCode"`
				Diff             string   `json:"diff"`
				AggregatedOutput string   `json:"aggregatedOutput"`
				Changes          []struct {
					Path string `json:"path"`
					Kind string `json:"kind"`
				} `json:"changes"`
			} `json:"item"`
		}
		if json.Unmarshal(params, &p) != nil {
			return
		}
		it := &p.Item
		// A reasoning item that completed with no text of its own falls
		// back to the deltas buffered for it.
		if it.Type == "reasoning" && strings.TrimSpace(it.Text) == "" && len(it.Summary) == 0 {
			if b := r.reasoning[it.ID]; b != nil {
				it.Text = b.String()
			}
		}
		item := codexItem{
			ID:               it.ID,
			Type:             it.Type,
			Text:             it.Text,
			Summary:          it.Summary,
			Command:          it.Command,
			Status:           it.Status,
			ExitCode:         it.ExitCode,
			Diff:             it.Diff,
			AggregatedOutput: it.AggregatedOutput,
		}
		for _, ch := range it.Changes {
			item.Changes = append(item.Changes, struct {
				Path string `json:"path"`
				Kind string `json:"kind"`
			}{ch.Path, ch.Kind})
		}
		r.handleEvent(codexEvent{Item: &item})
	case method == "error":
		var p struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(params, &p)
		r.handleEvent(codexEvent{Type: "error", Message: p.Message})
	}
}
