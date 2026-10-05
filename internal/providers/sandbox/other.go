//go:build !darwin && !linux

package sandbox

import (
	"context"
	"fmt"
	"os/exec"

	"mygo-agent/internal/harness"
)

// New returns the platform's sandbox provider: here, one that always
// reports the missing backend (the honesty rule, spec/sandbox.md).
func New() harness.Sandbox { return sandbox{} }

type sandbox struct{}

// sandboxedCommand reports that this platform has no sandbox backend. The
// honesty rule (spec/sandbox.md): agent mode never falls back to
// unsandboxed execution — the error names the remedy instead.
func (sandbox) Command(ctx context.Context, b harness.Boundary, name string, arg ...string) (*exec.Cmd, func(), error) {
	return nil, nil, fmt.Errorf("sandboxed execution is not implemented on this platform; switch the approval mode to Full Access to run unsandboxed")
}
