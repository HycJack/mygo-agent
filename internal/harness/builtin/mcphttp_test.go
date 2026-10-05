package builtin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"mygo-agent/internal/harness"
)

// The HTTP MCP twin had no test at all, which left two fixes resting on
// inspection alone: the ActionMCP declaration (without it the policy
// denies EVERY tool an HTTP server offers, which reads as a policy that
// forbids MCP rather than as a missing field) and the bounded handshake.

// mcpHTTPServer answers the initialize handshake and a tools/list with the
// given tool names, so the adapter can be driven end to end without a real
// MCP server.
func mcpHTTPServer(t *testing.T, names ...string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var req struct {
			ID     int             `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		body, _ := readAll(r)
		_ = json.Unmarshal(body, &req)
		reply := func(result any) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": req.ID, "result": result,
			})
		}
		switch req.Method {
		case "initialize":
			reply(map[string]any{
				"protocolVersion": "2024-11-05",
				"serverInfo":      map[string]any{"name": "test", "version": "1"},
				"capabilities":    map[string]any{"tools": map[string]any{}},
			})
		case "tools/list":
			var tools []map[string]any
			for _, n := range names {
				tools = append(tools, map[string]any{
					"name":        n,
					"description": "a tool",
					"inputSchema": map[string]any{"type": "object"},
				})
			}
			reply(map[string]any{"tools": tools})
		default:
			reply(map[string]any{})
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func readAll(r *http.Request) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}
	defer r.Body.Close()
	var buf []byte
	tmp := make([]byte, 4096)
	for {
		n, err := r.Body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			return buf, nil
		}
	}
}

// TestHTTPMCPToolsAreUsable is the regression this file exists for: a
// streamable-HTTP server whose tools declared no action had every one of
// them denied by the policy, in every mode, with no error anywhere.
func TestHTTPMCPToolsAreUsable(t *testing.T) {
	srv := mcpHTTPServer(t, "create_issue", "list_repos")
	h := &httpMCP{name: "github", url: srv.URL, client: srv.Client()}

	ctx := context.Background()
	tools, err := h.listTools(ctx)
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("got %d tools, want 2", len(tools))
	}
	agent := harness.Policy{Mode: harness.ModeAgent}
	readOnly := harness.Policy{Mode: harness.ModeReadOnly}
	for _, tool := range tools {
		if len(tool.Actions) == 0 {
			t.Fatalf("HTTP MCP tool %q declares no action, so the gate denies it everywhere", tool.Name)
		}
		if !slices.Contains(tool.Actions, ActionMCP) {
			t.Fatalf("HTTP MCP tool %q declares %v, want mcp.call", tool.Name, tool.Actions)
		}
		if got := agent.Resolve(tool.Name, tool.Actions); got != harness.PermAsk {
			t.Fatalf("agent-mode policy for %q = %q, want ask", tool.Name, got)
		}
		if got := readOnly.Resolve(tool.Name, tool.Actions); got != harness.PermDeny {
			t.Fatalf("read-only policy for %q = %q, want deny", tool.Name, got)
		}
	}
}

// TestHTTPMCPHandshakeIsBounded proves an unresponsive endpoint cannot
// hang the turn: the handshake runs under its own deadline.
func TestHTTPMCPHandshakeIsBounded(t *testing.T) {
	// The handshake budget is 20s, and proving the bound means waiting it
	// out. Skip in -short so a local loop is not held up; CI runs the full
	// suite, so the guard is still there.
	if testing.Short() {
		t.Skip("waits out the 20s handshake budget")
	}
	// A server that accepts the connection and never answers. The handler
	// blocks on a channel the cleanup closes: httptest.Server.Close waits
	// for outstanding handlers, so a handler blocked forever would hang the
	// test binary rather than fail it.
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	t.Cleanup(func() {
		close(release)
		srv.Close()
	})

	// Drive the real entry point, which is where the deadline lives. The
	// handshake budget is 20s, so allow generous headroom and fail loudly
	// rather than waiting forever.
	done := make(chan error, 1)
	go func() {
		_, err := startHTTPMCP(context.Background(), "hung", srv.URL)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("an unresponsive endpoint reported a successful handshake")
		}
	case <-time.After(45 * time.Second):
		t.Fatal("the HTTP handshake is still blocked after 45s; it is not bounded")
	}
}

// TestMCPToolNamesAreUniquePerServer proves two servers offering the same
// long tool name do not collapse into one, which used to route the call
// to whichever server was registered first.
func TestMCPToolNamesAreUniquePerServer(t *testing.T) {
	long := ""
	for len(long) < 70 {
		long += "name"
	}
	a := mcpToolName("one", "tool"+long)
	b := mcpToolName("two", "tool"+long)
	if a == b {
		t.Fatalf("two servers produced the same tool name %q", a)
	}
	if len(a) > 64 || len(b) > 64 {
		t.Fatalf("a truncated name exceeded the 64-char budget: %d / %d", len(a), len(b))
	}
	// A name that already fits is used verbatim, so an existing selector
	// rule such as "mcp_github_*: allow" keeps matching.
	if got := mcpToolName("github", "create_issue"); got != "mcp_github_create_issue" {
		t.Fatalf("a short name was rewritten to %q; existing permission rules would stop matching", got)
	}
}
