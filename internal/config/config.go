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
// spawns and speaks JSON-RPC to over stdio.
type MCPServer struct {
	Name    string   `json:"name"`
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
	Env     []string `json:"env,omitempty"`
}

// Version is the config.json schema version this binary writes
// (spec/data.md). Load refuses files from newer schemas.
const Version = 1

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
