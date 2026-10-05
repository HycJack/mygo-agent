package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestTurnLimitEmitsNoteEvent: the loop stops at the turn budget and the
// Host swallows ErrTurnLimit, so this event is the only place the user
// learns why. Its kind has to be one the projector switches on, and its
// message has to travel in Text.
func TestTurnLimitEmitsNoteEvent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"c1\","+
			"\"function\":{\"name\":\"noop\",\"arguments\":\"{}\"}}]}}]}\n\n"+
			"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n"+
			"data: [DONE]\n\n")
	}))
	defer srv.Close()
	ran := 0
	tools := []Tool{{
		Name:       "noop",
		Actions:    []Action{ActionFileRead}, // allowed in every mode
		Parameters: json.RawMessage(`{"type":"object"}`),
		Execute:    func(ctx context.Context, args string) (string, error) { ran++; return "ok", nil },
	}}

	var notes []string
	_, err := Run(t.Context(), LoopConfig{
		BaseURL: srv.URL, Model: "m", Tools: tools, MaxTurns: 2,
		Policy: Policy{Mode: ModeFull},
		OnEvent: func(e Event) {
			if e.Kind == EventNote {
				notes = append(notes, e.Text)
			}
		},
	}, []ChatMessage{{Role: "user", Content: "go"}})
	if !errors.Is(err, ErrTurnLimit) {
		t.Fatalf("want the turn-limit stop, got %v", err)
	}
	if ran != 2 {
		t.Fatalf("the loop must stop at MaxTurns: %d runs", ran)
	}
	if len(notes) != 1 {
		t.Fatalf("the stop must be announced once, got %d notes", len(notes))
	}
	if !strings.Contains(notes[0], "turn limit") {
		t.Fatalf("the note does not say why the turn stopped: %q", notes[0])
	}
}
