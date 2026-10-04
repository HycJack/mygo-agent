package main

import (
	"fmt"
	"slices"
	"strings"

	"mygo-agent/internal/agent"

	"github.com/egoist/mygo/ui"
)

// providerPreset is a one-click fill for the provider form: OpenAI-
// compatible endpoints the codex CLI can talk to with wire_api="chat".
var providerPresets = []struct {
	name    string
	baseURL string
	wire    string
	models  []string
}{
	{"OpenAI", "https://api.openai.com/v1", agent.WireResponses, []string{"gpt-5.2", "gpt-5.2-mini"}},
	{"DeepSeek", "https://api.deepseek.com/v1", agent.WireChat, []string{"deepseek-chat", "deepseek-reasoner"}},
	{"OpenRouter", "https://openrouter.ai/api/v1", agent.WireChat, []string{"openai/gpt-5.2"}},
	{"Ollama (local)", "http://localhost:11434/v1", agent.WireChat, []string{"llama3.2"}},
}

// settingsModal is the manage-providers dialog: the providers on the
// left, the chosen one's name, endpoint, key and models on the right.
// Every change saves immediately.
func (a *app) settingsModal(c *ui.Context) {
	t := c.Theme()
	ui.Modal(c, &a.settingsOpen, func() {
		ui.Row(c).Width(780).Height(480).Radius(12).Clip().Background(a.pal.Bg).
			Border(1, a.pal.Border).Shadow(0, 12, 32, 0, ui.RGBA(0, 0, 0, 0.5)).
			AlignItems(ui.Stretch).Children(func() {
			// The provider list.
			ui.Column(c).Width(220).Background(a.pal.SidebarBG).BorderWidth(0, 1, 0, 0).
				BorderColor(a.pal.Border).Children(func() {
				ui.Text(c, "PROVIDERS").FontSize(10.5).FontWeight(600).TextColor(a.pal.TextMuted).
					Padding(14, 14, 6).LetterSpacing(0.6)
				ui.Scroll(c).Grow(1).Padding(0, 8, 8).Children(func() {
					for i := range a.providers {
						p := &a.providers[i]
						row := ui.ButtonBase(c).Fill().Padding(7, 10).Radius(7).Gap(8)
						chosen := p.ID == a.settingsSel
						if chosen {
							row.Background(a.pal.Sel)
						} else if row.Hovered() {
							row.Background(a.pal.Hover)
						}
						if row.Clicked() {
							a.settingsSel = p.ID
						}
						row.ContextMenu(func(m *ui.Menu) {
							if m.Item("Remove provider").Chosen() {
								a.removeProvider(p.ID)
							}
						})
						row.Children(func() {
							ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
								ui.Text(c, p.Name).SingleLine().FontSize(12.5)
								ui.Textf(c, "%d models", len(p.Models)).SingleLine().FontSize(10.5).TextColor(a.pal.TextMuted)
							})
						})
					}
					add := ui.ButtonBase(c).Fill().Padding(7, 10).Radius(7).Gap(8).Margin(0, 0, 4)
					if add.Hovered() {
						add.Background(a.pal.Hover)
					}
					if add.Clicked() {
						a.addProvider()
					}
					add.Children(func() {
						ui.Icon(c, icPlus).FontSize(13).TextColor(t.TextMuted)
						ui.Text(c, "Add provider").FontSize(12.5).TextColor(t.TextMuted)
					})
				})
			})
			// The form.
			ui.Column(c).Grow(1).MinWidth(0).Children(func() {
				p := a.providerByID(a.settingsSel)
				if p == nil {
					ui.Column(c).Fill().Center().Gap(8).Children(func() {
						ui.Text(c, "No provider selected.").FontSize(13).TextColor(t.TextMuted)
						ui.Text(c, "Add one, or pick a preset to fill in.").FontSize(12).TextColor(a.pal.TextMuted)
					})
					return
				}
				pi := slices.IndexFunc(a.providers, func(x Provider) bool { return x.ID == p.ID })
				ui.Scroll(c).Grow(1).Children(func() {
					ui.Column(c).FillWidth().Padding(20, 24, 24).Gap(14).Children(func() {
						ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
							ui.Text(c, p.Name).FontSize(16).Bold().SingleLine().Grow(1).MinWidth(0)
							if p.ID == "codex" {
								ui.Text(c, "uses the codex CLI's own sign-in").FontSize(11).TextColor(t.TextMuted)
							}
						})
						if p.ID != "codex" {
							ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
								ui.Text(c, "Presets:").FontSize(11.5).TextColor(t.TextMuted)
								for _, ps := range providerPresets {
									pr := ui.Button(c, ps.name).FontSize(11.5)
									if pr.Clicked() {
										a.providers[pi].Name = ps.name
										a.providers[pi].BaseURL = ps.baseURL
										a.providers[pi].Wire = ps.wire
										a.providers[pi].Models = slices.Clone(ps.models)
										a.saveConfig()
									}
								}
							})
							a.formField(c, "Name", &a.providers[pi].Name, false)
							a.formField(c, "Base URL", &a.providers[pi].BaseURL, false)
							a.formField(c, "API key", &a.providers[pi].APIKey, true)
							// Which wire the endpoint speaks: the codex
							// and OpenAI models use the Responses API,
							// most other vendors chat completions.
							ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
								ui.Text(c, "API").FontSize(11.5).FontWeight(600).TextColor(t.TextMuted)
								for _, w := range []struct {
									id, label string
								}{{agent.WireChat, "Chat Completions"}, {agent.WireResponses, "Responses"}} {
									wire := a.providers[pi].Wire
									if wire == "" {
										wire = agent.WireChat
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
										a.providers[pi].Wire = w.id
										a.saveConfig()
									}
								}
							})
							ui.Column(c).Gap(4).Children(func() {
								ui.Text(c, "Models").FontSize(11.5).FontWeight(600).TextColor(t.TextMuted)
								ui.TokenField(c, &a.providers[pi].Models, nil)
								ui.Text(c, "Enter or comma adds a model; Backspace removes the last.").FontSize(11).TextColor(t.TextMuted)
							})
							ui.Row(c).Gap(8).AlignItems(ui.Center).Padding(10, 12).Radius(8).
								Background(a.pal.Card).Children(func() {
								ui.Icon(c, icTerminal).FontSize(13).TextColor(a.pal.TextMuted)
								ui.Textf(c, "Runs as: codex exec -c model_provider=%s -m <model>, API key via $%s",
									p.ID, envKeyFor(p.ID)).FontSize(11).TextColor(t.TextMuted).Grow(1).MinWidth(0)
							})
							ui.Row(c).Justify(ui.End).Children(func() {
								del := ui.Button(c, "Delete provider")
								if del.Clicked() {
									a.removeProvider(p.ID)
								}
							})
						} else {
							ui.Text(c, "The models below come from the codex CLI's own sign-in — no key needed. "+
								"Add a provider above to use any OpenAI-compatible endpoint.").FontSize(12.5).TextColor(t.TextMuted)
							ui.Row(c).Gap(6).Wrap().Children(func() {
								for _, m := range p.Models {
									ui.Text(c, m).Font("monospace").FontSize(12).Padding(4, 10).Radius(999).
										Background(a.pal.Card).Border(1, a.pal.Border)
								}
							})
							if ui.Button(c, "Add model").Clicked() {
								a.providers[pi].Models = append(a.providers[pi].Models, fmt.Sprintf("gpt-5.2-codex-%s", uid()))
								a.saveConfig()
							}
						}
						a.mcpSection(c)
					})
				})
			})
		})
	})
}

// mcpSection is the MCP servers block at the bottom of the settings
// dialog: each configured server spawns at run time and its tools join
// the agent's tool set as mcp_<server>_<tool>.
func (a *app) mcpSection(c *ui.Context) {
	t := c.Theme()
	ui.Box(c).Height(1).Background(a.pal.Border)
	ui.Column(c).Gap(8).Children(func() {
		ui.Text(c, "MCP SERVERS").FontSize(10.5).FontWeight(600).TextColor(t.TextMuted).LetterSpacing(0.6)
		if len(a.mcpServers) == 0 {
			ui.Text(c, "None configured. A server is a command speaking MCP over stdio; its tools join the agent's set.").FontSize(11.5).TextColor(t.TextMuted)
		}
		for i := range a.mcpServers {
			srv := &a.mcpServers[i]
			row := ui.Row(c).Gap(8).AlignItems(ui.Center).Padding(7, 10).Radius(7).Background(a.pal.Card).
				Border(1, a.pal.Border)
			row.ContextMenu(func(m *ui.Menu) {
				if m.Item("Remove server").Chosen() {
					a.mcpServers = slices.Delete(a.mcpServers, i, i+1)
					a.saveConfig()
				}
			})
			row.Children(func() {
				ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
					ui.Text(c, srv.Name).SingleLine().FontSize(12.5)
					ui.Text(c, srv.Command+" "+strings.Join(srv.Args, " ")).SingleLine().
						Font("monospace").FontSize(11).TextColor(t.TextMuted)
				})
			})
		}
		// Add form.
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Column(c).Gap(3).Grow(1).Children(func() {
				ui.Text(c, "Name").FontSize(10.5).TextColor(t.TextMuted)
				ui.TextInput(c, &a.mcpDraftName).Placeholder("filesystem").FontSize(12)
			})
			ui.Column(c).Gap(3).Grow(3).Children(func() {
				ui.Text(c, "Command and arguments").FontSize(10.5).TextColor(t.TextMuted)
				ui.TextInput(c, &a.mcpDraftCommand).Placeholder(`npx -y @modelcontextprotocol/server-filesystem /tmp`).FontSize(12)
			})
			add := ui.Button(c, "Add")
			if add.Clicked() {
				a.addMCPServer()
			}
		})
	})
}

// addMCPServer parses the draft "command args…" line into a server and
// saves it.
func (a *app) addMCPServer() {
	name := strings.TrimSpace(a.mcpDraftName)
	line := strings.TrimSpace(a.mcpDraftCommand)
	if name == "" || line == "" {
		return
	}
	fields := strings.Fields(line)
	a.mcpServers = append(a.mcpServers, agent.MCPServer{
		Name:    name,
		Command: fields[0],
		Args:    fields[1:],
	})
	a.mcpDraftName, a.mcpDraftCommand = "", ""
	a.saveConfig()
}

// formField is one labeled input of the provider form, saving on change.
func (a *app) formField(c *ui.Context, label string, value *string, password bool) {
	t := c.Theme()
	ui.Column(c).Gap(4).Children(func() {
		ui.Text(c, label).FontSize(11.5).FontWeight(600).TextColor(t.TextMuted)
		in := ui.TextInput(c, value).FontSize(13)
		if password {
			in.Password()
		}
		if in.Changed() {
			a.saveConfig()
		}
	})
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
		a.model = a.provider().Models[0]
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

// envKeyFor is the environment variable a provider's API key is handed
// to the codex CLI through (its model_providers.<id>.env_key).
func envKeyFor(providerID string) string {
	var b strings.Builder
	b.WriteString("MYGO_PROVIDER_")
	for _, r := range strings.ToUpper(providerID) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	b.WriteString("_API_KEY")
	return b.String()
}
