package harness

import "context"

// The Harness protocol (spec/architecture.md): one turn of agent work,
// behind four interchangeable adapters — the builtin loop, the codex
// app-server client, the claude stream-json client and the demo. The Host
// dispatches by Kind and never branches on an adapter's internals.

// Harness is one replaceable agent backend. Instances are per-turn: a
// struct may close over its turn's thread position and the Host's
// approval callback, the way OAC's daemon builds a fresh session per
// prompt.
type Harness interface {
	// Kind names the adapter; it is also the session-id namespace
	// ("codex" → Thread.CodexID, "claude" → Thread.ClaudeID).
	Kind() string
	// Run drives one turn to completion, emitting normalized Events.
	// It returns an error only for a failed turn; a stopped run is
	// context.Canceled and everything emitted so far stands.
	Run(ctx context.Context, turn Turn, emit func(Event)) error
}

// Turn carries everything a harness needs and nothing about Thread or UI.
type Turn struct {
	Prompt   string
	Workdir  string
	Mode     Mode
	Rules    Rules
	Model    string
	Effort   int // 0 low, 1 medium, 2 high
	MaxTurns int

	// SessionID resumes a prior harness session when non-empty.
	SessionID string

	// Sandbox is the shell execution boundary (nil: full access).
	Sandbox Sandbox

	// Memory loads the thread's transcript before the turn and receives
	// the final one after; nil means no memory (a fresh transcript).
	Memory Memory
	// MemoryKey is the key to load and store the transcript under.
	MemoryKey string
	// InitialTranscript seeds memory when nothing is stored yet.
	InitialTranscript []ChatMessage

	// OnApproval decides calls whose permission resolves to ask
	// (approvals.md). nil denies ask calls up front.
	OnApproval func(ApprovalRequest) ApprovalDecision
}

// EventKinds are the values of Event.Kind.
const (
	EventText       = "text"        // TextDelta
	EventToolStart  = "tool_start"  // ToolCall
	EventToolEnd    = "tool_end"    // ToolCall, Output, Exit
	EventReasoning  = "reasoning"   // Text
	EventFileChange = "file_change" // File, Diff, Paths
	EventSession    = "session"     // SessionID
	EventNote       = "note"        // Text (usage summary, turn-limit notice)
	EventError      = "error"       // Err
	EventDone       = "done"
)

// Event is one normalized thing a harness reports during a turn. It is
// the stream a future UI transport would consume (spec/architecture.md);
// the Host's projector turns it into message blocks.
type Event struct {
	Kind string

	TextDelta string // EventText
	Text      string // EventReasoning, EventNote
	Err       string // EventError

	ToolCall ToolCall // EventToolStart, EventToolEnd
	Output   string   // EventToolEnd
	Exit     int      // EventToolEnd: 0 ok, 1 failed

	// EventFileChange: a patch the harness applied or previews.
	File  string
	Diff  string
	Paths []string
	Edit  bool // render as an edit card

	// Card identity and timing for EventToolStart/End: the projector
	// matches a tool_end to the card a tool_start opened by ID, and
	// shows the duration.
	Ms int64 // EventToolEnd: how long the tool took

	// EventSession: the harness session id to persist for resume.
	SessionID string
}
