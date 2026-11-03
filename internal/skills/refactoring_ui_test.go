package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRefactoringUISkills_ParityAndStructure(t *testing.T) {
	// Look for .agents/skills relative to this package or workspace root
	candidates := []string{
		filepath.Join("..", "..", ".agents", "skills"),
		filepath.Join(".", ".agents", "skills"),
	}

	var skillsDir string
	for _, c := range candidates {
		if fi, err := os.Stat(c); err == nil && fi.IsDir() {
			skillsDir = c
			break
		}
	}
	if skillsDir == "" {
		t.Skip("skills directory not found relative to test location")
	}

	expectedSkills := []string{
		"establish-visual-hierarchy",
		"apply-typography-scale",
		"build-color-palette",
		"apply-consistent-spacing",
		"design-button-hierarchy",
		"eliminate-visual-clutter",
		"design-empty-states",
		"use-shadows-appropriately",
		"manage-color-contrast",
		"group-related-elements",
		"refactor-ui",
	}

	for _, slug := range expectedSkills {
		t.Run(slug, func(t *testing.T) {
			skillPath := filepath.Join(skillsDir, slug, "SKILL.md")
			data, err := os.ReadFile(skillPath)
			if err != nil {
				t.Fatalf("missing required Refactoring UI skill: %v", err)
			}
			content := string(data)

			parsed := ParseSkillContent(slug, content)
			if parsed.Name == "" {
				t.Errorf("skill %s has empty Name", slug)
			}
			if parsed.Description == "" {
				t.Errorf("skill %s has empty Description", slug)
			}
			if !strings.Contains(content, "domain: ui-design") {
				t.Errorf("skill %s missing 'domain: ui-design' in frontmatter", slug)
			}
			if !strings.Contains(content, "## Verification & Self-Improvement Rubric") {
				t.Errorf("skill %s missing 'Verification & Self-Improvement Rubric'", slug)
			}
			if !strings.Contains(content, "sila lesson") && !strings.Contains(content, "sila feedback") {
				t.Errorf("skill %s missing sila lesson/feedback hooks in rubric", slug)
			}
		})
	}
}
