package harness

// Memory is the transcript-store protocol between the harness and the
// Host (spec/architecture.md): the builtin loop reads the conversation
// history for a thread key and returns the final transcript for the Host
// to persist. The harness never knows where transcripts live.
type Memory interface {
	// LoadTranscript returns the stored transcript for the key, or nil.
	LoadTranscript(key string) []ChatMessage
	// StoreTranscript persists the transcript for the key.
	StoreTranscript(key string, msgs []ChatMessage)
}

// memoryKey namespaces a thread's transcript under its project, so the
// same thread id in two projects never shares history.
func MemoryKey(projectID, threadID string) string {
	return projectID + "/" + threadID
}
