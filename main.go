// Codex for Go: a desktop AI coding agent in the shape of the Codex app,
// built with MyGo's native UI — no webview, no HTML, no JavaScript.
//
// It has the task sidebar, the streaming thread with command and diff
// cards, the composer with model and approval mode, and an embedded
// terminal. Two agent backends: a built-in demo agent, and the real
// `codex exec` CLI when it is installed.
//
//	go run .
//
// Set CODEX_SHOT=<file.png> to capture a screenshot of the window and
// exit; set CODEX_SEED=1 to start with sample tasks.
package main

import (
	"log"
	"os"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func main() {
	a := newApp()
	a.load()
	if os.Getenv("CODEX_SEED") == "1" {
		a.seed()
	}
	if os.Getenv("CODEX_NAV") == "0" {
		a.navOpen = false
	}
	if os.Getenv("CODEX_WS") == "0" {
		a.wsOpen = false
	}
	if os.Getenv("CODEX_POPOVER") == "model" {
		a.modelMenu = true
	}
	if p := os.Getenv("CODEX_VIEW"); p != "" {
		if path, ok := strings.CutPrefix(p, "git:"); ok {
			a.openGitDiff(gitChange{Code: "M", Path: path})
		} else {
			a.openFile(p)
		}
	}
	mygo.App.WhenReady(func() {
		if os.Getenv("CODEX_TERM") == "1" {
			a.toggleTerminal(nil)
		}
		a.refreshGit()
		a.win = mygo.NewWindow(mygo.WindowOptions{
			Title:         "Codex",
			Width:         1240,
			Height:        800,
			MinWidth:      880,
			MinHeight:     560,
			TitleBarStyle: mygo.TitleBarHiddenInset,
			StateKey:      "codex-main",
			Content:       ui.View(a.view),
		})
		if shot := os.Getenv("CODEX_SHOT"); shot != "" {
			go func() {
				time.Sleep(2 * time.Second)
				png, err := a.win.CapturePage()
				if err != nil {
					log.Printf("capture: %v", err)
					os.Exit(1)
				}
				if err := os.WriteFile(shot, png, 0o644); err != nil {
					log.Printf("write shot: %v", err)
					os.Exit(1)
				}
				log.Printf("captured %s", shot)
				os.Exit(0)
			}()
		}
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

// seed fills the app with sample tasks, for screenshots and demos.
func (a *app) seed() {
	now := time.Now()

	t1 := &Thread{
		ID:        uid(),
		ProjectID: "default",
		Title:     "Fix the flaky retry test in the outbox worker",
		Created:   now.Add(-32 * time.Minute),
		Updated:   now.Add(-3 * time.Minute),
	}
	t1.Messages = []Message{
		{ID: uid(), Role: "user", At: now.Add(-6 * time.Minute),
			Text: "TestOutboxRetry fails about one run in ten on CI. Find the race and fix it."},
		{ID: uid(), Role: "assistant", At: now.Add(-4 * time.Minute), Blocks: []Block{
			{Type: "reasoning", Text: "The retry counter is read and written from the delivery goroutine and the test's timer without a lock; the test also polls a shared map, so the assertion can run between the two writes."},
			{Type: "command", Text: "go test -run TestOutboxRetry -count=20 ./internal/outbox/", Exit: 1, Ms: 890,
				Output: "--- FAIL: TestOutboxRetry (0.31s)\n    outbox_test.go:87: expected 2 attempts, got 3\n    outbox_test.go:92: attempt log: [send send send]\nFAIL\nFAIL\tgithub.com/acme/queue/internal/outbox\t0.89s\nFAIL"},
			{Type: "diff", File: "internal/outbox/retry.go", Add: 3, Del: 1, Open: true, Lines: []DiffLine{
				{Kind: ' ', Text: "type Retry struct {"},
				{Kind: ' ', Text: "\tattempts atomic.Int32"},
				{Kind: '-', Text: "\tmu       sync.Mutex"},
				{Kind: '+', Text: "\tdeadline time.Time"},
				{Kind: '+', Text: "\tbackoff  Backoff"},
				{Kind: '+', Text: ""},
				{Kind: ' ', Text: "func (r *Retry) Next(ctx context.Context) bool {"},
			}},
		}, Text: "Found it — two problems, and they compound:\n\n| Location | Problem | Fix |\n|---|---|---|\n| `Retry.attempts` | plain `int`, read from another goroutine | make it `atomic.Int32` |\n| `outbox_test.go:92` | polls a shared map without a lock | guard the map with a mutex |\n\n- `Retry.attempts` is a plain `int` written from the delivery goroutine and read by the test's timer, so the test can observe a stale count. That alone explains the flake.\n- The test polls a shared map without a lock, which can interleave with the write and double-count a retry.\n\nThe fix above makes the counter atomic and drops the now-unused mutex. With it:\n\n```go\nfunc (r *Retry) Next(ctx context.Context) bool {\n\tif r.attempts.Load() >= r.max {\n\t\treturn false\n\t}\n\tr.attempts.Add(1)\n\treturn r.backoff.Wait(ctx)\n}\n```\n\n`go test -run TestOutboxRetry -count=100` passes 100 of 100 locally. Want me to apply the same treatment to `TestDeliveryTimeout`, which polls in the same style?"},
	}

	t2 := &Thread{
		ID:        uid(),
		ProjectID: "default",
		Title:     "Explain the stream protocol adapter",
		Created:   now.Add(-4 * time.Hour),
		Updated:   now.Add(-2 * time.Hour),
	}
	t2.Messages = []Message{
		{ID: uid(), Role: "user", At: now.Add(-3 * time.Hour),
			Text: "What does the stream protocol adapter actually do? One paragraph, no code."},
		{ID: uid(), Role: "assistant", At: now.Add(-2 * time.Hour),
			Text: "It sits between the agent's event stream and the UI: it translates the engine's observation callbacks into the five shared message kinds, buffers them while the client reconnects, and replays anything the client missed — so neither side has to know about the other's wire format."},
	}

	t3 := &Thread{
		ID:        uid(),
		ProjectID: "default",
		Title:     "Draft the changelog for 0.3.0",
		Created:   now.AddDate(0, 0, -1).Add(-3 * time.Hour),
		Updated:   now.AddDate(0, 0, -1),
	}
	t3.Messages = []Message{
		{ID: uid(), Role: "user", At: now.AddDate(0, 0, -1).Add(-2 * time.Hour),
			Text: "Draft the 0.3.0 changelog from the merged PRs since 0.2.6."},
		{ID: uid(), Role: "assistant", At: now.AddDate(0, 0, -1),
			Blocks: []Block{
				{Type: "command", Text: "git log --oneline v0.2.6..HEAD", Exit: 0,
					Output: "a1b2c3d feat(envd): reverse WebSocket transport\n9e8f7a6 feat(service): durable checkpoints\n77c1d2e fix(harness): preserve tool order on resume"},
			},
			Text: "Here's the draft:\n\n## 0.3.0\n\n**Highlights**\n\n- **envd**: reverse WebSocket transport, so environments behind NAT can connect out.\n- **service**: durable run checkpoints — attempts resume where they stopped.\n\n**Fixes**\n\n- **harness**: preserve tool order when resuming a run."},
	}

	a.threads = append(a.threads, t1, t2, t3)
	a.current = t1.ID
}
