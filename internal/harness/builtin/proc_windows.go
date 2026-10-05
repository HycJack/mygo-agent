//go:build windows

package builtin

import "os/exec"

// procGroupAttr is a no-op on Windows: the default process kill takes
// the direct child, and job objects are out of scope here.
func procGroupAttr(cmd *exec.Cmd) {}
