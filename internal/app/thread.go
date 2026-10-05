package app

import (
	uipkg "mygo-agent/internal/ui"

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
func (a *app) transcriptVM(th *Thread) *uipkg.TranscriptVM {
	vm := &uipkg.TranscriptVM{
		Running: a.running,
		List:    a.listState(th.ID),
		Md:      a.md(),
		Pal:     a.pal,
	}
	for mi := range th.Messages {
		m := &th.Messages[mi]
		mv := uipkg.MessageVM{ID: m.ID, Role: m.Role, Text: m.Text, At: m.At, Running: m.Running}
		for bi := range m.Blocks {
			b := &m.Blocks[bi]
			mv.Blocks = append(mv.Blocks, uipkg.BlockVM{
				Type: b.Type, Text: b.Text, File: b.File, Output: b.Output,
				Add: b.Add, Del: b.Del, Lines: b.Lines,
				Exit: b.Exit, Ms: b.Ms, Open: b.Open, Edit: b.Edit, Running: b.Running,
				ToolID: b.ToolID, ApprovalID: b.ApprovalID,
			})
		}
		vm.Messages = append(vm.Messages, mv)
	}
	return vm
}

// transcriptActions adapts *app to ui.TranscriptActions for one thread.
type transcriptActions struct {
	a  *app
	th *Thread
}

func (h transcriptActions) ToggleBlock(msgID string, bi int) {
	for i := range h.th.Messages {
		m := &h.th.Messages[i]
		if m.ID != msgID || bi >= len(m.Blocks) {
			continue
		}
		m.Blocks[bi].Open = !m.Blocks[bi].Open
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
