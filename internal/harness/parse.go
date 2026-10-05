package harness

import "strings"

// ParseUnifiedDiff turns a unified diff into colored lines, keeping
// hunk headers as '@' lines and marking the word-level change of each
// replaced pair of lines.
func ParseUnifiedDiff(diff string) []DiffLine {
	var lines []DiffLine
	for _, line := range strings.Split(strings.TrimRight(diff, "\n"), "\n") {
		switch {
		case strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---") ||
			strings.HasPrefix(line, "diff ") || strings.HasPrefix(line, "index "):
			continue
		case strings.HasPrefix(line, "@@"):
			lines = append(lines, DiffLine{Kind: '@', Text: line})
		default:
			kind := byte(' ')
			if len(line) > 0 {
				switch line[0] {
				case '+', '-', ' ':
					kind = line[0]
					line = line[1:]
				}
			}
			lines = append(lines, DiffLine{Kind: kind, Text: line})
		}
	}
	MarkWordDiff(lines)
	return lines
}

// MarkWordDiff pairs each run of removed lines with the run of added
// lines that follows and marks the changed middle of each pair, so the
// renderer can highlight just what changed, as godiff does.
func MarkWordDiff(lines []DiffLine) {
	for i := 0; i < len(lines); {
		if lines[i].Kind != '-' {
			i++
			continue
		}
		j := i
		for j < len(lines) && lines[j].Kind == '-' {
			j++
		}
		k := j
		for k < len(lines) && lines[k].Kind == '+' {
			k++
		}
		for n := 0; i+n < j && j+n < k; n++ {
			markPair(&lines[i+n], &lines[j+n])
		}
		i = k
	}
}

// markPair marks the changed middle shared by a removed and an added
// line: the part left after their common prefix and suffix.
func markPair(del, add *DiffLine) {
	d, a := []rune(del.Text), []rune(add.Text)
	prefix := 0
	for prefix < len(d) && prefix < len(a) && d[prefix] == a[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(d)-prefix && suffix < len(a)-prefix &&
		d[len(d)-1-suffix] == a[len(a)-1-suffix] {
		suffix++
	}
	delMid, addMid := len(d)-prefix-suffix, len(a)-prefix-suffix
	if delMid == 0 && addMid == 0 {
		return
	}
	// Lines rewritten wholesale get no word mark; the row tint is enough.
	if delMid > 120 || addMid > 120 {
		return
	}
	// With too little shared text the mark would cover the whole line.
	if (prefix+suffix)*4 < len(d)+len(a) {
		return
	}
	del.MarkLo, del.MarkHi = prefix, len(d)-suffix
	add.MarkLo, add.MarkHi = prefix, len(a)-suffix
}

// diffStats counts the added and removed lines of a diff.
func DiffStats(lines []DiffLine) (add, del int) {
	for _, l := range lines {
		switch l.Kind {
		case '+':
			add++
		case '-':
			del++
		}
	}
	return add, del
}
