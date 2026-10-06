//go:build !windows

package builtin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeMCPServer writes a stdio MCP server for a test. The call script
// decides what a tools/call gets: die exits without answering, once
// fails the first call in band and answers the rest.
func fakeMCPServer(t *testing.T, script string) string {
	t.Helper()
	state := filepath.Join(t.TempDir(), "calls")
	body := `#!/bin/sh
state="` + state + `"
while IFS= read -r line; do
  resp=""
  id=$(printf '%s' "$line" | sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p')
  case "$line" in
    *'"initialize"'*)
      resp="{\"jsonrpc\":\"2.0\",\"id\":$id,\"result\":{\"protocolVersion\":\"2024-11-05\"}}" ;;
    *'"tools/list"'*)
      resp="{\"jsonrpc\":\"2.0\",\"id\":$id,\"result\":{\"tools\":[{\"name\":\"flaky\",\"inputSchema\":{\"type\":\"object\"}}]}}" ;;
    *'"tools/call"'*)
      n=0
      if [ -f "$state" ]; then n=$(cat "$state"); fi
      n=$((n + 1))
      printf '%s' "$n" > "$state"
      case "` + script + `" in
        die) exit 7 ;;
        huge)
          big=$(printf 'x%.0s' $(seq 1 60000))
          resp="{\"jsonrpc\":\"2.0\",\"id\":$id,\"result\":{\"content\":[{\"type\":\"text\",\"text\":\"$big\"}]}}" ;;
        once)
          if [ "$n" -eq 1 ]; then
            resp="{\"jsonrpc\":\"2.0\",\"id\":$id,\"error\":{\"code\":-32000,\"message\":\"tool exploded\"}}"
          else
            resp="{\"jsonrpc\":\"2.0\",\"id\":$id,\"result\":{\"content\":[{\"type\":\"text\",\"text\":\"recovered\"}]}}"
          fi ;;
      esac ;;
  esac
  if [ -n "$resp" ]; then printf '%s\n' "$resp"; fi
done
`
	path := filepath.Join(t.TempDir(), "server.sh")
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestMCPServerExitIsNotAnEmptySuccess: a server that dies mid-call used
// to close the pending channel with nothing in it, which the client read
// as an empty result and the app kept as a live client.
func TestMCPServerExitIsNotAnEmptySuccess(t *testing.T) {
	c, err := newMCPClient(t.Context(), MCPServer{Name: "flaky", Command: fakeMCPServer(t, "die")})
	if err != nil {
		t.Fatalf("the handshake must succeed: %v", err)
	}
	defer c.Close()
	tools, err := c.listTools(t.Context())
	if err != nil || len(tools) != 1 {
		t.Fatalf("tools/list: %d tools, %v", len(tools), err)
	}
	out, err := c.callTool(t.Context(), "flaky", "{}")
	if err == nil {
		t.Fatalf("a server that exits mid-call must fail, got %q", out)
	}
	if out != "" {
		t.Fatalf("a dead server must not produce output: %q", out)
	}
}

// TestMCPHandshakeFailsOnImmediateExit: the same hole at connect time —
// a server that never answers initialize must not be reported healthy.
func TestMCPHandshakeFailsOnImmediateExit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dead.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if c, err := newMCPClient(t.Context(), MCPServer{Name: "dead", Command: path}); err == nil {
		c.Close()
		t.Fatal("a server that dies before answering initialize must fail to connect")
	}
}

// TestMCPInBandErrorDoesNotPoisonTheClient: one failing tool call used
// to set the client's transport error, so every later call on the same
// connection failed for the rest of the session.
func TestMCPInBandErrorDoesNotPoisonTheClient(t *testing.T) {
	c, err := newMCPClient(t.Context(), MCPServer{Name: "flaky", Command: fakeMCPServer(t, "once")})
	if err != nil {
		t.Fatalf("the handshake must succeed: %v", err)
	}
	defer c.Close()

	if _, err := c.callTool(t.Context(), "flaky", "{}"); err == nil || !strings.Contains(err.Error(), "tool exploded") {
		t.Fatalf("the in-band error must reach the caller: %v", err)
	}
	got, err := c.callTool(t.Context(), "flaky", "{}")
	if err != nil {
		t.Fatalf("a second call must still work after an in-band error: %v", err)
	}
	if !strings.Contains(got, "recovered") {
		t.Fatalf("second call result: %q", got)
	}
}

// TestMCPToolNamesStayUniqueWhenTruncated: the gate routes a call to the
// first tool whose name matches, so two long server/tool pairs that
// truncate to the same 64 characters would answer for each other.
func TestMCPToolNamesStayUniqueWhenTruncated(t *testing.T) {
	tool := strings.Repeat("t", 20)
	a := mcpToolName(strings.Repeat("a", 70)+"1", tool)
	b := mcpToolName(strings.Repeat("a", 70)+"2", tool)
	if len(a) > 64 || len(b) > 64 {
		t.Fatalf("names must fit the provider's limit: %d %d", len(a), len(b))
	}
	if a == b {
		t.Fatalf("truncated names collided: %q", a)
	}
	if a != mcpToolName(strings.Repeat("a", 70)+"1", tool) {
		t.Fatal("a name must be stable, or a selector rule would stop matching")
	}
	if !strings.HasPrefix(a, "mcp_") || !strings.HasPrefix(b, "mcp_") {
		t.Fatalf("truncated names lost their prefix: %q %q", a, b)
	}
}

// TestMCPResultIsBoundedLikeALocalTool pins the cap: a tool the app does
// not own has no trim of its own, and its result rides straight into the
// context — the local tools' cap applies at the one place every MCP
// result passes through, shaped head and tail like theirs.
func TestMCPResultIsBoundedLikeALocalTool(t *testing.T) {
	c, err := newMCPClient(t.Context(), MCPServer{Name: "flaky", Command: fakeMCPServer(t, "huge")})
	if err != nil {
		t.Fatalf("the handshake must succeed: %v", err)
	}
	defer c.Close()

	got, err := c.callTool(t.Context(), "flaky", "{}")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) > maxToolResultBytes+100 {
		t.Fatalf("the result was not bounded: %d bytes", len(got))
	}
	if !strings.Contains(got, "output truncated") {
		t.Fatalf("the cut was not marked: %q", got[:80])
	}
	if !strings.HasPrefix(got, "xxxx") || !strings.HasSuffix(got, "xxxx") {
		t.Fatal("the bounded result lost an end")
	}
}
