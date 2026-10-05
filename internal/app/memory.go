package app

import "mygo-agent/internal/harness"

// threadMemory implements harness.Memory over the Host's thread store
// (spec/architecture.md): transcripts live on the Thread and persist with
// threads.json — one home for each datum.
type threadMemory struct {
	a *app
}

func (m threadMemory) LoadTranscript(key string) []harness.ChatMessage {
	th := m.a.threadByMemoryKey(key)
	if th == nil {
		return nil
	}
	return th.ChatLog
}

func (m threadMemory) StoreTranscript(key string, msgs []harness.ChatMessage) {
	th := m.a.threadByMemoryKey(key)
	if th == nil {
		return
	}
	m.a.update(func() {
		th.ChatLog = msgs
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
