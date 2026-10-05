package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"

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

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
)

// Block is a card inside an assistant message: a command it ran, a file
// patch it applied, a stretch of reasoning, an error, or a pending
// approval.
type Block struct {
	Type    string // "command" | "diff" | "reasoning" | "error" | "approval"
	Text    string // the command line, error message or reasoning text
	Output  string // command output
	Exit    int    // command exit code, -1 when unknown
	Running bool   // the command is still running
	Edit    bool   // a file edit, rendered as a diff card when done
	Ms      int64  // how long the tool took
	Open    bool   // the card's body is expanded
	File    string // the patched file
	Add     int
	Del     int
	Lines   []DiffLine

	// ApprovalID identifies the pending approval this card asks about;
	// the decision buttons resolve it through app.approvals.
	ApprovalID string
	// ToolID is the harness tool-call id that opened a command card; the
	// projector matches tool_end events to it.
	ToolID string
}

// Message is one turn of a thread: the user's prompt or the assistant's
// reply with its tool cards.
type Message struct {
	ID      string
	Role    string // "user" | "assistant"
	Text    string
	Blocks  []Block
	Running bool
	At      time.Time

	// LogAt marks where this assistant turn begins in the thread's
	// ChatLog, so regenerate can rewind it.
	LogAt int
}

// Thread is one Codex task, belonging to a project.
type Thread struct {
	ID        string
	ProjectID string // the project the task ran in
	Title     string
	Messages  []Message
	Created   time.Time
	Updated   time.Time
	CodexID   string // codex exec session id, for resuming
	ClaudeID  string // Claude Code session id, for resuming

	// ChatLog is the built-in backend's full transcript, including the
	// tool round-trips, so a task survives app restarts with context
	// intact.
	ChatLog []harness.ChatMessage `json:"chat_log,omitempty"`
}

// fsNode is one entry of the workspace file tree.
type fsNode struct {
	Name string
	Path string
	Dir  bool
}

// gitChange is one path of `git status --porcelain`: "M", "A", "D",
// "R", "??" and so on.
type gitChange struct {
	Code string
	Path string
}

// viewerState is what the file viewer shows while it is open.
type viewerState struct {
	Path    string // file path, or "git:<path>" for a working-tree diff
	Kind    string // "text" | "image" | "markdown" | "diff"
	Title   string
	Sub     string // "+12 −3" or a byte size
	Text    string // the raw text, for Markdown rendering
	Lines   []DiffLine
	Raw     string // the unrendered text, for the copy button
	Bitmap  *ui.Bitmap
	Err     string
	Note    string // e.g. "showing the first 20,000 lines"
	Loading bool
}

// The persisted types live in internal/config; alias them so the rest
// of the app reads naturally.
type (
	Project   = config.Project
	Provider  = config.Provider
	MCPServer = config.MCPServer
)

// app is the whole application state; the view is a function of it.
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
	backend    string // "demo" | "codex"

	providers []Provider

	workdir    string // the active project's path
	codexPath  string
	claudePath string
	savePath   string
	configPath string

	claudePid int // the running claude CLI process, for the stop test
	codexPid  int // the running codex CLI process

	term       *terminal.Terminal
	termOpen   bool
	termHeight float32

	running bool
	cancel  context.CancelFunc

	renaming    bool
	renameID    string
	renameDraft string

	threadMenu   bool
	backendMenu  bool
	modelMenu    bool
	projectMenu  bool
	hoverRow     string // the task row the pointer is on, for its delete button
	hoverMsg     string // the message the pointer is on, for its action row
	pickingDir   bool   // a native directory dialog is out
	settingsOpen bool   // the manage-providers modal is open
	settingsSel  string // the provider being edited there

	navOpen bool // the tasks sidebar is shown
	wsOpen  bool // the workspace panel (file tree + git changes) is shown

	viewerOpen bool

	// The workspace file tree.
	dirs     map[string]bool
	dirCache map[string][]fsNode

	// The git working tree, as `git status --porcelain` reports it.
	gitRoot  string // the repository root the workdir belongs to
	gitFiles []gitChange
	gitErr   string

	mcpServers []harness.MCPServer
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

	// uiCtx is the frame's context, stashed at the top of view() so
	// deferred actions (undo toasts) can reach it.
	uiCtx *ui.Context
}

// defaultModels are the models of the built-in Codex CLI provider.
var defaultModels = []string{"gpt-5.2-codex", "gpt-5.2", "gpt-5.1-codex-max", "gpt-5.1-codex-mini"}

func newApp() *app {
	a := &app{
		theme:         codexTheme(),
		pal:           codexPalette(),
		lists:         map[string]*ui.ListState{},
		approvals:     map[string]chan harness.ApprovalDecision{},
		sections:      map[string]bool{},
		dirs:          map[string]bool{},
		dirCache:      map[string][]fsNode{},
		model:         defaultModels[0],
		maxTurns:      25,
		mode:          1,
		effort:        1,
		backend:       "demo",
		termHeight:    260,
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
	case "demo", "codex", "builtin", "claude":
		a.backend = cfg.Backend
	}
	a.mcpServers = toAgentServers(cfg.MCPServers)
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
	// Migrate the pre-providers config: its model and custom models
	// fold into the built-in Codex CLI provider.
	if len(a.providers) == 0 && (len(cfg.CustomModels) > 0 || cfg.Model != "") {
		ms := slices.Clone(defaultModels)
		for _, m := range append([]string{cfg.Model}, cfg.CustomModels...) {
			if m != "" && !slices.Contains(ms, m) {
				ms = append(ms, m)
			}
		}
		a.providers = []Provider{{ID: "codex", Name: "Codex CLI", Wire: harness.WireResponses, Models: ms}}
		a.providerID = "codex"
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
	})
	if err == nil {
		a.configErr = ""
	}
}

// toAgentServers / fromAgentServers convert between the config type
// and the agent package's server description.
// effectiveMCPServers merges the configured servers with the active
// project's .mcp.json — the convention Claude Code and pi use, so
// checking a project in brings its tools along.
func (a *app) effectiveMCPServers() []harness.MCPServer {
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
		if dup || srv.Command == "" {
			continue
		}
		out = append(out, harness.MCPServer{Name: name, Command: srv.Command, Args: srv.Args, Env: srv.Env})
	}
	return out
}

func toAgentServers(in []config.MCPServer) []harness.MCPServer {
	if in == nil {
		return nil
	}
	out := make([]harness.MCPServer, len(in))
	for i, s := range in {
		out[i] = harness.MCPServer{Name: s.Name, Command: s.Command, Args: s.Args, Env: s.Env}
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

func fromAgentServers(in []harness.MCPServer) []config.MCPServer {
	if in == nil {
		return nil
	}
	out := make([]config.MCPServer, len(in))
	for i, s := range in {
		out[i] = config.MCPServer{Name: s.Name, Command: s.Command, Args: s.Args, Env: s.Env}
	}
	return out
}

// ensureDefaults fills in what a fresh or half-migrated config lacks.
func (a *app) ensureDefaults() {
	if len(a.projects) == 0 {
		a.projects = []Project{{ID: "default", Path: a.workdir}}
	}
	a.activeProject = a.activeID()
	for _, want := range []Provider{
		{ID: "codex", Name: "Codex CLI", Wire: harness.WireResponses, Models: slices.Clone(defaultModels)},
		{ID: "claude", Name: "Claude Code", Models: []string{"claude-sonnet-4-5", "claude-opus-4-1", "claude-haiku-4-5"}},
	} {
		if a.providerByID(want.ID) == nil {
			a.providers = append(a.providers, want)
		}
	}
	if len(a.providers) == 0 {
		a.providers = []Provider{{ID: "codex", Name: "Codex CLI", Models: slices.Clone(defaultModels)}}
	}
	if a.providerByID(a.providerID) == nil {
		a.providerID = a.providers[0].ID
	}
	if !slices.Contains(a.provider().Models, a.model) {
		if ms := a.provider().Models; len(ms) > 0 {
			a.model = ms[0]
		}
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

// writeFileAtomic writes through a temp file and a rename, so a crash
// mid-write never leaves a truncated file behind.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func uid() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (a *app) byID(id string) *Thread {
	for _, t := range a.threads {
		if t.ID == id {
			return t
		}
	}
	return nil
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
var errThreadsUnsupported = errors.New("file was written by a newer version")

// threadsFile is the legacy single-file threads.json shape, read only
// by the upgrade contract.
type threadsFile struct {
	Version int       `json:"version"`
	Threads []*Thread `json:"threads"`
}

// quarantine renames an unloadable data file out of the way so a later
// save cannot destroy it (spec/data.md). Returns the new name.
func quarantine(path, why string) string {
	for attempt := 0; ; attempt++ {
		name := path + "." + why
		if attempt > 0 {
			name = fmt.Sprintf("%s.%s.%d", path, why, attempt+1)
		}
		if _, err := os.Stat(name); err == nil {
			continue
		}
		if err := os.Rename(path, name); err != nil {
			return ""
		}
		return name
	}
}

// threadsLayout owns the per-thread persistence tree
// (spec/data.md): threads/<projectID>/<threadID>.json, one small file
// per task, mode 0600, atomic writes.
type threadsLayout struct {
	root string // <configDir>/codex-go/threads
}

func (l threadsLayout) file(th *Thread) (string, bool) {
	if l.root == "" || !safeName(th.ProjectID) || !safeName(th.ID) {
		return "", false
	}
	return filepath.Join(l.root, th.ProjectID, th.ID+".json"), true
}

// safeName refuses path separators and dot names before a value becomes
// a path segment.
func safeName(s string) bool {
	if s == "" || s == "." || s == ".." {
		return false
	}
	return !strings.ContainsAny(s, "/\\\x00")
}

// threadFile is the persisted shape of one thread file (spec/data.md).
type threadFile struct {
	Version  int                   `json:"version"`
	Meta     threadMeta            `json:"meta"`
	Messages []Message             `json:"messages"`
	ChatLog  []harness.ChatMessage `json:"chat_log,omitempty"`
}

type threadMeta struct {
	ID        string    `json:"id"`
	ProjectID string    `json:"project_id"`
	Title     string    `json:"title"`
	Created   time.Time `json:"created"`
	Updated   time.Time `json:"updated"`
	CodexID   string    `json:"codex_id,omitempty"`
	ClaudeID  string    `json:"claude_id,omitempty"`
}

func metaOf(th *Thread) threadMeta {
	return threadMeta{ID: th.ID, ProjectID: th.ProjectID, Title: th.Title,
		Created: th.Created, Updated: th.Updated, CodexID: th.CodexID, ClaudeID: th.ClaudeID}
}

// saveThread writes one thread's file atomically. The thread's Messages
// and ChatLog stay in memory; only their own file is touched.
func (a *app) saveThread(th *Thread) {
	path, ok := a.threadsDir.file(th)
	if !ok {
		return
	}
	data, err := json.MarshalIndent(threadFile{
		Version: 1, Meta: metaOf(th), Messages: th.Messages, ChatLog: th.ChatLog,
	}, "", "  ")
	if err != nil {
		return
	}
	if err := writeFileAtomic(path, data, 0o600); err == nil {
		a.threadsErr = ""
	}
}

// removeThreadFile deletes one thread's file. The caller keeps the
// thread in memory for the undo toast.
func (a *app) removeThreadFile(th *Thread) {
	if path, ok := a.threadsDir.file(th); ok {
		os.Remove(path)
		os.Remove(filepath.Dir(path)) // the project dir, now empty
	}
}

// loadThreads walks the tree and decodes every thread file. One bad
// file is quarantined and skipped; the rest still load (spec/data.md).
func (a *app) loadThreads() {
	if a.threadsDir.root == "" {
		return
	}
	entries, err := os.ReadDir(a.threadsDir.root)
	if err != nil {
		return // no tree yet
	}
	for _, proj := range entries {
		if !proj.IsDir() || !safeName(proj.Name()) {
			continue
		}
		files, err := os.ReadDir(filepath.Join(a.threadsDir.root, proj.Name()))
		if err != nil {
			continue
		}
		for _, f := range files {
			name := f.Name()
			if !strings.HasSuffix(name, ".json") || nameendsQuarantined(name) {
				continue
			}
			path := filepath.Join(a.threadsDir.root, proj.Name(), name)
			th, err := decodeThreadFile(path)
			if err != nil {
				why := "invalid"
				if errors.Is(err, errThreadsUnsupported) {
					why = "unsupported"
				}
				if kept := quarantine(path, why); kept != "" {
					a.threadsErr = filepath.Base(kept) + " (" + err.Error() + ")"
				}
				continue
			}
			if th.ProjectID == "" {
				th.ProjectID = a.activeProject
			}
			a.threads = append(a.threads, th)
		}
	}
	// Newest first, the order the sidebar expects.
	slices.SortFunc(a.threads, func(x, y *Thread) int {
		return y.Updated.Compare(x.Updated)
	})
	if len(a.threads) > 0 {
		a.current = a.threads[0].ID
		if th := a.byID(a.current); th != nil && th.ProjectID != a.activeProject {
			a.current = ""
		}
	}
}

// nameendsQuarantined reports a previously quarantined file; the scan
// skips them so quarantine renames stay once-per-file.
func nameendsQuarantined(name string) bool {
	return strings.HasSuffix(name, ".invalid") || strings.HasSuffix(name, ".unsupported")
}

// decodeThreadFile strictly decodes one thread file.
func decodeThreadFile(path string) (*Thread, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var tf threadFile
	if err := dec.Decode(&tf); err != nil {
		return nil, err
	}
	if tf.Version > 1 {
		return nil, fmt.Errorf("%w (thread schema %d)", errThreadsUnsupported, tf.Version)
	}
	return &Thread{
		ID: tf.Meta.ID, ProjectID: tf.Meta.ProjectID, Title: tf.Meta.Title,
		Created: tf.Meta.Created, Updated: tf.Meta.Updated,
		CodexID: tf.Meta.CodexID, ClaudeID: tf.Meta.ClaudeID,
		Messages: tf.Messages, ChatLog: tf.ChatLog,
	}, nil
}

// decodeThreads parses the legacy single-file threads.json: the wrapped
// versioned shape, or the bare pre-version array (the upgrade contract),
// strictly.
func decodeThreads(data []byte) ([]*Thread, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var wrapped threadsFile
	if err := dec.Decode(&wrapped); err == nil {
		if wrapped.Version > 1 {
			return nil, fmt.Errorf("%w (tasks schema %d)", errThreadsUnsupported, wrapped.Version)
		}
		return wrapped.Threads, nil
	}
	dec = json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var threads []*Thread
	if err := dec.Decode(&threads); err != nil {
		return nil, err
	}
	return threads, nil
}

// importLegacyThreads loads the pre-directory single-file
// threads.json once (bare array or wrapped), imports its threads into
// the tree, and renames the original .migrated — preserved, never
// overwritten (spec/data.md).
func (a *app) importLegacyThreads() {
	if a.savePath == "" {
		return
	}
	data, err := os.ReadFile(a.savePath)
	if err != nil {
		return
	}
	threads, err := decodeThreads(data)
	if err != nil {
		if name := quarantine(a.savePath, "invalid"); name != "" {
			a.threadsErr = filepath.Base(name) + " (" + err.Error() + ")"
		}
		return
	}
	for _, th := range threads {
		if th.ProjectID == "" {
			th.ProjectID = a.activeProject
		}
		a.saveThread(th)
		if th.ID == "" { // unsaved placeholder: keep in memory only
			continue
		}
	}
	os.Rename(a.savePath, a.savePath+".migrated")
	// The in-memory list is built by loadThreads from the tree; nothing
	// to set here.
	_ = threads
}

func (a *app) createThread() *Thread {
	now := time.Now()
	th := &Thread{ID: uid(), ProjectID: a.activeProject, Created: now, Updated: now}
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
	if a.running && a.current == id {
		a.stop()
	}
	if a.current == id {
		if len(a.threads) > 0 {
			a.current = a.threads[0].ID
		} else {
			a.current = ""
		}
	}
	a.focusComposer = true
	a.removeThreadFile(removed)
	removedAt := at
	c.ToastAction("Task deleted", "Undo", func() {
		if a.byID(removed.ID) != nil {
			return
		}
		if removedAt > len(a.threads) {
			removedAt = len(a.threads)
		}
		rest := append([]*Thread{removed}, a.threads[removedAt:]...)
		a.threads = append(a.threads[:removedAt], rest...)
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

// changedFiles counts the file patches across a thread's messages.
func changedFiles(th *Thread) int {
	n := 0
	for _, m := range th.Messages {
		for _, b := range m.Blocks {
			if b.Type == "diff" {
				n++
			}
		}
	}
	return n
}
