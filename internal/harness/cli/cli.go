// Package cli holds the pieces every CLI harness adapter shares: the
// process-group kill guard and a few display helpers. Adapters live in
// sibling packages (claude, codex, pi) and import this one plus the
// harness root — never the host.
package cli

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
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

// CompactedNotice is the one line a backend's own context compaction
// produces. The transcript keeps every turn, but from here on the model no
// longer sees the part that was summarised, so the user has to be told:
// without this a long task silently forgets, and the only symptom is an
// answer that contradicts something it agreed to twenty turns ago.
//
// The counts are optional because only some backends report them, and a
// missing count is no reason to stay quiet about the event itself.
func CompactedNotice(provider string, before, after int) string {
	if before > 0 && after > 0 {
		return fmt.Sprintf("%s compacted its context · %s → %s tokens · earlier turns are summarised, not lost",
			provider, CompactTokens(before), CompactTokens(after))
	}
	return fmt.Sprintf("%s compacted its context · earlier turns are summarised, not lost", provider)
}

// CompactTokens renders a token count at the size a note can carry: exact
// below a thousand, then one decimal while a decimal still carries
// information, then whole thousands. "25.9k → 5.3k" says the shape of a
// compaction; "25876 → 5253" is a wall of digits nobody reads.
func CompactTokens(n int) string {
	switch {
	case n < 1000:
		return strconv.Itoa(n)
	case n < 100000:
		return strconv.FormatFloat(float64(n)/1000, 'f', 1, 64) + "k"
	case n < 1000000:
		return strconv.Itoa(n/1000) + "k"
	default:
		return strconv.FormatFloat(float64(n)/1e6, 'f', 1, 64) + "M"
	}
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
