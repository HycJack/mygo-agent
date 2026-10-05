package app

import (
	"slices"

	"mygo-agent/internal/harness"
)

// threadMemory implements harness.Memory over the Host's thread store
// (spec/architecture.md): transcripts live on the Thread and persist with
// the thread file — one home for each datum.
type threadMemory struct {
	a *app
}

// LoadTranscript runs on the harness goroutine, not the main thread, so
// it goes through update() like every other host-state access and hands
// back a copy: the loop appends to what it is given, and a shared backing
// array would be a data race with the main thread's own saves.
func (m threadMemory) LoadTranscript(key string) []harness.ChatMessage {
	var out []harness.ChatMessage
	m.a.update(func() {
		th := m.a.threadByMemoryKey(key)
		if th == nil {
			return
		}
		out = slices.Clone(th.ChatLog)
	})
	return out
}

// StoreTranscript likewise runs on the harness goroutine; the final
// transcript replaces the live one on the main thread and is persisted
// with the thread.
func (m threadMemory) StoreTranscript(key string, msgs []harness.ChatMessage) {
	m.a.update(func() {
		th := m.a.threadByMemoryKey(key)
		if th == nil {
			return
		}
		th.ChatLog = slices.Clone(msgs)
		m.a.saveThread(th)
	})
}

// threadByMemoryKey resolves a "<project>/<thread>" key back to the
// thread, checking that the project part still matches.
func (a *app) threadByMemoryKey(key string) *Thread {
	for _, th := range a.threads {
		if harness.MemoryKey(th.ProjectID, th.ID) == key {
			return th
		}
	}
	return nil
}
