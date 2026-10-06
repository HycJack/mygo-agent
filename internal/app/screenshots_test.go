package app

// The screenshot harness: renders the app's main surfaces offscreen and
// writes PNGs, so UI changes can be audited visually. Guarded by
// MYGO_UI_SHOTS — plain `go test ./...` never runs it.

import (
	"encoding/json"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	uipkg "mygo-agent/internal/ui"

	"mygo-agent/internal/config"
	"mygo-agent/internal/harness"

	"github.com/egoist/mygo/ui"
	"slices"
)

func shotDir(t *testing.T) string {
	t.Helper()
	dir := os.Getenv("MYGO_UI_SHOTS")
	if dir == "" {
		t.Skip("MYGO_UI_SHOTS not set")
	}
	return dir
}

func shotApp(t *testing.T) *app {
	t.Helper()
	a := newTestApp(t)
	a.version = "9047d94"
	a.providers = []Provider{
		{ID: "codex", Name: "Codex CLI", Wire: harness.WireResponses, Models: slices.Clone(defaultModels)},
		{ID: "p-ds", Name: "DeepSeek", BaseURL: "https://api.deepseek.com/v1", APIKey: "sk-demo", Wire: harness.WireChat,
			Models: []string{"deepseek-chat", "deepseek-reasoner"}, ContextWindow: 65536},
		{ID: "claude", Name: "Claude Code", Models: []string{"claude-sonnet-4-5"}},
	}
	a.providerID, a.model = "codex", defaultModels[0]
	a.agents = []Agent{
		{ID: "default", Name: "Default"},
		{ID: "ag-review", Name: "Reviewer", Emoji: "🔍", Model: "deepseek-chat", Provider: "p-ds",
			Mode: ptrInt(0), SystemPrompt: "You are a code reviewer. Report findings; change nothing."},
		{ID: "ag-writer", Name: "Changelog", Emoji: "📝", Backend: "claude", Model: "claude-sonnet-4-5"},
	}
	a.activeAgent = "default"
	a.projects = []Project{{ID: "default", Path: a.workdir}}
	a.activeProject = "default"
	a.seed()
	return a
}
func ptrInt(n int) *int { return &n }

func writeShot(t *testing.T, tt *ui.Tester, dir, name string) {
	t.Helper()
	img := tt.Image()
	if img == nil {
		t.Fatalf("%s: no frame rendered", name)
	}
	f, err := os.Create(filepath.Join(dir, name+".png"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

func TestScreenshots(t *testing.T) {
	dir := shotDir(t)

	// 1. Home: the launcher and the composer.
	a := shotApp(t)
	tt := ui.NewTester(a.view, 1440, 900)
	a.current = ""
	tt.Frame()
	writeShot(t, tt, dir, "01-home")

	// 2. Thread: a full conversation with cards.
	th := a.threads[0]
	a.current = th.ID
	tt.Frame()
	writeShot(t, tt, dir, "02-thread")

	// 3. Thread with the workspace panel open.
	a.wsOpen = true
	a.refreshGit()
	tt.Frame()
	writeShot(t, tt, dir, "03-thread-workspace")
	a.wsOpen = false

	// 4. Settings: the provider form, with its models fetched.
	a.settingsOpen = true
	a.settingsTab = "providers"
	a.settingsSel = "p-ds"
	a.fetchedModels["p-ds"] = []string{"deepseek-chat", "deepseek-reasoner", "deepseek-v3.2"}
	tt.Frame()
	writeShot(t, tt, dir, "04-settings-provider")

	// 5. Settings: the agent form.
	a.settingsTab = "agents"
	a.settingsSel = "ag-review"
	tt.Frame()
	writeShot(t, tt, dir, "05-settings-agent")

	// 5b. Settings: the tools and skills tabs.
	a.settingsTab = "tools"
	tt.Frame()
	writeShot(t, tt, dir, "05b-settings-tools")
	a.settingsTab = "skills"
	tt.Frame()
	writeShot(t, tt, dir, "05c-settings-skills")
	a.settingsOpen = false

	// 6. The composer's agent popover.
	a.current = ""
	a.vm.AgentMenu = true
	tt.Frame()
	writeShot(t, tt, dir, "06-agent-picker")
	a.vm.AgentMenu = false

	// 7. Viewer: the thread trace (synthesized — the seeded task never
	// actually ran).
	a.current = th.ID
	now := time.Now()
	a.appendTrace(th, traceEvent{At: now.Add(-90 * time.Second), Kind: "tool_start",
		Tool: "bash", Summary: "$ go test ./internal/outbox/"})
	a.appendTrace(th, traceEvent{At: now.Add(-88 * time.Second), Kind: "tool_end",
		Tool: "bash", Ms: 1890, Summary: "FAIL: TestOutboxRetry", Failed: true})
	a.appendTrace(th, traceEvent{At: now.Add(-60 * time.Second), Kind: "turn",
		Ms: 31200, Tokens: 4120, Summary: "3 tool calls · 4120 tokens"})
	a.openTrace(th)
	tt.Frame()
	writeShot(t, tt, dir, "07-trace-viewer")
	a.viewerOpen = false

	// 8. Narrow window: how the composer row survives 980 px.
	tt.SetSize(980, 720)
	a.current = ""
	tt.Frame()
	writeShot(t, tt, dir, "08-home-narrow")

	_ = json.Marshal // keep encoding/json while the harness evolves
	_ = uipkg.ModeNames
	_ = config.Version
}
