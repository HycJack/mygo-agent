package agent

import (
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
		return perm
	}
	return p.modeDefault(actions)
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
	case ActionFileWrite, ActionShell:
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
