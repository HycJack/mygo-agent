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

// Block is a card inside an assistant message: a command it ran, a file
// patch it applied, a stretch of reasoning, an error, or a pending
// approval.
type Block struct {
	Type    string // "command" | "diff" | "reasoning" | "error" | "approval"
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

	// ChatLog is the built-in backend's full transcript, including the
	// tool round-trips, so a task survives app restarts with context
	// intact.
	ChatLog []harness.ChatMessage `json:"chat_log,omitempty"`
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
