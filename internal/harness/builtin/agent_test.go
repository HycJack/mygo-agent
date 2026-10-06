package builtin

import (
	"mygo-agent/internal/harness/cli"

	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestUnifiedDiff(t *testing.T) {
	oldText := "alpha\nbeta\ngamma\ndelta\n"
	newText := "alpha\nbeta\nGAMMA\ndelta\n"
	lines := UnifiedDiff(oldText, newText)
	var del, add, ctx int
	for _, l := range lines {
		switch l.Kind {
		case '-':
			del++
		case '+':
			add++
		case ' ':
			ctx++
		}
	}
	if del != 1 || add != 1 {
		t.Fatalf("want one removed and one added line, got −%d +%d: %v", del, add, lines)
	}
	if ctx < 2 {
		t.Fatalf("common lines missing from the diff: %v", lines)
	}
	// Reconstructing the new text from the diff must work: keep ' ' and
	// '+' lines in order.
	var rebuilt []string
	for _, l := range lines {
		if l.Kind == ' ' || l.Kind == '+' {
			rebuilt = append(rebuilt, l.Text)
		}
	}
	if strings.Join(rebuilt, "\n") != strings.TrimRight(newText, "\n") {
		t.Fatalf("rebuild mismatch: %q", strings.Join(rebuilt, "\n"))
	}
}

func TestLcsDiffOversizedFallsBackToReplacement(t *testing.T) {
	a := make([]string, 1500)
	b := make([]string, 1500)
	for i := range a {
		a[i] = "old line"
		b[i] = "new line"
	}
	lines := LcsDiff(a, b)
	var del, add int
	for _, l := range lines {
		switch l.Kind {
		case '-':
			del++
		case '+':
			add++
		}
	}
	if del != 1500 || add != 1500 {
		t.Fatalf("oversized diff: −%d +%d", del, add)
	}
}

func TestMCPToolName(t *testing.T) {
	n := mcpToolName("my server", "search/web?query")
	if n != "mcp_my_server_search_web_query" {
		t.Fatalf("name %q", n)
	}
	long := mcpToolName(strings.Repeat("s", 80), strings.Repeat("t", 80))
	if len(long) > 64 {
		t.Fatalf("name too long: %d", len(long))
	}
}

// TestSkillRootsIncludeAimanager pins the user-level discovery roots:
// the aimanager skills directory is scanned alongside the others
// (spec/agents.md).
func TestSkillRootsIncludeAimanager(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home")
	}
	roots := skillRoots(t.TempDir())
	for _, want := range []string{
		filepath.Join(home, ".agents", "skills"),
		filepath.Join(home, ".aimanager", "skills"),
		filepath.Join(home, ".codex-go", "skills"),
	} {
		if !slices.Contains(roots, want) {
			t.Fatalf("roots lack %q: %v", want, roots)
		}
	}
	// Project roots come first, in walk order.
	if filepath.Base(roots[0]) != "skills" || filepath.Base(filepath.Dir(roots[0])) != ".agents" {
		t.Fatalf("roots[0] = %q", roots[0])
	}
}

func TestSkillParseAndDiscover(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, ".agents", "skills", "pdf-tools")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: pdf-tools\ndescription: Extract text from PDFs.\n---\n\nRead references/formats.md first.\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	// The home directories may carry the user's own skills; only ours
	// must be present.
	set := DiscoverSkills(dir)
	if len(set.Skills) == 0 {
		t.Fatal("discovered no skills")
	}
	var found *Skill
	for i := range set.Skills {
		if set.Skills[i].Name == "pdf-tools" {
			found = &set.Skills[i]
		}
	}
	if found == nil || found.Description != "Extract text from PDFs." {
		t.Fatalf("pdf-tools missing or wrong: %+v", set.Skills)
	}
	got, ok := set.Load("pdf-tools")
	if !ok || !strings.Contains(got, "references/formats.md") {
		t.Fatalf("load: %q %v", got, ok)
	}
	if !strings.Contains(set.PromptSection(), "pdf-tools: Extract text from PDFs.") {
		t.Fatalf("prompt section: %q", set.PromptSection())
	}
}

func TestGatedToolUnknownAndBadArgs(t *testing.T) {
	// The gated path is the only path: a call that does not resolve to a
	// tool, or whose arguments are not JSON, never reaches an Execute.
	cfg := LoopConfig{Tools: Tools(".", nil), Policy: Policy{Mode: ModeFull}}
	_, err := runGatedTool(t.Context(), cfg, toolCallNamed("nope", ""))
	if err == nil || !strings.Contains(err.Error(), "unknown tool") {
		t.Fatalf("unknown tool must fail: %v", err)
	}
	if _, err := runGatedTool(t.Context(), cfg, toolCallNamed("bash", "{not json")); err == nil {
		t.Fatal("invalid arguments must fail")
	}
}

// toolCallNamed builds one call for a tool name and argument string.
func toolCallNamed(name, args string) ToolCall {
	var call ToolCall
	call.Function.Name = name
	call.Function.Arguments = args
	return call
}

func TestTrimOutput(t *testing.T) {
	if got := cli.TrimOutput("hello\n", 100); got != "hello" {
		t.Fatalf("trim changed a short output: %q", got)
	}
	// Head and tail, not head alone: both ends of the original survive
	// around the marker, which is where a command's errors live.
	src := strings.Repeat("x", 5000)
	got := cli.TrimOutput(src, 100)
	if !strings.HasPrefix(got, src[:66]) || !strings.HasSuffix(got, src[len(src)-34:]) {
		t.Fatalf("the trimmed output lost an end: %d bytes", len(got))
	}
	if !strings.Contains(got, "output truncated") {
		t.Fatalf("the cut was not marked: %q", got)
	}
}

func TestParseToolArgsRepairsWeakModels(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", "{}"},
		{"  {}  ", "{}"},
		{`{"command":"ls"}`, `{"command":"ls"}`},
		{"```json\n{\"command\":\"ls -la\"}\n```", `{"command":"ls -la"}`},
		{"{`command`:`ls`}", `{"command":"ls"}`},
		{"{\"command\":\"go test\",}", `{"command":"go test"}`},
		{"{“path”:“main.go”}", `{"path":"main.go"}`},
	}
	for _, c := range cases {
		got, err := parseToolArgs(c.in)
		if err != nil {
			t.Errorf("parseToolArgs(%q): %v", c.in, err)
			continue
		}
		var a, b map[string]any
		if json.Unmarshal([]byte(got), &a) != nil || json.Unmarshal([]byte(c.want), &b) != nil {
			t.Fatalf("bad JSON: %q vs %q", got, c.want)
		}
		if fmt.Sprint(a) != fmt.Sprint(b) {
			t.Errorf("parseToolArgs(%q) = %v, want %v", c.in, got, c.want)
		}
	}
	if _, err := parseToolArgs("not json at all {"); err == nil {
		t.Fatal("garbage must still fail")
	}
}

func TestRunGatedToolRepairsArguments(t *testing.T) {
	ran := ""
	tools := []Tool{{
		Name:       "bash",
		Actions:    []Action{ActionFileRead}, // allowed in every mode
		Parameters: json.RawMessage(`{"type":"object"}`),
		Execute: func(ctx context.Context, args string) (string, error) {
			ran = args
			return "ok", nil
		},
	}}
	out, err := runGatedTool(t.Context(), LoopConfig{Tools: tools, Policy: Policy{Mode: ModeAgent}},
		toolCallNamed("bash", "```json\n{\"command\":\"pwd\"}\n```"))
	if err != nil || out != "ok" || ran != `{"command":"pwd"}` {
		t.Fatalf("execute: %q %v %q", out, err, ran)
	}
}

// TestRunGatedToolDeniedByPolicy asserts the gate really is on the path
// this test drives: a denied call never reaches the tool.
func TestRunGatedToolDeniedByPolicy(t *testing.T) {
	ran := false
	tools := []Tool{{
		Name:       "write",
		Actions:    []Action{ActionFileWrite},
		Parameters: json.RawMessage(`{"type":"object"}`),
		Execute: func(ctx context.Context, args string) (string, error) {
			ran = true
			return "ok", nil
		},
	}}
	_, err := runGatedTool(t.Context(), LoopConfig{Tools: tools, Policy: Policy{Mode: ModeReadOnly}},
		toolCallNamed("write", "{}"))
	var deny *DenialError
	if !errors.As(err, &deny) || ran {
		t.Fatalf("read-only mode must deny the call before running it: %v (ran=%v)", err, ran)
	}
}

func TestStreamChatAcceptsObjectArguments(t *testing.T) {
	// A gateway that sends tool call arguments as an object, and no
	// index on its deltas.
	sse := "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"id\":\"call_1\",\"function\":{\"name\":\"read_file\",\"arguments\":{\"path\":\"main.go\"}}}]}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n" +
		"data: [DONE]\n\n"
	res := accumulateSSE(t, sse)
	if len(res.ToolCalls) != 1 {
		t.Fatalf("tool calls: %+v", res.ToolCalls)
	}
	tc := res.ToolCalls[0]
	if tc.Function.Name != "read_file" {
		t.Fatalf("name %q", tc.Function.Name)
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
		t.Fatalf("arguments not JSON: %q", tc.Function.Arguments)
	}
	if args["path"] != "main.go" {
		t.Fatalf("arguments: %v", args)
	}
}

// accumulateSSE runs a streaming call against an in-memory SSE body.
func accumulateSSE(t *testing.T, sse string) assistantResult {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, sse)
	}))
	defer srv.Close()
	var got strings.Builder
	res, err := streamChat(t.Context(), StreamConfig{
		BaseURL: srv.URL, Model: "m",
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	}, func(d string) { got.WriteString(d) })
	if err != nil {
		panic(err)
	}
	return res
}

func TestStreamChatAccumulatesFragmentedStringArgs(t *testing.T) {
	sse := "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"c1\",\"function\":{\"name\":\"bash\",\"arguments\":\"{\\\"comm\"}}]}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"and\\\":\\\"ls\\\"}\"}}]}}]}\n\n" +
		"data: [DONE]\n\n"
	res := accumulateSSE(t, sse)
	if len(res.ToolCalls) != 1 || res.ToolCalls[0].Function.Arguments != `{"command":"ls"}` {
		t.Fatalf("fragmented accumulation: %+v", res.ToolCalls)
	}
}

// TestParseSkillFoldedDescription pins the multi-line YAML folded
// description (">" / "|"): the indented lines after the key join the
// description until a top-level key ends the block.
func TestParseSkillFoldedDescription(t *testing.T) {
	s := parseSkill("/tmp/skill", `---
name: team-relay
description: >
  Coordinate a panel of agents
  answering one thread in order.
  Second paragraph too.
version: 2
---
body text`)
	if s.Name != "team-relay" {
		t.Fatalf("name = %q", s.Name)
	}
	want := "Coordinate a panel of agents answering one thread in order. Second paragraph too."
	if s.Description != want {
		t.Fatalf("description = %q, want %q", s.Description, want)
	}
}
