package goals

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// GoalDependencyBlockers evaluates all dependency blockers (both plain metadata dependencies
// and typed DependencyConditions) for a given goal.
func GoalDependencyBlockers(rootDir, slug string) ([]Diagnostic, error) {
	if strings.TrimSpace(slug) == "" {
		return nil, fmt.Errorf("goal slug is required")
	}
	slug = filepath.Clean(slug)
	rootDir = resolveGoalsRootDir(rootDir)
	goalDir := filepath.Join(rootDir, "goals", slug)

	if !fileExists(filepath.Join(goalDir, "goal.md")) && !fileExists(filepath.Join(goalDir, "work-package.json")) {
		return nil, fmt.Errorf("goal %q not found in %s", slug, filepath.Join(rootDir, "goals"))
	}

	var diags []Diagnostic

	// 1. Structured work package dependency conditions
	wpPath := filepath.Join(goalDir, "work-package.json")
	if fileExists(wpPath) {
		wp, err := LoadWorkPackage(rootDir, slug)
		if err != nil {
			return []Diagnostic{
				{
					Code:        DiagSpecNotReady,
					Severity:    "error",
					GoalSlug:    slug,
					Path:        "work-package.json",
					Message:     fmt.Sprintf("corrupt work-package.json: %v", err),
					Remediation: "Fix syntax or schema errors in work-package.json",
				},
			}, nil
		}

		// Parse plain depends_on in goal.md metadata if present
		var plainDeps []string
		hasGoalMd := fileExists(filepath.Join(goalDir, "goal.md"))
		if hasGoalMd {
			if data, err := os.ReadFile(filepath.Join(goalDir, "goal.md")); err == nil {
				_, _, _, deps, _, _, _ := parseGoalMetadata(string(data))
				plainDeps = deps
			}
		}

		var activeConditions []DependencyCondition
		if len(plainDeps) > 0 {
			for _, dc := range wp.DependencyConditions {
				if slices.Contains(plainDeps, dc.GoalSlug) {
					activeConditions = append(activeConditions, dc)
				}
			}
		} else if !hasGoalMd {
			activeConditions = wp.DependencyConditions
		}

		if len(activeConditions) > 0 {
			condDiags := EvaluateDependencyConditions(rootDir, activeConditions)
			diags = append(diags, condDiags...)
		}

		for _, dep := range plainDeps {
			depSlug := strings.TrimSpace(dep)
			if depSlug == "" {
				continue
			}
			// If already covered by a condition on depSlug, skip
			alreadyCovered := false
			for _, dc := range activeConditions {
				if dc.GoalSlug == depSlug {
					alreadyCovered = true
					break
				}
			}
			if !alreadyCovered {
				pDiags := evaluatePlainDependency(rootDir, slug, depSlug)
				diags = append(diags, pDiags...)
			}
		}
	} else {
		// 2. Legacy goal: evaluate plain depends_on from goal.md
		if data, err := os.ReadFile(filepath.Join(goalDir, "goal.md")); err == nil {
			_, _, _, deps, _, _, _ := parseGoalMetadata(string(data))
			for _, dep := range deps {
				depSlug := strings.TrimSpace(dep)
				if depSlug == "" {
					continue
				}
				pDiags := evaluatePlainDependency(rootDir, slug, depSlug)
				diags = append(diags, pDiags...)
			}
		}
	}

	// Sort diagnostics stably by GoalSlug, Code, Path, Message
	sort.Slice(diags, func(i, j int) bool {
		if diags[i].GoalSlug != diags[j].GoalSlug {
			return diags[i].GoalSlug < diags[j].GoalSlug
		}
		if diags[i].Code != diags[j].Code {
			return diags[i].Code < diags[j].Code
		}
		if diags[i].Path != diags[j].Path {
			return diags[i].Path < diags[j].Path
		}
		return diags[i].Message < diags[j].Message
	})

	return diags, nil
}

func evaluatePlainDependency(rootDir, targetSlug, depSlug string) []Diagnostic {
	depDir := filepath.Join(rootDir, "goals", depSlug)
	if !fileExists(filepath.Join(depDir, "goal.md")) && !fileExists(filepath.Join(depDir, "work-package.json")) {
		return []Diagnostic{
			{
				Code:        DiagMissingPrerequisite,
				Severity:    "error",
				GoalSlug:    depSlug,
				Path:        filepath.Join("goals", depSlug),
				Message:     fmt.Sprintf("prerequisite goal %q does not exist", depSlug),
				Remediation: fmt.Sprintf("Register predecessor goal %q before depending on it", depSlug),
			},
		}
	}

	// Check if predecessor is reopened
	if reopenRec, err := GetActiveReopen(rootDir, depSlug); err == nil && reopenRec != nil && reopenRec.Active {
		return []Diagnostic{
			{
				Code:        DiagDependencyGoalReopened,
				Severity:    "error",
				GoalSlug:    depSlug,
				Message:     fmt.Sprintf("prerequisite goal %q is currently in remediation (%s)", depSlug, reopenRec.Reason),
				Remediation: "Wait for prerequisite remediation to complete",
			},
		}
	}

	// Check if predecessor is implemented or accepted
	if fileExists(filepath.Join(depDir, "work-package.json")) {
		acc, _ := EvaluateGoalAcceptance(rootDir, depSlug)
		if acc == nil || !acc.Accepted {
			return []Diagnostic{
				{
					Code:        DiagMissingPrerequisite,
					Severity:    "error",
					GoalSlug:    depSlug,
					Message:     fmt.Sprintf("prerequisite structured goal %q is not accepted", depSlug),
					Remediation: fmt.Sprintf("Complete acceptance for goal %q first", depSlug),
				},
			}
		}
		return nil
	}

	// For legacy predecessor goals: check if implemented or certified
	if cert, _ := GetActiveCertification(rootDir, depSlug); cert != nil {
		return nil
	}
	if auditRec, _ := GetActiveAuditReceipt(rootDir, depSlug); auditRec != nil && auditRec.CommitSHA != "" && VerifyAuditReceipt(auditRec) == nil {
		return nil
	}
	if retroRec, _ := GetActiveRetrospective(rootDir, depSlug); retroRec != nil && retroRec.Commit != "" {
		return nil
	}
	if sup, _ := GetActiveSupersede(rootDir, depSlug); sup != nil {
		return nil
	}
	commits, _, _, _, _ := scanGitCommits(rootDir, []string{depSlug})
	if _, ok := commits[depSlug]; ok {
		hasUnchecked := goalHasUncheckedTasks(rootDir, depSlug)
		if !hasUnchecked {
			return nil
		}
	}

	return []Diagnostic{
		{
			Code:        DiagMissingPrerequisite,
			Severity:    "error",
			GoalSlug:    depSlug,
			Message:     fmt.Sprintf("prerequisite goal %q is not yet implemented or certified", depSlug),
			Remediation: fmt.Sprintf("Implement and certify goal %q first", depSlug),
		},
	}
}

// CheckGoalBlockersForScanner returns blockers mapped to unmet dependency slugs and reasons
// for use within ScanGoals.
func CheckGoalBlockersForScanner(rootDir, slug string) ([]string, []string) {
	blockers, err := GoalDependencyBlockers(rootDir, slug)
	if err != nil || len(blockers) == 0 {
		return nil, nil
	}

	var unmetSlugs []string
	var reasons []string

	for _, b := range blockers {
		if b.GoalSlug != "" && b.GoalSlug != slug && !slices.Contains(unmetSlugs, b.GoalSlug) {
			unmetSlugs = append(unmetSlugs, b.GoalSlug)
		}
		reasons = append(reasons, fmt.Sprintf("%s: %s", b.Code, b.Message))
	}

	return unmetSlugs, reasons
}
