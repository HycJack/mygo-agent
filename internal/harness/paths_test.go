//go:build !windows

package harness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The scan decides what the user is asked about, so both directions have
// to hold: a path they named is noticed, and nothing else is. A missed
// path leaves them with a refusal they never got to answer; a spurious
// one asks about a directory they never mentioned.

func TestDirsOutsideWorkdirFindsWhatThePromptNamed(t *testing.T) {
	ws := t.TempDir()
	got := dirsOutsideWorkdir("look at "+filepath.Join("/etc", "hosts")+" please", ws)
	if len(got) != 1 || got[0] != "/etc" {
		t.Fatalf("got %v, want [/etc]", got)
	}
}

func TestDirsOutsideWorkdirLeavesTheWorkspaceAlone(t *testing.T) {
	ws := t.TempDir()
	for _, text := range []string{
		"edit " + filepath.Join(ws, "main.go"),
		"and " + filepath.Join(ws, "sub", "dir", "file.txt"),
		// A ".." that stays inside: climbing out and back down is the
		// same directory, and asking about it would be noise.
		"read " + filepath.Join(ws, "sub", "..", "main.go"),
		// Written with a trailing slash, so it is unambiguously the
		// directory itself rather than a file named after it.
		"look in " + ws + "/",
	} {
		if got := dirsOutsideWorkdir(text, ws); len(got) != 0 {
			t.Errorf("prompt %q produced %v, want nothing — the workspace needs no asking about", text, got)
		}
	}
}

// The CLI's --add-dir takes directories only: passing a file path grants
// nothing, and the read is still refused. So a prompt naming a file has
// to be granted its directory, and the scan narrows to that. Naming a
// file with no directory part of its own — "look in /srv" — is read the
// same way, which is the one ambiguity here: /srv could be a file, and
// the grant would then be the filesystem root above it. That is the
// safe direction to be wrong in, and it is why the trailing-slash form
// above is the unambiguous one.
func TestDirsOutsideWorkdirGrantsTheDirectoryNotTheFile(t *testing.T) {
	ws := t.TempDir()
	got := dirsOutsideWorkdir("read /tmp/cxtest/somefile.txt", ws)
	if len(got) != 1 || got[0] != "/tmp/cxtest" {
		t.Fatalf("got %v, want [/tmp/cxtest] — --add-dir takes a directory, not a file", got)
	}
}

func TestDirsOutsideWorkdirCatchesTheParentItClimbsTo(t *testing.T) {
	ws := t.TempDir()
	// The mirror image of the case above, and the one that matters: a
	// ".." that really does leave the workspace is still outside, and the
	// scan must not read the leading workspace component as a grant.
	outside := filepath.Join(ws, "..", "elsewhere", "x.txt")
	got := dirsOutsideWorkdir("read "+outside, ws)
	if len(got) != 1 {
		t.Fatalf("got %v, want one grant — climbing out with .. is not the workspace", got)
	}
	if within(got[0], ws) {
		t.Fatalf("the grant %q is inside the workspace", got[0])
	}
}

func TestDirsOutsideWorkdirDoesNotFireOnProse(t *testing.T) {
	ws := t.TempDir()
	// None of these is a path. Firing on them would ask the user about
	// nothing, which teaches them to click through the card. The two
	// arithmetic slashes are the shape that once granted the process's
	// working directory: a bare "/" with a word after it is a separator,
	// not a location.
	for _, text := range []string{
		"please fix the parser and run the tests",
		"what does 3/4 evaluate to?",
		"add a --flag=value option",
		"the a/b/c ratio is wrong",
		"compare high / low quality settings",
		"what is 100 / 4",
		"",
	} {
		if got := dirsOutsideWorkdir(text, ws); len(got) != 0 {
			t.Errorf("prompt %q produced %v, want nothing", text, got)
		}
	}
}

func TestDirsOutsideWorkdirStripsSentencePunctuation(t *testing.T) {
	ws := t.TempDir()
	// The period ends the sentence, it is not part of the name.
	got := dirsOutsideWorkdir("read /etc/hosts.", ws)
	if len(got) != 1 || got[0] != "/etc" {
		t.Fatalf("got %v, want [/etc] — the sentence period was kept as part of the path", got)
	}
}

func TestDirsOutsideWorkdirResolvesHomeRelative(t *testing.T) {
	ws := t.TempDir()
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no home directory")
	}
	if within(ws, home) {
		t.Skip("the workspace is inside the home directory")
	}
	got := dirsOutsideWorkdir("open ~/notes/todo.md", ws)
	if len(got) != 1 {
		t.Fatalf("got %v, want one path", got)
	}
	if !strings.HasPrefix(got[0], home) {
		t.Fatalf("~ was not expanded: %q is not under %q", got[0], home)
	}
	// It is narrowed to the directory, not carried up to $HOME.
	if got[0] == home {
		t.Fatal("a ~/notes/todo.md reference widened to the whole home directory")
	}
}

// TestDirsOutsideWorkdirHomeRelativeFileGrantsItsDirectory pins the
// same narrowing for the ~ form: --add-dir takes directories only, so a
// file under ~ grants the directory that holds it. The old code granted
// the file itself — an ask the user could approve and still get nothing.
func TestDirsOutsideWorkdirHomeRelativeFileGrantsItsDirectory(t *testing.T) {
	ws := t.TempDir()
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no home directory")
	}
	if within(ws, home) {
		t.Skip("the workspace is inside the home directory")
	}
	got := dirsOutsideWorkdir("look at ~/src/project/main.go", ws)
	want := filepath.Join(home, "src", "project")
	if len(got) != 1 || got[0] != want {
		t.Fatalf("got %v, want [%s] — the grant is the file's directory, not the file", got, want)
	}
}

func TestDirsOutsideWorkdirDeduplicatesAndSorts(t *testing.T) {
	ws := t.TempDir()
	text := "compare /var/log/a.txt, /etc/hosts and /var/log/b.txt and /etc/hosts again"
	got := dirsOutsideWorkdir(text, ws)
	want := []string{"/etc", "/var/log"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
	// The same prompt must produce the same ask on every run.
	if again := dirsOutsideWorkdir(text, ws); strings.Join(again, ",") != strings.Join(got, ",") {
		t.Fatalf("the same prompt produced two different asks: %v then %v", got, again)
	}
}

func TestDirsOutsideWorkdirKeepsTheRootItself(t *testing.T) {
	ws := t.TempDir()
	// "/" is the whole filesystem. It is a real thing a prompt can name,
	// and hiding it would mean the card understates what is being asked.
	if got := dirsOutsideWorkdir("search everything under /", ws); len(got) != 1 {
		t.Fatalf("got %v, want one entry", got)
	}
}

// TestNarrowGrantDecidesByTheDisk pins the file-or-directory decision the
// ~ form needs: a directory named without a slash is granted as named,
// a file — and a path that does not exist yet — grants its directory.
// A bare Dir() widened "~/notes" to the whole home directory.
func TestNarrowGrantDecidesByTheDisk(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(sub, "a.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := narrowGrant(sub); got != sub {
		t.Fatalf("a named directory was narrowed away: %q", got)
	}
	if got := narrowGrant(file); got != sub {
		t.Fatalf("a file must grant its directory: got %q, want %q", got, sub)
	}
	if got := narrowGrant(filepath.Join(dir, "not", "there", "x.md")); got != filepath.Join(dir, "not", "there") {
		t.Fatalf("a path not there yet must grant its would-be directory: %q", got)
	}
}

// TestDirsOutsideWorkdirKeepsANamedHomeDirectory pins the directory form
// of the ~ reference: "check ~/Documents" must grant ~/Documents, not
// widen to the whole home.
func TestDirsOutsideWorkdirKeepsANamedHomeDirectory(t *testing.T) {
	ws := t.TempDir()
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no home directory")
	}
	if within(ws, home) {
		t.Skip("the workspace is inside the home directory")
	}
	named := filepath.Join(home, "Documents")
	if st, err := os.Stat(named); err != nil || !st.IsDir() {
		t.Skip("no ~/Documents on this machine")
	}
	got := dirsOutsideWorkdir("check ~/Documents for the files", ws)
	if len(got) != 1 || got[0] != named {
		t.Fatalf("got %v, want [%s] — a named home directory was narrowed or widened", got, named)
	}
}

// TestDirsOutsideWorkdirTrailingSlashOfArithmetic pins the sentence-end
// edge of the bare-slash rule: "what is 100 /" is a division missing its
// right operand, not the root being named.
func TestDirsOutsideWorkdirTrailingSlashOfArithmetic(t *testing.T) {
	ws := t.TempDir()
	if got := dirsOutsideWorkdir("what is 100 /", ws); len(got) != 0 {
		t.Fatalf("a trailing arithmetic slash produced %v, want nothing", got)
	}
}
