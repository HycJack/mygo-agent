//go:build !windows

package builtin

import (
	"os/exec"
	"syscall"
	"time"
)

// procGroupAttr runs a command in its own process group and turns a
// context kill into a group kill, so children and grandchildren of the
// command die with it (spec/sandbox.md: the process-group stop applies).
// A shell command has no clean shutdown worth preserving — its result is
// "unknown outcome" either way — so the group gets SIGKILL directly, and
// WaitDelay only bounds the output pipes.
func procGroupAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
	cmd.WaitDelay = 3 * time.Second
}
