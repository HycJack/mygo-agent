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

	// 2b. Group relay thread: 成员头像（纯色+首字母）、归属标签、路由
	// note、按种类折叠的命令组、插话与总结——router 接力的完整形态。
	a.agents = append(a.agents,
		Agent{ID: "ag-m1", Name: "需求", Emoji: "📋", SystemPrompt: "需求分析：把想法变成可验收的清单"},
		Agent{ID: "ag-m2", Name: "架构", Emoji: "🏗️", SystemPrompt: "架构设计：模块划分与接口契约"},
		Agent{ID: "ag-m3", Name: "开发", Emoji: "🛠️", SystemPrompt: "编码实现：小步提交，遵循仓库风格"},
		Agent{ID: "ag-team", Name: "产品流水线", Emoji: "🎼", Panel: []string{"需求", "架构", "开发"},
			PanelRoute: "router", RouterProvider: "p-ds", RouterModel: "deepseek-chat"},
	)
	now := time.Now()
	min := func(d time.Duration) time.Time { return now.Add(-d) }
	gth := &Thread{ID: "t-group", ProjectID: "default", AgentID: "ag-team",
		Title: "群聊接力：重试计数器的修复", Created: min(30 * time.Minute), Updated: min(time.Minute)}
	gth.Messages = []Message{
		{ID: uid(), Role: "user", Text: "重试计数器偶发丢更新，查一下并修复。", At: min(28 * time.Minute)},
		{ID: uid(), Role: "assistant", AgentID: "ag-m1", At: min(26 * time.Minute),
			Blocks: []Block{
				{Type: blockNote, Text: "→ 需求: 复现路径不明确，先定验收标准"},
				{Type: blockText, Text: "验收标准明确为三条：并发下计数**不丢失**、重试上限 3 次、失败可观测。范围不含队列化重试。"},
			},
			Text: "验收标准明确为三条：并发下计数**不丢失**、重试上限 3 次、失败可观测。"},
		{ID: uid(), Role: "assistant", AgentID: "ag-m2", At: min(20 * time.Minute),
			Blocks: []Block{
				{Type: blockNote, Text: "→ 架构: 原子化计数器，读改写合并为 CAS"},
				{Type: blockCommand, Text: "$ grep -rn \"retryCount\" internal/", Output: "internal/retry/counter.go:14\ninternal/retry/policy.go:31", Exit: 0, Ms: 120},
				{Type: blockText, Text: "现状是裸 `count++`。方案：换成 `atomic.CompareAndSwap` 循环，重试上限进 policy，不引入锁。"},
			},
			Text: "现状是裸 count++。方案：换成 atomic CAS 循环，重试上限进 policy。"},
		{ID: uid(), Role: "user", Text: "@开发 补个并发测试再改", At: min(12 * time.Minute)},
		{ID: uid(), Role: "assistant", AgentID: "ag-m3", At: min(8 * time.Minute),
			Blocks: []Block{
				{Type: blockNote, Text: "→ 开发: 用户点名，先补测试再实现"},
				{Type: blockCommand, Text: "$ cat > internal/retry/counter_test.go <<'EOF'\nfunc TestConcurrentCounts(t *testing.T) { … }\nEOF", Output: "", Exit: 0, Ms: 45},
				{Type: blockCommand, Text: "$ go test ./internal/retry/ -race -count=100", Output: "ok  \tmygo-agent/internal/retry\t8.412s", Exit: 0, Ms: 8412},
				{Type: blockDiff, File: "internal/retry/counter.go", Add: 14, Del: 6, Open: true,
					Lines: []DiffLine{{Kind: '-', Text: "func (c *Counter) Next() int {"}, {Kind: '-', Text: "\tc.count++"}, {Kind: '-', Text: "\treturn c.count"}, {Kind: '+', Text: "func (c *Counter) Next() int {"}, {Kind: '+', Text: "\tfor {"}, {Kind: '+', Text: "\t\tv := c.count"}, {Kind: '+', Text: "\t\tif c.count.CompareAndSwap(v, v+1) {"}, {Kind: '+', Text: "\t\t\treturn int(v) + 1"}, {Kind: '+', Text: "\t\t}"}, {Kind: '+', Text: "\t}"}}},
				{Type: blockText, Text: "测试先行：并发 100 轮全绿，实现换成 CAS 循环，`+14/−6`。"},
			},
			Text: "测试先行：并发 100 轮全绿，实现换成 CAS 循环。"},
		{ID: uid(), Role: "assistant", AgentID: "ag-team", At: min(3 * time.Minute),
			Blocks: []Block{
				{Type: blockNote, Text: "→ 产品流水线: final wrap-up"},
				{Type: blockText, Text: "**结论**：计数器丢更新已修复并合入。\n- 方案：`atomic.CompareAndSwap` 循环，无锁\n- 质量：race 下 100 轮全绿，验收三条全满足\n- 后续：失败可观测由运营侧埋点跟进"},
			},
			Text: "**结论**：计数器丢更新已修复并合入。"},
	}
	a.threads = append(a.threads, gth)
	a.current = gth.ID
	tt.Frame()
	writeShot(t, tt, dir, "02b-thread-group")
	a.current = th.ID

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
	if !tt.HasText("codexhost-delegation") {
		t.Errorf("SKILLS-RENDER: skill card missing; empty-state=%v dialogSkills=%d texts=%v",
			tt.HasText("No skills discovered"), len(a.dialogSkills), tt.Texts())
	}
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
	traceAt := time.Now()
	a.appendTrace(th, traceEvent{At: traceAt.Add(-90 * time.Second), Kind: "tool_start",
		Tool: "bash", Summary: "$ go test ./internal/outbox/"})
	a.appendTrace(th, traceEvent{At: traceAt.Add(-88 * time.Second), Kind: "tool_end",
		Tool: "bash", Ms: 1890, Summary: "FAIL: TestOutboxRetry", Failed: true})
	a.appendTrace(th, traceEvent{At: traceAt.Add(-60 * time.Second), Kind: "turn",
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
