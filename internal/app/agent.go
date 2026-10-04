package app

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// send takes the draft, appends it to the thread, and starts the agent.
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
	a.save()
	at := len(th.Messages) - 1
	if a.backend == "builtin" {
		// No new user turn: the transcript already holds it.
		go a.runBuiltin(th, "", at)
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
	a.save()
	if a.win != nil {
		a.win.SetTitle("Codex — " + th.Title)
	}
	a.dispatch(th, prompt, at)
}

// dispatch hands a turn to the selected backend in the background.
func (a *app) dispatch(th *Thread, prompt string, at int) {
	switch {
	case a.backend == "builtin":
		go a.runBuiltin(th, prompt, at)
	case a.backend == "claude" && a.claudePath != "":
		go a.runClaude(th, prompt, at)
	case a.backend == "codex" && a.codexPath != "":
		go a.runCodex(th, prompt, at)
	default:
		go a.runDemo(th, prompt, at)
	}
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
		a.save()
		if a.wsOpen {
			a.refreshGit() // the agent may have changed files
		}
	})
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
func (a *app) runDemo(th *Thread, prompt string, at int) {
	ctx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel
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

// runCodex runs the real codex binary over exec --json, mapping its
// events to the thread's cards, and resumes the session on later turns.
func (a *app) runCodex(th *Thread, prompt string, at int) {
	ctx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel
	defer cancel()

	sandbox := []string{"read-only", "workspace-write", "danger-full-access"}[a.mode]
	args := []string{"exec", "--json", "--skip-git-repo-check", "-s", sandbox, "-m", a.model,
		"-c", "model_reasoning_effort=" + []string{"low", "medium", "high"}[a.effort],
		"-C", a.workdir}
	cmdEnv := os.Environ()
	// A custom provider is wired in as a codex model_providers entry: an
	// OpenAI-compatible endpoint whose key arrives through an env var.
	if p := a.provider(); p != nil && p.ID != "codex" && p.BaseURL != "" {
		id := p.ID
		args = append(args,
			"-c", fmt.Sprintf("model_provider=%q", id),
			"-c", fmt.Sprintf("model_providers.%s.name=%q", id, p.Name),
			"-c", fmt.Sprintf("model_providers.%s.base_url=%q", id, p.BaseURL),
			"-c", fmt.Sprintf("model_providers.%s.wire_api=%q", id, "chat"),
			"-c", fmt.Sprintf("model_providers.%s.env_key=%q", id, envKeyFor(id)),
		)
		cmdEnv = append(cmdEnv, envKeyFor(id)+"="+p.APIKey)
	}
	if th.CodexID != "" {
		args = append([]string{"exec", "resume", th.CodexID}, args[1:]...)
	}
	args = append(args, prompt)

	cmd := exec.CommandContext(ctx, a.codexPath, args...)
	cmd.Dir = a.workdir
	procGroupAttr(cmd)
	cmd.Env = cmdEnv
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		a.finish(th, at, "codex: "+err.Error())
		return
	}
	var stderrTail strings.Builder
	cmd.Stderr = &stderrTail
	if err := cmd.Start(); err != nil {
		a.finish(th, at, "codex: "+err.Error())
		return
	}
	a.codexPid = cmd.Process.Pid

	run := &codexRun{a: a, th: th, at: at, blocks: map[string]int{}}
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		run.handle(line)
	}
	sawEvent := run.sawEvent
	waitErr := cmd.Wait()
	errText := ""
	if ctx.Err() != nil {
		// Stopped by the user; keep what arrived.
	} else if !sawEvent || waitErr != nil {
		tail := strings.TrimSpace(stderrTail.String())
		if tail == "" && waitErr != nil {
			tail = waitErr.Error()
		}
		if tail != "" {
			errText = truncTitle("codex: "+tail, 400)
		}
	}
	a.finish(th, at, errText)
}

// codexEvent is one JSONL line of codex exec --json; only the fields the
// app maps are declared, everything else is ignored.
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
