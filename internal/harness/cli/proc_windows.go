//go:build windows

package cli

import "os/exec"

type execCmd = exec.Cmd

// setProcGroup is a no-op on Windows: the default process kill applies.
func setProcGroup(cmd *exec.Cmd) {}
