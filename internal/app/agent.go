package app

import (
	"context"

	"strings"
	"time"
)

// send takes the draft, appends it to the thread, and starts the harness.
func (a *app) send() {
	prompt := strings.TrimSpace(a.draft)
	if prompt == "" || a.running {
		return
	}
	a.setDraft("")
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
	promptForSend := ""
	for i := len(th.Messages) - 1; i >= 0; i-- {
		if th.Messages[i].Role == "user" {
			promptForSend = th.Messages[i].Text
			break
		}
	}
	if a.backend == "builtin" {
		th.ChatLog = th.ChatLog[:min(int(logAt), len(th.ChatLog))]
	}
	th.invalidateDiffCount() // the messages were rewound
	now := time.Now()
	th.Messages = append(th.Messages, Message{ID: uid(), Role: "assistant", Running: true, At: now, LogAt: logAt})
	a.running = true
	a.saveThread(th)
	at := len(th.Messages) - 1
	// The built-in transcript already holds the user's turn — re-sending
	// the prompt would append it twice. The CLI backends start from their
	// own session and need the prompt again.
	if a.backend == "builtin" {
		promptForSend = ""
	}
	a.dispatch(th, promptForSend, at)
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

// setCancel swaps the run's cancel func (see cancelMu).
func (a *app) setCancel(c context.CancelFunc) {
	a.cancelMu.Lock()
	a.cancel = c
	a.cancelMu.Unlock()
}

// stop cancels the run; what the agent said so far stays.
func (a *app) stop() {
	a.cancelMu.Lock()
	c := a.cancel
	a.cancel = nil
	a.cancelMu.Unlock()
	if c != nil {
		c()
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
		a.setCancel(nil)
		a.saveThread(th)
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
