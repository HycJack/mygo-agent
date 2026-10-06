// Package agent implements the app's built-in agent: an OpenAI-
// compatible streaming client, the tool-calling loop, the built-in
// tools, skill discovery (Agent Skills spec) and a stdio MCP client.
// The design follows pi and pi-ai-go.
package builtin

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"
)

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

// Tool is a tool the agent may call. Actions is what the tool does, from
// the closed catalog in permissions.go; the permission gate decides by
// action, not by name.
type Tool struct {
	Name        string
	Description string
	Parameters  json.RawMessage // JSON schema, object type
	Actions     []Action
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

	// ContextWindow is the provider's context window in tokens, as
	// declared in the provider's config. Zero sends nothing; a declared
	// window opts the chat wire into stream_options.include_usage, so
	// the final chunk carries the prompt token count the watermark
	// trigger reads. The responses wire reports usage regardless.
	ContextWindow int
}

// assistantResult is what one streaming call produced.
type assistantResult struct {
	Content   string
	ToolCalls []ToolCall
	Finish    string
	Err       string
	// Prompt/CompletionTokens are what the wire reported, zero when it
	// reported nothing: the watermark trigger reads the prompt side.
	PromptTokens     int
	CompletionTokens int
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
	if cfg.ContextWindow > 0 {
		// Opted in by a declared window: the last chunk then carries
		// the usage the watermark reads. Servers that reject the field
		// are rare, and the window that armed it is also the off
		// switch.
		body["stream_options"] = map[string]any{"include_usage": true}
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
	client := &http.Client{Timeout: llmRequestTimeout}
	resp, err := doWithRetry(ctx, client, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		if cfg.APIKey != "" {
			req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
		}
		return req, nil
	})
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
			Usage *struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
			} `json:"usage"`
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
		if chunk.Usage != nil {
			res.PromptTokens = chunk.Usage.PromptTokens
			res.CompletionTokens = chunk.Usage.CompletionTokens
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
				// Assign, never append: a gateway that repeats the name on
				// every delta used to produce "get_weatherget_weather".
				acc.name = tc.Function.Name
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
	// The accumulated tool calls come out in index order whatever the
	// numbering the gateway used: a 1-based or gapped index is a shape
	// to accommodate, not a reason to drop every call after the first
	// hole.
	idxs := make([]int, 0, len(byIndex))
	for i := range byIndex {
		idxs = append(idxs, i)
	}
	slices.Sort(idxs)
	for _, idx := range idxs {
		acc := byIndex[idx]
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

// llmRequestTimeout bounds one provider exchange. The turn context is
// much wider than a single response, so without it a stalled provider
// would hold the turn open indefinitely.
const llmRequestTimeout = 5 * time.Minute

// llmAttempts is the total number of tries for a request: one plus a
// small bounded retry of the failures that are actually transient.
const llmAttempts = 3

// doWithRetry sends the request built by newReq and returns the first
// response the caller has to look at. Only a transport failure and a
// 429/5xx are repeated — a 4xx is the request's own fault — and the
// retry wraps the send alone: once a body is being streamed, nothing is
// ever replayed.
func doWithRetry(ctx context.Context, client *http.Client, newReq func() (*http.Request, error)) (*http.Response, error) {
	backoff := 200 * time.Millisecond
	var lastErr error
	for attempt := range llmAttempts {
		req, err := newReq()
		if err != nil {
			return nil, err
		}
		resp, err := client.Do(req)
		switch {
		case err != nil:
			lastErr = err
		case !retryableStatus(resp.StatusCode):
			return resp, nil
		default:
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
			resp.Body.Close()
			lastErr = fmt.Errorf("provider returned %s: %s", resp.Status, strings.TrimSpace(string(b)))
		}
		if attempt == llmAttempts-1 {
			break
		}
		select {
		case <-time.After(backoff):
			backoff *= 2
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return nil, lastErr
}

// retryableStatus is what a later attempt may still answer.
func retryableStatus(code int) bool {
	return code == http.StatusTooManyRequests || code >= 500
}
