package builtin

import "mygo-agent/internal/harness"

// The built-in loop reads the harness protocol through local names: the
// aliases below bind the root package's types into this package, so the
// loop and tool code read exactly as they did when they lived at the
// root. Only protocol types are aliased — everything the loop owns
// (LoopConfig, Run, StreamConfig, Tool, the errors) is declared here.

type (
	Turn       = harness.Turn
	Event      = harness.Event
	Harness    = harness.Harness
	Memory     = harness.Memory
	Endpoint   = harness.Endpoint
	ToolCall   = harness.ToolCall
	DiffLine   = harness.DiffLine
	Sandbox    = harness.Sandbox
	Boundary   = harness.Boundary
	Rules      = harness.Rules
	Policy     = harness.Policy
	Mode       = harness.Mode
	Permission = harness.Permission

	ChatMessage      = harness.ChatMessage
	ApprovalRequest  = harness.ApprovalRequest
	ApprovalDecision = harness.ApprovalDecision
	DenialError      = harness.DenialError
	Action           = harness.Action
	ToolOptions      = harness.ToolOptions
)

const (
	WireChat      = harness.WireChat
	WireResponses = harness.WireResponses

	// Event kinds the loop emits, read through the protocol root so a
	// renamed kind can never silently stop matching the Host's switch.
	EventText      = harness.EventText
	EventToolStart = harness.EventToolStart
	EventToolEnd   = harness.EventToolEnd
	EventNote      = harness.EventNote
	EventError     = harness.EventError
	EventDone      = harness.EventDone

	ModeReadOnly = harness.ModeReadOnly
	ModeAgent    = harness.ModeAgent
	ModeFull     = harness.ModeFull

	PermAsk   = harness.PermAsk
	PermAllow = harness.PermAllow
	PermDeny  = harness.PermDeny

	ActionFileRead  = harness.ActionFileRead
	ActionFileWrite = harness.ActionFileWrite
	ActionShell     = harness.ActionShell
	ActionSkill     = harness.ActionSkill
	ActionMCP       = harness.ActionMCP
)

var (
	ApprovalSummary      = harness.ApprovalSummary
	PermissionFromConfig = harness.PermissionFromConfig
	MemoryKey            = harness.MemoryKey
	UnifiedDiff          = harness.UnifiedDiff
	ParseUnifiedDiff     = harness.ParseUnifiedDiff
	DiffStats            = harness.DiffStats
	MarkWordDiff         = harness.MarkWordDiff
	LcsDiff              = harness.LcsDiff
)
