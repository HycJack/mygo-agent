// Package cli holds the pieces every CLI harness adapter shares: the
// process-group kill guard and a few display helpers. Adapters live in
// sibling packages (claude, codex, pi) and import this one plus the
// harness root — never the host.
package cli

import (
	"encoding/json"
	"regexp"
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
//
// The carriage return goes with the trailing newline: a Windows command
// ends its line with CRLF, so trimming only the \n left a \r inside the
// stored card — a stray character on screen, and a mismatch against the
// command's actual words for anything that compares them.
func TrimOutput(s string, max int) string {
	s = strings.TrimRight(s, "\r\n")
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

// Redact masks credential-shaped text before it reaches a card, a
// transcript or a log. Truncation bounds a line but does not hide a
// secret in it: `curl -H "Authorization: Bearer sk-…"` is exactly the
// case a user must be able to read enough of to approve, and exactly the
// case that must not put a live token on screen or into persisted JSON
// (spec/approvals.md: the ask is bounded and redacted).
//
// The patterns are deliberately narrow — a header or assignment whose key
// names a secret, plus the well-known literal token prefixes. A broad
// "anything long" rule would redact ordinary long paths and hashes and
// make the ask unreadable, which is its own safety problem.
func Redact(s string) string {
	for _, p := range secretPatterns {
		s = p.re.ReplaceAllString(s, p.repl)
	}
	return s
}

type redaction struct {
	re   *regexp.Regexp
	repl string
}

// secretPatterns match a key that names a credential followed by its
// value, plus the well-known literal token prefixes. Each rule keeps the
// key visible and masks only the value, so the user can still tell which
// secret a call would use.
//
// The patterns are deliberately narrow — a header or assignment whose key
// names a secret, plus the well-known literal token prefixes. A broad
// "anything long" rule would redact ordinary long paths and hashes and
// make the ask unreadable, which is its own safety problem.
var secretPatterns = []redaction{
	// Authorization: Bearer <token>  (and Proxy-Authorization)
	{regexp.MustCompile(`(?i)((?:proxy-)?authorization\s*[:=]\s*)(?:bearer\s+|basic\s+|token\s+)?\S+`), "${1}«redacted»"},
	// --api-key X, --token=X, --password X
	{regexp.MustCompile(`(?i)(--?(?:api[-_]?key|token|password|passwd|secret|access[-_]?key)\s*[:=]?\s*)\S+`), "${1}«redacted»"},
	// API_KEY=…, GITHUB_TOKEN: …, AWS_SECRET_ACCESS_KEY=…
	{regexp.MustCompile(`(?i)(\b[A-Z0-9_]*(?:api[-_]?key|token|password|passwd|secret|access[-_]?key)\b\s*[:=]\s*)("?)\S+`), "${1}${2}«redacted»"},
	// Well-known literal token formats, anywhere. Only the vendor marker
	// is kept — for these formats the "prefix" IS the credential, so
	// showing the match would defeat the point. The marker still tells the
	// user which secret a call would use.
	{regexp.MustCompile(`\b(sk-|ghp_|gho_|github_pat_|xox[baprs]-|AKIA|AIza|eyJ)[A-Za-z0-9_.\-]{8,}`), "${1}«redacted»"},
}
