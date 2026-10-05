package cli

import (
	"io"
	"os/exec"
	"time"
)

// ReapGrace is how long a CLI gets to exit after its turn settled, before
// its process group is torn down. A CLI that exits on stdin EOF is gone in
// milliseconds; the grace only covers a slow final flush, so it never cuts
// short a turn that is still producing output.
var ReapGrace = 3 * time.Second

// Reap waits for a CLI to exit after the turn settled, and reports
// whether the process group had to be killed.
//
// `cmd.WaitDelay` only engages once the context is already done, so a CLI
// that lingers after announcing the turn is over would hold the turn
// forever. stdin is closed first when the caller can — that is what makes
// a resident CLI exit (the claude lesson, spec/cli-backends.md) — and a
// CLI that stays anyway gets a group SIGTERM, then a leader kill so Wait
// can always return.
//
// `killed` is not a failure signal: a stop we asked for, or a CLI we had
// to clean up, is a normal end to a turn. Callers use it only to keep a
// deliberate kill out of the reply's error text.
func Reap(cmd *exec.Cmd, stdin io.Closer) (waitErr error, killed bool) {
	if stdin != nil {
		_ = stdin.Close()
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	settled := func() (error, bool) {
		select {
		case err := <-done:
			return err, false
		case <-time.After(ReapGrace):
			return nil, true
		}
	}
	err, overdue := settled()
	if !overdue {
		return err, false
	}
	if kill := cmd.Cancel; kill != nil {
		_ = kill() // ProcGroupAttr: SIGTERM to the whole group
	}
	if err, overdue := settled(); !overdue {
		return err, true
	}
	// Alive after the group SIGTERM: kill the leader so Wait can return.
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	err, _ = settled()
	return err, true
}
