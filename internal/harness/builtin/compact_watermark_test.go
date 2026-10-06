package builtin

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"mygo-agent/internal/harness"
)

// The watermark arms summarising compaction: past four fifths of a
// declared window, the transcript is summarised by the model itself
// instead of dropped, and the note says so. These tests drive the real
// loop against fake providers, because the trigger lives between
// rounds, not inside any one call.

// sseBody builds one chat-completions SSE answer: text + usage.
func sseBody(text string, prompt, completion int) string {
	delta := ""
	if text != "" {
		delta = "data: " + `{"choices":[{"delta":{"content":"` + text + `"}}]}` + "\n\n"
	}
	return delta + "data: " + `{"choices":[],"usage":{"prompt_tokens":` + itoa(prompt) +
		`,"completion_tokens":` + itoa(completion) + `}}` + "\n\ndata: [DONE]\n\n"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// TestWatermarkSummarisesInsteadOfDropping drives a full loop past the
// watermark: the first response reports 9000 prompt tokens against a
// 10000-token window, so round two must open with a summarising call —
// no tools, a summarising system prompt — and continue from a
// transcript that keeps its system head, one summary message and the
// recent tail.
func TestWatermarkSummarisesInsteadOfDropping(t *testing.T) {
	var notes atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Role    string `json:"role"`
				Content any    `json:"content"`
			} `json:"messages"`
			Tools         any `json:"tools"`
			StreamOptions any `json:"stream_options"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		if body.StreamOptions == nil {
			t.Error("a declared window must arm stream_options.include_usage")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		// The summarising call: system asks for a summary, no tools.
		if len(body.Messages) > 0 {
			if sys, _ := body.Messages[0].Content.(string); strings.Contains(sys, "summarise") && body.Tools == nil {
				// The summary's facts come from the calls' arguments,
				// not the results: the input must name the command.
				var input string
				if len(body.Messages) > 1 {
					input, _ = body.Messages[1].Content.(string)
				}
				if !strings.Contains(input, "echo hello-from-tool") {
					t.Errorf("the summariser input lost the call's arguments: %.120q", input)
				}
				io.WriteString(w, sseBody("The user wants the parser fixed; two files were edited; tests still fail.", 500, 60))
				return
			}
		}
		// A transcript that already carries the summary: the work is done.
		for _, m := range body.Messages {
			if s, _ := m.Content.(string); strings.Contains(s, "[context compacted") {
				io.WriteString(w, sseBody("all done", 2000, 5))
				return
			}
		}
		// Without the marker: answer with a tool call and usage over
		// the watermark — the tool call is what keeps the loop running
		// into the round where the watermark fires.
		io.WriteString(w, "data: "+`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1",`+
			`"function":{"name":"noop","arguments":"{\"command\":\"echo hello-from-tool\"}"}}]},"finish_reason":"tool_calls"}]}`+"\n\n"+
			"data: "+`{"choices":[],"usage":{"prompt_tokens":9000,"completion_tokens":10}}`+"\n\ndata: [DONE]\n\n")
	}))
	defer srv.Close()

	messages := []harness.ChatMessage{{Role: "system", Content: "you are a coding agent"}}
	// Tool exchanges in the MIDDLE of the transcript: the summary's
	// input must carry their calls' arguments.
	for i := 0; i < 6; i++ {
		messages = append(messages,
			harness.ChatMessage{Role: "assistant", ToolCalls: []harness.ToolCall{{
				ID: "c" + itoa(i),
				Function: struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				}{Name: "bash", Arguments: `{"command":"echo hello-from-tool"}`},
			}}},
			harness.ChatMessage{Role: "tool", Content: "out", ToolCallID: "c" + itoa(i)},
		)
	}
	for i := 0; i < 6; i++ {
		messages = append(messages,
			harness.ChatMessage{Role: "user", Content: strings.Repeat("pad ", 20) + itoa(i)},
			harness.ChatMessage{Role: "assistant", Content: strings.Repeat("reply ", 20) + itoa(i)},
		)
	}
	messages = append(messages, harness.ChatMessage{Role: "user", Content: "watermark-prompt: fix the parser"})

	tool := Tool{Name: "noop", Actions: []harness.Action{harness.ActionFileRead},
		Parameters: obj(`{}`), Execute: func(context.Context, string) (string, error) { return "", nil }}
	res, err := Run(t.Context(), LoopConfig{
		BaseURL: srv.URL, Model: "m",
		ContextWindow: 10000,
		Tools:         []Tool{tool},
		MaxTurns:      6,
		OnEvent: func(e harness.Event) {
			if e.Kind == harness.EventNote && strings.Contains(e.Text, "compacted") {
				notes.Add(1)
			}
		},
	}, messages)
	if err != nil {
		t.Fatal(err)
	}
	if notes.Load() == 0 {
		t.Fatal("the compaction note was never emitted")
	}
	// The transcript keeps its system head...
	if sys, _ := res[0].Content.(string); !strings.Contains(sys, "coding agent") {
		t.Fatalf("the system head was lost: %v", res[0].Role)
	}
	// ...exactly one compaction marker...
	markers := 0
	for _, m := range res {
		if s, _ := m.Content.(string); strings.Contains(s, "[context compacted") {
			markers++
		}
	}
	if markers != 1 {
		t.Fatalf("got %d compaction markers, want 1", markers)
	}
	// ...and the recent tail keeps the current turn verbatim: the prompt
	// sits inside the tail window (the very last message is the reply
	// the loop appended after compaction).
	found := false
	for _, m := range res[len(res)-compactTailSize:] {
		if s, _ := m.Content.(string); strings.Contains(s, "watermark-prompt") {
			found = true
		}
	}
	if !found {
		t.Fatal("the tail lost the current prompt")
	}
}

// TestWatermarkWithoutUsageKeepsCountCompaction: no window declared, the
// count backstop works exactly as before and nothing summarises.
func TestWatermarkWithoutUsageKeepsCountCompaction(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			StreamOptions any `json:"stream_options"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if body.StreamOptions != nil {
			t.Error("an undeclared window must not send stream_options")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, sseBody("done", 0, 0))
	}))
	defer srv.Close()

	messages := []harness.ChatMessage{{Role: "user", Content: "hi"}}
	if _, err := Run(t.Context(), LoopConfig{
		BaseURL: srv.URL, Model: "m", MaxTurns: 1,
	}, messages); err != nil {
		t.Fatal(err)
	}
}

// TestAutoCompactFallsBackWhenTheSummariserFails pins the backstop: a
// failed summarising call must not fail the turn — the loop falls back
// to dropping turns.
func TestAutoCompactFallsBackWhenTheSummariserFails(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Content any `json:"content"`
			} `json:"messages"`
			Tools any `json:"tools"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if len(body.Messages) > 0 {
			if sys, _ := body.Messages[0].Content.(string); strings.Contains(sys, "summarise") {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
		}
		if calls.Add(1) == 1 {
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, sseBody("working", 9000, 10))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, sseBody("done anyway", 100, 5))
	}))
	defer srv.Close()

	messages := []harness.ChatMessage{{Role: "system", Content: "sys"}}
	for i := 0; i < 6; i++ {
		messages = append(messages,
			harness.ChatMessage{Role: "user", Content: strings.Repeat("pad ", 20) + itoa(i)},
			harness.ChatMessage{Role: "assistant", Content: strings.Repeat("reply ", 20) + itoa(i)},
		)
	}
	res, err := Run(t.Context(), LoopConfig{
		BaseURL: srv.URL, Model: "m", ContextWindow: 10000, MaxTurns: 4,
	}, messages)
	if err != nil {
		t.Fatalf("a failed summariser must not fail the turn: %v", err)
	}
	for _, m := range res {
		if s, _ := m.Content.(string); strings.Contains(s, "[context compacted") {
			t.Fatal("the fallback produced no summary, yet one appeared")
		}
	}
}

// TestAutoCompactNeverOrphansAToolResult pins the one cut boundary that
// matters: the kept window must not start on a tool result — a result
// whose call is summarised away is an invalid exchange both wires
// refuse, and the broken transcript is the one that gets stored. The
// naive cut at len-tail lands on exactly such a result here.
func TestAutoCompactNeverOrphansAToolResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Role    string `json:"role"`
				Content any    `json:"content"`
			} `json:"messages"`
			Tools any `json:"tools"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if len(body.Messages) > 0 {
			if sys, _ := body.Messages[0].Content.(string); strings.Contains(sys, "summarise") && body.Tools == nil {
				w.Header().Set("Content-Type", "text/event-stream")
				io.WriteString(w, sseBody("the summary", 500, 60))
				return
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, sseBody("working", 9000, 10))
	}))
	defer srv.Close()

	// Tool exchanges only — the single-prompt agentic run, where every
	// exchange is an assistant call and its result. The naive cut at
	// len-tail lands on a tool message.
	msgs := []harness.ChatMessage{{Role: "system", Content: "sys"}}
	for i := 0; i < 10; i++ {
		id := "c" + itoa(i)
		msgs = append(msgs,
			harness.ChatMessage{Role: "assistant", ToolCalls: []harness.ToolCall{{ID: id}}},
			harness.ChatMessage{Role: "tool", Content: "out", ToolCallID: id},
		)
	}
	out, _, _, ok := autoCompact(t.Context(), LoopConfig{BaseURL: srv.URL, Model: "m", ContextWindow: 10000}, msgs, 9000)
	if !ok {
		t.Fatal("a tool-only transcript must still compact — the single-prompt run is the case that needs it")
	}
	kept := 0
	for i, m := range out {
		if m.Role != "tool" {
			continue
		}
		kept++
		matched := false
		for _, prev := range out[:i] {
			for _, tc := range prev.ToolCalls {
				if tc.ID == m.ToolCallID {
					matched = true
				}
			}
		}
		if !matched {
			t.Fatalf("a tool result (call %q) lost the assistant message that made it", m.ToolCallID)
		}
	}
	// And the other direction: every call still in the transcript has its
	// results after it — no assistant-with-calls dangling in the summary.
	for i, m := range out {
		if len(m.ToolCalls) == 0 {
			continue
		}
		for _, tc := range m.ToolCalls {
			found := false
			for _, next := range out[i+1:] {
				if next.Role == "tool" && next.ToolCallID == tc.ID {
					found = true
				}
			}
			if !found {
				t.Fatalf("an assistant call (%q) lost its tool result", tc.ID)
			}
		}
	}
	if kept == 0 {
		t.Fatal("no tool results survived — the whole task was summarised away")
	}
}

// TestWatermarkFiresOnTheFirstRound pins the turn-start case: a
// transcript that already sits over the window (bytes-estimated, no
// usage yet) compacts on its first round instead of failing it.
func TestWatermarkFiresOnTheFirstRound(t *testing.T) {
	var notes atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Role    string `json:"role"`
				Content any    `json:"content"`
			} `json:"messages"`
			Tools any `json:"tools"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if len(body.Messages) > 0 {
			if sys, _ := body.Messages[0].Content.(string); strings.Contains(sys, "summarise") && body.Tools == nil {
				w.Header().Set("Content-Type", "text/event-stream")
				io.WriteString(w, sseBody("the earlier work, summarised", 500, 60))
				return
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, sseBody("done", 0, 0))
	}))
	defer srv.Close()

	// ~40KB of transcript against a 10000-token window: the estimate
	// alone (~10k tokens) is over the four-fifths line. Enough messages
	// that a summary has a middle worth writing.
	messages := []harness.ChatMessage{{Role: "user", Content: strings.Repeat("pad ", 500)}}
	for i := 0; i < 10; i++ {
		messages = append(messages,
			harness.ChatMessage{Role: "assistant", Content: strings.Repeat("reply ", 334)},
			harness.ChatMessage{Role: "user", Content: strings.Repeat("pad ", 500)},
		)
	}
	res, err := Run(t.Context(), LoopConfig{
		BaseURL: srv.URL, Model: "m", ContextWindow: 10000, MaxTurns: 3,
		OnEvent: func(e harness.Event) {
			if e.Kind == harness.EventNote && strings.Contains(e.Text, "compacted") {
				notes.Add(1)
			}
		},
	}, messages)
	if err != nil {
		t.Fatal(err)
	}
	if notes.Load() == 0 {
		t.Fatal("an over-window first round never compacted")
	}
	compacted := false
	for _, m := range res {
		if s, _ := m.Content.(string); strings.Contains(s, CompactedPrefix) {
			compacted = true
		}
	}
	if !compacted {
		t.Fatal("the run finished without the summary marker")
	}
}
