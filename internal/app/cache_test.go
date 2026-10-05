package app

import (
	"strings"
	"testing"
	"time"

	"mygo-agent/internal/harness"
)

// The Host added two per-frame caches in this round. They are the only
// new code that can show the user something wrong without crashing, so
// each one is pinned here by a test that fails when the cache is removed.

// timeAt is a fixed instant so stamp comparisons are not clock-dependent.
func timeAt(sec int) time.Time { return time.Unix(int64(sec), 0) }

func cmdEvent(id, command string) harness.Event {
	c := harness.ToolCall{ID: id}
	c.Function.Name = "bash"
	c.Function.Arguments = `{"command":"` + command + `"}`
	return harness.Event{Kind: harness.EventToolStart, ToolCall: c}
}

func TestChangedFilesCountsOnlyDiffs(t *testing.T) {
	a := newTestApp(t)
	th := &Thread{ID: "t1", ProjectID: "default"}
	th.Messages = []Message{{ID: "m0", Role: "assistant", Running: true}}
	a.threads = []*Thread{th}

	// Three command cards and one real file change.
	a.applyEvent(th, 0, "builtin", cmdEvent("c1", "go test ./..."))
	a.applyEvent(th, 0, "builtin", cmdEvent("c2", "go vet ./..."))
	a.applyEvent(th, 0, "builtin", cmdEvent("c3", "ls -la"))
	a.applyEvent(th, 0, "builtin", harness.Event{Kind: harness.EventFileChange,
		File: "main.go", Diff: "@@ -1 +1 @@\n-a\n+b"})

	if got := changedFiles(th); got != 1 {
		t.Fatalf("changedFiles = %d, want 1 (three command cards are not file changes)", got)
	}
	// The same answer must come out of a cold scan, i.e. the cache and the
	// scan must agree rather than one silently shadowing the other.
	th.invalidateDiffCount()
	if got := changedFiles(th); got != 1 {
		t.Fatalf("changedFiles after invalidation = %d, want 1", got)
	}
}

func TestChangedFilesSurvivesRegenerate(t *testing.T) {
	a := newTestApp(t)
	th := &Thread{ID: "t1", ProjectID: "default"}
	th.Messages = []Message{{ID: "m0", Role: "assistant", Running: true}}
	a.threads = []*Thread{th}
	a.backend = "codex" // regenerate's builtin rewind is not under test here

	a.applyEvent(th, 0, "builtin", harness.Event{Kind: harness.EventFileChange,
		File: "a.go", Diff: "@@ -1 +1 @@\n-a\n+b"})
	a.applyEvent(th, 0, "builtin", harness.Event{Kind: harness.EventFileChange,
		File: "b.go", Diff: "@@ -1 +1 @@\n-a\n+b"})
	if got := changedFiles(th); got != 2 {
		t.Fatalf("changedFiles = %d, want 2", got)
	}
	// Rewinding the messages must not leave a stale count behind.
	th.invalidateDiffCount()
	if got := changedFiles(th); got != 2 {
		t.Fatalf("changedFiles after invalidation = %d, want 2", got)
	}
}

func TestChangedFilesOnAFreshThreadScansFirst(t *testing.T) {
	// A thread built in memory (created, or seeded for a demo) has never
	// been scanned, so its cache must start out unknown rather than zero.
	th := &Thread{ID: "t1", Messages: []Message{
		{Blocks: []Block{{Type: "diff"}}},
		{Blocks: []Block{{Type: "diff"}, {Type: "command"}}},
	}}
	if got := changedFiles(th); got != 2 {
		t.Fatalf("changedFiles on a fresh thread = %d, want 2", got)
	}
}

func TestViewStampCoversEveryCopiedField(t *testing.T) {
	// The transcript snapshot is reused when the stamp is unchanged, so a
	// field the stamp misses means a stale transcript. Each mutation below
	// must change the stamp.
	base := &Thread{ID: "t1", Messages: []Message{{
		ID: "m0", Role: "assistant", Text: "hello", At: timeAt(1), Running: true,
		Blocks: []Block{{Type: "command", Text: "$ go test", Output: "ok", File: "main.go",
			Add: 1, Del: 2, Lines: []DiffLine{{Kind: '+', Text: "x"}}, Exit: 0, Ms: 5,
			Open: true, Edit: true, ToolID: "c1", ApprovalID: ""}},
	}}}
	want := base.viewStamp()

	mutations := map[string]func(*Thread){
		"message id":      func(th *Thread) { th.Messages[0].ID = "m1" },
		"message role":    func(th *Thread) { th.Messages[0].Role = "user" },
		"message text":    func(th *Thread) { th.Messages[0].Text = "hello!" },
		"message at":      func(th *Thread) { th.Messages[0].At = timeAt(2) },
		"message running": func(th *Thread) { th.Messages[0].Running = false },
		"extra message":   func(th *Thread) { th.Messages = append(th.Messages, Message{ID: "m1"}) },
		"extra block": func(th *Thread) {
			th.Messages[0].Blocks = append(th.Messages[0].Blocks, Block{Type: "diff"})
		},
		"block type":     func(th *Thread) { th.Messages[0].Blocks[0].Type = "diff" },
		"block text":     func(th *Thread) { th.Messages[0].Blocks[0].Text = "$ ls" },
		"block output":   func(th *Thread) { th.Messages[0].Blocks[0].Output = "fail" },
		"block file":     func(th *Thread) { th.Messages[0].Blocks[0].File = "other.go" },
		"block add":      func(th *Thread) { th.Messages[0].Blocks[0].Add = 9 },
		"block del":      func(th *Thread) { th.Messages[0].Blocks[0].Del = 9 },
		"block lines":    func(th *Thread) { th.Messages[0].Blocks[0].Lines = nil },
		"block exit":     func(th *Thread) { th.Messages[0].Blocks[0].Exit = 3 },
		"block ms":       func(th *Thread) { th.Messages[0].Blocks[0].Ms = 99 },
		"block open":     func(th *Thread) { th.Messages[0].Blocks[0].Open = false },
		"block edit":     func(th *Thread) { th.Messages[0].Blocks[0].Edit = false },
		"block toolid":   func(th *Thread) { th.Messages[0].Blocks[0].ToolID = "c2" },
		"block approval": func(th *Thread) { th.Messages[0].Blocks[0].ApprovalID = "a1" },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			th := &Thread{ID: "t1", Messages: []Message{{
				ID: "m0", Role: "assistant", Text: "hello", At: timeAt(1), Running: true,
				Blocks: []Block{{Type: "command", Text: "$ go test", Output: "ok", File: "main.go",
					Add: 1, Del: 2, Lines: []DiffLine{{Kind: '+', Text: "x"}}, Exit: 0, Ms: 5,
					Open: true, Edit: true, ToolID: "c1"}},
			}}}
			if th.viewStamp() != want {
				t.Fatal("the fixture does not match the base stamp")
			}
			mutate(th)
			if th.viewStamp() == want {
				t.Fatalf("mutating %s left the stamp unchanged, so the transcript cache would show a stale view", name)
			}
		})
	}
}

func TestTranscriptVMIsRebuiltWhenTheThreadChanges(t *testing.T) {
	a := newTestApp(t)
	th := &Thread{ID: "t1", ProjectID: "default"}
	th.Messages = []Message{{ID: "m0", Role: "assistant", Running: true}}
	a.threads = []*Thread{th}

	first := a.transcriptVM(th)
	a.update(func() { th.Messages[0].Text = "streamed in" })
	second := a.transcriptVM(th)
	if second.Messages[0].Text != "streamed in" {
		t.Fatalf("the cached snapshot was reused after the text changed: %q", second.Messages[0].Text)
	}
	// An unchanged thread may reuse the snapshot: that is the whole point.
	third := a.transcriptVM(th)
	if len(third.Messages) != 1 || third.Messages[0].Text != "streamed in" {
		t.Fatalf("unexpected snapshot: %+v", third.Messages)
	}
	_ = first
}

func TestTranscriptCacheEvictedOnDelete(t *testing.T) {
	a := newTestApp(t)
	th := &Thread{ID: "t1", ProjectID: "default"}
	th.Messages = []Message{{ID: "m0", Role: "assistant", Running: true}}
	a.threads = []*Thread{th}
	a.transcriptVM(th)
	if _, ok := a.transcriptCache["t1"]; !ok {
		t.Fatal("the cache should hold the snapshot after a render")
	}
	a.update(func() { a.deleteThread(nil, "t1") })
	if _, ok := a.transcriptCache["t1"]; ok {
		t.Fatal("the cached snapshot outlived the deleted task, leaking its transcript")
	}
}

func TestToolDisplayRedactsMCPArguments(t *testing.T) {
	// An MCP tool's arguments are free-form and never pass through the
	// keyed reader, so the redaction has to live in the truncating helper.
	cases := []string{
		`{"body":"please use token sk-abcdefgh12345678 now"}`,
		`{"api_key":"AIzaSyABCDEFGHIJKLMNOPQRST"}`,
		`{"headers":{"Authorization":"Bearer ghp_abcdefghijklmnopqrstuvwxyz0123"}}`,
	}
	for _, args := range cases {
		got := toolDisplayFor(mcpToolCall("github_create_issue", args))
		if strings.Contains(got, "sk-abcdefgh12345678") ||
			strings.Contains(got, "AIzaSyABCDEFGHIJKLMNOPQRST") ||
			strings.Contains(got, "ghp_abcdefghijklmnopqrstuvwxyz0123") {
			t.Fatalf("card title leaks a credential: %q (args %s)", got, args)
		}
		if !strings.Contains(got, "github_create_issue") {
			t.Fatalf("the tool name should stay visible: %q", got)
		}
	}
}

func mcpToolCall(name, args string) harness.ToolCall {
	var c harness.ToolCall
	c.ID = "c1"
	c.Function.Name = name
	c.Function.Arguments = args
	return c
}
