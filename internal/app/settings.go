package app

import (
	uipkg "mygo-agent/internal/ui"

	"fmt"
	"slices"
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
	uipkg.Settings(c, vm, settingsActions{a: a})
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
	for i := range a.providers {
		p := &a.providers[i]
		vm.Providers = append(vm.Providers, uipkg.ProviderEditVM{
			ID: p.ID, Name: p.Name, BaseURL: p.BaseURL, APIKey: p.APIKey,
			Wire: p.Wire, Models: slices.Clone(p.Models),
			Codex: p.ID == "codex",
			RunsAs: fmt.Sprintf("Runs as: codex exec -c model_provider=%s -m <model>, API key via $%s",
				p.ID, codex.EnvKey(p.ID)),
		})
	}
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
	return vm
}

// syncSettings mirrors the form's bindings into host state and saves
// when the frame actually changed a provider.
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
	if dirty {
		a.saveConfig()
	}
}

// settingsActions adapts *app to ui.SettingsActions.
type settingsActions struct{ a *app }

func (h settingsActions) Select(id string)         { h.a.settingsSel = id }
func (h settingsActions) AddProvider()             { h.a.addProvider() }
func (h settingsActions) RemoveProvider(id string) { h.a.removeProvider(id) }
func (h settingsActions) AddMCP()                  { h.a.addMCPServer() }

func (h settingsActions) RemoveMCP(i int) {
	if i < 0 || i >= len(h.a.mcpServers) {
		return
	}
	h.a.mcpServers = slices.Delete(h.a.mcpServers, i, i+1)
	h.a.saveConfig()
}

func (h settingsActions) ApplyPreset(id string, preset int) {
	if preset < 0 || preset >= len(providerPresets) {
		return
	}
	p := h.a.providerByID(id)
	if p == nil || p.ID == "codex" {
		return
	}
	ps := providerPresets[preset]
	p.Name, p.BaseURL, p.Wire = ps.name, ps.baseURL, ps.wire
	p.Models = slices.Clone(ps.models)
	h.a.saveConfig()
}

func (h settingsActions) SetWire(id, wire string) {
	p := h.a.providerByID(id)
	if p == nil {
		return
	}
	p.Wire = wire
	h.a.saveConfig()
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
