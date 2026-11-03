package goals

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
)

// CheckWorkPackageReady validates that an opted-in goal package has complete task contracts
// and a valid, current planner spec review before implementation begins.
// If the goal does not contain a work-package.json (legacy mode), it returns nil, nil for full compatibility.
// If work-package.json is present, it returns all discovered Diagnostics and an error if any diagnostic is found.
func CheckWorkPackageReady(rootDir, slug string) ([]Diagnostic, error) {
	resolvedRoot := resolveGoalsRootDir(rootDir)
	goalDir := filepath.Join(resolvedRoot, "goals", slug)
	wpPath := filepath.Join(goalDir, "work-package.json")

	// Legacy goals without work-package.json are compatible
	if !fileExists(wpPath) {
		return nil, nil
	}

	// 1. Load work package
	pkg, err := LoadWorkPackage(resolvedRoot, slug)
	if err != nil {
		diag := Diagnostic{
			Code:        DiagSpecNotReady,
			Severity:    "error",
			GoalSlug:    slug,
			Path:        "work-package.json",
			Message:     fmt.Sprintf("corrupt work-package.json: %v", err),
			Remediation: "resolve JSON syntax errors and ensure schema compliance",
		}
		return []Diagnostic{diag}, fmt.Errorf("SPEC_NOT_READY: corrupt work-package.json: %w", err)
	}

	// 2. Validate structural task readiness
	diags := ValidateTaskReadiness(resolvedRoot, pkg)

	// 3. Check for and validate spec-review.json
	reviewPath := filepath.Join(goalDir, "spec-review.json")
	if !fileExists(reviewPath) {
		diags = append(diags, Diagnostic{
			Code:        DiagSpecReviewMissing,
			Severity:    "error",
			GoalSlug:    slug,
			Path:        "spec-review.json",
			Message:     "spec-review.json is missing for structured work package",
			Remediation: "record and save planner spec review before claiming goal",
		})
	} else {
		review, err := LoadSpecReview(resolvedRoot, slug)
		if err != nil {
			diags = append(diags, Diagnostic{
				Code:        DiagSpecNotReady,
				Severity:    "error",
				GoalSlug:    slug,
				Path:        "spec-review.json",
				Message:     fmt.Sprintf("corrupt spec-review.json: %v", err),
				Remediation: "re-save valid spec-review.json",
			})
		} else {
			reviewDiags := ValidateSpecReview(pkg, *review)
			diags = append(diags, reviewDiags...)
		}
	}

	// 4. Validate dependency conditions if present
	if len(pkg.DependencyConditions) > 0 {
		var activeConditions []DependencyCondition
		var plainDeps []string
		hasGoalMd := fileExists(filepath.Join(goalDir, "goal.md"))
		if hasGoalMd {
			if data, err := os.ReadFile(filepath.Join(goalDir, "goal.md")); err == nil {
				_, _, _, deps, _, _, _ := parseGoalMetadata(string(data))
				plainDeps = deps
			}
		}
		if len(plainDeps) > 0 {
			for _, dc := range pkg.DependencyConditions {
				if slices.Contains(plainDeps, dc.GoalSlug) {
					activeConditions = append(activeConditions, dc)
				}
			}
		} else if !hasGoalMd {
			activeConditions = pkg.DependencyConditions
		}
		if len(activeConditions) > 0 {
			condDiags := EvaluateDependencyConditions(resolvedRoot, activeConditions)
			diags = append(diags, condDiags...)
		}
	}

	if len(diags) > 0 {
		return diags, fmt.Errorf("SPEC_NOT_READY: goal %q has %d unresolved readiness diagnostic(s)", slug, len(diags))
	}

	return nil, nil
}
