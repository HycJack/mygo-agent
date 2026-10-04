//go:build !windows

package app

import (
	"os/exec"
	"syscall"
	"time"
)

// procGroupAttr runs a CLI backend in its own process group and turns
// a stop into a group SIGTERM (escalating to SIGKILL after a delay),
// so the CLI and any tool processes it spawned actually die. Used for
// the codex and Claude Code backends.
func procGroupAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		}
		return nil
	}
	cmd.WaitDelay = 3 * time.Second
}
