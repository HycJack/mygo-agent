package builtin

import (
	"mygo-agent/internal/harness/cli"

	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"time"
)

// Tools builds the built-in tool set, bound to a working directory.
func Tools(workdir string, skills *SkillSet, opts ...ToolOptions) []Tool {
	var o ToolOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	return []Tool{
		bashTool(workdir, o),
		readFileTool(workdir),
		editFileTool(workdir, o),
		listFilesTool(workdir),
		grepTool(workdir),
		readSkillTool(workdir, skills),
	}
}

func obj(properties string) json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":` + properties + `}`)
}

func bashTool(workdir string, o ToolOptions) Tool {
	return Tool{
		Name:        "bash",
		Description: "Run a shell command in the project directory and return its combined output. Use for builds, tests, git and anything else.",
		Actions:     []Action{ActionShell},
		Parameters: obj(`{"command":{"type":"string","description":"The shell command to run."},
			"timeout_seconds":{"type":"integer","description":"Optional timeout, 120 by default."}}`),
		Execute: func(ctx context.Context, args string) (string, error) {
			var in struct {
				Command        string `json:"command"`
				TimeoutSeconds int    `json:"timeout_seconds"`
			}
			if err := json.Unmarshal([]byte(args), &in); err != nil {
				return "", err
			}
			if strings.TrimSpace(in.Command) == "" {
				return "", fmt.Errorf("command is required")
			}
			timeout := time.Duration(in.TimeoutSeconds) * time.Second
			if timeout <= 0 {
				timeout = 120 * time.Second
			}
			ctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			if err := ctx.Err(); err != nil {
				return "", cancelled("bash")
			}
			shell, flag := "bash", "-c"
			if runtime.GOOS == "windows" {
				shell, flag = "powershell", "-NoProfile -Command"
			}
			argv := append(strings.Fields(flag), in.Command)
			cmd, cleanup, err := shellCommand(ctx, o, workdir, shell, argv...)
			if cleanup != nil {
				defer cleanup()
			}
			if err != nil {
				return "", err
			}
			// The cap applies while the child writes, not after it exits:
			// CombinedOutput would hold a runaway command's whole output
			// in memory first.
			out := &capBuffer{max: maxCommandOutput}
			cmd.Stdout, cmd.Stderr = out, out
			err = cmd.Run()
			res := cli.TrimOutput(out.String(), 32<<10)
			if err != nil {
				// Killed or timed out, the outcome is unknown
				// (spec/sandbox.md): the result says so.
				switch {
				case errors.Is(ctx.Err(), context.DeadlineExceeded):
					res += "\ncommand timed out; its outcome is unknown"
				case ctx.Err() != nil:
					res += "\ncommand was cancelled; its outcome is unknown"
				}
				return res, err
			}
			if res == "" {
				res = "(no output)"
			}
			return res, nil
		},
	}
}

func readFileTool(workdir string) Tool {
	return Tool{
		Name:        "read_file",
		Description: "Read a text file relative to the project directory, optionally a range of lines. Returns the file with line numbers.",
		Actions:     []Action{ActionFileRead},
		Parameters: obj(`{"path":{"type":"string","description":"File path, relative to the project directory."},
			"offset":{"type":"integer","description":"First line to read, 1-based."},
			"limit":{"type":"integer","description":"How many lines to read, 400 by default."}}`),
		Execute: func(ctx context.Context, args string) (string, error) {
			var in struct {
				Path   string `json:"path"`
				Offset int    `json:"offset"`
				Limit  int    `json:"limit"`
			}
			if err := json.Unmarshal([]byte(args), &in); err != nil {
				return "", err
			}
			if err := ctx.Err(); err != nil {
				return "", cancelled("read_file")
			}
			if in.Limit <= 0 {
				in.Limit = 400
			}
			full := safeJoin(workdir, in.Path)
			if refusesSensitive(full) {
				return "", errSensitivePath
			}
			// Refuse a whole-file slurp, but honour the range the caller
			// actually asked for. Refusing on total size while the remedy
			// in the message is "read it in ranges" made every range read
			// of a large lockfile fail too, so the file was permanently
			// unreadable. A ranged read streams, so the file's total size
			// is irrelevant — readRange bounds what it accumulates.
			st, err := os.Stat(full)
			if err != nil {
				return "", err
			}
			if st.Size() > maxReadFileBytes && in.Offset <= 1 {
				return "", fmt.Errorf("%s is %d bytes, over the %d-byte read limit; read a range of it with offset and limit",
					in.Path, st.Size(), maxReadFileBytes)
			}
			data, err := readRange(full, in.Offset, in.Limit)
			if err != nil {
				return "", err
			}
			lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
			from, to := 1, len(lines)
			if from < 1 {
				from = 1
			}
			if to > len(lines) {
				to = len(lines)
			}
			var b strings.Builder
			for i := from; i <= to; i++ {
				fmt.Fprintf(&b, "%5d  %s\n", i, lines[i-1])
			}
			if to < len(lines) {
				fmt.Fprintf(&b, "… %d more lines\n", len(lines)-to)
			}
			return b.String(), nil
		},
	}
}

// readRange streams the requested 1-based line range out of a file and
// returns just those lines. It never holds the whole file, which is the
// point: a ranged read of a huge lockfile or bundle has to work, and
// loading the file only to throw most of it away is what the size cap
// forbids. What it accumulates is bounded by maxRangeReadBytes, so a huge
// line count in the range cannot size the heap either.
func readRange(path string, offset, limit int) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	// One line may be up to the range cap; anything longer is a binary or
	// a minified bundle, and refusing it beats growing the heap.
	sc.Buffer(make([]byte, 64*1024), maxRangeReadBytes)
	if offset < 1 {
		offset = 1
	}
	var b strings.Builder
	line, held := 0, 0
	for sc.Scan() {
		line++
		if line < offset {
			continue
		}
		if limit > 0 && line >= offset+limit {
			break
		}
		// sc.Bytes() is valid until the next Scan, so measure before writing.
		line := sc.Bytes()
		if held+len(line) > maxRangeReadBytes {
			return "", fmt.Errorf("that range is over the %d-byte read limit; narrow it with offset and limit", maxRangeReadBytes)
		}
		held += len(line)
		b.Write(line)
		b.WriteByte('\n')
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	return b.String(), nil
}

func editFileTool(workdir string, o ToolOptions) Tool {
	return Tool{
		Name:        "edit_file",
		Description: "Replace an exact string inside a file. The old text must appear exactly once. Returns a unified diff of the change.",
		Actions:     []Action{ActionFileWrite},
		Parameters: obj(`{"path":{"type":"string","description":"File path, relative to the project directory."},
			"old_text":{"type":"string","description":"The exact text to replace."},
			"new_text":{"type":"string","description":"The replacement text."}}`),
		Execute: func(ctx context.Context, args string) (string, error) {
			var in struct {
				Path    string `json:"path"`
				OldText string `json:"old_text"`
				NewText string `json:"new_text"`
			}
			if err := json.Unmarshal([]byte(args), &in); err != nil {
				return "", err
			}
			if err := ctx.Err(); err != nil {
				return "", cancelled("edit_file")
			}
			full := safeJoin(workdir, in.Path)
			// Lexical containment is not enough: a symlink inside the
			// workdir pointing out of it would walk straight past the
			// gate, so the resolved paths are compared too.
			if o.ConfineWrites && !(underDir(workdir, full) && underDir(canonical(workdir), canonical(full))) {
				return "", fmt.Errorf("in the current mode edit_file only writes inside the project directory: %s is outside", in.Path)
			}
			// The edit reads the whole file into memory — and holds it
			// three times over (bytes, string, replacement). A stat first
			// is what keeps a model-named large file from being the one
			// call that OOMs the app; read_file refuses the same size.
			if st, serr := os.Stat(full); serr == nil && st.Size() > maxReadFileBytes {
				return "", fmt.Errorf("%s is %d bytes, over the %d-byte edit limit; edit a range of it with bash instead",
					in.Path, st.Size(), maxReadFileBytes)
			}
			data, err := os.ReadFile(full)
			if err != nil {
				return "", err
			}
			src := string(data)
			n := strings.Count(src, in.OldText)
			switch {
			case n == 0:
				return "", fmt.Errorf("old_text not found in %s", in.Path)
			case n > 1:
				return "", fmt.Errorf("old_text appears %d times in %s; include more context", n, in.Path)
			}
			next := strings.Replace(src, in.OldText, in.NewText, 1)
			mode := os.FileMode(0o644)
			if st, serr := os.Stat(full); serr == nil {
				mode = st.Mode().Perm() // an edit keeps the file's mode
			}
			if err := os.WriteFile(full, []byte(next), mode); err != nil {
				return "", err
			}
			var b strings.Builder
			for _, l := range UnifiedDiff(in.OldText, in.NewText) {
				b.WriteByte(l.Kind)
				b.WriteByte(' ')
				b.WriteString(l.Text)
				b.WriteByte('\n')
			}
			return b.String(), nil
		},
	}
}

func listFilesTool(workdir string) Tool {
	return Tool{
		Name:        "list_files",
		Description: "List a directory of the project, one entry per line, directories with a trailing slash.",
		Actions:     []Action{ActionFileRead},
		Parameters:  obj(`{"path":{"type":"string","description":"Directory path, relative to the project directory; empty means the project root."}}`),
		Execute: func(ctx context.Context, args string) (string, error) {
			var in struct {
				Path string `json:"path"`
			}
			_ = json.Unmarshal([]byte(args), &in)
			if err := ctx.Err(); err != nil {
				return "", cancelled("list_files")
			}
			dir := safeJoin(workdir, in.Path)
			if refusesSensitive(dir) {
				return "", errSensitivePath
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				return "", err
			}
			names := make([]string, 0, len(entries))
			for _, e := range entries {
				n := e.Name()
				if e.IsDir() {
					n += "/"
					// A listing of a credential store is a map of it;
					// skip the entry rather than the whole listing so the
					// rest of the directory still renders.
					if refusesSensitive(filepath.Join(dir, n)) {
						continue
					}
				}
				names = append(names, n)
			}
			slices.Sort(names)
			if len(names) == 0 {
				return "(empty)", nil
			}
			// A listing is a map, not a census: past 200 entries the
			// tail is the same directory noise repeated, and it all
			// rides into the context. The marker names the way out.
			if len(names) > 200 {
				names = append(names[:200], fmt.Sprintf("… %d more entries; list a narrower path …", len(names)-200))
			}
			return strings.Join(names, "\n"), nil
		},
	}
}

func grepTool(workdir string) Tool {
	return Tool{
		Name:        "grep",
		Description: "Search file contents under a directory with a regular expression. Returns file:line: match lines, at most 200.",
		Actions:     []Action{ActionFileRead},
		Parameters: obj(`{"pattern":{"type":"string","description":"Go regular expression."},
			"path":{"type":"string","description":"Directory to search, relative to the project directory; empty means the project root."}}`),
		Execute: func(ctx context.Context, args string) (string, error) {
			var in struct {
				Pattern string `json:"pattern"`
				Path    string `json:"path"`
			}
			if err := json.Unmarshal([]byte(args), &in); err != nil {
				return "", err
			}
			re, err := regexp.Compile(in.Pattern)
			if err != nil {
				return "", err
			}
			root := safeJoin(workdir, in.Path)
			if refusesSensitive(root) {
				return "", errSensitivePath
			}
			var out []string
			skips := map[string]bool{".git": true, "node_modules": true, "target": true, "dist": true}
			_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
				// The walk is not interruptible from outside, so the stop
				// button is honoured here between entries.
				if ctx.Err() != nil {
					return filepath.SkipAll
				}
				// err and d must be handled before d is touched: WalkDir
				// calls back with a NIL DirEntry when the root itself
				// cannot be lstat'ed, so a model-authored path that does
				// not exist would dereference nil and take the app down.
				if err != nil {
					return filepath.SkipAll
				}
				if d == nil {
					return filepath.SkipAll
				}
				// Every entry is checked, not just the root: a walk rooted
				// outside the project crosses credential stores on the way,
				// and a FILE symlink into one is not a directory, so
				// gating on d.IsDir() alone would let os.ReadFile below
				// follow it straight into the store.
				if refusesSensitive(path) {
					if d.IsDir() {
						return filepath.SkipDir
					}
					return nil
				}
				if len(out) >= 200 {
					return filepath.SkipAll
				}
				if d.IsDir() {
					if skips[d.Name()] {
						return filepath.SkipDir
					}
					return nil
				}
				if info, ierr := d.Info(); ierr != nil || info.Size() > 1<<20 {
					return nil
				}
				data, err := os.ReadFile(path)
				if err != nil || bytes.IndexByte(data, 0) >= 0 {
					return nil
				}
				rel, _ := filepath.Rel(root, path)
				for i, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
					if re.MatchString(line) {
						out = append(out, fmt.Sprintf("%s:%d: %s", rel, i+1, strings.TrimSpace(line)))
						if len(out) >= 200 {
							break
						}
					}
				}
				return nil
			})
			if err := ctx.Err(); err != nil {
				return "", cancelled("grep")
			}
			if len(out) == 0 {
				return "(no matches)", nil
			}
			return strings.Join(out, "\n"), nil
		},
	}
}

func readSkillTool(workdir string, skills *SkillSet) Tool {
	return Tool{
		Name:        "read_skill",
		Description: "Load the full instructions of a skill by name. The available skills are listed in the system prompt.",
		Actions:     []Action{ActionSkill},
		Parameters:  obj(`{"name":{"type":"string","description":"The skill's name from the system prompt."}}`),
		Execute: func(ctx context.Context, args string) (string, error) {
			var in struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal([]byte(args), &in); err != nil {
				return "", err
			}
			if err := ctx.Err(); err != nil {
				return "", cancelled("read_skill")
			}
			if skills == nil {
				return "", fmt.Errorf("no skills are installed")
			}
			content, ok := skills.Load(in.Name)
			if !ok {
				return "", fmt.Errorf("no skill named %q", in.Name)
			}
			// A skill is instructions, and instructions are written by
			// whoever installed them — the same cap a tool result gets
			// keeps an oversized SKILL.md from being an unbounded ride
			// into the context.
			return cli.TrimOutput(content, maxToolResultBytes), nil
		},
	}
}

// cmdEnv is the child process environment.
func cmdEnv() []string {
	return os.Environ()
}

// safeJoin resolves path under root and refuses escapes. An absolute path
// passes through unchanged: reading anywhere on disk is a read action and
// the permission gate governs it; write confinement is the caller's
// (see ToolOptions.ConfineWrites).
func safeJoin(root, path string) string {
	if path == "" {
		return root
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(root, filepath.Clean("/"+path))
}

// sensitivePaths are the credential stores the file tools refuse — the
// same list the sandbox masks for a shell command
// (internal/providers/sandbox/credentials.go). Without it here the
// shell boundary was
// the only thing standing between the model and `~/.aws/credentials`:
// `file.read` is allow in every mode, an absolute path passes straight
// through safeJoin, and the content leaves the machine in the next
// request. A deny list on one path and not the other is a hole, not a
// policy.
//
// Both the literal and the symlink-resolved form of each store are kept.
// Resolution is best-effort — a path that does not exist yet cannot be
// resolved — and a home directory reached through a symlink would
// otherwise make every comparison miss.
var sensitivePaths = func() [][2]string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil
	}
	var out [][2]string
	for _, sub := range []string{".ssh", ".aws", ".gnupg", ".kube", ".docker", ".config/gh", ".netrc"} {
		raw := filepath.Clean(filepath.Join(home, sub))
		out = append(out, [2]string{raw, canonical(raw)})
	}
	return out
}()

// errSensitivePath is the settled denial a credential read gets. It is a
// normal tool error the model can read and react to, not a crash.
var errSensitivePath = errors.New("refused: that path is a credential store (ssh keys, cloud credentials, tokens); the sandbox boundary hides it from shell commands too, and the app does not read it on the model's behalf")

// refusesSensitive reports whether path is inside a credential store. A
// symlink or a `..` segment must not be a way around it, so both forms
// of the candidate are compared against both forms of every store.
//
// The comparison also folds case, because the default volume on macOS and
// Windows is case-insensitive: a purely textual check lets
// `~/.SSH/id_rsa` through a list that holds `~/.ssh`, and the read then
// succeeds at the filesystem level. Folding on a case-sensitive volume
// costs nothing — it can only refuse a path that a case-sensitive
// filesystem would have refused anyway.
func refusesSensitive(path string) bool {
	if path == "" || len(sensitivePaths) == 0 {
		return false
	}
	forms := [2]string{pathKey(filepath.Clean(path)), pathKey(canonical(path))}
	for _, store := range sensitivePaths {
		for _, s := range store {
			if s == "" {
				continue
			}
			key := pathKey(s)
			for _, f := range forms {
				if f == key || underDir(key, f) {
					return true
				}
			}
		}
	}
	return false
}

// pathKey normalizes a path for comparison by folding case, so the same
// location compares equal however it was spelled. Separators are left
// alone: underDir goes through filepath.Rel, which wants the native form.
func pathKey(p string) string { return strings.ToLower(p) }

// underDir reports whether path is root itself or inside it.
func underDir(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// Read caps. maxCommandOutput is the ceiling on what one shell command
// may hold in memory; it sits well above the trim the model sees, so
// TrimOutput's "first N" marker still describes what was dropped.
const (
	maxCommandOutput   = 1 << 20
	maxReadFileBytes   = 2 << 20
	maxToolResultBytes = 32 << 10
	// maxRangeReadBytes bounds a single line while streaming a range, and
	// the longest line a ranged read will accept at all.
	maxRangeReadBytes = 2 << 20
)

// capBuffer keeps the first max bytes a command writes and drops the
// rest, so a runaway command cannot size the heap. It is safe for the
// merged stdout and stderr of one exec.Cmd: os/exec shares a single pipe
// when the two writers are the same value, so one goroutine writes here.
type capBuffer struct {
	buf bytes.Buffer
	max int
}

// Write accepts every byte the child offers and retains only the first
// max: a command that would fill memory is slowed, not failed.
func (c *capBuffer) Write(p []byte) (int, error) {
	if room := c.max - c.buf.Len(); room > 0 {
		c.buf.Write(p[:min(room, len(p))])
	}
	return len(p), nil
}

func (c *capBuffer) String() string { return c.buf.String() }

// cancelled is what the walk tools report when a stop arrives: the tool
// name says which call gave up, which the bare context error does not.
func cancelled(tool string) error {
	return fmt.Errorf("%s was cancelled", tool)
}

// shellCommand builds the command for one shell tool invocation. Without
// a Sandbox (full access) it is a plain child process; with one, the
// scratch directory is created here — the provider owns the boundary,
// the harness owns the child's environment and the cleanup order.
// Either way the child leads its own process group: a stop or a timeout
// kills the whole tree, and WaitDelay bounds the output pipes a
// surviving grandchild would otherwise hold open forever.
func shellCommand(ctx context.Context, o ToolOptions, workdir, name string, arg ...string) (*exec.Cmd, func(), error) {
	if o.Sandbox == nil {
		cmd := exec.CommandContext(ctx, name, arg...)
		cmd.Dir = workdir
		procGroupAttr(cmd)
		return cmd, nil, nil
	}
	scratch, err := os.MkdirTemp("", "mygo-sandbox-")
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() { os.RemoveAll(scratch) }
	if err := os.MkdirAll(filepath.Join(scratch, "tmp"), 0o700); err != nil {
		cleanup()
		return nil, nil, err
	}
	cmd, wrapCleanup, err := o.Sandbox.Command(ctx, Boundary{
		Workdir: canonical(workdir),
		Scratch: scratch,
		Network: "deny",
	}, name, arg...)
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	if wrapCleanup != nil {
		inner := cleanup
		cleanup = func() { wrapCleanup(); inner() }
	}
	cmd.Dir = workdir
	procGroupAttr(cmd)
	// The child sees a minimal environment pointing at the boundary's
	// writable places, so caches and temp files land inside the grants.
	cmd.Env = []string{
		"HOME=" + scratch,
		"TMPDIR=" + filepath.Join(scratch, "tmp"),
		"PATH=" + os.Getenv("PATH"),
	}
	return cmd, cleanup, nil
}

// canonical absolutizes and resolves a grant path: grants enter profiles
// as real paths, never through a symlink the child could sway.
func canonical(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	return abs
}
