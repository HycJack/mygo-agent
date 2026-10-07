package ui

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestMentionQuery: the popup opens on the draft's last unclosed @ and
// closes once whitespace follows it — no caret needed, typing at the
// end is the case.
func TestMentionQuery(t *testing.T) {
	cases := []struct {
		draft string
		query string
		open  bool
	}{
		{"hello @", "", true},
		{"hello @架", "架", true},
		{"hello @架构 ", "", false}, // whitespace closes the mention
		{"hello @架构\n", "", false},
		{"@A done, now @", "", true},
		{"no mention at all", "", false},
		{"email me@somewhere", "somewhere", true}, // an @ is an @; harmless
	}
	for _, tc := range cases {
		q, open := MentionQuery(tc.draft)
		if open != tc.open || q != tc.query {
			t.Fatalf("MentionQuery(%q) = %q,%v want %q,%v", tc.draft, q, open, tc.query, tc.open)
		}
	}
}

// TestMentionCandidatesAndApply: prefix filter (case-insensitive), cap
// at six, and completion replaces the trailing "@query" with "@name ".
func TestMentionCandidatesAndApply(t *testing.T) {
	names := []string{"需求", "架构", "开发", "测试", "发布", "运营", "产品流水线"}
	if got := MentionCandidates("", names); len(got) != 6 {
		t.Fatalf("empty query = %v, want the first six", got)
	}
	if got := MentionCandidates("测", names); len(got) != 1 || got[0] != "测试" {
		t.Fatalf("prefix 测 = %v", got)
	}
	if got := MentionCandidates("zz", names); len(got) != 0 {
		t.Fatalf("no match = %v", got)
	}
	got := ApplyMention("评估一下 @架", "架构")
	if got != "评估一下 @架构 " {
		t.Fatalf("apply = %q", got)
	}
	if ApplyMention("no at", "架构") != "no at" {
		t.Fatal("apply without an @ must be a no-op")
	}
}

// TestMentionPopupCompletes: the popup lists the panel members while an
// @ is being typed, hides when there is none, and clicking a candidate
// completes the draft.
func TestMentionPopupCompletes(t *testing.T) {
	vm := &ViewModel{Draft: "评估一下 @架", Mentions: []string{"架构"}, Pal: CodexPalette()}
	acts := nopActions{}
	tt := ui.NewTester(func(c *ui.Context) { Composer(c, vm, acts) }, 900, 400)
	if !tt.HasText("@架构") {
		t.Fatalf("the candidate pill is missing: %v", tt.Texts())
	}
	if err := tt.Click("@架构"); err != nil {
		t.Fatalf("clicking the candidate: %v", err)
	}
	if vm.Draft != "评估一下 @架构 " || vm.Mentions != nil {
		t.Fatalf("draft = %q mentions = %v", vm.Draft, vm.Mentions)
	}
	// No @ in progress: no popup.
	vm2 := &ViewModel{Draft: "plain text", Pal: CodexPalette()}
	tt2 := ui.NewTester(func(c *ui.Context) { Composer(c, vm2, acts) }, 900, 400)
	if tt2.HasText("@架构") {
		t.Fatalf("the popup showed without a mention in progress: %v", tt2.Texts())
	}
}

// nopActions satisfies the composer's callback interface.
type nopActions struct{}

func (nopActions) Send()                          {}
func (nopActions) Stop()                          {}
func (nopActions) SetDraft(string)                {}
func (nopActions) SetMode(int)                    {}
func (nopActions) SetEffort(int)                  {}
func (nopActions) PickModel(string, string)       {}
func (nopActions) OpenSettings(string)            {}
func (nopActions) SetAgent(string)                {}
func (nopActions) NewGroup()                      {}
func (nopActions) SaveConfig()                    {}
