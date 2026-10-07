package ui

// The composer's @-mention candidates (spec/relay-router.md): typing "@"
// opens the panel roster, filtered by what follows the at-sign. The
// trigger is the draft's LAST unclosed @ — an at-sign with no whitespace
// after it — because the toolkit does not expose the caret; typing at
// the end (the overwhelmingly common case) behaves exactly like a
// caret-driven popup.

import "strings"

// MentionQuery returns the name prefix being typed after the draft's
// last unclosed @, and whether the popup should show at all: an @ with
// whitespace after it (or no @) is not a mention in progress.
func MentionQuery(draft string) (string, bool) {
	at := strings.LastIndex(draft, "@")
	if at < 0 {
		return "", false
	}
	tail := draft[at+1:]
	if strings.ContainsAny(tail, " \t\n") {
		return "", false // whitespace after the @: that mention is done
	}
	return tail, true
}

// MentionCandidates filters the roster names by the query: prefix
// match, case-insensitive, at most six rows.
func MentionCandidates(query string, names []string) []string {
	if !strings.ContainsFunc(query, func(r rune) bool { return r != ' ' }) && query != "" {
		return nil
	}
	q := strings.ToLower(strings.TrimSpace(query))
	var out []string
	for _, name := range names {
		if q == "" || strings.HasPrefix(strings.ToLower(name), q) {
			out = append(out, name)
			if len(out) == 6 {
				break
			}
		}
	}
	return out
}

// ApplyMention replaces the draft's trailing "@query" with "@name " and
// returns the new draft. no-op when there is nothing to replace.
func ApplyMention(draft, name string) string {
	at := strings.LastIndex(draft, "@")
	if at < 0 {
		return draft
	}
	return draft[:at] + "@" + name + " "
}
