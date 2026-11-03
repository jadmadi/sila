package skillcreator

import (
	"os"
	"path/filepath"

	"github.com/jadmadi/sila/internal/config"
	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/store"
)

// ProjectGrounding captures the active repository's stack, rules, and memory.
type ProjectGrounding struct {
	ProjectDir      string   `json:"project_dir"`
	ProjectID       string   `json:"project_id"`
	TechStack       []string `json:"tech_stack"`
	Manifests       []string `json:"manifests"`
	RulesSummary    string   `json:"rules_summary"`
	NegativeLessons []string `json:"negative_lessons"`
	ActiveGoals     []string `json:"active_goals"`
	ExistingSkills  []string `json:"existing_skills"`
}

// CollectProjectGrounding extracts ground truth from the project filesystem and store.
func CollectProjectGrounding(st *store.Store, projectDir string) (*ProjectGrounding, error) {
	if projectDir == "" {
		if cwd, err := os.Getwd(); err == nil {
			projectDir = cwd
		} else {
			projectDir = "."
		}
	}
	projectDir = filepath.Clean(projectDir)

	g := &ProjectGrounding{
		ProjectDir:      projectDir,
		TechStack:       make([]string, 0),
		Manifests:       make([]string, 0),
		NegativeLessons: make([]string, 0),
		ActiveGoals:     make([]string, 0),
		ExistingSkills:  make([]string, 0),
	}

	// 1. Detect manifests & tech stack (deterministic ordered inspection)
	manifestList := []struct {
		file  string
		stack string
	}{
		{"go.mod", "Go"},
		{"package.json", "Node/TypeScript"},
		{"wrangler.jsonc", "Cloudflare Workers"},
		{"wrangler.toml", "Cloudflare Workers"},
		{"Cargo.toml", "Rust"},
		{"pyproject.toml", "Python"},
		{"requirements.txt", "Python"},
		{"pom.xml", "Java/Maven"},
		{"build.gradle", "Java/Gradle"},
		{"Gemfile", "Ruby"},
	}

	for _, m := range manifestList {
		mPath := filepath.Join(projectDir, m.file)
		if _, err := os.Stat(mPath); err == nil {
			g.Manifests = append(g.Manifests, m.file)
			found := false
			for _, s := range g.TechStack {
				if s == m.stack {
					found = true
					break
				}
			}
			if !found {
				g.TechStack = append(g.TechStack, m.stack)
			}
		}
	}

	// 2. Read AGENTS.md rules
	for _, ruleCandidate := range []string{"AGENTS.md", "AGENTS.latest.md", "CLAUDE.md", ".cursorrules"} {
		rPath := filepath.Join(projectDir, ruleCandidate)
		if data, err := os.ReadFile(rPath); err == nil {
			summary := string(data)
			runes := []rune(summary)
			if len(runes) > 2500 {
				summary = string(runes[:2450]) + "\n... [truncated for prompt economy]"
			}
			g.RulesSummary = summary
			break
		}
	}

	// 3. Scan existing skills in .agents/skills/
	skillsDir := filepath.Join(projectDir, ".agents", "skills")
	if entries, err := os.ReadDir(skillsDir); err == nil {
		realSkillsDir, _ := filepath.EvalSymlinks(skillsDir)
		visitedDirs := make(map[string]bool)
		for _, e := range entries {
			targetPath := filepath.Join(skillsDir, e.Name())
			fi, err := os.Stat(targetPath)
			if err != nil || !fi.IsDir() {
				continue
			}
			realPath, err := filepath.EvalSymlinks(targetPath)
			if err != nil || (realSkillsDir != "" && realPath == realSkillsDir) || visitedDirs[realPath] {
				continue
			}
			visitedDirs[realPath] = true
			g.ExistingSkills = append(g.ExistingSkills, e.Name())
		}
	}

	// 4. Scan active goals
	if res, err := goals.ScanGoals(projectDir); err == nil && res != nil {
		for _, goal := range res.Goals {
			if goal.Status == "pending" || goal.Status == "ready" || goal.Status == "claimed" {
				g.ActiveGoals = append(g.ActiveGoals, goal.Slug+": "+goal.Title)
				if len(g.ActiveGoals) >= 6 {
					break
				}
			}
		}
	}

	// 5. Query institutional negative lessons and project ID from store
	if st != nil {
		if pName := filepath.Base(projectDir); pName != "" {
			g.ProjectID = pName
		}
		if lessons, err := st.LessonFacts("", 10); err == nil {
			for _, l := range lessons {
				g.NegativeLessons = append(g.NegativeLessons, l.Claim)
				if len(g.NegativeLessons) >= 5 {
					break
				}
			}
		}
	}

	// Fallback project ID from config
	if g.ProjectID == "" {
		if pCfgPath := config.FindProjectConfig(projectDir); pCfgPath != "" {
			g.ProjectID = filepath.Base(filepath.Dir(filepath.Dir(pCfgPath)))
		}
	}
	if g.ProjectID == "" {
		g.ProjectID = filepath.Base(projectDir)
	}

	return g, nil
}
