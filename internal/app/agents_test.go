package app

// The agents block (spec/agents.md): the v1→v2 migration, the per-turn
// assembly from an agent over the app defaults, the thread binding, and
// the settings dialog's mirror.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	uipkg "mygo-agent/internal/ui"

	"mygo-agent/internal/config"
	"mygo-agent/internal/harness"
	"mygo-agent/internal/harness/builtin"
)

func TestV1ConfigMigratesToDefaultAgent(t *testing.T) {
	a := newTestApp(t)
	dir := t.TempDir()
	a.configPath = filepath.Join(dir, "config.json")
	v1 := `{
	  "version": 1,
	  "projects": [{"id": "default", "path": "."}],
	  "active_project": "default",
	  "providers": [{"id": "codex", "name": "Codex CLI"}],
	  "provider": "codex",
	  "model": "gpt-5.2",
	  "effort": 1,
	  "backend": "builtin"
	}`
	if err := os.WriteFile(a.configPath, []byte(v1), 0o600); err != nil {
		t.Fatal(err)
	}
	a.loadConfig()
	a.ensureDefaults()
	if len(a.agents) != 1 || a.agents[0].ID != "default" || a.agents[0].Name != "Default" {
		t.Fatalf("v1 config migrated to %+v", a.agents)
	}
	if a.defaultAgent != "default" {
		t.Fatalf("default agent = %q", a.defaultAgent)
	}
	// The migrated state saves as v2 and loads back whole — an old config
	// upgrades in place, never down.
	a.saveConfig()
	cfg, err := config.Load(a.configPath)
	if err != nil {
		t.Fatalf("the migrated config does not reload: %v", err)
	}
	if cfg.Version != config.Version || len(cfg.Agents) != 1 || cfg.DefaultAgent != "default" {
		t.Fatalf("reloaded config: version %d, agents %+v, default %q",
			cfg.Version, cfg.Agents, cfg.DefaultAgent)
	}
}

// TestPlanTurnAssemblesTheAgent proves the turn is the agent's profile
// over the app defaults: every override lands, everything empty follows
// the app (spec/agents.md).
func TestPlanTurnAssemblesTheAgent(t *testing.T) {
	a := newTestApp(t)
	a.mode = int(harness.ModeFull)
	a.model = "app-model"
	a.maxTurns = 25
	a.providers = append(a.providers, Provider{ID: "p2", Name: "two", BaseURL: "https://two.test",
		APIKey: "k2", Wire: harness.WireChat, ContextWindow: 1000, Models: []string{"agent-model"}})
	low, readOnly := 0, int(harness.ModeReadOnly)
	a.agents = append(a.agents, Agent{
		ID: "ag-1", Name: "Reviewer", Backend: "claude",
		Provider: "p2", Model: "agent-model",
		Effort: &low, Mode: &readOnly, MaxTurns: 5,
		SystemPrompt: "Review only.",
		Tools: config.AgentTools{
			Disabled: []string{"bash"},
			Rules:    map[string]string{"grep": "allow"},
		},
		MCPServers: []string{"filesystem"},
		Skills:     config.AgentSkills{Mode: "custom", Allow: []string{"review"}, Deny: []string{"writer"}},
	})
	th := &Thread{ID: "t1", ProjectID: "default", AgentID: "ag-1", ClaudeID: "sess-c"}
	plan := a.planTurn(th, "hi")
	turn := plan.turn

	if plan.backend != "claude" {
		t.Fatalf("backend = %q, want the agent's", plan.backend)
	}
	if turn.Model != "agent-model" || turn.Effort != 0 || turn.MaxTurns != 5 {
		t.Fatalf("model/effort/maxTurns = %q/%d/%d", turn.Model, turn.Effort, turn.MaxTurns)
	}
	if turn.Mode != harness.ModeReadOnly {
		t.Fatalf("mode = %d, want the agent's read-only", turn.Mode)
	}
	if turn.SessionID != "sess-c" {
		t.Fatalf("session id = %q, want the claude one for the agent's backend", turn.SessionID)
	}
	if turn.Endpoint == nil || turn.Endpoint.BaseURL != "https://two.test" ||
		turn.Endpoint.APIKey != "k2" || turn.Endpoint.ContextWindow != 1000 {
		t.Fatalf("endpoint = %+v, want the agent's provider", turn.Endpoint)
	}
	if turn.ToolEnabled["bash"] {
		t.Fatal("the disabled tool did not reach the turn")
	}
	if _, ok := turn.ToolEnabled["grep"]; ok {
		t.Fatal("an unlisted tool gained an entry")
	}
	if turn.Rules["grep"] != harness.PermAllow {
		t.Fatalf("agent rules did not reach the turn: %v", turn.Rules)
	}
	if len(turn.Skills.Allow) != 1 || turn.Skills.Allow[0] != "review" ||
		len(turn.Skills.Deny) != 1 || turn.Skills.Deny[0] != "writer" {
		t.Fatalf("skills = %+v", turn.Skills)
	}
	if turn.SystemPrompt != "Review only." {
		t.Fatalf("system prompt = %q", turn.SystemPrompt)
	}
}

// TestPlanTurnMCPSubset pins the server list: an agent naming servers
// mounts exactly those; an agent naming none mounts everything.
func TestPlanTurnMCPSubset(t *testing.T) {
	a := newTestApp(t)
	a.mcpServers = []builtin.MCPServer{
		{Name: "filesystem", Command: "fs"}, {Name: "github", Command: "gh"},
	}
	a.agents = append(a.agents, Agent{ID: "ag-fs", Name: "FS", MCPServers: []string{"filesystem"}})

	got := a.planTurn(&Thread{ID: "t1", ProjectID: "default", AgentID: "ag-fs"}, "hi").turn.MCPServers
	if len(got) != 1 || got[0].Name != "filesystem" {
		t.Fatalf("subset = %+v, want filesystem only", got)
	}
	got = a.planTurn(&Thread{ID: "t2", ProjectID: "default"}, "hi").turn.MCPServers
	if len(got) != 2 {
		t.Fatalf("empty list = %+v, want every server", got)
	}
}

// TestPlanTurnEmptyAgentInheritsApp pins the identity property: the zero
// profile is the pre-agents app.
func TestPlanTurnEmptyAgentInheritsApp(t *testing.T) {
	a := newTestApp(t)
	a.mode = int(harness.ModeFull)
	a.model = "app-model"
	a.effort = 2
	a.maxTurns = 9
	a.permRules = harness.Rules{"bash": harness.PermAsk}
	a.mcpServers = []builtin.MCPServer{{Name: "filesystem", Command: "fs"}}

	th := &Thread{ID: "t1", ProjectID: "default"}
	turn := a.planTurn(th, "hi").turn
	if th.AgentID != "default" {
		t.Fatalf("binding = %q, want the default stamped", th.AgentID)
	}
	if turn.Model != "app-model" || turn.Effort != 2 || turn.MaxTurns != 9 {
		t.Fatalf("model/effort/maxTurns = %q/%d/%d, want the app's", turn.Model, turn.Effort, turn.MaxTurns)
	}
	if turn.Mode != harness.ModeFull {
		t.Fatalf("mode = %d, want the app's", turn.Mode)
	}
	if turn.Rules["bash"] != harness.PermAsk {
		t.Fatalf("global rules lost: %v", turn.Rules)
	}
	if len(turn.MCPServers) != 1 || turn.ToolEnabled != nil || turn.SystemPrompt != "" {
		t.Fatalf("empty profile changed the turn: %+v", turn)
	}
}

// TestCreateThreadBindsActiveAgent pins the binding at task creation and
// the mode an agent with its own default seeds.
func TestCreateThreadBindsActiveAgent(t *testing.T) {
	a := newTestApp(t)
	readOnly := int(harness.ModeReadOnly)
	a.agents = append(a.agents, Agent{ID: "ag-ro", Name: "Reader", Mode: &readOnly})
	a.activeAgent = "ag-ro"

	th := a.createThread()
	if th.AgentID != "ag-ro" {
		t.Fatalf("new task bound to %q", th.AgentID)
	}
	if a.mode != int(harness.ModeReadOnly) {
		t.Fatalf("mode = %d, want the agent's default seeded", a.mode)
	}
}

// TestSetActiveAgentRebindsThread pins the composer's switch: the
// selection now, the current thread's binding from its next turn.
func TestSetActiveAgentRebindsThread(t *testing.T) {
	a := newTestApp(t)
	readOnly := int(harness.ModeReadOnly)
	a.agents = append(a.agents, Agent{ID: "ag-ro", Name: "Reader", Mode: &readOnly})
	a.mode = int(harness.ModeFull)

	th := a.createThread()
	a.activeAgent = ""
	a.mode = int(harness.ModeFull)

	h := homeActions{a: a}
	h.SetAgent("ag-ro")
	if a.activeAgent != "ag-ro" || th.AgentID != "ag-ro" {
		t.Fatalf("selection %q, thread %q", a.activeAgent, th.AgentID)
	}
	if a.mode != int(harness.ModeReadOnly) {
		t.Fatalf("mode = %d, want the agent's default", a.mode)
	}
	// An unknown id changes nothing.
	h.SetAgent("nope")
	if a.activeAgent != "ag-ro" {
		t.Fatalf("unknown agent moved the selection to %q", a.activeAgent)
	}
}

// TestRemoveAgentRepairsPointers pins the deletion repairs: the default,
// the composer's selection and the dialog's, and the last-agent guard.
func TestRemoveAgentRepairsPointers(t *testing.T) {
	a := newTestApp(t)
	a.agents = append(a.agents, Agent{ID: "ag-2", Name: "Two"})
	a.defaultAgent = "default"
	a.activeAgent = "ag-2"
	a.settingsSel = "ag-2"

	a.removeAgent("ag-2")
	if a.defaultAgent != "default" || a.activeAgent != "" || a.settingsSel != "default" {
		t.Fatalf("pointers after remove: default %q active %q sel %q",
			a.defaultAgent, a.activeAgent, a.settingsSel)
	}
	a.removeAgent("default")
	if len(a.agents) != 1 {
		t.Fatalf("the last agent was removed: %+v", a.agents)
	}
}

// TestAgentSettingsSync drives the dialog's mirror: the VM's bindings
// land on the profile and persist (spec/agents.md).
func TestAgentSettingsSync(t *testing.T) {
	a := newTestApp(t)
	dir := t.TempDir()
	a.configPath = filepath.Join(dir, "config.json")

	vm := a.settingsVM()
	ai := slices.IndexFunc(vm.Agents, func(ag uipkg.AgentEditVM) bool { return ag.ID == "default" })
	if ai < 0 {
		t.Fatalf("no default agent in the dialog: %+v", vm.Agents)
	}
	ag := &vm.Agents[ai]
	ag.Name = "Reviewer"
	ag.Emoji = "🔍"
	ag.Backend = "claude"
	ag.Model = "claude-sonnet-4-5"
	ag.Effort = 1 // VM offset: 1 → effort 0 (low)
	ag.Mode = 2   // → mode 1 (agent)
	ag.MaxTurns = "7"
	ag.SystemPrompt = "Be terse."
	ag.ToolsDisabled = []string{"bash"}
	ag.MCPServers = []string{"filesystem"}
	ag.SkillsDeny = []string{"writer"}
	a.syncSettings(vm)

	got := a.agentByID("default")
	if got == nil || got.Name != "Reviewer" || got.Emoji != "🔍" || got.Backend != "claude" ||
		got.Model != "claude-sonnet-4-5" || got.SystemPrompt != "Be terse." {
		t.Fatalf("synced profile: %+v", got)
	}
	if got.Effort == nil || *got.Effort != 0 || got.Mode == nil || *got.Mode != 1 {
		t.Fatalf("effort/mode = %v/%v", got.Effort, got.Mode)
	}
	if got.MaxTurns != 7 || !slices.Equal(got.Tools.Disabled, []string{"bash"}) ||
		!slices.Equal(got.MCPServers, []string{"filesystem"}) ||
		!slices.Equal(got.Skills.Deny, []string{"writer"}) {
		t.Fatalf("tools/mcp/skills: %+v", got)
	}
	// The inheritance sentinel survives the round trip: effort back to
	// "app default" clears the pointer.
	vm2 := a.settingsVM()
	ai = slices.IndexFunc(vm2.Agents, func(ag uipkg.AgentEditVM) bool { return ag.ID == "default" })
	vm2.Agents[ai].Effort = 0
	vm2.Agents[ai].MaxTurns = "not a number"
	a.syncSettings(vm2)
	if got := a.agentByID("default"); got.Effort != nil || got.MaxTurns != 7 {
		t.Fatalf("inheritance/parse: effort %v, maxTurns %d", got.Effort, got.MaxTurns)
	}
	// And it all persisted.
	cfg, err := config.Load(a.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Agents) != 1 || cfg.Agents[0].Name != "Reviewer" {
		t.Fatalf("persisted agents: %+v", cfg.Agents)
	}
}

// TestThreadFileCarriesAgentID pins the thread file's new meta field
// (spec/data.md): saved, loaded, and absent when unbound.
func TestThreadFileCarriesAgentID(t *testing.T) {
	a := newTestApp(t)
	a.threadsDir = threadsLayout{root: t.TempDir()}
	now := time.Now()
	th := &Thread{ID: "t1", ProjectID: "default", AgentID: "ag-1", Created: now, Updated: now}
	a.saveThread(th)

	path := filepath.Join(a.threadsDir.root, "default", "t1.json")
	got, err := decodeThreadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.AgentID != "ag-1" {
		t.Fatalf("agent_id = %q", got.AgentID)
	}

	// A thread bound on the next turn picks the id up on save.
	th2 := &Thread{ID: "t2", ProjectID: "default", Created: now, Updated: now}
	a.planTurn(th2, "hi")
	a.saveThread(th2)
	got2, err := decodeThreadFile(filepath.Join(a.threadsDir.root, "default", "t2.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got2.AgentID != "default" {
		t.Fatalf("the stamped binding did not persist: %q", got2.AgentID)
	}
}

// TestTwoThreadsRunConcurrently is the P2 acceptance (spec/agents.md):
// two tasks — bound to different agents — run at the same time, and
// stopping one leaves the other to finish untouched.
func TestTwoThreadsRunConcurrently(t *testing.T) {
	a := newTestApp(t)
	a.backend = "builtin"
	// A slow provider: the 400 ms delay is what makes "at the same time"
	// observable instead of lucky.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(400 * time.Millisecond)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: response.output_text.delta\n"+
			`data: {"type":"response.output_text.delta","delta":"done"}`+"\n\n"+
			"event: response.completed\n"+
			`data: {"type":"response.completed","response":{"output":[]}}`+"\n\n")
	}))
	defer srv.Close()
	a.providers = []Provider{{ID: "p1", Name: "Test", BaseURL: srv.URL, APIKey: "k",
		Models: []string{"test-model"}, Wire: harness.WireResponses}}
	a.providerID, a.model = "p1", "test-model"

	readOnly := int(harness.ModeReadOnly)
	a.agents = append(a.agents,
		Agent{ID: "ag-a", Name: "A"},
		Agent{ID: "ag-b", Name: "B", Mode: &readOnly})

	now := time.Now()
	thA := &Thread{ID: "t-a", ProjectID: "default", AgentID: "ag-a", Created: now, Updated: now}
	thB := &Thread{ID: "t-b", ProjectID: "default", AgentID: "ag-b", Created: now, Updated: now}
	for _, th := range []*Thread{thA, thB} {
		th.Messages = []Message{{ID: "m0", Role: "assistant", Running: true, At: now}}
		a.threads = append(a.threads, th)
	}
	a.current = "t-b"

	go runBackend(a, thA, "one", 0)
	go runBackend(a, thB, "two", 0)

	// Both in flight at once — the old global running flag could not
	// even say this.
	both := false
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		a.update(func() { both = a.isRunning("t-a") && a.isRunning("t-b") })
		if both {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !both {
		t.Fatal("the two turns never ran at the same time")
	}

	// Stopping A scopes to A.
	a.stopThread("t-a")
	a.update(func() {
		if a.isRunning("t-a") {
			t.Fatal("the stopped thread is still registered")
		}
		if !a.isRunning("t-b") {
			t.Fatal("the stop leaked into the other thread")
		}
	})

	// B finishes on its own; A settles stopped.
	waitTurn(t, a, thB, 0)
	waitTurn(t, a, thA, 0)
	a.update(func() {
		if a.isRunning("t-a") || a.isRunning("t-b") {
			t.Fatal("settled turns left entries in the run registry")
		}
	})
	if thB.Messages[0].Text != "done" {
		t.Fatalf("the untouched thread lost its reply: %q", thB.Messages[0].Text)
	}
}
