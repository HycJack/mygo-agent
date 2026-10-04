// Package config owns the persisted configuration: projects, model
// providers with their keys, MCP servers, and the agent's defaults.
// It is plain JSON, written atomically, and it migrates older shapes
// on read.
package config

import (
	"encoding/json"
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
}

// MCPServer is one Model Context Protocol server: a command the app
// spawns and speaks JSON-RPC to over stdio.
type MCPServer struct {
	Name    string   `json:"name"`
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
	Env     []string `json:"env,omitempty"`
}

// Config is the on-disk shape of config.json.
type Config struct {
	Projects      []Project   `json:"projects"`
	ActiveProject string      `json:"active_project"`
	Providers     []Provider  `json:"providers"`
	Provider      string      `json:"provider"`
	Model         string      `json:"model"`
	Effort        int         `json:"effort"`
	Backend       string      `json:"backend"`
	MCPServers    []MCPServer `json:"mcp_servers,omitempty"`
	MaxTurns      int         `json:"max_turns,omitempty"`
	// CustomModels is the pre-providers field, read only to migrate it.
	CustomModels []string `json:"custom_models,omitempty"`
}

// Load reads and parses the config file. A missing or broken file is
// not an error: the zero Config is returned and defaults apply.
func Load(path string) (Config, error) {
	var cfg Config
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	err = json.Unmarshal(data, &cfg)
	return cfg, err
}

// Save writes the config atomically (temp file + rename) with 0600 —
// provider API keys live here.
func Save(path string, cfg Config) error {
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
