package builtin

import (
	"mygo-agent/internal/harness/cli"

	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// mcpClient is a stdio MCP client for one server: initialize,
// tools/list and tools/call, with JSON-RPC ids matched to a pending map.
type mcpClient struct {
	name   string
	cmd    *exec.Cmd
	in     io.WriteCloser
	sc     *bufio.Scanner
	stderr *tailBuffer

	// mu guards the request table and the transport-death flag. wmu is
	// separate on purpose: a blocked write must not stop the read loop
	// from delivering a response, or the two pipes deadlock each other.
	mu      sync.Mutex
	wmu     sync.Mutex
	pending map[int]chan json.RawMessage
	nextID  int
	err     error
}

// write sends one framed message. The write mutex is held across the
// write itself so two callers cannot interleave halves of a line, and
// nothing else is: the read loop never takes it.
func (c *mcpClient) write(payload []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_, err := c.in.Write(append(payload, '\n'))
	return err
}

var mcpNameRe = regexp.MustCompile(`[^A-Za-z0-9_-]`)

// mcpToolName is the tool name the model sees: providers allow at most
// 64 characters of [A-Za-z0-9_-], as pi's client does. A name that fits
// is used verbatim, so a selector rule keeps matching; a truncated one
// carries a hash of the full server+tool identity, because two long
// pairs can share a prefix and the gate routes by first name match —
// the wrong server would answer.
func mcpToolName(server, tool string) string {
	n := mcpNameRe.ReplaceAllString("mcp_"+server+"_"+tool, "_")
	if len(n) <= 64 {
		return n
	}
	h := fnv.New64a()
	_, _ = io.WriteString(h, n) // a hash write never fails
	sum := strconv.FormatUint(h.Sum64(), 36)
	return n[:64-len(sum)-1] + "_" + sum
}

func newMCPClient(ctx context.Context, s MCPServer) (*mcpClient, error) {
	cmd := exec.CommandContext(ctx, s.Command, s.Args...)
	cmd.Env = append(cmdEnv(), s.Env...)
	// The server leads its own process group, so a cancelled turn takes
	// its children with it instead of leaving them behind.
	procGroupAttr(cmd)
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	// A server that dies usually says why on stderr; the tail of it is
	// what makes a transport death readable, so it is kept, not dropped.
	stderr := &tailBuffer{max: 8 << 10}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting %s: %w", s.Command, err)
	}
	c := &mcpClient{
		name:    s.Name,
		cmd:     cmd,
		in:      in,
		sc:      bufio.NewScanner(out),
		stderr:  stderr,
		pending: map[int]chan json.RawMessage{},
	}
	c.sc.Buffer(make([]byte, 64*1024), 4<<20)
	// Reap the child. Nothing else waits on it, so without this every MCP
	// server the turn starts is left as a <defunct> process for as long as
	// the app runs.
	go func() { _ = cmd.Wait() }()
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
	c.mu.Unlock()
	if werr := c.write(payload); werr != nil {
		return nil, werr
	}
	select {
	case res := <-ch:
		return res, nil
	case <-ctx.Done():
		// Drop the pending entry: the reply, if it ever arrives, has
		// nowhere to go, and leaving the channel here grows the map by
		// one per timeout for the life of the client.
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
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
		c.mu.Unlock()
		if ch != nil {
			if msg.Error != nil {
				ch <- errPayload(msg.Error.Message)
			} else {
				ch <- msg.Result
			}
			close(ch)
		}
	}
	// The server is gone: every waiter is failed here rather than closed
	// empty, which a caller would read as an empty success.
	c.mu.Lock()
	c.err = c.deadErr("exited")
	for id, ch := range c.pending {
		ch <- errPayload(c.err.Error())
		close(ch)
		delete(c.pending, id)
	}
	c.mu.Unlock()
}

// errPayload is the in-band failure shape. A server's own error and a
// dead transport are reported the same way, so a caller only has to
// check one shape — and errCheck is what reads it.
func errPayload(msg string) json.RawMessage {
	return json.RawMessage(`{"error":{"message":` + strconvQuote(msg) + `}}`)
}

// deadErr describes a dead transport, with the server's own last words
// when it left any.
func (c *mcpClient) deadErr(what string) error {
	if tail := strings.TrimSpace(c.stderr.String()); tail != "" {
		return fmt.Errorf("mcp server %q %s: %s", c.name, what, cli.Trunc(tail, 200))
	}
	return fmt.Errorf("mcp server %q %s", c.name, what)
}

// tailBuffer keeps the last max bytes written to it, dropping older
// ones: a server's diagnostics are worth a tail, not a transcript.
type tailBuffer struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.max {
		t.buf = t.buf[len(t.buf)-t.max:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
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
	// An empty payload means the transport died before answering; a
	// successful handshake must carry a result.
	if len(res) == 0 {
		return c.deadErr("exited during initialize")
	}
	if errCheck(res) != "" {
		return fmt.Errorf("initialize: %s", errCheck(res))
	}
	c.notify("notifications/initialized")
	return nil
}

func (c *mcpClient) notify(method string) {
	payload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method})
	_ = c.write(payload)
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
	if len(res) == 0 {
		return "", c.deadErr("exited during a tool call")
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
		return cli.TrimOutput(string(res), maxToolResultBytes), nil
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
	// A tool the app does not own has no trim of its own, and its
	// result rides straight into the context: the cap the local tools
	// apply at their own exits is applied here, at the one place every
	// MCP result passes through.
	return cli.TrimOutput(s, maxToolResultBytes), nil
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
	name string
	t    mcpTransport
}

// StartServer connects to an MCP server and performs the handshake:
// streamable HTTP when the entry has a URL, otherwise the command as a
// stdio transport.
func StartServer(ctx context.Context, s MCPServer) (*ServerClient, error) {
	t, err := startTransport(ctx, s)
	if err != nil {
		return nil, err
	}
	return &ServerClient{name: s.Name, t: t}, nil
}

// startTransport resolves the transport for a server entry.
func startTransport(ctx context.Context, s MCPServer) (mcpTransport, error) {
	if s.URL != "" {
		return startHTTPMCP(ctx, s.Name, s.URL)
	}
	return newMCPClient(ctx, s)
}

// ListTools returns the server's tools as agent tools.
func (sc *ServerClient) ListTools(ctx context.Context) ([]Tool, error) { return sc.t.listTools(ctx) }

// CallTool invokes one tool on the server.
func (sc *ServerClient) CallTool(ctx context.Context, name, args string) (string, error) {
	return sc.t.callTool(ctx, name, args)
}

// ServerName is the configured name of the server, the prefix of every
// tool it exposes (mcp_github_*) and what a selector rule matches on.
func (sc *ServerClient) ServerName() string { return sc.name }

// Close shuts the transport down.
func (sc *ServerClient) Close() { sc.t.Close() }
