package app

import (
	uipkg "mygo-agent/internal/ui"

	"fmt"
	"slices"
	"strconv"
	"strings"

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
		return
	}
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
		Sel:  a.settingsSel,
		Pal:  a.pal,
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
			Codex: p.ID == "codex", RunsAs: runsAs,
		})
	}
	vm.ProviderIDs = strings.Join(ids, ", ")
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
		vm.Agents = append(vm.Agents, ve)
	}
	return vm
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
		!slices.Equal(ag.Panel, v.Panel) {
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
		ag.Panel = v.Panel
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
	if p == nil || p.ID == "codex" || preset < 0 || preset >= len(providerPresets) {
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

func (h settingsActions) AddModel(id string) {
	p := h.a.providerByID(id)
	if p == nil {
		return
	}
	p.Models = append(p.Models, fmt.Sprintf("gpt-5.2-codex-%s", uid()))
	h.a.saveConfig()
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
