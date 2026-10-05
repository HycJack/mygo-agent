// Package codex adapts the codex CLI through `codex app-server --stdio`,
// the JSON-RPC transport (spec/cli-backends.md): policy rides
// thread/start, tool approvals arrive as server-requests and surface
// through the turn's approval callback. One fresh app-server per turn;
// session resume goes through thread/resume.
package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"mygo-agent/internal/harness"
	"mygo-agent/internal/harness/cli"
)

// Harness runs one turn against the codex binary at Bin.
type Harness struct{ Bin string }

// New builds a codex harness for the binary at bin.
func New(bin string) *Harness { return &Harness{Bin: bin} }

// Kind implements harness.Harness.
func (h *Harness) Kind() string { return "codex" }

// Run drives one turn to completion. It returns nil for a clean turn or
// a stopped one (whatever arrived stays emitted); a non-nil error's
// message is the failure text for the reply card.
func (h *Harness) Run(ctx context.Context, turn harness.Turn, emit func(harness.Event)) error {
	prompt := turn.Prompt

	mode := turn.Mode
	sandbox := []string{"read-only", "workspace-write", "danger-full-access"}[mode]
	policy := "never"
	if mode == harness.ModeAgent {
		policy = "on-request"
	}

	// Config overrides ride the spawn args: the reasoning effort always,
	// and a custom endpoint as a model_providers entry whose key arrives
	// through an env var.
	args := []string{"app-server", "--stdio",
		"-c", "model_reasoning_effort=" + []string{"low", "medium", "high"}[turn.Effort]}
	var cmdEnv []string
	if ep := turn.Endpoint; ep != nil && ep.BaseURL != "" {
		id := ep.ID
		args = append(args,
			"-c", fmt.Sprintf("model_provider=%q", id),
			"-c", fmt.Sprintf("model_providers.%s.name=%q", id, ep.Name),
			"-c", fmt.Sprintf("model_providers.%s.base_url=%q", id, ep.BaseURL),
			"-c", fmt.Sprintf("model_providers.%s.wire_api=%q", id, "chat"),
			"-c", fmt.Sprintf("model_providers.%s.env_key=%q", id, EnvKey(id)),
		)
		cmdEnv = append(os.Environ(), EnvKey(id)+"="+ep.APIKey)
	} else {
		cmdEnv = os.Environ()
	}

	cmd := exec.CommandContext(ctx, h.Bin, args...)
	cmd.Dir = turn.Workdir
	cli.ProcGroupAttr(cmd)
	cmd.Env = cmdEnv
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("codex: %s", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("codex: %s", err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("codex: %s", err)
	}

	project := func(ev harness.Event) {
		if emit != nil {
			emit(ev)
		}
	}
	run := newRun(turn, project)
	c := newApp(stdin, stdout)

	// abort tears the transport down on every early exit: cancel kills
	// the process group and Wait reaps it, so no zombie outlives the run.
	abort := func(format string, args ...any) error {
		cancel := cmd.Cancel
		if cancel != nil {
			_ = cancel()
		}
		c.close()
		_ = cmd.Wait()
		return fmt.Errorf("codex: "+format, args...)
	}
	c.onNotification = func(method string, params json.RawMessage) {
		run.notify(method, params)
	}
	c.onInteraction = func(method string, id int, params json.RawMessage) {
		h.interact(ctx, run, c, method, id, params)
	}
	go c.readLoop()

	// initialize: opts into the experimental thread API.
	if _, err := c.call(ctx, "initialize", map[string]any{
		"clientInfo":   map[string]any{"name": "mygo-agent", "version": "1"},
		"capabilities": map[string]any{"experimentalApi": true},
	}); err != nil {
		return abort("initialize: %s", err.Error())
	}

	// thread/start, or thread/resume for a known session.
	startParams := map[string]any{
		"cwd":            turn.Workdir,
		"approvalPolicy": policy,
		"sandbox":        sandbox,
	}
	method := "thread/start"
	if turn.SessionID != "" {
		method = "thread/resume"
		startParams["threadId"] = turn.SessionID
	}
	if turn.Model != "" {
		startParams["model"] = turn.Model
	}
	raw, err := c.call(ctx, method, startParams)
	if err != nil {
		return abort("%s: %s", method, err.Error())
	}
	var started struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	_ = json.Unmarshal(raw, &started)
	if id := started.Thread.ID; id != "" {
		project(harness.Event{Kind: harness.EventSession, SessionID: id})
	}

	threadID := started.Thread.ID
	if threadID == "" {
		threadID = turn.SessionID // a resume answer may omit the thread
	}
	if _, err := c.call(ctx, "turn/start", map[string]any{
		"threadId": threadID,
		"input":    []map[string]any{{"type": "text", "text": prompt}},
	}); err != nil {
		return abort("turn/start: %s", err.Error())
	}

	// The stream carries the turn; wait for it to settle or the run to
	// be stopped, then give the reader a bounded moment to apply the
	// trailing events (a completed turn can outrun its last item).
	select {
	case <-c.turnDone:
	case <-ctx.Done():
	}
	if ctx.Err() == nil {
		select {
		case <-c.streamDone:
		case <-time.After(250 * time.Millisecond):
		}
	}
	c.close()
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		return nil // stopped by the user; keep whatever arrived
	}
	if run.sawEvent && waitErr == nil {
		return nil
	}
	tail := strings.TrimSpace(stderr.String())
	if tail == "" && waitErr != nil {
		tail = waitErr.Error()
	}
	if tail == "" {
		return nil
	}
	return fmt.Errorf("codex: %s", tail)
}

// EnvKey is the environment variable a custom endpoint's API key is
// handed to the codex CLI through (its model_providers.<id>.env_key).
func EnvKey(providerID string) string {
	var b strings.Builder
	b.WriteString("MYGO_PROVIDER_")
	for _, r := range strings.ToUpper(providerID) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	b.WriteString("_API_KEY")
	return b.String()
}

// interact maps one approval server-request onto the turn's approval
// callback and writes the decision back.
func (h *Harness) interact(ctx context.Context, run *run, c *app, method string, id int, params json.RawMessage) {
	req := harness.ApprovalRequest{Call: harness.ToolCall{ID: fmt.Sprintf("codex-%d", id)}}
	switch method {
	case "item/commandExecution/requestApproval":
		var p struct {
			Command string `json:"command"`
			Reason  string `json:"reason"`
		}
		_ = json.Unmarshal(params, &p)
		req.Summary = "$ " + cli.Trunc(p.Command, 200)
		req.Reason = p.Reason
	case "item/fileChange/requestApproval":
		var p struct {
			GrantRoot string `json:"grantRoot"`
			Reason    string `json:"reason"`
		}
		_ = json.Unmarshal(params, &p)
		req.Summary = "apply file changes: " + cli.Trunc(p.GrantRoot, 160)
		req.Reason = p.Reason
	default:
		// Permission-profile escalation is answered with the empty
		// profile — a deny — and surfaced as a note (spec/cli-backends.md).
		c.reply(id, map[string]any{"permissions": map[string]any{}, "scope": "turn"})
		run.emit(harness.Event{Kind: harness.EventNote, Text: "codex asked for wider permissions; declined"})
		return
	}
	decision := harness.ApprovalDecision{Reason: "no approval handler is configured"}
	if run.turn.OnApproval != nil {
		decision = run.turn.OnApproval(ctx, req)
	}
	value := "decline"
	if decision.Approved {
		value = "accept"
	}
	c.reply(id, map[string]any{"decision": value})
}

// codexApp is one app-server connection: JSON-RPC request/response
// matching over NDJSON, with notifications and server-requests dispatched
// to the caller's callbacks.
type app struct {
	stdin     io.WriteCloser
	sc        *bufio.Scanner
	nextID    int
	turnDone  chan struct{}
	turnOnce  sync.Once
	closeOnce sync.Once

	mu           sync.Mutex
	closed       bool
	pending      map[int]chan json.RawMessage
	interactions map[int]bool
	streamDone   chan struct{} // closed when the reader exits

	onNotification func(method string, params json.RawMessage)
	onInteraction  func(method string, id int, params json.RawMessage)
}

func newApp(stdin io.WriteCloser, stdout io.Reader) *app {
	c := &app{
		stdin:        stdin,
		turnDone:     make(chan struct{}),
		streamDone:   make(chan struct{}),
		pending:      map[int]chan json.RawMessage{},
		interactions: map[int]bool{},
	}
	c.sc = bufio.NewScanner(stdout)
	c.sc.Buffer(make([]byte, 64*1024), 16<<20)
	return c
}

func (c *app) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, fmt.Errorf("codex: connection closed")
	}
	c.nextID++
	id := c.nextID
	ch := make(chan json.RawMessage, 1)
	c.pending[id] = ch
	req, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	_, werr := c.stdin.Write(append(req, '\n'))
	c.mu.Unlock()
	if werr != nil {
		return nil, werr
	}
	select {
	case frame := <-ch:
		if frame == nil {
			return nil, fmt.Errorf("codex: connection closed before %s answered", method)
		}
		var res struct {
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(frame, &res) != nil {
			return nil, fmt.Errorf("codex: malformed response to %s", method)
		}
		if res.Error != nil {
			return nil, fmt.Errorf("codex: %s: %d %s", method, res.Error.Code, res.Error.Message)
		}
		return res.Result, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(60 * time.Second):
		return nil, fmt.Errorf("codex: %s timed out", method)
	}
}

// reply answers a server-request exactly once per id; the delete makes
// timeout/cancel/duplicate races settle on the first reply
// (spec/cli-backends.md).
func (c *app) reply(id int, result any) {
	c.mu.Lock()
	if !c.interactions[id] {
		c.mu.Unlock()
		return
	}
	delete(c.interactions, id)
	if c.closed {
		c.mu.Unlock()
		return
	}
	res, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	_, _ = c.stdin.Write(append(res, '\n'))
	c.mu.Unlock()
}

// declineAll answers every pending interaction with a decline — the
// timeout and cancellation path.
func (c *app) declineAll() {
	c.mu.Lock()
	ids := make([]int, 0, len(c.interactions))
	for id := range c.interactions {
		ids = append(ids, id)
	}
	c.mu.Unlock()
	for _, id := range ids {
		c.reply(id, map[string]any{"decision": "decline"})
	}
}

func (c *app) close() {
	c.declineAll()
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.closed = true
		c.mu.Unlock()
	})
}

// readLoop demultiplexes the stream into responses, notifications and
// server-requests. Handlers are registered before turn/start
// (spec/cli-backends.md), so a server-request can never arrive unhandled.
func (c *app) readLoop() {
	for c.sc.Scan() {
		line := strings.TrimSpace(c.sc.Text())
		if line == "" {
			continue
		}
		var frame struct {
			ID     any             `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		if json.Unmarshal([]byte(line), &frame) != nil {
			continue
		}
		switch {
		case frame.Method != "" && frame.ID != nil:
			id, ok := frameID(frame.ID)
			if !ok {
				continue
			}
			c.mu.Lock()
			c.interactions[id] = true
			c.mu.Unlock()
			if c.onInteraction != nil {
				c.onInteraction(frame.Method, id, frame.Params)
			}
		case frame.Method != "":
			if frame.Method == "turn/completed" || frame.Method == "turn/failed" {
				c.settleTurn()
			}
			if c.onNotification != nil {
				c.onNotification(frame.Method, frame.Params)
			}
		case frame.ID != nil:
			if id, ok := frameID(frame.ID); ok {
				c.mu.Lock()
				if ch := c.pending[id]; ch != nil {
					delete(c.pending, id)
					ch <- frameOrError([]byte(line), frame.Error)
				}
				c.mu.Unlock()
			}
		}
	}
	// The process is gone: unstick the turn wait and every pending call.
	c.settleTurn()
	close(c.streamDone)
	c.mu.Lock()
	c.closed = true
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
	c.mu.Unlock()
}

// frameOrError folds a JSON-RPC error member into the response frame so
// call() can report it: the frame travels whole, call() unwraps result
// and error itself.
func frameOrError(frame []byte, errMember json.RawMessage) json.RawMessage {
	if len(errMember) > 0 {
		var probe struct {
			Result json.RawMessage `json:"result"`
		}
		if json.Unmarshal(frame, &probe) != nil || len(probe.Result) == 0 {
			return []byte(`{"error":` + string(errMember) + `}`)
		}
	}
	return frame
}

func (c *app) settleTurn() {
	c.turnOnce.Do(func() { close(c.turnDone) })
}

func frameID(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int:
		return int(n), true
	default:
		return 0, false
	}
}
