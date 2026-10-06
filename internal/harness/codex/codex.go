// Package codex adapts the codex CLI through `codex app-server --stdio`,
// the JSON-RPC transport (spec/cli-backends.md): policy rides
// thread/start, tool approvals arrive as server-requests and surface
// through the turn's approval callback. One fresh app-server per turn;
// session resume goes through thread/resume.
package codex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"mygo-agent/internal/harness"
	"mygo-agent/internal/harness/cli"
)

// Harness runs one turn against the codex binary at Bin.
type Harness struct {
	Bin string
	// ApprovalTimeout bounds each approval wait; zero means the shared
	// harness.DefaultApprovalTimeout. Tests shorten it.
	ApprovalTimeout time.Duration
}

// New builds a codex harness for the binary at bin.
func New(bin string) *Harness { return &Harness{Bin: bin} }

// Kind implements harness.Harness.
func (h *Harness) Kind() string { return "codex" }

// Run drives one turn to completion. It returns nil for a clean turn or
// a stopped one (whatever arrived stays emitted); a non-nil error's
// message is the failure text for the reply card.
func (h *Harness) Run(ctx context.Context, turn harness.Turn, emit func(harness.Event)) error {
	prompt := turn.Prompt

	// Mode and Effort are plain ints on Turn and Mode is clamped nowhere
	// in the repo, so both are switched rather than indexed: an
	// out-of-range value must fail closed, not panic the dispatch
	// goroutine (there is no recover anywhere in the process).
	sandbox := "read-only"
	policy := "never"
	switch turn.Mode {
	case harness.ModeAgent:
		sandbox, policy = "workspace-write", "on-request"
	case harness.ModeFull:
		sandbox = "danger-full-access"
	}

	// Config overrides ride the spawn args: the reasoning effort always,
	// and a custom endpoint as a model_providers entry whose key arrives
	// through an env var. spawnArgs owns that, including the wire the
	// provider speaks and the rejection of a chat-completions endpoint
	// newer codex CLIs refuse to load.
	args, cmdEnv, err := spawnArgs(turn)
	if err != nil {
		return err
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
	run.approvalTimeout = h.ApprovalTimeout
	c := newApp(stdin, stdout)

	// abort tears the transport down on every early exit: cancel kills
	// the process group and Wait reaps it, so no zombie outlives the run.
	// seal keeps the reader goroutine from emitting into a run that is
	// about to return.
	//
	// What the CLI said on stderr is the diagnosis, and it is only
	// available after Wait: a server that dies before answering looks
	// identical from the wire ("connection closed") whether it rejected a
	// config override, refused the sandbox, or crashed. Reporting the
	// transport's own words alongside it is the difference between a user
	// who can act on the failure and one who can only retry it.
	abort := func(format string, args ...any) error {
		_ = stdin.Close()
		cancel := cmd.Cancel
		if cancel != nil {
			_ = cancel()
		}
		c.close()
		_ = cmd.Wait()
		run.seal()
		err := fmt.Errorf(format, args...)
		// The transport's messages already carry the "codex:" prefix, so
		// this does not add a second one.
		if tail := strings.TrimSpace(stderr.String()); tail != "" {
			return fmt.Errorf("%w — %s", err, cli.Trunc(tail, 600))
		}
		return err
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
	// turn/start is turn-scoped: its answer only acknowledges the turn,
	// and the turn itself streams notifications until turnDone settles
	// it. A fixed call cap here would kill a legitimately long turn, so
	// this one call waits on the caller's context alone.
	if _, err := c.callWithin(ctx, "turn/start", map[string]any{
		"threadId": threadID,
		"input":    []map[string]any{{"type": "text", "text": prompt}},
	}, 0); err != nil {
		return abort("turn/start: %s", err.Error())
	}

	// The stream carries the turn; wait for it to settle or the run to
	// be stopped.
	select {
	case <-c.turnDone:
	case <-ctx.Done():
	}
	// Give the reader a bounded moment to apply the trailing events — a
	// completed turn can outrun its last item, and the frame that settles
	// the turn is often not the last one on the pipe. The app-server stays
	// resident after the turn, so its reader is parked in Scan() with
	// nothing left to read; waiting for it to EXIT would cost the whole
	// grace on every turn, which is the common case and the expensive one.
	// Draining is therefore bounded by the frames in flight, not by the
	// reader's exit: seal() below is what guarantees no event outlives Run.
	c.drain(readerGrace, readerQuiet)
	// The app-server is a resident process: it exits only on stdin EOF, so
	// Reap closes it first. A server that stays anyway is torn down by
	// process group after a bounded grace — without that bound, WaitDelay
	// only engages once the context is already done and a lingering
	// app-server would hold the turn forever. This is the same guard
	// claude and pi use (spec/cli-backends.md, the claude lesson).
	waitErr, killed := cli.Reap(cmd, stdin)
	c.close()
	// stdin is closed now, so the reader's next Scan returns EOF and it
	// exits. Waiting for that — not for a timeout — is what keeps a late
	// frame from reaching the Host after finish() marked the thread done.
	c.waitReader(readerGrace)
	run.seal()
	fail, saw, garbage := run.outcome()
	if ctx.Err() != nil {
		return nil // stopped by the user; keep whatever arrived
	}
	if fail != "" {
		return fmt.Errorf("codex: %s", fail)
	}
	// A group teardown is a cleanup, not a failure — a server that had to
	// be killed after settling still produced its turn.
	if saw && (waitErr == nil || killed) {
		return nil
	}
	if !saw && garbage > 0 {
		// Frames arrived but nothing in them parsed. Reporting that as
		// a clean turn would drop the model's answer silently.
		return fmt.Errorf("codex: unreadable app-server output (%d unreadable frames)", garbage)
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

// readerGrace bounds how long the run waits for its reader goroutine to
// stop, both while draining the tail of a settled turn and before
// returning. A reader that overruns is dropped, not waited on: stalling
// the UI behind a stuck pipe is worse than losing the last frame.
const readerGrace = 250 * time.Millisecond

// readerQuiet is how long the reader must be idle before the stream counts
// as drained. It only has to outlast the gap between two frames of one
// turn, which is far below it even on a slow machine.
const readerQuiet = 20 * time.Millisecond

// EnvKey is the environment variable a custom endpoint's API key is
// handed to the codex CLI through (its model_providers.<id>.env_key).
//
// Every byte that is not a letter or a digit is escaped as _XX, and case
// is left alone. The obvious mapping — upper-case and fold everything
// else to _ — collides: "a-b", "a_b", "AB" and "ab" all land on the same
// key, and two configured endpoints would then share one secret. Escaping
// keeps the mapping injective (and the name a valid env var), so a stored
// key always reaches the provider it was configured for.
func EnvKey(providerID string) string {
	var b strings.Builder
	b.WriteString("MYGO_PROVIDER_")
	for i := 0; i < len(providerID); i++ {
		c := providerID[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
			b.WriteByte(c)
		default:
			b.WriteByte('_')
			b.WriteString(strings.ToUpper(strconv.FormatInt(int64(c), 16)))
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
		// Redact before truncating (permissions.go): the card is drawn on
		// screen and persisted into the thread file, and a cut can hide a
		// secret's tail behind the ellipsis. The summary is model-proposed
		// text — a `curl -H "Authorization: Bearer …"` must not survive
		// either way.
		req.Summary = "$ " + cli.Trunc(cli.Redact(p.Command), 200)
		req.Reason = cli.Redact(p.Reason)
	case "item/fileChange/requestApproval":
		var p struct {
			GrantRoot string `json:"grantRoot"`
			Reason    string `json:"reason"`
		}
		_ = json.Unmarshal(params, &p)
		req.Summary = "apply file changes: " + cli.Trunc(cli.Redact(p.GrantRoot), 160)
		req.Reason = cli.Redact(p.Reason)
	default:
		// Permission-profile escalation is answered with the empty
		// profile — a deny — and surfaced as a note (spec/cli-backends.md).
		c.reply(id, map[string]any{"permissions": map[string]any{}, "scope": "turn"})
		run.send(harness.Event{Kind: harness.EventNote, Text: "codex asked for wider permissions; declined"})
		return
	}
	decision := harness.ApprovalDecision{Reason: "no approval handler is configured"}
	if run.turn.OnApproval != nil {
		// Every approval wait is bounded (spec/approvals.md): a card on a
		// live run must never sit unanswered. Expiry settles the request
		// as a decline inside the stream — not a failed turn — so the
		// card and the model both learn why.
		actx, cancel := harness.ApprovalContext(ctx, run.approvalTimeout)
		decision = run.turn.OnApproval(actx, req)
		if actx.Err() != nil {
			// A decision that arrives after the deadline is not a decision.
			decision = harness.ApprovalDecision{Reason: harness.ApprovalReason(actx)}
		}
		cancel()
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

	mu  sync.Mutex
	wmu sync.Mutex // serializes stdin writes on its own: the
	// reader delivers responses under mu, so holding mu across a write
	// that blocks would stall both pipes at once and deadlock them.
	closed       bool
	inflight     int // frames read but not yet handed to a handler
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

// call issues one request under the standard cap.
func (c *app) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	return c.callWithin(ctx, method, params, callTimeout)
}

// callWithin issues one request, giving up after limit (0: wait on ctx
// alone). Setup calls must answer promptly; turn/start is exempt, because
// capping it would cut a legitimately long turn short.
func (c *app) callWithin(ctx context.Context, method string, params any, limit time.Duration) (json.RawMessage, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, fmt.Errorf("codex: connection closed")
	}
	c.nextID++
	id := c.nextID
	ch := make(chan json.RawMessage, 1)
	c.pending[id] = ch
	c.mu.Unlock()
	req, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if werr := c.send(append(req, '\n')); werr != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, werr
	}
	var timeout <-chan time.Time
	if limit > 0 {
		timeout = time.After(limit)
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
	case <-timeout:
		return nil, fmt.Errorf("codex: %s timed out", method)
	}
}

// callTimeout caps a setup call. The app-server answers these as it
// starts; only turn/start outlives the cap.
const callTimeout = 60 * time.Second

// send writes one frame under the write lock alone.
func (c *app) send(frame []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_, err := c.stdin.Write(frame)
	return err
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
	c.mu.Unlock()
	res, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	_ = c.send(append(res, '\n'))
}

// replyError answers a request whose id could not be correlated, so a
// server-request is never left open: an unanswered one hangs the turn
// (spec/cli-backends.md: no hung CLI).
func (c *app) replyError(id json.RawMessage, code int, message string) {
	res, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error":   map[string]any{"code": code, "message": message},
	})
	if err != nil {
		return
	}
	_ = c.send(append(res, '\n'))
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
		// Count the frame as in flight for as long as it takes to hand
		// it to a handler, so drain() can tell a pipe with unread frames
		// from one that is merely idle behind an open socket.
		c.mu.Lock()
		c.inflight++
		c.mu.Unlock()
		c.handleFrame(line)
		c.mu.Lock()
		c.inflight--
		c.mu.Unlock()
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

// handleFrame demultiplexes one NDJSON line into a response, a
// notification or a server-request.
func (c *app) handleFrame(line string) {
	var frame struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if json.Unmarshal([]byte(line), &frame) != nil {
		return
	}
	id := bytes.TrimSpace(frame.ID)
	hasID := len(id) > 0 && !bytes.Equal(id, []byte("null"))
	switch {
	case frame.Method != "" && hasID:
		n, ok := frameID(id)
		if !ok {
			// Not an id this client can correlate, but the server is
			// still waiting on an answer. Reply with an error object
			// rather than dropping the request: an unanswered
			// server-request hangs the turn (spec/cli-backends.md).
			c.replyError(id, -32600, "unsupported request id")
			return
		}
		c.mu.Lock()
		c.interactions[n] = true
		c.mu.Unlock()
		if c.onInteraction != nil {
			c.onInteraction(frame.Method, n, frame.Params)
		}
	case frame.Method != "":
		if frame.Method == "turn/completed" || frame.Method == "turn/failed" {
			c.settleTurn()
		}
		if c.onNotification != nil {
			c.onNotification(frame.Method, frame.Params)
		}
	case hasID:
		if n, ok := frameID(id); ok {
			c.mu.Lock()
			if ch := c.pending[n]; ch != nil {
				delete(c.pending, n)
				ch <- frameOrError([]byte(line), frame.Error)
			}
			c.mu.Unlock()
		}
	}
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

// waitReader gives the reader goroutine a bounded moment to finish, so
// the run normally settles with its trailing events already emitted.
func (c *app) waitReader(limit time.Duration) {
	select {
	case <-c.streamDone:
	case <-time.After(limit):
	}
}

// drain waits for the reader to work through the frames still in flight,
// without waiting for it to stop reading. A resident app-server keeps its
// stdout open after the turn, so its reader parks in Scan() with nothing
// left to deliver, and streamDone does not close until Reap tears the
// process down; waiting for that costs the full grace on every single
// turn, which is the common case and the expensive one.
//
// "In flight" is the frames the reader has taken off the pipe but not yet
// handed to a handler. A frame the server wrote a moment ago has not been
// counted yet, so bare quiescence would return too early and drop the
// tail of a turn that outran its completion notice. Hence the quiet
// window: the wait ends only once the reader has been idle long enough
// for a straggler to have arrived.
func (c *app) drain(limit, quiet time.Duration) {
	deadline := time.Now().Add(limit)
	idleSince := time.Now()
	for time.Now().Before(deadline) {
		c.mu.Lock()
		busy := c.inflight > 0
		c.mu.Unlock()
		if busy {
			idleSince = time.Now()
		} else if time.Since(idleSince) >= quiet {
			return
		}
		select {
		case <-c.streamDone:
			return
		case <-time.After(250 * time.Microsecond):
		}
	}
}

// frameID extracts an id this client can correlate. Only the plain
// integers the client hands out round-trip through the maps; anything
// else is answered with an error instead of being matched.
func frameID(raw json.RawMessage) (int, bool) {
	n, err := strconv.Atoi(string(raw))
	if err != nil {
		return 0, false
	}
	return n, true
}

// spawnArgs builds the app-server command line and environment for one
// turn: the reasoning effort always, and a custom endpoint as a
// model_providers entry whose key arrives through an env var.
func spawnArgs(turn harness.Turn) ([]string, []string, error) {
	// Mode and Effort are plain ints on Turn and are clamped nowhere in the
	// repo, so both are switched rather than indexed: an out-of-range value
	// must fail closed, not panic the dispatch goroutine (there is no
	// recover anywhere in the process).
	effort := "medium"
	switch turn.Effort {
	case 0:
		effort = "low"
	case 2:
		effort = "high"
	}
	args := []string{"app-server", "--stdio",
		"-c", "model_reasoning_effort=" + effort}
	cmdEnv := os.Environ()
	if ep := turn.Endpoint; ep != nil && ep.BaseURL != "" {
		wire := ep.Wire
		if wire == "" {
			wire = harness.WireResponses
		}
		// Newer codex CLIs refuse wire_api="chat" outright
		// (openai/codex discussion 7782); failing here names the fix
		// instead of surfacing a config error after spawn.
		if wire == harness.WireChat {
			return nil, nil, fmt.Errorf(
				"codex: provider %q speaks chat completions, which this codex CLI no longer supports — set the provider's API to Responses (or pick a Responses-compatible endpoint), or switch backends",
				ep.Name)
		}
		id := ep.ID
		args = append(args,
			"-c", fmt.Sprintf("model_provider=%q", id),
			"-c", fmt.Sprintf("model_providers.%s.name=%q", id, ep.Name),
			"-c", fmt.Sprintf("model_providers.%s.base_url=%q", id, ep.BaseURL),
			"-c", fmt.Sprintf("model_providers.%s.wire_api=%q", id, wire),
			"-c", fmt.Sprintf("model_providers.%s.env_key=%q", id, EnvKey(id)),
		)
		cmdEnv = append(cmdEnv, EnvKey(id)+"="+ep.APIKey)
	}
	return args, cmdEnv, nil
}
