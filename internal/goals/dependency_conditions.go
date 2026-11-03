package goals

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Diagnostic codes for dependency condition evaluation.
const (
	DiagDependencyConditionInvalid  = "DEPENDENCY_CONDITION_INVALID"
	DiagDependencyPathTraversal     = "DEPENDENCY_PATH_TRAVERSAL"
	DiagDependencyGoalNotFound      = "DEPENDENCY_GOAL_NOT_FOUND"
	DiagDependencyGoalReopened      = "DEPENDENCY_GOAL_REOPENED"
	DiagDependencyGoalSuperseded    = "DEPENDENCY_GOAL_SUPERSEDED"
	DiagDependencyGoalNotAccepted   = "DEPENDENCY_GOAL_NOT_ACCEPTED"
	DiagDependencyTaskNotFound      = "DEPENDENCY_TASK_NOT_FOUND"
	DiagDependencyTaskNotAccepted   = "DEPENDENCY_TASK_NOT_ACCEPTED"
	DiagDependencyArtifactNotFound  = "DEPENDENCY_ARTIFACT_NOT_FOUND"
	DiagDependencyArtifactMismatch  = "DEPENDENCY_ARTIFACT_DIGEST_MISMATCH"
	DiagDependencyArtifactMalformed = "DEPENDENCY_ARTIFACT_MALFORMED"
	DiagDependencyPointerInvalid    = "DEPENDENCY_POINTER_INVALID"
	DiagDependencyPointerNotFound   = "DEPENDENCY_POINTER_NOT_FOUND"
	DiagDependencyValueTypeMismatch = "DEPENDENCY_VALUE_TYPE_MISMATCH"
	DiagDependencyDecisionMismatch  = "DEPENDENCY_DECISION_MISMATCH"
	DiagDependencyCycle             = "DEPENDENCY_CYCLE"
)

// EvaluateDependencyConditions evaluates a list of typed dependency conditions against
// repository goal state. All conditions are strictly ANDed; any failure yields one or
// more Diagnostic blockers.
func EvaluateDependencyConditions(rootDir string, conditions []DependencyCondition) []Diagnostic {
	if len(conditions) == 0 {
		return nil
	}
	if rootDir == "" {
		rootDir = "."
	}
	rootDir = filepath.Clean(rootDir)

	var diags []Diagnostic

	// Check graph cycles across all goals in the repository
	if g, err := ScanGoalsDependencies(rootDir); err == nil && g != nil {
		cycles := g.DetectCycles()
		if len(cycles) > 0 {
			cycleGoals := make(map[string]bool)
			for _, cycle := range cycles {
				for _, member := range cycle {
					cycleGoals[member] = true
				}
			}
			for _, dc := range conditions {
				if cycleGoals[dc.GoalSlug] {
					diags = append(diags, Diagnostic{
						Code:        DiagDependencyCycle,
						Severity:    "error",
						GoalSlug:    dc.GoalSlug,
						Message:     fmt.Sprintf("dependency cycle detected involving goal %q", dc.GoalSlug),
						Remediation: "Resolve circular goal dependencies in plan or metadata",
					})
				}
			}
		}
	}

	for i, dc := range conditions {
		condDiags := evaluateSingleCondition(rootDir, i, dc)
		diags = append(diags, condDiags...)
	}

	return diags
}

func evaluateSingleCondition(rootDir string, index int, dc DependencyCondition) []Diagnostic {
	var diags []Diagnostic

	// 1. Validate condition kind
	if !dc.Kind.IsValid() {
		return []Diagnostic{
			{
				Code:        DiagDependencyConditionInvalid,
				Severity:    "error",
				GoalSlug:    dc.GoalSlug,
				Message:     fmt.Sprintf("condition[%d]: invalid kind %q", index, dc.Kind),
				Remediation: "Specify one of: accepted_goal, accepted_task, artifact_digest, decision_equals",
			},
		}
	}

	// 2. Validate goal_slug
	slug := strings.TrimSpace(dc.GoalSlug)
	if slug == "" {
		return []Diagnostic{
			{
				Code:        DiagDependencyConditionInvalid,
				Severity:    "error",
				Message:     fmt.Sprintf("condition[%d]: missing goal_slug", index),
				Remediation: "Provide a valid goal_slug",
			},
		}
	}
	cleanSlug := filepath.Clean(filepath.ToSlash(slug))
	if cleanSlug == "." || cleanSlug == ".." || strings.HasPrefix(cleanSlug, "../") || strings.Contains(cleanSlug, "/") || filepath.IsAbs(slug) {
		return []Diagnostic{
			{
				Code:        DiagDependencyPathTraversal,
				Severity:    "error",
				GoalSlug:    slug,
				Message:     fmt.Sprintf("condition[%d]: goal_slug %q contains illegal traversal or path characters", index, slug),
				Remediation: "Use a simple goal slug without directory separators",
			},
		}
	}

	// 3. Verify predecessor goal existence
	goalDir := filepath.Join(rootDir, "goals", slug)
	goalMdPath := filepath.Join(goalDir, "goal.md")
	if !fileExists(goalMdPath) {
		return []Diagnostic{
			{
				Code:        DiagDependencyGoalNotFound,
				Severity:    "error",
				GoalSlug:    slug,
				Path:        filepath.Join("goals", slug),
				Message:     fmt.Sprintf("predecessor goal %q does not exist", slug),
				Remediation: "Register the predecessor goal before depending on it",
			},
		}
	}

	// 4. Verify goal is not reopened
	if reopenRec, err := GetActiveReopen(rootDir, slug); err == nil && reopenRec != nil && reopenRec.Active {
		return []Diagnostic{
			{
				Code:        DiagDependencyGoalReopened,
				Severity:    "error",
				GoalSlug:    slug,
				Message:     fmt.Sprintf("predecessor goal %q is currently reopened for remediation (%s)", slug, reopenRec.Reason),
				Remediation: "Complete remediation on predecessor goal before unlocking dependent",
			},
		}
	}

	// 5. Verify goal is not superseded without explicit replacement
	if supRec, err := GetActiveSupersede(rootDir, slug); err == nil && supRec != nil {
		return []Diagnostic{
			{
				Code:        DiagDependencyGoalSuperseded,
				Severity:    "error",
				GoalSlug:    slug,
				Message:     fmt.Sprintf("predecessor goal %q has been superseded by %s", slug, supRec.SupersededBy),
				Remediation: fmt.Sprintf("Update dependency to point to replacement goal %q", supRec.SupersededBy),
			},
		}
	}

	// 6. Evaluate by variant
	switch dc.Kind {
	case DepConditionAcceptedGoal:
		diags = append(diags, evaluateAcceptedGoal(rootDir, slug)...)

	case DepConditionAcceptedTask:
		diags = append(diags, evaluateAcceptedTask(rootDir, slug, dc.TaskID)...)

	case DepConditionArtifactDigest:
		diags = append(diags, evaluateArtifactDigest(rootDir, slug, dc.Artifact, dc.SHA256)...)

	case DepConditionDecisionEquals:
		diags = append(diags, evaluateDecisionEquals(rootDir, slug, dc)...)
	}

	return diags
}

func evaluateAcceptedGoal(rootDir, slug string) []Diagnostic {
	eval, err := EvaluateGoalAcceptance(rootDir, slug)
	if err != nil {
		return []Diagnostic{
			{
				Code:        DiagDependencyGoalNotAccepted,
				Severity:    "error",
				GoalSlug:    slug,
				Message:     fmt.Sprintf("predecessor goal %q evaluation error: %v", slug, err),
				Remediation: "Ensure predecessor goal acceptance review is recorded",
			},
		}
	}
	if eval == nil || !eval.Accepted || eval.EvidenceStatus != EvidenceStatusAccepted {
		statusStr := "incomplete"
		if eval != nil {
			statusStr = string(eval.EvidenceStatus)
		}
		return []Diagnostic{
			{
				Code:        DiagDependencyGoalNotAccepted,
				Severity:    "error",
				GoalSlug:    slug,
				Message:     fmt.Sprintf("predecessor goal %q is not accepted (status: %s)", slug, statusStr),
				Remediation: "Accept all mandatory tasks and certify predecessor goal before proceeding",
			},
		}
	}
	return nil
}

func evaluateAcceptedTask(rootDir, slug, taskID string) []Diagnostic {
	tid := strings.TrimSpace(taskID)
	if tid == "" {
		return []Diagnostic{
			{
				Code:        DiagDependencyConditionInvalid,
				Severity:    "error",
				GoalSlug:    slug,
				Message:     "accepted_task condition missing task_id",
				Remediation: "Specify task_id (e.g. T-001)",
			},
		}
	}
	cleanTaskID := filepath.Clean(filepath.ToSlash(tid))
	if cleanTaskID == "." || cleanTaskID == ".." || strings.Contains(cleanTaskID, "/") || filepath.IsAbs(tid) {
		return []Diagnostic{
			{
				Code:        DiagDependencyPathTraversal,
				Severity:    "error",
				GoalSlug:    slug,
				Message:     fmt.Sprintf("task_id %q contains illegal characters", tid),
				Remediation: "Specify a simple task ID (e.g. T-001)",
			},
		}
	}

	taskAcc, err := LoadTaskAcceptance(rootDir, slug, tid)
	if err != nil || taskAcc == nil {
		return []Diagnostic{
			{
				Code:        DiagDependencyTaskNotFound,
				Severity:    "error",
				GoalSlug:    slug,
				Message:     fmt.Sprintf("predecessor task %s in goal %q acceptance record not found", tid, slug),
				Remediation: "Accept predecessor task before unlocking dependent",
			},
		}
	}
	if !taskAcc.Accepted || taskAcc.EvidenceStatus != EvidenceStatusAccepted {
		return []Diagnostic{
			{
				Code:        DiagDependencyTaskNotAccepted,
				Severity:    "error",
				GoalSlug:    slug,
				Message:     fmt.Sprintf("predecessor task %s in goal %q is not accepted (status: %s)", tid, slug, taskAcc.EvidenceStatus),
				Remediation: "Accept predecessor task with 'sila goals accept' before unlocking dependent",
			},
		}
	}
	return nil
}

func evaluateArtifactDigest(rootDir, slug, artifact, expectedSHA string) []Diagnostic {
	art := strings.TrimSpace(artifact)
	if art == "" {
		return []Diagnostic{
			{
				Code:        DiagDependencyConditionInvalid,
				Severity:    "error",
				GoalSlug:    slug,
				Message:     "artifact_digest condition missing artifact path",
				Remediation: "Provide artifact relative path",
			},
		}
	}
	cleanArt := filepath.Clean(filepath.ToSlash(art))
	if cleanArt == "." || cleanArt == ".." || strings.HasPrefix(cleanArt, "../") || filepath.IsAbs(art) {
		return []Diagnostic{
			{
				Code:        DiagDependencyPathTraversal,
				Severity:    "error",
				GoalSlug:    slug,
				Message:     fmt.Sprintf("artifact path %q attempts path traversal", art),
				Remediation: "Provide a clean relative path",
			},
		}
	}

	sha := strings.TrimSpace(expectedSHA)
	if len(sha) != 64 {
		return []Diagnostic{
			{
				Code:        DiagDependencyConditionInvalid,
				Severity:    "error",
				GoalSlug:    slug,
				Message:     fmt.Sprintf("expected sha256 %q is invalid (must be 64-char hex)", sha),
				Remediation: "Provide full 64-character SHA-256 hex digest",
			},
		}
	}

	artPath, fullPath := resolvePredecessorArtifact(rootDir, slug, cleanArt)
	data, err := os.ReadFile(fullPath)
	if err != nil {
		return []Diagnostic{
			{
				Code:        DiagDependencyArtifactNotFound,
				Severity:    "error",
				GoalSlug:    slug,
				Path:        artPath,
				Message:     fmt.Sprintf("artifact file %q not found on disk in predecessor goal %q", cleanArt, slug),
				Remediation: "Verify artifact path and ensure predecessor task produced it",
			},
		}
	}

	actualSum := sha256.Sum256(data)
	actualSHA := hex.EncodeToString(actualSum[:])
	if strings.ToLower(actualSHA) != strings.ToLower(sha) {
		return []Diagnostic{
			{
				Code:        DiagDependencyArtifactMismatch,
				Severity:    "error",
				GoalSlug:    slug,
				Path:        artPath,
				Message:     fmt.Sprintf("artifact %q digest mismatch: expected %s, observed %s", cleanArt, sha, actualSHA),
				Remediation: "Ensure artifact has not been modified after acceptance",
			},
		}
	}

	return nil
}

func evaluateDecisionEquals(rootDir, slug string, dc DependencyCondition) []Diagnostic {
	art := strings.TrimSpace(dc.Artifact)
	if art == "" {
		return []Diagnostic{
			{
				Code:        DiagDependencyConditionInvalid,
				Severity:    "error",
				GoalSlug:    slug,
				Message:     "decision_equals condition missing artifact",
				Remediation: "Specify artifact path (e.g. decision.json)",
			},
		}
	}
	cleanArt := filepath.Clean(filepath.ToSlash(art))
	if cleanArt == "." || cleanArt == ".." || strings.HasPrefix(cleanArt, "../") || filepath.IsAbs(art) {
		return []Diagnostic{
			{
				Code:        DiagDependencyPathTraversal,
				Severity:    "error",
				GoalSlug:    slug,
				Message:     fmt.Sprintf("artifact path %q attempts path traversal", art),
				Remediation: "Provide a clean relative path",
			},
		}
	}

	pointer := strings.TrimSpace(dc.Pointer)
	if pointer != "" && !strings.HasPrefix(pointer, "/") {
		return []Diagnostic{
			{
				Code:        DiagDependencyPointerInvalid,
				Severity:    "error",
				GoalSlug:    slug,
				Message:     fmt.Sprintf("JSON pointer %q must start with /", pointer),
				Remediation: "Prefix JSON pointer with / (e.g. /decision)",
			},
		}
	}

	if dc.Equals == nil {
		return []Diagnostic{
			{
				Code:        DiagDependencyConditionInvalid,
				Severity:    "error",
				GoalSlug:    slug,
				Message:     "decision_equals condition missing equals value",
				Remediation: "Specify expected scalar value for equals",
			},
		}
	}

	// 1. Predecessor goal must be accepted
	eval, err := EvaluateGoalAcceptance(rootDir, slug)
	if err != nil || eval == nil || !eval.Accepted || eval.EvidenceStatus != EvidenceStatusAccepted {
		statusStr := "unaccepted"
		if eval != nil {
			statusStr = string(eval.EvidenceStatus)
		}
		return []Diagnostic{
			{
				Code:        DiagDependencyGoalNotAccepted,
				Severity:    "error",
				GoalSlug:    slug,
				Message:     fmt.Sprintf("predecessor goal %q must be accepted before evaluating decision (status: %s)", slug, statusStr),
				Remediation: "Accept predecessor goal first",
			},
		}
	}

	// 2. Read artifact file on disk
	artPath, fullPath := resolvePredecessorArtifact(rootDir, slug, cleanArt)
	data, err := os.ReadFile(fullPath)
	if err != nil {
		return []Diagnostic{
			{
				Code:        DiagDependencyArtifactNotFound,
				Severity:    "error",
				GoalSlug:    slug,
				Path:        artPath,
				Message:     fmt.Sprintf("decision artifact %q not found on disk: %v", cleanArt, err),
				Remediation: "Ensure predecessor task produced the artifact file",
			},
		}
	}

	// 3. Verify artifact digest against accepted evidence (AC-002)
	// Editing decision.json after acceptance blocks on artifact digest before value comparison.
	recordedDigest, dErr := findRecordedArtifactDigest(rootDir, slug, cleanArt, eval, dc.SHA256)
	if dErr != nil {
		return []Diagnostic{
			{
				Code:        DiagDependencyArtifactMismatch,
				Severity:    "error",
				GoalSlug:    slug,
				Path:        artPath,
				Message:     fmt.Sprintf("decision artifact %q not attested in accepted evidence: %v", cleanArt, dErr),
				Remediation: "Record artifact in predecessor task acceptance receipts",
			},
		}
	}

	actualSum := sha256.Sum256(data)
	actualSHA := hex.EncodeToString(actualSum[:])
	if strings.ToLower(actualSHA) != strings.ToLower(recordedDigest) {
		return []Diagnostic{
			{
				Code:        DiagDependencyArtifactMismatch,
				Severity:    "error",
				GoalSlug:    slug,
				Path:        artPath,
				Message:     fmt.Sprintf("decision artifact %q digest mismatch against accepted evidence: expected %s, observed %s", cleanArt, recordedDigest, actualSHA),
				Remediation: "Restore the decision artifact to its accepted state",
			},
		}
	}

	// 4. Parse JSON
	var parsed any
	if err := json.Unmarshal(data, &parsed); err != nil {
		return []Diagnostic{
			{
				Code:        DiagDependencyArtifactMalformed,
				Severity:    "error",
				GoalSlug:    slug,
				Path:        artPath,
				Message:     fmt.Sprintf("decision artifact %q contains invalid JSON: %v", cleanArt, err),
				Remediation: "Ensure decision artifact is well-formed JSON",
			},
		}
	}

	// 5. Resolve pointer
	val, err := resolveJSONPointer(parsed, pointer)
	if err != nil {
		return []Diagnostic{
			{
				Code:        DiagDependencyPointerNotFound,
				Severity:    "error",
				GoalSlug:    slug,
				Path:        artPath,
				Message:     fmt.Sprintf("JSON pointer %q in artifact %q failed: %v", pointer, cleanArt, err),
				Remediation: "Verify JSON pointer against artifact schema",
			},
		}
	}

	// 6. Check scalar type and compare value
	switch val.(type) {
	case map[string]any, []any:
		return []Diagnostic{
			{
				Code:        DiagDependencyValueTypeMismatch,
				Severity:    "error",
				GoalSlug:    slug,
				Path:        artPath,
				Message:     fmt.Sprintf("pointer %q resolved to non-scalar JSON type %T; exact scalar required", pointer, val),
				Remediation: "Point to a string, boolean, or numeric property",
			},
		}
	}

	if !scalarsEqual(val, dc.Equals) {
		return []Diagnostic{
			{
				Code:        DiagDependencyDecisionMismatch,
				Severity:    "error",
				GoalSlug:    slug,
				Path:        artPath,
				Message:     fmt.Sprintf("decision mismatch on goal %q (%s%s): expected %v, observed %v", slug, cleanArt, pointer, dc.Equals, val),
				Remediation: fmt.Sprintf("Goal %q decision must be %v to unlock successor", slug, dc.Equals),
			},
		}
	}

	return nil
}

func resolvePredecessorArtifact(rootDir, slug, cleanArt string) (string, string) {
	cand1 := filepath.Join("goals", slug, cleanArt)
	full1 := filepath.Join(rootDir, cand1)
	if fileExists(full1) {
		return cand1, full1
	}
	cand2 := cleanArt
	full2 := filepath.Join(rootDir, cand2)
	if fileExists(full2) {
		return cand2, full2
	}
	return cand1, full1
}

func findRecordedArtifactDigest(rootDir, slug, cleanArt string, eval *AcceptanceResult, fallbackSHA string) (string, error) {
	baseName := filepath.Base(cleanArt)

	// Check receipts in accepted evaluation
	if eval != nil {
		for _, rp := range eval.ReceiptPaths {
			rFull := resolveReceiptPath(rootDir, slug, rp)
			if rData, err := os.ReadFile(rFull); err == nil {
				if ev, err := DecodeTaskEvidence(rData); err == nil {
					for _, art := range ev.Artifacts {
						artClean := filepath.Clean(filepath.ToSlash(art.Path))
						if artClean == cleanArt || filepath.Base(artClean) == baseName || strings.HasSuffix(artClean, "/"+cleanArt) {
							if art.SHA256 != "" {
								return strings.ToLower(art.SHA256), nil
							}
						}
					}
				}
			}
		}
	}

	// Check task acceptance records on disk
	accDir := filepath.Join(rootDir, "goals", slug, "acceptance")
	if entries, err := os.ReadDir(accDir); err == nil {
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), "-accepted.json") {
				data, err := os.ReadFile(filepath.Join(accDir, e.Name()))
				if err == nil {
					if res, err := DecodeAcceptanceResult(data); err == nil {
						for _, rp := range res.ReceiptPaths {
							rFull := resolveReceiptPath(rootDir, slug, rp)
							if rData, err := os.ReadFile(rFull); err == nil {
								if ev, err := DecodeTaskEvidence(rData); err == nil {
									for _, art := range ev.Artifacts {
										artClean := filepath.Clean(filepath.ToSlash(art.Path))
										if artClean == cleanArt || filepath.Base(artClean) == baseName || strings.HasSuffix(artClean, "/"+cleanArt) {
											if art.SHA256 != "" {
												return strings.ToLower(art.SHA256), nil
											}
										}
									}
								}
							}
						}
					}
				}
			}
		}
	}

	if fallbackSHA != "" && len(fallbackSHA) == 64 {
		return strings.ToLower(fallbackSHA), nil
	}

	return "", fmt.Errorf("no accepted receipt recorded sha256 for artifact %q", cleanArt)
}

func scalarsEqual(a, b any) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}

	// Same raw value
	if a == b {
		return true
	}

	// String comparison
	aStr, aIsStr := a.(string)
	bStr, bIsStr := b.(string)
	if aIsStr && bIsStr {
		return aStr == bStr
	}

	// Boolean comparison
	aBool, aIsBool := a.(bool)
	bBool, bIsBool := b.(bool)
	if aIsBool && bIsBool {
		return aBool == bBool
	}

	// Numeric comparison across float64/int/etc.
	aNum, aIsNum := toFloat64(a)
	bNum, bIsNum := toFloat64(b)
	if aIsNum && bIsNum {
		return aNum == bNum
	}

	// Fallback to formatted representation
	return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
}

func toFloat64(val any) (float64, bool) {
	switch v := val.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case int32:
		return float64(v), true
	case json.Number:
		if f, err := v.Float64(); err == nil {
			return f, true
		}
	}
	return 0, false
}
