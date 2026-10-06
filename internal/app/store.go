package app

// The thread store: one file per thread under threads/<projectID>/,
// written atomically (spec/data.md), plus the legacy single-file
// threads.json import and the quarantine rule for unreadable files.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"mygo-agent/internal/harness"
)

// writeFileAtomic writes through a temp file and a rename, so a crash
// mid-write never leaves a truncated file behind.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

var errThreadsUnsupported = errors.New("file was written by a newer version")

// threadsFile is the legacy single-file threads.json shape, read only
// by the upgrade contract.
type threadsFile struct {
	Version int       `json:"version"`
	Threads []*Thread `json:"threads"`
}

// quarantine renames an unloadable data file out of the way so a later
// save cannot destroy it (spec/data.md). Returns the new name.
func quarantine(path, why string) string {
	for attempt := 0; ; attempt++ {
		name := path + "." + why
		if attempt > 0 {
			name = fmt.Sprintf("%s.%s.%d", path, why, attempt+1)
		}
		if _, err := os.Stat(name); err == nil {
			continue
		}
		if err := os.Rename(path, name); err != nil {
			return ""
		}
		return name
	}
}

// threadsLayout owns the per-thread persistence tree
// (spec/data.md): threads/<projectID>/<threadID>.json, one small file
// per task, mode 0600, atomic writes.
type threadsLayout struct {
	root string // <configDir>/codex-go/threads
}

func (l threadsLayout) file(th *Thread) (string, bool) {
	if l.root == "" || !safeName(th.ProjectID) || !safeName(th.ID) {
		return "", false
	}
	return filepath.Join(l.root, th.ProjectID, th.ID+".json"), true
}

// safeName refuses path separators and dot names before a value becomes
// a path segment.
func safeName(s string) bool {
	if s == "" || s == "." || s == ".." {
		return false
	}
	return !strings.ContainsAny(s, "/\\\x00")
}

// threadFile is the persisted shape of one thread file (spec/data.md).
type threadFile struct {
	Version  int                   `json:"version"`
	Meta     threadMeta            `json:"meta"`
	Messages []Message             `json:"messages"`
	ChatLog  []harness.ChatMessage `json:"chat_log,omitempty"`
}

type threadMeta struct {
	ID        string    `json:"id"`
	ProjectID string    `json:"project_id"`
	Title     string    `json:"title"`
	Created   time.Time `json:"created"`
	Updated   time.Time `json:"updated"`
	CodexID   string    `json:"codex_id,omitempty"`
	ClaudeID  string    `json:"claude_id,omitempty"`
	PiID      string    `json:"pi_id,omitempty"`
}

func metaOf(th *Thread) threadMeta {
	return threadMeta{ID: th.ID, ProjectID: th.ProjectID, Title: th.Title,
		Created: th.Created, Updated: th.Updated,
		CodexID: th.CodexID, ClaudeID: th.ClaudeID, PiID: th.PiID}
}

// saveThread writes one thread's file atomically. The thread's Messages
// and ChatLog stay in memory; only their own file is touched.
func (a *app) saveThread(th *Thread) {
	if th.dropped {
		// A thread deleted mid-run stays deleted: an event that lands
		// after removeThreadFile must not re-create the file. Undo is
		// the only thing that clears the flag.
		return
	}
	path, ok := a.threadsDir.file(th)
	if !ok {
		return
	}
	data, err := json.MarshalIndent(threadFile{
		Version: 1, Meta: metaOf(th), Messages: th.Messages, ChatLog: th.ChatLog,
	}, "", "  ")
	if err != nil {
		a.threadsErr = "this task could not be saved: " + err.Error()
		return
	}
	// A save that fails must say so. Swallowing it loses the turn with no
	// trace, and data.md invariant 3 already establishes the mechanism for
	// making a load failure visible.
	if err := writeFileAtomic(path, data, 0o600); err != nil {
		a.threadsErr = "this task could not be written to " + path + ": " + err.Error()
		return
	}
	a.threadsErr = ""
}

// removeThreadFile deletes one thread's file. The caller keeps the
// thread in memory for the undo toast.
func (a *app) removeThreadFile(th *Thread) {
	if path, ok := a.threadsDir.file(th); ok {
		// A file that survives deletion comes back on the next launch, so
		// a failure here is reported rather than ignored.
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			a.threadsErr = "the task's file could not be deleted (" + err.Error() + "); it will reappear on the next launch"
			return
		}
		// The project dir, now empty. A non-empty dir is not an error.
		os.Remove(filepath.Dir(path))
	}
}

// loadThreads walks the tree and decodes every thread file. One bad
// file is quarantined and skipped; the rest still load (spec/data.md).
func (a *app) loadThreads() {
	if a.threadsDir.root == "" {
		return
	}
	entries, err := os.ReadDir(a.threadsDir.root)
	if err != nil {
		return // no tree yet
	}
	for _, proj := range entries {
		if !proj.IsDir() || !safeName(proj.Name()) {
			continue
		}
		files, err := os.ReadDir(filepath.Join(a.threadsDir.root, proj.Name()))
		if err != nil {
			continue
		}
		for _, f := range files {
			name := f.Name()
			if !strings.HasSuffix(name, ".json") || nameendsQuarantined(name) {
				continue
			}
			path := filepath.Join(a.threadsDir.root, proj.Name(), name)
			th, err := decodeThreadFile(path)
			if err != nil {
				why := "invalid"
				if errors.Is(err, errThreadsUnsupported) {
					why = "unsupported"
				}
				if kept := quarantine(path, why); kept != "" {
					a.threadsErr = filepath.Base(kept) + " (" + err.Error() + ")"
				}
				continue
			}
			if th.ProjectID == "" {
				th.ProjectID = a.activeProject
			}
			a.threads = append(a.threads, th)
		}
	}
	// Newest first, the order the sidebar expects.
	slices.SortFunc(a.threads, func(x, y *Thread) int {
		return y.Updated.Compare(x.Updated)
	})
	if len(a.threads) > 0 {
		a.current = a.threads[0].ID
		if th := a.byID(a.current); th != nil && th.ProjectID != a.activeProject {
			a.current = ""
		}
	}
}

// nameendsQuarantined reports a previously quarantined file; the scan
// skips them so quarantine renames stay once-per-file.
func nameendsQuarantined(name string) bool {
	return strings.HasSuffix(name, ".invalid") || strings.HasSuffix(name, ".unsupported")
}

// decodeThreadFile strictly decodes one thread file.
func decodeThreadFile(path string) (*Thread, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var tf threadFile
	if err := dec.Decode(&tf); err != nil {
		return nil, err
	}
	if tf.Version > 1 {
		return nil, fmt.Errorf("%w (thread schema %d)", errThreadsUnsupported, tf.Version)
	}
	return &Thread{
		ID: tf.Meta.ID, ProjectID: tf.Meta.ProjectID, Title: tf.Meta.Title,
		Created: tf.Meta.Created, Updated: tf.Meta.Updated,
		CodexID: tf.Meta.CodexID, ClaudeID: tf.Meta.ClaudeID, PiID: tf.Meta.PiID,
		Messages: tf.Messages, ChatLog: tf.ChatLog,
		// The diff count is derived state; the zero value already means
		// "never counted", so the first read scans.
	}, nil
}

// decodeThreads parses the legacy single-file threads.json: the wrapped
// versioned shape, or the bare pre-version array (the upgrade contract),
// strictly.
func decodeThreads(data []byte) ([]*Thread, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var wrapped threadsFile
	if err := dec.Decode(&wrapped); err == nil {
		if wrapped.Version > 1 {
			return nil, fmt.Errorf("%w (tasks schema %d)", errThreadsUnsupported, wrapped.Version)
		}
		return wrapped.Threads, nil
	}
	dec = json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var threads []*Thread
	if err := dec.Decode(&threads); err != nil {
		return nil, err
	}
	return threads, nil
}

// importLegacyThreads loads the pre-directory single-file
// threads.json once (bare array or wrapped), imports its threads into
// the tree, and renames the original .migrated — preserved, never
// overwritten (spec/data.md).
func (a *app) importLegacyThreads() {
	if a.savePath == "" {
		return
	}
	data, err := os.ReadFile(a.savePath)
	if err != nil {
		return
	}
	threads, err := decodeThreads(data)
	if err != nil {
		if name := quarantine(a.savePath, "invalid"); name != "" {
			a.threadsErr = filepath.Base(name) + " (" + err.Error() + ")"
		}
		return
	}
	for _, th := range threads {
		if th.ProjectID == "" {
			th.ProjectID = a.activeProject
		}
		a.saveThread(th)
		if th.ID == "" { // unsaved placeholder: keep in memory only
			continue
		}
	}
	// Never clobber an existing .migrated: the original bytes are the
	// only copy of what was there before this app touched it.
	migrated := a.savePath + ".migrated"
	if _, err := os.Stat(migrated); err == nil {
		quarantine(migrated, "superseded")
	}
	if err := os.Rename(a.savePath, migrated); err != nil {
		a.threadsErr = "the legacy tasks file could not be set aside (" + err.Error() + "); it will be imported again next launch"
	}
	// The in-memory list is built by loadThreads from the tree; nothing
	// to set here.
	_ = threads
}
