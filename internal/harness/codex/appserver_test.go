package codex

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"mygo-agent/internal/harness"
)

// fakeAppServer is a stub `codex app-server --stdio`: it answers the three
// setup calls over NDJSON and then plays the canned frames for its
// scenario. The loop that follows the setup calls is the resident
// process's real behaviour — it only leaves when its stdin closes, so a
// run that forgets to close the pipe would hang here instead of in
// production.
const fakeAppServer = `#!/bin/bash
printf '%s\n' "$@" > args.txt
while IFS= read -r line; do
  printf '%s\n' "$line" >> requests.txt
  # The client marshals a map, so its keys come out sorted: match the id
  # and the method by name rather than by position.
  id=$(printf '%s' "$line" | grep -o '"id":[0-9]*' | head -1 | cut -d: -f2)
  method=$(printf '%s' "$line" | sed -n 's/.*"method":"\([^"]*\)".*/\1/p')
  case "$method" in
    initialize)
      printf '{"jsonrpc":"2.0","id":%s,"result":{}}\n' "$id"
      ;;
    thread/start|thread/resume)
      printf '{"jsonrpc":"2.0","id":%s,"result":{"thread":{"id":"th_1"}}}\n' "$id"
      ;;
    turn/start)
      printf '{"jsonrpc":"2.0","id":%s,"result":{"turnId":"turn_1"}}\n' "$id"
      case "{{scenario}}" in
      failed)
        printf '{"jsonrpc":"2.0","method":"turn/failed","params":{"error":{"message":"model stream broke"}}}\n'
        ;;
      stream)
        # A fast stream that fills the pipe and then blocks on the write,
        # so at the stop there is a real backlog left for the reader to
        # work through. It is the backlog that makes an emit land after
        # the run has returned, which is the bug this scenario is for.
        i=0
        while [ $i -lt 5000 ]; do
          printf '{"jsonrpc":"2.0","method":"item/agentMessage/delta","params":{"itemId":"msg_1","delta":"streaming"}}\n'
          i=$((i+1))
        done
        ;;
      *)
        printf '{"jsonrpc":"2.0","method":"item/started","params":{"item":{"id":"item_0","type":"command_execution","command":"go test ./...","status":"in_progress"}}}\n'
        printf '{"jsonrpc":"2.0","method":"item/completed","params":{"item":{"id":"item_0","type":"command_execution","command":"go test ./...","status":"completed","exitCode":0,"aggregatedOutput":"ok"}}}\n'
        printf '{"jsonrpc":"2.0","method":"item/agentMessage/delta","params":{"itemId":"msg_1","delta":"Hello "}}\n'
        printf '{"jsonrpc":"2.0","method":"item/agentMessage/delta","params":{"itemId":"msg_1","delta":"world"}}\n'
        printf '{"jsonrpc":"2.0","method":"turn/completed","params":{}}\n'
        ;;
      esac
      ;;
  esac
done
`

// fakeBin writes the stub app-server for one scenario and returns its
// path. The scenario is baked into the script rather than passed through
// the environment, so the stub does not depend on what else the test
// binary has in its env.
func fakeBin(t *testing.T, scenario string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-codex.sh")
	body := strings.ReplaceAll(fakeAppServer, "{{scenario}}", scenario)
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// collector gathers a run's events under a lock: the adapter emits from
// its reader goroutine, and the tests read the stream from the test
// goroutine. Once closed it records late events separately instead of
// mixing them in, because an event after Run returned is a defect the
// Host cannot survive.
type collector struct {
	mu      sync.Mutex
	evs     []harness.Event
	after   []harness.Event
	closed  bool
	waiters []chan struct{}
}

func (c *collector) emit(ev harness.Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		c.after = append(c.after, ev)
		return
	}
	c.evs = append(c.evs, ev)
	for _, w := range c.waiters {
		select {
		case w <- struct{}{}:
		default:
		}
	}
}

// close marks Run as returned, from the same goroutine that ran it.
func (c *collector) close() {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
}

func (c *collector) snapshot() (evs, after []harness.Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]harness.Event(nil), c.evs...), append([]harness.Event(nil), c.after...)
}

// waitFor blocks until at least n events have arrived.
func (c *collector) waitFor(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		c.mu.Lock()
		got := len(c.evs)
		wake := make(chan struct{}, 1)
		c.waiters = append(c.waiters, wake)
		c.mu.Unlock()
		if got >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("only %d of %d events arrived", got, n)
		}
		select {
		case <-wake:
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// drive runs one turn to completion and returns its events.
func drive(t *testing.T, bin, dir string, turn harness.Turn) (*collector, error) {
	t.Helper()
	col := &collector{}
	if turn.Workdir == "" {
		turn.Workdir = dir
	}
	done := make(chan error, 1)
	go func() {
		err := New(bin).Run(context.Background(), turn, col.emit)
		col.close()
		done <- err
	}()
	select {
	case err := <-done:
		return col, err
	case <-time.After(20 * time.Second):
		t.Fatal("run did not settle")
		return nil, nil
	}
}

func kinds(evs []harness.Event, kind string) []harness.Event {
	var out []harness.Event
	for _, ev := range evs {
		if ev.Kind == kind {
			out = append(out, ev)
		}
	}
	return out
}

// TestRunFakeAppServer drives the JSON-RPC transport end to end against a
// stub app-server: the thread id arrives, one command opens exactly one
// card and closes it, the reply streams as deltas, and Run returns even
// though the stub keeps its pipes open — closing the turn's stdin is what
// reaps a resident app-server.
func TestRunFakeAppServer(t *testing.T) {
	dir := t.TempDir()
	bin := fakeBin(t, "ok")
	col, err := drive(t, bin, dir, harness.Turn{Workdir: dir, Mode: harness.ModeAgent, Prompt: "hi"})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	evs, late := col.snapshot()
	if len(late) != 0 {
		t.Fatalf("events after Run returned: %+v", late)
	}

	sessions := kinds(evs, harness.EventSession)
	if len(sessions) != 1 || sessions[0].SessionID != "th_1" {
		t.Fatalf("session events: %+v", sessions)
	}
	starts, ends := kinds(evs, harness.EventToolStart), kinds(evs, harness.EventToolEnd)
	if len(starts) != 1 || len(ends) != 1 {
		t.Fatalf("tool cards unbalanced: %d starts, %d ends (%+v)", len(starts), len(ends), evs)
	}
	if starts[0].ToolCall.ID != ends[0].ToolCall.ID || ends[0].Output != "ok" || ends[0].Exit != 0 {
		t.Fatalf("tool card does not match its start: %+v / %+v", starts[0], ends[0])
	}
	text := ""
	for _, ev := range kinds(evs, harness.EventText) {
		text += ev.TextDelta
	}
	if text != "Hello world" {
		t.Fatalf("reply text %q", text)
	}
	// The spawn args are the adapter's contract with the CLI; check the
	// stub actually saw them.
	args, err := os.ReadFile(filepath.Join(dir, "args.txt"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"app-server", "--stdio", "model_reasoning_effort="} {
		if !strings.Contains(string(args), want) {
			t.Fatalf("spawn args %q lack %q", strings.Join(strings.Fields(string(args)), " "), want)
		}
	}
}

// TestRunTurnFailedIsNotSilent pins that a failed turn explains itself.
// The JSON-RPC wire settles the turn on turn/failed, so a mapping that
// misses that method leaves the reply stopping with no error event and a
// nil return — the one failure the user cannot act on.
func TestRunTurnFailedIsNotSilent(t *testing.T) {
	dir := t.TempDir()
	col, err := drive(t, fakeBin(t, "failed"), dir, harness.Turn{Workdir: dir, Mode: harness.ModeAgent})
	if err == nil {
		t.Fatal("a failed turn returned nil")
	}
	if !strings.Contains(err.Error(), "model stream broke") {
		t.Fatalf("run error %v", err)
	}
	evs, _ := col.snapshot()
	errs := kinds(evs, harness.EventError)
	if len(errs) != 1 || !strings.Contains(errs[0].Err, "model stream broke") {
		t.Fatalf("error events: %+v", errs)
	}
}

// TestRunCancelEmitsNothingAfterReturn pins the emit-after-return
// guard. The Host finishes and saves the thread the instant Run returns,
// so a reader goroutine still emitting lands text in a thread that is
// already done. This is the one adapter with a background reader, which
// is why the wait and the seal both matter.
func TestRunCancelEmitsNothingAfterReturn(t *testing.T) {
	dir := t.TempDir()
	col := &collector{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		err := New(fakeBin(t, "stream")).Run(ctx, harness.Turn{Workdir: dir, Mode: harness.ModeAgent}, col.emit)
		col.close()
		done <- err
	}()

	col.waitFor(t, 3) // the stream is flowing
	cancel()

	var err error
	select {
	case err = <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("cancelled run did not settle")
	}
	// A stopped run keeps whatever arrived and reports no failure.
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("a stopped run reported a failure: %v", err)
	}
	if evs, _ := col.snapshot(); len(evs) == 0 {
		t.Fatal("no events arrived before the stop")
	}
	// Nothing may arrive after Run returned.
	time.Sleep(300 * time.Millisecond)
	if _, late := col.snapshot(); len(late) != 0 {
		t.Fatalf("%d events after Run returned: %+v", len(late), late)
	}
}

// TestRunOutOfRangeModeSurvives pins that an unclamped Turn.Mode or
// Turn.Effort cannot panic the dispatch goroutine. Mode is a bare int
// clamped nowhere in the repo and the process has no recover, so
// indexing an options array with it would take the app down; an
// out-of-range value has to fall back to the safe defaults instead.
func TestRunOutOfRangeModeSurvives(t *testing.T) {
	dir := t.TempDir()
	turn := harness.Turn{Workdir: dir, Mode: harness.Mode(99), Effort: 42, Prompt: "hi"}
	if _, err := drive(t, fakeBin(t, "ok"), dir, turn); err != nil {
		t.Fatalf("run: %v", err)
	}
	params, err := os.ReadFile(filepath.Join(dir, "requests.txt"))
	if err != nil {
		t.Fatal(err)
	}
	// Fail closed: an unknown mode gets the read-only sandbox, and an
	// unknown effort the middle one — not the last element of an
	// options array, and not a panic.
	if !strings.Contains(string(params), `"sandbox":"read-only"`) {
		t.Fatalf("sandbox for an out-of-range mode: %s", strings.Join(strings.Fields(string(params)), " "))
	}
	args, err := os.ReadFile(filepath.Join(dir, "args.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(args), "model_reasoning_effort=medium") {
		t.Fatalf("effort for an out-of-range value: %q", strings.Join(strings.Fields(string(args)), " "))
	}
}

// TestRunDeadAppServerReportsWhy: a server that dies before answering
// looks the same on the wire as one that closed politely — "connection
// closed" — so without the process's own words on stderr the user is left
// with a message that names the symptom and nothing else. This is the
// difference between a failure they can act on and one they can only
// retry, so the tail has to reach the error, and the transport's own
// "codex:" prefix must not be doubled on the way out.
func TestRunDeadAppServerReportsWhy(t *testing.T) {
	dir := t.TempDir()
	// A stub that refuses to start: it says why on stderr and leaves,
	// which is what a bad config override or an unsupported sandbox
	// looks like from the client side.
	bin := filepath.Join(dir, "dead-codex.sh")
	body := `#!/bin/bash
echo "error: unexpected argument '--stdio' found" >&2
exit 2
`
	if err := os.WriteFile(bin, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	turn := harness.Turn{Workdir: dir, Mode: harness.ModeAgent, Prompt: "hi"}
	_, err := drive(t, bin, dir, turn)
	if err == nil {
		t.Fatal("a server that never answered reported a clean turn")
	}
	msg := err.Error()
	if !strings.Contains(msg, "initialize") {
		t.Fatalf("the error does not say which call failed: %q", msg)
	}
	// The reason the server gave is the only actionable part.
	if !strings.Contains(msg, "unexpected argument") {
		t.Fatalf("the CLI's own explanation was dropped: %q", msg)
	}
	if strings.Contains(msg, "codex:initialize:") {
		t.Fatalf("the prefix is doubled: %q", msg)
	}
	if n := strings.Count(msg, "codex:"); n > 1 {
		t.Fatalf("codex: appears %d times: %q", n, msg)
	}
}

// recorder is a stdin stand-in that keeps every frame written to it and
// can park a write to model a child that has stopped reading.
type recorder struct {
	frames  chan string
	entered chan struct{}
	block   chan struct{}
	once    sync.Once
}

func (w *recorder) Write(p []byte) (int, error) {
	w.once.Do(func() {
		if w.entered != nil {
			close(w.entered)
		}
	})
	if w.block != nil {
		<-w.block
	}
	select {
	case w.frames <- strings.TrimSpace(string(p)):
	default:
	}
	return len(p), nil
}

func (w *recorder) Close() error { return nil }

// TestUncorrelatableRequestStillAnswered pins that a server-request whose
// id this client cannot match is still answered. The old code skipped the
// frame entirely, and an unanswered server-request is exactly the hung
// CLI spec/cli-backends.md forbids.
func TestUncorrelatableRequestStillAnswered(t *testing.T) {
	w := &recorder{frames: make(chan string, 4)}
	c := newApp(w, strings.NewReader(
		`{"jsonrpc":"2.0","id":"abc-1","method":"item/permissions/requestApproval","params":{}}`+"\n"))
	go c.readLoop()
	select {
	case frame := <-w.frames:
		if !strings.Contains(frame, `"id":"abc-1"`) || !strings.Contains(frame, `"error"`) {
			t.Fatalf("answered with %q", frame)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no reply to a request with an uncorrelatable id")
	}
}

// TestReplyDoesNotBlockTheReader pins that a write which cannot complete
// does not stall the reader. The reader delivers responses under the same
// mutex the reply path used to hold across its write, so one blocked
// write stalled stdout draining too and a turn that answered slowly
// wedged both pipes. The write now has a lock of its own.
func TestReplyDoesNotBlockTheReader(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	w := &recorder{frames: make(chan string, 4), entered: make(chan struct{}), block: block}
	c := newApp(w, strings.NewReader(`{"jsonrpc":"2.0","id":1,"result":{}}`+"\n"))

	c.mu.Lock()
	pending := make(chan json.RawMessage, 1)
	c.pending[1] = pending
	c.interactions[1] = true
	c.mu.Unlock()

	go c.reply(1, map[string]any{"decision": "decline"})
	<-w.entered // the reply is now parked inside its write

	go c.readLoop()
	select {
	case frame := <-pending:
		if len(frame) == 0 {
			t.Fatal("empty response delivered")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a blocked write stopped the reader from delivering a response")
	}
}

// TestCallDoesNotBlockTheReader pins the same separation on the request
// path: a write that stalls must not stop the reader from unblocking a
// pending call, or the turn's own setup would deadlock against itself.
func TestCallDoesNotBlockTheReader(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	w := &recorder{frames: make(chan string, 4), entered: make(chan struct{}), block: block}
	c := newApp(w, strings.NewReader(`{"jsonrpc":"2.0","id":1,"result":{"ok":true}}`+"\n"))

	reply := make(chan json.RawMessage, 1)
	c.mu.Lock()
	c.pending[1] = reply
	c.mu.Unlock()

	go c.send([]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
	<-w.entered // the write is parked, and no longer holds c.mu

	go c.readLoop()
	select {
	case <-reply:
	case <-time.After(5 * time.Second):
		t.Fatal("a blocked write stopped the reader from delivering a response")
	}
}

// TestEnvKeyIsInjective pins that distinct provider ids never share one
// env var. Folding to upper case and collapsing punctuation collided on
// "a-b"/"a_b" and "AB"/"ab", so two configured endpoints would hand the
// same secret to codex under one name.
func TestEnvKeyIsInjective(t *testing.T) {
	ids := []string{"a-b", "a_b", "AB", "ab", "A-B", "a b", "1/2", "x.y", "openai", "OpenAI", "my-provider_1"}
	seen := map[string]string{}
	for _, id := range ids {
		key := EnvKey(id)
		if prev, dup := seen[key]; dup {
			t.Fatalf("ids %q and %q share env key %q", prev, id, key)
		}
		seen[key] = id
		if !strings.HasPrefix(key, "MYGO_PROVIDER_") || !strings.HasSuffix(key, "_API_KEY") {
			t.Fatalf("id %q: %q is not a provider api-key name", id, key)
		}
		for _, r := range key {
			ok := r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_'
			if !ok {
				t.Fatalf("id %q: %q is not a valid env name", id, key)
			}
		}
	}
}

// TestReadLoopClosesPendingOnExit pins that losing the process unsticks
// the waiters instead of hanging the run: the turn's settle signal, every
// pending call and the reader's own exit all fire.
func TestReadLoopClosesPendingOnExit(t *testing.T) {
	pr, pw := io.Pipe()
	c := newApp(pw, pr)
	c.mu.Lock()
	pending := make(chan json.RawMessage, 1)
	c.pending[1] = pending
	c.mu.Unlock()
	go c.readLoop()
	_ = pw.Close() // the app-server is gone

	select {
	case <-c.streamDone:
	case <-time.After(5 * time.Second):
		t.Fatal("reader did not report its exit")
	}
	select {
	case <-c.turnDone:
	case <-time.After(5 * time.Second):
		t.Fatal("a lost process did not settle the turn")
	}
	select {
	case frame, ok := <-pending:
		if ok && frame != nil {
			t.Fatalf("pending call answered with %q", frame)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a pending call was left waiting on a lost process")
	}
}
