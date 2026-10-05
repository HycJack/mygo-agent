package agent

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResponsesInputConversion(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "system", Content: "be brief"},
		{Role: "user", Content: "run ls"},
		{Role: "assistant", Content: "", ToolCalls: []ToolCall{{
			ID: "call_1", Type: "function",
			Function: struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			}{Name: "bash", Arguments: `{"command":"ls"}`},
		}}},
		{Role: "tool", Content: "file.txt", ToolCallID: "call_1"},
		{Role: "assistant", Content: "here you go"},
	}
	instructions, input := responsesInput(msgs)
	if instructions != "be brief" {
		t.Fatalf("instructions %q", instructions)
	}
	types := make([]string, 0, len(input))
	for _, item := range input {
		b, _ := json.Marshal(item)
		var m map[string]any
		json.Unmarshal(b, &m)
		if tpe, ok := m["type"].(string); ok {
			types = append(types, tpe)
			continue
		}
		if role, ok := m["role"].(string); ok {
			types = append(types, "role:"+role)
		}
	}
	want := []string{"role:user", "function_call", "function_call_output", "role:assistant"}
	if strings.Join(types, ",") != strings.Join(want, ",") {
		t.Fatalf("input items %v, want %v", types, want)
	}
	// The function call item carries the arguments through.
	b, _ := json.Marshal(input[1])
	if !strings.Contains(string(b), `"call_id":"call_1"`) || !strings.Contains(string(b), "bash") {
		t.Fatalf("function_call item: %s", b)
	}
}

func TestResponsesBody(t *testing.T) {
	cfg := StreamConfig{
		Model: "gpt-5.2-codex", Wire: WireResponses, ReasoningEffort: "high",
		Messages: []ChatMessage{{Role: "system", Content: "sys"}},
		Tools:    []Tool{{Name: "bash", Description: "d", Parameters: json.RawMessage(`{"type":"object"}`)}},
	}
	body := responsesBody(cfg)
	if body["store"] != false || body["stream"] != true {
		t.Fatalf("store/stream: %v", body)
	}
	if body["instructions"] != "sys" {
		t.Fatalf("instructions: %v", body["instructions"])
	}
	reasoning, ok := body["reasoning"].(map[string]any)
	if !ok || reasoning["effort"] != "high" {
		t.Fatalf("reasoning: %v", body["reasoning"])
	}
	tools := body["tools"].([]map[string]any)
	if tools[0]["type"] != "function" || tools[0]["name"] != "bash" {
		t.Fatalf("tools: %v", tools)
	}
}

func TestStreamResponsesEvents(t *testing.T) {
	sse := "event: response.output_text.delta\n" +
		`data: {"type":"response.output_text.delta","delta":"working "}` + "\n\n" +
		"event: response.output_item.added\n" +
		`data: {"type":"response.output_item.added","item":{"type":"function_call","id":"fc_1","call_id":"call_9","name":"bash","arguments":""}}` + "\n\n" +
		"event: response.function_call_arguments.delta\n" +
		`data: {"type":"response.function_call_arguments.delta","item_id":"fc_1","delta":"{\"comm"}` + "\n\n" +
		"event: response.function_call_arguments.delta\n" +
		`data: {"type":"response.function_call_arguments.delta","item_id":"fc_1","delta":"and\":\"ls\"}"}` + "\n\n" +
		"event: response.output_item.done\n" +
		`data: {"type":"response.output_item.done","item":{"type":"function_call","call_id":"call_9","name":"bash","arguments":"{\"command\":\"ls\"}"}}` + "\n\n" +
		"event: response.completed\n" +
		`data: {"type":"response.completed","response":{"output":[]}}` + "\n\n"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Errorf("path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, sse)
	}))
	defer srv.Close()

	var text strings.Builder
	res, err := streamChat(t.Context(), StreamConfig{
		BaseURL: srv.URL, Wire: WireResponses, Model: "gpt-5.2-codex",
		Messages: []ChatMessage{{Role: "system", Content: "sys"}, {Role: "user", Content: "hi"}},
	}, func(d string) { text.WriteString(d) })
	if err != nil {
		t.Fatal(err)
	}
	if text.String() != "working " {
		t.Fatalf("text %q", text.String())
	}
	if len(res.ToolCalls) != 1 {
		t.Fatalf("tool calls %+v", res.ToolCalls)
	}
	call := res.ToolCalls[0]
	if call.ID != "call_9" || call.Function.Name != "bash" {
		t.Fatalf("call %+v", call)
	}
	var args map[string]any
	if json.Unmarshal([]byte(call.Function.Arguments), &args) != nil || args["command"] != "ls" {
		t.Fatalf("arguments %q", call.Function.Arguments)
	}
}

func TestStreamResponsesFailed(t *testing.T) {
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "event: response.failed\n"+
			`data: {"type":"response.failed","response":{"error":{"message":"quota exhausted"}}}`+"\n\n")
	}))
	defer srv2.Close()
	_, err := streamChat(t.Context(), StreamConfig{
		BaseURL: srv2.URL, Wire: WireResponses, Model: "m",
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	}, func(string) {})
	if err == nil || !strings.Contains(err.Error(), "quota exhausted") {
		t.Fatalf("error %v", err)
	}
}
