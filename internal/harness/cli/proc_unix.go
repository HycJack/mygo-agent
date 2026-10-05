//go:build !windows

package cli

import (
	"os/exec"
	"syscall"
	"time"
)

// execCmd mirrors *exec.Cmd so callers keep importing os/exec, not us.
type execCmd = exec.Cmd

// setProcGroup runs the CLI in its own process group and turns a stop
// into a group SIGTERM (escalating to SIGKILL after the delay), so the
// CLI and the tool processes it spawned actually die.
func setProcGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		}
		return nil
	}
	cmd.WaitDelay = 3 * time.Second
}
