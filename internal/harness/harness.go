package harness

import "context"

// The Harness protocol (spec/architecture.md): one turn of agent work,
// behind four interchangeable adapters — the builtin loop, the codex
// app-server client and the claude stream-json client. The Host
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

	// Endpoint is an OpenAI-compatible endpoint a CLI adapter runs
	// against instead of the CLI's own sign-in (codex only today).
	// nil means the CLI's own credentials apply.
	Endpoint *Endpoint

	// Sandbox is the shell execution boundary (nil: full access).
	Sandbox Sandbox

	// MCPServers are the servers this turn mounts (spec/agents.md). The
	// built-in adapter spawns them in process; a CLI adapter maps what
	// its protocol can (spec/cli-backends.md). nil mounts nothing.
	MCPServers []MCPServer

	// ToolEnabled filters the built-in tool registry by name
	// (spec/agents.md): an entry set to false removes that tool; absent
	// or true keeps it; nil keeps everything. The loop hands it to the
	// tool options; MCP tools are governed by MCPServers instead.
	ToolEnabled map[string]bool

	// Skills narrows the skills the turn may see; the zero value keeps
	// everything discovered.
	Skills SkillSelection

	// SystemPrompt is appended to the adapter's own system prompt when
	// the adapter can inject one: the built-in seeds it into the
	// transcript head, claude passes --append-system-prompt, codex has
	// no injection point and ignores it.
	SystemPrompt string

	// Memory loads the thread's transcript before the turn and receives
	// the final one after; nil means no memory (a fresh transcript).
	Memory Memory
	// MemoryKey is the key to load and store the transcript under.
	MemoryKey string
	// InitialTranscript seeds memory when nothing is stored yet.
	InitialTranscript []ChatMessage

	// OnApproval decides calls whose permission resolves to ask
	// (approvals.md). It receives the run's context, so a stopped run
	// settles its pending cards. nil denies ask calls up front.
	OnApproval func(ctx context.Context, req ApprovalRequest) ApprovalDecision

	// OnOutsideDir decides whether the run may reach the directories
	// outside Workdir that its prompt named (approvals.md). A CLI's own
	// path boundary sits below its tool-permission layer, so those
	// refusals never arrive as can_use_tool and the Host would otherwise
	// never learn of them. nil keeps the run confined to the workspace.
	OnOutsideDir func(ctx context.Context, req OutsideDirRequest) bool
}

// Endpoint is an OpenAI-compatible endpoint a CLI adapter can be pointed
// at instead of the CLI vendor's own service.
type Endpoint struct {
	ID      string
	Name    string
	BaseURL string
	APIKey  string
	Wire    string // "chat" or "responses"; empty means the adapter's default (codex: responses, builtin: chat)
	// ContextWindow is the provider's declared context window in tokens;
	// the built-in loop reads it to arm the watermark compaction
	// (spec/architecture.md, tool output budgets). The CLI adapters do
	// not use it.
	ContextWindow int
}

// MCPServer is one Model Context Protocol server the Host resolves and
// hands the harness on the Turn: a command to spawn and speak JSON-RPC
// to over stdio, or a streamable HTTP endpoint (URL set, no command).
type MCPServer struct {
	Name    string   `json:"name"`
	Command string   `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`
	Env     []string `json:"env,omitempty"`
	URL     string   `json:"url,omitempty"`
}

// SkillSelection narrows the skills a turn may see (spec/agents.md).
// An empty Allow keeps everything discovered; Deny wins over Allow.
type SkillSelection struct {
	Allow []string
	Deny  []string
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

// ChatMessage is one message of a model conversation.
type ChatMessage struct {
	Role       string     `json:"role"`
	Content    any        `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

// ToolCall is one function the assistant asked for.
type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// ApprovalRequest asks the host to decide one call (approvals.md).
type ApprovalRequest struct {
	Call    ToolCall
	Summary string
	Reason  string // why policy asked, from a fixed vocabulary
}

// ApprovalDecision is the host's answer. Approved covers exactly the one
// call; there is no "always allow" decision — durable authority lives in
// permission rules.
type ApprovalDecision struct {
	Approved bool
	Reason   string // the denial reason shown to the model; empty when approved
}

// ToolOptions tunes how the built-in tools execute. The host derives
// them from the approval mode: agent mode sandboxes the shell and
// confines writes to the workspace; full access does neither.
type ToolOptions struct {
	// Sandbox wraps shell commands in the workspace-scoped execution
	// boundary (spec/sandbox.md). nil means no sandbox (full access);
	// a provider whose platform has no backend reports that as the
	// tool's error instead of running unsandboxed.
	Sandbox Sandbox
	// ConfineWrites rejects edit_file targets outside the workdir.
	ConfineWrites bool
	// Enabled filters the built-in tool registry by name (spec/agents.md):
	// an entry set to false removes that tool; absent or true keeps it.
	// nil keeps everything. MCP tools are not in the registry — the
	// turn's server list governs them.
	Enabled map[string]bool
}

// WireAPI names the two request shapes a provider endpoint may speak.
const (
	WireChat      = "chat"      // POST /chat/completions
	WireResponses = "responses" // POST /responses — what the codex models use
)
