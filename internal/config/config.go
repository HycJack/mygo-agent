// Package config owns the persisted configuration: projects, model
// providers with their keys, MCP servers, and the agent's defaults.
// It is plain JSON, written atomically, and it migrates older shapes
// on read.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Project is a working directory the tasks run in.
type Project struct {
	ID   string `json:"id"`
	Path string `json:"path"`
}

// Provider is one model vendor: an OpenAI-compatible endpoint with its
// key and the models it serves. The built-in "codex" and "claude"
// providers instead use whatever those CLIs are signed in with.
type Provider struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	BaseURL string   `json:"base_url,omitempty"`
	APIKey  string   `json:"api_key,omitempty"`
	Models  []string `json:"models,omitempty"`
	// Wire selects the request shape: "chat" (default, chat
	// completions) or "responses" (the Responses API the codex and
	// OpenAI models use).
	Wire string `json:"wire,omitempty"`
	// ContextWindow is the provider's context window in tokens. Zero
	// leaves token accounting off; a declared window arms the built-in
	// agent's watermark: near the top of it the transcript is
	// summarised instead of dropped (spec/architecture.md, tool
	// output budgets).
	ContextWindow int `json:"context_window,omitempty"`
}

// MCPServer is one Model Context Protocol server: a command the app
// spawns and speaks JSON-RPC to over stdio, or a streamable HTTP
// endpoint (URL set, no command).
type MCPServer struct {
	Name    string   `json:"name"`
	Command string   `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`
	Env     []string `json:"env,omitempty"`
	URL     string   `json:"url,omitempty"`
}

// Version is the config.json schema version this binary writes
// (spec/data.md). Load refuses files from newer schemas; version 1
// files still load and the host migrates them (the agents block is
// synthesized from the app-level defaults).
const Version = 2

// Agent is a configured agent profile (spec/agents.md): which backend
// runs it, which model it uses, which tools, MCP servers and skills it
// gets, and how it is allowed to act. A profile, not a running thing —
// a session binds to one and the host assembles its turns from it.
//
// Empty fields inherit the app-level selection (the provider, model,
// effort, mode and backend the composer shows), so the zero Agent
// behaves exactly like the pre-agents app.
type Agent struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Emoji string `json:"emoji,omitempty"`
	// Backend runs this agent: builtin | codex | claude | pi. Empty
	// follows the app's backend switch.
	Backend string `json:"backend,omitempty"`
	// Provider is a provider id and Model one of its models; either
	// empty follows the app's selection.
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	// Effort and Mode are the reasoning effort (0 low, 1 medium, 2 high)
	// and the default approval mode (0 read-only, 1 agent, 2 full). nil
	// follows the app's selection.
	Effort *int `json:"effort,omitempty"`
	Mode   *int `json:"mode,omitempty"`
	// MaxTurns is the tool-round budget; zero follows the app's.
	MaxTurns     int    `json:"max_turns,omitzero"`
	SystemPrompt string `json:"system_prompt,omitempty"`
	// Tools disables built-in tools by name and adds per-tool permission
	// rules on top of the global ones (spec/permissions.md).
	Tools AgentTools `json:"tools,omitempty"`
	// MCPServers names the servers this agent mounts; empty mounts all
	// of them (the app-level list merged with the project's .mcp.json).
	MCPServers []string `json:"mcp_servers,omitempty"`
	// Panel turns this agent's threads into a group relay (spec/agents.md):
	// the named agents answer in order, each seeing the earlier members'
	// replies in the shared conversation. Names, not ids — the same
	// convention mcp_servers uses. Empty means a solo agent.
	Panel []string `json:"panel,omitempty"`
	// PanelRoute selects how the relay picks the next speaker
	// (spec/relay-router.md): "" or "sequence" keeps the configured order
	// and each member speaks once (the pre-router behavior); "router"
	// asks a coordinator model after every reply which member speaks
	// next — or that the relay is done.
	PanelRoute string `json:"panel_route,omitempty"`
	// PanelMaxRounds caps how many member replies one user turn may
	// dispatch in router mode; zero defaults to 8. Sequence mode ignores
	// it — a sequence relay is exactly one reply per member.
	PanelMaxRounds int `json:"panel_max_rounds,omitzero"`
	// PanelStallRounds caps consecutive replies from the same member
	// before the relay is force-ended (two agents complimenting each
	// other in a loop); zero defaults to 3.
	PanelStallRounds int `json:"panel_stall_rounds,omitzero"`
	// PanelMaxTokens is the routed relay's total token budget across all
	// its members within one user turn; zero disables the budget.
	PanelMaxTokens int `json:"panel_max_tokens,omitzero"`
	// PanelTimeout bounds the whole routed relay in wall-clock seconds;
	// zero disables it. A hung member is already bounded by the
	// per-request timeouts; this bounds the relay as a whole.
	PanelTimeout int `json:"panel_timeout,omitzero"`
	// PanelSummarizer names the panel member who writes the final
	// wrap-up when a routed relay ends; empty means the thread's own
	// agent. A name, like panel.
	PanelSummarizer string `json:"panel_summarizer,omitempty"`
	// PanelBlurb is the duty line the routing roster reads when deciding
	// who speaks next — the coordinator's choice is made from it, one
	// line per member. Empty falls back to the head of system_prompt,
	// keeping routing and persona in one string.
	PanelBlurb string `json:"panel_blurb,omitempty"`
	// RouterProvider and RouterModel pick the coordinator model of a
	// router relay — a cheap local model is the point. Either empty
	// follows the app's selection.
	RouterProvider string `json:"router_provider,omitempty"`
	RouterModel    string `json:"router_model,omitempty"`
	// RouterWire selects the coordinator's request shape (spec/
	// relay-router.md): "" or "chat" is one chat-completions model with
	// a JSON-reply prompt; "decision" is the Jev decision API (Ollama's
	// /v1/systemone, the tev1 class of models) — a choice question over
	// the panel plus a noul question for ending, constrained and scored;
	// "hybrid" is both: a big chat model (RouterProvider/RouterModel)
	// reads the full transcript and writes a situation brief, then the
	// decision model (RouterJudge*, falling back to Router*) makes the
	// final call on it — comprehension from the big model, a calibrated
	// constrained verdict from the decision model.
	RouterWire string `json:"router_wire,omitempty"`
	// RouterJudgeProvider and RouterJudgeModel pick the decision model
	// of the decision and hybrid wires; both empty fall back to
	// RouterProvider/RouterModel (a pure-decision config), then the
	// app's selection.
	RouterJudgeProvider string `json:"router_judge_provider,omitempty"`
	RouterJudgeModel    string `json:"router_judge_model,omitempty"`
	// Skills narrows the discovered skills.
	Skills AgentSkills `json:"skills,omitempty"`
}

// AgentTools is the tool half of an Agent profile.
type AgentTools struct {
	// Disabled names built-in tools the agent does not get (the registry
	// is the six in spec/permissions.md; MCP tools are governed by the
	// server list instead).
	Disabled []string `json:"disabled,omitempty"`
	// Rules are selector rules layered over the global ones — the more
	// specific agent rule wins (spec/permissions.md, selector rules).
	Rules map[string]string `json:"rules,omitempty"`
}

// AgentSkills narrows which skills an agent sees.
type AgentSkills struct {
	// Mode: "" or "project"/"all" discovers as usual; "custom" applies
	// the allow/deny lists.
	Mode  string   `json:"mode,omitempty"`
	Allow []string `json:"allow,omitempty"`
	Deny  []string `json:"deny,omitempty"`
}

// Config is the on-disk shape of config.json.
type Config struct {
	Version       int         `json:"version"`
	Projects      []Project   `json:"projects"`
	ActiveProject string      `json:"active_project"`
	Providers     []Provider  `json:"providers"`
	Provider      string      `json:"provider"`
	Model         string      `json:"model"`
	Effort        int         `json:"effort"`
	Backend       string      `json:"backend"`
	MCPServers    []MCPServer `json:"mcp_servers,omitempty"`
	Permissions   Permissions `json:"permissions,omitempty"`
	MaxTurns      int         `json:"max_turns,omitzero"`
	// Agents are the configured agent profiles and DefaultAgent names
	// the one new tasks bind to when nothing else chose (spec/agents.md).
	Agents       []Agent `json:"agents,omitempty"`
	DefaultAgent string  `json:"default_agent,omitempty"`
	// CustomModels is the pre-providers field, read only to migrate it.
	CustomModels []string `json:"custom_models,omitempty"`
}

// Permissions is the host-configured selector override block.
type Permissions struct {
	Rules map[string]string `json:"rules,omitempty"`
}

// ErrUnsupportedVersion reports a file written by a newer schema. The
// caller must not overwrite it (spec/data.md).
var ErrUnsupportedVersion = errors.New("file was written by a newer version")

// Load reads and parses the config file strictly: unknown fields are
// errors, so a typo in a hand-edited file cannot silently zero a
// setting. A file from a newer schema returns ErrUnsupportedVersion.
func Load(path string) (Config, error) {
	var cfg Config
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, err
	}
	if cfg.Version > Version {
		return Config{}, fmt.Errorf("%w (%d > %d)", ErrUnsupportedVersion, cfg.Version, Version)
	}
	return cfg, nil
}

// Save writes the config atomically (temp file + rename) with 0600 —
// provider API keys live here. The schema version is stamped here so a
// caller cannot forget it.
func Save(path string, cfg Config) error {
	cfg.Version = Version
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return WriteFile(path, data, 0o600)
}

// WriteFile writes data through a temp file and a rename, so a crash
// mid-write never leaves a truncated file behind.
func WriteFile(path string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
