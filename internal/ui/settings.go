package ui

import (
	"fmt"
	"slices"
	"strings"

	"mygo-agent/internal/harness"

	"github.com/egoist/mygo/ui"
)

// The settings dialog renders here over a ViewModel snapshot
// (spec/architecture.md). Top-level tabs keep the left rail short no
// matter how many resource kinds the app grows; within a tab, the
// form's editable fields bind into the snapshot and the host mirrors
// them back after the frame, saving. Structural changes go through
// actions.

// Settings tabs, in the order the tab bar shows them.
const (
	TabAgents   = "agents"
	TabProvider = "providers"
	TabTools    = "tools"
	TabSkills   = "skills"
	TabMCP      = "mcp"
)

// ProviderEditVM is one provider row of the list and, chosen, its form.
type ProviderEditVM struct {
	ID      string
	Name    string
	BaseURL string
	APIKey  string
	Wire    string
	Models  []string

	// RunsAs is the display hint of how the CLI runs this provider.
	RunsAs string
	// Fetching / FetchErr / Available are the model-fetch state: the
	// provider's own /models listing, to pick additions from.
	Fetching  bool
	FetchErr  string
	Available []string
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

// ToolInfoVM is one row of the built-in tool registry, for the tools
// tab and the agent form's enable checkboxes (spec/agents.md).
type ToolInfoVM struct {
	Name        string
	Description string
	Actions     []string
}

// SkillVM is one discovered skill, for the skills tab and the agent
// form's allow checkboxes.
type SkillVM struct {
	Name        string
	Description string
	Source      string // "project" | "user"
}

// AgentEditVM is one agent row of the list and, chosen, its form. The
// fields are bindings the host mirrors back after the frame
// (spec/agents.md). Effort and Mode are offset by one: 0 means "follow
// the app", so a selector can express the inheritance.
type AgentEditVM struct {
	ID       string
	Name     string
	Emoji    string
	Backend  string // "" follows the app's switch
	Provider string // provider id; "" follows the app's selection
	Model    string // "" follows the app's selection
	// ProviderOpts are the real configured providers (the Provider row
	// picks from these), and ModelOptions are the models of whichever
	// provider this agent resolves to — or the pinned CLI backend's own
	// table (spec/agents.md).
	ProviderOpts []ProviderVM
	ModelOptions []string
	Effort       int    // 0 follow the app, else 1 + effort
	Mode         int    // 0 follow the app, else 1 + mode
	MaxTurns     string // "" follows the app
	SystemPrompt string

	ToolsDisabled []string
	MCPServers    []string // names; nil mounts everything
	SkillsAllow   []string // empty discovers as usual
	SkillsDeny    []string
	// Panel is the group relay's member names, in answer order
	// (spec/agents.md); empty means a solo agent.
	Panel []string
	// Sub is the resolved backend·model (or "panel · N agents") the
	// pickers show; the rail row uses the same.
	Sub string
}

// SettingsVM is the render input of the settings dialog. The provider,
// agent and MCP draft fields are bindings: the view writes them, the
// host mirrors them back after the frame.
type SettingsVM struct {
	Open bool
	Tab  string
	Sel  string

	Agents    []AgentEditVM
	Providers []ProviderEditVM
	Presets   []PresetVM
	MCPServer []MCPVM
	Catalog   []ToolInfoVM
	Skills    []SkillVM

	MCPName, MCPCommand string

	// AgentNames lists the configured agent names for the panel hint.
	AgentNames string

	Pal Palette
}

// SettingsActions is what the settings dialog calls back for.
type SettingsActions interface {
	// SelectTab switches the top-level tab.
	SelectTab(tab string)
	// Select chooses the provider or agent being edited within a tab.
	Select(id string)
	AddProvider()
	RemoveProvider(id string)
	// ApplyPreset fills provider id with the preset at index i of the
	// VM's Presets (written into this frame's snapshot; the host mirrors
	// it after the frame).
	ApplyPreset(id string, preset int)
	// SetWire switches the API a provider speaks (same snapshot rule).
	SetWire(id, wire string)
	// FetchModels queries the provider's own /models listing so models
	// can be picked instead of typed.
	FetchModels(id string)
	// AddMCP parses the synced draft lines into a server.
	AddMCP()
	// RemoveMCP deletes the server at i.
	RemoveMCP(i int)
	// The agent half (spec/agents.md): AddAgent appends a blank profile,
	// RemoveAgent deletes one (never the last). AddGroup appends a group
	// profile and opens its form — the visible entry to the relay. The
	// backend pills bind into the snapshot like every other value field.
	AddAgent()
	RemoveAgent(id string)
	AddGroup()
}

// settingsTabs is the dialog's top bar: one pill per resource kind, so
// the left rail only ever shows the active kind's list.
func settingsTabs(c *ui.Context, vm *SettingsVM, acts SettingsActions) {
	t := c.Theme()
	ui.Row(c).Padding(10, 12, 0).Gap(6).Children(func() {
		for _, tab := range []struct {
			id    string
			label string
		}{
			{TabAgents, "Agents"},
			{TabProvider, "Providers"},
			{TabTools, "Tools"},
			{TabSkills, "Skills"},
			{TabMCP, "MCP"},
		} {
			on := vm.Tab == tab.id
			pill := ui.ButtonBase(c).Padding(5, 12).Radius(999)
			if on {
				pill.Background(c.Theme().Text)
				pill.Children(func() { ui.Text(c, tab.label).FontSize(12).TextColor(t.AccentText) })
			} else {
				pill.Border(1, c.Theme().Border)
				pill.Children(func() { ui.Text(c, tab.label).FontSize(12).TextColor(t.TextMuted) })
			}
			if pill.Clicked() {
				acts.SelectTab(tab.id)
			}
		}
	})
}

// railHeader is a left-rail section header.
func railHeader(c *ui.Context, label string) {
	ui.Text(c, label).FontSize(10.5).FontWeight(600).TextColor(c.Theme().TextMuted).
		Padding(10, 14, 6).LetterSpacing(0.6)
}

// railRow is one selectable row of the left rail.
func railRow(c *ui.Context, vm *SettingsVM, chosen bool, title, sub string, click func(), remove string, removeFn func()) {
	row := ui.ButtonBase(c).Fill().Padding(7, 10).Radius(7).Gap(8)
	if chosen {
		row.Background(vm.Pal.Sel)
	} else if row.Hovered() {
		row.Background(vm.Pal.Hover)
	}
	if row.Clicked() {
		click()
	}
	if removeFn != nil {
		row.ContextMenu(func(m *ui.Menu) {
			if m.Item(remove).Chosen() {
				removeFn()
			}
		})
	}
	row.Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
			ui.Text(c, title).SingleLine().FontSize(12.5)
			if sub != "" {
				ui.Text(c, sub).SingleLine().FontSize(10.5).TextColor(vm.Pal.TextMuted)
			}
		})
	})
}

// railAdd is the rail's "+ Add …" row.
func railAdd(c *ui.Context, vm *SettingsVM, label string, click func()) {
	add := ui.ButtonBase(c).Fill().Padding(7, 10).Radius(7).Gap(8)
	if add.Hovered() {
		add.Background(vm.Pal.Hover)
	}
	if add.Clicked() {
		click()
	}
	add.Children(func() {
		ui.Icon(c, IconPlus).FontSize(13).TextColor(c.Theme().TextMuted)
		ui.Text(c, label).FontSize(12.5).TextColor(c.Theme().TextMuted)
	})
}

// Settings is the settings dialog: a tab bar, the active tab's list on
// the left, its form on the right. Every change saves immediately (the
// host mirrors the bindings back and persists).
func Settings(c *ui.Context, vm *SettingsVM, acts SettingsActions) {
	ui.Modal(c, &vm.Open, func() {
		ui.Column(c).Width(880).Height(560).Clip().Children(func() {
			settingsTabs(c, vm, acts)
			ui.Row(c).Grow(1).MinHeight(0).Clip().AlignItems(ui.Stretch).Children(func() {
				switch vm.Tab {
				case TabProvider:
					settingsProviders(c, vm, acts)
				case TabTools:
					settingsTools(c, vm)
				case TabSkills:
					settingsSkills(c, vm)
				case TabMCP:
					ui.Column(c).Grow(1).Children(func() {
						ui.Scroll(c).Grow(1).Children(func() {
							ui.Column(c).FillWidth().Padding(20, 24, 24).Gap(14).Children(func() {
								ui.Text(c, "MCP servers").FontSize(16).Bold()
								ui.Text(c, "Global servers every project gets; a project's own .mcp.json merges in at run time. A server is a stdio command or an https:// URL.").FontSize(12).TextColor(c.Theme().TextMuted)
								mcpSection(c, vm, acts)
							})
						})
					})
				default:
					settingsAgents(c, vm, acts)
				}
			})
		})
	})
}

// settingsAgents is the agents tab: the profiles on the left, the
// chosen profile's form on the right.
func settingsAgents(c *ui.Context, vm *SettingsVM, acts SettingsActions) {
	ui.Column(c).Width(220).Background(vm.Pal.SidebarBG).BorderWidth(0, 1, 0, 0).
		BorderColor(vm.Pal.Border).Children(func() {
		railHeader(c, "AGENTS")
		ui.Scroll(c).Grow(1).Padding(0, 8, 8).Children(func() {
			for ai := range vm.Agents {
				ag := &vm.Agents[ai]
				sub := ag.Sub
				if sub == "" {
					sub = "app default"
				}
				railRow(c, vm, ag.ID == vm.Sel, strings.TrimSpace(ag.Emoji+" "+ag.Name), sub,
					func() { acts.Select(ag.ID) }, "Remove agent", func() { acts.RemoveAgent(ag.ID) })
			}
			railAdd(c, vm, "Add agent", acts.AddAgent)
			railAdd(c, vm, "New group…", acts.AddGroup)
		})
	})
	ui.Column(c).Grow(1).MinWidth(0).Children(func() {
		ai := slices.IndexFunc(vm.Agents, func(ag AgentEditVM) bool { return ag.ID == vm.Sel })
		if ai < 0 {
			emptyPane(c, "No agent selected.", "Add one, or pick a card on the home screen.")
			return
		}
		ag := &vm.Agents[ai]
		ui.Scroll(c).Grow(1).Children(func() {
			ui.Column(c).FillWidth().Padding(20, 24, 24).Gap(14).Children(func() {
				agentForm(c, vm, acts, ag)
			})
		})
	})
}

// settingsProviders is the providers tab.
func settingsProviders(c *ui.Context, vm *SettingsVM, acts SettingsActions) {
	ui.Column(c).Width(220).Background(vm.Pal.SidebarBG).BorderWidth(0, 1, 0, 0).
		BorderColor(vm.Pal.Border).Children(func() {
		railHeader(c, "PROVIDERS")
		ui.Scroll(c).Grow(1).Padding(0, 8, 8).Children(func() {
			for pi := range vm.Providers {
				p := &vm.Providers[pi]
				railRow(c, vm, p.ID == vm.Sel, p.Name,
					fmt.Sprintf("%d models", len(p.Models)),
					func() { acts.Select(p.ID) }, "Remove provider", func() { acts.RemoveProvider(p.ID) })
			}
			railAdd(c, vm, "Add provider", acts.AddProvider)
		})
	})
	ui.Column(c).Grow(1).MinWidth(0).Children(func() {
		pi := slices.IndexFunc(vm.Providers, func(p ProviderEditVM) bool { return p.ID == vm.Sel })
		if pi < 0 {
			emptyPane(c, "No provider selected.", "Add one, or pick a preset to fill in.")
			return
		}
		p := &vm.Providers[pi]
		ui.Scroll(c).Grow(1).Children(func() {
			ui.Column(c).FillWidth().Padding(20, 24, 24).Gap(14).Children(func() {
				ui.Text(c, p.Name).FontSize(16).Bold().SingleLine().Grow(1).MinWidth(0)
				settingsForm(c, vm, acts, p)
			})
		})
	})
}

// settingsTools is the tools tab: the built-in registry, read-only —
// what each tool does and what it is allowed to do. Per-agent enabling
// lives on the agent form.
func settingsTools(c *ui.Context, vm *SettingsVM) {
	ui.Column(c).Grow(1).Children(func() {
		ui.Scroll(c).Grow(1).Children(func() {
			ui.Column(c).FillWidth().Padding(20, 24, 24).Gap(10).Children(func() {
				ui.Text(c, "Tools").FontSize(16).Bold()
				ui.Text(c, "The built-in registry every agent starts from. An agent's form turns individual tools off; MCP tools arrive from the MCP tab.").FontSize(12).TextColor(c.Theme().TextMuted)
				for _, ti := range vm.Catalog {
					card := ui.Column(c).FillWidth().Padding(12, 14).Radius(8).
						Background(vm.Pal.Card).Border(1, vm.Pal.Border).Gap(4)
					card.Children(func() {
						ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
							ui.Text(c, ti.Name).Font("monospace").FontSize(13)
							for _, a := range ti.Actions {
								ui.Text(c, a).FontSize(10).Padding(1, 7).Radius(999).
									Background(vm.Pal.Bg).Border(1, vm.Pal.Border).TextColor(c.Theme().TextMuted)
							}
						})
						ui.Text(c, ti.Description).FontSize(11.5).TextColor(c.Theme().TextMuted)
					})
				}
			})
		})
	})
}

// settingsSkills is the skills tab: every skill discovery found for the
// active project, with where it came from.
func settingsSkills(c *ui.Context, vm *SettingsVM) {
	t := c.Theme()
	ui.Column(c).Grow(1).Children(func() {
		ui.Scroll(c).Grow(1).Children(func() {
			ui.Column(c).FillWidth().Padding(20, 24, 24).Gap(10).Children(func() {
				ui.Text(c, "Skills").FontSize(16).Bold()
				ui.Text(c, "Discovered for the active project (.agents/skills up the tree, plus your user directories). The system prompt advertises names and descriptions; agents load the full skill on demand. An agent's form narrows these to an allow list.").FontSize(12).TextColor(c.Theme().TextMuted)
				if len(vm.Skills) == 0 {
					ui.Text(c, "No skills discovered. Put a SKILL.md directory under .agents/skills/ in the project (or ~/.agents/skills/).").FontSize(12).TextColor(c.Theme().TextMuted)
				}
				for _, sk := range vm.Skills {
					card := ui.Column(c).FillWidth().Padding(12, 14).Radius(8).
						Background(vm.Pal.Card).Border(1, vm.Pal.Border).Gap(4)
					card.Children(func() {
						ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
							ui.Text(c, sk.Name).FontSize(13)
							ui.Text(c, sk.Source).FontSize(10).Padding(1, 7).Radius(999).
								Background(vm.Pal.Bg).Border(1, vm.Pal.Border).TextColor(t.TextMuted)
						})
						desc := sk.Description
						if desc == "" {
							desc = "(no description)"
						}
						ui.Text(c, desc).FontSize(11.5).TextColor(t.TextMuted)
					})
				}
			})
		})
	})
}

func emptyPane(c *ui.Context, title, sub string) {
	ui.Column(c).Fill().Center().Gap(8).Children(func() {
		ui.Text(c, title).FontSize(13).TextColor(c.Theme().TextMuted)
		ui.Text(c, sub).FontSize(12).TextColor(c.Theme().TextMuted)
	})
}

// settingsForm is the editable half of the form for a non-codex
// provider: presets, the connection fields, the wire, and the models —
// with the provider's own listing to pick from.
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
	pillRow(c, "API", func() string {
		if p.Wire == harness.WireResponses {
			return harness.WireResponses
		}
		return harness.WireChat
	}, []pillOpt{
		{v: harness.WireChat, label: "Chat Completions"},
		{v: harness.WireResponses, label: "Responses"},
	}, func(next string) { p.Wire = next })
	ui.Column(c).Gap(4).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "Models").FontSize(11.5).FontWeight(600).TextColor(t.TextMuted).Grow(1)
			if p.Fetching {
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					ui.Spinner(c)
					ui.Text(c, "Fetching…").FontSize(11).TextColor(t.TextMuted)
				})
			} else if ui.Button(c, "Fetch available").Clicked() {
				acts.FetchModels(p.ID)
			}
		})
		ui.TokenField(c, &p.Models, nil)
		if p.FetchErr != "" {
			ui.Text(c, "Fetch failed: "+p.FetchErr).FontSize(11).TextColor(c.Theme().Warning)
		} else {
			ui.Text(c, "Enter or comma adds a model; Backspace removes the last.").FontSize(11).TextColor(t.TextMuted)
		}
		// The provider's own listing: click a chip to add it to the
		// model set; chips already in the set read as chosen.
		if len(p.Available) > 0 {
			ui.Row(c).Gap(6).Wrap().Children(func() {
				for _, id := range p.Available {
					have := slices.Contains(p.Models, id)
					chip := ui.ButtonBase(c).Padding(3, 10).Radius(999).Gap(6).Cursor(ui.CursorPointer)
					if have {
						chip.Background(vm.Pal.CardHover)
						chip.Children(func() {
							ui.Text(c, id).Font("monospace").FontSize(11).TextColor(t.TextMuted)
							ui.Icon(c, IconCheck).FontSize(11).TextColor(t.Text)
						})
					} else {
						chip.Border(1, t.Border)
						chip.Children(func() {
							ui.Text(c, id).Font("monospace").FontSize(11).TextColor(t.Text)
							ui.Icon(c, IconPlus).FontSize(11).TextColor(t.TextMuted)
						})
					}
					if chip.Clicked() && !have {
						p.Models = append(p.Models, id)
					}
				}
			})
		}
	})
	// The codex-runtime hint only shows when a codex backend can
	// actually reach this provider — for the built-in loop it is noise.
	if p.RunsAs != "" {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Padding(10, 12).Radius(8).
			Background(vm.Pal.Card).Children(func() {
			ui.Icon(c, IconTerminal).FontSize(13).TextColor(vm.Pal.TextMuted)
			ui.Text(c, p.RunsAs).FontSize(11).TextColor(vm.Pal.TextMuted).Grow(1).MinWidth(0)
		})
	}
	ui.Row(c).Justify(ui.End).Children(func() {
		if ui.Button(c, "Delete provider").Clicked() {
			acts.RemoveProvider(p.ID)
		}
	})
}

// pillOpt is one choice of a pill row.
type pillOpt struct {
	v     string
	label string
}

// pillRow is the form's single-choice row: one labelled pill per
// option. The choice binds into the snapshot — the host mirrors it
// after the frame; a host action here would be overwritten by that same
// mirror with this frame's stale snapshot. currentFn resolves the
// snapshot's effective current value (empty wire means chat, say).
func pillRow(c *ui.Context, label string, currentFn func() string, opts []pillOpt, set func(string)) {
	t := c.Theme()
	ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
		ui.Text(c, label).FontSize(11.5).FontWeight(600).TextColor(t.TextMuted)
		current := currentFn()
		for _, o := range opts {
			on := current == o.v
			pill := ui.ButtonBase(c).Padding(4, 10).Radius(999)
			if on {
				pill.Background(t.Text)
				pill.Children(func() { ui.Text(c, o.label).FontSize(11.5).TextColor(t.AccentText) })
			} else {
				pill.Border(1, t.Border)
				pill.Children(func() { ui.Text(c, o.label).FontSize(11.5).TextColor(t.TextMuted) })
			}
			if pill.Clicked() {
				set(o.v)
			}
		}
	})
}

// pillOptInt is one choice of an integer pill row.
type pillOptInt struct {
	v     int
	label string
}

// pillRowInt is pillRow for the offset integer selectors (effort, mode):
// the snapshot value and the option value share the same scale.
func pillRowInt(c *ui.Context, label string, current *int, opts []pillOptInt) {
	t := c.Theme()
	ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
		ui.Text(c, label).FontSize(11.5).FontWeight(600).TextColor(t.TextMuted)
		for _, o := range opts {
			on := *current == o.v
			pill := ui.ButtonBase(c).Padding(4, 10).Radius(999)
			if on {
				pill.Background(t.Text)
				pill.Children(func() { ui.Text(c, o.label).FontSize(11.5).TextColor(t.AccentText) })
			} else {
				pill.Border(1, t.Border)
				pill.Children(func() { ui.Text(c, o.label).FontSize(11.5).TextColor(t.TextMuted) })
			}
			if pill.Clicked() {
				*current = o.v
			}
		}
	})
}

func pillOptsFromProviders(ps []ProviderVM) []pillOpt {
	out := make([]pillOpt, 0, len(ps))
	for _, p := range ps {
		out = append(out, pillOpt{v: p.ID, label: p.Name})
	}
	return out
}

// modelChips is the agent's model row: one wrap chip per model of the
// resolved provider (plus "App default"), single-select, bound into the
// snapshot like every value field.
func modelChips(c *ui.Context, label string, current *string, models []string) {
	t := c.Theme()
	ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
		ui.Text(c, label).FontSize(11.5).FontWeight(600).TextColor(t.TextMuted)
	})
	ui.Row(c).Gap(6).Wrap().Children(func() {
		chip := ui.ButtonBase(c).Padding(3, 10).Radius(999)
		if *current == "" {
			chip.Background(t.Text)
			chip.Children(func() { ui.Text(c, "App default").FontSize(11.5).TextColor(t.AccentText) })
		} else {
			chip.Border(1, t.Border)
			chip.Children(func() { ui.Text(c, "App default").FontSize(11.5).TextColor(t.TextMuted) })
		}
		if chip.Clicked() {
			*current = ""
		}
		for _, m := range models {
			m := m
			on := *current == m
			mc := ui.ButtonBase(c).Padding(3, 10).Radius(999).Gap(6)
			if on {
				mc.Background(t.Text)
				mc.Children(func() {
					ui.Text(c, m).Font("monospace").FontSize(11).TextColor(t.AccentText)
					ui.Icon(c, IconCheck).FontSize(11).TextColor(t.AccentText)
				})
			} else {
				mc.Border(1, t.Border)
				mc.Children(func() { ui.Text(c, m).Font("monospace").FontSize(11).TextColor(t.TextMuted) })
			}
			if mc.Clicked() {
				*current = m
			}
		}
	})
}

// formField is one labeled input of the form; the host saves the
// mirrored value after the frame.
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

// formArea is a labeled multi-line input.
func formArea(c *ui.Context, label string, value *string, hint string) {
	t := c.Theme()
	ui.Column(c).Gap(4).Children(func() {
		ui.Text(c, label).FontSize(11.5).FontWeight(600).TextColor(t.TextMuted)
		ui.TextAreaBase(c, value).FontSize(13).MinHeight(72)
		if hint != "" {
			ui.Text(c, hint).FontSize(11).TextColor(t.TextMuted)
		}
	})
}

// mcpSection is the MCP servers block: each configured server spawns at
// run time and its tools join the agent's tool set as
// mcp_<server>_<tool>.
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

// toggleInList adds name to the list when on removes it when off — and
// unwinds to nil when the list comes to cover everything, because an
// empty list is the config's "all of them / discover as usual".
func toggleInList(list []string, all []string, name string, on bool) []string {
	has := slices.Contains(list, name)
	if on && !has {
		out := append(slices.Clone(list), name)
		slices.Sort(out)
		if slices.Equal(out, all) {
			return nil
		}
		return out
	}
	if !on && has {
		out := slices.DeleteFunc(slices.Clone(list), func(s string) bool { return s == name })
		if len(out) == 0 {
			return nil
		}
		return out
	}
	return list
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

	sectionLabel(c, "MODEL")
	pillRow(c, "Backend", func() string { return ag.Backend }, []pillOpt{
		{v: "", label: "App default"},
		{v: "builtin", label: "Built-in"},
		{v: "claude", label: "Claude Code"},
		{v: "codex", label: "Codex CLI"},
		{v: "pi", label: "Pi"},
	}, func(next string) { ag.Backend = next })
	switch ag.Backend {
	case "claude", "codex":
		// A pinned CLI backend signs in with the CLI's own account and
		// picks from its own model table — no provider involved.
		ui.Textf(c, "Runs the %s binary with its own sign-in; no provider needed.", strings.Title(ag.Backend)).FontSize(11).TextColor(t.TextMuted)
	case "pi":
		ui.Text(c, "Pi resolves models from its own configuration; nothing to pick here.").FontSize(11).TextColor(t.TextMuted)
	default:
		// builtin (or following the app): pick the provider, then the
		// model from that provider's list.
		opts := append([]pillOpt{{v: "", label: "App default"}}, pillOptsFromProviders(ag.ProviderOpts)...)
		pillRow(c, "Provider", func() string { return ag.Provider }, opts, func(next string) { ag.Provider = next })
	}
	modelChips(c, "Model", &ag.Model, ag.ModelOptions)

	sectionLabel(c, "BEHAVIOR")
	pillRowInt(c, "Effort", &ag.Effort, []pillOptInt{
		{v: 0, label: "App default"}, {v: 1, label: "Low"}, {v: 2, label: "Medium"}, {v: 3, label: "High"},
	})
	pillRowInt(c, "Mode", &ag.Mode, []pillOptInt{
		{v: 0, label: "App default"}, {v: 1, label: "Read Only"}, {v: 2, label: "Agent"}, {v: 3, label: "Full Access"},
	})
	formField(c, "Max turns", &ag.MaxTurns, false)

	sectionLabel(c, "INSTRUCTIONS")
	formArea(c, "System prompt (appended)", &ag.SystemPrompt,
		"Joined after the built-in prompt on every turn of this agent.")

	sectionLabel(c, "TOOLS")
	ui.Text(c, "Unchecked tools are removed from this agent's registry.").FontSize(11).TextColor(t.TextMuted)
	for _, ti := range vm.Catalog {
		enabled := !slices.Contains(ag.ToolsDisabled, ti.Name)
		toolCheckbox(c, vm.Pal, ti.Name, ti.Description, enabled, func(on bool) {
			if on {
				ag.ToolsDisabled = slices.DeleteFunc(ag.ToolsDisabled, func(s string) bool { return s == ti.Name })
			} else {
				ag.ToolsDisabled = append(ag.ToolsDisabled, ti.Name)
			}
		})
	}

	sectionLabel(c, "MCP SERVERS")
	ui.Text(c, "Unchecked servers stay unmounted for this agent; all checked (or none touched) mounts everything.").FontSize(11).TextColor(t.TextMuted)
	allNames := make([]string, 0, len(vm.MCPServer))
	for i := range vm.MCPServer {
		allNames = append(allNames, vm.MCPServer[i].Name)
	}
	for _, name := range allNames {
		on := ag.MCPServers == nil || slices.Contains(ag.MCPServers, name)
		mcpCheckbox(c, vm.Pal, name, on, func(on bool) {
			ag.MCPServers = toggleInList(ag.MCPServers, allNames, name, on)
		})
	}

	sectionLabel(c, "SKILLS")
	ui.Text(c, "Checked skills are this agent's allow list; leave all checked (or none touched) to discover as usual.").FontSize(11).TextColor(t.TextMuted)
	for _, sk := range vm.Skills {
		on := ag.SkillsAllow == nil || slices.Contains(ag.SkillsAllow, sk.Name)
		skillCheckbox(c, vm.Pal, sk.Name, sk.Description, sk.Source, on, func(on bool) {
			all := make([]string, 0, len(vm.Skills))
			for _, s := range vm.Skills {
				all = append(all, s.Name)
			}
			ag.SkillsAllow = toggleInList(ag.SkillsAllow, all, sk.Name, on)
		})
	}

	sectionLabel(c, "GROUP RELAY")
	ui.Column(c).Gap(4).Children(func() {
		ui.Text(c, "Panel members").FontSize(11.5).FontWeight(600).TextColor(t.TextMuted)
		ui.TokenField(c, &ag.Panel, nil)
		ui.Textf(c, "Agent names, in answer order (%s): each member sees the earlier replies in the shared conversation. Available: %s",
			"this agent is excluded", vm.AgentNames).FontSize(11).TextColor(t.TextMuted)
	})

	ui.Row(c).Justify(ui.End).Children(func() {
		if ui.Button(c, "Delete agent").Clicked() {
			acts.RemoveAgent(ag.ID)
		}
	})
	_ = t
}

// sectionLabel is a form section divider.
func sectionLabel(c *ui.Context, label string) {
	ui.Box(c).Height(1).Margin(6, 0, 0).Background(c.Theme().Border)
	ui.Text(c, label).FontSize(10.5).FontWeight(600).TextColor(c.Theme().TextMuted).
		Margin(8, 0, 0).LetterSpacing(0.6)
}

// toolCheckbox is one registry row of the agent form: checked means the
// tool is IN this agent's set.
func toolCheckbox(c *ui.Context, pal Palette, name, desc string, enabled bool, apply func(on bool)) {
	checked, was := enabled, enabled
	checkboxRow(c, pal, name, desc, "", &checked)
	if checked != was {
		apply(checked)
	}
}

// mcpCheckbox is one configured server row: checked means mounted.
func mcpCheckbox(c *ui.Context, pal Palette, name string, on bool, apply func(on bool)) {
	checked, was := on, on
	checkboxRow(c, pal, name, "", "", &checked)
	if checked != was {
		apply(checked)
	}
}

// skillCheckbox is one discovered skill row: checked means allowed.
func skillCheckbox(c *ui.Context, pal Palette, name, desc, source string, on bool, apply func(on bool)) {
	checked, was := on, on
	checkboxRow(c, pal, name, desc, source, &checked)
	if checked != was {
		apply(checked)
	}
}

// checkboxRow is one row with a live checkbox: checked flips in-frame,
// and the change is applied to the snapshot through apply immediately.
func checkboxRow(c *ui.Context, pal Palette, title, sub, badge string, checked *bool) {
	t := c.Theme()
	row := ui.Row(c).Gap(10).AlignItems(ui.Center).Padding(8, 12).Radius(8).
		Background(t.Surface)
	row.Children(func() {
		ui.Checkbox(c, checked, "")
		ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, title).FontSize(12.5)
				if badge != "" {
					ui.Text(c, badge).FontSize(10).Padding(1, 7).Radius(999).
						Background(pal.Bg).Border(1, t.Border).TextColor(t.TextMuted)
				}
			})
			if sub != "" {
				ui.Text(c, sub).FontSize(11).TextColor(t.TextMuted)
			}
		})
	})
}
