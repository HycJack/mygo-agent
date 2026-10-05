package builtin

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestEditFileRefusesSymlinkEscape covers the write confinement: a
// symlink inside the workdir pointing out of it passes a lexical check,
// and the write would land outside the workspace.
func TestEditFileRefusesSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating a symlink on windows needs a privilege")
	}
	work, outside := t.TempDir(), t.TempDir()
	target := filepath.Join(outside, "hosts")
	if err := os.WriteFile(target, []byte("root:x:0:0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(work, "link")); err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(work, "inside.txt")
	if err := os.WriteFile(inside, []byte("a\nb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	edit := editFileTool(work, ToolOptions{ConfineWrites: true})

	// The control: a real file inside the workdir still edits.
	if _, err := edit.Execute(t.Context(), `{"path":"inside.txt","old_text":"a","new_text":"z"}`); err != nil {
		t.Fatalf("an edit inside the workdir must be allowed: %v", err)
	}
	// The escape: the same edit through the link must be refused.
	_, err := edit.Execute(t.Context(), `{"path":"link/hosts","old_text":"root","new_text":"pwned"}`)
	if err == nil || !strings.Contains(err.Error(), "outside") {
		t.Fatalf("a symlink out of the workdir must be refused: %v", err)
	}
	if data, _ := os.ReadFile(target); string(data) != "root:x:0:0\n" {
		t.Fatalf("the file outside the workdir was written: %q", data)
	}
}

// TestBashOutputIsCappedWhileRunning asserts the cap applies during the
// read: a command that would fill memory is bounded, and the model still
// gets the "first N" marker rather than an empty or huge result.
func TestBashOutputIsCappedWhileRunning(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the bash tool drives powershell on windows")
	}
	var bash Tool
	for _, tl := range Tools(t.TempDir(), nil) {
		if tl.Name == "bash" {
			bash = tl
		}
	}
	out, err := bash.Execute(t.Context(), `{"command":"yes hello | head -c 5000000"}`)
	if err != nil {
		t.Fatalf("a truncated command is not a failure: %v", err)
	}
	if len(out) > maxCommandOutput {
		t.Fatalf("output was not capped: %d bytes", len(out))
	}
	if !strings.Contains(out, "output truncated") {
		t.Fatalf("the cap must say what was dropped: %q", out[max(0, len(out)-60):])
	}
}

// TestReadFileRefusesOversizedFiles: a whole-file slurp must not size
// the heap; the caller is told to read ranges instead.
func TestReadFileRefusesOversizedFiles(t *testing.T) {
	work := t.TempDir()
	big := filepath.Join(work, "big.txt")
	if err := os.WriteFile(big, make([]byte, maxReadFileBytes+1), 0o644); err != nil {
		t.Fatal(err)
	}
	small := filepath.Join(work, "small.txt")
	if err := os.WriteFile(small, []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	read := readFileTool(work)
	if _, err := read.Execute(t.Context(), `{"path":"small.txt"}`); err != nil {
		t.Fatalf("a small read must still work: %v", err)
	}
	_, err := read.Execute(t.Context(), `{"path":"big.txt"}`)
	if err == nil || !strings.Contains(err.Error(), "read limit") {
		t.Fatalf("an oversized file must be refused: %v", err)
	}
}

// TestToolsHonourCancellation: the walk tools could not be interrupted
// before, so the stop button did nothing until they finished.
func TestToolsHonourCancellation(t *testing.T) {
	work := t.TempDir()
	if err := os.WriteFile(filepath.Join(work, "a.txt"), []byte("needle\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already stopped: every walk tool must say so and return
	for _, tc := range []struct {
		tool Tool
		args string
	}{
		{grepTool(work), `{"pattern":"needle"}`},
		{listFilesTool(work), `{}`},
		{readFileTool(work), `{"path":"a.txt"}`},
		{readSkillTool(work, nil), `{"name":"any"}`},
	} {
		if _, err := tc.tool.Execute(ctx, tc.args); err == nil || !strings.Contains(err.Error(), "cancelled") {
			t.Errorf("%s: a stopped run must report the cancellation, got %v", tc.tool.Name, err)
		}
	}
}

// TestGrepOnAMissingPathDoesNotPanic pins a crash the walk guard
// introduced: WalkDir calls back with a NIL DirEntry when the root itself
// cannot be lstat'ed, and the path is model-authored, so one bad guess
// used to take the whole app down. There is no recover() anywhere.
func TestGrepOnAMissingPathDoesNotPanic(t *testing.T) {
	dir := t.TempDir()
	tools := Tools(dir, DiscoverSkills(dir))
	for _, tool := range tools {
		if tool.Name != "grep" {
			continue
		}
		// The point is that it returns at all. An empty result for a path
		// that does not exist is acceptable; taking the process down is
		// not.
		if _, err := tool.Execute(context.Background(), `{"pattern":"x","path":"no-such-dir"}`); err != nil {
			t.Logf("grep on a missing path reported %v", err)
		}
		// A path that exists but is a file, not a directory.
		f := filepath.Join(dir, "afile")
		if err := os.WriteFile(f, []byte("hello\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := tool.Execute(context.Background(), `{"pattern":"x","path":"afile"}`); err != nil {
			t.Fatalf("grep on a file path: %v", err)
		}
		return
	}
	t.Fatal("grep is missing from the tool set")
}

// TestGrepRefusesAFileSymlinkIntoACredentialStore closes a leak the
// directory check missed: WalkDir reports a file symlink as a non-dir, so
// gating on d.IsDir() alone let os.ReadFile follow it into the store.
func TestGrepRefusesAFileSymlinkIntoACredentialStore(t *testing.T) {
	if len(sensitivePaths) == 0 {
		t.Skip("no home directory to derive credential stores from")
	}
	store := sensitivePaths[0][1]
	if err := os.MkdirAll(store, 0o700); err != nil {
		t.Skipf("cannot create %s: %v", store, err)
	}
	defer os.RemoveAll(store)
	secret := filepath.Join(store, "credentials")
	if err := os.WriteFile(secret, []byte("aws_secret_access_key = LEAKME\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	link := filepath.Join(dir, "innocent.txt")
	if err := os.Symlink(secret, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	tools := Tools(dir, DiscoverSkills(dir))
	for _, tool := range tools {
		if tool.Name != "grep" {
			continue
		}
		got, err := tool.Execute(context.Background(), `{"pattern":"LEAKME"}`)
		if err != nil {
			t.Fatalf("grep failed outright: %v", err)
		}
		if strings.Contains(got, "LEAKME") {
			t.Fatalf("grep followed a symlink into a credential store: %q", got)
		}
		return
	}
	t.Fatal("grep is missing from the tool set")
}

// TestRefusesSensitiveFoldsCase proves the case bypass is closed: the
// default volume on macOS and Windows is case-insensitive, so a purely
// textual check lets ~/.SSH/id_rsa past a list holding ~/.ssh.
func TestRefusesSensitiveFoldsCase(t *testing.T) {
	if len(sensitivePaths) == 0 {
		t.Skip("no home directory to derive credential stores from")
	}
	store := sensitivePaths[0][1]
	if !refusesSensitive(store) {
		t.Fatalf("the store itself is not refused: %s", store)
	}
	if !refusesSensitive(strings.ToUpper(store)) {
		t.Fatal("an upper-cased credential path is not refused")
	}
	if !refusesSensitive(filepath.Join(strings.ToUpper(store), "id_rsa")) {
		t.Fatal("a file inside an upper-cased credential dir is not refused")
	}
	// Folding must not over-block an unrelated path.
	if refusesSensitive(strings.ToUpper(filepath.Dir(filepath.Dir(store))) + "/notes.md") {
		t.Fatal("an ordinary path was refused after case folding")
	}
}

// TestReadFileRangeWorksOnALargeFile proves the oversize guard leaves the
// remedy in its own message usable: a ranged read of a big file works.
func TestReadFileRangeWorksOnALargeFile(t *testing.T) {
	dir := t.TempDir()
	big := filepath.Join(dir, "big.txt")
	var b strings.Builder
	for i := 1; i <= 200_000; i++ {
		fmt.Fprintf(&b, "line %d padding padding padding\n", i)
	}
	if err := os.WriteFile(big, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(big); st.Size() <= maxReadFileBytes {
		t.Skipf("fixture is only %d bytes, under the whole-file cap", st.Size())
	}
	tools := Tools(dir, DiscoverSkills(dir))
	for _, tool := range tools {
		if tool.Name != "read_file" {
			continue
		}
		// Whole-file read of something over the cap is still refused.
		if _, err := tool.Execute(context.Background(), `{"path":"big.txt"}`); err == nil {
			t.Fatal("a whole-file read over the cap was allowed")
		}
		// A ranged read must work, which is what the refusal message tells
		// the model to do.
		got, err := tool.Execute(context.Background(), `{"path":"big.txt","offset":10,"limit":3}`)
		if err != nil {
			t.Fatalf("a ranged read of a large file failed: %v", err)
		}
		if !strings.Contains(got, "line 10") {
			t.Fatalf("ranged read returned the wrong lines: %q", got)
		}
		if strings.Contains(got, "line 20") {
			t.Fatalf("ranged read ignored the limit: %q", got)
		}
		return
	}
	t.Fatal("read_file is missing from the tool set")
}
