package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"mygo-agent/internal/agent"
	"mygo-agent/internal/config"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
)

// Block is a card inside an assistant message: a command it ran, a file
// patch it applied, a stretch of reasoning, or an error.
type Block struct {
	Type    string // "command" | "diff" | "reasoning" | "error"
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

	mcpServers []agent.MCPServer

	mcpDraftName    string
	mcpDraftCommand string

	// The file viewer that takes the main area while open.
	viewer     viewerState
	viewerWrap bool

	focusComposer bool
	version       string
}

// defaultModels are the models of the built-in Codex CLI provider.
var defaultModels = []string{"gpt-5.2-codex", "gpt-5.2", "gpt-5.1-codex-max", "gpt-5.1-codex-mini"}

func newApp() *app {
	a := &app{
		theme:         codexTheme(),
		pal:           codexPalette(),
		lists:         map[string]*ui.ListState{},
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
		wsOpen:        true,
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
	a.loadConfig()
	a.ensureDefaults()
	a.workdir = a.project().Path
	return a
}

// loadConfig reads config.json into the app's fields, migrating older
// shapes (pre-providers custom models, wire-less codex provider).
func (a *app) loadConfig() {
	cfg, err := config.Load(a.configPath)
	if err != nil {
		return
	}
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
		a.providers = []Provider{{ID: "codex", Name: "Codex CLI", Wire: agent.WireResponses, Models: ms}}
		a.providerID = "codex"
	}
}

// saveConfig snapshots the app's settings into a Config and writes it.
func (a *app) saveConfig() {
	if a.configPath == "" {
		return
	}
	_ = config.Save(a.configPath, config.Config{
		Projects:      a.projects,
		ActiveProject: a.activeProject,
		Providers:     a.providers,
		Provider:      a.providerID,
		Model:         a.model,
		Effort:        a.effort,
		Backend:       a.backend,
		MCPServers:    fromAgentServers(a.mcpServers),
		MaxTurns:      a.maxTurns,
	})
}

// toAgentServers / fromAgentServers convert between the config type
// and the agent package's server description.
// effectiveMCPServers merges the configured servers with the active
// project's .mcp.json — the convention Claude Code and pi use, so
// checking a project in brings its tools along.
func (a *app) effectiveMCPServers() []agent.MCPServer {
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
	names := make([]string, 0, len(f.MCPServers))
	for name := range f.MCPServers {
		names = append(names, name)
	}
	slices.Sort(names)
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
		out = append(out, agent.MCPServer{Name: name, Command: srv.Command, Args: srv.Args, Env: srv.Env})
	}
	return out
}

func toAgentServers(in []config.MCPServer) []agent.MCPServer {
	if in == nil {
		return nil
	}
	out := make([]agent.MCPServer, len(in))
	for i, s := range in {
		out[i] = agent.MCPServer{Name: s.Name, Command: s.Command, Args: s.Args, Env: s.Env}
	}
	return out
}

func fromAgentServers(in []agent.MCPServer) []config.MCPServer {
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
		{ID: "codex", Name: "Codex CLI", Wire: agent.WireResponses, Models: slices.Clone(defaultModels)},
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
func writeFileAtomic(path string, data []byte, perm os.FileMode) {
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	tmp := path + ".tmp"
	if os.WriteFile(tmp, data, perm) != nil {
		return
	}
	_ = os.Rename(tmp, path)
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
// tests, where there is no window, it just runs fn.
func (a *app) update(fn func()) {
	if a.win != nil {
		a.win.Update(fn)
		return
	}
	fn()
}

func (a *app) save() {
	if a.savePath == "" {
		return
	}
	data, err := json.MarshalIndent(a.threads, "", "  ")
	if err != nil {
		return
	}
	writeFileAtomic(a.savePath, data, 0o644)
}

func (a *app) load() {
	data, err := os.ReadFile(a.savePath)
	if err != nil {
		return
	}
	var threads []*Thread
	if json.Unmarshal(data, &threads) == nil && len(threads) > 0 {
		a.threads = threads
		// Threads from before projects existed join the active project.
		for _, t := range a.threads {
			if t.ProjectID == "" {
				t.ProjectID = a.activeProject
			}
		}
		a.current = threads[0].ID
		if th := a.byID(a.current); th != nil && th.ProjectID != a.activeProject {
			a.current = ""
		}
	}
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
	a.save()
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
		a.save()
	})
}

// visibleThreads returns the active project's threads matching the
// search, grouped by how recently they were updated: each thread goes in
// the first group that fits, so the groups partition the list.
func (a *app) visibleThreads() []threadGroup {
	q := strings.ToLower(strings.TrimSpace(a.search))
	names := []struct {
		key, title string
		is         func(time.Time) bool
	}{
		{"today", "Today", func(t time.Time) bool { return sameDay(t, time.Now()) }},
		{"yesterday", "Yesterday", func(t time.Time) bool { return sameDay(t, time.Now().AddDate(0, 0, -1)) }},
		{"week", "Previous 7 Days", func(t time.Time) bool { return time.Since(t) < 7*24*time.Hour }},
		{"month", "Previous 30 Days", func(t time.Time) bool { return time.Since(t) < 30*24*time.Hour }},
		{"older", "Older", func(time.Time) bool { return true }},
	}
	byKey := map[string]*threadGroup{}
	var groups []threadGroup
	for _, th := range a.threads {
		if th.ProjectID != a.activeProject {
			continue
		}
		if q != "" && !threadMatches(th, q) {
			continue
		}
		for _, n := range names {
			if !n.is(th.Updated) {
				continue
			}
			g := byKey[n.key]
			if g == nil {
				groups = append(groups, threadGroup{key: n.key, title: n.title})
				g = &groups[len(groups)-1]
				byKey[n.key] = g
			}
			g.threads = append(g.threads, th)
			break
		}
	}
	return groups
}

type threadGroup struct {
	key, title string
	threads    []*Thread
}

func threadMatches(th *Thread, q string) bool {
	if strings.Contains(strings.ToLower(th.Title), q) {
		return true
	}
	for _, m := range th.Messages {
		if strings.Contains(strings.ToLower(m.Text), q) {
			return true
		}
	}
	return false
}

func sameDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

// relTime formats a time the way the sidebar does: "now", "12m", "3h",
// the weekday within the last week, otherwise the date.
func relTime(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 7*24*time.Hour:
		return t.Format("Mon")
	case t.Year() == time.Now().Year():
		return t.Format("1/2")
	default:
		return t.Format("1/2/06")
	}
}

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
