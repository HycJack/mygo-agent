package app

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"mygo-agent/internal/config"
	"mygo-agent/internal/harness"
)

// TestDeletingTheRunningThreadStopsTheRun pins the delete contract: the
// stop belongs to the thread whose turn is running, not the one on
// screen. Keyed on current, deleting a background task leaked the run —
// it kept spending tokens, and whatever its turn drained after the
// delete could re-save the file the delete had removed.
//
// The provider never answers, so the only way the run can settle is the
// cancel a.stop() fires; that is what the assertions observe. The test
// goroutine touches host state only under a.update — the run goroutine
// is live for the whole test, and headless update is what serializes
// against it.
func TestDeletingTheRunningThreadStopsTheRun(t *testing.T) {
	gate := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-gate // the turn never completes on its own
	}))
	t.Cleanup(srv.Close)              // cleanup runs LIFO: the gate opens first,
	t.Cleanup(func() { close(gate) }) // so Close never waits on the handler.

	a := newTestApp(t)
	a.mode = 2
	// startTurn saves the thread; without this the write lands in the
	// developer's real threads directory.
	a.threadsDir = threadsLayout{root: filepath.Join(t.TempDir(), "threads")}
	a.providers = []config.Provider{{
		ID: "p1", Name: "Test", BaseURL: srv.URL, APIKey: "k",
		Models: []string{"test-model"}, Wire: harness.WireResponses,
	}}
	a.providerID, a.model = "p1", "test-model"

	th := &Thread{ID: "t1", ProjectID: "default"}
	a.threads = append(a.threads, th)
	a.current = "t1"
	a.startTurn(th, "keep running")

	running := false
	a.update(func() { running = a.isRunning("t1") })
	if !running {
		t.Fatal("the turn is not running")
	}

	// Another task is on screen when the running one is deleted — the
	// shape that used to leak the run.
	other := &Thread{ID: "t2", ProjectID: "default"}
	a.update(func() {
		a.threads = append(a.threads, other)
		a.current = "t2"
		a.deleteThread(nil, "t1")
	})

	deadline := time.Now().Add(10 * time.Second)
	for {
		settled := false
		a.update(func() { settled = !a.isRunning("t1") && a.current == "t2" })
		if settled {
			break
		}
		if time.Now().After(deadline) {
			running := false
			a.update(func() { running = a.isRunning("t1") })
			t.Fatalf("the run never settled after the delete: running=%v current=%q",
				running, a.current)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestADroppedThreadStaysDeleted pins the guard that keeps a delete
// final: events from the deleted turn can still reach saveThread while
// the run drains, and without the flag one of them re-created the file
// removeThreadFile had just removed. Undo is what clears the flag.
func TestADroppedThreadStaysDeleted(t *testing.T) {
	a := newTestApp(t)
	a.threadsDir = threadsLayout{root: filepath.Join(t.TempDir(), "threads")}
	th := &Thread{ID: "t1", ProjectID: "default"}
	a.threads = append(a.threads, th)
	a.saveThread(th)
	path, ok := a.threadsDir.file(th)
	if !ok {
		t.Fatal("no path for the thread file")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the thread file was never written: %v", err)
	}

	// The delete marks the thread dropped and removes its file.
	th.dropped = true
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	// An event draining late must not resurrect it.
	a.saveThread(th)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("a dropped thread's file was rewritten by a late save: %v", err)
	}

	// Undo clears the flag and rewrites the file.
	th.dropped = false
	a.saveThread(th)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("undo could not rewrite the file: %v", err)
	}
}
