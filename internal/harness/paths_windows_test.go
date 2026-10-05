//go:build windows

package harness

import "testing"

// The scan is off on Windows, deliberately, and this pins why so it stays
// a decision rather than drifting into an accident.
//
// Running these prompts on Windows CI produced:
//
//	"look at /etc/hosts"        -> []            (filepath.Join wrote \etc\hosts,
//	                                                which pathInText cannot see)
//	"read /tmp/cxtest/somefile"  -> [D:\tmp\cxtest] (filepath.Abs put a POSIX
//	                                                path on whatever drive the
//	                                                runner was on)
//	"read /etc/hosts."           -> [D:\etc]
//
// The second and third are the dangerous ones: they name a directory the
// prompt never mentioned, on a drive the user never named. A grant is a
// real widening of what the model may read, so a scan that guesses is
// worse than one that stays quiet.
func TestTheScanIsOffOnWindowsRatherThanGuessing(t *testing.T) {
	ws := t.TempDir()
	for _, prompt := range []string{
		"look at /etc/hosts please",
		`read C:\Users\someone\notes.md`,
		"read C:/Users/someone/notes.md",
		"read " + ws + `\..\elsewhere\x.txt`,
	} {
		if got := dirsOutsideWorkdir(prompt, ws); got != nil {
			t.Errorf("prompt %q produced %v, want nothing on Windows", prompt, got)
		}
	}
}

// The workspace being a Windows path is not the reason it is off; a
// relative prompt with no path at all must still be silent rather than
// panicking or inventing a grant.
func TestTheOffSwitchStillHandlesAnEmptyWorkspace(t *testing.T) {
	if got := dirsOutsideWorkdir("just do the thing", ""); got != nil {
		t.Fatalf("got %v, want nothing", got)
	}
}
