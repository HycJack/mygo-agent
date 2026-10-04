// Package agent implements the app's built-in agent: an OpenAI-
// compatible streaming client, the tool-calling loop, the built-in
// tools, skill discovery (Agent Skills spec) and a stdio MCP client.
// The design follows pi and pi-ai-go.
package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ChatMessage is one message of the OpenAI chat transcript.
type ChatMessage struct {
	Role       string     `json:"role"`
	Content    any        `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

// ToolCall is one function the assistant asked for.
type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// chatTool is the tools entry of the request body.
type chatTool struct {
	Type     string   `json:"type"`
	Function chatFunc `json:"function"`
}

type chatFunc struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// Tool is a tool the agent may call.
type Tool struct {
	Name        string
	Description string
	Parameters  json.RawMessage // JSON schema, object type
	Execute     func(ctx context.Context, args string) (string, error)
}

// StreamConfig configures one streaming completion call. Wire selects
// the request shape: WireChat (default) or WireResponses.
type StreamConfig struct {
	BaseURL  string
	APIKey   string
	Model    string
	Wire     string
	Messages []ChatMessage
	Tools    []Tool

	ReasoningEffort string // responses API only: low / medium / high
}

// assistantResult is what one streaming call produced.
type assistantResult struct {
	Content   string
	ToolCalls []ToolCall
	Finish    string
	Err       string
}

// streamChat runs one streaming completion over the provider's wire
// API and feeds text deltas to onText. It returns the assembled
// assistant message.
func streamChat(ctx context.Context, cfg StreamConfig, onText func(delta string)) (assistantResult, error) {
	if cfg.Wire == WireResponses {
		return streamResponses(ctx, cfg, onText)
	}
	return streamChatCompletions(ctx, cfg, onText)
}

// streamChatCompletions calls POST {base}/chat/completions with
// stream:true and feeds deltas to onText. It returns the assembled
// assistant message.
func streamChatCompletions(ctx context.Context, cfg StreamConfig, onText func(delta string)) (assistantResult, error) {
	body := map[string]any{
		"model":    cfg.Model,
		"messages": cfg.Messages,
		"stream":   true,
	}
	if len(cfg.Tools) > 0 {
		tools := make([]chatTool, len(cfg.Tools))
		for i, t := range cfg.Tools {
			tools[i] = chatTool{Type: "function", Function: chatFunc{
				Name: t.Name, Description: t.Description, Parameters: t.Parameters,
			}}
		}
		body["tools"] = tools
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return assistantResult{}, err
	}
	url := strings.TrimRight(cfg.BaseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return assistantResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	}

	client := &http.Client{Timeout: 0} // the context bounds the call
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
	// Accumulate tool-call fragments by index.
	type toolAccum struct {
		id      string
		name    string
		args    strings.Builder
		argsObj []byte
	}
	byIndex := map[int]*toolAccum{}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			if data == "[DONE]" {
				break
			}
			continue
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content   string `json:"content"`
					ToolCalls []struct {
						Index    *int   `json:"index"`
						ID       string `json:"id"`
						Type     string `json:"type"`
						Function struct {
							Name string `json:"name"`
							// Arguments arrives as a string from most
							// providers, but some gateways send it as
							// an object; both are accepted.
							Arguments json.RawMessage `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal([]byte(data), &chunk) != nil {
			continue
		}
		if chunk.Error != nil {
			res.Err = chunk.Error.Message
			continue
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		choice := chunk.Choices[0]
		if d := choice.Delta.Content; d != "" {
			res.Content += d
			onText(d)
		}
		for _, tc := range choice.Delta.ToolCalls {
			idx := 0
			if tc.Index != nil {
				idx = *tc.Index
			}
			acc := byIndex[idx]
			if acc == nil {
				acc = &toolAccum{}
				byIndex[idx] = acc
			}
			if tc.ID != "" {
				acc.id = tc.ID
			}
			if tc.Function.Name != "" {
				acc.name += tc.Function.Name
			}
			if len(tc.Function.Arguments) > 0 {
				var s string
				if json.Unmarshal(tc.Function.Arguments, &s) == nil {
					acc.args.WriteString(s)
				} else {
					// An object: remember the latest one; it wins over
					// any string fragments seen before.
					acc.args.Reset()
					acc.argsObj = append([]byte(nil), tc.Function.Arguments...)
				}
			}
		}
		if choice.FinishReason != nil {
			res.Finish = *choice.FinishReason
		}
	}
	if err := sc.Err(); err != nil && !errors.Is(err, io.EOF) && ctx.Err() == nil {
		return res, fmt.Errorf("reading stream: %w", err)
	}
	if res.Err != "" {
		return res, errors.New(res.Err)
	}
	for idx := 0; len(byIndex) > 0; idx++ {
		acc, ok := byIndex[idx]
		if !ok {
			break
		}
		args := acc.args.String()
		if args == "" && len(acc.argsObj) > 0 {
			args = string(acc.argsObj)
		}
		res.ToolCalls = append(res.ToolCalls, ToolCall{
			ID:   acc.id,
			Type: "function",
			Function: struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			}{Name: acc.name, Arguments: args},
		})
	}
	return res, nil
}

// httpClientTimeout is the budget for one whole streaming call.
const httpClientTimeout = 10 * time.Minute

var _ = httpClientTimeout
