package app

import (
	uipkg "mygo-agent/internal/ui"

	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"mygo-agent/internal/harness"
	"mygo-agent/internal/harness/builtin"
	"mygo-agent/internal/harness/codex"

	"github.com/egoist/mygo/ui"
)

// The manage-providers dialog renders in internal/ui over a ViewModel
// snapshot (spec/architecture.md); this file is the bridge: snapshot
// assembly, the Actions implementation, the mirror of the form's
// bindings back into host state, and the host-side mutations.

// providerPreset is a one-click fill for the provider form: OpenAI-
// compatible endpoints the codex CLI can talk to with wire_api="chat".
var providerPresets = []struct {
	name    string
	baseURL string
	wire    string
	models  []string
}{
	{"OpenAI", "https://api.openai.com/v1", harness.WireResponses, []string{"gpt-5.2", "gpt-5.2-mini"}},
	{"DeepSeek", "https://api.deepseek.com/v1", harness.WireChat, []string{"deepseek-chat", "deepseek-reasoner"}},
	{"OpenRouter", "https://openrouter.ai/api/v1", harness.WireChat, []string{"openai/gpt-5.2"}},
	{"Ollama (local)", "http://localhost:11434/v1", harness.WireChat, []string{"llama3.2"}},
}

// settingsModal renders the manage-providers dialog.
func (a *app) settingsModal(c *ui.Context) {
	// Nothing to draw while closed. The snapshot clones every provider's
	// model list and formats two strings per provider, so building it
	// unconditionally made the common case (a closed dialog) the most
	// expensive thing in the frame.
	if !a.settingsOpen {
		a.settingsWereOpen = false
		return
	}
	if !a.settingsWereOpen {
		// Opening the dialog refreshes its per-open data: the skills
		// discovery for the active project (it walks directories), so a
		// skill added since last time is there.
		a.dialogSkills = builtin.DiscoverSkills(a.workdir).Skills
	}
	a.settingsWereOpen = true
	vm := a.settingsVM()
	uipkg.Settings(c, vm, settingsActions{a: a, vm: vm})
	a.settingsOpen = vm.Open // the backdrop and Escape close it view-side
	a.syncSettings(vm)
}

// settingsVM snapshots the dialog's render input. The providers' fields
// and the MCP draft lines are copies the view binds into; syncSettings
// mirrors them back after the frame.
func (a *app) settingsVM() *uipkg.SettingsVM {
	vm := &uipkg.SettingsVM{
		Open: a.settingsOpen,
		Tab:  a.settingsTab,
		Sel:  a.settingsSel,
		Pal:  a.pal,
	}
	if vm.Tab == "" {
		vm.Tab = uipkg.TabAgents
	}
	vm.MCPName, vm.MCPCommand = a.mcpDraftName, a.mcpDraftCommand
	// The codex-runtime hint only matters when something actually runs
	// the codex CLI against this provider: the app's backend switch, or
	// an agent pinned to codex.
	codexInUse := a.backend == "codex"
	for i := range a.agents {
		if a.agents[i].Backend == "codex" {
			codexInUse = true
		}
	}
	ids := make([]string, 0, len(a.providers))
	agentNames := make([]string, 0, len(a.agents))
	for i := range a.agents {
		agentNames = append(agentNames, a.agents[i].Name)
	}
	for i := range a.providers {
		p := &a.providers[i]
		runsAs := ""
		if codexInUse && p.ID != "codex" {
			runsAs = fmt.Sprintf("Runs as: codex exec -c model_provider=%s -m <model>, API key via $%s",
				p.ID, codex.EnvKey(p.ID))
		}
		ids = append(ids, p.ID)
		vm.Providers = append(vm.Providers, uipkg.ProviderEditVM{
			ID: p.ID, Name: p.Name, BaseURL: p.BaseURL, APIKey: p.APIKey,
			Wire: p.Wire, Models: slices.Clone(p.Models),
			RunsAs:    runsAs,
			Available: a.fetchedModels[p.ID], FetchErr: a.fetchErrs[p.ID],
		})
	}
	vm.Catalog = a.catalogVM()
	vm.Skills = a.skillsVM()
	vm.AgentNames = strings.Join(agentNames, ", ")
	for _, ps := range providerPresets {
		vm.Presets = append(vm.Presets, uipkg.PresetVM{Name: ps.name})
	}
	for _, s := range a.mcpServers {
		line := s.URL
		if line == "" {
			line = s.Command + " " + strings.Join(s.Args, " ")
		}
		vm.MCPServer = append(vm.MCPServer, uipkg.MCPVM{
			Name: s.Name, Command: line,
		})
	}
	for i := range a.agents {
		ag := &a.agents[i]
		ve := uipkg.AgentEditVM{
			ID: ag.ID, Name: ag.Name, Emoji: ag.Emoji,
			Backend: ag.Backend, Provider: ag.Provider, Model: ag.Model,
			// 0 in the VM means "follow the app"; the config stores nil.
			Effort:        -1,
			Mode:          -1,
			SystemPrompt:  ag.SystemPrompt,
			ToolsDisabled: slices.Clone(ag.Tools.Disabled),
			MCPServers:    slices.Clone(ag.MCPServers),
			SkillsAllow:   slices.Clone(ag.Skills.Allow),
			SkillsDeny:    slices.Clone(ag.Skills.Deny),
			Panel:         slices.Clone(ag.Panel),
		}
		// The routed relay's knobs (spec/relay-router.md): numbers as
		// text, "" = default or off; the route and wire are segments.
		if ag.PanelRoute == "router" {
			ve.PanelRoute = 1
		}
		ve.PanelBlurb = ag.PanelBlurb
		ve.PanelMaxRounds = optNum(ag.PanelMaxRounds)
		ve.PanelStall = optNum(ag.PanelStallRounds)
		ve.PanelMaxTokens = optNum(ag.PanelMaxTokens)
		ve.PanelTimeout = optNum(ag.PanelTimeout)
		ve.PanelSummarizer = ag.PanelSummarizer
		ve.RouterWire = routerWireIndex(ag.RouterWire)
		ve.RouterProvider = ag.RouterProvider
		ve.RouterModel = ag.RouterModel
		// The VM's 0 means "follow the app", so a set value loads
		// shifted up by one — mirroring syncAgent's minus one on the way
		// back. Without the shift every rendered frame dragged the value
		// one segment toward the default until it dissolved into it.
		if ag.Effort != nil {
			ve.Effort = *ag.Effort + 1
		}
		if ag.Mode != nil {
			ve.Mode = *ag.Mode + 1
		}
		if ag.MaxTurns > 0 {
			ve.MaxTurns = strconv.Itoa(ag.MaxTurns)
		}
		ve.Sub = a.agentSub(ag)
		// The model chips come from whoever will serve this agent: a
		// pinned CLI backend serves its own table, otherwise the
		// resolved provider's list (spec/agents.md).
		switch {
		case ag.Backend == "claude":
			ve.ModelOptions = claudeModels
		case ag.Backend == "codex":
			ve.ModelOptions = defaultModels
		default:
			pid := ag.Provider
			if pid == "" {
				pid = a.providerID
			}
			if p := a.providerByID(pid); p != nil {
				ve.ModelOptions = p.Models
			}
		}
		for i := range a.providers {
			if a.providers[i].BaseURL != "" {
				ve.ProviderOpts = append(ve.ProviderOpts, uipkg.ProviderVM{
					ID: a.providers[i].ID, Name: a.providers[i].Name,
				})
			}
		}
		vm.Agents = append(vm.Agents, ve)
	}
	return vm
}

// catalogVM snapshots the built-in tool registry for the tools tab and
// the agent form's checkboxes (spec/agents.md). The registry is static,
// so the snapshot is cached for the process.
func (a *app) catalogVM() []uipkg.ToolInfoVM {
	catalogOnce.Do(func() { catalogCached = builtin.ToolCatalog() })
	out := make([]uipkg.ToolInfoVM, len(catalogCached))
	for i, ti := range catalogCached {
		out[i] = uipkg.ToolInfoVM{Name: ti.Name, Description: ti.Description, Actions: ti.Actions}
	}
	return out
}

var (
	catalogOnce   sync.Once
	catalogCached []builtin.ToolInfo
)

// skillsVM lists the skills discovered for the active project, with
// where each came from. Refreshed when the dialog opens
// (settingsModal's open edge), not per frame.
func (a *app) skillsVM() []uipkg.SkillVM {
	out := make([]uipkg.SkillVM, 0, len(a.dialogSkills))
	for _, sk := range a.dialogSkills {
		source := "user"
		if strings.HasPrefix(sk.Dir, a.workdir) {
			source = "project"
		}
		out = append(out, uipkg.SkillVM{Name: sk.Name, Description: sk.Description, Source: source})
	}
	return out
}

// optNum renders an optional number for its form field: "" means the
// default or off.
func optNum(n int) string {
	if n <= 0 {
		return ""
	}
	return strconv.Itoa(n)
}

// routerWireIndex maps the config's router_wire to the form segment.
func routerWireIndex(wire string) int {
	switch wire {
	case "decision":
		return 1
	case "hybrid":
		return 2
	}
	return 0
}

// routerWireFromIndex is the config value the form segment writes.
func routerWireFromIndex(i int) string {
	switch i {
	case 1:
		return "decision"
	case 2:
		return "hybrid"
	}
	return ""
}

// syncSettings mirrors the form's bindings into host state and saves
// when the frame actually changed a provider or an agent.
func (a *app) syncSettings(vm *uipkg.SettingsVM) {
	a.mcpDraftName, a.mcpDraftCommand = vm.MCPName, vm.MCPCommand
	dirty := false
	for i := range vm.Providers {
		v := &vm.Providers[i]
		p := a.providerByID(v.ID)
		if p == nil {
			continue // removed while the frame was up
		}
		if p.Name != v.Name || p.BaseURL != v.BaseURL || p.APIKey != v.APIKey ||
			p.Wire != v.Wire || !slices.Equal(p.Models, v.Models) {
			p.Name, p.BaseURL, p.APIKey, p.Wire = v.Name, v.BaseURL, v.APIKey, v.Wire
			p.Models = v.Models
			dirty = true
		}
	}
	for i := range vm.Agents {
		dirty = a.syncAgent(&vm.Agents[i]) || dirty
	}
	if dirty {
		a.saveConfig()
	}
}

// syncAgent mirrors one agent form's bindings into its profile. The VM
// carries effort/mode offset by one (0 = follow the app) and the turn
// budget as text; an unparseable number is ignored rather than guessed.
func (a *app) syncAgent(v *uipkg.AgentEditVM) bool {
	ag := a.agentByID(v.ID)
	if ag == nil {
		return false // removed while the frame was up
	}
	v.Name = strings.TrimSpace(v.Name)
	if v.Name == "" {
		v.Name = ag.Name // an agent without a name is not a thing
	}
	maxTurns := 0
	if n, err := strconv.Atoi(strings.TrimSpace(v.MaxTurns)); err == nil && n > 0 {
		maxTurns = n
	} else if ag.MaxTurns > 0 {
		// A typo in one box keeps what the profile had rather than
		// silently zeroing the budget.
		v.MaxTurns = strconv.Itoa(ag.MaxTurns)
		maxTurns = ag.MaxTurns
	}
	effort := -1
	if v.Effort >= 0 {
		effort = v.Effort - 1
	}
	mode := -1
	if v.Mode >= 0 {
		mode = v.Mode - 1
	}
	// The routed relay's knobs (spec/relay-router.md). A typo in a
	// number keeps what the profile had — the MaxTurns convention.
	route := "sequence"
	if v.PanelRoute == 1 {
		route = "router"
	}
	panelNum := func(s string, cur int) int {
		s = strings.TrimSpace(s)
		if s == "" {
			return 0
		}
		if n, err := strconv.Atoi(s); err == nil && n >= 0 {
			return n
		}
		return cur
	}
	maxRounds := panelNum(v.PanelMaxRounds, ag.PanelMaxRounds)
	stall := panelNum(v.PanelStall, ag.PanelStallRounds)
	maxTokens := panelNum(v.PanelMaxTokens, ag.PanelMaxTokens)
	timeout := panelNum(v.PanelTimeout, ag.PanelTimeout)
	wire := routerWireFromIndex(v.RouterWire)
	sameInt := func(p *int, n int) bool {
		if (p == nil) != (n < 0) {
			return false
		}
		return p == nil || *p == n
	}
	if ag.Name != v.Name || ag.Emoji != v.Emoji || ag.Backend != v.Backend ||
		ag.Provider != v.Provider || ag.Model != v.Model ||
		ag.SystemPrompt != v.SystemPrompt || ag.MaxTurns != maxTurns ||
		!sameInt(ag.Effort, effort) || !sameInt(ag.Mode, mode) ||
		!slices.Equal(ag.Tools.Disabled, v.ToolsDisabled) ||
		!slices.Equal(ag.MCPServers, v.MCPServers) ||
		!slices.Equal(ag.Skills.Allow, v.SkillsAllow) ||
		!slices.Equal(ag.Skills.Deny, v.SkillsDeny) ||
		!slices.Equal(ag.Panel, v.Panel) ||
		ag.PanelRoute != route || ag.PanelBlurb != v.PanelBlurb ||
		ag.PanelMaxRounds != maxRounds || ag.PanelStallRounds != stall ||
		ag.PanelMaxTokens != maxTokens || ag.PanelTimeout != timeout ||
		ag.PanelSummarizer != v.PanelSummarizer ||
		ag.RouterWire != wire || ag.RouterProvider != v.RouterProvider ||
		ag.RouterModel != v.RouterModel {
		ag.Name, ag.Emoji, ag.Backend = v.Name, v.Emoji, v.Backend
		ag.Provider, ag.Model, ag.SystemPrompt = v.Provider, v.Model, v.SystemPrompt
		ag.MaxTurns = maxTurns
		ag.Effort, ag.Mode = nil, nil
		if effort >= 0 {
			e := effort
			ag.Effort = &e
		}
		if mode >= 0 {
			m := mode
			ag.Mode = &m
		}
		ag.Tools.Disabled = v.ToolsDisabled
		ag.MCPServers = v.MCPServers
		ag.Skills.Allow, ag.Skills.Deny = v.SkillsAllow, v.SkillsDeny
		// A server list covering every configured server is the same
		// thing as no list - normalize to nil (mount all), keeping the
		// config clean for servers added later.
		if len(a.mcpServers) > 0 && len(ag.MCPServers) == len(a.mcpServers) {
			names := make([]string, 0, len(a.mcpServers))
			for _, srv := range a.mcpServers {
				names = append(names, srv.Name)
			}
			slices.Sort(names)
			cover := slices.Clone(ag.MCPServers)
			slices.Sort(cover)
			if slices.Equal(names, cover) {
				ag.MCPServers = nil
			}
		}
		ag.Panel = v.Panel
		ag.PanelRoute = route
		ag.PanelBlurb = strings.TrimSpace(v.PanelBlurb)
		ag.PanelMaxRounds = maxRounds
		ag.PanelStallRounds = stall
		ag.PanelMaxTokens = maxTokens
		ag.PanelTimeout = timeout
		ag.PanelSummarizer = strings.TrimSpace(v.PanelSummarizer)
		ag.RouterWire = wire
		ag.RouterProvider, ag.RouterModel = v.RouterProvider, v.RouterModel
		return true
	}
	return false
}

// settingsActions adapts *app to ui.SettingsActions. vm is this frame's
// snapshot: value edits that arrive mid-frame (pills, presets) write
// into it, and syncSettings mirrors them to host after the frame —
// mutating host directly here would be overwritten by that mirror with
// the frame's stale snapshot.
type settingsActions struct {
	a  *app
	vm *uipkg.SettingsVM
}

// providerVM finds the snapshot row of one provider.
func (h settingsActions) providerVM(id string) *uipkg.ProviderEditVM {
	for i := range h.vm.Providers {
		if h.vm.Providers[i].ID == id {
			return &h.vm.Providers[i]
		}
	}
	return nil
}

func (h settingsActions) Select(id string)         { h.a.settingsSel = id }
func (h settingsActions) AddProvider()             { h.a.addProvider() }
func (h settingsActions) RemoveProvider(id string) { h.a.removeProvider(id) }
func (h settingsActions) AddMCP()                  { h.a.addMCPServer() }
func (h settingsActions) AddAgent()                { h.a.addAgent() }
func (h settingsActions) AddGroup()                { h.a.addGroup() }
func (h settingsActions) RemoveAgent(id string)    { h.a.removeAgent(id) }

func (h settingsActions) RemoveMCP(i int) {
	if i < 0 || i >= len(h.a.mcpServers) {
		return
	}
	h.a.mcpServers = slices.Delete(h.a.mcpServers, i, i+1)
	h.a.saveConfig()
}

// ApplyPreset fills the snapshot's provider fields from the preset; the
// mirror applies it to the host and saves.
func (h settingsActions) ApplyPreset(id string, preset int) {
	p := h.providerVM(id)
	if p == nil || preset < 0 || preset >= len(providerPresets) {
		return
	}
	ps := providerPresets[preset]
	p.Name, p.BaseURL, p.Wire = ps.name, ps.baseURL, ps.wire
	p.Models = slices.Clone(ps.models)
}

// SetWire writes the wire choice into the snapshot; the mirror applies.
func (h settingsActions) SetWire(id, wire string) {
	if p := h.providerVM(id); p != nil {
		p.Wire = wire
	}
}

// addMCPServer parses the draft line into a server and saves it. The
// line is either a "command args…" stdio server or an http(s) URL for a
// streamable HTTP one — the transport picks by URL at spawn time.
func (a *app) addMCPServer() {
	name := strings.TrimSpace(a.mcpDraftName)
	line := strings.TrimSpace(a.mcpDraftCommand)
	if name == "" || line == "" {
		return
	}
	srv := builtin.MCPServer{Name: name}
	if strings.HasPrefix(line, "http://") || strings.HasPrefix(line, "https://") {
		srv.URL = line
	} else {
		fields := strings.Fields(line)
		srv.Command, srv.Args = fields[0], fields[1:]
	}
	a.mcpServers = append(a.mcpServers, srv)
	a.mcpDraftName, a.mcpDraftCommand = "", ""
	a.saveConfig()
}

// addProvider appends an empty provider and opens it for editing.
func (a *app) addProvider() {
	p := Provider{ID: "prov-" + uid(), Name: "New provider"}
	a.providers = append(a.providers, p)
	a.settingsSel = p.ID
	a.saveConfig()
}

// addGroup appends a group profile — the visible entry to the relay
// (spec/agents.md) — and opens its form, where the panel members go.
func (a *app) addGroup() {
	ag := Agent{ID: "ag-" + uid(), Name: "Group"}
	a.agents = append(a.agents, ag)
	a.settingsSel, a.settingsTab = ag.ID, uipkg.TabAgents
	a.settingsOpen = true
	a.saveConfig()
}

// addAgent appends a blank profile and opens it for editing. An empty
// profile inherits the app's selection, so it is runnable as-is.
func (a *app) addAgent() {
	ag := Agent{ID: "ag-" + uid(), Name: "New agent"}
	a.agents = append(a.agents, ag)
	a.settingsSel = ag.ID
	a.saveConfig()
}

// removeAgent deletes a profile, repairing every pointer to it: the
// default, the composer's selection and the dialog's. The last agent
// stays — the app always has one agent to bind a task to.
func (a *app) removeAgent(id string) {
	if len(a.agents) <= 1 {
		return
	}
	at := slices.IndexFunc(a.agents, func(ag Agent) bool { return ag.ID == id })
	if at < 0 {
		return
	}
	a.agents = slices.Delete(a.agents, at, at+1)
	if a.defaultAgent == id {
		a.defaultAgent = a.agents[0].ID
	}
	if a.activeAgent == id {
		a.activeAgent = ""
	}
	if a.settingsSel == id {
		a.settingsSel = a.agents[0].ID
	}
	a.saveConfig()
}

// removeProvider deletes a provider, repairing the selection.
func (a *app) removeProvider(id string) {
	if len(a.providers) <= 1 || id == "codex" {
		return
	}
	at := slices.IndexFunc(a.providers, func(p Provider) bool { return p.ID == id })
	if at < 0 {
		return
	}
	a.providers = slices.Delete(a.providers, at, at+1)
	if a.providerID == id {
		a.providerID = a.providers[0].ID
		// addProvider creates with an empty model list, so the provider
		// taking the active slot may have none: fall back to the empty
		// model instead of indexing out of range — there is no recover
		// anywhere in the process.
		if models := a.provider().Models; len(models) > 0 {
			a.model = models[0]
		} else {
			a.model = ""
		}
	}
	if a.settingsSel == id {
		if len(a.providers) > 0 {
			a.settingsSel = a.providers[0].ID
		} else {
			a.settingsSel = ""
		}
	}
	a.saveConfig()
}

// SelectTab switches the dialog's tab, landing on the tab's first row so
// the form never shows a resource from another tab.
func (h settingsActions) SelectTab(tab string) {
	switch tab {
	case uipkg.TabAgents, uipkg.TabProvider, uipkg.TabTools, uipkg.TabSkills, uipkg.TabMCP:
		h.a.settingsTab = tab
	}
	switch tab {
	case uipkg.TabProvider:
		if h.a.providerByID(h.a.settingsSel) == nil && len(h.a.providers) > 0 {
			h.a.settingsSel = h.a.providers[0].ID
		}
	case uipkg.TabAgents:
		if h.a.agentByID(h.a.settingsSel) == nil {
			h.a.settingsSel = h.a.defaultAgentID()
		}
	}
}

// FetchModels asks the provider itself what it serves (GET /models on
// the OpenAI-compatible endpoint). It runs inline — the click blocks
// the dialog for the round trip — and lands in the frame's snapshot
// plus the app's session cache.
func (h settingsActions) FetchModels(id string) {
	v := h.providerVM(id)
	if v == nil {
		return
	}
	p := h.a.providerByID(id)
	if p == nil || p.BaseURL == "" {
		v.FetchErr = "no base URL configured"
		return
	}
	models, err := fetchProviderModels(p.BaseURL, p.APIKey)
	if err != nil {
		v.FetchErr = err.Error()
		return
	}
	v.FetchErr = ""
	v.Available = models
	h.a.fetchedModels[id] = models
}

// fetchProviderModels reads an OpenAI-compatible /models listing, with
// a fallback for Ollama's own shape.
func fetchProviderModels(baseURL, apiKey string) ([]string, error) {
	url := strings.TrimRight(baseURL, "/") + "/models"
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	var openai struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if json.Unmarshal(data, &openai) == nil && len(openai.Data) > 0 {
		out := make([]string, 0, len(openai.Data))
		for _, m := range openai.Data {
			if m.ID != "" {
				out = append(out, m.ID)
			}
		}
		sort.Strings(out)
		return out, nil
	}
	var ollama struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if json.Unmarshal(data, &ollama) == nil && len(ollama.Models) > 0 {
		out := make([]string, 0, len(ollama.Models))
		for _, m := range ollama.Models {
			if m.Name != "" {
				out = append(out, m.Name)
			}
		}
		sort.Strings(out)
		return out, nil
	}
	return nil, fmt.Errorf("unrecognized models response from %s", url)
}
