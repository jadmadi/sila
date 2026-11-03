package goals

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// taskAcceptancePathInGoalDir resolves a task acceptance record inside a goal directory,
// preferring the canonical evidence path written by SaveTaskAcceptance.
func taskAcceptancePathInGoalDir(goalDir, taskID string) string {
	cleanTaskID := filepath.Clean(taskID)
	p1 := filepath.Join(goalDir, "evidence", cleanTaskID, "acceptance.json")
	p2 := filepath.Join(goalDir, "acceptance", cleanTaskID+".json")
	if fileExists(p2) && !fileExists(p1) {
		return p2
	}
	return p1
}

// TaskAcceptancePath returns the filesystem path to a task's persisted acceptance record.
func TaskAcceptancePath(rootDir, slug, taskID string) string {
	return taskAcceptancePathInGoalDir(filepath.Join(resolveGoalsRootDir(rootDir), "goals", filepath.Clean(slug)), taskID)
}

// GoalAcceptancePath returns the filesystem path to a goal's persisted acceptance record.
func GoalAcceptancePath(rootDir, slug string) string {
	cleanSlug := filepath.Clean(slug)
	p1 := filepath.Join(resolveGoalsRootDir(rootDir), "goals", cleanSlug, "evidence", "acceptance.json")
	p2 := filepath.Join(resolveGoalsRootDir(rootDir), "goals", cleanSlug, "acceptance.json")
	if fileExists(p2) && !fileExists(p1) {
		return p2
	}
	return p1
}

// SaveTaskAcceptance atomically records an AcceptanceResult and optional selection/review envelopes
// under goals/<slug>/acceptance/<task-id>.json.
func SaveTaskAcceptance(rootDir, slug, taskID string, res *AcceptanceResult, sel *ReceiptSelection, rev *AcceptanceReview) error {
	if res == nil {
		return fmt.Errorf("acceptance result is nil")
	}
	if strings.TrimSpace(slug) == "" {
		slug = res.GoalSlug
	}
	if strings.TrimSpace(slug) == "" {
		return fmt.Errorf("goal slug is required")
	}
	if strings.TrimSpace(taskID) == "" {
		taskID = res.TaskID
	}
	if strings.TrimSpace(taskID) == "" {
		return fmt.Errorf("task ID is required")
	}

	targetPath := TaskAcceptancePath(rootDir, slug, taskID)
	dir := filepath.Dir(targetPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create acceptance directory: %w", err)
	}

	data, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal acceptance result: %w", err)
	}
	if err := AtomicWriteFile(targetPath, data, 0644); err != nil {
		return fmt.Errorf("write task acceptance: %w", err)
	}

	// Also persist selection and review alongside acceptance result if provided
	if sel != nil {
		selPath := filepath.Join(dir, fmt.Sprintf("%s-selection.json", filepath.Clean(taskID)))
		if selData, err := json.MarshalIndent(sel, "", "  "); err == nil {
			_ = AtomicWriteFile(selPath, selData, 0644)
		}
	}
	if rev != nil {
		revPath := filepath.Join(dir, fmt.Sprintf("%s-review.json", filepath.Clean(taskID)))
		if revData, err := json.MarshalIndent(rev, "", "  "); err == nil {
			_ = AtomicWriteFile(revPath, revData, 0644)
		}
	}

	return nil
}

// LoadTaskAcceptance reads and decodes a task's persisted acceptance record.
func LoadTaskAcceptance(rootDir, slug, taskID string) (*AcceptanceResult, error) {
	p := TaskAcceptancePath(rootDir, slug, taskID)
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	return DecodeAcceptanceResult(data)
}

// SaveGoalAcceptance atomically records a goal-level AcceptanceResult under goals/<slug>/acceptance.json.
func SaveGoalAcceptance(rootDir, slug string, res *AcceptanceResult) error {
	if res == nil {
		return fmt.Errorf("acceptance result is nil")
	}
	if strings.TrimSpace(slug) == "" {
		slug = res.GoalSlug
	}
	targetPath := GoalAcceptancePath(rootDir, slug)
	dir := filepath.Dir(targetPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create goal acceptance directory: %w", err)
	}

	data, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal goal acceptance result: %w", err)
	}
	return AtomicWriteFile(targetPath, data, 0644)
}

// LoadGoalAcceptance reads and decodes a goal's persisted acceptance record.
func LoadGoalAcceptance(rootDir, slug string) (*AcceptanceResult, error) {
	p := GoalAcceptancePath(rootDir, slug)
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	return DecodeAcceptanceResult(data)
}

// GoalAcceptanceSummary summarizes goal-level acceptance metrics.
type GoalAcceptanceSummary struct {
	Status        EvidenceStatus `json:"status"`
	AcceptedTasks int            `json:"accepted_tasks"`
	TotalTasks    int            `json:"total_tasks"`
	RequiredCases int            `json:"required_cases"`
	PassedCases   int            `json:"passed_cases"`
	Result        *AcceptanceResult
}

// EvaluateGoalAcceptance derives structured goal acceptance by aggregating and revalidating
// task-level acceptance results against the current specification and source candidate.
func EvaluateGoalAcceptance(rootDir, slug string) (*AcceptanceResult, error) {
	if strings.TrimSpace(slug) == "" {
		return nil, fmt.Errorf("goal slug is required")
	}
	rootDir = resolveGoalsRootDir(rootDir)
	slug = filepath.Clean(slug)
	goalDir := filepath.Join(rootDir, "goals", slug)

	// Check if goal directory exists
	if !fileExists(filepath.Join(goalDir, "goal.md")) && !fileExists(filepath.Join(goalDir, "work-package.json")) {
		return nil, fmt.Errorf("goal %q not found in %s", slug, filepath.Join(rootDir, "goals"))
	}

	// For legacy goals without work-package.json, acceptance protocol is not_run
	wpPath := filepath.Join(goalDir, "work-package.json")
	if !fileExists(wpPath) {
		res := &AcceptanceResult{
			SchemaVersion:  AcceptanceResultSchemaVersion,
			GoalSlug:       slug,
			TaskID:         "",
			Accepted:       false,
			EvidenceStatus: EvidenceStatusNotRun,
			Diagnostics:    []Diagnostic{},
		}
		if cert, _ := GetActiveCertification(rootDir, slug); cert != nil {
			res.ReviewedAt = cert.CertifiedAt.Format(time.RFC3339)
		}
		return res, nil
	}

	// Structured goal: load work-package
	wp, err := LoadWorkPackage(rootDir, slug)
	if err != nil {
		return nil, fmt.Errorf("load work package for %s: %w", slug, err)
	}
	currentSpecDigest, err := WorkPackageDigest(wp)
	if err != nil {
		return nil, fmt.Errorf("compute spec digest: %w", err)
	}

	// Capture current candidate digest if available (non-blocking on error)
	currentCandDigest := ""
	currentCommit := ""
	if cand, err := CaptureTaskCandidate(rootDir, slug); err == nil && cand != nil {
		currentCandDigest = TaskCandidateDigest(*cand)
		currentCommit = cand.Commit
	} else {
		cmdRev := exec.Command("git", "rev-parse", "HEAD")
		cmdRev.Dir = rootDir
		if outRev, err := cmdRev.Output(); err == nil {
			currentCommit = strings.TrimSpace(string(outRev))
		}
	}

	res := &AcceptanceResult{
		SchemaVersion:   AcceptanceResultSchemaVersion,
		GoalSlug:        slug,
		TaskID:          "",
		Accepted:        false,
		EvidenceStatus:  EvidenceStatusIncomplete,
		SpecDigest:      currentSpecDigest,
		CandidateDigest: currentCandDigest,
		ReceiptPaths:    []string{},
		MissingCaseIDs:  []string{},
		FailedCaseIDs:   []string{},
		Diagnostics:     []Diagnostic{},
		ReviewedAt:      "",
	}

	if len(wp.Tasks) == 0 {
		res.EvidenceStatus = EvidenceStatusIncomplete
		res.Diagnostics = append(res.Diagnostics, Diagnostic{
			Code:     "NO_TASKS_DEFINED",
			Severity: "error",
			GoalSlug: slug,
			Message:  "work package defines zero tasks",
		})
		return res, nil
	}

	// Map mandatory cases per task
	taskMandatoryCases := make(map[string][]string, len(wp.Tasks))
	for _, t := range wp.Tasks {
		declared := make(map[string]bool, len(t.AcceptanceIDs))
		for _, aid := range t.AcceptanceIDs {
			declared[aid] = true
		}
		var mCases []string
		for _, ac := range wp.Acceptance {
			if declared[ac.ID] && ac.Mandatory {
				mCases = append(mCases, ac.ID)
			}
		}
		sort.Strings(mCases)
		taskMandatoryCases[t.ID] = mCases
	}

	anyTaskStale := false
	anyTaskFailed := false
	allTasksAccepted := true
	anyReceiptsFound := false
	latestReview := ""

	for _, t := range wp.Tasks {
		mCases := taskMandatoryCases[t.ID]
		taskAcc, err := LoadTaskAcceptance(rootDir, slug, t.ID)
		if err != nil {
			// Task acceptance record absent
			allTasksAccepted = false
			res.MissingCaseIDs = append(res.MissingCaseIDs, mCases...)
			continue
		}

		if len(taskAcc.ReceiptPaths) > 0 {
			anyReceiptsFound = true
			res.ReceiptPaths = append(res.ReceiptPaths, taskAcc.ReceiptPaths...)
		}

		// Revalidate task acceptance against current state
		isStale := false

		// 1. Spec digest revalidation
		if taskAcc.SpecDigest != currentSpecDigest {
			isStale = true
			res.Diagnostics = append(res.Diagnostics, Diagnostic{
				Code:     "SPEC_DIGEST_STALE",
				Severity: "warning",
				GoalSlug: slug,
				Path:     filepath.Join("goals", slug, "acceptance", t.ID+".json"),
				Message:  fmt.Sprintf("task %s acceptance spec_digest %q is stale against current %q", t.ID, taskAcc.SpecDigest, currentSpecDigest),
			})
		}

		// 2. Candidate source revalidation (AC-008: Changing source after acceptance makes current evidence stale)
		if taskAcc.CandidateDigest != "" {
			if currentCandDigest != "" && currentCandDigest != taskAcc.CandidateDigest {
				isStale = true
				res.Diagnostics = append(res.Diagnostics, Diagnostic{
					Code:     "CANDIDATE_DIGEST_STALE",
					Severity: "warning",
					GoalSlug: slug,
					Path:     filepath.Join("goals", slug, "acceptance", t.ID+".json"),
					Message:  fmt.Sprintf("task %s acceptance candidate_digest %q is stale against current %q", t.ID, taskAcc.CandidateDigest, currentCandDigest),
				})
			} else if currentCandDigest == "" && currentCommit != "" && !strings.HasPrefix(currentCommit, taskAcc.CandidateDigest) && !strings.HasPrefix(taskAcc.CandidateDigest, currentCommit) {
				isStale = true
				res.Diagnostics = append(res.Diagnostics, Diagnostic{
					Code:     "CANDIDATE_COMMIT_STALE",
					Severity: "warning",
					GoalSlug: slug,
					Path:     filepath.Join("goals", slug, "acceptance", t.ID+".json"),
					Message:  fmt.Sprintf("task %s acceptance candidate %q is stale against current commit %q", t.ID, taskAcc.CandidateDigest, currentCommit),
				})
			}
		}

		// 3. Receipt files existence on disk
		for _, rp := range taskAcc.ReceiptPaths {
			rFull := resolveReceiptPath(rootDir, slug, rp)
			if !fileExists(rFull) {
				isStale = true
				res.Diagnostics = append(res.Diagnostics, Diagnostic{
					Code:     "RECEIPT_FILE_MISSING",
					Severity: "warning",
					GoalSlug: slug,
					Path:     rp,
					Message:  fmt.Sprintf("receipt %s for task %s no longer exists on disk", rp, t.ID),
				})
			}
		}

		if isStale {
			anyTaskStale = true
			allTasksAccepted = false
			res.MissingCaseIDs = append(res.MissingCaseIDs, mCases...)
		} else if !taskAcc.Accepted || taskAcc.EvidenceStatus != EvidenceStatusAccepted {
			allTasksAccepted = false
			if len(taskAcc.FailedCaseIDs) > 0 || taskAcc.EvidenceStatus == EvidenceStatusFailed {
				anyTaskFailed = true
				res.FailedCaseIDs = append(res.FailedCaseIDs, taskAcc.FailedCaseIDs...)
			}
			res.MissingCaseIDs = append(res.MissingCaseIDs, taskAcc.MissingCaseIDs...)
		} else {
			// Task is fully accepted and not stale
			if taskAcc.ReviewedAt > latestReview {
				latestReview = taskAcc.ReviewedAt
			}
		}
	}

	sort.Strings(res.MissingCaseIDs)
	sort.Strings(res.FailedCaseIDs)
	res.ReviewedAt = latestReview

	// 4. Dependency conditions revalidation (AC-008: dependency drift makes evidence stale)
	if len(wp.DependencyConditions) > 0 {
		condDiags := EvaluateDependencyConditions(rootDir, wp.DependencyConditions)
		if len(condDiags) > 0 {
			anyTaskStale = true
			allTasksAccepted = false
			for _, cd := range condDiags {
				res.Diagnostics = append(res.Diagnostics, Diagnostic{
					Code:     "DEPENDENCY_DRIFT",
					Severity: "warning",
					GoalSlug: slug,
					Path:     filepath.Join("goals", cd.GoalSlug),
					Message:  fmt.Sprintf("dependency condition on goal %q failed: %s: %s", cd.GoalSlug, cd.Code, cd.Message),
				})
			}
		}
	}

	if anyTaskStale {
		res.EvidenceStatus = EvidenceStatusStale
		res.Accepted = false
	} else if anyTaskFailed {
		res.EvidenceStatus = EvidenceStatusFailed
		res.Accepted = false
	} else if allTasksAccepted {
		res.EvidenceStatus = EvidenceStatusAccepted
		res.Accepted = true
	} else if !anyReceiptsFound {
		res.EvidenceStatus = EvidenceStatusIncomplete
		res.Accepted = false
	} else {
		res.EvidenceStatus = EvidenceStatusIncomplete
		res.Accepted = false
	}

	return res, nil
}
