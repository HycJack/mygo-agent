package codex

import (
	"encoding/json"
	"strings"
	"sync"
	"time"

	"mygo-agent/internal/harness"
	"mygo-agent/internal/harness/cli"
)

// run maps codex events (exec --json lines or app-server items) onto the
// normalized harness.Event stream. It owns no thread state: the Host
// projector turns events into cards (spec/architecture.md).
type run struct {
	turn      harness.Turn
	emit      func(harness.Event)
	reasoning map[string]*strings.Builder // app-server: itemId -> delta buffer
	deltas    map[string]bool             // app-server: message items already streamed as deltas
	started   map[string]time.Time        // app-server: item id -> when its start card opened
	sawEvent  bool                        // something usable was emitted
	garbage   int                         // frames dropped because their params did not parse
	fail      string                      // reason from turn/failed, if the turn failed

	// approvalTimeout bounds one approval wait; zero uses the shared
	// harness.DefaultApprovalTimeout.
	approvalTimeout time.Duration

	mu   sync.Mutex // guards done and the emit call itself
	done bool       // the run settled; later events are dropped
}

func newRun(turn harness.Turn, emit func(harness.Event)) *run {
	return &run{turn: turn, emit: emit}
}

// send is the only path to the Host's emit. It drops the event once the
// run has settled: the Host finishes the thread the instant Run returns
// (internal/app/backends.go), so a late event would land in a thread
// that is already saved and marked done.
func (r *run) send(ev harness.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.done {
		return
	}
	// Only a produced event counts as a turn having produced something;
	// a stream of unparseable frames is not a clean turn.
	r.sawEvent = true
	if r.emit != nil {
		r.emit(ev)
	}
}

// seal marks the run settled. After it, send delivers nothing, so no
// event can reach the Host after Run returns.
func (r *run) seal() {
	r.mu.Lock()
	r.done = true
	r.mu.Unlock()
}

// unreadable counts a frame that had to be dropped, so the transport can
// tell a silent turn from a clean one.
func (r *run) unreadable() {
	r.mu.Lock()
	r.garbage++
	r.mu.Unlock()
}

// failTurn records the app-server's own reason and says it on the stream:
// a reply that simply stops has no explanation. The lock is the one send
// takes, so the transport can read the reason back without racing the
// reader goroutine.
func (r *run) failTurn(reason string) {
	if reason == "" {
		reason = "The agent failed."
	}
	r.mu.Lock()
	r.fail = reason
	r.mu.Unlock()
	r.send(harness.Event{Kind: harness.EventError, Err: reason})
}

// outcome is a locked snapshot of the run's terminal bookkeeping, read
// after seal.
func (r *run) outcome() (fail string, saw bool, garbage int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.fail, r.sawEvent, r.garbage
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
	switch {
	case ev.Type == "thread.started" && ev.ThreadID != "":
		r.send(harness.Event{Kind: harness.EventSession, SessionID: ev.ThreadID})
	case strings.HasSuffix(ev.Type, ".delta") && ev.Delta != "":
		r.send(harness.Event{Kind: harness.EventText, TextDelta: ev.Delta})
	case ev.Item != nil:
		r.item(*ev.Item, !strings.HasSuffix(ev.Type, "started"))
	case ev.Type == "error" || ev.Type == "turn.failed":
		msg := ev.Message
		if msg == "" {
			msg = "The agent failed."
		}
		r.send(harness.Event{Kind: harness.EventError, Err: msg})
	case ev.Type == "turn.completed" || ev.Type == "thread.completed":
		// The reply is complete; the transport decides when reading ends.
	}
}

// normalizeItemType folds the two spellings of an item type onto one.
// The app-server wire names items in camelCase (`commandExecution`,
// `agentMessage`, `fileChange`) while the exec `--json` wire uses
// snake_case; matching only one spelling silently mapped a real
// `agentMessage` or `fileChange` to nothing at all.
func normalizeItemType(t string) string {
	switch t {
	case "commandExecution":
		return "command_execution"
	case "fileChange":
		return "file_change"
	case "agentMessage":
		return "agent_message"
	case "tokenCount", "token_count":
		return "token_count"
	case "contextCompaction":
		return "context_compaction"
	default:
		return t
	}
}

// item maps one codex item (either wire's shape) onto events. Both
// wires announce an item twice — when it starts and when it completes —
// so completed says which of the two frames this is.
func (r *run) item(it codexItem, completed bool) {
	switch normalizeItemType(it.Type) {
	case "command_execution":
		if r.started == nil {
			r.started = map[string]time.Time{}
		}
		// The Host projector opens a block per tool_start and closes the
		// newest one on tool_end, so a second start for the same item id
		// would leave the first block running forever. Open it once.
		opened, ok := r.started[it.ID]
		if !ok {
			opened = time.Now()
			r.started[it.ID] = opened
			r.send(harness.Event{
				Kind:     harness.EventToolStart,
				ToolCall: r.toolCall(it.ID, it.Command),
				Text:     "$ " + it.Command,
			})
		}
		if completed {
			exit := 0
			if it.ExitCode != nil {
				exit = *it.ExitCode
			}
			// The wire carries no duration, so measure the card's own
			// elapsed time — the projector shows it as the tool's Ms.
			r.send(harness.Event{
				Kind:     harness.EventToolEnd,
				ToolCall: r.toolCall(it.ID, it.Command),
				Text:     "$ " + it.Command,
				Output:   it.AggregatedOutput,
				Exit:     exit,
				Ms:       time.Since(opened).Milliseconds(),
			})
		}
	case "file_change":
		var paths []string
		for _, ch := range it.Changes {
			paths = append(paths, ch.Path)
		}
		r.send(harness.Event{
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
			r.send(harness.Event{Kind: harness.EventText, TextDelta: it.Text})
		}
	case "reasoning":
		text := it.Text
		if text == "" && len(it.Summary) > 0 {
			text = strings.Join(it.Summary, " ")
		}
		if text != "" {
			r.send(harness.Event{Kind: harness.EventReasoning, Text: text})
		}
	case "context_compaction":
		// codex folds its own context when the thread outgrows the window.
		// It announces this as an item — not as a thread/compacted
		// notification, which is why reading the binary's string table is
		// not enough and the item type is what has to be matched. The item
		// is announced twice (started, completed); the note belongs to the
		// completed frame, or one compaction would be reported twice.
		if completed {
			r.send(harness.Event{
				Kind: harness.EventNote,
				Text: cli.CompactedNotice("codex", 0, 0),
			})
		}
	case "error":
		if it.Text != "" {
			r.send(harness.Event{Kind: harness.EventError, Err: it.Text})
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
// itemKey accepts both spellings of an item id. The app-server wire uses
// itemId and the exec wire item_id; reading only one meant a spelling
// change on the wire silently stopped de-duplicating a message, which
// doubled every reply instead of failing.
func itemKey(camel, snake string) string {
	if camel != "" {
		return camel
	}
	return snake
}

func (r *run) notify(method string, params json.RawMessage) {
	switch {
	case method == "item/agentMessage/delta":
		var p struct {
			ItemID      string `json:"itemId"`
			ItemIDSnake string `json:"item_id"`
			Delta       string `json:"delta"`
		}
		if json.Unmarshal(params, &p) == nil && p.Delta != "" {
			if r.deltas == nil {
				r.deltas = map[string]bool{}
			}
			r.deltas[itemKey(p.ItemID, p.ItemIDSnake)] = true
			r.send(harness.Event{Kind: harness.EventText, TextDelta: p.Delta})
		}
	case method == "item/reasoning/textDelta" || method == "item/reasoning/summaryTextDelta":
		var p struct {
			ItemID      string `json:"itemId"`
			ItemIDSnake string `json:"item_id"`
			Delta       string `json:"delta"`
		}
		if json.Unmarshal(params, &p) == nil && p.Delta != "" {
			if r.reasoning == nil {
				r.reasoning = map[string]*strings.Builder{}
			}
			key := itemKey(p.ItemID, p.ItemIDSnake)
			b := r.reasoning[key]
			if b == nil {
				b = &strings.Builder{}
				r.reasoning[key] = b
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
			r.unreadable()
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
		// The slash method name carries which of the two frames this is,
		// the same way exec --json's item type does.
		r.handleEvent(codexEvent{
			Type: "item." + strings.TrimPrefix(method, "item/"),
			Item: &item,
		})
	case method == "turn/failed":
		// The turn settled as a failure: say so on the stream and keep
		// the text for Run to return as the turn's error.
		r.failTurn(turnFailure(params))
	case method == "error":
		var p struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(params, &p) != nil {
			r.unreadable()
			return
		}
		r.handleEvent(codexEvent{Type: "error", Message: p.Message})
	}
}

// turnFailure decodes a turn/failed payload. The app-server nests the
// reason under error; it may be a message object or a bare string, and
// older shapes carry a top-level message or reason instead.
func turnFailure(params json.RawMessage) string {
	var p struct {
		Error   json.RawMessage `json:"error"`
		Message string          `json:"message"`
		Reason  string          `json:"reason"`
	}
	if json.Unmarshal(params, &p) != nil {
		return ""
	}
	if len(p.Error) > 0 {
		var obj struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(p.Error, &obj) == nil && obj.Message != "" {
			return obj.Message
		}
		var text string
		if json.Unmarshal(p.Error, &text) == nil && text != "" {
			return text
		}
	}
	if p.Message != "" {
		return p.Message
	}
	return p.Reason
}
