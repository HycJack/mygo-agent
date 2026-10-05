package builtin

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func toolCall(name, args string) ToolCall {
	var c ToolCall
	c.Function.Name = name
	c.Function.Arguments = args
	return c
}

func echoTool(name string, actions ...Action) Tool {
	if len(actions) == 0 {
		actions = []Action{ActionFileRead}
	}
	return Tool{
		Name:    name,
		Actions: actions,
		Execute: func(ctx context.Context, args string) (string, error) {
			return "ran " + name, nil
		},
	}
}

func TestPolicyModeDefaults(t *testing.T) {
	bash := echoTool("bash", ActionShell)
	edit := echoTool("edit_file", ActionFileWrite)
	read := echoTool("read_file", ActionFileRead)
	mcp := echoTool("mcp_srv_tool", ActionMCP)

	cases := []struct {
		mode Mode
		tool Tool
		want Permission
	}{
		{ModeReadOnly, bash, PermDeny},
		{ModeReadOnly, edit, PermDeny},
		{ModeReadOnly, mcp, PermDeny},
		{ModeReadOnly, read, PermAllow},
		{ModeAgent, bash, PermAllow},
		{ModeAgent, edit, PermAllow},
		{ModeAgent, mcp, PermAsk},
		{ModeAgent, read, PermAllow},
		{ModeFull, bash, PermAllow},
		{ModeFull, mcp, PermAllow},
	}
	for _, c := range cases {
		p := Policy{Mode: c.mode}
		if got := p.Resolve(c.tool.Name, c.tool.Actions); got != c.want {
			t.Errorf("mode %d %s: got %s, want %s", c.mode, c.tool.Name, got, c.want)
		}
	}
	// A tool with no declared action fails closed.
	if got := (Policy{Mode: ModeFull}).Resolve("mystery", nil); got != PermDeny {
		t.Fatalf("undeclared action must fail closed, got %s", got)
	}
}

// TestRuleValueFailClosed pins the fail-closed rule: a rule whose value
// is not exactly allow/deny/ask denies, even in full mode — a malformed
// rule must never widen the gate (permissions.md).
func TestRuleValueFailClosed(t *testing.T) {
	p := Policy{
		Mode: ModeFull,
		Rules: Rules{
			"bash":      Permission("Deny"), // wrong case
			"edit_file": Permission(""),     // empty
			"mcp_x_y":   Permission("alow"), // typo
		},
	}
	cases := map[string][]Action{
		"bash":      {ActionShell},
		"edit_file": {ActionFileWrite},
		"mcp_x_y":   {ActionMCP},
	}
	for name, actions := range cases {
		if got := p.Resolve(name, actions); got != PermDeny {
			t.Errorf("malformed rule %q: got %s, want deny", name, got)
		}
	}
	// A valid rule in the same policy still resolves normally.
	ok := Policy{Mode: ModeFull, Rules: Rules{"bash": PermAllow}}
	if got := ok.Resolve("bash", []Action{ActionShell}); got != PermAllow {
		t.Errorf("valid rule: got %s, want allow", got)
	}
}

// TestPermissionFromConfig pins the loader's validation: case and space
// are tolerated, anything else fails closed to deny.
func TestPermissionFromConfig(t *testing.T) {
	cases := map[string]Permission{
		"allow": PermAllow, " ALLOW ": PermAllow,
		"deny": PermDeny, "Deny": PermDeny, "deny  ": PermDeny,
		"ask": PermAsk,
		// Anything else denies, never allows.
		"alow": PermDeny, "": PermDeny, "yes": PermDeny, "Allow ": PermAllow,
	}
	for in, want := range cases {
		if got := PermissionFromConfig(in); got != want {
			t.Errorf("PermissionFromConfig(%q): got %s, want %s", in, got, want)
		}
	}
}

// TestEditFileConfinesWrites asserts the permissions.md rule: with
// ConfineWrites (agent mode) the file tool itself rejects targets outside
// the workdir; in full mode it does not.
func TestEditFileConfinesWrites(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	inside := filepath.Join(dir, "in.txt")
	outFile := filepath.Join(outside, "out.txt")
	for _, f := range []string{inside, outFile} {
		if err := os.WriteFile(f, []byte("a"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	findEdit := func(tools []Tool) Tool {
		for _, tl := range tools {
			if tl.Name == "edit_file" {
				return tl
			}
		}
		t.Fatal("no edit_file tool")
		return Tool{}
	}
	args := func(path string) string {
		return fmt.Sprintf(`{"path":%q,"old_text":"a","new_text":"b"}`, path)
	}

	edit := findEdit(Tools(dir, nil, ToolOptions{ConfineWrites: true}))
	if _, err := edit.Execute(context.Background(), args(outside)); err == nil {
		t.Fatal("confined edit_file wrote outside the workdir")
	}
	if _, err := edit.Execute(context.Background(), args(inside)); err != nil {
		t.Fatalf("confined edit_file refused an in-workdir target: %v", err)
	}

	// Full access: the same outside target is allowed, that is the mode's
	// contract.
	free := findEdit(Tools(dir, nil))
	if _, err := free.Execute(context.Background(), args(outFile)); err != nil {
		t.Fatalf("full-access edit_file refused an outside target: %v", err)
	}
}

func TestPolicySelectorRules(t *testing.T) {
	p := Policy{
		Mode: ModeAgent,
		Rules: Rules{
			"bash":           PermAsk,   // exact
			"mcp_github_*":   PermAllow, // prefix
			"mcp_unsafe_*":   PermDeny,  // competing prefix
			"mcp_github_x_*": PermAsk,   // longer prefix wins over shorter
			"*":              PermDeny,  // global fallback
			"read_file":      PermAllow, // exact beats the global fallback
		},
	}
	cases := map[string]Permission{
		"bash":           PermAsk,
		"mcp_github_pr":  PermAllow,
		"mcp_unsafe_rm":  PermDeny,
		"mcp_github_x_y": PermAsk,
		"read_file":      PermAllow,
		"grep":           PermDeny, // the * fallback
		"list_files":     PermDeny,
	}
	for name, want := range cases {
		if got := p.Resolve(name, []Action{ActionFileRead}); got != want {
			t.Errorf("%s: got %s, want %s", name, got, want)
		}
	}
}

// gate runs one call through the loop's gate without streaming.
func gate(ctx context.Context, cfg LoopConfig, call ToolCall) (string, error) {
	return runGatedTool(ctx, cfg, call)
}

func TestGateDeniesInReadOnly(t *testing.T) {
	cfg := LoopConfig{Policy: Policy{Mode: ModeReadOnly}, Tools: []Tool{echoTool("bash", ActionShell)}}
	out, err := gate(context.Background(), cfg, toolCall("bash", `{}`))
	if err == nil {
		t.Fatalf("readonly allowed the shell: %q", out)
	}
	var d *DenialError
	if !asDenial(err, &d) {
		t.Fatalf("want a DenialError, got %T %v", err, err)
	}
	if !strings.Contains(d.Error(), "bash") {
		t.Fatalf("denial does not name the tool: %v", d)
	}
}

func asDenial(err error, d **DenialError) bool {
	if e, ok := err.(*DenialError); ok {
		*d = e
		return true
	}
	return false
}

func TestGateApproveAndDeny(t *testing.T) {
	cfg := LoopConfig{
		Policy: Policy{Mode: ModeAgent, Rules: Rules{"bash": PermAsk}},
		Tools:  []Tool{echoTool("bash", ActionShell)},
		OnApproval: func(_ context.Context, req ApprovalRequest) ApprovalDecision {
			if req.Summary == "" {
				t.Error("approval request has no summary")
			}
			if strings.Contains(req.Summary, "secret") {
				return ApprovalDecision{Reason: "looks secret"}
			}
			return ApprovalDecision{Approved: true}
		},
	}
	if out, err := gate(context.Background(), cfg, toolCall("bash", `{}`)); err != nil || out != "ran bash" {
		t.Fatalf("approved call did not run: %q %v", out, err)
	}
	_, err := gate(context.Background(), cfg, toolCall("bash", `{"command":"echo secret"}`))
	var d *DenialError
	if !asDenial(err, &d) || d.Reason != "looks secret" {
		t.Fatalf("want denial with reason, got %v", err)
	}
}

func TestGateAskWithoutHandlerDenies(t *testing.T) {
	cfg := LoopConfig{
		Policy: Policy{Mode: ModeAgent, Rules: Rules{"bash": PermAsk}},
		Tools:  []Tool{echoTool("bash", ActionShell)},
	}
	_, err := gate(context.Background(), cfg, toolCall("bash", `{}`))
	var d *DenialError
	if !asDenial(err, &d) || !strings.Contains(d.Reason, "no approval handler") {
		t.Fatalf("ask without handler must deny up front, got %v", err)
	}
}

func TestGateApprovalTimeoutDenies(t *testing.T) {
	cfg := LoopConfig{
		Policy:          Policy{Mode: ModeAgent, Rules: Rules{"bash": PermAsk}},
		Tools:           []Tool{echoTool("bash", ActionShell)},
		OnApproval:      func(ctx context.Context, _ ApprovalRequest) ApprovalDecision { <-ctx.Done(); return ApprovalDecision{} },
		ApprovalTimeout: 50 * time.Millisecond,
	}
	start := time.Now()
	_, err := gate(context.Background(), cfg, toolCall("bash", `{}`))
	if time.Since(start) > 5*time.Second {
		t.Fatal("the gate waited far beyond its timeout")
	}
	var d *DenialError
	if !asDenial(err, &d) || !strings.Contains(d.Reason, "timed out") {
		t.Fatalf("expiry must deny, got %v", err)
	}
}

func TestGateCancellationDenies(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cfg := LoopConfig{
		Policy:          Policy{Mode: ModeAgent, Rules: Rules{"bash": PermAsk}},
		Tools:           []Tool{echoTool("bash", ActionShell)},
		OnApproval:      func(ctx context.Context, _ ApprovalRequest) ApprovalDecision { <-ctx.Done(); return ApprovalDecision{} },
		ApprovalTimeout: time.Minute,
	}
	go func() { time.Sleep(30 * time.Millisecond); cancel() }()
	_, err := gate(ctx, cfg, toolCall("bash", `{}`))
	var d *DenialError
	if !asDenial(err, &d) || !strings.Contains(d.Reason, "cancelled") {
		t.Fatalf("cancellation must deny, got %v", err)
	}
}

func TestApprovalSummaryRedactsAndBounds(t *testing.T) {
	long := strings.Repeat("x", 500)
	s := ApprovalSummary(toolCall("bash", `{"command":"echo `+long+`"}`))
	if len(s) > 200 {
		t.Fatalf("summary not bounded: %d", len(s))
	}
	if s := ApprovalSummary(toolCall("edit_file", `{"path":"/etc/hosts"}`)); s != "edit_file /etc/hosts" {
		t.Fatalf("path summary: %q", s)
	}
	// MCP calls show a bounded view of their arguments: the user is not
	// approving a bare tool name.
	if s := ApprovalSummary(toolCall("mcp_x_y", `{"q":"z"}`)); s != `mcp_x_y {"q":"z"}` {
		t.Fatalf("mcp summary: %q", s)
	}
	if s := ApprovalSummary(toolCall("mcp_x_y", `{"q":"`+long+`"}`)); len(s) > 200 {
		t.Fatalf("mcp summary not bounded: %d", len(s))
	}
	if s := ApprovalSummary(toolCall("mcp_x_y", `{}`)); s != `mcp_x_y {}` {
		t.Fatalf("empty mcp summary: %q", s)
	}
}
