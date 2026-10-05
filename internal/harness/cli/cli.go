// Package cli holds the pieces every CLI harness adapter shares: the
// process-group kill guard and a few display helpers. Adapters live in
// sibling packages (claude, codex, pi) and import this one plus the
// harness root — never the host.
package cli

import (
	"encoding/json"
	"slices"
	"strings"
)

// ProcGroupAttr guards one CLI process: it runs in its own process
// group and a stop becomes a group SIGTERM (escalating per the exec
// package's WaitDelay), so the CLI and any tool processes it spawned
// actually die.
func ProcGroupAttr(cmd *execCmd) { setProcGroup(cmd) }

// Trunc cuts s to about n bytes without splitting a rune, appending an
// ellipsis when it does.
func Trunc(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// ShortArgs summarizes a raw JSON argument object for a card title: the
// string-valued fields joined, or the raw JSON cut short.
func ShortArgs(raw json.RawMessage) string {
	var in map[string]any
	if json.Unmarshal(raw, &in) != nil || in == nil {
		s := string(raw)
		if len(s) > 120 {
			return s[:120] + "…"
		}
		return s
	}
	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	var parts []string
	for _, k := range keys {
		if s, ok := in[k].(string); ok {
			parts = append(parts, s)
		}
	}
	joined := strings.Join(parts, " ")
	if len(joined) > 120 {
		joined = joined[:120] + "…"
	}
	return joined
}

// TrimOutput caps a tool result so one command cannot flood the context.
func TrimOutput(s string, max int) string {
	s = strings.TrimRight(s, "\n")
	if len(s) <= max {
		return s
	}
	return s[:max] + "\n… output truncated …"
}

// ShortSession trims a session id for a summary note.
func ShortSession(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
