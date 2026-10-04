//go:build windows

package app

import "os/exec"

// procGroupAttr is a no-op on Windows: the default process kill
// applies.
func procGroupAttr(cmd *exec.Cmd) {}
