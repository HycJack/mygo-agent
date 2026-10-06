package builtin

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEditFileRefusesAnOverLimitFile pins the memory bound: the edit
// holds the file three times over (bytes, string, replacement), so an
// unbounded read let a model-named large file be the one call that OOMs
// the app. read_file refuses the same size.
func TestEditFileRefusesAnOverLimitFile(t *testing.T) {
	work := t.TempDir()
	big := filepath.Join(work, "big.txt")
	if err := os.WriteFile(big, bytes.Repeat([]byte("a\n"), maxReadFileBytes/2+1), 0o644); err != nil {
		t.Fatal(err)
	}
	small := filepath.Join(work, "small.txt")
	if err := os.WriteFile(small, []byte("a\nb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	edit := editFileTool(work, ToolOptions{ConfineWrites: true})

	// The control: a file under the limit still edits.
	if _, err := edit.Execute(t.Context(), `{"path":"small.txt","old_text":"a","new_text":"z"}`); err != nil {
		t.Fatalf("an edit under the limit must be allowed: %v", err)
	}
	// The bound: an over-limit file is refused, not read.
	_, err := edit.Execute(t.Context(), `{"path":"big.txt","old_text":"a","new_text":"b"}`)
	if err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("an over-limit file must be refused with the limit named: %v", err)
	}
}
