package app

import (
	"context"
	"errors"

	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"maps"
	uipkg "mygo-agent/internal/ui"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"mygo-agent/internal/config"
	"mygo-agent/internal/harness"
	"mygo-agent/internal/harness/builtin"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
)

type app struct {
	win   *mygo.Window
	theme *ui.Theme
	pal   palette

	projects      []Project
	activeProject string // project id
	threads       []*Thread
	current       string // thread id, "" is the home screen
	lists         map[string]*ui.ListState
	sections      map[string]bool

	draft  string
	search string

	model      string
	maxTurns   int    // the built-in agent's tool-round budget
	providerID string // the provider the model comes from
	mode       int    // 0 read-only, 1 agent, 2 full access
	effort     int    // 0 low, 1 medium, 2 high — the model's reasoning effort
	backend    string // builtin | codex | claude | pi

	// The configured agents (spec/agents.md): new tasks bind to one, the
	// composer can switch a thread to another, and each turn is
	// assembled from the agent's profile over the app-level defaults.
	agents       []Agent
	defaultAgent string // the config's default; "" resolves to the first agent
	activeAgent  string // the composer's selection; "" follows defaultAgent

	providers []Provider

	workdir    string // the active project's path
	codexPath  string
	claudePath string
	piPath     string
	savePath   string
	configPath string

	claudePid int // the running claude CLI process, for the stop test
	codexPid  int // the running codex CLI process

	term       *terminal.Terminal
	termOpen   bool
	termHeight float32

	// groupQueue holds a group turn's remaining panel members, keyed by
	// thread id (spec/agents.md): finish pops the next one and dispatches
	// it, so a panel runs as one registered run on the main thread.
	// Cleared by stopThread — a stopped relay does not continue.
	groupQueue map[string][]string

	// runs maps thread id to its in-flight turn (spec/agents.md P2):
	// two tasks run at once, and every stop belongs to one thread. The
	// narrow mutex is the old cancelMu lesson kept: the map is written
	// from the dispatching frame and from finish's update, and a second
	// lock avoids re-entering a.mu.
	runsMu sync.Mutex
	runs   map[string]*runState

	renaming    bool
	renameID    string
	renameDraft string

	threadMenu   bool
	modelMenu    bool
	agentMenu    bool // the agent picker popover (composer + rail share it)
	projectMenu  bool
	hoverRow     string // the task row the pointer is on, for its delete button
	pickingDir   bool   // a native directory dialog is out
	settingsOpen bool   // the settings modal is open
	settingsSel  string // the provider or agent being edited there
	settingsTab  string // the dialog's active tab (spec/agents.md)
	// The dialog's per-open data: skills discovered for the active
	// project when it opened, and each provider's own /models listing
	// for this session (never persisted).
	groupDrafting    bool // the new-group dialog is open (spec/agents.md)
	groupDraftName   string
	groupDraftOn     map[string]bool // member id -> checked
	dialogSkills     []builtin.Skill
	settingsWereOpen bool // the settings dialog was open last frame
	fetchedModels    map[string][]string
	fetchErrs        map[string]string

	navOpen bool // the tasks sidebar is shown
	// Panel widths, DIP, dragged on the dividers between the panels.
	navWidth float32
	wsWidth  float32
	wsOpen   bool // the workspace panel (file tree + git changes) is shown

	viewerOpen bool

	// The workspace file tree.
	dirs     map[string]bool
	dirCache map[string][]fsNode

	// The git working tree, as `git status --porcelain` reports it.
	gitRoot  string // the repository root the workdir belongs to
	gitFiles []gitChange
	gitErr   string

	mcpServers []builtin.MCPServer
	permRules  harness.Rules // tool-name selector overrides from config.json
	// approvalTimeout overrides the loop's approval deadline; 0 keeps the
	// loop's 10-minute default.
	approvalTimeout time.Duration

	// Pending approval decisions, keyed by tool call id: the waiting
	// OnApproval call blocks on the channel; the card's buttons send.
	approvals map[string]chan harness.ApprovalDecision

	// mu serializes update() in headless runs (tests): in production the
	// window serializes everything on the main thread; without it the
	// tests' own update calls would race the run's.
	mu sync.Mutex

	mcpDraftName    string
	mcpDraftCommand string

	// Incremental markdown cache backing the shared renderer, keyed by
	// message id (the renderer itself lives in internal/ui).
	mdCache *uipkg.MdCache

	// The file viewer that takes the main area while open.
	viewer     viewerState
	viewerWrap bool

	focusComposer bool
	version       string

	// Load-failure notices for the data files (spec/data.md); shown in
	// the settings dialog until the next successful save clears them.
	threadsErr string
	configErr  string

	// threadsLayout is the per-thread persistence tree; root "" disables
	// persistence (tests).
	threadsDir threadsLayout

	// vm / sidebarVM are the persistent shared-view models published to
	// internal/ui (spec/architecture.md): host-owned fields refresh in
	// place each frame, view-owned fields (draft, search, menus) survive
	// across frames so keystrokes are not dropped.
	vm        *uipkg.ViewModel
	sidebarVM *uipkg.SidebarVM

	// transcriptCache holds each thread's last transcript snapshot, keyed
	// by thread id and validated by Thread.viewStamp, so an unchanged
	// frame does not re-copy every message and block.
	transcriptCache map[string]transcriptEntry

	// uiCtx is the frame's context, stashed at the top of view() so
	// deferred actions (undo toasts) can reach it.
	uiCtx *ui.Context
}

// defaultModels are the models of the built-in Codex CLI provider.
var defaultModels = []string{"gpt-5.2-codex", "gpt-5.2", "gpt-5.1-codex-max", "gpt-5.1-codex-mini"}

// claudeModels are the Claude Code backend's well-known models, shown
// by the composer's picker when that backend is the one running.
var claudeModels = []string{"claude-sonnet-4-5", "claude-opus-4-1", "claude-haiku-4-5"}

func newApp() *app {
	a := &app{
		theme:         codexTheme(),
		pal:           codexPalette(),
		lists:         map[string]*ui.ListState{},
		approvals:     map[string]chan harness.ApprovalDecision{},
		runs:          map[string]*runState{},
		fetchedModels: map[string][]string{},
		fetchErrs:     map[string]string{},
		groupQueue:    map[string][]string{},
		sections:      map[string]bool{},
		dirs:          map[string]bool{},
		dirCache:      map[string][]fsNode{},
		model:         defaultModels[0],
		maxTurns:      25,
		mode:          1,
		effort:        1,
		backend:       "builtin",
		termHeight:    260,
		navWidth:      264,
		wsWidth:       240,
		navOpen:       true,
		wsOpen:        false,
		focusComposer: true,
	}
	a.workdir, _ = os.Getwd()
	if a.workdir == "" {
		a.workdir, _ = os.UserHomeDir()
	}
	if p, err := exec.LookPath("codex"); err == nil {
		a.codexPath = p
	}
	if p, err := exec.LookPath("claude"); err == nil {
		a.claudePath = p
	}
	if p, err := exec.LookPath("pi"); err == nil {
		a.piPath = p
	}
	base, err := os.UserConfigDir()
	if err != nil {
		base, _ = os.UserHomeDir()
	}
	dir := filepath.Join(base, "codex-go")
	a.savePath = filepath.Join(dir, "threads.json")
	a.configPath = filepath.Join(dir, "config.json")
	a.threadsDir = threadsLayout{root: filepath.Join(dir, "threads")}
	a.loadConfig()
	a.ensureDefaults()
	a.workdir = a.project().Path
	return a
}

// loadConfig reads config.json into the app's fields, migrating older
// shapes (pre-providers custom models, wire-less codex provider). A
// file that fails to load is quarantined (spec/data.md): the reason
// shows in the settings dialog until the next save clears it.
func (a *app) loadConfig() {
	cfg, err := config.Load(a.configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		why := "invalid"
		if errors.Is(err, config.ErrUnsupportedVersion) {
			why = "unsupported"
		}
		if name := quarantine(a.configPath, why); name != "" {
			a.configErr = "settings file was moved to " + name + " (" + err.Error() + "); defaults apply — the original bytes are preserved"
		} else {
			a.configErr = "settings file could not be read (" + err.Error() + "); defaults apply"
		}
		return
	}
	a.configErr = ""
	a.projects = cfg.Projects
	a.activeProject = cfg.ActiveProject
	a.providers = cfg.Providers
	a.providerID = cfg.Provider
	if cfg.Model != "" {
		a.model = cfg.Model
	}
	if cfg.Effort >= 0 && cfg.Effort <= 2 {
		a.effort = cfg.Effort
	}
	switch cfg.Backend {
	case "codex", "builtin", "claude", "pi":
		a.backend = cfg.Backend
	}
	a.mcpServers = toAgentServers(cfg.MCPServers)
	a.agents = cfg.Agents
	a.defaultAgent = cfg.DefaultAgent
	if len(cfg.Permissions.Rules) > 0 {
		rules := make(harness.Rules, len(cfg.Permissions.Rules))
		for sel, perm := range cfg.Permissions.Rules {
			rules[sel] = harness.PermissionFromConfig(perm)
		}
		a.permRules = rules
	}
	if cfg.MaxTurns > 0 {
		a.maxTurns = cfg.MaxTurns
	}
	// Migrate the pre-providers config: its custom models join the
	// codex CLI's well-known table, reachable from the picker's codex
	// group — there is no codex provider to host them anymore.
	if len(cfg.CustomModels) > 0 {
		for _, m := range cfg.CustomModels {
			if m != "" && !slices.Contains(defaultModels, m) {
				defaultModels = append(defaultModels, m)
			}
		}
	}
}

// saveConfig snapshots the app's settings into a Config and writes it.
func (a *app) saveConfig() {
	if a.configPath == "" {
		return
	}
	err := config.Save(a.configPath, config.Config{
		Projects:      a.projects,
		ActiveProject: a.activeProject,
		Providers:     a.providers,
		Provider:      a.providerID,
		Model:         a.model,
		Effort:        a.effort,
		Backend:       a.backend,
		MCPServers:    fromAgentServers(a.mcpServers),
		Permissions:   config.Permissions{Rules: permRulesToConfig(a.permRules)},
		MaxTurns:      a.maxTurns,
		Agents:        a.agents,
		DefaultAgent:  a.defaultAgent,
	})
	if err == nil {
		a.configErr = ""
		return
	}
	a.configErr = "settings could not be saved: " + err.Error()
}

// toAgentServers / fromAgentServers convert between the config type
// and the agent package's server description.
// effectiveMCPServers merges the configured servers with the active
// project's .mcp.json — the convention Claude Code and pi use, so
// checking a project in brings its tools along.
func (a *app) effectiveMCPServers() []builtin.MCPServer {
	out := slices.Clone(a.mcpServers)
	data, err := os.ReadFile(filepath.Join(a.workdir, ".mcp.json"))
	if err != nil {
		return out
	}
	var f struct {
		MCPServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
			Env     []string `json:"env"`
			URL     string   `json:"url"`
		} `json:"mcpServers"`
	}
	if json.Unmarshal(data, &f) != nil {
		return out
	}
	names := slices.Sorted(maps.Keys(f.MCPServers))
	for _, name := range names {
		srv := f.MCPServers[name]
		dup := false
		for _, have := range out {
			if have.Name == name {
				dup = true
				break
			}
		}
		if dup || srv.Command == "" && srv.URL == "" {
			continue
		}
		out = append(out, builtin.MCPServer{Name: name, Command: srv.Command, Args: srv.Args, Env: srv.Env, URL: srv.URL})
	}
	return out
}

func toAgentServers(in []config.MCPServer) []builtin.MCPServer {
	if in == nil {
		return nil
	}
	out := make([]builtin.MCPServer, len(in))
	for i, s := range in {
		out[i] = builtin.MCPServer{Name: s.Name, Command: s.Command, Args: s.Args, Env: s.Env, URL: s.URL}
	}
	return out
}

// permRulesToConfig copies the agent rules into plain strings for the
// config file.
func permRulesToConfig(in harness.Rules) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for sel, perm := range in {
		out[sel] = string(perm)
	}
	return out
}

func fromAgentServers(in []builtin.MCPServer) []config.MCPServer {
	if in == nil {
		return nil
	}
	out := make([]config.MCPServer, len(in))
	for i, s := range in {
		out[i] = config.MCPServer{Name: s.Name, Command: s.Command, Args: s.Args, Env: s.Env, URL: s.URL}
	}
	return out
}

// ensureDefaults fills in what a fresh or half-migrated config lacks.
func (a *app) ensureDefaults() {
	if len(a.projects) == 0 {
		a.projects = []Project{{ID: "default", Path: a.workdir}}
	}
	a.activeProject = a.activeID()
	// The codex and claude pseudo-providers of the pre-agents configs
	// are gone: they were never vendors, just the CLI backends wearing a
	// provider costume. Their ids are filtered out on load, and the CLI
	// backends run their own sign-in without a provider.
	a.providers = slices.DeleteFunc(a.providers, func(p Provider) bool {
		return p.ID == "codex" || p.ID == "claude"
	})
	if a.providerByID(a.providerID) == nil && len(a.providers) > 0 {
		a.providerID = a.providers[0].ID
	}
	// A model that no provider serves is a CLI backend's model name —
	// keep it instead of snapping to the first provider's first model.
	if p := a.providerByID(a.providerID); p != nil && len(p.Models) > 0 && !slices.Contains(p.Models, a.model) {
		a.model = p.Models[0]
	}
	// The v1→v2 migration (spec/data.md): a config with no agents folds
	// into a single Default agent. An empty profile inherits the app's
	// selection, so the fold is the identity — an old config behaves
	// exactly as it did, and now has an agent to edit.
	if len(a.agents) == 0 {
		a.agents = []Agent{{ID: "default", Name: "Default"}}
		a.defaultAgent = "default"
	}
	if a.agentByID(a.defaultAgent) == nil {
		a.defaultAgent = a.agents[0].ID
	}
	if a.activeAgentID() == "" {
		a.activeAgent = a.defaultAgent
	}
}

// activeID returns the project id to use: the configured one while it
// still exists, else the first project's.
func (a *app) activeID() string {
	if a.activeProject != "" && a.projectByID(a.activeProject) != nil {
		return a.activeProject
	}
	if len(a.projects) > 0 {
		return a.projects[0].ID
	}
	return ""
}

func (a *app) project() *Project {
	return a.projectByID(a.activeProject)
}

func (a *app) projectByID(id string) *Project {
	for i := range a.projects {
		if a.projects[i].ID == id {
			return &a.projects[i]
		}
	}
	return nil
}

func (a *app) provider() *Provider {
	return a.providerByID(a.providerID)
}

func (a *app) providerByID(id string) *Provider {
	for i := range a.providers {
		if a.providers[i].ID == id {
			return &a.providers[i]
		}
	}
	return nil
}

// agentByName finds a configured agent by exact name — the panel
// membership convention (names, like mcp_servers).
func (a *app) agentByName(name string) *Agent {
	for i := range a.agents {
		if a.agents[i].Name == name {
			return &a.agents[i]
		}
	}
	return nil
}

// agentByID finds a configured agent by id.
func (a *app) agentByID(id string) *Agent {
	for i := range a.agents {
		if a.agents[i].ID == id {
			return &a.agents[i]
		}
	}
	return nil
}

// agentFor resolves the agent a thread is bound to: its own binding,
// else the default (spec/agents.md). The v1 migration guarantees at
// least one agent exists, so this is nil only on a half-built app.
func (a *app) agentFor(th *Thread) *Agent {
	if th != nil && th.AgentID != "" {
		if ag := a.agentByID(th.AgentID); ag != nil {
			return ag
		}
	}
	if ag := a.agentByID(a.defaultAgent); ag != nil {
		return ag
	}
	if len(a.agents) > 0 {
		return &a.agents[0]
	}
	return nil
}

// activeAgentID is the agent the composer shows and a new task binds:
// the user's selection, else the default.
func (a *app) activeAgentID() string {
	if ag := a.agentByID(a.activeAgent); ag != nil {
		return ag.ID
	}
	return a.defaultAgentID()
}

// defaultAgentID resolves the configured default to a real agent.
func (a *app) defaultAgentID() string {
	if ag := a.agentByID(a.defaultAgent); ag != nil {
		return ag.ID
	}
	if len(a.agents) > 0 {
		return a.agents[0].ID
	}
	return ""
}

// backendFor resolves the backend a thread's next turn runs on: the
// thread's agent's choice, else the app's switch.
func (a *app) backendFor(th *Thread) string {
	if ag := a.agentFor(th); ag != nil && ag.Backend != "" {
		return ag.Backend
	}
	return a.backend
}

// switchProject makes p the active project: the workdir, the file tree,
// the git panel and the terminal all follow it.
func (a *app) switchProject(id string) {
	p := a.projectByID(id)
	if p == nil || p.ID == a.activeProject {
		return
	}
	a.activeProject = id
	a.workdir = p.Path
	a.current = "" // back to the home screen of the new project
	a.dirCache = map[string][]fsNode{}
	a.refreshGit()
	a.recreateTerminal()
	a.saveConfig()
}

// addProjectPath adds the directory as a project and switches to it.
func (a *app) addProjectPath(path string) {
	path = filepath.Clean(path)
	if path == "" {
		return
	}
	for i := range a.projects {
		if a.projects[i].Path == path {
			a.switchProject(a.projects[i].ID)
			return
		}
	}
	p := Project{ID: uid(), Path: path}
	a.projects = append(a.projects, p)
	a.switchProject(p.ID)
}

// removeProject forgets a project; its tasks stay in threads.json.
func (a *app) removeProject(id string) {
	at := slices.IndexFunc(a.projects, func(p Project) bool { return p.ID == id })
	if at < 0 || len(a.projects) == 1 {
		return
	}
	a.projects = slices.Delete(a.projects, at, at+1)
	if a.activeProject == id {
		a.switchProject(a.projects[0].ID)
	}
	a.saveConfig()
}

// recreateTerminal restarts the docked shell in the new project.
func (a *app) recreateTerminal() {
	if a.term != nil {
		a.term.Close()
		a.term = nil
	}
	if a.termOpen {
		a.openTerminal()
	}
}

// openTerminal starts the docked shell in the active project, if it is
// not running yet.
func (a *app) openTerminal() bool {
	if a.term != nil {
		return true
	}
	term, err := terminal.New(terminal.Options{
		Dir: a.workdir,
		Font: terminal.Font{
			Family: "JetBrains Mono, SF Mono, Menlo, monospace",
			Size:   12,
		},
		Theme:      terminal.DarkTheme(),
		Scrollback: 10 << 20,
	})
	if err != nil {
		return false
	}
	a.term = term
	if a.win != nil {
		w := a.win
		w.OnClosed(func() { term.Close() })
	}
	return true
}
func uid() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// clampInt bounds n to [lo, hi]. The mode and effort selectors are plain
// ints that reach the adapters, which index arrays and flag lists with
// them; nothing in the repository recovers from a panic, so an
// out-of-range value from the UI or a hand-edited file has to be made
// harmless here rather than trusted.
func clampInt(n, lo, hi int) int { return min(max(n, lo), hi) }

// clampMode bounds the approval mode to the three the policy knows. A
// value outside the range is not clamped to the nearest end: clamping
// upward would turn a corrupt or hand-edited 9 into FULL ACCESS, and
// clamping downward would silently demote a legitimate "agent". Neither
// end is a safe guess, so an unknown mode fails to the most restrictive
// one (permissions.md: unknown everything fails closed).
func clampMode(m int) int {
	if m < int(harness.ModeReadOnly) || m > int(harness.ModeFull) {
		return int(harness.ModeReadOnly)
	}
	return m
}

func (a *app) byID(id string) *Thread {
	for _, t := range a.threads {
		if t.ID == id {
			return t
		}
	}
	return nil
}

// runState is one thread's in-flight turn: everything a stop needs.
type runState struct {
	cancel context.CancelFunc
}

// runStart registers the thread's in-flight turn. A thread runs one
// turn — the send guards see to that — so a stale entry here means a
// guard failed somewhere, and the loser is stopped rather than left
// spending tokens with nobody holding its card.
func (a *app) runStart(thID string, cancel context.CancelFunc) {
	a.runsMu.Lock()
	defer a.runsMu.Unlock()
	if a.runs == nil {
		a.runs = map[string]*runState{}
	}
	if stale := a.runs[thID]; stale != nil && stale.cancel != nil {
		stale.cancel()
	}
	a.runs[thID] = &runState{cancel: cancel}
}

// runEnd clears the thread's entry: the turn is over, whatever the
// outcome, and a late finish must not leave a ghost a stop could hit.
func (a *app) runEnd(thID string) {
	a.runsMu.Lock()
	delete(a.runs, thID)
	a.runsMu.Unlock()
}

// isRunning reports whether the thread's turn is in flight. The empty
// id (the home screen) is never running.
func (a *app) isRunning(thID string) bool {
	if thID == "" {
		return false
	}
	a.runsMu.Lock()
	defer a.runsMu.Unlock()
	return a.runs[thID] != nil
}

// currentRunning reports whether the thread on screen is running — the
// composer's Stop, the header's badge and the transcript's gating all
// key on it.
func (a *app) currentRunning() bool {
	th := a.currentThread()
	return th != nil && a.isRunning(th.ID)
}

// stopThread cancels the thread's run and clears the entry. A thread
// with no run is a no-op, which is what makes deleting or escaping on
// an idle thread free.
func (a *app) stopThread(thID string) {
	a.runsMu.Lock()
	st := a.runs[thID]
	delete(a.runs, thID)
	a.runsMu.Unlock()
	// A stopped relay does not continue: finish finds no queue and ends
	// the group turn with the participant that was running.
	delete(a.groupQueue, thID)
	if st != nil && st.cancel != nil {
		st.cancel()
	}
}

func (a *app) currentThread() *Thread {
	if a.current == "" {
		return nil
	}
	return a.byID(a.current)
}

func (a *app) listState(id string) *ui.ListState {
	st := a.lists[id]
	if st == nil {
		st = &ui.ListState{}
		a.lists[id] = st
	}
	return st
}

// update runs fn on the main thread and draws a new frame; in headless
// tests, where there is no window, it just runs fn — under the app lock,
// so tests driving the app from another goroutine are serialized with
// the run's own updates.
func (a *app) update(fn func()) {
	if a.win != nil {
		a.win.Update(fn)
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	fn()
}

// errThreadsUnsupported reports a data file from a newer schema.

func (a *app) createThread() *Thread {
	now := time.Now()
	th := &Thread{ID: uid(), ProjectID: a.activeProject, Created: now, Updated: now, AgentID: a.activeAgentID()}
	// A task started from an agent with its own default approval mode
	// starts in that mode — the composer's selector stays the per-run
	// override, the agent only seeds it (spec/agents.md).
	if ag := a.agentByID(th.AgentID); ag != nil && ag.Mode != nil {
		a.mode = clampMode(*ag.Mode)
	}
	// Newest first: the sidebar groups by Updated, newest at the top.
	a.threads = append([]*Thread{th}, a.threads...)
	a.current = th.ID
	return th
}

func (a *app) deleteThread(c *ui.Context, id string) {
	at := -1
	for i, t := range a.threads {
		if t.ID == id {
			at = i
			break
		}
	}
	if at < 0 {
		return
	}
	removed := a.threads[at]
	a.threads = append(a.threads[:at], a.threads[at+1:]...)
	// The stop belongs to the thread that is running, not the one on
	// screen: current moves freely while turns run, so keying on it
	// leaked the run — it kept spending tokens, its next event re-saved
	// the file this delete removed, and its approval card sat pending
	// until the deadline. stopThread is already scoped to the id, so
	// deleting an idle thread is a no-op here.
	a.stopThread(id)
	// Events already drained from the CLI can still land before the
	// cancel takes effect; the flag is what keeps them from resurrecting
	// the file removeThreadFile is about to delete.
	removed.dropped = true
	if a.current == id {
		if len(a.threads) > 0 {
			a.current = a.threads[0].ID
		} else {
			a.current = ""
		}
	}
	a.focusComposer = true
	// The cached snapshot holds every message and block of the thread;
	// keeping it after the task is gone leaks the whole transcript for
	// the life of the process.
	delete(a.transcriptCache, removed.ID)
	a.removeThreadFile(removed)
	removedAt := at
	// The undo toast is a UI affordance, not part of the deletion: the
	// task is already gone from state and disk whether or not a surface is
	// attached to offer a way back.
	if c == nil {
		return
	}
	c.ToastAction("Task deleted", "Undo", func() {
		if a.byID(removed.ID) != nil {
			return
		}
		if removedAt > len(a.threads) {
			removedAt = len(a.threads)
		}
		rest := append([]*Thread{removed}, a.threads[removedAt:]...)
		a.threads = append(a.threads[:removedAt], rest...)
		removed.dropped = false
		a.current = removed.ID
		a.saveThread(removed) // the undo rewrites the removed file
	})
}

// truncTitle collapses whitespace and caps a title at n runes.
func truncTitle(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// changedFiles counts the file patches across a thread's messages. The
// count is cached on the thread: the header reads it every frame, and a
// full scan of every block of every message is the wrong price for a
// label. Projector appends keep it current; a message rewrite marks it
// stale and the next read recomputes.
func changedFiles(th *Thread) int {
	if th == nil {
		return 0
	}
	if th.diffsKnown {
		return th.diffCount
	}
	n := 0
	for _, m := range th.Messages {
		for _, b := range m.Blocks {
			if b.Type == "diff" {
				n++
			}
		}
	}
	th.diffCount, th.diffsKnown = n, true
	return n
}

// noteDiffBlock records that a diff card was appended, keeping the cached
// changed-file count current without a rescan.
func (th *Thread) noteDiffBlock() {
	// On a thread whose count was never established, stay unknown: the
	// next read scans and gets the right answer, where incrementing from
	// an unestablished zero would bake the drift in.
	if th.diffsKnown {
		th.diffCount++
	}
}

// invalidateDiffCount marks the cache for recomputation after a rewrite.
func (th *Thread) invalidateDiffCount() { th.diffsKnown = false }
