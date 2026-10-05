package app

import (
	"mygo-agent/internal/harness"

	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// threadStore points an app at a fresh per-thread store (spec/data.md).
func threadStore(t *testing.T) (*app, string) {
	t.Helper()
	a := newTestApp(t)
	store := filepath.Join(t.TempDir(), "threads")
	a.threadsDir = threadsLayout{root: store}
	return a, store
}

// TestThreadFileRoundTrip proves one thread = one file in its project
// directory, and it loads back whole: meta, messages and ChatLog.
func TestThreadFileRoundTrip(t *testing.T) {
	a, store := threadStore(t)
	th := &Thread{ID: "abc123", ProjectID: "p1", Title: "Round trip",
		Created: time.Now(), Updated: time.Now()}
	th.Messages = []Message{{ID: "m0", Role: "user", Text: "hello"}}
	th.ChatLog = harnessChatMessage()
	a.saveThread(th)

	path := filepath.Join(store, "p1", "abc123.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("thread file missing: %v", err)
	}

	b := newTestApp(t)
	b.threadsDir = threadsLayout{root: store}
	b.activeProject = "p1"
	b.loadThreads()
	if len(b.threads) != 1 {
		t.Fatalf("threads: %+v", b.threads)
	}
	got := b.threads[0]
	if got.Title != "Round trip" || len(got.Messages) != 1 || got.Messages[0].Text != "hello" || len(got.ChatLog) != 1 {
		t.Fatalf("round trip incomplete: %+v", got)
	}
}

// TestLegacyThreadsJSONImportsOnce proves the upgrade contract: the
// single-file store imports into the tree and is renamed .migrated —
// preserved, never deleted (spec/data.md).
func TestLegacyThreadsJSONImportsOnce(t *testing.T) {
	dir := t.TempDir()
	a := newTestApp(t)
	a.threadsDir = threadsLayout{root: filepath.Join(dir, "threads")}
	a.savePath = filepath.Join(dir, "threads.json")
	if err := os.WriteFile(a.savePath, []byte(
		`[{"ID":"t1","ProjectID":"default","Title":"Legacy task"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	a.importLegacyThreads()
	if _, err := os.Stat(filepath.Join(dir, "threads", "default", "t1.json")); err != nil {
		t.Fatal("the imported thread has no file")
	}
	a.loadThreads()
	if len(a.threads) != 1 || a.threads[0].Title != "Legacy task" {
		t.Fatalf("legacy import failed: %+v", a.threads)
	}
	if _, err := os.Stat(filepath.Join(dir, "threads", "default", "t1.json")); err != nil {
		t.Fatal("the imported thread has no file")
	}
	if _, err := os.Stat(a.savePath); !os.IsNotExist(err) {
		t.Fatal("the legacy file was not renamed away")
	}
	if _, err := os.Stat(a.savePath + ".migrated"); err != nil {
		t.Fatal("the legacy file was not preserved as .migrated")
	}
}

// TestCorruptThreadFileIsQuarantinedAlone proves per-thread blast
// radius: one corrupt file is renamed .invalid and skipped; the other
// threads still load (spec/data.md).
func TestCorruptThreadFileIsQuarantinedAlone(t *testing.T) {
	a, store := threadStore(t)
	good := &Thread{ID: "good1", ProjectID: "p1", Title: "Good", Created: time.Now(), Updated: time.Now()}
	a.saveThread(good)
	bad := filepath.Join(store, "p1", "bad1.json")
	if err := os.WriteFile(bad, []byte(`{"version":1,"meta":`), 0o600); err != nil {
		t.Fatal(err)
	}

	a.activeProject = "p1"
	a.loadThreads()
	if len(a.threads) != 1 || a.threads[0].ID != "good1" {
		t.Fatalf("the good thread did not survive: %+v", a.threads)
	}
	if a.threadsErr == "" {
		t.Fatal("the corruption was not surfaced")
	}
	if _, err := os.Stat(bad + ".invalid"); err != nil {
		t.Fatal("the corrupt file was not preserved")
	}
}

// TestNewerThreadFileIsQuarantined proves the version guard for thread
// files: a newer schema is refused and preserved, never misparsed.
func TestNewerThreadFileIsQuarantined(t *testing.T) {
	a, store := threadStore(t)
	p := filepath.Join(store, "p1", "future.json")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(`{"version":9,"meta":{"id":"future"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	a.activeProject = "p1"
	a.loadThreads()
	if a.threadsErr == "" || !strings.Contains(a.threadsErr, "newer version") {
		t.Fatalf("version refusal not surfaced: %q", a.threadsErr)
	}
	if _, err := os.Stat(p + ".unsupported"); err != nil {
		t.Fatal("the newer file was not preserved")
	}
}

// TestDeleteRemovesOnlyItsFile proves deletion granularity: removing a
// task deletes exactly its file; undo rewrites it.
func TestDeleteRemovesOnlyItsFile(t *testing.T) {
	a, store := threadStore(t)
	t1 := &Thread{ID: "gone1", ProjectID: "p1", Title: "Gone", Created: time.Now(), Updated: time.Now()}
	t2 := &Thread{ID: "kept1", ProjectID: "p1", Title: "Kept", Created: time.Now(), Updated: time.Now()}
	a.saveThread(t1)
	a.saveThread(t2)

	a.removeThreadFile(t1)
	if _, err := os.Stat(filepath.Join(store, "p1", "gone1.json")); !os.IsNotExist(err) {
		t.Fatal("the deleted thread's file survives")
	}
	if _, err := os.Stat(filepath.Join(store, "p1", "kept1.json")); err != nil {
		t.Fatal("the sibling file was removed too")
	}
	a.saveThread(t1) // the undo path
	if _, err := os.Stat(filepath.Join(store, "p1", "gone1.json")); err != nil {
		t.Fatal("the undo did not restore the file")
	}
}

// TestThreadFilesAre0600 proves the permission rule: thread content
// includes code snippets, so the files stay private.
func TestThreadFilesAre0600(t *testing.T) {
	a, store := threadStore(t)
	a.saveThread(&Thread{ID: "perm1", ProjectID: "p1", Title: "P", Created: time.Now(), Updated: time.Now()})
	st, err := os.Stat(filepath.Join(store, "p1", "perm1.json"))
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("thread file mode %v, want 0600", st.Mode().Perm())
	}
}

// harnessChatMessage builds the one-entry ChatLog the round-trip test
// asserts on.
func harnessChatMessage() []harness.ChatMessage {
	return []harness.ChatMessage{{Role: "user", Content: "hi"}}
}
