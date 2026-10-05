package harness

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// WireAPI names the two request shapes a provider may speak.
const (
	WireChat      = "chat"      // POST /chat/completions
	WireResponses = "responses" // POST /responses — what the codex models use
)

// streamResponses runs one streaming call against the OpenAI Responses
// API: instructions carry the system prompt, input carries the typed
// transcript, and function calls come back as output items.
func streamResponses(ctx context.Context, cfg StreamConfig, onText func(string)) (assistantResult, error) {
	body := responsesBody(cfg)
	payload, err := json.Marshal(body)
	if err != nil {
		return assistantResult{}, err
	}
	url := strings.TrimRight(cfg.BaseURL, "/") + "/responses"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(payload)))
	if err != nil {
		return assistantResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	}

	client := &http.Client{Timeout: 0}
	resp, err := client.Do(req)
	if err != nil {
		return assistantResult{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		return assistantResult{}, fmt.Errorf("provider returned %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}

	var res assistantResult
	type acc struct {
		callID string
		name   string
		args   strings.Builder
		done   ToolCall
	}
	order := []string{}
	byItem := map[string]*acc{}
	doneByCall := map[string]ToolCall{}
	msgDone := strings.Builder{}

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" {
			continue
		}
		var ev struct {
			Type string `json:"type"`
			// output_text.delta
			Delta string `json:"delta"`
			// output_item.added / output_item.done
			Item *struct {
				Type      string `json:"type"`
				ID        string `json:"id"`
				CallID    string `json:"call_id"`
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
				Content   []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"item"`
			Response *struct {
				Error *struct {
					Message string `json:"message"`
				} `json:"error"`
			} `json:"response"`
			Message string `json:"message"`
		}
		if json.Unmarshal([]byte(data), &ev) != nil {
			continue
		}
		switch ev.Type {
		case "response.output_text.delta":
			res.Content += ev.Delta
			onText(ev.Delta)
		case "response.output_item.added":
			if ev.Item != nil && ev.Item.Type == "function_call" {
				id := ev.Item.ID
				byItem[id] = &acc{callID: ev.Item.CallID, name: ev.Item.Name}
				order = append(order, id)
			}
		case "response.function_call_arguments.delta":
			// The item_id of the streaming call is in item_id; find the
			// accumulator whose name is still filling.
			if ev.Item != nil {
				if a := byItem[ev.Item.ID]; a != nil {
					a.args.WriteString(ev.Delta)
				}
			}
		case "response.output_item.done":
			if ev.Item == nil {
				continue
			}
			switch ev.Item.Type {
			case "function_call":
				call := ToolCall{ID: ev.Item.CallID, Type: "function"}
				call.Function.Name = ev.Item.Name
				call.Function.Arguments = ev.Item.Arguments
				doneByCall[call.ID] = call
				if a := byItem[ev.Item.ID]; a != nil {
					a.done = call
				}
			case "message":
				for _, c := range ev.Item.Content {
					if c.Type == "output_text" {
						msgDone.WriteString(c.Text)
					}
				}
			}
		case "response.completed":
			if ev.Response != nil && ev.Response.Error != nil {
				res.Err = ev.Response.Error.Message
			}
			// The full output is available here; the accumulated pieces
			// already mirror it.
		case "response.failed":
			if ev.Response != nil && ev.Response.Error != nil {
				res.Err = ev.Response.Error.Message
			} else {
				res.Err = "the response failed"
			}
		case "error":
			res.Err = ev.Message
		}
	}
	if err := sc.Err(); err != nil && !errors.Is(err, io.EOF) && ctx.Err() == nil {
		return res, fmt.Errorf("reading stream: %w", err)
	}
	if res.Err != "" {
		return res, errors.New(res.Err)
	}
	if res.Content == "" && msgDone.Len() > 0 {
		res.Content = msgDone.String()
	}
	for _, id := range order {
		a := byItem[id]
		if call, ok := doneByCall[a.callID]; ok {
			res.ToolCalls = append(res.ToolCalls, call)
			continue
		}
		res.ToolCalls = append(res.ToolCalls, ToolCall{
			ID:   a.callID,
			Type: "function",
			Function: struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			}{Name: a.name, Arguments: a.args.String()},
		})
	}
	if res.Finish == "" {
		if len(res.ToolCalls) > 0 {
			res.Finish = "tool_calls"
		} else {
			res.Finish = "stop"
		}
	}
	return res, nil
}

// responsesBody builds the request: instructions for the system
// prompt, typed input items for the transcript.
func responsesBody(cfg StreamConfig) map[string]any {
	instructions, input := responsesInput(cfg.Messages)
	body := map[string]any{
		"model":        cfg.Model,
		"instructions": instructions,
		"input":        input,
		"stream":       true,
		"store":        false,
	}
	if len(cfg.Tools) > 0 {
		tools := make([]map[string]any, len(cfg.Tools))
		for i, t := range cfg.Tools {
			tools[i] = map[string]any{
				"type":        "function",
				"name":        t.Name,
				"description": t.Description,
				"parameters":  t.Parameters,
			}
		}
		body["tools"] = tools
	}
	if cfg.ReasoningEffort != "" {
		body["reasoning"] = map[string]any{"effort": cfg.ReasoningEffort}
	}
	return body
}

// responsesInput converts the chat-shaped transcript into Responses
// input items: user text, assistant output text, function calls and
// their outputs.
func responsesInput(msgs []ChatMessage) (instructions string, input []map[string]any) {
	var sys strings.Builder
	for _, m := range msgs {
		text, _ := m.Content.(string)
		switch m.Role {
		case "system":
			if sys.Len() > 0 {
				sys.WriteString("\n\n")
			}
			sys.WriteString(text)
		case "user":
			input = append(input, map[string]any{
				"role":    "user",
				"content": []map[string]any{{"type": "input_text", "text": text}},
			})
		case "assistant":
			if text != "" {
				input = append(input, map[string]any{
					"role":    "assistant",
					"content": []map[string]any{{"type": "output_text", "text": text}},
				})
			}
			for _, tc := range m.ToolCalls {
				input = append(input, map[string]any{
					"type":      "function_call",
					"call_id":   tc.ID,
					"name":      tc.Function.Name,
					"arguments": tc.Function.Arguments,
				})
			}
		case "tool":
			input = append(input, map[string]any{
				"type":    "function_call_output",
				"call_id": m.ToolCallID,
				"output":  text,
			})
		}
	}
	return sys.String(), input
}
