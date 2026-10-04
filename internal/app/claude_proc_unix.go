//go:build !windows

package app

import (
	"os/exec"
	"syscall"
	"time"
)

// claudeProcAttr runs the claude CLI in its own process group and turns
// a stop into a group SIGTERM (escalating to SIGKILL after a delay), so
// node and any tool processes it spawned actually die.
func claudeProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		}
		return nil
	}
	cmd.WaitDelay = 3 * time.Second
}
