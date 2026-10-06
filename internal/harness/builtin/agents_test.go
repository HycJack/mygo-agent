package builtin

// The agent-facing tool registry (spec/agents.md): the enable filter and
// the catalog, and the skill selection.

import (
	"slices"
	"testing"
)

func TestToolsEnabledFilter(t *testing.T) {
	all := Tools("", nil)
	if len(all) != 6 {
		t.Fatalf("registry = %d tools", len(all))
	}
	filtered := Tools("", nil, ToolOptions{Enabled: map[string]bool{
		"bash": false, "edit_file": false,
	}})
	var names []string
	for _, tl := range filtered {
		names = append(names, tl.Name)
	}
	want := []string{"read_file", "list_files", "grep", "read_skill"}
	if !slices.Equal(names, want) {
		t.Fatalf("filtered = %v, want %v", names, want)
	}
	// An unknown name in the map changes nothing.
	extra := Tools("", nil, ToolOptions{Enabled: map[string]bool{"nope": false}})
	if len(extra) != 6 {
		t.Fatalf("unknown names dropped tools: %d", len(extra))
	}
}

func TestToolCatalog(t *testing.T) {
	cat := ToolCatalog()
	var names []string
	for _, ti := range cat {
		names = append(names, ti.Name)
		if len(ti.Actions) == 0 {
			t.Fatalf("%s declares no actions", ti.Name)
		}
		if ti.Description == "" {
			t.Fatalf("%s has no description", ti.Name)
		}
	}
	want := []string{"bash", "read_file", "edit_file", "list_files", "grep", "read_skill"}
	if !slices.Equal(names, want) {
		t.Fatalf("catalog = %v, want %v", names, want)
	}
	// The actions match the permission catalog (spec/permissions.md).
	byName := map[string][]string{}
	for _, ti := range cat {
		byName[ti.Name] = ti.Actions
	}
	if !slices.Equal(byName["bash"], []string{"shell.exec"}) {
		t.Fatalf("bash actions = %v", byName["bash"])
	}
	if !slices.Equal(byName["read_skill"], []string{"skill.read"}) {
		t.Fatalf("read_skill actions = %v", byName["read_skill"])
	}
}

func skillSet(names ...string) *SkillSet {
	s := &SkillSet{byName: map[string]*Skill{}}
	for _, n := range names {
		s.Skills = append(s.Skills, Skill{Name: n})
		s.byName[n] = &s.Skills[len(s.Skills)-1]
	}
	return s
}

func skillNames(s *SkillSet) []string {
	var out []string
	for _, sk := range s.Skills {
		out = append(out, sk.Name)
	}
	return out
}

func TestSkillSetSelect(t *testing.T) {
	set := skillSet("alpha", "beta", "gamma")

	got := skillNames(set.Select(SkillSelection{Allow: []string{"beta"}}))
	if !slices.Equal(got, []string{"beta"}) {
		t.Fatalf("allow = %v", got)
	}
	got = skillNames(set.Select(SkillSelection{Deny: []string{"alpha"}}))
	if !slices.Equal(got, []string{"beta", "gamma"}) {
		t.Fatalf("deny = %v", got)
	}
	// Deny wins over Allow.
	got = skillNames(set.Select(SkillSelection{Allow: []string{"alpha", "beta"}, Deny: []string{"beta"}}))
	if !slices.Equal(got, []string{"alpha"}) {
		t.Fatalf("deny-wins = %v", got)
	}
	// An empty selection returns the receiver, not a copy.
	if set.Select(SkillSelection{}) != set {
		t.Fatal("empty selection rebuilt the set")
	}
	// nil survives.
	var none *SkillSet
	if none.Select(SkillSelection{Allow: []string{"x"}}) != nil {
		t.Fatal("nil set grew skills")
	}
	// The filtered set still loads by name.
	if _, ok := set.Select(SkillSelection{Allow: []string{"beta"}}).Load("alpha"); ok {
		t.Fatal("a filtered-out skill stayed loadable")
	}
}
