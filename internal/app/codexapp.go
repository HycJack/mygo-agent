package app

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
)

// runCodex drives the codex CLI through `codex app-server --stdio`, the
// JSON-RPC transport (spec/cli-backends.md): policy rides thread/start,
// tool approvals arrive as server-requests and surface as approval cards.
// One fresh app-server per turn; session resume goes through thread/resume.
func (a *app) runCodex(ctx context.Context, th *Thread, turn harness.Turn, emit func(harness.Event)) {
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

	mode := turn.Mode
	sandbox := []string{"read-only", "workspace-write", "danger-full-access"}[mode]
	policy := "never"
	if mode == harness.ModeAgent {
		policy = "on-request"
	}

	// Config overrides ride the spawn args: the reasoning effort always,
	// and a custom provider as a model_providers entry whose key arrives
	// through an env var (the same wiring codex exec used).
	args := []string{"app-server", "--stdio",
		"-c", "model_reasoning_effort=" + []string{"low", "medium", "high"}[turn.Effort]}
	var cmdEnv []string
	if p := a.provider(); p != nil && p.ID != "codex" && p.BaseURL != "" {
		id := p.ID
		args = append(args,
			"-c", fmt.Sprintf("model_provider=%q", id),
			"-c", fmt.Sprintf("model_providers.%s.name=%q", id, p.Name),
			"-c", fmt.Sprintf("model_providers.%s.base_url=%q", id, p.BaseURL),
			"-c", fmt.Sprintf("model_providers.%s.wire_api=%q", id, "chat"),
			"-c", fmt.Sprintf("model_providers.%s.env_key=%q", id, envKeyFor(id)),
		)
		cmdEnv = append(os.Environ(), envKeyFor(id)+"="+p.APIKey)
	} else {
		cmdEnv = os.Environ()
	}

	cmd := exec.CommandContext(ctx, a.codexPath, args...)
	cmd.Dir = a.workdir
	procGroupAttr(cmd)
	cmd.Env = cmdEnv
	stdin, err := cmd.StdinPipe()
	if err != nil {
		a.finish(th, at, "codex: "+err.Error())
		return
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		a.finish(th, at, "codex: "+err.Error())
		return
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		a.finish(th, at, "codex: "+err.Error())
		return
	}
	a.codexPid = cmd.Process.Pid
	println("DBG started pid", a.codexPid)

	project := func(ev harness.Event) { a.applyEvent(th, at, "codex", ev) }
	run := newCodexRun(project)
	c := newCodexApp(stdin, stdout)

	// abort tears the transport down on every early exit: cancel kills
	// the process group and Wait reaps it, so no zombie outlives the run.
	abort := func(format string, args ...any) {
		cancel()
		c.close()
		_ = cmd.Wait()
		a.finish(th, at, "codex: "+fmt.Sprintf(format, args...))
	}
	c.onNotification = func(method string, params json.RawMessage) {
		a.codexNotify(run, method, params)
	}
	c.onInteraction = func(method string, id int, params json.RawMessage) {
		a.codexInteraction(ctx, th, at, run, c, method, id, params)
	}
	go c.readLoop()

	println("DBG before init")
	// initialize: opts into the experimental thread API.
	if _, err := c.call(ctx, "initialize", map[string]any{
		"clientInfo":   map[string]any{"name": "mygo-agent", "version": a.version},
		"capabilities": map[string]any{"experimentalApi": true},
	}); err != nil {
		println("DBG init failed:", err.Error())
		abort("initialize: %s", err.Error())
		return
	}

	// thread/start, or thread/resume for a known session.
	startParams := map[string]any{
		"cwd":            a.workdir,
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
		abort("%s: %s", method, err.Error())
		return
	}
	var started struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	_ = json.Unmarshal(raw, &started)
	if id := started.Thread.ID; id != "" {
		a.update(func() {
			th.CodexID = id
			a.saveThread(th)
		})
	}

	if _, err := c.call(ctx, "turn/start", map[string]any{
		"threadId": th.CodexID,
		"input":    []map[string]any{{"type": "text", "text": prompt}},
	}); err != nil {
		abort("turn/start: %s", err.Error())
		return
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
	errText := ""
	if ctx.Err() != nil {
		// Stopped by the user; keep what arrived.
	} else if !run.sawEvent || waitErr != nil {
		tail := strings.TrimSpace(stderr.String())
		if tail == "" && waitErr != nil {
			tail = waitErr.Error()
		}
		if tail != "" {
			errText = truncTitle("codex: "+tail, 400)
		}
	}
	a.finish(th, at, errText)
}

// codexApp is one app-server connection: JSON-RPC request/response
// matching over NDJSON, with notifications and server-requests dispatched
// to the caller's callbacks.
type codexApp struct {
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

func newCodexApp(stdin io.WriteCloser, stdout io.Reader) *codexApp {
	c := &codexApp{
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

func (c *codexApp) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
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
func (c *codexApp) reply(id int, result any) {
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
func (c *codexApp) declineAll() {
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

func (c *codexApp) close() {
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
func (c *codexApp) readLoop() {
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

func (c *codexApp) settleTurn() {
	c.turnOnce.Do(func() { close(c.turnDone) })
}

func frameID(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	default:
		return 0, false
	}
}

// codexInteraction maps one approval server-request onto the shared
// approval card and writes the decision back.
func (a *app) codexInteraction(ctx context.Context, th *Thread, at int, run *codexRun, c *codexApp, method string, id int, params json.RawMessage) {
	req := harness.ApprovalRequest{Call: harness.ToolCall{ID: fmt.Sprintf("codex-%d", id)}}
	switch method {
	case "item/commandExecution/requestApproval":
		var p struct {
			Command string `json:"command"`
			Reason  string `json:"reason"`
		}
		_ = json.Unmarshal(params, &p)
		req.Summary = "$ " + truncTitle(p.Command, 200)
		req.Reason = p.Reason
	case "item/fileChange/requestApproval":
		var p struct {
			GrantRoot string `json:"grantRoot"`
			Reason    string `json:"reason"`
		}
		_ = json.Unmarshal(params, &p)
		req.Summary = "apply file changes: " + truncTitle(p.GrantRoot, 160)
		req.Reason = p.Reason
	default:
		// Permission-profile escalation is answered with the empty
		// profile — a deny — and surfaced as a note (spec/cli-backends.md).
		c.reply(id, map[string]any{"permissions": map[string]any{}, "scope": "turn"})
		a.applyEvent(th, at, "codex", harness.Event{Kind: harness.EventNote, Text: "codex asked for wider permissions; declined"})
		return
	}
	decision := a.waitForApproval(ctx, th, at, req)
	value := "decline"
	if decision.Approved {
		value = "accept"
	}
	c.reply(id, map[string]any{"decision": value})
}

// codexNotify streams one app-server notification into the thread,
// reusing codexRun's item→card mapping through handleEvent.
func (a *app) codexNotify(run *codexRun, method string, params json.RawMessage) {
	run.sawEvent = true
	switch {
	case method == "item/agentMessage/delta":
		var p struct {
			Delta string `json:"delta"`
		}
		if json.Unmarshal(params, &p) == nil && p.Delta != "" {
			run.emit(harness.Event{Kind: harness.EventText, TextDelta: p.Delta})
		}
	case method == "item/reasoning/textDelta" || method == "item/reasoning/summaryTextDelta":
		var p struct {
			ItemID string `json:"itemId"`
			Delta  string `json:"delta"`
		}
		if json.Unmarshal(params, &p) == nil && p.Delta != "" {
			if run.reasoning == nil {
				run.reasoning = map[string]*strings.Builder{}
			}
			b := run.reasoning[p.ItemID]
			if b == nil {
				b = &strings.Builder{}
				run.reasoning[p.ItemID] = b
			}
			b.WriteString(p.Delta)
		}
	case method == "item/started" || method == "item/completed":
		// The app-server item wire is camelCase, unlike exec --json's
		// snake_case codexItem tags; decode through the alias and map.
		var p struct {
			Item struct {
				ID               string   `json:"id"`
				Type             string   `json:"type"`
				Text             string   `json:"text"`
				Summary          []string `json:"summary"`
				Command          string   `json:"command"`
				Status           string   `json:"status"`
				ExitCode         *int     `json:"exitCode"`
				Diff             string   `json:"diff"`
				AggregatedOutput string   `json:"aggregatedOutput"`
				Changes          []struct {
					Path string `json:"path"`
					Kind string `json:"kind"`
				} `json:"changes"`
			} `json:"item"`
		}
		if json.Unmarshal(params, &p) != nil {
			return
		}
		it := &p.Item
		// A reasoning item that completed with no text of its own falls
		// back to the deltas buffered for it.
		if it.Type == "reasoning" && strings.TrimSpace(it.Text) == "" && len(it.Summary) == 0 {
			if b := run.reasoning[it.ID]; b != nil {
				it.Text = b.String()
			}
		}
		item := codexItem{
			ID:               it.ID,
			Type:             it.Type,
			Text:             it.Text,
			Summary:          it.Summary,
			Command:          it.Command,
			Status:           it.Status,
			ExitCode:         it.ExitCode,
			Diff:             it.Diff,
			AggregatedOutput: it.AggregatedOutput,
		}
		for _, ch := range it.Changes {
			item.Changes = append(item.Changes, struct {
				Path string `json:"path"`
				Kind string `json:"kind"`
			}{ch.Path, ch.Kind})
		}
		run.handleEvent(codexEvent{Item: &item})
	case method == "error":
		var p struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(params, &p)
		run.handleEvent(codexEvent{Type: "error", Message: p.Message})
	}
}
