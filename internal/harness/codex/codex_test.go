package codex

import (
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
