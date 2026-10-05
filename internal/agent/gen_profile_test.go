package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDumpProfile(t *testing.T) {
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	b := boundary{Workdir: dir, Scratch: filepath.Join(dir, "scratch"), Network: "deny"}
	os.WriteFile("/tmp/sb_profile.txt", []byte(seatbeltProfile(b)), 0o644)
}
