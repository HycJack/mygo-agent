package builtin

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// TestStreamChatAcceptsOneBasedIndices: some gateways number tool calls
// from one, or skip an index. Stopping at the first hole dropped every
// call and the turn ended as a plain stop.
func TestStreamChatAcceptsOneBasedIndices(t *testing.T) {
	sse := "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":1,\"id\":\"c1\"," +
		"\"function\":{\"name\":\"list_files\",\"arguments\":\"{}\"}}]}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":2,\"id\":\"c2\"," +
		"\"function\":{\"name\":\"read_file\",\"arguments\":\"{}\"}}]}}]}\n\n" +
		"data: [DONE]\n\n"
	res := accumulateSSE(t, sse)
	if len(res.ToolCalls) != 2 {
		t.Fatalf("both tool calls must survive: %+v", res.ToolCalls)
	}
	if res.ToolCalls[0].Function.Name != "list_files" || res.ToolCalls[1].Function.Name != "read_file" {
		t.Fatalf("calls out of order: %+v", res.ToolCalls)
	}
	if res.ToolCalls[0].ID != "c1" || res.ToolCalls[1].ID != "c2" {
		t.Fatalf("ids mixed up: %+v", res.ToolCalls)
	}
}

// TestStreamChatRetriesTransientStatus: a 429 is the provider asking for
// a moment, not a reason to fail the turn.
func TestStreamChatRetriesTransientStatus(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			io.WriteString(w, "slow down")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"recovered\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer srv.Close()
	res, err := streamChat(t.Context(), StreamConfig{
		BaseURL: srv.URL, Model: "m",
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	}, func(string) {})
	if err != nil {
		t.Fatalf("a 429 must be retried: %v", err)
	}
	if res.Content != "recovered" || attempts.Load() != 2 {
		t.Fatalf("content %q after %d attempts", res.Content, attempts.Load())
	}
}

// TestStreamChatDoesNotRetryClientErrors: a 400 is the request's own
// fault, so repeating it only wastes the user's time.
func TestStreamChatDoesNotRetryClientErrors(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, "unknown model")
	}))
	defer srv.Close()
	_, err := streamChat(t.Context(), StreamConfig{
		BaseURL: srv.URL, Model: "nope",
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	}, func(string) {})
	if err == nil || !strings.Contains(err.Error(), "400") {
		t.Fatalf("a 400 must surface: %v", err)
	}
	if n := attempts.Load(); n != 1 {
		t.Fatalf("a 400 was sent %d times", n)
	}
}

// TestStreamChatRetryReplaysCleanDrop: a provider that drops the SSE
// stream before any delta was delivered is replayed whole — the "for
// one replay only" rule that mirrors doWithRetry's no-replay rule.
func TestStreamChatRetryReplaysCleanDrop(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if attempts.Add(1) == 1 {
			io.WriteString(w, ": keepalive\n\n")
			w.(http.Flusher).Flush()
			panic(http.ErrAbortHandler) // the wire dies before any delta
		}
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"recovered\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer srv.Close()
	var got strings.Builder
	res, err := streamChatRetry(t.Context(), StreamConfig{
		BaseURL: srv.URL, Model: "m",
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	}, func(delta string) { got.WriteString(delta) })
	if err != nil {
		t.Fatalf("a clean mid-stream drop must be replayed: %v", err)
	}
	if res.Content != "recovered" || got.String() != "recovered" || attempts.Load() != 2 {
		t.Fatalf("content %q streamed %q after %d attempts", res.Content, got.String(), attempts.Load())
	}
}

// TestStreamChatRetryNeverDuplicates: once a delta reached the
// transcript the turn is no longer replayable — a retry would
// duplicate the text, so the drop fails the turn as before.
func TestStreamChatRetryNeverDuplicates(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"par\"}}]}\n\n")
		w.(http.Flusher).Flush()
		panic(http.ErrAbortHandler)
	}))
	defer srv.Close()
	var got strings.Builder
	_, err := streamChatRetry(t.Context(), StreamConfig{
		BaseURL: srv.URL, Model: "m",
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	}, func(delta string) { got.WriteString(delta) })
	if err == nil {
		t.Fatal("a drop after emission must fail the turn")
	}
	if got.String() != "par" || attempts.Load() != 1 {
		t.Fatalf("streamed %q after %d attempts — the turn was replayed", got.String(), attempts.Load())
	}
}
