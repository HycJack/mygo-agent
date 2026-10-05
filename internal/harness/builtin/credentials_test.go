package builtin

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The shell boundary in internal/providers/sandbox masks the credential
// stores. These tests pin that the file tools honour the same list: the
// gate allows `file.read` in every mode, so without this a read of
// ~/.aws/credentials would succeed in read-only mode and leave the
// machine in the next request.

// TestFileToolsRefuseCredentialStores drives the tools end to end against
// the package's real sensitive-path list. It is skipped when the list is
// empty (a home we could not resolve), so it never fails spuriously on an
// unusual CI account.
func TestFileToolsRefuseCredentialStores(t *testing.T) {
	if len(sensitivePaths) == 0 {
		t.Skip("no home directory to derive credential stores from")
	}
	victim := sensitivePaths[0][1] // the resolved form, what a real read resolves to
	work := t.TempDir()
	tools := Tools(work, DiscoverSkills(work))
	var read, list, grep Tool
	for _, t2 := range tools {
		switch t2.Name {
		case "read_file":
			read = t2
		case "list_files":
			list = t2
		case "grep":
			grep = t2
		}
	}
	ctx := context.Background()

	if _, err := read.Execute(ctx, `{"path":`+quote(victim)+`}`); err == nil ||
		!strings.Contains(err.Error(), "credential") {
		t.Fatalf("read_file(%s) = %v, want a credential refusal", victim, err)
	}
	if _, err := list.Execute(ctx, `{"path":`+quote(victim)+`}`); err == nil ||
		!strings.Contains(err.Error(), "credential") {
		t.Fatalf("list_files on a credential store = %v, want a refusal", err)
	}
	if _, err := grep.Execute(ctx, `{"pattern":".","path":`+quote(victim)+`}`); err == nil ||
		!strings.Contains(err.Error(), "credential") {
		t.Fatalf("grep over a credential store = %v, want a refusal", err)
	}
}

// TestFileToolsStillReadOrdinaryFiles guards the denylist against
// over-blocking: an ordinary absolute path outside the project is still a
// legitimate read.
func TestFileToolsStillReadOrdinaryFiles(t *testing.T) {
	if len(sensitivePaths) == 0 {
		t.Skip("no home directory to derive credential stores from")
	}
	work := t.TempDir()
	outside := t.TempDir()
	good := filepath.Join(outside, "notes.md")
	if err := os.WriteFile(good, []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tools := Tools(work, DiscoverSkills(work))
	for _, tool := range tools {
		if tool.Name != "read_file" {
			continue
		}
		got, err := tool.Execute(context.Background(), `{"path":`+quote(good)+`}`)
		if err != nil {
			t.Fatalf("reading an ordinary file outside the project failed: %v", err)
		}
		if !strings.Contains(got, "hello") {
			t.Fatalf("unexpected content: %q", got)
		}
		return
	}
	t.Fatal("read_file is missing from the tool set")
}

func quote(s string) string {
	return `"` + strings.ReplaceAll(s, `\`, `\\`) + `"`
}
