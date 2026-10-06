package ui

import (
	"slices"
	"strings"

	"mygo-agent/internal/harness"

	"github.com/egoist/mygo/ui"
)

// The manage-providers dialog renders here over a ViewModel snapshot
// (spec/architecture.md). The form's editable fields (names, endpoints,
// keys, model lists) bind into the snapshot; the host mirrors them back
// after the frame and saves. Structural changes go through actions.

// ProviderEditVM is one provider row of the list and, chosen, its form.
type ProviderEditVM struct {
	ID      string
	Name    string
	BaseURL string
	APIKey  string
	Wire    string
	Models  []string

	// Codex marks the built-in provider: it uses the codex CLI's own
	// sign-in and shows a read-only view.
	Codex bool
	// RunsAs is the display hint of how the CLI runs this provider.
	RunsAs string
}

// MCPVM is one configured MCP server line.
type MCPVM struct {
	Name    string
	Command string // "cmd args…" as typed
}

// PresetVM is one preset button of the provider form; the host fills in
// what a click means through ApplyPreset.
type PresetVM struct {
	Name string
}

// AgentEditVM is one agent row of the list and, chosen, its form. The
// fields are bindings the host mirrors back after the frame
// (spec/agents.md). Effort and Mode are offset by one: 0 means "follow
// the app", so a segment can express the inheritance.
type AgentEditVM struct {
	ID           string
	Name         string
	Emoji        string
	Backend      string // "" follows the app's switch
	Provider     string // provider id; "" follows the app's selection
	Model        string // "" follows the app's selection
	Effort       int    // 0 follow the app, else 1 + effort
	Mode         int    // 0 follow the app, else 1 + mode
	MaxTurns     string // "" follows the app
	SystemPrompt string

	ToolsDisabled []string
	MCPServers    []string // names; empty mounts everything
	SkillsAllow   []string // both empty discovers as usual
	SkillsDeny    []string
}

// SettingsVM is the render input of the settings dialog. The provider
// fields and the MCP draft lines are bindings: the view writes them, the
// host mirrors them back after the frame.
type SettingsVM struct {
	Open bool
	Sel  string

	Providers []ProviderEditVM
	Presets   []PresetVM
	MCPServer []MCPVM
	Agents    []AgentEditVM

	MCPName, MCPCommand string

	Pal Palette
}

// SettingsActions is what the settings dialog calls back for.
type SettingsActions interface {
	// Select chooses the provider or agent being edited.
	Select(id string)
	AddProvider()
	RemoveProvider(id string)
	// ApplyPreset fills provider id with the preset at index i of the
	// VM's Presets (the host owns the values).
	ApplyPreset(id string, preset int)
	// SetWire switches the API a provider speaks.
	SetWire(id, wire string)
	// AddModel appends a placeholder model to the codex provider.
	AddModel(id string)
	// AddMCP parses the synced draft lines into a server.
	AddMCP()
	// RemoveMCP deletes the server at i.
	RemoveMCP(i int)
	// The agent half (spec/agents.md): AddAgent appends a blank profile,
	// RemoveAgent deletes one (never the last), SetAgentBackend pins a
	// profile to a backend ("" follows the app's switch again).
	AddAgent()
	RemoveAgent(id string)
	SetAgentBackend(id, backend string)
}

// Settings is the manage-providers dialog: the providers on the left,
// the chosen one's name, endpoint, key and models on the right, and the
// MCP servers at the bottom. Every change saves immediately (the host
// mirrors the bindings back and persists).
func Settings(c *ui.Context, vm *SettingsVM, acts SettingsActions) {
	t := c.Theme()
	ui.Modal(c, &vm.Open, func() {
		// Modal's panel already paints the look; keep only the fixed
		// size and the clip for the two-pane layout inside.
		ui.Row(c).Width(780).Height(480).Clip().
			AlignItems(ui.Stretch).Children(func() {
			// The provider list.
			ui.Column(c).Width(220).Background(vm.Pal.SidebarBG).BorderWidth(0, 1, 0, 0).
				BorderColor(vm.Pal.Border).Children(func() {
				ui.Text(c, "PROVIDERS").FontSize(10.5).FontWeight(600).TextColor(vm.Pal.TextMuted).
					Padding(14, 14, 6).LetterSpacing(0.6)
				ui.Scroll(c).Grow(1).Padding(0, 8, 4).Children(func() {
					for pi := range vm.Providers {
						p := &vm.Providers[pi]
						row := ui.ButtonBase(c).Fill().Padding(7, 10).Radius(7).Gap(8)
						chosen := p.ID == vm.Sel
						if chosen {
							row.Background(vm.Pal.Sel)
						} else if row.Hovered() {
							row.Background(vm.Pal.Hover)
						}
						if row.Clicked() {
							acts.Select(p.ID)
						}
						row.ContextMenu(func(m *ui.Menu) {
							if m.Item("Remove provider").Chosen() {
								acts.RemoveProvider(p.ID)
							}
						})
						row.Children(func() {
							ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
								ui.Text(c, p.Name).SingleLine().FontSize(12.5)
								ui.Textf(c, "%d models", len(p.Models)).SingleLine().FontSize(10.5).TextColor(vm.Pal.TextMuted)
							})
						})
					}
					add := ui.ButtonBase(c).Fill().Padding(7, 10).Radius(7).Gap(8)
					if add.Hovered() {
						add.Background(vm.Pal.Hover)
					}
					if add.Clicked() {
						acts.AddProvider()
					}
					add.Children(func() {
						ui.Icon(c, IconPlus).FontSize(13).TextColor(t.TextMuted)
						ui.Text(c, "Add provider").FontSize(12.5).TextColor(t.TextMuted)
					})
				})
				// The agents (spec/agents.md): profiles a task binds to,
				// edited in the same pane as the providers.
				ui.Text(c, "AGENTS").FontSize(10.5).FontWeight(600).TextColor(vm.Pal.TextMuted).
					Padding(10, 14, 6).LetterSpacing(0.6)
				ui.Scroll(c).Grow(1).Padding(0, 8, 8).Children(func() {
					for ai := range vm.Agents {
						ag := &vm.Agents[ai]
						row := ui.ButtonBase(c).Fill().Padding(7, 10).Radius(7).Gap(8)
						chosen := ag.ID == vm.Sel
						if chosen {
							row.Background(vm.Pal.Sel)
						} else if row.Hovered() {
							row.Background(vm.Pal.Hover)
						}
						if row.Clicked() {
							acts.Select(ag.ID)
						}
						row.ContextMenu(func(m *ui.Menu) {
							if m.Item("Remove agent").Chosen() {
								acts.RemoveAgent(ag.ID)
							}
						})
						row.Children(func() {
							ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
								ui.Text(c, strings.TrimSpace(ag.Emoji+" "+ag.Name)).SingleLine().FontSize(12.5)
								backend := ag.Backend
								if backend == "" {
									backend = "app default"
								}
								ui.Text(c, backend).SingleLine().FontSize(10.5).TextColor(vm.Pal.TextMuted)
							})
						})
					}
					addAgent := ui.ButtonBase(c).Fill().Padding(7, 10).Radius(7).Gap(8).Margin(0, 0, 4)
					if addAgent.Hovered() {
						addAgent.Background(vm.Pal.Hover)
					}
					if addAgent.Clicked() {
						acts.AddAgent()
					}
					addAgent.Children(func() {
						ui.Icon(c, IconPlus).FontSize(13).TextColor(t.TextMuted)
						ui.Text(c, "Add agent").FontSize(12.5).TextColor(t.TextMuted)
					})
				})
			})
			// The form: an agent's when one is selected, else the
			// provider's.
			ui.Column(c).Grow(1).MinWidth(0).Children(func() {
				ai := slices.IndexFunc(vm.Agents, func(ag AgentEditVM) bool { return ag.ID == vm.Sel })
				if ai >= 0 {
					ag := &vm.Agents[ai]
					ui.Scroll(c).Grow(1).Children(func() {
						ui.Column(c).FillWidth().Padding(20, 24, 24).Gap(14).Children(func() {
							agentForm(c, vm, acts, ag)
						})
					})
					return
				}
				pi := slices.IndexFunc(vm.Providers, func(p ProviderEditVM) bool { return p.ID == vm.Sel })
				if pi < 0 {
					ui.Column(c).Fill().Center().Gap(8).Children(func() {
						ui.Text(c, "No provider selected.").FontSize(13).TextColor(t.TextMuted)
						ui.Text(c, "Add one, or pick a preset to fill in.").FontSize(12).TextColor(vm.Pal.TextMuted)
					})
					return
				}
				p := &vm.Providers[pi]
				ui.Scroll(c).Grow(1).Children(func() {
					ui.Column(c).FillWidth().Padding(20, 24, 24).Gap(14).Children(func() {
						ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
							ui.Text(c, p.Name).FontSize(16).Bold().SingleLine().Grow(1).MinWidth(0)
							if p.Codex {
								ui.Text(c, "uses the codex CLI's own sign-in").FontSize(11).TextColor(t.TextMuted)
							}
						})
						if !p.Codex {
							settingsForm(c, vm, acts, p)
						} else {
							ui.Text(c, "The models below come from the codex CLI's own sign-in — no key needed. "+
								"Add a provider above to use any OpenAI-compatible endpoint.").FontSize(12.5).TextColor(t.TextMuted)
							ui.Row(c).Gap(6).Wrap().Children(func() {
								for _, m := range p.Models {
									ui.Text(c, m).Font("monospace").FontSize(12).Padding(4, 10).Radius(999).
										Background(vm.Pal.Card).Border(1, vm.Pal.Border)
								}
							})
							if ui.Button(c, "Add model").Clicked() {
								acts.AddModel(p.ID)
							}
						}
						mcpSection(c, vm, acts)
					})
				})
			})
		})
	})
}

// settingsForm is the editable half of the form for a non-codex
// provider: presets, the connection fields, the wire, and the models.
func settingsForm(c *ui.Context, vm *SettingsVM, acts SettingsActions, p *ProviderEditVM) {
	t := c.Theme()
	ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "Presets:").FontSize(11.5).TextColor(t.TextMuted)
		for i, ps := range vm.Presets {
			i, ps := i, ps
			pr := ui.Button(c, ps.Name).FontSize(11.5)
			if pr.Clicked() {
				acts.ApplyPreset(p.ID, i)
			}
		}
	})
	formField(c, "Name", &p.Name, false)
	formField(c, "Base URL", &p.BaseURL, false)
	formField(c, "API key", &p.APIKey, true)
	// Which wire the endpoint speaks: the codex and OpenAI models use
	// the Responses API, most other vendors chat completions.
	ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "API").FontSize(11.5).FontWeight(600).TextColor(t.TextMuted)
		for _, w := range []struct {
			id, label string
		}{{harness.WireChat, "Chat Completions"}, {harness.WireResponses, "Responses"}} {
			wire := p.Wire
			if wire == "" {
				wire = harness.WireChat
			}
			b := ui.ButtonBase(c).Padding(4, 10).Radius(999).Gap(6)
			if wire == w.id {
				b.Background(t.Text)
				b.Children(func() {
					ui.Text(c, w.label).FontSize(11.5).TextColor(t.AccentText)
				})
			} else {
				b.Border(1, t.Border)
				b.Children(func() {
					ui.Text(c, w.label).FontSize(11.5).TextColor(t.TextMuted)
				})
			}
			if b.Clicked() {
				acts.SetWire(p.ID, w.id)
			}
		}
	})
	ui.Column(c).Gap(4).Children(func() {
		ui.Text(c, "Models").FontSize(11.5).FontWeight(600).TextColor(t.TextMuted)
		ui.TokenField(c, &p.Models, nil)
		ui.Text(c, "Enter or comma adds a model; Backspace removes the last.").FontSize(11).TextColor(t.TextMuted)
	})
	ui.Row(c).Gap(8).AlignItems(ui.Center).Padding(10, 12).Radius(8).
		Background(vm.Pal.Card).Children(func() {
		ui.Icon(c, IconTerminal).FontSize(13).TextColor(vm.Pal.TextMuted)
		ui.Text(c, p.RunsAs).FontSize(11).TextColor(vm.Pal.TextMuted).Grow(1).MinWidth(0)
	})
	ui.Row(c).Justify(ui.End).Children(func() {
		if ui.Button(c, "Delete provider").Clicked() {
			acts.RemoveProvider(p.ID)
		}
	})
}

// formField is one labeled input of the provider form; the host saves
// the mirrored value after the frame.
func formField(c *ui.Context, label string, value *string, password bool) {
	t := c.Theme()
	ui.Column(c).Gap(4).Children(func() {
		ui.Text(c, label).FontSize(11.5).FontWeight(600).TextColor(t.TextMuted)
		in := ui.TextInput(c, value).FontSize(13)
		if password {
			in.Password()
		}
	})
}

// mcpSection is the MCP servers block at the bottom of the settings
// dialog: each configured server spawns at run time and its tools join
// the agent's tool set as mcp_<server>_<tool>.
func mcpSection(c *ui.Context, vm *SettingsVM, acts SettingsActions) {
	t := c.Theme()
	ui.Box(c).Height(1).Background(vm.Pal.Border)
	ui.Column(c).Gap(8).Children(func() {
		ui.Text(c, "MCP SERVERS").FontSize(10.5).FontWeight(600).TextColor(t.TextMuted).LetterSpacing(0.6)
		if len(vm.MCPServer) == 0 {
			ui.Text(c, "None configured. A server is a command speaking MCP over stdio, or an https:// URL for streamable HTTP; its tools join the agent's set.").FontSize(11.5).TextColor(t.TextMuted)
		}
		for i := range vm.MCPServer {
			srv := &vm.MCPServer[i]
			row := ui.Row(c).Gap(8).AlignItems(ui.Center).Padding(7, 10).Radius(7).Background(vm.Pal.Card).
				Border(1, vm.Pal.Border)
			row.ContextMenu(func(m *ui.Menu) {
				if m.Item("Remove server").Chosen() {
					acts.RemoveMCP(i)
				}
			})
			row.Children(func() {
				ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
					ui.Text(c, srv.Name).SingleLine().FontSize(12.5)
					ui.Text(c, srv.Command).SingleLine().
						Font("monospace").FontSize(11).TextColor(t.TextMuted)
				})
			})
		}
		// Add form.
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Column(c).Gap(3).Grow(1).Children(func() {
				ui.Text(c, "Name").FontSize(10.5).TextColor(t.TextMuted)
				ui.TextInput(c, &vm.MCPName).Placeholder("filesystem").FontSize(12)
			})
			ui.Column(c).Gap(3).Grow(3).Children(func() {
				ui.Text(c, "Command, or https:// URL").FontSize(10.5).TextColor(t.TextMuted)
				ui.TextInput(c, &vm.MCPCommand).Placeholder(`npx -y @modelcontextprotocol/server-filesystem /tmp`).FontSize(12)
			})
			if ui.Button(c, "Add").Clicked() {
				acts.AddMCP()
			}
		})
	})
}

// agentForm is the editable half for one agent profile (spec/agents.md):
// what it is, what runs it, what it may do. An empty field means
// "follow the app's selection", which is what makes the zero agent
// behave like the pre-agents app.
func agentForm(c *ui.Context, vm *SettingsVM, acts SettingsActions, ag *AgentEditVM) {
	t := c.Theme()
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, strings.TrimSpace(ag.Emoji+" "+ag.Name)).FontSize(16).Bold().SingleLine().Grow(1).MinWidth(0)
	})
	formField(c, "Name", &ag.Name, false)
	formField(c, "Emoji", &ag.Emoji, false)
	// Which backend runs the agent; empty follows the app's switch.
	ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "Backend").FontSize(11.5).FontWeight(600).TextColor(t.TextMuted)
		for _, b := range []struct{ id, label string }{
			{"", "App default"}, {"builtin", "Built-in"},
			{"claude", "Claude Code"}, {"codex", "Codex CLI"}, {"pi", "Pi"},
		} {
			pill := ui.ButtonBase(c).Padding(4, 10).Radius(999)
			if ag.Backend == b.id {
				pill.Background(t.Text)
				pill.Children(func() { ui.Text(c, b.label).FontSize(11.5).TextColor(t.AccentText) })
			} else {
				pill.Border(1, t.Border)
				pill.Children(func() { ui.Text(c, b.label).FontSize(11.5).TextColor(t.TextMuted) })
			}
			if pill.Clicked() {
				acts.SetAgentBackend(ag.ID, b.id)
			}
		}
	})
	formField(c, "Provider id", &ag.Provider, false)
	ui.Text(c, "Empty follows the app's selection; the ids are the providers on the left.").FontSize(11).TextColor(t.TextMuted)
	formField(c, "Model", &ag.Model, false)
	// Effort and Mode carry an extra leading segment: index 0 is
	// "follow the app", the rest are the real values offset by one.
	ui.Row(c).Gap(14).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "Effort").FontSize(11.5).FontWeight(600).TextColor(t.TextMuted)
		Segments(c, ag.Effort+1, []string{"App default", "Low", "Medium", "High"}, func(i int) { ag.Effort = i - 1 }, vm.Pal)
	})
	ui.Row(c).Gap(14).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "Mode").FontSize(11.5).FontWeight(600).TextColor(t.TextMuted)
		Segments(c, ag.Mode+1, []string{"App default", "Read Only", "Agent", "Full Access"}, func(i int) { ag.Mode = i - 1 }, vm.Pal)
	})
	formField(c, "Max turns", &ag.MaxTurns, false)
	formField(c, "System prompt (appended)", &ag.SystemPrompt, false)
	ui.Column(c).Gap(4).Children(func() {
		ui.Text(c, "Disabled tools").FontSize(11.5).FontWeight(600).TextColor(t.TextMuted)
		ui.TokenField(c, &ag.ToolsDisabled, nil)
		ui.Text(c, "Built-in registry: bash, read_file, edit_file, list_files, grep, read_skill.").FontSize(11).TextColor(t.TextMuted)
	})
	ui.Column(c).Gap(4).Children(func() {
		ui.Text(c, "MCP servers").FontSize(11.5).FontWeight(600).TextColor(t.TextMuted)
		ui.TokenField(c, &ag.MCPServers, nil)
		ui.Text(c, "Names from the MCP list; empty mounts all of them.").FontSize(11).TextColor(t.TextMuted)
	})
	ui.Column(c).Gap(4).Children(func() {
		ui.Text(c, "Skills allow / deny").FontSize(11.5).FontWeight(600).TextColor(t.TextMuted)
		ui.TokenField(c, &ag.SkillsAllow, nil)
		ui.TokenField(c, &ag.SkillsDeny, nil)
		ui.Text(c, "Names filter the discovered skills; deny wins, both empty discovers as usual.").FontSize(11).TextColor(t.TextMuted)
	})
	ui.Row(c).Justify(ui.End).Children(func() {
		if ui.Button(c, "Delete agent").Clicked() {
			acts.RemoveAgent(ag.ID)
		}
	})
}
