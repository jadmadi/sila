package goals

import (
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
)

var (
	cgoViolationRE = regexp.MustCompile(`(?i)\b(enable[_\s-]?cgo|requires?[_\s-]?cgo|with\s+cgo|using\s+cgo|cgo_enabled\s*=\s*1)\b`)
	cgoNegationRE  = regexp.MustCompile(`(?i)\b(no[_\s-]?cgo|zero[_\s-]?cgo|cgo_enabled\s*=\s*0|without\s+cgo|cgo-free|free\s+of\s+cgo|cgo\s+prohibited)\b`)

	copyleftViolationRE = regexp.MustCompile(`(?i)\b(adopt|use|import|license\s+under|licensed\s+as|depend\s+on)\s+(?:the\s+)?(gpl|agpl|sspl)\b`)
	copyleftNegationRE  = regexp.MustCompile(`(?i)\b(ban|prohibit|prevent|avoid|disallow|unauthorized|no\s+(?:gpl|agpl)|against\s+(?:gpl|agpl)|relicensing)\b`)

	daemonViolationRE = regexp.MustCompile(`(?i)\b(run\s+as|deploy\s+as|spawn|create|start)\s+(?:a\s+)?(background\s+daemon|daemon\s+process|systemd\s+service)\b`)
	daemonNegationRE  = regexp.MustCompile(`(?i)\b(no\s+daemon|reject|avoid|ban|prohibit|zero\s+daemon)\b`)
)

// FileTouchCollision describes a concurrency conflict between declared files to touch.
type FileTouchCollision struct {
	File            string `json:"file"`
	ConflictingGoal string `json:"conflicting_goal"`
	GoalStatus      string `json:"goal_status"` // "claimed", "pending"
	Agent           string `json:"agent,omitempty"`
}

// PreFlightReport captures the findings of the three-axis goal creation pre-flight sentry.
type PreFlightReport struct {
	Valid              bool                 `json:"valid"`
	Errors             []string             `json:"errors,omitempty"`
	Warnings           []string             `json:"warnings,omitempty"`
	Collisions         []FileTouchCollision `json:"collisions,omitempty"`
	SuggestedSequence  int                  `json:"suggested_sequence,omitempty"`
	SuggestedDependsOn []string             `json:"suggested_depends_on,omitempty"`
}

// NormalizeShape normalizes task shape to "ship" or "scout".
func NormalizeShape(shape string) string {
	low := strings.ToLower(strings.TrimSpace(shape))
	if low == "scout" || low == "investigate" || low == "research" || low == "audit" {
		return "scout"
	}
	return "ship"
}

// PreFlightGoalCheck audits proposed goal parameters against existing goals before scaffolding.
// It detects file touch concurrency collisions, semantic contradictions, and calculates topological ordering.
func PreFlightGoalCheck(rootDir string, params CreateGoalParams) (*PreFlightReport, error) {
	rootDir = resolveGoalsRootDir(rootDir)
	if rootDir == "" && params.RootDir != "" {
		rootDir = resolveGoalsRootDir(params.RootDir)
	}

	report := &PreFlightReport{
		Valid: true,
	}

	if params.Slug == "" {
		report.Errors = append(report.Errors, "goal slug is required")
		report.Valid = false
		return report, nil
	}
	slug := cleanSlug(params.Slug)
	if slug == "" {
		report.Errors = append(report.Errors, fmt.Sprintf("invalid goal slug %q: must contain alphanumeric characters and hyphens", params.Slug))
		report.Valid = false
		return report, nil
	}

	// 1. Slug existence check
	goalDir := filepath.Join(rootDir, "goals", slug)
	if !params.Overwrite && fileExists(filepath.Join(goalDir, "goal.md")) {
		report.Errors = append(report.Errors, fmt.Sprintf("goal %q already exists in goals/%s", slug, slug))
	}

	// Scan existing repository goals
	audit, err := ScanGoals(rootDir)
	if err != nil && !strings.Contains(err.Error(), "no goals directory") {
		// Non-fatal, but record if scan failed
		report.Warnings = append(report.Warnings, fmt.Sprintf("failed to scan existing goals: %v", err))
	}

	existingMap := make(map[string]*Goal)
	maxExistingSeq := 0
	if audit != nil {
		for i := range audit.Goals {
			g := &audit.Goals[i]
			existingMap[g.Slug] = g
			if g.Sequence > maxExistingSeq {
				maxExistingSeq = g.Sequence
			}
		}
	}

	// 2. Dependency & Cycle checks
	candidateDeps := make([]string, 0, len(params.DependsOn))
	for _, dep := range params.DependsOn {
		dep = strings.TrimSpace(dep)
		if dep == "" {
			continue
		}
		if dep == slug {
			report.Errors = append(report.Errors, fmt.Sprintf("goal %q cannot depend on itself", slug))
			continue
		}
		candidateDeps = append(candidateDeps, dep)
		if _, exists := existingMap[dep]; !exists {
			report.Warnings = append(report.Warnings, fmt.Sprintf("declared prerequisite %q does not exist in goals/", dep))
		}
	}

	if len(candidateDeps) > 0 && audit != nil && len(audit.Goals) > 0 {
		graph := BuildDependencyGraphFromSlice(audit.Goals)
		if cycleErr := graph.ValidateNoCycle(slug, candidateDeps); cycleErr != nil {
			report.Errors = append(report.Errors, cycleErr.Error())
		}
	}

	// 3. File touch collision check (Axis 1)
	shape := NormalizeShape(params.Shape)
	var candidateFiles []string
	for _, f := range params.FilesToTouch {
		if c := NormalizeGoalPath(f); c != "" {
			candidateFiles = append(candidateFiles, c)
		}
	}

	suggestedDepsMap := make(map[string]bool)
	for _, d := range candidateDeps {
		suggestedDepsMap[d] = true
	}

	if shape != "scout" && len(candidateFiles) > 0 && audit != nil {
		for _, otherGoal := range audit.Goals {
			if otherGoal.Slug == slug {
				continue
			}
			if otherGoal.Shape == "scout" {
				continue
			}
			// Only pending or claimed goals have pending/in-flight modifications
			if otherGoal.Status == "implemented" || otherGoal.Status == "superseded" {
				continue
			}
			// If already dependent on otherGoal, execution is topologically serialized
			if suggestedDepsMap[otherGoal.Slug] {
				continue
			}
			// If otherGoal depends on candidate, it is also serialized
			depOnCandidate := slices.Contains(otherGoal.DependsOn, slug)
			if depOnCandidate {
				continue
			}

			// Gather files from otherGoal
			var otherFiles []string
			if otherGoal.Claim != nil && !otherGoal.Claim.Stale && otherGoal.Status == "claimed" && len(otherGoal.Claim.FilesToTouch) > 0 {
				otherFiles = otherGoal.Claim.FilesToTouch
			} else {
				otherFiles = otherGoal.FilesToTouch
			}

			overlaps := OverlappingPaths(candidateFiles, otherFiles)
			if len(overlaps) > 0 {
				agentName := ""
				agentSuffix := ""
				if otherGoal.Claim != nil {
					agentName = otherGoal.Claim.Agent
					if agentName == "" {
						agentName = otherGoal.Claim.ClaimedBy
					}
				}
				if agentName != "" {
					agentSuffix = fmt.Sprintf(" (claimed by @%s)", agentName)
				}

				for _, ov := range overlaps {
					report.Collisions = append(report.Collisions, FileTouchCollision{
						File:            ov,
						ConflictingGoal: otherGoal.Slug,
						GoalStatus:      otherGoal.Status,
						Agent:           agentName,
					})
				}

				report.Warnings = append(report.Warnings, fmt.Sprintf(
					"concurrency warning: file touch collision on %v with %s goal %q%s; consider adding %q to depends_on",
					overlaps, otherGoal.Status, otherGoal.Slug, agentSuffix, otherGoal.Slug,
				))

				if !suggestedDepsMap[otherGoal.Slug] {
					suggestedDepsMap[otherGoal.Slug] = true
					report.SuggestedDependsOn = append(report.SuggestedDependsOn, otherGoal.Slug)
				}
			}
		}
	}

	// 4. Semantic / Invariant Contradiction Check (Axis 2)
	titleTrimmed := strings.TrimSpace(params.Title)
	if titleTrimmed != "" && audit != nil {
		lowerTitle := strings.ToLower(titleTrimmed)
		for _, otherGoal := range audit.Goals {
			if otherGoal.Slug == slug {
				continue
			}
			if strings.ToLower(strings.TrimSpace(otherGoal.Title)) == lowerTitle {
				report.Warnings = append(report.Warnings, fmt.Sprintf(
					"semantic warning: goal title %q is identical to existing goal %q",
					params.Title, otherGoal.Slug,
				))
			}
		}
	}

	// Audit invariants and description against core repository invariants
	invariantsToCheck := append([]string(nil), params.Invariants...)
	if params.Description != "" {
		invariantsToCheck = append(invariantsToCheck, params.Description)
	}

	for _, text := range invariantsToCheck {
		if cgoViolationRE.MatchString(text) && !cgoNegationRE.MatchString(text) {
			report.Warnings = append(report.Warnings, "invariant warning: Sila architecture requires pure Go with zero CGO (CGO_ENABLED=0)")
		}
		if copyleftViolationRE.MatchString(text) && !copyleftNegationRE.MatchString(text) {
			report.Warnings = append(report.Warnings, "invariant warning: repository policy strictly prohibits unauthorized third-party copyleft dependencies (GPL/AGPL/SSPL)")
		}
		if daemonViolationRE.MatchString(text) && !daemonNegationRE.MatchString(text) {
			report.Warnings = append(report.Warnings, "invariant warning: Sila rejects background daemons in favor of lightweight ephemeral execution")
		}
	}

	// 5. Topological Order Suggestion (Axis 3)
	sort.Strings(report.SuggestedDependsOn)

	if params.Sequence > 0 {
		report.SuggestedSequence = params.Sequence
	} else {
		maxDepSeq := 0
		allDepRefs := append([]string(nil), candidateDeps...)
		allDepRefs = append(allDepRefs, report.SuggestedDependsOn...)
		for _, dep := range allDepRefs {
			if g, ok := existingMap[dep]; ok && g.Sequence > maxDepSeq {
				maxDepSeq = g.Sequence
			}
		}
		if maxDepSeq > 0 {
			report.SuggestedSequence = maxDepSeq + 1
		} else {
			report.SuggestedSequence = maxExistingSeq + 1
		}
	}

	// 6. Validity & Strict Mode Evaluation
	if len(report.Errors) > 0 {
		report.Valid = false
	}
	if params.Strict && (len(report.Warnings) > 0 || len(report.Collisions) > 0) {
		report.Valid = false
	}

	return report, nil
}
