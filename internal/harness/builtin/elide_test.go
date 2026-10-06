package builtin

import (
	"strings"
	"testing"

	"mygo-agent/internal/harness"
)

// ElideToolResults is the layer between per-result trimming and dropping
// whole turns: over budget, the oldest tool results become one-line
// placeholders, oldest first, and the most recent messages are never
// touched — the model may still be working from them.

func elideFixture(n int) []harness.ChatMessage {
	msgs := []harness.ChatMessage{{Role: "user", Content: "go"}}
	for i := 0; i < n; i++ {
		msgs = append(msgs,
			harness.ChatMessage{Role: "assistant", ToolCalls: []harness.ToolCall{{ID: callID(i)}}},
			harness.ChatMessage{Role: "tool", Content: strings.Repeat("x", 40<<10), ToolCallID: callID(i)},
		)
	}
	return msgs
}

func callID(i int) string { return "call_" + string(rune('a'+i)) }

func TestElideKeepsTheBudgetByElidingOldestFirst(t *testing.T) {
	msgs := elideFixture(12) // 12 × 40KB = 480KB
	budget := 300 << 10      // reachable: eliding ~5 results gets under it

	out := ElideToolResults(msgs, budget)

	var held int
	for _, m := range out {
		if m.Role == "tool" {
			held += len(m.Content.(string))
		}
	}
	if held > budget+len("[output elided: 40960 bytes]") {
		t.Fatalf("the budget did not hold: %d bytes of tool results", held)
	}
	// Oldest first: an early result is a placeholder, the newest are not.
	if !strings.Contains(out[2].Content.(string), "output elided") {
		t.Fatalf("the oldest result survived: %q", out[2].Content)
	}
	last := out[len(out)-1].Content.(string)
	if strings.Contains(last, "output elided") {
		t.Fatal("a recent result was elided before the budget demanded it")
	}
	// Pairing survives: the tool message and its call id are still a pair.
	if out[2].ToolCallID != callID(0) {
		t.Fatalf("eliding broke the pairing: %q", out[2].ToolCallID)
	}
}

// TestElideYieldsTheBudgetToTheRecentWindow pins the priority when the
// two conflict: five recent results × 40KB already outweigh a 100KB
// budget, and the window is the thing that holds — everything elidable
// is elided, and the recent results are not.
func TestElideYieldsTheBudgetToTheRecentWindow(t *testing.T) {
	msgs := elideFixture(12)
	out := ElideToolResults(msgs, 100<<10)

	var elidable, recent int
	for i, m := range out {
		if m.Role != "tool" {
			continue
		}
		if strings.Contains(m.Content.(string), "output elided") {
			elidable++
			if i >= len(out)-10 {
				t.Fatalf("a recent message (%d) was elided", i)
			}
		} else {
			recent++
		}
	}
	if elidable != 7 || recent != 5 {
		t.Fatalf("got %d elided / %d intact, want 7 / 5 — the window must hold", elidable, recent)
	}
}

func TestElideNeverTouchesTheRecentWindow(t *testing.T) {
	msgs := elideFixture(4) // 160KB, all of it inside the recent window
	out := ElideToolResults(msgs, 1<<10)
	for i, m := range out {
		if m.Role == "tool" && strings.Contains(m.Content.(string), "output elided") {
			t.Fatalf("message %d was elided inside the recent window", i)
		}
	}
}

func TestElideLeavesAnUnderBudgetTranscriptAlone(t *testing.T) {
	msgs := elideFixture(2)
	out := ElideToolResults(msgs, 200<<10)
	for i, m := range out {
		if m.Role == "tool" && m.Content.(string) != msgs[i].Content.(string) {
			t.Fatalf("an under-budget transcript was changed at %d", i)
		}
	}
}

func TestElideFallsBackToTheDefaultBudget(t *testing.T) {
	// The default budget with the same deep fixture: five recent results
	// (200KB) outweigh 128KB, so the window holds and everything outside
	// it is elided — and the zero budget still resolved to the default,
	// not to "unbounded" (7 elided, not 0).
	msgs := elideFixture(12)
	out := ElideToolResults(msgs, 0)
	var elided int
	for _, m := range out {
		if m.Role == "tool" && strings.Contains(m.Content.(string), "output elided") {
			elided++
		}
	}
	if elided != 7 {
		t.Fatalf("got %d elided with the default budget, want 7", elided)
	}
}
