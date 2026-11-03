package heal

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/onboard"
	"github.com/jadmadi/sila/internal/store"
)

// Step is one healing unit. Automatic steps are mechanical and runnable by
// sila; judgment steps need a human or an agent to supply content.
type Step struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Command   string   `json:"command,omitempty"`
	Automatic bool     `json:"automatic"`
	Reason    string   `json:"reason,omitempty"`
	Files     []string `json:"files,omitempty"`
}

// Plan is the healing plan for one repository.
type Plan struct {
	RepoRoot   string                   `json:"repo_root"`
	Project    string                   `json:"project"`
	Automatic  int                      `json:"automatic"`
	Judgment   int                      `json:"judgment"`
	Steps      []Step                   `json:"steps"`
	AliasPairs []store.ProjectMergePair `json:"-"`
	GoalsScan  *goals.AuditResult       `json:"-"`
}

// BuildPlan inspects one repository and returns its healing plan. It is the
// per-repo unit of work: each agent heals the repository it is working in, so
// the fleet converges through use instead of a central sweep.
func BuildPlan(st *store.Store, repoRoot string) (*Plan, error) {
	plan := &Plan{RepoRoot: repoRoot}
	pid := store.ProjectID(repoRoot)
	if st != nil {
		p, ok, err := st.ProjectForDir(repoRoot)
		switch {
		case err == nil && ok:
			pid = p.ID
		case err == nil && !st.ReadOnly:
			// A repository sila is healing gets its own registration:
			// resolution never invents a row, and since repository roots no
			// longer inherit a registered container's identity, an
			// unregistered repository would have no project to scope its
			// rules, handoffs, and facts to. Dry runs and read-only handles
			// (salam, inspection) never write.
			reg, regErr := st.EnsureProject(repoRoot)
			if regErr != nil {
				return nil, regErr
			}
			pid = reg.ID
		}
	}
	plan.Project = pid

	// 1. Duplicate registrations that involve this repository.
	if st != nil {
		if pairs, err := st.AliasDuplicatePairs(); err == nil {
			var names []string
			for _, p := range pairs {
				if !sameDir(p.KeepPath, repoRoot) && !sameDir(p.DropPath, repoRoot) {
					continue
				}
				plan.AliasPairs = append(plan.AliasPairs, p)
				names = append(names, p.KeepID+" ↔ "+p.DropID)
			}
			if len(names) > 0 {
				plan.Steps = append(plan.Steps, Step{
					ID:        "aliases",
					Title:     fmt.Sprintf("Merge %d duplicate %s for this repository", len(names), plural(len(names), "registration")),
					Command:   "sila projects merge <keep> <drop>",
					Automatic: true,
					Reason:    "the same real directory is registered under two paths; goals, claims, and counts are doubled",
				})
			}
		}
	}

	// 2. Wiring and knowledge gaps for this repository.
	rep := onboard.CheckWithOptions(st, repoRoot, plan.Project, false, true)
	plan.GoalsScan = rep.GoalsScan
	needProvision := false
	var provisionFiles []string
	for _, item := range rep.Items {
		switch item.Name {
		case "Agent rules", "Sila skill":
			if item.Status != "pass" {
				needProvision = true
				if len(item.Files) > 0 {
					provisionFiles = append(provisionFiles, item.Files...)
				}
			}
		case "Git hook":
			if item.Status != "pass" {
				needProvision = true
				provisionFiles = append(provisionFiles, ".git/hooks/pre-commit")
			}
		case "MCP server":
			if item.Status != "pass" && len(item.Files) > 0 {
				needProvision = true
				provisionFiles = append(provisionFiles, item.Files...)
			} else if item.Status != "pass" {
				plan.Steps = append(plan.Steps, judgmentStep("mcp", "Configure MCP client",
					"sila install --no-hooks",
					"register sila MCP server in your preferred AI coding client"))
			}
		case "Session handoff":
			if item.Status != "pass" {
				plan.Steps = append(plan.Steps, judgmentStep("handoff", "Record a session handoff",
					"sila handoff \"<summary>\" --open=\"<task>\"",
					"the next session starts from the handoff; only the agent knows what changed"))
			}
		case "Recorded directives":
			if item.Status != "pass" {
				plan.Steps = append(plan.Steps, judgmentStep("memo", "Record architectural directives",
					"sila memo \"<directive>\"",
					"decisions must be recorded deliberately; sila never invents them"))
			}
		case "Solution lessons":
			if item.Status != "pass" {
				plan.Steps = append(plan.Steps, judgmentStep("lesson", "Record solved problems",
					"sila lesson \"<symptom>\" --cause=... --fix=...",
					"lessons are the write side of recall"))
			}
		case "Legacy inventory":
			if item.Status != "pass" {
				plan.Steps = append(plan.Steps, judgmentStep("legacy", "Map legacy task surfaces",
					"sila onboard --json",
					"brain stores and markdown lists are not sila goals; mapping them needs review"))
			}
		}
	}
	if needProvision {
		hasSkillPath := false
		for _, f := range provisionFiles {
			if f == ".agents/skills/sila/SKILL.md" {
				hasSkillPath = true
				break
			}
		}
		if !hasSkillPath {
			if _, err := os.Stat(filepath.Join(repoRoot, ".agents", "skills", "sila", "SKILL.md")); err != nil {
				provisionFiles = append(provisionFiles, ".agents/skills/sila/SKILL.md")
			}
		}
		onboardingGoalMissing := !onboard.HasTrackedGoalsOrMarker(repoRoot)
		if onboardingGoalMissing {
			provisionFiles = append(provisionFiles,
				"goals/sila-onboarding/goal.md",
				"goals/sila-onboarding/plan.md",
				"goals/sila-onboarding/facts.md",
			)
		}
		if _, err := os.Stat(filepath.Join(repoRoot, ".git")); err == nil {
			if onboardingGoalMissing && !hasGitIgnoreHygiene(repoRoot) {
				provisionFiles = append(provisionFiles, ".gitignore")
			}
		}
		plan.Steps = append(plan.Steps, Step{
			ID:        "provision",
			Title:     "Provision sila wiring in this repository",
			Command:   "sila onboard",
			Automatic: true,
			Reason:    "managed rules, the Sila skill, git hooks, MCP configs, the OpenCode plugin, and the completion goal",
			Files:     provisionFiles,
		})
	}

	// 3. Claim hygiene for this repository.
	if paths, err := goals.PruneClosedClaimsFromScan(repoRoot, rep.GoalsScan, true); err == nil && len(paths) > 0 {
		plan.Steps = append(plan.Steps, Step{
			ID:        "prune",
			Title:     fmt.Sprintf("Prune %d claim %s on closed goals", len(paths), plural(len(paths), "record")),
			Command:   "sila goals prune --claims",
			Automatic: true,
			Reason:    "locks survived on goals that already shipped",
			Files:     paths,
		})
	}
	if released, err := goals.ReleaseStaleClaims(repoRoot, 72*time.Hour, true); err == nil && len(released) > 0 {
		var staleFiles []string
		for _, r := range released {
			staleFiles = append(staleFiles, filepath.Join("goals", r.Goal, "claim.json"))
		}
		plan.Steps = append(plan.Steps, Step{
			ID:        "stale",
			Title:     fmt.Sprintf("Release %d stale %s", len(released), plural(len(released), "claim")),
			Command:   "sila goals release --stale",
			Automatic: true,
			Reason:    "claims older than 72h whose recorded process is gone or inactive",
			Files:     staleFiles,
		})
	}
	if needsIgnore, err := goals.NeedsGitIgnoreClaim(repoRoot); err == nil && needsIgnore {
		plan.Steps = append(plan.Steps, Step{
			ID:        "gitignore-claims",
			Title:     "Ignore ephemeral goal claims and .sila/state/ in .gitignore",
			Command:   "sila heal",
			Automatic: true,
			Reason:    "goals/*/claim.json and .sila/state/ are ephemeral working state and should not dirty git working tree",
			Files:     []string{".gitignore"},
		})
	}

	// 4. Cloudflare cfclean credential sanitization filter.
	if NeedsCFGitFilter(repoRoot) {
		plan.Steps = append(plan.Steps, Step{
			ID:        "cfclean",
			Title:     "Configure cfclean credential sanitization filter in .gitattributes and git config",
			Command:   "sila heal",
			Automatic: true,
			Reason:    "Cloudflare Worker project detected; cfclean sanitizes account IDs and database UUIDs before git commits",
			Files:     []string{".gitattributes"},
		})
	}

	// 5. Runtime restarts, reported when replaced or orphaned MCP server binaries are running.
	if st != nil {
		if _, c := store.InspectMCPFreshness(); c != nil && c.Status != store.StatusPass {
			detail := c.Detail
			if detail == "" {
				detail = c.Message
			}
			plan.Steps = append(plan.Steps, judgmentStep("restart-mcp", "Restart MCP clients to pick up the current binary",
				"restart the listed clients (agy, opencode, ...)", detail))
		}
	}

	for _, s := range plan.Steps {
		if s.Automatic {
			plan.Automatic++
		} else {
			plan.Judgment++
		}
	}
	return plan, nil
}

func judgmentStep(id, title, command, reason string) Step {
	return Step{ID: id, Title: title, Command: command, Reason: reason}
}

func hasGitIgnoreHygiene(repoRoot string) bool {
	data, err := os.ReadFile(filepath.Join(repoRoot, ".gitignore"))
	if err != nil {
		return false
	}
	content := string(data)
	return strings.Contains(content, goals.ClaimIgnorePattern) && strings.Contains(content, goals.StateIgnorePattern)
}

// sameDir reports whether a registered path and a local path are the same real directory.
func sameDir(registered, local string) bool {
	ra, errA := filepath.EvalSymlinks(registered)
	rb, errB := filepath.EvalSymlinks(local)
	if errA != nil || errB != nil {
		return false
	}
	return filepath.Clean(ra) == filepath.Clean(rb)
}

func plural(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}

// ApplyAliases merges the duplicate pairs the plan scoped to this repository.
func ApplyAliases(st *store.Store, plan *Plan) ([]string, error) {
	var merged []string
	for _, p := range plan.AliasPairs {
		if err := st.MergeProjects(p.KeepID, p.DropID); err != nil {
			return merged, fmt.Errorf("merge %s into %s: %w", p.DropID, p.KeepID, err)
		}
		merged = append(merged, fmt.Sprintf("merged %s into %s", p.DropID, p.KeepID))
	}
	return merged, nil
}

// RepoRootPath returns the cleaned repository root a heal run targets.
func RepoRootPath(path string) string {
	if strings.TrimSpace(path) == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return ""
		}
		return filepath.Clean(cwd)
	}
	return filepath.Clean(path)
}

// StackInfo aliases onboard.StackInfo for callers in the heal package.
type StackInfo = onboard.StackInfo

// DetectProjectStack inspects the repository and returns its detected stack.
func DetectProjectStack(repoRoot string) StackInfo {
	return onboard.DetectProjectStack(repoRoot)
}

// PlanHeal builds the healing plan for a repository without requiring a persistent store.
func PlanHeal(repoRoot string) (*Plan, error) {
	return BuildPlan(nil, RepoRootPath(repoRoot))
}

// ApplyHeal applies automatic healing steps to the repository and returns the updated plan.
func ApplyHeal(repoRoot string) (*Plan, error) {
	root := RepoRootPath(repoRoot)
	plan, err := BuildPlan(nil, root)
	if err != nil {
		return nil, err
	}
	if plan.Automatic == 0 {
		return plan, nil
	}
	stack := DetectProjectStack(root)
	for _, step := range plan.Steps {
		if !step.Automatic {
			continue
		}
		switch step.ID {
		case "provision":
			_ = onboard.Provision(root, plan.Project, stack.Primary)
		case "prune":
			_, _ = goals.PruneClosedClaims(root, false)
		case "stale":
			_, _ = goals.ReleaseStaleClaims(root, 72*time.Hour, false)
		case "gitignore-claims":
			_, _ = goals.EnsureGitIgnoreClaim(root)
		case "cfclean":
			_, _ = EnsureCFGitFilter(root)
		}
	}
	return BuildPlan(nil, root)
}
