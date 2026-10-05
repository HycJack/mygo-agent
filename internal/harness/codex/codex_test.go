//go:build !windows

package codex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mygo-agent/internal/harness"
)

// TestSpawnArgsWire pins the custom-endpoint wiring: the provider's
// declared wire rides through (empty means responses), and a
// chat-completions endpoint is rejected up front with the fix named —
// newer codex CLIs refuse wire_api="chat" after spawn.
func TestSpawnArgsWire(t *testing.T) {
	turn := harness.Turn{Endpoint: &harness.Endpoint{
		ID: "prov-1", Name: "Test", BaseURL: "http://x/v1", APIKey: "k",
	}}
	args, env, err := spawnArgs(turn)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, "\n")
	if !strings.Contains(joined, `wire_api="responses"`) {
		t.Fatalf("empty wire should default to responses: %s", joined)
	}
	if !strings.Contains(joined, `model_provider="prov-1"`) {
		t.Fatalf("provider not selected: %s", joined)
	}
	// EnvKey keeps the id's case and hex-escapes anything outside
	// [A-Za-z0-9], so distinct ids cannot collide on one env var.
	wantEnv := EnvKey("prov-1") + "=k"
	if got := EnvKey("prov-1"); got != "MYGO_PROVIDER_prov_2D1_API_KEY" {
		t.Fatalf("EnvKey encoding changed: %q", got)
	}
	foundKey := false
	for _, e := range env {
		if e == wantEnv {
			foundKey = true
		}
	}
	if !foundKey {
		t.Fatalf("api key env %q missing: %v", wantEnv, env)
	}

	turn.Endpoint.Wire = harness.WireChat
	if _, _, err := spawnArgs(turn); err == nil || !strings.Contains(err.Error(), "Responses") {
		t.Fatalf("chat endpoint should be rejected with the fix named, got: %v", err)
	}
}

// TestRunSpawnsTheProviderWire is the regression for the defect that made
// this a false sense of safety: spawnArgs had the right logic and its own
// tests, and Run never called it — it inlined a second copy that still
// passed wire_api="chat". The unit tests were green the whole time
// because they exercised a function production code did not reach.
//
// So this drives Run itself, against a stub that records the argv it was
// given. A test of a helper is only worth what the caller does with it.
func TestRunSpawnsTheProviderWire(t *testing.T) {
	dir := t.TempDir()
	turn := harness.Turn{Workdir: dir, Mode: harness.ModeAgent, Prompt: "hi",
		Endpoint: &harness.Endpoint{
			ID: "prov-2a9a9d8a5746", Name: "Custom", BaseURL: "http://x/v1", APIKey: "k",
		}}
	if _, err := drive(t, fakeBin(t, "ok"), dir, turn); err != nil {
		t.Fatalf("run: %v", err)
	}
	args, err := os.ReadFile(filepath.Join(dir, "args.txt"))
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(strings.Fields(string(args)), " ")
	// A chat-completions provider is what the CLI rejects outright
	// (openai/codex discussion 7782), so the arg must never say chat.
	if strings.Contains(got, `wire_api="chat"`) {
		t.Fatalf("Run spawned the rejected wire: %s", got)
	}
	if !strings.Contains(got, `wire_api="responses"`) {
		t.Fatalf("the provider's wire did not ride through: %s", got)
	}
}

// TestRunRejectsAChatEndpoint checks the other half through Run: the
// refusal has to happen before the process is spawned, with the fix
// named, rather than as a config error from a server that never started.
func TestRunRejectsAChatEndpoint(t *testing.T) {
	dir := t.TempDir()
	turn := harness.Turn{Workdir: dir, Mode: harness.ModeAgent, Prompt: "hi",
		Endpoint: &harness.Endpoint{
			ID: "prov-1", Name: "Chatty", BaseURL: "http://x/v1", APIKey: "k",
			Wire: harness.WireChat,
		}}
	_, err := drive(t, fakeBin(t, "ok"), dir, turn)
	if err == nil {
		t.Fatal("a chat-completions endpoint was accepted")
	}
	if !strings.Contains(err.Error(), "Responses") {
		t.Fatalf("the error does not name the fix: %v", err)
	}
	// Nothing was spawned, so the stub left no trace of a run.
	if _, statErr := os.Stat(filepath.Join(dir, "args.txt")); statErr == nil {
		t.Fatal("the process was spawned despite the endpoint being unsupported")
	}
}
