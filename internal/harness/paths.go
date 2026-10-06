package harness

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
)

// Deciding which directories a prompt is reaching for.
//
// The scan runs on the prompt alone, before the CLI starts, because
// granting means putting directories on the command line. Two failures
// are possible and both matter: a path that was not noticed leaves the
// user with a refusal they never got to answer, and a path wrongly
// noticed asks about something they never asked for. The rules below
// are therefore deliberately narrow — a path has to look like a real
// filesystem location to be considered at all.

// pathInText matches an absolute path as it appears in prose or in
// code: a slash that can start a path — the beginning of the text,
// whitespace, an opening quote or bracket — followed by whole path
// segments. The whole-segment shape is what makes every match a
// directory: "/tmp/a.txt" matches only "/tmp/", so the grant is the
// file's directory, and a name with no leading boundary ("3/4",
// "a/b/c") never matches at all. A bare "/" matches too, and the scan
// below decides whether it is the root being named or an arithmetic
// separator.
var pathInText = regexp.MustCompile(`(?:^|[\s"'` + "`" + `(=\[])/(?:[A-Za-z0-9_.~-]+/)*`)

// homeRelative matches ~/... , the other way a path is written in a
// prompt that a person is thinking about their own machine. It is
// matched separately and removed from the text before the absolute
// scan, or "~/notes/todo.md" would also be read as the absolute path
// "/notes/todo.md" — a second, wrong grant for a directory that may not
// exist.
var homeRelative = regexp.MustCompile(`~(?:/[A-Za-z0-9_.~-]+)*/?`)

// DirsOutsideWorkdir is dirsOutsideWorkdir, for adapters that run in
// another package: which directories a prompt reaches for is a property
// of the prompt and the workspace, not of any one CLI.
func DirsOutsideWorkdir(text, workdir string) []string {
	return dirsOutsideWorkdir(text, workdir)
}

// dirsOutsideWorkdir returns the roots outside workdir that text refers
// to, deduplicated and sorted, each narrowed to the shallowest
// directory that still covers what was named.
//
// Narrowing is the point. A prompt that says "look at
// ~/src/project/main.go" needs ~/src/project, not ~/src — and certainly
// not $HOME. Keeping the parent would widen the grant to every sibling
// the user never mentioned, which is exactly the kind of silent
// over-reach this scan exists to prevent.
func dirsOutsideWorkdir(text, workdir string) []string {
	if text == "" {
		return nil
	}
	// A Windows path is a different language: it carries a drive letter and
	// is written with backslashes, and pathInText understands neither.
	// Worse, the POSIX paths it *does* match are then placed by filepath.Abs
	// onto whatever drive the process happens to be on — measured on CI,
	// "/etc/hosts" became "D:\etc", a real grant for a directory nobody
	// named. Asking about the wrong directory is worse than not asking, so
	// this stays off until the pattern learns drives and separators. A
	// Windows run simply behaves as it did before this feature existed.
	if runtime.GOOS == "windows" {
		return nil
	}
	// The workspace in the form the prompt is likely to name it, and in
	// the form the system reaches it by; see withinAny for why both.
	root := mustAbs(workdir)
	grants := map[string]bool{}
	var add func(dir string)
	add = func(dir string) {
		abs := filepath.Clean(mustAbs(dir))
		if abs == "" || withinAny(abs, root) {
			return
		}
		if abs == "/" {
			// The root covers everything else named, so one question
			// answers all of them rather than several that overlap.
			for k := range grants {
				delete(grants, k)
			}
			grants["/"] = true
			return
		}
		// Nested grants collapse: naming /var/log and /var/lib in one
		// prompt is one question, and the answer for /var covers both.
		for have := range grants {
			if withinAny(abs, have) {
				return
			}
		}
		grants[abs] = true
	}

	// Home-relative first, and taken out of the text: the absolute
	// pattern would otherwise read the "/notes/todo.md" inside it.
	for _, m := range homeRelative.FindAllString(text, -1) {
		home := mustHome()
		if home == "" || m == "~" {
			continue
		}
		text = strings.Replace(text, m, " ", 1)
		p := filepath.Join(home, strings.TrimPrefix(m, "~"))
		if !strings.HasSuffix(m, "/") {
			// The ~ form captures the whole path, so its last name may
			// be a directory the user named as one ("tidy up
			// ~/Downloads") or a file ("open ~/notes/todo.md") — and
			// --add-dir takes directories only. The disk decides: a
			// directory is granted as named, anything else — a file,
			// or a path not there yet — grants the directory that
			// holds it. A bare Dir() would widen "~/notes" to the
			// whole home, the exact over-reach this scan exists to
			// prevent.
			p = narrowGrant(p)
		}
		add(p)
	}
	for _, loc := range pathInText.FindAllStringIndex(text, -1) {
		// Every match ends in a slash — the regex keeps whole segments
		// only — so the grant is the directory those segments name. A
		// bare final name is never part of the match: "see
		// /tmp/a.txt." and "check /srv/data" both grant the directory
		// above the last name, which is the documented ambiguity, in
		// the safe direction for a file.
		m := strings.TrimLeft(text[loc[0]:loc[1]], " \t\n\"'`(=[]")
		if m == "/" {
			// "search everything under /" names the root; "100 / 4"
			// and "high / low" are arithmetic. The slash is the root
			// only where the sentence can end after it — a word
			// following it means it was a separator, and granting the
			// root (or, before this check, the process's working
			// directory) for one of those would be a card about
			// nothing. A number before it at the very end ("what is
			// 100 /") is a division missing its right operand, not
			// the root.
			if rest := strings.TrimLeft(text[loc[1]:], " \t"); rest != "" {
				continue
			}
			if trailingNumber(text[:loc[0]]) {
				continue
			}
			add("/")
			continue
		}
		add(m[:len(m)-1])
	}

	// The prompt is prose, so the order paths came in is an accident of
	// how it was written; sorting makes the card and the command line the
	// same on every run of the same prompt.
	out := make([]string, 0, len(grants))
	for g := range grants {
		out = append(out, g)
	}
	sort.Strings(out)
	return out
}

// narrowGrant is the file-or-directory decision one named path gets: a
// path the disk says is a directory is granted as named, anything else —
// a file, or a path that does not exist yet — narrows to the directory
// that holds it, because --add-dir takes directories only.
func narrowGrant(p string) string {
	if st, err := os.Stat(p); err == nil && st.IsDir() {
		return p
	}
	return filepath.Dir(p)
}

// trailingNumber reports whether s ends in a whitespace-delimited run of
// digits: the left operand of a division whose right operand never came.
func trailingNumber(s string) bool {
	s = strings.TrimRight(s, " \t\n")
	if i := strings.LastIndexAny(s, " \t\n"); i >= 0 {
		s = s[i+1:]
	}
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// withinAny reports whether p is inside root. Both paths are compared
// in their literal form and again with as much of each symlink chain
// resolved as exists, because resolution is all-or-nothing and these
// paths routinely do not resolve: a prompt names
// /var/folders/.../elsewhere/a.go, and EvalSymlinks on that fails
// outright since nothing below "elsewhere" is there yet. One comparison
// alone would then decide the question with a form the other side never
// had — and on macOS, where /var is really /private/var, that reads
// every workspace path as outside and asks about the directory the user
// is already in.
func withinAny(p, root string) bool {
	return within(p, root) ||
		within(resolveExisting(p), resolveExisting(root)) ||
		within(resolveExisting(p), root) ||
		within(p, resolveExisting(root))
}

// resolveExisting resolves the longest prefix of p that exists and
// reattaches the tail. It walks up with Dir/Base rather than Split,
// because Split reports an empty base for "/var" — which is exactly the
// symlink macOS puts above /private/var, and treating it as the end of
// the chain is what stopped resolution one level too soon.
func resolveExisting(p string) string {
	abs := filepath.Clean(mustAbs(p))
	rest, cur := "", abs
	for {
		if resolved, err := filepath.EvalSymlinks(cur); err == nil {
			if rest == "" {
				return resolved
			}
			return filepath.Join(resolved, rest)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return abs // the root itself; nothing left to resolve
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}

// within reports whether p is inside root, comparing clean absolute
// paths so ".." cannot step outside.
func within(p, root string) bool {
	if p == root {
		return true
	}
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// mustAbs is filepath.Abs for a path that is already usable, falling
// back to the input rather than failing: a path this scan cannot place
// is still worth asking about, just not worth dropping.
func mustAbs(p string) string {
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return p
}

// mustHome is the user's home directory, the one place a "~" in a
// prompt can be resolved. It is read on demand and never fatal: a home
// this scan cannot place is not a reason to drop a path the user named.
func mustHome() string {
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		return filepath.Clean(h)
	}
	return ""
}
