package app

import (
	uipkg "mygo-agent/internal/ui"

	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"mygo-agent/internal/harness"
	"mygo-agent/internal/harness/builtin"

	"github.com/egoist/mygo/ui"
)

func newTestApp(t *testing.T) *app {
	t.Helper()
	a := newApp()
	a.savePath = ""   // keep tests off the user's config directory
	a.configPath = "" // (and off its saved configuration)
	a.workdir = t.TempDir()
	// newApp already loaded the user's real configuration; reset what it
	// brought along so tests see a fresh install.
	a.projects = []Project{{ID: "default", Path: a.workdir}}
	a.activeProject = "default"
	a.providers = []Provider{{ID: "codex", Name: "Codex CLI", Models: slices.Clone(defaultModels)}}
	a.providerID = "codex"
	a.model = defaultModels[0]
	a.effort = 1
	a.backend = "builtin"
	a.mcpServers = nil
	// The agents block too: newApp's migration may have folded the real
	// config into profiles the tests should not see.
	a.agents = []Agent{{ID: "default", Name: "Default"}}
	a.defaultAgent = "default"
	a.activeAgent = ""
	return a
}

func waitUntil(t *testing.T, tt *ui.Tester, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		tt.Frame()
		if done() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timed out waiting for the agent to finish")
}

// TestSettingsModalCloses pins the dialog's close contract: a backdrop
// click must close it and stay closed — the view-side flag has to reach
// host state, or the next frame rebuilds the dialog open again.
func TestSettingsModalCloses(t *testing.T) {
	a := newTestApp(t)
	a.settingsSel = "codex"
	tt := ui.NewTester(a.view, 1240, 800)

	a.settingsTab = "providers"
	a.settingsOpen = true
	tt.Frame()
	if !tt.HasText("PROVIDERS") {
		t.Fatal("the settings dialog is not on screen")
	}

	// The dim backdrop: a click far outside the panel.
	tt.ClickAt(30, 100)
	tt.Frame()
	tt.Frame()
	if a.settingsOpen {
		t.Fatal("the backdrop click did not reach host state")
	}
	if tt.HasText("PROVIDERS") {
		t.Fatal("the settings dialog is still on screen")
	}
}

func TestHomeRenders(t *testing.T) {
	a := newTestApp(t)
	tt := ui.NewTester(a.view, 1240, 800)
	if !tt.HasText("What are we coding next?") {
		t.Fatalf("home greeting missing; texts: %v", tt.Texts())
	}
	if !tt.HasText("New task") {
		t.Fatal("sidebar is missing the new-task button")
	}
}

func TestChipFillsDraft(t *testing.T) {
	a := newTestApp(t)
	tt := ui.NewTester(a.view, 1240, 800)
	if err := tt.Click("Find and fix a failing test"); err != nil {
		t.Fatal(err)
	}
	if a.draft != "Find and fix a failing test" {
		t.Fatalf("draft is %q", a.draft)
	}
}

// TestSendRunsBuiltinAgent drives the full send path through the UI:
// the Send button creates the thread, the built-in agent runs against a
// fake provider (tool round trip included), and the composer and rail
// settle back when the turn finishes.
func TestSendRunsBuiltinAgent(t *testing.T) {
	dir := t.TempDir()
	a, _ := builtinFixture(t, dir)
	tt := ui.NewTester(a.view, 1240, 800)
	a.homeViewModel().Draft = "Explain the layout system"
	tt.Frame()
	if err := tt.Click("Send"); err != nil {
		t.Fatal(err)
	}
	if len(a.threads) != 1 {
		t.Fatalf("expected 1 thread, have %d", len(a.threads))
	}
	th := a.threads[0]
	if len(th.Messages) != 2 || th.Messages[0].Role != "user" {
		t.Fatalf("messages after send: %d", len(th.Messages))
	}
	if th.Title == "" {
		t.Fatal("the thread was not titled from the prompt")
	}
	waitUntil(t, tt, func() bool {
		stopped := false
		a.update(func() { stopped = !a.isRunning(th.ID) })
		return stopped
	})
	// The composer is back to Send, and the rail's spinner is gone.
	tt.Frame()
	if tt.HasText("Stop") {
		t.Fatal("the send button still shows Stop after the turn finished")
	}
	for _, tvm := range a.sidebarViewModel().Threads {
		if tvm.Running {
			t.Fatal("the sidebar still shows the task as running")
		}
	}
	reply := th.Messages[1]
	if !strings.Contains(reply.Text, "all done") {
		t.Fatalf("reply text %q", reply.Text)
	}
	var ran bool
	for _, b := range reply.Blocks {
		if b.Type == "command" && strings.Contains(b.Output, "hello-from-tool") {
			ran = true
		}
	}
	if !ran {
		t.Fatalf("the tool card is missing: %+v", reply.Blocks)
	}
	if !tt.HasText("Explain the layout system") {
		t.Fatal("the sent message is not on screen")
	}
}

func TestSidebarSwitchAndRename(t *testing.T) {
	a := newTestApp(t)
	now := time.Now()
	a.threads = []*Thread{
		{ID: "t1", ProjectID: "default", Title: "First task", Updated: now},
		{ID: "t2", ProjectID: "default", Title: "Second task", Updated: now.Add(-time.Hour)},
	}
	a.current = "t1"
	tt := ui.NewTester(a.view, 1240, 800)
	if err := tt.Click("Second task"); err != nil {
		t.Fatal(err)
	}
	if a.current != "t2" {
		t.Fatalf("current is %q", a.current)
	}
	// Rename through the modal.
	a.renaming, a.renameID, a.renameDraft = true, "t2", "Renamed task"
	tt.Frame()
	if err := tt.Click("Rename"); err != nil {
		t.Fatal(err)
	}
	if a.byID("t2").Title != "Renamed task" {
		t.Fatalf("title is %q", a.byID("t2").Title)
	}
	if a.renaming {
		t.Fatal("the rename dialog is still open")
	}
}

func TestDeleteTask(t *testing.T) {
	a := newTestApp(t)
	now := time.Now()
	a.threads = []*Thread{
		{ID: "t1", ProjectID: "default", Title: "Doomed task", Updated: now},
		{ID: "t2", ProjectID: "default", Title: "Kept task", Updated: now.Add(-time.Hour)},
	}
	a.current = "t2"
	tt := ui.NewTester(a.view, 1240, 800)
	// Hover the row so its delete button is built, then click it.
	r, ok := tt.Find("Doomed task")
	if !ok {
		t.Fatal("the task row is missing")
	}
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	tt.Frame()
	tt.Frame()
	if err := tt.Click("Delete task"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.byID("t1") != nil {
		t.Fatal("the task was not deleted")
	}
	if a.current != "t2" {
		t.Fatalf("the selection moved to %q", a.current)
	}
}

func TestConfigMigratesLegacyModels(t *testing.T) {
	dir := t.TempDir()
	cfg := `{
  "model": "old-model",
  "custom_models": ["legacy-model"],
  "effort": 2,
  "backend": "codex"
}`
	if err := os.WriteFile(dir+"/config.json", []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	a := newTestApp(t)
	a.configPath = dir + "/config.json"
	a.loadConfig()
	a.ensureDefaults()
	p := a.providerByID("codex")
	if p == nil {
		t.Fatal("the built-in codex provider is missing after migration")
	}
	for _, m := range []string{"old-model", "legacy-model", "gpt-5.2-codex"} {
		if !slices.Contains(p.Models, m) {
			t.Fatalf("migrated models lack %q: %v", m, p.Models)
		}
	}
}

func TestProviderConfigPersists(t *testing.T) {
	dir := t.TempDir()
	a := newTestApp(t)
	a.configPath = dir + "/config.json"
	a.projects = []Project{{ID: "p1", Path: "/tmp/proj"}}
	a.activeProject = "p1"
	a.providers = append(a.providers, Provider{
		ID: "prov1", Name: "DeepSeek", BaseURL: "https://api.deepseek.com/v1",
		APIKey: "sk-test", Models: []string{"deepseek-chat"},
	})
	a.providerID, a.model, a.effort, a.backend = "prov1", "deepseek-chat", 2, "codex"
	a.saveConfig()

	b := newTestApp(t)
	b.configPath = a.configPath
	b.loadConfig()
	b.ensureDefaults()
	if b.providerID != "prov1" || b.model != "deepseek-chat" {
		t.Fatalf("provider selection lost: %s / %s", b.providerID, b.model)
	}
	p := b.providerByID("prov1")
	if p == nil || p.APIKey != "sk-test" || p.BaseURL != "https://api.deepseek.com/v1" {
		t.Fatalf("provider did not round-trip: %+v", p)
	}
	if b.activeProject != "p1" || b.projectByID("p1").Path != "/tmp/proj" {
		t.Fatalf("projects did not round-trip: %v", b.projects)
	}
}

func TestProjectSwitchFiltersThreads(t *testing.T) {
	a := newTestApp(t)
	a.projects = []Project{{ID: "pa", Path: "/tmp/a"}, {ID: "pb", Path: "/tmp/b"}}
	a.activeProject = "pa"
	now := time.Now()
	a.threads = []*Thread{
		{ID: "t1", ProjectID: "pa", Title: "Task in A", Updated: now},
		{ID: "t2", ProjectID: "pb", Title: "Task in B", Updated: now},
	}
	a.current = "t1"
	a.switchProject("pb")
	if a.activeProject != "pb" || a.workdir != "/tmp/b" {
		t.Fatalf("switch failed: %q %q", a.activeProject, a.workdir)
	}
	// The rail's snapshot must not leak threads from other projects.
	for _, th := range a.sidebarViewModel().Threads {
		if th.ID == "t1" {
			t.Fatal("thread from another project leaked into the sidebar")
		}
	}
	if a.currentThread() != nil {
		t.Fatal("switching projects should return to the home screen")
	}
}

func TestSearchFiltersTasks(t *testing.T) {
	a := newTestApp(t)
	now := time.Now()
	a.threads = []*Thread{
		{ID: "t1", ProjectID: "default", Title: "Fix the parser", Updated: now},
		{ID: "t2", ProjectID: "default", Title: "Write the docs", Updated: now.Add(-time.Hour)},
	}
	tt := ui.NewTester(a.view, 1240, 800)
	tt.Frame() // build the rail's persistent view model
	a.sidebarVM.Search = "parser"
	tt.Frame()
	if tt.HasText("Write the docs") {
		t.Fatal("the filter did not hide the unrelated task")
	}
	if !tt.HasText("Fix the parser") {
		t.Fatal("the matching task is missing")
	}
}

func TestMarkdownTableAndBlocks(t *testing.T) {
	src := strings.Join([]string{
		"| Field | Type |",
		"|-------|------|",
		"| attempts | `atomic.Int32` |",
		"| deadline | time.Time |",
		"",
		"> quoted note",
		"",
		"---",
		"",
		"1. first item",
		"2. second item",
	}, "\n")
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(codexTheme())
		uipkg.Markdown(c, uipkg.NewMdCache(), "test", src, true, codexPalette())
	}, 900, 1200)
	for _, want := range []string{"Field", "Type", "attempts", "deadline", "time.Time",
		"quoted note", "first item", "second item"} {
		if !tt.HasText(want) {
			t.Logf("text: %q", want)
			t.Fatalf("missing %q in %v", want, tt.Texts())
		}
	}
	for _, gone := range []string{"| Field |", "|---|", "> quoted"} {
		if tt.HasText(gone) {
			for _, s := range tt.Texts() {
				t.Logf("text: %q", s)
			}
			t.Fatalf("raw markdown leaked: %q", gone)
		}
	}
}

func TestMarkdownSpans(t *testing.T) {
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(codexTheme())
		uipkg.Markdown(c, uipkg.NewMdCache(), "test",
			"Plain **bold** and `code` and ~~gone~~ and [a link](https://example.com).\n\n```go\nx := 1\n```",
			true, codexPalette())
	}, 400, 300)
	// The marks are gone, the words stay (inline runs are separate text
	// nodes).
	for _, want := range []string{"Plain ", "bold", " and ", "code", " and ", "a link"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %v", want, tt.Texts())
		}
	}
	if tt.HasText("**") || tt.HasText("~~") {
		t.Fatal("markdown marks leaked into the text")
	}
	if !tt.HasText("x := 1") {
		t.Fatal("code fence missing")
	}
	if !tt.HasText("go") {
		t.Fatal("code card language label missing")
	}
}

func TestUnifiedDiffParsing(t *testing.T) {
	lines := harness.ParseUnifiedDiff("diff --git a/x.go b/x.go\n--- a/x.go\n+++ b/x.go\n@@ -1,3 +1,3 @@\n context\n-removed\n+added")
	if len(lines) != 4 || lines[0].Kind != '@' {
		t.Fatalf("got %d lines: %v", len(lines), lines)
	}
	if lines[1].Kind != ' ' || lines[2].Kind != '-' || lines[3].Kind != '+' {
		t.Fatalf("kinds: %c %c %c", lines[1].Kind, lines[2].Kind, lines[3].Kind)
	}
	if lines[2].Text != "removed" || lines[3].Text != "added" {
		t.Fatalf("texts: %q %q", lines[2].Text, lines[3].Text)
	}
	add, del := harness.DiffStats(lines)
	if add != 1 || del != 1 {
		t.Fatalf("stats: +%d −%d", add, del)
	}
}

func TestWordDiffMarks(t *testing.T) {
	// "return old value" → "return new value": the middle word changed.
	lines := harness.ParseUnifiedDiff("-return oldValue\n+return newValue\n context")
	del, add := lines[0], lines[1]
	if del.MarkHi <= del.MarkLo || add.MarkHi <= add.MarkLo {
		t.Fatalf("no word marks: %+v %+v", del, add)
	}
	d := []rune(del.Text)
	a := []rune(add.Text)
	// Both words share the suffix "Value", so the mark is just the part
	// that truly changed.
	if string(d[del.MarkLo:del.MarkHi]) != "old" {
		t.Fatalf("del mark %q", string(d[del.MarkLo:del.MarkHi]))
	}
	if string(a[add.MarkLo:add.MarkHi]) != "new" {
		t.Fatalf("add mark %q", string(a[add.MarkLo:add.MarkHi]))
	}
	// Whole-line rewrites get no mark; the row tint is enough.
	lines = harness.ParseUnifiedDiff("-completely different\n+totally other things")
	if lines[0].MarkHi > lines[0].MarkLo {
		t.Fatalf("unexpected mark on a rewrite: %+v", lines[0])
	}
}

func TestEffectiveMCPServersMergesDotMCPJSON(t *testing.T) {
	a := newTestApp(t)
	a.mcpServers = []builtin.MCPServer{{Name: "configured", Command: "configured-cmd"}}
	mcpJSON := `{"mcpServers":{"from-project":{"command":"npx","args":["-y","@modelcontextprotocol/server-everything"]}}}`
	if err := os.WriteFile(filepath.Join(a.workdir, ".mcp.json"), []byte(mcpJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	got := a.effectiveMCPServers()
	if len(got) != 2 {
		t.Fatalf("servers %+v", got)
	}
	if got[0].Name != "configured" || got[1].Name != "from-project" || got[1].Command != "npx" {
		t.Fatalf("merge order/content wrong: %+v", got)
	}
	// The project file never overrides the configured name.
}
