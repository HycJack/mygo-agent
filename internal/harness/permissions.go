package harness

import (
	"encoding/json"

	"mygo-agent/internal/harness/cli"

	"fmt"
	"strings"
)

// Action is one verb of the closed catalog in spec/permissions.md: what a
// tool does, independent of its name. A tool without a declared action
// fails closed.
type Action string

const (
	ActionFileRead  Action = "file.read"
	ActionFileWrite Action = "file.write"
	ActionShell     Action = "shell.exec"
	ActionSkill     Action = "skill.read"
	ActionMCP       Action = "mcp.call"
	ActionDelegate  Action = "agent.delegate"
)

// Mode is the approval-mode selector, matching the UI's three segments and
// the CLI backends' flag mapping.
type Mode int

const (
	ModeReadOnly Mode = iota // 0
	ModeAgent                // 1
	ModeFull                 // 2
)

// Permission is what the gate decided for one prepared call.
type Permission string

const (
	PermAllow Permission = "allow"
	PermDeny  Permission = "deny"
	PermAsk   Permission = "ask"
)

// Rules are host-configured selector overrides (config.json
// permissions.rules): tool-name selector → permission. Selectors match
// exactly, or by the longest prefix ending in *, or the bare *.
type Rules map[string]Permission

// Policy decides permission for a tool call: the mode's per-action
// defaults, overridden per tool name by Rules.
type Policy struct {
	Mode  Mode
	Rules Rules
}

// Resolve returns the permission for a call to the named tool.
// Selector rules win over the mode default; unknown everything fails
// closed.
func (p Policy) Resolve(toolName string, actions []Action) Permission {
	if perm, ok := p.Rules.match(toolName); ok {
		switch perm {
		case PermAllow, PermDeny, PermAsk:
			return perm
		default:
			// A malformed rule value never widens the gate.
			return PermDeny
		}
	}
	return p.modeDefault(actions)
}

// PermissionFromConfig validates a host-configured rule value. The
// catalog is exactly allow, deny, ask (permissions.md); case and
// surrounding space are tolerated, anything else fails closed to deny —
// a typo like "alow" or "Deny" must not read as permission to run.
func PermissionFromConfig(s string) Permission {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "allow":
		return PermAllow
	case "deny":
		return PermDeny
	case "ask":
		return PermAsk
	default:
		return PermDeny
	}
}

// modeDefault maps every declared action through the mode's defaults and
// returns the strictest result: any denied action denies the call, any
// asking action asks, else allow.
func (p Policy) modeDefault(actions []Action) Permission {
	if len(actions) == 0 {
		return PermDeny
	}
	result := PermAllow
	for _, a := range actions {
		switch p.actionDefault(a) {
		case PermDeny:
			return PermDeny
		case PermAsk:
			result = PermAsk
		}
	}
	return result
}

// actionDefault is the mode's default for one action (spec/permissions.md).
func (p Policy) actionDefault(a Action) Permission {
	switch a {
	case ActionFileRead, ActionSkill:
		return PermAllow
	case ActionFileWrite, ActionShell, ActionDelegate:
		if p.Mode >= ModeAgent {
			return PermAllow
		}
		return PermDeny
	case ActionMCP:
		switch p.Mode {
		case ModeReadOnly:
			return PermDeny
		case ModeAgent:
			return PermAsk
		default:
			return PermAllow
		}
	default:
		return PermDeny
	}
}

// match resolves a selector for a tool name: exact, then the longest
// trailing-* prefix, then the bare *.
func (r Rules) match(tool string) (Permission, bool) {
	if r == nil {
		return "", false
	}
	if p, ok := r[tool]; ok {
		return p, true
	}
	best := -1
	var found Permission
	for sel, perm := range r {
		prefix, ok := strings.CutSuffix(sel, "*")
		if !ok || prefix == "" {
			continue
		}
		if strings.HasPrefix(tool, prefix) && len(prefix) > best {
			best = len(prefix)
			found = perm
		}
	}
	if best >= 0 {
		return found, true
	}
	p, ok := r["*"]
	return p, ok
}

// DenialError is the settled result of a denied call: bounded text the
// model can read and react to, never a crash or a silent no-op.
type DenialError struct {
	Tool   string
	Reason string
}

func (e *DenialError) Error() string {
	if e.Reason == "" {
		return fmt.Sprintf("permission denied: the tool %q is not allowed in the current mode; ask the user to change the approval mode or the permission rules", e.Tool)
	}
	return fmt.Sprintf("permission denied (%s): %s", e.Tool, e.Reason)
}

// ApprovalSummary renders the bounded, redacted one-line presentation of a
// call for an approval prompt: command text, path, or server/tool name —
// never credentials or wholesale argument dumps.
func ApprovalSummary(call ToolCall) string {
	var in map[string]any
	_ = json.Unmarshal([]byte(call.Function.Arguments), &in)
	get := func(k string) string {
		if s, ok := in[k].(string); ok {
			return s
		}
		return ""
	}
	// Redact before truncating: a cut can otherwise hide the tail of a
	// secret behind the ellipsis, and a card is persisted into the
	// thread file, not just drawn.
	red := func(s string, n int) string { return cli.Trunc(cli.Redact(s), n) }
	name := call.Function.Name
	switch {
	case name == "bash":
		return "$ " + red(strings.Join(strings.Fields(get("command")), " "), 160)
	case get("path") != "":
		return name + " " + red(get("path"), 160)
	case get("pattern") != "":
		return name + " " + red(get("pattern"), 120)
	case get("name") != "":
		return name + " " + red(get("name"), 120)
	default:
		if _, ok := strings.CutPrefix(name, "mcp_"); ok {
			// MCP arguments are free-form: a bounded, flattened view so
			// the ask is not a blind yes (spec/approvals.md).
			if args := red(strings.Join(strings.Fields(call.Function.Arguments), " "), 160); args != "" {
				return name + " " + args
			}
		}
		return name
	}
}
