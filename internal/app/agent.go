package app

import (
	"context"

	"fmt"
	"mygo-agent/internal/harness"
	"os/exec"
	"strings"
	"time"
)

// send takes the draft, appends it to the thread, and starts the harness.
func (a *app) send() {
	prompt := strings.TrimSpace(a.draft)
	if prompt == "" || a.running {
		return
	}
	a.draft = ""
	th := a.currentThread()
	if th == nil {
		th = a.createThread()
	}
	a.startTurn(th, prompt)
}

// resend re-runs a prompt the user already sent, as a fresh turn.
func (a *app) resend(th *Thread, text string) {
	if a.running || strings.TrimSpace(text) == "" {
		return
	}
	a.startTurn(th, text)
}

// regenerate drops the thread's last assistant reply and runs the same
// turn again. The built-in backend rewinds its ChatLog to where the
// turn started, so the retry sees the same history.
func (a *app) regenerate(th *Thread) {
	if a.running || len(th.Messages) == 0 {
		return
	}
	if th.Messages[len(th.Messages)-1].Role != "assistant" {
		return
	}
	last := th.Messages[len(th.Messages)-1]
	logAt := last.LogAt
	th.Messages = th.Messages[:len(th.Messages)-1]
	prompt := ""
	for i := len(th.Messages) - 1; i >= 0; i-- {
		if th.Messages[i].Role == "user" {
			prompt = th.Messages[i].Text
			break
		}
	}
	if a.backend == "builtin" {
		th.ChatLog = th.ChatLog[:min(int(logAt), len(th.ChatLog))]
	}
	now := time.Now()
	th.Messages = append(th.Messages, Message{ID: uid(), Role: "assistant", Running: true, At: now, LogAt: logAt})
	a.running = true
	a.saveThread(th)
	at := len(th.Messages) - 1
	if a.backend == "builtin" {
		// No new user turn: the transcript already holds it.
		go a.runBuiltinLegacy(th, "", at)
		return
	}
	a.dispatch(th, prompt, at)
}

// startTurn appends the user's prompt and a placeholder reply, then
// dispatches to the backend.
func (a *app) startTurn(th *Thread, prompt string) {
	now := time.Now()
	th.Messages = append(th.Messages, Message{ID: uid(), Role: "user", Text: prompt, At: now})
	if th.Title == "" {
		th.Title = truncTitle(prompt, 44)
	}
	th.Updated = now
	th.Messages = append(th.Messages, Message{ID: uid(), Role: "assistant", Running: true, At: now})
	a.running = true
	a.focusComposer = true
	at := len(th.Messages) - 1
	a.saveThread(th)
	if a.win != nil {
		a.win.SetTitle("Codex — " + th.Title)
	}
	a.dispatch(th, prompt, at)
}

// stop cancels the run; what the agent said so far stays.
func (a *app) stop() {
	if a.cancel != nil {
		a.cancel()
		a.cancel = nil
	}
}

// finish marks the running reply done and saves the thread.
func (a *app) finish(th *Thread, at int, errText string) {
	a.update(func() {
		if cur := a.byID(th.ID); cur == nil || at >= len(cur.Messages) {
			a.running = false
			return
		}
		m := &th.Messages[at]
		m.Running = false
		if errText != "" {
			m.Blocks = append(m.Blocks, Block{Type: "error", Text: errText})
		}
		if m.Text == "" && errText == "" && len(m.Blocks) == 0 {
			m.Text = "(no response)"
		}
		th.Updated = time.Now()
		a.running = false
		a.cancel = nil
		a.saveThread(th)
		if a.wsOpen {
			a.refreshGit() // the agent may have changed files
		}
	})
}

// runBuiltinLegacy is the test shim: a background context, a fresh turn.
func (a *app) runBuiltinLegacy(th *Thread, prompt string, at int) {
	go func() {
		turn := a.turnFor(th, prompt)
		h := builtinHarness{a: a, th: th, at: at}
		_ = h.Run(context.Background(), turn, func(ev harness.Event) {
			a.applyEvent(th, at, h.Kind(), ev)
		})
	}()
}

// adoptCancel wires a parent context into the app's stop path: the
// returned cancel runs on stop and at turn end.
func (a *app) adoptCancel(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	a.cancel = cancel
	return ctx, cancel
}

// reply ensures the running message exists at at and returns it.
func reply(th *Thread, at int) *Message {
	if at < len(th.Messages) {
		return &th.Messages[at]
	}
	return nil
}

// runDemo is the built-in agent: it thinks, runs one real command,
// writes a plan with code and a sample patch, streaming as it goes.
func (a *app) runDemo(ctx context.Context, th *Thread, turn harness.Turn, emit func(harness.Event)) {
	prompt, at := turn.Prompt, -1
	for i := range th.Messages {
		if th.Messages[i].Running {
			at = i
		}
	}
	if at < 0 {
		at = len(th.Messages) - 1
	}
	ctx, cancel := a.adoptCancel(ctx)
	defer cancel()

	a.update(func() {
		if m := reply(th, at); m != nil {
			m.Blocks = append(m.Blocks, Block{Type: "reasoning", Text: "Reading the workspace and thinking about how to approach this."})
		}
	})
	if !sleep(ctx, 700*time.Millisecond) {
		a.finish(th, at, "")
		return
	}

	// Run one real, read-only command so the card shows true output.
	cmdLine := "git status --short"
	a.update(func() {
		if m := reply(th, at); m != nil {
			m.Blocks = append(m.Blocks, Block{Type: "command", Text: cmdLine, Running: true, Exit: -1, Open: false})
		}
	})
	started := time.Now()
	ctxCmd, cancelCmd := context.WithTimeout(ctx, 4*time.Second)
	out, err := exec.CommandContext(ctxCmd, "git", "-C", a.workdir, "status", "--short").CombinedOutput()
	cancelCmd()
	a.update(func() {
		if m := reply(th, at); m != nil && len(m.Blocks) > 0 {
			b := &m.Blocks[len(m.Blocks)-1]
			b.Ms = time.Since(started).Milliseconds()
			b.Running = false
			b.Output = string(out)
			if err != nil {
				b.Exit = 1
			} else {
				b.Exit = 0
			}
		}
	})
	if !sleep(ctx, 250*time.Millisecond) {
		a.finish(th, at, "")
		return
	}

	a.update(func() {
		if m := reply(th, at); m != nil {
			m.Blocks = append(m.Blocks, demoDiff()...)
		}
	})

	text := demoReply(prompt)
	chunks := splitChunks(text, 4)
	for _, ch := range chunks {
		if !sleep(ctx, 16*time.Millisecond) {
			a.finish(th, at, "")
			return
		}
		a.update(func() {
			if m := reply(th, at); m != nil {
				m.Text += ch
			}
		})
	}
	a.finish(th, at, "")
}

// codexEvent is one JSONL line of codex exec --json or one app-server
// item; only the fields the app maps are declared, everything else is
// ignored.
type codexEvent struct {
	Type     string     `json:"type"`
	ThreadID string     `json:"thread_id"`
	Message  string     `json:"message"`
	Delta    string     `json:"delta"`
	Item     *codexItem `json:"item"`
}

type codexItem struct {
	ID               string   `json:"id"`
	Type             string   `json:"type"`
	Command          string   `json:"command"`
	Status           string   `json:"status"`
	AggregatedOutput string   `json:"aggregated_output"`
	ExitCode         *int     `json:"exit_code"`
	Text             string   `json:"text"`
	Summary          []string `json:"summary"`
	Diff             string   `json:"diff"`
	Changes          []struct {
		Path string `json:"path"`
		Kind string `json:"kind"`
	} `json:"changes"`
}

// sleep waits for d, returning false if the run was stopped first.
func sleep(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

// splitChunks cuts s into pieces of about n bytes, never mid-rune.
func splitChunks(s string, n int) []string {
	var chunks []string
	r := []rune(s)
	for i := 0; i < len(r); i += n {
		end := i + n
		if end > len(r) {
			end = len(r)
		}
		chunks = append(chunks, string(r[i:end]))
	}
	return chunks
}

// demoDiff is the sample patch the demo agent applies.
func demoDiff() []Block {
	return []Block{{
		Type: "diff",
		File: "internal/agent/runner.go",
		Add:  2,
		Del:  1,
		Open: true,
		Lines: []DiffLine{
			{Kind: ' ', Text: "func (r *Runner) Run(ctx context.Context, task Task) error {"},
			{Kind: ' ', Text: "\tratelimit.Wait(ctx)"},
			{Kind: '-', Text: "\treturn r.exec(ctx, task)"},
			{Kind: '+', Text: "\terr := r.exec(ctx, task)"},
			{Kind: '+', Text: "\treturn errors.Wrap(err, \"exec task\")"},
			{Kind: ' ', Text: "}"},
		},
	}}
}

// demoReply writes the demo agent's answer to a prompt.
func demoReply(prompt string) string {
	short := truncTitle(prompt, 60)
	return fmt.Sprintf(`Here's my plan for **%s**:

- First I'd map the area of the codebase involved, then make the smallest change that works.
- I run the tests after each step, and I stop for your review before anything irreversible.

The core change looks like this:

`+"```go"+`
func (r *Runner) Run(ctx context.Context, task Task) error {
	ratelimit.Wait(ctx)
	return errors.Wrap(r.exec(ctx, task), "exec task")
}
`+"```"+`

Want me to apply it? I can also extend the plan with tests and a changelog entry if you'd like.

*Demo agent — switch the backend at the bottom left to run the real* `+"`codex exec`"+` *CLI.*`, short)
}
