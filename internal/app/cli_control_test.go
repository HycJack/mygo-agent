//go:build !windows

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mygo-agent/internal/harness"
)

// waitForApprovalCard polls the running reply for a pending approval
// card and returns its id.
func waitForApprovalCard(t *testing.T, a *app, th *Thread) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		for i := range th.Messages {
			for _, b := range th.Messages[i].Blocks {
				if b.Type == "approval" && b.Running && b.ApprovalID != "" {
					return b.ApprovalID
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	a.update(func() {
		for i, m := range th.Messages {
			t.Logf("message[%d] role=%s running=%v text=%q", i, m.Role, m.Running, m.Text)
			for j, b := range m.Blocks {
				t.Logf("  block[%d] type=%s running=%v text=%q out=%q", j, b.Type, b.Running, b.Text, b.Output)
			}
		}
	})
	t.Fatal("no pending approval card appeared")
	return ""
}

// fakeCodexAppServer speaks the app-server wire minimally: it answers
// initialize and thread/start, and on turn/start asks for one command
// approval before completing. The received decision lands in
// <dir>/decision.json.
const fakeCodexAppServer = `#!/bin/bash
while IFS= read -r line; do
  case "$line" in
    *'"method":"initialize"'*)
      id=$(printf '%s' "$line" | sed -E 's/.*"id":([0-9]+).*/\1/')
      printf '{"jsonrpc":"2.0","id":%s,"result":{"userAgent":"fake-codex"}}\n' "$id" ;;
    *'"method":"thread/start"'*)
      id=$(printf '%s' "$line" | sed -E 's/.*"id":([0-9]+).*/\1/')
      printf '{"jsonrpc":"2.0","id":%s,"result":{"thread":{"id":"sess-app-1"}}}\n' "$id" ;;
    *'"method":"turn/start"'*)
      id=$(printf '%s' "$line" | sed -E 's/.*"id":([0-9]+).*/\1/')
      printf '{"jsonrpc":"2.0","id":%s,"result":{"turnId":"u"}}\n' "$id"
      printf '{"jsonrpc":"2.0","method":"item/started","params":{"item":{"id":"it1","type":"command_execution","command":"echo hi","status":"in_progress"}}}\n'
      printf '{"jsonrpc":"2.0","id":900,"method":"item/commandExecution/requestApproval","params":{"threadId":"t","turnId":"u","itemId":"it1","command":"rm -rf /tmp/x","reason":"outside sandbox"}}\n'
      while IFS= read -r resp; do
        case "$resp" in
          *'"id":900'*) printf '%s\n' "$resp" > "decision.json"; break ;;
        esac
      done
      printf '{"jsonrpc":"2.0","method":"item/completed","params":{"item":{"id":"it1","type":"command_execution","command":"echo hi","status":"completed","exitCode":0,"aggregatedOutput":"hi"}}}\n'
      printf '{"jsonrpc":"2.0","method":"item/agentMessage/delta","params":{"itemId":"m1","delta":"all "}}\n'
      printf '{"jsonrpc":"2.0","method":"item/agentMessage/delta","params":{"itemId":"m1","delta":"done"}}\n'
      printf '{"jsonrpc":"2.0","method":"turn/completed","params":{"threadId":"t","turn":{"id":"u","status":"completed"}}}\n'
      exit 0 ;;
  esac
done
`

// fakeClaude speaks the stream-json control protocol: after the user
// message it asks can_use_tool for Bash and records the control_response
// it receives in <dir>/claude_decision.json before finishing.
const fakeClaude = `#!/bin/bash
IFS= read -r user
printf '%s\n' "$user" > "prompt.json"
printf '{"type":"system","subtype":"init","session_id":"sess-claude-1"}\n'
printf '{"type":"assistant","message":{"content":[{"type":"text","text":"hello "}]}}\n'
printf '{"type":"control_request","request_id":"req-1","request":{"subtype":"can_use_tool","tool_name":"Bash","input":{"command":"rm -rf /tmp/x"}}}\n'
IFS= read -r resp
printf '%s\n' "$resp" > "claude_decision.json"
printf '{"type":"assistant","message":{"content":[{"type":"text","text":"world"}]}}\n'
printf '{"type":"result","subtype":"success","duration_ms":5,"total_cost_usd":0,"result":"ok"}\n'
`

func writeScript(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func readJSON(t *testing.T, path string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
			return string(data)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no file at %s", path)
	return ""
}

// newCLITestApp builds an app wired to a fake CLI binary for one backend.
func newCLITestApp(t *testing.T, dir, backend, script string) (*app, *Thread) {
	t.Helper()
	a := newTestApp(t)
	a.workdir = dir
	a.backend = backend
	a.mode = 1 // agent
	if backend == "codex" {
		a.codexPath = script
	} else {
		a.claudePath = script
	}
	th := &Thread{ID: "t1", ProjectID: "default"}
	th.Messages = []Message{{ID: "m0", Role: "assistant", Running: true}}
	a.threads = append(a.threads, th)
	a.current = "t1"
	return a, th
}

// TestCodexAppServerApprovalRoundTrip drives the app-server transport
// end to end: thread id persisted, approval card for the command,
// decision written back as accept, cards and text rendered.
func TestCodexAppServerApprovalRoundTrip(t *testing.T) {
	dir := t.TempDir()
	script := writeScript(t, dir, "fake-codex-app-server", fakeCodexAppServer)
	a, th := newCLITestApp(t, dir, "codex", script)

	go runBackend(a, th, "clean up", 0)
	id := waitForApprovalCard(t, a, th)
	a.resolveApproval(id, harness.ApprovalDecision{Approved: true})
	waitTurn(t, a, th, 0)

	if th.CodexID != "sess-app-1" {
		t.Fatalf("session id %q", th.CodexID)
	}
	decision := readJSON(t, filepath.Join(dir, "decision.json"))
	if !strings.Contains(decision, `"decision":"accept"`) {
		t.Fatalf("decision not accepted: %s", decision)
	}
	m := th.Messages[0]
	if !strings.Contains(m.Text, "all ") {
		t.Fatalf("reply text %q", m.Text)
	}
	var cmd, approval bool
	for _, b := range m.Blocks {
		switch {
		case b.Type == "command" && b.Exit == 0 && b.Output == "hi":
			cmd = true
		case b.Type == "approval" && !b.Running && b.Exit == 0:
			approval = true
		}
	}
	if !cmd || !approval {
		t.Fatalf("cards: %+v", m.Blocks)
	}
}

// TestCodexAppServerDeclineRoundTrip proves a denial reaches codex as
// {"decision":"decline"}.
func TestCodexAppServerDeclineRoundTrip(t *testing.T) {
	dir := t.TempDir()
	script := writeScript(t, dir, "fake-codex-app-server", fakeCodexAppServer)
	a, th := newCLITestApp(t, dir, "codex", script)

	go runBackend(a, th, "clean up", 0)
	id := waitForApprovalCard(t, a, th)
	a.resolveApproval(id, harness.ApprovalDecision{Reason: "not in agent mode"})
	waitTurn(t, a, th, 0)

	decision := readJSON(t, filepath.Join(dir, "decision.json"))
	if !strings.Contains(decision, `"decision":"decline"`) {
		t.Fatalf("decision not declined: %s", decision)
	}
}

// TestClaudeCanUseToolAllow proves the control protocol round trip: the
// allow decision goes back with the echoed input, the turn completes.
func TestClaudeCanUseToolAllow(t *testing.T) {
	dir := t.TempDir()
	script := writeScript(t, dir, "fake-claude", fakeClaude)
	a, th := newCLITestApp(t, dir, "claude", script)

	go runBackend(a, th, "clean up", 0)
	id := waitForApprovalCard(t, a, th)
	if id != "req-1" {
		t.Fatalf("approval id %q, want the CLI's request_id", id)
	}
	a.resolveApproval(id, harness.ApprovalDecision{Approved: true})
	waitTurn(t, a, th, 0)

	if th.ClaudeID != "sess-claude-1" {
		t.Fatalf("session id %q", th.ClaudeID)
	}
	decision := readJSON(t, filepath.Join(dir, "claude_decision.json"))
	if !strings.Contains(decision, `"behavior":"allow"`) || !strings.Contains(decision, "updatedInput") {
		t.Fatalf("allow decision malformed: %s", decision)
	}
	if !strings.Contains(decision, "rm -rf /tmp/x") {
		t.Fatalf("input not echoed: %s", decision)
	}
	if !strings.Contains(th.Messages[0].Text, "hello world") {
		t.Fatalf("reply text %q", th.Messages[0].Text)
	}
}

// TestClaudeCanUseToolDeny proves the deny path carries the reason.
func TestClaudeCanUseToolDeny(t *testing.T) {
	dir := t.TempDir()
	script := writeScript(t, dir, "fake-claude", fakeClaude)
	a, th := newCLITestApp(t, dir, "claude", script)

	go runBackend(a, th, "clean up", 0)
	id := waitForApprovalCard(t, a, th)
	a.resolveApproval(id, harness.ApprovalDecision{Reason: "too risky"})
	waitTurn(t, a, th, 0)

	decision := readJSON(t, filepath.Join(dir, "claude_decision.json"))
	if !strings.Contains(decision, `"behavior":"deny"`) || !strings.Contains(decision, "too risky") {
		t.Fatalf("deny decision malformed: %s", decision)
	}
}

// TestClaudeReadOnlyAutoDeny proves the read-only mode denies can_use_tool
// up front — no card, no wait.
func TestClaudeReadOnlyAutoDeny(t *testing.T) {
	dir := t.TempDir()
	script := writeScript(t, dir, "fake-claude", fakeClaude)
	a, th := newCLITestApp(t, dir, "claude", script)
	a.mode = 0

	go runBackend(a, th, "look around", 0)
	waitTurn(t, a, th, 0)

	decision := readJSON(t, filepath.Join(dir, "claude_decision.json"))
	if !strings.Contains(decision, `"behavior":"deny"`) || !strings.Contains(decision, "read-only mode") {
		t.Fatalf("read-only did not auto-deny: %s", decision)
	}
	for i := range th.Messages {
		for _, b := range th.Messages[i].Blocks {
			if b.Type == "approval" {
				t.Fatal("read-only must not surface an approval card")
			}
		}
	}
}

// TestClaudeUnknownControlSubtype proves an unsupported control request
// gets an error reply instead of stalling the CLI.
func TestClaudeUnknownControlSubtype(t *testing.T) {
	dir := t.TempDir()
	script := writeScript(t, dir, "fake-claude", `#!/bin/bash
IFS= read -r user
printf '{"type":"system","subtype":"init","session_id":"sess-2"}\n'
printf '{"type":"control_request","request_id":"req-9","request":{"subtype":"mcp_message","server":"x"}}\n'
IFS= read -r resp
printf '%s\n' "$resp" > "claude_decision.json"
printf '{"type":"result","subtype":"success","duration_ms":1,"total_cost_usd":0,"result":"ok"}\n'
`)
	a, th := newCLITestApp(t, dir, "claude", script)

	go runBackend(a, th, "hi", 0)
	waitTurn(t, a, th, 0)

	decision := readJSON(t, filepath.Join(dir, "claude_decision.json"))
	if !strings.Contains(decision, `"subtype":"error"`) || !strings.Contains(decision, "req-9") {
		t.Fatalf("unsupported subtype not answered with an error: %s", decision)
	}
}
