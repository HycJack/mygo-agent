package agent

import (
	"bytes"
	"context"
	"encoding/json"
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
func Tools(workdir string, skills *SkillSet) []Tool {
	return []Tool{
		bashTool(workdir),
		readFileTool(workdir),
		editFileTool(workdir),
		listFilesTool(workdir),
		grepTool(workdir),
		readSkillTool(workdir, skills),
	}
}

func obj(properties string) json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":` + properties + `}`)
}

func bashTool(workdir string) Tool {
	return Tool{
		Name:        "bash",
		Description: "Run a shell command in the project directory and return its combined output. Use for builds, tests, git and anything else.",
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
			shell, flag := "bash", "-c"
			if runtime.GOOS == "windows" {
				shell, flag = "powershell", "-NoProfile -Command"
			}
			argv := append(strings.Fields(flag), in.Command)
			cmd := exec.CommandContext(ctx, shell, argv...)
			cmd.Dir = workdir
			out, err := cmd.CombinedOutput()
			res := TrimOutput(string(out), 32<<10)
			if err != nil {
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
			if in.Limit <= 0 {
				in.Limit = 400
			}
			data, err := os.ReadFile(safeJoin(workdir, in.Path))
			if err != nil {
				return "", err
			}
			lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
			from, to := in.Offset, in.Offset+in.Limit
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

func editFileTool(workdir string) Tool {
	return Tool{
		Name:        "edit_file",
		Description: "Replace an exact string inside a file. The old text must appear exactly once. Returns a unified diff of the change.",
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
			full := safeJoin(workdir, in.Path)
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
			if err := os.WriteFile(full, []byte(next), 0o644); err != nil {
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
		Parameters:  obj(`{"path":{"type":"string","description":"Directory path, relative to the project directory; empty means the project root."}}`),
		Execute: func(ctx context.Context, args string) (string, error) {
			var in struct {
				Path string `json:"path"`
			}
			_ = json.Unmarshal([]byte(args), &in)
			entries, err := os.ReadDir(safeJoin(workdir, in.Path))
			if err != nil {
				return "", err
			}
			names := make([]string, 0, len(entries))
			for _, e := range entries {
				n := e.Name()
				if e.IsDir() {
					n += "/"
				}
				names = append(names, n)
			}
			slices.Sort(names)
			if len(names) == 0 {
				return "(empty)", nil
			}
			return strings.Join(names, "\n"), nil
		},
	}
}

func grepTool(workdir string) Tool {
	return Tool{
		Name:        "grep",
		Description: "Search file contents under a directory with a regular expression. Returns file:line: match lines, at most 200.",
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
			var out []string
			skips := map[string]bool{".git": true, "node_modules": true, "target": true, "dist": true}
			_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
				if err != nil || len(out) >= 200 {
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
		Parameters:  obj(`{"name":{"type":"string","description":"The skill's name from the system prompt."}}`),
		Execute: func(ctx context.Context, args string) (string, error) {
			var in struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal([]byte(args), &in); err != nil {
				return "", err
			}
			if skills == nil {
				return "", fmt.Errorf("no skills are installed")
			}
			content, ok := skills.Load(in.Name)
			if !ok {
				return "", fmt.Errorf("no skill named %q", in.Name)
			}
			return content, nil
		},
	}
}

// cmdEnv is the child process environment.
func cmdEnv() []string {
	return os.Environ()
}

// safeJoin resolves path under root and refuses escapes.
func safeJoin(root, path string) string {
	if path == "" {
		return root
	}
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(root, filepath.Clean("/"+path))
}
