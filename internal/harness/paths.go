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
// code, keeping whether it was written with a trailing slash: "look in
// /srv" names a directory, "/srv/a.txt" names a file inside one, and
// only the second grants /srv's parent as well.
//
// The slash has to be preceded by something that can start a path —
// the beginning of the text, whitespace, or a quote. Without that,
// "a/b/c" reads as the absolute /b/c and a prompt about ratios ends up
// asking for a grant on /b. The first segment must also look like a
// name rather than a number, which rules out "3/4".
var pathInText = regexp.MustCompile(`(?:^|[\s"'` + "`" + `(=\[])/(?:[A-Za-z0-9_.~-]+/)*(?:[A-Za-z0-9_.~-]+/)?`)

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
		add(filepath.Join(home, strings.TrimPrefix(m, "~")))
	}
	for _, m := range pathInText.FindAllString(text, -1) {
		// Drop the delimiter the match kept, then the sentence period:
		// "see /tmp/a.txt." means /tmp, not /tmp/a.txt.
		m = strings.TrimLeft(m, " \t\n\"'`(=[]")
		m = strings.TrimRight(m, ".")
		if m == "" {
			continue
		}
		if m == "/" || strings.HasSuffix(m, "/") {
			// A trailing slash names the directory itself, so the grant
			// is that directory and not its parent.
			add(strings.TrimRight(m, "/"))
			continue
		}
		// No trailing slash means a file, so the grant is its directory.
		add(filepath.Dir(m))
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
