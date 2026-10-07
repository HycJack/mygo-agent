package app

import (
	uipkg "mygo-agent/internal/ui"
	"strings"

	"mygo-agent/internal/harness"

	"github.com/egoist/mygo/ui"
)

// The transcript renders in internal/ui over a ViewModel snapshot
// (spec/architecture.md); this file is the bridge: snapshot assembly,
// the Actions implementation, and nothing else.

// messages renders the conversation of the current thread.
func (a *app) messages(c *ui.Context, th *Thread) {
	uipkg.Transcript(c, a.transcriptVM(th), transcriptActions{a: a, th: th})
}

// transcriptVM snapshots the thread's messages for the transcript view.
//
// The copy is the ViewModel contract, but re-copying every message and
// block on every frame is the wrong price for a view that only redraws
// when something changed. The snapshot is therefore rebuilt only when the
// thread's shape actually moved: a new message, a new block, or a
// rewind. Running is stamped separately because it flips on its own.
func (a *app) transcriptVM(th *Thread) *uipkg.TranscriptVM {
	vm := &uipkg.TranscriptVM{
		Running: a.isRunning(th.ID),
		List:    a.listState(th.ID),
		Md:      a.md(),
		Pal:     a.pal,
	}
	stamp := th.viewStamp()
	if cached, ok := a.transcriptCache[th.ID]; ok && cached.stamp == stamp {
		// Only Running is stamped outside the snapshot: it belongs to the
		// thread's run, not its messages, and flips without touching them.
		cached.vm.Running = a.isRunning(th.ID)
		vm.Messages = cached.vm.Messages
		return vm
	}
	group := len(a.panelFor(a.agentFor(th))) > 0
	for mi := range th.Messages {
		m := &th.Messages[mi]
		mv := uipkg.MessageVM{ID: m.ID, Role: m.Role, Text: m.Text, At: m.At, Running: m.Running}
		// A group thread labels every reply with its member (spec/agents.md);
		// solo threads keep the clean look.
		if group && m.Role == "assistant" && m.AgentID != "" {
			if ag := a.agentByID(m.AgentID); ag != nil {
				mv.AgentLabel = strings.TrimSpace(ag.Emoji + " " + ag.Name)
				mv.AgentEmoji = ag.Emoji
				mv.AgentName = ag.Name
			}
		}
		// The ordered sequence is the render input; the flattened cards
		// stay for the actions that address a block by position.
		mv.Items = itemize(m)
		for _, it := range mv.Items {
			mv.Blocks = append(mv.Blocks, it.Blocks...)
		}
		vm.Messages = append(vm.Messages, mv)
	}
	if a.transcriptCache == nil {
		a.transcriptCache = map[string]transcriptEntry{}
	}
	// The cached copy must not alias the caller's blocks: the view holds
	// this snapshot across frames and the host keeps mutating its own.
	snap := *vm
	a.transcriptCache[th.ID] = transcriptEntry{stamp: stamp, vm: &snap}
	return vm
}

// transcriptActions adapts *app to ui.TranscriptActions for one thread.
type transcriptActions struct {
	a  *app
	th *Thread
}

// ToggleBlock opens or closes every card of one kind in a message — the
// folded group a click comes from stands for all of them, wherever they
// sit between the prose, so one click opens the work and one click
// closes it. A group has no state of its own: the open state is the open
// state of its members, and it survives a reload with the thread.
func (h transcriptActions) ToggleBlock(msgID string, bi int) {
	for i := range h.th.Messages {
		m := &h.th.Messages[i]
		if m.ID != msgID || bi < 0 || bi >= len(m.Blocks) {
			continue
		}
		key := groupKey(&m.Blocks[bi])
		open := !m.Blocks[bi].Open
		// Errors and approvals are deliberately ungrouped — each says
		// something actionable of its own — so they flip alone.
		if !groupable(m.Blocks[bi].Type) {
			m.Blocks[bi].Open = open
			return
		}
		for j := range m.Blocks {
			if groupKey(&m.Blocks[j]) == key {
				m.Blocks[j].Open = open
			}
		}
		return
	}
}

func (h transcriptActions) Resend(msgID, text string) {
	h.a.resend(h.th, text)
}

func (h transcriptActions) Regenerate() {
	h.a.regenerate(h.th)
}

// ResolveApproval delivers an approval card's decision to the waiting
// call (spec/approvals.md).
func (h transcriptActions) ResolveApproval(callID string, approved bool) {
	d := harness.ApprovalDecision{Reason: "the user denied this call"}
	if approved {
		d = harness.ApprovalDecision{Approved: true}
	}
	h.a.resolveApproval(callID, d)
}

// transcriptEntry is one thread's last transcript snapshot and the stamp
// it was built from. A fresh stamp replaces it; a matching one is reused.
type transcriptEntry struct {
	stamp uint64
	vm    *uipkg.TranscriptVM
}
