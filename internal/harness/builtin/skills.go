package builtin

import (
	"os"
	"path/filepath"
	"strings"
)

// Skill is one discovered skill: a directory whose SKILL.md carries
// frontmatter (name, description) and the instructions themselves.
type Skill struct {
	Name        string
	Description string
	Dir         string // the skill directory; SKILL.md lives inside
}

// SkillSet is every skill discovered for a project.
type SkillSet struct {
	Skills []Skill
	byName map[string]*Skill
}

// DiscoverSkills scans the project's .agents/skills (walking up to the
// repository root), ~/.agents/skills and ~/.codex-go/skills for
// directories containing SKILL.md, per the Agent Skills specification.
func DiscoverSkills(projectDir string) *SkillSet {
	set := &SkillSet{byName: map[string]*Skill{}}
	roots := skillRoots(projectDir)
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			dir := filepath.Join(root, e.Name())
			data, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
			if err != nil {
				continue
			}
			s := parseSkill(dir, string(data))
			if s.Name == "" {
				s.Name = e.Name()
			}
			if _, dup := set.byName[s.Name]; dup {
				continue
			}
			set.Skills = append(set.Skills, s)
			set.byName[s.Name] = &set.Skills[len(set.Skills)-1]
		}
	}
	return set
}

// skillRoots lists the skill directories, project-scoped first.
func skillRoots(projectDir string) []string {
	var roots []string
	// Walk up from the project to its repository root for .agents/skills.
	dir := projectDir
	for range 8 {
		roots = append(roots, filepath.Join(dir, ".agents", "skills"))
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	if home, err := os.UserHomeDir(); err == nil {
		roots = append(roots,
			filepath.Join(home, ".agents", "skills"),
			filepath.Join(home, ".codex-go", "skills"),
		)
	}
	return roots
}

// parseSkill reads the frontmatter's name and description out of a
// SKILL.md's body.
func parseSkill(dir, content string) Skill {
	s := Skill{Dir: dir}
	lines := strings.Split(content, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return s
	}
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "---" {
			break
		}
		if v, ok := strings.CutPrefix(line, "name:"); ok {
			s.Name = strings.TrimSpace(v)
		}
		if v, ok := strings.CutPrefix(line, "description:"); ok {
			s.Description = strings.TrimSpace(v)
		}
	}
	return s
}

// Load returns a skill's full SKILL.md content.
func (s *SkillSet) Load(name string) (string, bool) {
	sk, ok := s.byName[name]
	if !ok {
		return "", false
	}
	path := filepath.Join(sk.Dir, "SKILL.md")
	// The stat is the memory guard, not the context cap (read_skill
	// trims after): a pathologically large SKILL.md is refused whole
	// rather than read into memory first.
	if st, err := os.Stat(path); err != nil || st.Size() > 2<<20 {
		return "", false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	body := string(data)
	// The model already knows name and description; keep the body lean.
	return body, true
}

// PromptSection renders the system prompt's skill advertisement: names
// and descriptions only, the way pi does it.
func (s *SkillSet) PromptSection() string {
	if s == nil || len(s.Skills) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n## Skills\n\nSpecialised instructions you can load with the read_skill tool. Load one when the task matches its description.\n")
	for _, sk := range s.Skills {
		desc := sk.Description
		if desc == "" {
			desc = "(no description)"
		}
		b.WriteString("- " + sk.Name + ": " + desc + "\n")
	}
	return b.String()
}
