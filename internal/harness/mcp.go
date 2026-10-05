package harness

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

// MCPServer is one Model Context Protocol server in the configuration:
// a command the app spawns and speaks JSON-RPC to over stdio.
type MCPServer struct {
	Name    string   `json:"name"`
	Command string   `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`
	Env     []string `json:"env,omitempty"`
	URL     string   `json:"url,omitempty"`
}

// mcpClient is a stdio MCP client for one server: initialize,
// tools/list and tools/call, with JSON-RPC ids matched to a pending map.
type mcpClient struct {
	name string
	cmd  *exec.Cmd
	in   io.WriteCloser
	sc   *bufio.Scanner

	mu      sync.Mutex
	pending map[int]chan json.RawMessage
	nextID  int
	err     error
}

var mcpNameRe = regexp.MustCompile(`[^A-Za-z0-9_-]`)

// mcpToolName is the tool name the model sees: providers allow at most
// 64 characters of [A-Za-z0-9_-], as pi's client does.
func mcpToolName(server, tool string) string {
	n := "mcp_" + server + "_" + tool
	n = mcpNameRe.ReplaceAllString(n, "_")
	if len(n) > 64 {
		n = n[:64]
	}
	return n
}

func newMCPClient(ctx context.Context, s MCPServer) (*mcpClient, error) {
	cmd := exec.CommandContext(ctx, s.Command, s.Args...)
	cmd.Env = append(cmdEnv(), s.Env...)
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = nil // the server's logs are not for the model
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting %s: %w", s.Command, err)
	}
	c := &mcpClient{
		name:    s.Name,
		cmd:     cmd,
		in:      in,
		sc:      bufio.NewScanner(out),
		pending: map[int]chan json.RawMessage{},
	}
	c.sc.Buffer(make([]byte, 64*1024), 4<<20)
	go c.readLoop()
	if err := c.init(ctx); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

// call sends one JSON-RPC request and waits for its response.
func (c *mcpClient) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return nil, c.err
	}
	c.nextID++
	id := c.nextID
	ch := make(chan json.RawMessage, 1)
	c.pending[id] = ch
	req := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		req["params"] = params
	}
	payload, err := json.Marshal(req)
	if err != nil {
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, err
	}
	_, werr := c.in.Write(append(payload, '\n'))
	c.mu.Unlock()
	if werr != nil {
		return nil, werr
	}
	select {
	case res := <-ch:
		return res, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// readLoop dispatches responses to their pending calls.
func (c *mcpClient) readLoop() {
	for c.sc.Scan() {
		line := c.sc.Bytes()
		var msg struct {
			ID     *int            `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(line, &msg) != nil || msg.ID == nil {
			continue
		}
		c.mu.Lock()
		ch := c.pending[*msg.ID]
		delete(c.pending, *msg.ID)
		if msg.Error != nil {
			c.err = fmt.Errorf("mcp: %s", msg.Error.Message)
		}
		c.mu.Unlock()
		if ch != nil {
			if msg.Error != nil {
				ch <- json.RawMessage(`{"error":` + strconvQuote(msg.Error.Message) + `}`)
			} else {
				ch <- msg.Result
			}
			close(ch)
		}
	}
	c.mu.Lock()
	c.err = fmt.Errorf("mcp server %q exited", c.name)
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
	c.mu.Unlock()
}

// init performs the MCP handshake.
func (c *mcpClient) init(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	res, err := c.call(ctx, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "codex-go", "version": "0.1.0"},
	})
	if err != nil {
		return err
	}
	if errCheck(res) != "" {
		return fmt.Errorf("initialize: %s", errCheck(res))
	}
	c.notify("notifications/initialized")
	return nil
}

func (c *mcpClient) notify(method string) {
	payload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method})
	c.mu.Lock()
	c.in.Write(append(payload, '\n'))
	c.mu.Unlock()
}

// listTools returns the server's tools as agent tools.
func (c *mcpClient) listTools(ctx context.Context) ([]Tool, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	res, err := c.call(ctx, "tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	if msg := errCheck(res); msg != "" {
		return nil, fmt.Errorf("tools/list: %s", msg)
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
			Name:        mcpToolName(c.name, tool.Name),
			Description: "MCP " + c.name + ": " + tool.Description,
			Actions:     []Action{ActionMCP},
			Parameters:  schema,
			Execute: func(ctx context.Context, args string) (string, error) {
				return c.callTool(ctx, tool.Name, args)
			},
		})
	}
	return tools, nil
}

// callTool invokes one tool and returns its text content.
func (c *mcpClient) callTool(ctx context.Context, name, args string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	params := map[string]any{"name": name}
	if args != "" {
		var parsed map[string]any
		if json.Unmarshal([]byte(args), &parsed) == nil && parsed != nil {
			params["arguments"] = parsed
		}
	}
	res, err := c.call(ctx, "tools/call", params)
	if err != nil {
		return "", err
	}
	if msg := errCheck(res); msg != "" {
		return "", fmt.Errorf("%s", msg)
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

// Close shuts the server process down.
func (c *mcpClient) Close() {
	c.in.Close()
	if c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
}

// errCheck returns the error message inside a JSON-RPC result, if the
// server reported the failure in-band.
func errCheck(res json.RawMessage) string {
	var e struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(res, &e) == nil && e.Error != nil {
		return e.Error.Message
	}
	return ""
}

func strconvQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// mcpTransport is one connected server: stdio or streamable HTTP.
type mcpTransport interface {
	listTools(ctx context.Context) ([]Tool, error)
	callTool(ctx context.Context, name, args string) (string, error)
	Close()
}

// ServerClient is a connected MCP server, safe to use from the app.
type ServerClient struct {
	t mcpTransport
}

// StartServer connects to an MCP server and performs the handshake:
// streamable HTTP when the entry has a URL, otherwise the command as a
// stdio transport.
func StartServer(ctx context.Context, s MCPServer) (*ServerClient, error) {
	t, err := startTransport(ctx, s)
	if err != nil {
		return nil, err
	}
	return &ServerClient{t: t}, nil
}

// startTransport resolves the transport for a server entry.
func startTransport(ctx context.Context, s MCPServer) (mcpTransport, error) {
	if s.URL != "" {
		return startHTTPMCP(ctx, s.URL)
	}
	return newMCPClient(ctx, s)
}

// ListTools returns the server's tools as agent tools.
func (sc *ServerClient) ListTools(ctx context.Context) ([]Tool, error) { return sc.t.listTools(ctx) }

// CallTool invokes one tool on the server.
func (sc *ServerClient) CallTool(ctx context.Context, name, args string) (string, error) {
	return sc.t.callTool(ctx, name, args)
}

// ServerName is the configured name of the server.
func (sc *ServerClient) ServerName() string { return "" }

// Close shuts the transport down.
func (sc *ServerClient) Close() { sc.t.Close() }
