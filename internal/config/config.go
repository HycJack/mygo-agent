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
