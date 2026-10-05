package agent

import "testing"

func TestCompactHistoryKeepsRecentTurns(t *testing.T) {
	var msgs []ChatMessage
	msgs = append(msgs, ChatMessage{Role: "system", Content: "sys"})
	msgs = append(msgs, ChatMessage{Role: "user", Content: "first"})
	for round := 0; round < 10; round++ {
		msgs = append(msgs,
			ChatMessage{Role: "assistant", ToolCalls: []ToolCall{{ID: "c", Type: "function"}}},
			ChatMessage{Role: "tool", Content: "result", ToolCallID: "c"},
			ChatMessage{Role: "user", Content: "next"},
		)
	}
	got := CompactHistory(msgs, 14)
	if len(got) != 14 {
		t.Fatalf("len %d, want 14", len(got))
	}
	if got[0].Role != "system" || got[1].Role != "user" {
		t.Fatalf("system/first-user missing: %s / %s", got[0].Role, got[1].Role)
	}
	// The kept window starts on a user turn, never on an orphaned tool
	// result.
	for i := 1; i < len(got); i++ {
		if got[i].Role == "tool" && got[i-1].Role != "assistant" {
			t.Fatalf("orphaned tool result at %d", i)
		}
	}
}

func TestCompactHistoryNoopWhenSmall(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "system", Content: "s"},
		{Role: "user", Content: "u"},
		{Role: "assistant", Content: "a"},
	}
	if got := CompactHistory(msgs, 50); len(got) != len(msgs) {
		t.Fatalf("small transcript changed: %d", len(got))
	}
}
