package goals

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// PlannotatorInterview captures interview.json fields.
type PlannotatorInterview struct {
	Project     string   `json:"project"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Scope       string   `json:"scope"`
	NonGoals    []string `json:"non_goals"`
}

// PlannotatorFactMeta captures fact entries in facts.meta.json or facts-review.json.
type PlannotatorFactMeta struct {
	ID                    string `json:"id"`
	Fact                  string `json:"fact"`
	AutomatedVerification bool   `json:"automatedVerification"`
	Verified              bool   `json:"verified"`
	Passed                bool   `json:"passed"`
	Source                string `json:"source"`
}

// ImportPlannotatorPackage imports a Plannotator directory or files into goals/<slug>.
func ImportPlannotatorPackage(rootDir, slug, sourceDir string) error {
	if slug == "" {
		return fmt.Errorf("goal slug is required")
	}
	slug = filepath.Clean(slug)
	rootDir = resolveGoalsRootDir(rootDir)
	goalDir := filepath.Join(rootDir, "goals", slug)
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		return fmt.Errorf("create goal dir: %w", err)
	}

	searchDirs := []string{sourceDir}
	if sourceDir == "" {
		searchDirs = []string{
			goalDir,
			filepath.Join(rootDir, ".plannotator", slug),
			filepath.Join(rootDir, ".plannotator"),
		}
	}

	findFile := func(filename string) string {
		for _, d := range searchDirs {
			if d == "" {
				continue
			}
			p := filepath.Join(d, filename)
			if fileExists(p) {
				return p
			}
		}
		return ""
	}

	// 1. Read interview.json
	interviewPath := findFile("interview.json")
	var interview PlannotatorInterview
	if interviewPath != "" {
		if data, err := os.ReadFile(interviewPath); err == nil {
			_ = json.Unmarshal(data, &interview)
		}
	}

	title := interview.Title
	if title == "" {
		title = slugToTitle(slug)
	}
	desc := interview.Description
	if desc == "" && interview.Scope != "" {
		desc = interview.Scope
	}
	if desc == "" {
		desc = fmt.Sprintf("Imported plannotator package for %s", slug)
	}

	// 2. Read facts.meta.json or facts-review.json or facts-result.json
	factsPath := findFile("facts.meta.json")
	if factsPath == "" {
		factsPath = findFile("facts-result.json")
	}
	if factsPath == "" {
		factsPath = findFile("facts-review.json")
	}

	var factsList []PlannotatorFactMeta
	if factsPath != "" {
		if data, err := os.ReadFile(factsPath); err == nil {
			// Try array of facts or { facts: [...] }
			if err := json.Unmarshal(data, &factsList); err != nil {
				type wrapper struct {
					Facts   []PlannotatorFactMeta `json:"facts"`
					Results []PlannotatorFactMeta `json:"results"`
				}
				var w wrapper
				if err2 := json.Unmarshal(data, &w); err2 == nil {
					if len(w.Facts) > 0 {
						factsList = w.Facts
					} else {
						factsList = w.Results
					}
				}
			}
		}
	}

	// 3. Write/update goal.md
	goalPath := filepath.Join(goalDir, "goal.md")
	if !fileExists(goalPath) {
		var gb strings.Builder
		gb.WriteString(fmt.Sprintf("# Goal: %s\n\n", title))
		gb.WriteString("## Goal Description\n")
		gb.WriteString(desc + "\n\n")
		gb.WriteString("## Dependencies & Execution Order\n")
		gb.WriteString("- **Mode**: Independent (disjoint, parallelizable)\n")
		gb.WriteString("- **Depends On**: none\n")
		gb.WriteString("- **Sequence**: -\n")
		gb.WriteString("- **Shape**: ship\n\n")
		gb.WriteString("## References\n")
		gb.WriteString(fmt.Sprintf("- **Shared Understanding & Fact Sheet**: [`goals/%s/facts.md`](facts.md)\n", slug))
		gb.WriteString(fmt.Sprintf("- **Execution Plan**: [`goals/%s/plan.md`](plan.md)\n\n", slug))
		gb.WriteString("## Done Condition\n")
		gb.WriteString("1. All requirements described in the goal and execution plan are implemented.\n")
		gb.WriteString("2. All automated unit and integration tests pass cleanly with `-race`.\n")
		gb.WriteString("3. Zero architectural regressions; definitions of done satisfied.\n")
		if err := AtomicWriteFile(goalPath, []byte(gb.String()), 0644); err != nil {
			return fmt.Errorf("write goal.md: %w", err)
		}
	}

	// 4. Write/update facts.md
	factsMdPath := filepath.Join(goalDir, "facts.md")
	if len(factsList) > 0 || !fileExists(factsMdPath) {
		var fb strings.Builder
		fb.WriteString(fmt.Sprintf("# Facts & Invariants: %s\n\n", title))
		fb.WriteString("## Ground Truth & Validated Invariants\n")
		if len(factsList) > 0 {
			fb.WriteString("| ID | Fact / Invariant | Automated Verification | Verified | Source |\n")
			fb.WriteString("| :--- | :--- | :---: | :---: | :--- |\n")
			for _, f := range factsList {
				autoStr := "false"
				if f.AutomatedVerification {
					autoStr = "true"
				}
				verStr := "pending"
				if f.Verified || f.Passed {
					verStr = "pass"
				}
				fb.WriteString(fmt.Sprintf("| `%s` | %s | `%s` | `%s` | %s |\n", f.ID, f.Fact, autoStr, verStr, f.Source))
			}
		} else {
			fb.WriteString("- Documented codebase facts and requirements.\n")
		}
		if len(interview.NonGoals) > 0 {
			fb.WriteString("\n## Non-Goals & Boundaries\n")
			for _, ng := range interview.NonGoals {
				fb.WriteString(fmt.Sprintf("- %s\n", ng))
			}
		}
		if err := AtomicWriteFile(factsMdPath, []byte(fb.String()), 0644); err != nil {
			return fmt.Errorf("write facts.md: %w", err)
		}
	}

	// 5. Check if plan.md exists in sourceDir
	sourcePlan := findFile("plan.md")
	targetPlan := filepath.Join(goalDir, "plan.md")
	if sourcePlan != "" && sourcePlan != targetPlan {
		if data, err := os.ReadFile(sourcePlan); err == nil {
			_ = AtomicWriteFile(targetPlan, data, 0644)
		}
	} else if !fileExists(targetPlan) {
		var pb strings.Builder
		pb.WriteString(fmt.Sprintf("# Plan: %s\n\n", title))
		pb.WriteString("## Execution Steps\n")
		pb.WriteString("- [ ] **Phase 1 — Contracts & Schemas**: Define models, data structures, and signatures.\n")
		pb.WriteString("- [ ] **Phase 2 — Core Implementation**: Implement business logic, routes, and error handling.\n")
		pb.WriteString("- [ ] **Phase 3 — Verification & Tests**: Write unit tests and run end-to-end verification.\n")
		pb.WriteString("- [ ] **Phase 4 — Attribution Commit**: Commit with `(goals/" + slug + "/goal.md)` or `[goal: " + slug + "]` using a `feat:`, `fix:`, `refactor:`, `perf:`, or `chore:` subject (docs/test commits are ignored), then run `sila goals`.\n")
		_ = AtomicWriteFile(targetPlan, []byte(pb.String()), 0644)
	}

	// Auto-sync README
	if audit, err := ScanGoals(rootDir); err == nil {
		_ = SyncReadme(rootDir, audit)
	}

	return nil
}
