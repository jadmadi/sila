package goals

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/render"
)

// DependencyBlocker represents a machine-readable structured blocker on a goal.
type DependencyBlocker struct {
	Code       string `json:"code"`
	GoalSlug   string `json:"goal_slug"`
	Required   any    `json:"required,omitempty"`
	Observed   any    `json:"observed,omitempty"`
	SourceGoal string `json:"source_goal,omitempty"`
	Artifact   string `json:"artifact,omitempty"`
	Pointer    string `json:"pointer,omitempty"`
	Message    string `json:"message,omitempty"`
}

// LinkedReport represents an evaluation report linked to a goal package.
type LinkedReport struct {
	ID           string    `json:"id"`
	PersonaID    string    `json:"persona_id"`
	PersonaNumID int       `json:"persona_num_id,omitempty"`
	PersonaName  string    `json:"persona_name,omitempty"`
	Status       string    `json:"status"`
	Summary      string    `json:"summary,omitempty"`
	UpdatedAt    time.Time `json:"updated_at,omitempty"`
}

// GoalView encapsulates the complete epistemic detail of a goal package for terminal inspection and machine serialization.
type GoalView struct {
	Slug               string               `json:"slug"`
	Title              string               `json:"title"`
	Description        string               `json:"description"`
	Status             string               `json:"status"` // "implemented", "claimed", "pending", "draft", "superseded"
	Tier               string               `json:"tier"`   // "immediate", "roadmap", "nice-to-have", "wont-fix"
	Shape              string               `json:"shape"`  // "ship", "scout"
	Independent        bool                 `json:"independent"`
	Sequence           int                  `json:"sequence,omitempty"`
	DependsOn          []string             `json:"depends_on,omitempty"`
	DependencyWarnings []string             `json:"dependency_warnings,omitempty"`
	UnmetDeps          []string             `json:"unmet_dependencies,omitempty"`
	Blocked            bool                 `json:"blocked"`
	BlockedReason      string               `json:"blocked_reason,omitempty"`
	RootBlockers       []string             `json:"root_blockers,omitempty"`
	Blockers           []DependencyBlocker  `json:"blockers,omitempty"`
	DownstreamCount    int                  `json:"downstream_count,omitempty"`
	FilesToTouch       []string             `json:"files_to_touch,omitempty"`
	FileConflicts      []FileConflict       `json:"file_conflicts,omitempty"`
	Commit             string               `json:"commit,omitempty"`
	Task               string               `json:"task,omitempty"`
	TasksTotal         int                  `json:"tasks_total"`
	TasksMet           int                  `json:"tasks_met"`
	AcceptanceStatus   string               `json:"acceptance_status,omitempty"` // not_run | incomplete | failed | stale | accepted
	AcceptedTasks      int                  `json:"accepted_tasks,omitempty"`
	RequiredCases      int                  `json:"required_cases,omitempty"`
	PassedCases        int                  `json:"passed_cases,omitempty"`
	Tasks              []GoalTask           `json:"tasks,omitempty"`
	Invariants         []string             `json:"invariants,omitempty"`
	DoneConditions     []string             `json:"done_conditions,omitempty"`
	LaunchCmd          string               `json:"launch_cmd"`
	Claim              *GoalClaim           `json:"claim,omitempty"`
	Reopen             *ReopenRecord        `json:"reopen,omitempty"`
	Supersede          *SupersededRecord    `json:"supersede,omitempty"`
	Certification      *CertificationRecord `json:"certification,omitempty"`
	WorkPackageReady   *bool                `json:"work_package_ready,omitempty"`
	SpecDigest         string               `json:"spec_digest,omitempty"`
	Diagnostics        []Diagnostic         `json:"diagnostics,omitempty"`
	Personas           []string             `json:"personas,omitempty"`
	Reports            []LinkedReport       `json:"reports,omitempty"`
	HasGoalMd          bool                 `json:"has_goal_md"`
	HasPlanMd          bool                 `json:"has_plan_md"`
	HasFactsMd         bool                 `json:"has_facts_md"`
	GoalMdPath         string               `json:"goal_md_path,omitempty"`
	PlanMdPath         string               `json:"plan_md_path,omitempty"`
	FactsMdPath        string               `json:"facts_md_path,omitempty"`
}

// ResolveGoalByIdentifier resolves a goal from an AuditResult by 1-based roadmap numerical index {N}
// or by slug (exact match, case-insensitive match, or unambiguous prefix match).
func ResolveGoalByIdentifier(res *AuditResult, ident string) (*Goal, error) {
	if res == nil || len(res.Goals) == 0 {
		return nil, fmt.Errorf("goal not found: no goals in roadmap")
	}

	ident = strings.TrimSpace(ident)
	if ident == "" {
		return nil, fmt.Errorf("goal identifier is required (specify slug or 1-based index)")
	}

	// Strip common prefixes/suffixes if pasted directly
	ident = strings.TrimPrefix(ident, "goals/")
	ident = strings.TrimSuffix(ident, "/goal.md")
	ident = strings.TrimSuffix(ident, "/plan.md")
	ident = strings.TrimSuffix(ident, "/facts.md")
	ident = strings.TrimSpace(ident)

	// Numerical index {N} resolution
	if n, err := strconv.Atoi(ident); err == nil {
		if n <= 0 {
			return nil, fmt.Errorf("invalid goal index %d: index must be a positive 1-based number", n)
		}

		// 1. First priority: Active roadmap goals (ready, claimed, blocked, contested, draft).
		// In res.Goals, ScanGoals already sorts ready pending first, then claimed, then blocked pending.
		var activeRoadmap []Goal
		for _, g := range res.Goals {
			if g.Status != "implemented" && g.Status != "superseded" {
				activeRoadmap = append(activeRoadmap, g)
			}
		}

		if len(activeRoadmap) > 0 {
			if n <= len(activeRoadmap) {
				target := activeRoadmap[n-1]
				return &target, nil
			}
			// Check if any goal explicitly has Sequence == n
			for _, g := range res.Goals {
				if g.Sequence == n {
					target := g
					return &target, nil
				}
			}
			return nil, fmt.Errorf("goal index %d out of bounds (active roadmap has %d ready/pending goals)", n, len(activeRoadmap))
		}

		// 2. If no active goals, resolve against Sequence or full goals list
		for _, g := range res.Goals {
			if g.Sequence == n {
				target := g
				return &target, nil
			}
		}
		if n <= len(res.Goals) {
			target := res.Goals[n-1]
			return &target, nil
		}
		return nil, fmt.Errorf("goal index %d out of bounds (roadmap has %d goals)", n, len(res.Goals))
	}

	// Slug resolution
	// 1. Exact match
	for _, g := range res.Goals {
		if g.Slug == ident {
			target := g
			return &target, nil
		}
	}

	// 2. Case-insensitive exact match
	for _, g := range res.Goals {
		if strings.EqualFold(g.Slug, ident) {
			target := g
			return &target, nil
		}
	}

	// 3. Unique prefix match
	lowerIdent := strings.ToLower(ident)
	var matches []Goal
	for _, g := range res.Goals {
		if strings.HasPrefix(strings.ToLower(g.Slug), lowerIdent) {
			matches = append(matches, g)
		}
	}

	if len(matches) == 1 {
		target := matches[0]
		return &target, nil
	}
	if len(matches) > 1 {
		var matchSlugs []string
		for _, m := range matches {
			matchSlugs = append(matchSlugs, m.Slug)
		}
		return nil, fmt.Errorf("ambiguous goal prefix %q matches %d goals: %s", ident, len(matches), strings.Join(matchSlugs, ", "))
	}

	return nil, fmt.Errorf("goal %q not found in roadmap", ident)
}

// BuildGoalView constructs a full GoalView by combining goal metadata with parsed facts, plan tasks, and done conditions.
func BuildGoalView(rootDir string, g *Goal) (*GoalView, error) {
	if g == nil {
		return nil, fmt.Errorf("goal cannot be nil")
	}
	rootDir = resolveGoalsRootDir(rootDir)
	goalDir := filepath.Join(rootDir, "goals", g.Slug)

	goalPath, hasGoal := findGoalFile(goalDir, "goal.md")
	planPath, hasPlan := findGoalFile(goalDir, "plan.md")
	factsPath, hasFacts := findGoalFile(goalDir, "facts.md")

	view := &GoalView{
		Slug:               g.Slug,
		Title:              g.Title,
		Description:        g.Description,
		Status:             g.Status,
		Tier:               g.Tier,
		Shape:              g.Shape,
		Independent:        g.Independent,
		Sequence:           g.Sequence,
		DependsOn:          g.DependsOn,
		DependencyWarnings: g.DependencyWarnings,
		UnmetDeps:          g.UnmetDeps,
		Blocked:            g.Blocked,
		BlockedReason:      g.BlockedReason,
		RootBlockers:       g.RootBlockers,
		DownstreamCount:    g.DownstreamCount,
		FilesToTouch:       g.FilesToTouch,
		FileConflicts:      g.FileConflicts,
		Commit:             g.Commit,
		Task:               g.Task,
		TasksTotal:         g.TasksTotal,
		TasksMet:           g.TasksMet,
		LaunchCmd:          g.LaunchCmd,
		Claim:              g.Claim,
		Reopen:             g.Reopen,
		Supersede:          g.Supersede,
		Certification:      g.Certification,
		HasGoalMd:          hasGoal,
		HasPlanMd:          hasPlan,
		HasFactsMd:         hasFacts,
		GoalMdPath:         goalPath,
		PlanMdPath:         planPath,
		FactsMdPath:        factsPath,
	}

	hasWP := fileExists(filepath.Join(goalDir, "work-package.json"))
	if hasPlan || hasWP {
		tasks, err := GetGoalTasks(rootDir, g.Slug)
		if err != nil {
			return nil, fmt.Errorf("read goal tasks: %w", err)
		}
		view.Tasks = tasks
		if len(tasks) > 0 {
			met := 0
			for _, t := range tasks {
				if t.Completed {
					met++
				}
			}
			view.TasksTotal = len(tasks)
			view.TasksMet = met
		}
	}

	view.AcceptanceStatus = g.AcceptanceStatus
	view.AcceptedTasks = g.AcceptedTasks
	view.RequiredCases = g.RequiredCases
	view.PassedCases = g.PassedCases

	if hasWP {
		if view.AcceptanceStatus == "" {
			if accRes, err := EvaluateGoalAcceptance(rootDir, g.Slug); err == nil && accRes != nil {
				view.AcceptanceStatus = string(accRes.EvidenceStatus)
				view.AcceptedTasks = g.TasksMet
				if wp, err := LoadWorkPackage(rootDir, g.Slug); err == nil && wp != nil {
					reqCases := 0
					for _, ac := range wp.Acceptance {
						if ac.Mandatory {
							reqCases++
						}
					}
					view.RequiredCases = reqCases
					view.PassedCases = reqCases - len(accRes.MissingCaseIDs) - len(accRes.FailedCaseIDs)
					if view.PassedCases < 0 {
						view.PassedCases = 0
					}
				}
			}
		}
		diags, _ := CheckWorkPackageReady(rootDir, g.Slug)
		isReady := len(diags) == 0
		view.WorkPackageReady = &isReady
		view.Diagnostics = diags
		if wp, err := LoadWorkPackage(rootDir, g.Slug); err == nil && wp != nil {
			if dig, err := WorkPackageDigest(wp); err == nil {
				view.SpecDigest = dig
			}
		}
	}

	if hasFacts {
		if data, err := os.ReadFile(factsPath); err == nil {
			view.Invariants = parseInvariantsFromFacts(string(data))
		}
	}

	if hasGoal {
		if data, err := os.ReadFile(goalPath); err == nil {
			view.DoneConditions = parseDoneConditionsFromGoal(string(data))
			if view.Description == "" {
				_, desc, _, _, _, _, _, _ := parseGoalMetadataWithTier(string(data))
				view.Description = desc
			}
		}
	}

	view.Personas = g.Personas
	if len(view.Personas) == 0 && hasGoal {
		if data, err := os.ReadFile(goalPath); err == nil {
			view.Personas = ParseGoalPersonas(string(data))
		}
	}

	// Scan reports from .sila/reports/
	reportsDir := filepath.Join(rootDir, ".sila", "reports")
	if entries, err := os.ReadDir(reportsDir); err == nil {
		normSlug := strings.TrimPrefix(g.Slug, "goals/")
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(reportsDir, e.Name()))
			if err != nil {
				continue
			}
			var rep struct {
				ID             string    `json:"id"`
				AssociatedGoal string    `json:"associated_goal"`
				PersonaID      string    `json:"persona_id"`
				PersonaNumID   int       `json:"persona_num_id"`
				PersonaName    string    `json:"persona_name"`
				Status         string    `json:"status"`
				Summary        string    `json:"summary"`
				UpdatedAt      time.Time `json:"updated_at"`
			}
			if err := json.Unmarshal(data, &rep); err != nil {
				continue
			}
			assoc := strings.TrimPrefix(rep.AssociatedGoal, "goals/")
			if strings.EqualFold(assoc, normSlug) {
				view.Reports = append(view.Reports, LinkedReport{
					ID:           rep.ID,
					PersonaID:    rep.PersonaID,
					PersonaNumID: rep.PersonaNumID,
					PersonaName:  rep.PersonaName,
					Status:       rep.Status,
					Summary:      rep.Summary,
					UpdatedAt:    rep.UpdatedAt,
				})
			}
		}
	}

	view.Blockers = ResolveGoalBlockers(rootDir, g.Slug)
	if len(view.Blockers) > 0 {
		view.Blocked = true
	}

	return view, nil
}

// ResolveGoalBlockers inspects a goal package and returns structured machine-readable dependency blockers.
func ResolveGoalBlockers(rootDir, slug string) []DependencyBlocker {
	if strings.TrimSpace(slug) == "" {
		return nil
	}
	slug = filepath.Clean(slug)
	rootDir = resolveGoalsRootDir(rootDir)
	goalDir := filepath.Join(rootDir, "goals", slug)

	var blockers []DependencyBlocker

	wpPath := filepath.Join(goalDir, "work-package.json")
	if fileExists(wpPath) {
		wp, err := LoadWorkPackage(rootDir, slug)
		if err != nil {
			return []DependencyBlocker{
				{
					Code:     DiagSpecNotReady,
					GoalSlug: slug,
					Message:  fmt.Sprintf("corrupt work-package.json: %v", err),
				},
			}
		}

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

		for _, dc := range activeConditions {
			if b := evaluateSingleConditionBlocker(rootDir, slug, dc); b != nil {
				blockers = append(blockers, *b)
			}
		}

		for _, dep := range plainDeps {
			depSlug := strings.TrimSpace(dep)
			if depSlug == "" {
				continue
			}
			covered := false
			for _, dc := range activeConditions {
				if dc.GoalSlug == depSlug {
					covered = true
					break
				}
			}
			if !covered {
				if pDiags := evaluatePlainDependency(rootDir, slug, depSlug); len(pDiags) > 0 {
					for _, pd := range pDiags {
						blockers = append(blockers, DependencyBlocker{
							Code:       pd.Code,
							GoalSlug:   slug,
							SourceGoal: depSlug,
							Required:   "implemented",
							Observed:   "unmet",
							Message:    pd.Message,
						})
					}
				}
			}
		}
	} else {
		if data, err := os.ReadFile(filepath.Join(goalDir, "goal.md")); err == nil {
			_, _, _, deps, _, _, _ := parseGoalMetadata(string(data))
			for _, dep := range deps {
				depSlug := strings.TrimSpace(dep)
				if depSlug == "" {
					continue
				}
				if pDiags := evaluatePlainDependency(rootDir, slug, depSlug); len(pDiags) > 0 {
					for _, pd := range pDiags {
						blockers = append(blockers, DependencyBlocker{
							Code:       pd.Code,
							GoalSlug:   slug,
							SourceGoal: depSlug,
							Required:   "implemented",
							Observed:   "unmet",
							Message:    pd.Message,
						})
					}
				}
			}
		}
	}

	return blockers
}

func evaluateSingleConditionBlocker(rootDir, slug string, dc DependencyCondition) *DependencyBlocker {
	depDir := filepath.Join(rootDir, "goals", dc.GoalSlug)
	if !fileExists(filepath.Join(depDir, "goal.md")) && !fileExists(filepath.Join(depDir, "work-package.json")) {
		return &DependencyBlocker{
			Code:       DiagDependencyGoalNotFound,
			GoalSlug:   slug,
			SourceGoal: dc.GoalSlug,
			Message:    fmt.Sprintf("predecessor goal %q does not exist", dc.GoalSlug),
		}
	}

	if reopenRec, err := GetActiveReopen(rootDir, dc.GoalSlug); err == nil && reopenRec != nil && reopenRec.Active {
		return &DependencyBlocker{
			Code:       DiagDependencyGoalReopened,
			GoalSlug:   slug,
			SourceGoal: dc.GoalSlug,
			Observed:   "reopened",
			Message:    fmt.Sprintf("predecessor goal %q is currently reopened for remediation (%s)", dc.GoalSlug, reopenRec.Reason),
		}
	}

	if supRec, err := GetActiveSupersede(rootDir, dc.GoalSlug); err == nil && supRec != nil {
		return &DependencyBlocker{
			Code:       DiagDependencyGoalSuperseded,
			GoalSlug:   slug,
			SourceGoal: dc.GoalSlug,
			Observed:   "superseded",
			Message:    fmt.Sprintf("predecessor goal %q has been superseded by %s", dc.GoalSlug, supRec.SupersededBy),
		}
	}

	switch dc.Kind {
	case DepConditionAcceptedGoal:
		eval, err := EvaluateGoalAcceptance(rootDir, dc.GoalSlug)
		if err != nil || eval == nil || !eval.Accepted || eval.EvidenceStatus != EvidenceStatusAccepted {
			statusStr := "unaccepted"
			if eval != nil {
				statusStr = string(eval.EvidenceStatus)
			}
			return &DependencyBlocker{
				Code:       DiagDependencyGoalNotAccepted,
				GoalSlug:   slug,
				SourceGoal: dc.GoalSlug,
				Required:   "accepted",
				Observed:   statusStr,
				Message:    fmt.Sprintf("predecessor goal %q is not accepted (status: %s)", dc.GoalSlug, statusStr),
			}
		}

	case DepConditionAcceptedTask:
		taskAcc, err := LoadTaskAcceptance(rootDir, dc.GoalSlug, dc.TaskID)
		if err != nil || taskAcc == nil || !taskAcc.Accepted || taskAcc.EvidenceStatus != EvidenceStatusAccepted {
			statusStr := "unaccepted"
			if taskAcc != nil {
				statusStr = string(taskAcc.EvidenceStatus)
			}
			return &DependencyBlocker{
				Code:       DiagDependencyTaskNotAccepted,
				GoalSlug:   slug,
				SourceGoal: dc.GoalSlug,
				Required:   "accepted",
				Observed:   statusStr,
				Message:    fmt.Sprintf("predecessor task %s in goal %q is not accepted", dc.TaskID, dc.GoalSlug),
			}
		}

	case DepConditionArtifactDigest:
		cleanArt := filepath.Clean(filepath.ToSlash(strings.TrimSpace(dc.Artifact)))
		_, fullPath := resolvePredecessorArtifact(rootDir, dc.GoalSlug, cleanArt)
		data, err := os.ReadFile(fullPath)
		if err != nil {
			return &DependencyBlocker{
				Code:       DiagDependencyArtifactNotFound,
				GoalSlug:   slug,
				SourceGoal: dc.GoalSlug,
				Artifact:   cleanArt,
				Observed:   "missing",
				Message:    fmt.Sprintf("artifact file %q not found on disk in predecessor %q", cleanArt, dc.GoalSlug),
			}
		}
		actualSum := sha256.Sum256(data)
		actualSHA := hex.EncodeToString(actualSum[:])
		if strings.ToLower(actualSHA) != strings.ToLower(dc.SHA256) {
			return &DependencyBlocker{
				Code:       DiagDependencyArtifactMismatch,
				GoalSlug:   slug,
				SourceGoal: dc.GoalSlug,
				Artifact:   cleanArt,
				Required:   dc.SHA256,
				Observed:   actualSHA,
				Message:    fmt.Sprintf("artifact %q digest mismatch: expected %s, observed %s", cleanArt, dc.SHA256, actualSHA),
			}
		}

	case DepConditionDecisionEquals:
		eval, err := EvaluateGoalAcceptance(rootDir, dc.GoalSlug)
		if err != nil || eval == nil || !eval.Accepted || eval.EvidenceStatus != EvidenceStatusAccepted {
			statusStr := "unaccepted"
			if eval != nil {
				statusStr = string(eval.EvidenceStatus)
			}
			return &DependencyBlocker{
				Code:       DiagDependencyGoalNotAccepted,
				GoalSlug:   slug,
				SourceGoal: dc.GoalSlug,
				Required:   "accepted",
				Observed:   statusStr,
				Message:    fmt.Sprintf("predecessor goal %q must be accepted before evaluating decision (status: %s)", dc.GoalSlug, statusStr),
			}
		}

		cleanArt := filepath.Clean(filepath.ToSlash(strings.TrimSpace(dc.Artifact)))
		_, fullPath := resolvePredecessorArtifact(rootDir, dc.GoalSlug, cleanArt)
		data, err := os.ReadFile(fullPath)
		if err != nil {
			return &DependencyBlocker{
				Code:       DiagDependencyArtifactNotFound,
				GoalSlug:   slug,
				SourceGoal: dc.GoalSlug,
				Artifact:   cleanArt,
				Observed:   "missing",
				Message:    fmt.Sprintf("decision artifact %q not found on disk in predecessor %q", cleanArt, dc.GoalSlug),
			}
		}

		recordedDigest, dErr := findRecordedArtifactDigest(rootDir, dc.GoalSlug, cleanArt, eval, dc.SHA256)
		if dErr != nil {
			return &DependencyBlocker{
				Code:       DiagDependencyArtifactMismatch,
				GoalSlug:   slug,
				SourceGoal: dc.GoalSlug,
				Artifact:   cleanArt,
				Message:    fmt.Sprintf("decision artifact %q not attested in accepted evidence: %v", cleanArt, dErr),
			}
		}
		actualSum := sha256.Sum256(data)
		actualSHA := hex.EncodeToString(actualSum[:])
		if strings.ToLower(actualSHA) != strings.ToLower(recordedDigest) {
			return &DependencyBlocker{
				Code:       DiagDependencyArtifactMismatch,
				GoalSlug:   slug,
				SourceGoal: dc.GoalSlug,
				Artifact:   cleanArt,
				Required:   recordedDigest,
				Observed:   actualSHA,
				Message:    fmt.Sprintf("decision artifact %q digest mismatch: expected %s, observed %s", cleanArt, recordedDigest, actualSHA),
			}
		}

		var parsed any
		if err := json.Unmarshal(data, &parsed); err != nil {
			return &DependencyBlocker{
				Code:       DiagDependencyArtifactMalformed,
				GoalSlug:   slug,
				SourceGoal: dc.GoalSlug,
				Artifact:   cleanArt,
				Message:    fmt.Sprintf("decision artifact %q contains invalid JSON: %v", cleanArt, err),
			}
		}

		pointer := strings.TrimSpace(dc.Pointer)
		val, err := resolveJSONPointer(parsed, pointer)
		if err != nil {
			return &DependencyBlocker{
				Code:       DiagDependencyPointerNotFound,
				GoalSlug:   slug,
				SourceGoal: dc.GoalSlug,
				Artifact:   cleanArt,
				Pointer:    pointer,
				Message:    fmt.Sprintf("JSON pointer %q in artifact %q failed: %v", pointer, cleanArt, err),
			}
		}

		switch val.(type) {
		case map[string]any, []any:
			return &DependencyBlocker{
				Code:       DiagDependencyValueTypeMismatch,
				GoalSlug:   slug,
				SourceGoal: dc.GoalSlug,
				Artifact:   cleanArt,
				Pointer:    pointer,
				Message:    fmt.Sprintf("pointer %q resolved to non-scalar JSON type %T; exact scalar required", pointer, val),
			}
		}

		if !scalarsEqual(val, dc.Equals) {
			return &DependencyBlocker{
				Code:       DiagDependencyDecisionMismatch,
				GoalSlug:   slug,
				Required:   dc.Equals,
				Observed:   val,
				SourceGoal: dc.GoalSlug,
			}
		}
	}

	return nil
}

// parseInvariantsFromFacts extracts bulleted invariants from facts.md content under "## Architectural Invariants".
func parseInvariantsFromFacts(content string) []string {
	var invariants []string
	lines := strings.Split(content, "\n")
	inSection := false
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if strings.HasPrefix(trimmed, "##") {
			lower := strings.ToLower(trimmed)
			if strings.Contains(lower, "invariant") || strings.Contains(lower, "constraint") {
				inSection = true
				continue
			}
			if inSection {
				break
			}
		}
		if inSection {
			if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") || strings.HasPrefix(trimmed, "+ ") {
				item := strings.TrimSpace(trimmed[2:])
				if item != "" {
					invariants = append(invariants, item)
				}
			}
		}
	}
	return invariants
}

// parseDoneConditionsFromGoal extracts done conditions from goal.md content under "## Done Condition".
func parseDoneConditionsFromGoal(content string) []string {
	var conditions []string
	lines := strings.Split(content, "\n")
	inSection := false
	numRE := regexp.MustCompile(`^\d+\.\s*(.+)$`)
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if strings.HasPrefix(trimmed, "##") {
			lower := strings.ToLower(trimmed)
			if strings.Contains(lower, "done condition") || strings.Contains(lower, "definition of done") {
				inSection = true
				continue
			}
			if inSection {
				break
			}
		}
		if inSection {
			if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") || strings.HasPrefix(trimmed, "+ ") {
				item := strings.TrimSpace(trimmed[2:])
				if item != "" {
					conditions = append(conditions, item)
				}
			} else if m := numRE.FindStringSubmatch(trimmed); len(m) > 1 {
				conditions = append(conditions, strings.TrimSpace(m[1]))
			}
		}
	}
	return conditions
}

// renderGoalClaimStatus formats a claim status string with appropriate color badge.
func renderGoalClaimStatus(ui render.UI, status string) string {
	switch strings.ToLower(status) {
	case "blocked":
		return ui.Red("⛔ [BLOCKED]")
	case "evaluating", "review":
		return ui.Yellow("🔍 [EVALUATING]")
	case "done", "completed":
		return ui.Green("✔ [DONE]")
	case "in_progress", "in-progress":
		return ui.Cyan("🔵 [IN-PROGRESS]")
	default:
		return ui.Cyan(fmt.Sprintf("[%s]", strings.ToUpper(status)))
	}
}

// RenderGoalViewTerminal renders an ANSI-formatted, human-readable terminal inspection card.
func RenderGoalViewTerminal(ui render.UI, view *GoalView) string {
	if view == nil {
		return ""
	}
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("%s — %s\n\n", ui.Cyan(ui.Bold("sila · goal view")), ui.Bold(view.Slug)))

	// Title
	sb.WriteString(fmt.Sprintf("  %s %s\n", ui.Bold(view.Title), ui.Dim("("+view.Slug+")")))

	// Badges line
	modeBadge := ui.Cyan("⚡ [Independent]")
	if !view.Independent {
		if view.Blocked {
			modeBadge = ui.Red("⛔ [Blocked]")
		} else {
			modeBadge = ui.Green("🔗 [Ready · Dependencies Met]")
		}
	}
	if len(view.FileConflicts) > 0 {
		modeBadge += " " + ui.Red("⚠️ [File Conflict]")
	}

	statusBadge := ""
	switch view.Status {
	case "implemented":
		statusBadge = ui.Green("🟢 [IMPLEMENTED]")
		if view.Commit != "" {
			statusBadge += " " + ui.Dim("commit "+view.Commit)
		}
	case "claimed":
		agentName := "agent"
		if view.Claim != nil {
			if view.Claim.Agent != "" {
				agentName = view.Claim.Agent
			} else if view.Claim.ClaimedBy != "" {
				agentName = view.Claim.ClaimedBy
			}
		}
		statusBadge = ui.Cyan("🔵 [IN-PROGRESS / CLAIMED]") + " " + ui.Cyan("@"+agentName)
		if view.Claim != nil && view.Claim.Stale {
			statusBadge += " " + ui.Red("[STALE]")
		}
	case "superseded":
		statusBadge = ui.Dim("🟣 [SUPERSEDED]")
		if view.Supersede != nil && view.Supersede.SupersededBy != "" {
			statusBadge += " " + ui.Dim("(by "+view.Supersede.SupersededBy+")")
		}
	case "draft":
		statusBadge = ui.Dim("📝 [DRAFT]")
	case "pending":
		fallthrough
	default:
		if view.Blocked {
			statusBadge = ui.Red("⛔ [BLOCKED]")
		} else if len(view.FileConflicts) > 0 {
			statusBadge = ui.Red("⚠️ [CONTESTED]")
		} else {
			statusBadge = ui.Yellow("🟡 [READY TO CLAIM]")
		}
	}

	seqBadge := ""
	if view.Sequence > 0 {
		seqBadge = fmt.Sprintf(" %s", ui.Dim(fmt.Sprintf("(Seq #%d)", view.Sequence)))
	}

	sb.WriteString(fmt.Sprintf("  %s %s %s %s%s\n\n", shapeBadgeDim(ui, view.Shape), tierBadge(ui, view.Tier), statusBadge, modeBadge, seqBadge))

	// Status detail / blockers / claims
	if view.Blocked {
		reason := view.BlockedReason
		if len(view.Blockers) > 0 {
			var blockerDescs []string
			for _, b := range view.Blockers {
				switch b.Code {
				case DiagDependencyDecisionMismatch:
					blockerDescs = append(blockerDescs, fmt.Sprintf("decision mismatch on %s: required %v, observed %v", b.SourceGoal, b.Required, b.Observed))
				case DiagDependencyArtifactNotFound:
					blockerDescs = append(blockerDescs, fmt.Sprintf("missing artifact: %s in %s", b.Artifact, b.SourceGoal))
				case DiagDependencyArtifactMismatch:
					blockerDescs = append(blockerDescs, fmt.Sprintf("artifact digest mismatch: %s in %s", b.Artifact, b.SourceGoal))
				case DiagDependencyGoalNotAccepted:
					blockerDescs = append(blockerDescs, fmt.Sprintf("prerequisite %s is not accepted", b.SourceGoal))
				case DiagDependencyGoalReopened:
					blockerDescs = append(blockerDescs, fmt.Sprintf("prerequisite %s is reopened", b.SourceGoal))
				default:
					if b.Message != "" {
						blockerDescs = append(blockerDescs, b.Message)
					} else {
						blockerDescs = append(blockerDescs, fmt.Sprintf("%s on %s", b.Code, b.SourceGoal))
					}
				}
			}
			reason = strings.Join(blockerDescs, "; ")
		} else if reason == "" && len(view.UnmetDeps) > 0 {
			reason = fmt.Sprintf("Must implement %s first", strings.Join(view.UnmetDeps, ", "))
		}
		sb.WriteString(fmt.Sprintf("  %s %s\n", ui.Bold("Blocked By:"), ui.Red(reason)))
	}
	if len(view.FileConflicts) > 0 {
		for _, cf := range view.FileConflicts {
			sb.WriteString(fmt.Sprintf("  %s touches %v currently held by active goal %s (@%s)\n",
				ui.Red(ui.Bold("Collision:")), cf.OverlappingFiles, cf.ConflictingGoal, cf.HolderAgent))
		}
	}
	if view.Claim != nil {
		if view.Claim.Status != "" {
			sb.WriteString(fmt.Sprintf("  %s %s\n", ui.Bold("Claim Status:"), renderGoalClaimStatus(ui, view.Claim.Status)))
		}
		if view.Claim.Note != "" {
			sb.WriteString(fmt.Sprintf("  %s %s\n", ui.Bold("Claim Note:"), ui.Yellow(view.Claim.Note)))
		}
	}

	// Dependencies
	if len(view.DependsOn) > 0 {
		sb.WriteString(fmt.Sprintf("  %s %s\n", ui.Bold("Dependencies:"), ui.Cyan(strings.Join(view.DependsOn, ", "))))
	}

	// Review Squad
	if len(view.Personas) > 0 {
		sb.WriteString(fmt.Sprintf("  %s %s\n", ui.Bold("Review Squad:"), ui.Cyan(strings.Join(view.Personas, ", "))))
	}

	// Files to Touch
	if len(view.FilesToTouch) > 0 {
		sb.WriteString(fmt.Sprintf("  %s %s\n", ui.Bold("Files to Touch:"), strings.Join(view.FilesToTouch, ", ")))
	}

	// Launch command
	if view.LaunchCmd != "" {
		sb.WriteString(fmt.Sprintf("  %s %s\n", ui.Bold("Launch Cmd:"), ui.Dim(view.LaunchCmd)))
	}

	// Description
	if view.Description != "" {
		sb.WriteString("\n" + ui.Bold("Description:") + "\n")
		for _, line := range strings.Split(view.Description, "\n") {
			sb.WriteString(fmt.Sprintf("  %s\n", line))
		}
	}

	// Execution Plan / Checklist
	if len(view.Tasks) > 0 {
		pct := 0
		if view.TasksTotal > 0 {
			pct = (view.TasksMet * 100) / view.TasksTotal
		}
		bar := renderProgressBar(ui, view.TasksMet, view.TasksTotal, 8)
		sb.WriteString(fmt.Sprintf("\n%s\n", ui.Bold(fmt.Sprintf("Execution Plan (%s %d/%d tasks completed · %d%%):", bar, view.TasksMet, view.TasksTotal, pct))))
		for _, t := range view.Tasks {
			box := ui.Dim("[ ]")
			text := t.Text
			if t.Completed {
				box = ui.Green("[✔]")
				text = ui.Green(text)
			}
			sb.WriteString(fmt.Sprintf("  %s %2d. %s\n", box, t.Index, text))
		}
	}

	// Invariants
	if len(view.Invariants) > 0 {
		sb.WriteString("\n" + ui.Bold("Architectural Invariants & Constraints:") + "\n")
		for _, inv := range view.Invariants {
			sb.WriteString(fmt.Sprintf("  • %s\n", inv))
		}
	}

	// Done Conditions
	if len(view.DoneConditions) > 0 {
		sb.WriteString("\n" + ui.Bold("Done Conditions:") + "\n")
		for _, dc := range view.DoneConditions {
			sb.WriteString(fmt.Sprintf("  ✔ %s\n", dc))
		}
	}

	// Linked Evaluation Reports
	if len(view.Reports) > 0 {
		sb.WriteString("\n" + ui.Bold(fmt.Sprintf("Linked Evaluation Reports (%d):", len(view.Reports))) + "\n")
		for _, rep := range view.Reports {
			statusBadge := ui.Yellow(strings.ToUpper(rep.Status))
			if rep.Status == "critical" {
				statusBadge = ui.Red(strings.ToUpper(rep.Status))
			} else if rep.Status == "pass" || rep.Status == "success" {
				statusBadge = ui.Green(strings.ToUpper(rep.Status))
			}
			personaLabel := rep.PersonaID
			if rep.PersonaName != "" {
				personaLabel = fmt.Sprintf("[%d] %s (%s)", rep.PersonaNumID, rep.PersonaName, rep.PersonaID)
			}
			sb.WriteString(fmt.Sprintf("  • %s %s by %s\n", ui.Bold(rep.ID), statusBadge, ui.Cyan(personaLabel)))
			if rep.Summary != "" {
				sb.WriteString(fmt.Sprintf("    %s\n", rep.Summary))
			}
		}
	}

	// Next step advice
	sb.WriteString("\n" + ui.Dim("---------------------------------------------------------------") + "\n")
	if view.Status == "pending" && !view.Blocked && len(view.FileConflicts) == 0 {
		sb.WriteString(fmt.Sprintf("  %s %s\n", ui.Bold("Next:"), ui.Cyan(fmt.Sprintf("sila goals claim %s --agent=<name>", view.Slug))))
	} else if view.Status == "claimed" {
		sb.WriteString(fmt.Sprintf("  %s %s\n", ui.Bold("Execute:"), ui.Cyan(view.LaunchCmd)))
	} else if view.Status == "implemented" {
		sb.WriteString(fmt.Sprintf("  %s Goal is implemented and verified on disk.\n", ui.Green("✔")))
	}

	return sb.String()
}

// RenderGoalViewTOON formats a GoalView into Token-Oriented Object Notation (TOON).
func RenderGoalViewTOON(view *GoalView) string {
	if view == nil {
		return ""
	}
	modeStr := "independent"
	if !view.Independent {
		modeStr = "dependent"
	}
	fields := [][2]string{
		{"slug", view.Slug},
		{"title", view.Title},
		{"status", view.Status},
		{"tier", view.Tier},
		{"shape", view.Shape},
		{"mode", modeStr},
	}
	if view.Sequence > 0 {
		fields = append(fields, [2]string{"sequence", strconv.Itoa(view.Sequence)})
	}
	if len(view.DependsOn) > 0 {
		fields = append(fields, [2]string{"dependencies", strings.Join(view.DependsOn, ", ")})
	}
	if len(view.Personas) > 0 {
		fields = append(fields, [2]string{"personas", strings.Join(view.Personas, ", ")})
	}
	if len(view.UnmetDeps) > 0 {
		fields = append(fields, [2]string{"unmet_dependencies", strings.Join(view.UnmetDeps, ", ")})
	}
	if view.Blocked {
		reason := view.BlockedReason
		if len(view.Blockers) > 0 {
			var blockerDescs []string
			for _, b := range view.Blockers {
				if b.Code == DiagDependencyDecisionMismatch {
					blockerDescs = append(blockerDescs, fmt.Sprintf("decision mismatch on %s: required %v, observed %v", b.SourceGoal, b.Required, b.Observed))
				} else if b.Artifact != "" {
					blockerDescs = append(blockerDescs, fmt.Sprintf("%s (%s in %s)", b.Code, b.Artifact, b.SourceGoal))
				} else {
					blockerDescs = append(blockerDescs, fmt.Sprintf("%s (%s)", b.Code, b.SourceGoal))
				}
			}
			reason = strings.Join(blockerDescs, "; ")
		} else if reason == "" {
			reason = "unmet dependencies"
		}
		fields = append(fields, [2]string{"blocked_reason", reason})
	}
	if len(view.FilesToTouch) > 0 {
		fields = append(fields, [2]string{"files_to_touch", strings.Join(view.FilesToTouch, ", ")})
	}
	if view.Commit != "" {
		fields = append(fields, [2]string{"commit", view.Commit})
	}
	if view.Claim != nil {
		agent := view.Claim.Agent
		if agent == "" {
			agent = view.Claim.ClaimedBy
		}
		fields = append(fields, [2]string{"claimed_by", agent})
		if view.Claim.Status != "" {
			fields = append(fields, [2]string{"claim_status", view.Claim.Status})
		}
		if view.Claim.Note != "" {
			fields = append(fields, [2]string{"claim_note", view.Claim.Note})
		}
	}
	fields = append(fields, [2]string{"tasks_met", strconv.Itoa(view.TasksMet)})
	fields = append(fields, [2]string{"tasks_total", strconv.Itoa(view.TasksTotal)})
	if view.Description != "" {
		fields = append(fields, [2]string{"description", view.Description})
	}
	fields = append(fields, [2]string{"launch_cmd", view.LaunchCmd})

	objTOON := render.FormatTOONObject("goal_view", fields)

	var sb strings.Builder
	sb.WriteString(objTOON)

	if len(view.Tasks) > 0 {
		sb.WriteString("\n  tasks:\n")
		for _, t := range view.Tasks {
			mark := "[ ]"
			if t.Completed {
				mark = "[x]"
			}
			sb.WriteString(fmt.Sprintf("    - %s %d. %s\n", mark, t.Index, t.Text))
		}
	}

	if len(view.Invariants) > 0 {
		sb.WriteString("  invariants:\n")
		for _, inv := range view.Invariants {
			sb.WriteString(fmt.Sprintf("    - %s\n", inv))
		}
	}

	if len(view.DoneConditions) > 0 {
		sb.WriteString("  done_conditions:\n")
		for _, dc := range view.DoneConditions {
			sb.WriteString(fmt.Sprintf("    - %s\n", dc))
		}
	}

	if len(view.Reports) > 0 {
		sb.WriteString("  reports:\n")
		for _, r := range view.Reports {
			sb.WriteString(fmt.Sprintf("    - %s (%s) [%s]\n", r.ID, r.PersonaID, r.Status))
		}
	}

	return sb.String()
}
