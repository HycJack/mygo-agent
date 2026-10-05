package harness

import (
	"strings"
)

// DiffLine is one line of a diff: Kind is '+', '-' or ' ' for context.
// The app's renderer adds hunk headers and word-level marks on top.
type DiffLine struct {
	Kind   byte
	Text   string
	Number int
	MarkLo int
	MarkHi int
}

// unifiedDiff renders the change from old to new text as colored diff
// lines: common head and tail become context, the middle is diffed
// with a line-based longest common subsequence.
func UnifiedDiff(oldText, newText string) []DiffLine {
	a := strings.Split(strings.TrimRight(oldText, "\n"), "\n")
	b := strings.Split(strings.TrimRight(newText, "\n"), "\n")

	var out []DiffLine
	i := 0
	for i < len(a) && i < len(b) && a[i] == b[i] {
		out = append(out, DiffLine{Kind: ' ', Text: a[i]})
		i++
	}
	a, b = a[i:], b[i:]

	tail := 0
	for tail < len(a) && tail < len(b) && a[len(a)-1-tail] == b[len(b)-1-tail] {
		tail++
	}
	midA, midB := a[:len(a)-tail], b[:len(b)-tail]

	out = append(out, LcsDiff(midA, midB)...)
	for k := len(a) - tail; k < len(a); k++ {
		out = append(out, DiffLine{Kind: ' ', Text: a[k]})
	}
	return out
}

// lcsDiff diffs two line slices with the classic DP, bounded: past the
// cap it degrades to a wholesale replacement.
func LcsDiff(a, b []string) []DiffLine {
	const maxLines = 1200
	if len(a) > maxLines || len(b) > maxLines {
		var out []DiffLine
		for _, l := range a {
			out = append(out, DiffLine{Kind: '-', Text: l})
		}
		for _, l := range b {
			out = append(out, DiffLine{Kind: '+', Text: l})
		}
		return out
	}
	// lcs[i][j] = length of the LCS of a[i:] and b[j:].
	lcs := make([][]int, len(a)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			switch {
			case a[i] == b[j]:
				lcs[i][j] = lcs[i+1][j+1] + 1
			case lcs[i+1][j] >= lcs[i][j+1]:
				lcs[i][j] = lcs[i+1][j]
			default:
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}
	var out []DiffLine
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			out = append(out, DiffLine{Kind: ' ', Text: a[i]})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			out = append(out, DiffLine{Kind: '-', Text: a[i]})
			i++
		default:
			out = append(out, DiffLine{Kind: '+', Text: b[j]})
			j++
		}
	}
	for ; i < len(a); i++ {
		out = append(out, DiffLine{Kind: '-', Text: a[i]})
	}
	for ; j < len(b); j++ {
		out = append(out, DiffLine{Kind: '+', Text: b[j]})
	}
	return out
}
