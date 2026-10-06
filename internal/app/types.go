package app

import (
	"time"

	"github.com/egoist/mygo/ui"

	"mygo-agent/internal/config"
	"mygo-agent/internal/harness"
)

// The data shapes the app persists and renders: threads with their
// messages and cards, the viewer's current file, and the workspace's
// tree entries and git changes. Behavior lives elsewhere.

// Block kinds. The set is closed: the view groups on it and the projector
// writes it, so a kind neither recognizes is a bug on both sides rather
// than a card that silently renders as nothing.
const (
	blockCommand   = "command"   // a tool call: one quiet row, output folded away
	blockDiff      = "diff"      // a file the agent changed
	blockReasoning = "reasoning" // the agent thinking, folded away
	blockNote      = "note"      // the harness reporting on the turn
	blockError     = "error"     // something that went wrong
	blockApproval  = "approval"  // a pending or decided permission ask
	// blockText is the agent's prose, in the ordered sequence. It is not a
	// card: it renders as markdown at the point it was said, so a turn
	// reads in the order it happened. Message.Text remains the
	// concatenation of every text block, for copy and for reseeding the
	// built-in transcript.
	blockText = "text"
)

// Block is one ordered piece of an assistant message: prose, a command it
// ran, a file patch it applied, a stretch of reasoning, an error, or a
// pending approval. Order is arrival order, and it is the order the
// transcript renders in.
type Block struct {
	Type    string // one of the block kinds above
	Text    string // the command line, error message or reasoning text
	Output  string // command output
	Exit    int    // command exit code, -1 when unknown
	Running bool   // the command is still running
	Edit    bool   // a file edit, rendered as a diff card when done
	Ms      int64  // how long the tool took
	Open    bool   // the card's body is expanded
	File    string // the patched file
	Add     int
	Del     int
	Lines   []DiffLine

	// ApprovalID identifies the pending approval this card asks about;
	// the decision buttons resolve it through app.approvals.
	ApprovalID string
	// ToolID is the harness tool-call id that opened a command card; the
	// projector matches tool_end events to it.
	ToolID string
}

// Message is one turn of a thread: the user's prompt or the assistant's
// reply with its tool cards.
type Message struct {
	ID      string
	Role    string // "user" | "assistant"
	Text    string
	Blocks  []Block
	Running bool
	At      time.Time

	// LogAt marks where this assistant turn begins in the thread's
	// ChatLog, so regenerate can rewind it.
	LogAt int
}

// Thread is one Codex task, belonging to a project.
type Thread struct {
	ID        string
	ProjectID string // the project the task ran in
	Title     string
	Messages  []Message
	Created   time.Time
	Updated   time.Time
	CodexID   string // codex exec session id, for resuming
	ClaudeID  string // Claude Code session id, for resuming
	PiID      string // pi coding agent session id, for resuming

	// dropped marks a thread the host has deleted while events from its
	// last turn could still be in flight. Such an event can still reach
	// saveThread before the run drains; the flag is what keeps a deleted
	// task from rewriting the file its deletion removed. Unexported, so
	// it never reaches the thread file; undo clears it.
	dropped bool

	// ChatLog is the built-in backend's full transcript, including the
	// tool round-trips, so a task survives app restarts with context
	// intact.
	ChatLog []harness.ChatMessage `json:"chat_log,omitempty"`

	// diffCount caches how many diff blocks the thread holds, so the
	// header does not rescan every block of every message each frame.
	// diffsKnown, not diffsStale, is the polarity that matters: the zero
	// Thread must mean "never counted", or a thread built in memory (a
	// created task, seeded demo data) would report zero forever. A stale
	// count is cosmetic rather than a correctness problem, so an unknown
	// count is simply rescanned on the next read.
	diffCount  int
	diffsKnown bool
}

// viewStamp is a cheap, complete fingerprint of everything the transcript
// ViewModel copies out of a thread. It exists so the host can skip
// rebuilding an identical snapshot, and it is deliberately exhaustive
// over the copied fields: a stamp that missed one would show the user a
// stale transcript, which is a far worse bug than a per-frame walk. The
// walk allocates nothing, so skipping the copy still removes the real
// cost. Diff Lines are immutable once a block is built, so their length
// is enough. A text block is mixed through the same path as any other
// block, which is what makes a streaming reply invalidate the cache.
func (th *Thread) viewStamp() uint64 {
	const (
		off = 14695981039346656037
		pr  = 1099511628211
	)
	h := uint64(off)
	mix := func(v uint64) { h = (h ^ v) * pr }
	mixStr := func(s string) {
		mix(uint64(len(s)))
		for i := 0; i < len(s); i++ {
			mix(uint64(s[i]))
		}
	}
	mix(uint64(len(th.Messages)))
	for i := range th.Messages {
		m := &th.Messages[i]
		mixStr(m.ID)
		mixStr(m.Role)
		mixStr(m.Text)
		// At is copied into MessageVM and rendered as the timestamp, so
		// leaving it out would make the stamp incomplete.
		mix(uint64(m.At.UnixNano()))
		if m.Running {
			mix(1)
		}
		mix(uint64(len(m.Blocks)))
		for j := range m.Blocks {
			b := &m.Blocks[j]
			mixStr(b.Type)
			mixStr(b.Text)
			mixStr(b.File)
			mixStr(b.Output)
			mixStr(b.ToolID)
			mixStr(b.ApprovalID)
			mix(uint64(b.Add))
			mix(uint64(b.Del))
			mix(uint64(len(b.Lines)))
			mix(uint64(b.Exit))
			mix(uint64(b.Ms))
			if b.Open {
				mix(1)
			}
			if b.Edit {
				mix(2)
			}
			if b.Running {
				mix(4)
			}
		}
	}
	return h
}

// fsNode is one entry of the workspace file tree.
type fsNode struct {
	Name string
	Path string
	Dir  bool
}

// gitChange is one path of `git status --porcelain`: "M", "A", "D",
// "R", "??" and so on.
type gitChange struct {
	Code string
	Path string
}

// viewerState is what the file viewer shows while it is open.
type viewerState struct {
	Path    string // file path, or "git:<path>" for a working-tree diff
	Kind    string // "text" | "image" | "markdown" | "diff"
	Title   string
	Sub     string // "+12 −3" or a byte size
	Text    string // the raw text, for Markdown rendering
	Lines   []DiffLine
	Raw     string // the unrendered text, for the copy button
	Bitmap  *ui.Bitmap
	Err     string
	Note    string // e.g. "showing the first 20,000 lines"
	Loading bool
}

// The persisted types live in internal/config; alias them so the rest
// of the app reads naturally.
type (
	Project   = config.Project
	Provider  = config.Provider
	MCPServer = config.MCPServer
)

// app is the whole application state; the view is a function of it.
