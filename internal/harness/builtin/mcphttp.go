package builtin

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// httpMCP is a Streamable HTTP MCP transport: JSON-RPC over HTTP POST,
// with an optional Mcp-Session-Id from initialize. Responses may be a
// single JSON document or an SSE stream; both are accepted.
type httpMCP struct {
	url     string
	client  *http.Client
	session string
	mu      sync.Mutex
	nextID  int
}

// startHTTPMCP opens a transport and performs the initialize handshake.
func startHTTPMCP(ctx context.Context, url string) (*httpMCP, error) {
	h := &httpMCP{url: url, client: &http.Client{}}
	if _, err := h.rpc(ctx, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "mygo-agent", "version": "0.1.0"},
	}); err != nil {
		return nil, err
	}
	h.notify("notifications/initialized")
	return h, nil
}

// rpc posts one JSON-RPC request and returns the result payload.
func (h *httpMCP) rpc(ctx context.Context, method string, params any) (json.RawMessage, error) {
	h.mu.Lock()
	id := h.nextID + 1
	h.nextID = id
	h.mu.Unlock()

	body := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		body["params"] = params
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.url, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	h.mu.Lock()
	if h.session != "" {
		req.Header.Set("Mcp-Session-Id", h.session)
	}
	h.mu.Unlock()

	resp, err := h.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		h.mu.Lock()
		h.session = sid
		h.mu.Unlock()
	}
	if resp.StatusCode == http.StatusAccepted {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		return nil, fmt.Errorf("mcp http: %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}

	ct := resp.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "text/event-stream") {
		return h.scanSSE(resp.Body, id)
	}
	var envelope struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return nil, err
	}
	if envelope.Error != nil {
		return nil, fmt.Errorf("mcp: %s", envelope.Error.Message)
	}
	return envelope.Result, nil
}

// scanSSE reads an SSE stream until the response with the given id
// arrives, returning its result payload.
func (h *httpMCP) scanSSE(body io.Reader, id int) (json.RawMessage, error) {
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		var envelope struct {
			ID     *int            `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal([]byte(data), &envelope) != nil {
			continue
		}
		if envelope.ID != nil && *envelope.ID == id {
			if envelope.Error != nil {
				return nil, fmt.Errorf("mcp: %s", envelope.Error.Message)
			}
			return envelope.Result, nil
		}
	}
	return nil, fmt.Errorf("mcp: stream ended without a response")
}

// notify posts a notification (no id, no response expected).
func (h *httpMCP) notify(method string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	payload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.url, bytes.NewReader(payload))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	h.mu.Lock()
	if h.session != "" {
		req.Header.Set("Mcp-Session-Id", h.session)
	}
	h.mu.Unlock()
	if resp, err := h.client.Do(req); err == nil {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
	}
}

// listTools returns the server's tools as agent tools.
func (h *httpMCP) listTools(ctx context.Context) ([]Tool, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	res, err := h.rpc(ctx, "tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Tools []struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			InputSchema json.RawMessage `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(res, &parsed); err != nil {
		return nil, err
	}
	tools := make([]Tool, 0, len(parsed.Tools))
	for _, tl := range parsed.Tools {
		tool := tl
		schema := tool.InputSchema
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object"}`)
		}
		tools = append(tools, Tool{
			Name:        mcpToolName(h.url, tool.Name),
			Description: "MCP: " + tool.Description,
			Parameters:  schema,
			Execute: func(ctx context.Context, args string) (string, error) {
				return h.callTool(ctx, tool.Name, args)
			},
		})
	}
	return tools, nil
}

// callTool invokes one tool and returns its text content.
func (h *httpMCP) callTool(ctx context.Context, name, args string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	params := map[string]any{"name": name}
	if args != "" {
		var parsed map[string]any
		if json.Unmarshal([]byte(args), &parsed) == nil && parsed != nil {
			params["arguments"] = parsed
		}
	}
	res, err := h.rpc(ctx, "tools/call", params)
	if err != nil {
		return "", err
	}
	var out struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(res, &out); err != nil {
		return string(res), nil
	}
	var b strings.Builder
	for _, blk := range out.Content {
		if blk.Type == "text" {
			b.WriteString(blk.Text)
			b.WriteString("\n")
		}
	}
	s := strings.TrimRight(b.String(), "\n")
	if s == "" {
		s = "(empty result)"
	}
	return s, nil
}

// Close releases the transport; HTTP has no process to stop.
func (h *httpMCP) Close() {}
