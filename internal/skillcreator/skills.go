package skillcreator

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jadmadi/sila/internal/skills"
)

// ProjectSkill represents a structured inspection of a project agent skill on disk.
type ProjectSkill struct {
	Slug        string   `json:"slug"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Path        string   `json:"path"`
	HasScripts  bool     `json:"has_scripts"`
	HasRefs     bool     `json:"has_references"`
	Triggers    []string `json:"triggers,omitempty"`
	RawMarkdown string   `json:"raw_markdown,omitempty"`
}

// ListProjectSkills scans the project directory (.agents/skills/) and returns
// structured metadata for all discovered skills, sorted alphabetically by slug.
func ListProjectSkills(projectDir string) ([]ProjectSkill, error) {
	skillsDir := filepath.Join(projectDir, ".agents", "skills")
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read skills dir %s: %w", skillsDir, err)
	}

	realSkillsDir, _ := filepath.EvalSymlinks(skillsDir)
	visitedDirs := make(map[string]bool)

	var skillsList []ProjectSkill
	for _, e := range entries {
		targetPath := filepath.Join(skillsDir, e.Name())

		fi, err := os.Stat(targetPath)
		if err != nil || !fi.IsDir() {
			continue
		}

		realPath, err := filepath.EvalSymlinks(targetPath)
		if err != nil {
			continue
		}

		if realSkillsDir != "" && realPath == realSkillsDir {
			continue
		}

		if visitedDirs[realPath] {
			continue
		}
		visitedDirs[realPath] = true

		slug := e.Name()
		skillPath := filepath.Join(targetPath, "SKILL.md")
		data, err := os.ReadFile(skillPath)
		if err != nil {
			skillPath = filepath.Join(targetPath, "skill.md")
			data, err = os.ReadFile(skillPath)
			if err != nil {
				continue
			}
		}

		relDisplayPath := filepath.Join(".agents", "skills", slug, filepath.Base(skillPath))
		skill := parseSkillMarkdown(slug, relDisplayPath, string(data))

		// Check scripts and references subdirectories
		if sEntries, sErr := os.ReadDir(filepath.Join(targetPath, "scripts")); sErr == nil && len(sEntries) > 0 {
			skill.HasScripts = true
		}
		if rEntries, rErr := os.ReadDir(filepath.Join(targetPath, "references")); rErr == nil && len(rEntries) > 0 {
			skill.HasRefs = true
		}

		skillsList = append(skillsList, skill)
	}

	sort.Slice(skillsList, func(i, j int) bool {
		return skillsList[i].Slug < skillsList[j].Slug
	})

	return skillsList, nil
}

// LoadProjectSkill reads and parses a specific project skill by slug or relative path.
func LoadProjectSkill(projectDir, slugOrPath string) (*ProjectSkill, error) {
	cleanSlug := strings.TrimSpace(slugOrPath)
	cleanSlug = strings.TrimPrefix(cleanSlug, ".agents/skills/")
	cleanSlug = strings.TrimSuffix(cleanSlug, "/SKILL.md")
	cleanSlug = strings.TrimSuffix(cleanSlug, "/skill.md")
	cleanSlug = strings.Trim(cleanSlug, "/")

	if cleanSlug == "" || cleanSlug == "." || cleanSlug == ".." || filepath.Base(cleanSlug) != cleanSlug || strings.ContainsAny(cleanSlug, `/\\`) {
		return nil, fmt.Errorf("invalid skill name %q", slugOrPath)
	}

	skillDir := filepath.Join(projectDir, ".agents", "skills", cleanSlug)
	fi, err := os.Stat(skillDir)
	if err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("skill %q not found in %s", cleanSlug, filepath.Join(".agents", "skills", cleanSlug))
	}

	realDir, err := filepath.EvalSymlinks(skillDir)
	if err != nil {
		realDir = skillDir
	}

	skillPath := filepath.Join(realDir, "SKILL.md")
	data, err := os.ReadFile(skillPath)
	if err != nil {
		skillPath = filepath.Join(realDir, "skill.md")
		data, err = os.ReadFile(skillPath)
		if err != nil {
			return nil, fmt.Errorf("skill %q not found in %s", cleanSlug, filepath.Join(".agents", "skills", cleanSlug))
		}
	}

	relPath := filepath.Join(".agents", "skills", cleanSlug, filepath.Base(skillPath))
	skill := parseSkillMarkdown(cleanSlug, relPath, string(data))
	skill.RawMarkdown = string(data)

	if sEntries, sErr := os.ReadDir(filepath.Join(realDir, "scripts")); sErr == nil && len(sEntries) > 0 {
		skill.HasScripts = true
	}
	if rEntries, rErr := os.ReadDir(filepath.Join(realDir, "references")); rErr == nil && len(rEntries) > 0 {
		skill.HasRefs = true
	}

	return &skill, nil
}

func parseSkillMarkdown(slug, relPath, content string) ProjectSkill {
	parsed := skills.ParseSkillContent(slug, content)

	skill := ProjectSkill{
		Slug:        slug,
		Name:        parsed.Name,
		Description: parsed.Description,
		Path:        relPath,
		Triggers:    make([]string, 0),
		RawMarkdown: content,
	}

	// Scan markdown body for triggers and title fallback
	scanner := bufio.NewScanner(strings.NewReader(parsed.Body))
	inTriggers := false

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "# ") && skill.Name == slug {
			skill.Name = strings.TrimSpace(strings.TrimPrefix(trimmed, "# "))
		}

		if strings.HasPrefix(trimmed, "## Trigger Scenarios") || strings.HasPrefix(trimmed, "## Triggers") {
			inTriggers = true
			continue
		} else if strings.HasPrefix(trimmed, "## ") && inTriggers {
			inTriggers = false
		}

		if inTriggers {
			if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") || strings.HasPrefix(trimmed, "• ") {
				tText := strings.TrimSpace(trimmed[2:])
				if tText != "" {
					skill.Triggers = append(skill.Triggers, tText)
				}
			}
		}
	}

	return skill
}
