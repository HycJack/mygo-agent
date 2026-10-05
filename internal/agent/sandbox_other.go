//go:build !darwin && !linux

package agent

import (
	"context"
	"fmt"
	"os/exec"
)

// sandboxedCommand reports that this platform has no sandbox backend. The
// honesty rule (spec/sandbox.md): agent mode never falls back to
// unsandboxed execution — the error names the remedy instead.
func sandboxedCommand(ctx context.Context, b boundary, name string, arg ...string) (*exec.Cmd, error) {
	return nil, fmt.Errorf("sandboxed execution is not implemented on this platform; switch the approval mode to Full Access to run unsandboxed")
}
