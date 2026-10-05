package harness

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestBashKilledOutcomeIsUnknown asserts the failure rule: a command
// killed by cancellation reports its outcome as unknown instead of a
// clean failure (spec/sandbox.md).
func TestBashKilledOutcomeIsUnknown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var bash Tool
	for _, tl := range Tools(t.TempDir(), nil) {
		if tl.Name == "bash" {
			bash = tl
		}
	}
	if bash.Name == "" {
		t.Fatal("no bash tool")
	}
	go func() { time.Sleep(100 * time.Millisecond); cancel() }()
	out, err := bash.Execute(ctx, `{"command":"sleep 5"}`)
	if err == nil {
		t.Fatal("a cancelled command must fail")
	}
	if !strings.Contains(out, "outcome is unknown") {
		t.Fatalf("the result does not say the outcome is unknown: %q", out)
	}
}
