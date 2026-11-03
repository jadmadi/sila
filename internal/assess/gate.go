package assess

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/jadmadi/sila/internal/git"
)

// VetoRecord represents a non-overrulable blocking veto on a goal.
type VetoRecord struct {
	VetoID      string     `json:"veto_id"`
	GoalSlug    string     `json:"goal_slug"`
	AuditorName string     `json:"auditor_name"`
	SessionID   string     `json:"session_id"`
	Reason      string     `json:"reason"`
	DefectFile  string     `json:"defect_file,omitempty"`
	VetoedAt    time.Time  `json:"vetoed_at"`
	Active      bool       `json:"active"`
	ResolvedAt  *time.Time `json:"resolved_at,omitempty"`
	ResolvedBy  string     `json:"resolved_by,omitempty"`
	ReceiptPath string     `json:"receipt_path,omitempty"`
}

// CheckReviewerVeto checks whether an active non-overrulable veto exists on the goal.
func CheckReviewerVeto(rootDir, goalSlug string) (*VetoRecord, error) {
	if goalSlug == "" {
		return nil, nil
	}
	goalSlug = filepath.Clean(goalSlug)
	vetoPath := filepath.Join(rootDir, "goals", goalSlug, "veto.json")
	if _, err := os.Stat(vetoPath); os.IsNotExist(err) {
		return nil, nil
	}
	data, err := os.ReadFile(vetoPath)
	if err != nil {
		return nil, fmt.Errorf("read veto.json: %w", err)
	}
	var record VetoRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, fmt.Errorf("parse veto.json: %w", err)
	}
	if record.Active {
		return &record, nil
	}
	return nil, nil
}

// RecordReviewerVeto persists a non-overrulable veto for a goal.
func RecordReviewerVeto(rootDir, goalSlug, auditorName, sessionID, reason, defectFile string) (*VetoRecord, error) {
	goalSlug = filepath.Clean(goalSlug)
	goalDir := filepath.Join(rootDir, "goals", goalSlug)
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		return nil, fmt.Errorf("mkdir goal dir: %w", err)
	}

	record := &VetoRecord{
		VetoID:      fmt.Sprintf("veto_%s_%d", goalSlug, time.Now().UnixNano()),
		GoalSlug:    goalSlug,
		AuditorName: auditorName,
		SessionID:   sessionID,
		Reason:      reason,
		DefectFile:  defectFile,
		VetoedAt:    time.Now().UTC(),
		Active:      true,
	}

	bytes, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal veto.json: %w", err)
	}
	vetoPath := filepath.Join(goalDir, "veto.json")
	if err := os.WriteFile(vetoPath, bytes, 0644); err != nil {
		return nil, fmt.Errorf("write veto.json: %w", err)
	}
	return record, nil
}

// ResolveReviewerVeto marks an active veto resolved by an auditor through a verified audit receipt.
// Task producers cannot call this; only a reviewer with a valid receipt can resolve the veto.
func ResolveReviewerVeto(rootDir, goalSlug, auditorName, sessionID, receiptPath string) error {
	goalSlug = filepath.Clean(goalSlug)
	vetoPath := filepath.Join(rootDir, "goals", goalSlug, "veto.json")
	if _, err := os.Stat(vetoPath); os.IsNotExist(err) {
		return nil // No veto to resolve
	}
	data, err := os.ReadFile(vetoPath)
	if err != nil {
		return fmt.Errorf("read veto.json: %w", err)
	}
	var record VetoRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return fmt.Errorf("parse veto.json: %w", err)
	}
	if !record.Active {
		return nil // Already resolved
	}

	now := time.Now().UTC()
	record.Active = false
	record.ResolvedAt = &now
	record.ResolvedBy = auditorName
	record.ReceiptPath = receiptPath

	bytes, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal veto.json: %w", err)
	}
	return os.WriteFile(vetoPath, bytes, 0644)
}

// ScanActiveVetoes returns all active reviewer vetoes across goals in rootDir.
func ScanActiveVetoes(rootDir string) ([]VetoRecord, error) {
	goalsDir := filepath.Join(rootDir, "goals")
	entries, err := os.ReadDir(goalsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var active []VetoRecord
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		v, err := CheckReviewerVeto(rootDir, e.Name())
		if err == nil && v != nil && v.Active {
			active = append(active, *v)
		}
	}
	return active, nil
}

// CheckRuleOfFiveCompletion runs Rule-of-Five verification for a candidate goal and staged/modified files.
func CheckRuleOfFiveCompletion(rootDir, goalSlug string, stagedFiles []string) (*RuleOfFiveReport, error) {
	if goalSlug == "" {
		return nil, nil
	}
	rep := VerifyRuleOfFive(rootDir, goalSlug, "", "", stagedFiles, RuleOfFiveOptions{
		AllowSkippedTests: true,
		AllowWarnings:     true,
	})
	return rep, nil
}

// BaselineRecord tracks the verified patch ID baseline for a goal.
type BaselineRecord struct {
	GoalSlug    string    `json:"goal_slug"`
	PatchID     string    `json:"patch_id"`
	BaseRef     string    `json:"base_ref"`
	RecordedAt  time.Time `json:"recorded_at"`
	CertifiedBy string    `json:"certified_by,omitempty"`
}

// RebaseDriftReport records the status of semantic rebase drift detection for a goal.
type RebaseDriftReport struct {
	GoalSlug       string `json:"goal_slug"`
	BaselinePatch  string `json:"baseline_patch"`
	CurrentPatch   string `json:"current_patch"`
	DriftDetected  bool   `json:"drift_detected"`
	ReTestRequired bool   `json:"re_test_required"`
	ReTestPassed   bool   `json:"re_test_passed"`
	Detail         string `json:"detail"`
}

// BaselinePatchPath returns the filepath where a goal's patch-id baseline is recorded.
func BaselinePatchPath(rootDir, goalSlug string) string {
	goalSlug = filepath.Clean(goalSlug)
	return filepath.Join(rootDir, "goals", goalSlug, "baseline.json")
}

// LoadBaselinePatch loads the baseline record for a goal, or returns nil if none exists.
func LoadBaselinePatch(rootDir, goalSlug string) (*BaselineRecord, error) {
	if goalSlug == "" {
		return nil, nil
	}
	p := BaselinePatchPath(rootDir, goalSlug)
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var rec BaselineRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, fmt.Errorf("parse baseline.json: %w", err)
	}
	return &rec, nil
}

// StoreBaselinePatch saves the baseline record for a goal.
func StoreBaselinePatch(rootDir, goalSlug, patchID, baseRef, certifiedBy string) (*BaselineRecord, error) {
	if goalSlug == "" {
		return nil, fmt.Errorf("goal slug is required")
	}
	rec := &BaselineRecord{
		GoalSlug:    goalSlug,
		PatchID:     patchID,
		BaseRef:     baseRef,
		RecordedAt:  time.Now().UTC(),
		CertifiedBy: certifiedBy,
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return nil, err
	}
	p := BaselinePatchPath(rootDir, goalSlug)
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(p, data, 0644); err != nil {
		return nil, err
	}
	return rec, nil
}

// TestRunner is a function type for running the test suite when re-testing is needed.
type TestRunner func(ctx context.Context, rootDir string) (bool, string, error)

// CheckRebaseDrift inspects whether the patch-id against trunk merge-base has drifted
// from the recorded baseline. If divergence occurs, it triggers testRunner to verify semantic safety.
func CheckRebaseDrift(ctx context.Context, rootDir, goalSlug, baseBranch string, testRunner TestRunner) (*RebaseDriftReport, error) {
	if goalSlug == "" {
		return &RebaseDriftReport{Detail: "no goal specified"}, nil
	}

	patchRes, err := git.ComputeTreePatchID(ctx, rootDir, baseBranch, true)
	if err != nil {
		return nil, fmt.Errorf("compute tree patch-id: %w", err)
	}

	if !patchRes.HasDiff {
		return &RebaseDriftReport{
			GoalSlug: goalSlug,
			Detail:   "clean worktree: no diff against merge base",
		}, nil
	}

	baseline, err := LoadBaselinePatch(rootDir, goalSlug)
	if err != nil {
		return nil, fmt.Errorf("load baseline patch: %w", err)
	}

	// 1. Initial baseline recording
	if baseline == nil || baseline.PatchID == "" {
		_, err := StoreBaselinePatch(rootDir, goalSlug, patchRes.PatchID, patchRes.BaseRef, "initial-certification")
		if err != nil {
			return nil, fmt.Errorf("store baseline patch: %w", err)
		}
		return &RebaseDriftReport{
			GoalSlug:      goalSlug,
			BaselinePatch: patchRes.PatchID,
			CurrentPatch:  patchRes.PatchID,
			DriftDetected: false,
			Detail:        "baseline patch-id recorded",
		}, nil
	}

	// 2. Exact match: no drift
	if baseline.PatchID == patchRes.PatchID {
		return &RebaseDriftReport{
			GoalSlug:      goalSlug,
			BaselinePatch: baseline.PatchID,
			CurrentPatch:  patchRes.PatchID,
			DriftDetected: false,
			Detail:        "patch-id matches baseline",
		}, nil
	}

	// 3. Drift detected: trigger re-test loop
	rep := &RebaseDriftReport{
		GoalSlug:       goalSlug,
		BaselinePatch:  baseline.PatchID,
		CurrentPatch:   patchRes.PatchID,
		DriftDetected:  true,
		ReTestRequired: true,
	}

	if testRunner == nil {
		testRunner = DefaultReTestRunner
	}

	passed, _, testErr := testRunner(ctx, rootDir)
	if passed && testErr == nil && ctx.Err() == nil {
		rep.ReTestPassed = true
		// Automatically refresh baseline on verified passing re-test
		_, err := StoreBaselinePatch(rootDir, goalSlug, patchRes.PatchID, patchRes.BaseRef, "re-test-certified")
		if err != nil {
			return nil, fmt.Errorf("store baseline patch: %w", err)
		}
		baseShort := baseline.PatchID
		if len(baseShort) > 12 {
			baseShort = baseShort[:12]
		}
		currShort := patchRes.PatchID
		if len(currShort) > 12 {
			currShort = currShort[:12]
		}
		rep.Detail = fmt.Sprintf("semantic rebase drift detected (%s -> %s); automated re-test passed, baseline updated", baseShort, currShort)
		return rep, nil
	}

	rep.ReTestPassed = false
	baseShort := baseline.PatchID
	if len(baseShort) > 12 {
		baseShort = baseShort[:12]
	}
	currShort := patchRes.PatchID
	if len(currShort) > 12 {
		currShort = currShort[:12]
	}
	errReason := testErr
	if errReason == nil && ctx.Err() != nil {
		errReason = ctx.Err()
	}
	if errReason == nil && passed {
		errReason = errors.New("inconsistent test runner output: passed=true with error")
	}
	if errReason != nil {
		rep.Detail = fmt.Sprintf("semantic rebase drift detected (%s -> %s); automated re-test failed: %v", baseShort, currShort, errReason)
	} else {
		rep.Detail = fmt.Sprintf("semantic rebase drift detected (%s -> %s); automated re-test failed", baseShort, currShort)
	}
	return rep, nil
}
