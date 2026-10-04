//go:build windows

package app

import "os/exec"

// claudeProcAttr is a no-op on Windows: TaskKill-style group stops are
// not needed there, the default process kill applies.
func claudeProcAttr(cmd *exec.Cmd) {}
