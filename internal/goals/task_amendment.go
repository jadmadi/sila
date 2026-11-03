package goals

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// TaskAmendmentSchemaVersion is the canonical schema version for task amendments.
const TaskAmendmentSchemaVersion = "sila/task-amendment/v1"

// TaskAmendment captures an explicit, versioned amendment to a goal's WorkPackage contract.
type TaskAmendment struct {
	SchemaVersion       string   `json:"schema_version"`
	GoalSlug            string   `json:"goal_slug"`
	OldRevision         int      `json:"old_revision"`
	NewRevision         int      `json:"new_revision"`
	OldDigest           string   `json:"old_digest"`
	NewDigest           string   `json:"new_digest"`
	Reason              string   `json:"reason"`
	Authority           string   `json:"authority"`
	AffectedTaskIDs     []string `json:"affected_task_ids"`
	AffectedCaseIDs     []string `json:"affected_case_ids"`
	RetainedReceipts    []string `json:"retained_receipts"`
	InvalidatedReceipts []string `json:"invalidated_receipts"`
	ApprovedAt          string   `json:"approved_at"`
}

// TaskAmendmentPath returns the path to an archived amendment record.
func TaskAmendmentPath(rootDir, slug string, revision int) string {
	return filepath.Join(resolveGoalsRootDir(rootDir), "goals", filepath.Clean(slug), "amendments", fmt.Sprintf("amendment-rev%d.json", revision))
}

// ValidateTaskAmendment checks structural constraints of a TaskAmendment.
func ValidateTaskAmendment(am *TaskAmendment) error {
	if am == nil {
		return fmt.Errorf("task amendment is nil")
	}
	if am.SchemaVersion != TaskAmendmentSchemaVersion {
		return fmt.Errorf("unsupported schema_version %q: expected %q", am.SchemaVersion, TaskAmendmentSchemaVersion)
	}
	if strings.TrimSpace(am.GoalSlug) == "" {
		return fmt.Errorf("missing goal_slug")
	}
	if !kebabCaseSlugRegex.MatchString(am.GoalSlug) {
		return fmt.Errorf("invalid goal_slug %q: must be kebab-case", am.GoalSlug)
	}
	if am.OldRevision < 1 {
		return fmt.Errorf("invalid old_revision %d: must be >= 1", am.OldRevision)
	}
	if am.NewRevision != am.OldRevision+1 {
		return fmt.Errorf("invalid new_revision %d: must be %d", am.NewRevision, am.OldRevision+1)
	}
	if len(am.OldDigest) != 64 {
		return fmt.Errorf("invalid old_digest: must be 64-char sha256 hex")
	}
	if len(am.NewDigest) != 64 {
		return fmt.Errorf("invalid new_digest: must be 64-char sha256 hex")
	}
	if strings.TrimSpace(am.Reason) == "" {
		return fmt.Errorf("missing amendment reason")
	}
	if strings.TrimSpace(am.Authority) == "" {
		return fmt.Errorf("missing amendment authority")
	}
	if strings.TrimSpace(am.ApprovedAt) == "" {
		return fmt.Errorf("missing approved_at timestamp")
	}
	if _, err := time.Parse(time.RFC3339, am.ApprovedAt); err != nil {
		return fmt.Errorf("invalid approved_at timestamp: %w", err)
	}

	return nil
}

// DecodeTaskAmendment decodes and validates a TaskAmendment strictly from JSON bytes.
func DecodeTaskAmendment(data []byte) (*TaskAmendment, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, fmt.Errorf("empty amendment data")
	}

	if err := checkDuplicateKeysAndEOF(data); err != nil {
		return nil, err
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()

	var am TaskAmendment
	if err := dec.Decode(&am); err != nil {
		return nil, fmt.Errorf("decode task amendment: %w", err)
	}

	if err := ValidateTaskAmendment(&am); err != nil {
		return nil, err
	}

	return &am, nil
}

// LoadTaskAmendment loads an archived amendment for the given goal revision.
func LoadTaskAmendment(rootDir, slug string, revision int) (*TaskAmendment, error) {
	p := TaskAmendmentPath(rootDir, slug, revision)
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("read task amendment %s: %w", p, err)
	}
	return DecodeTaskAmendment(data)
}

// ApplyTaskAmendment validates the amendment and atomically applies next *WorkPackage.
// It verifies old/new revisions and digests, recomputes changed tasks/cases, rejects underdeclared impact,
// preserves retired IDs, archives old package bytes, and atomically publishes the new revision.
func ApplyTaskAmendment(rootDir, slug string, amendment TaskAmendment, next *WorkPackage) error {
	if !kebabCaseSlugRegex.MatchString(slug) {
		return fmt.Errorf("invalid goal slug %q", slug)
	}
	if next == nil {
		return fmt.Errorf("nil next work package")
	}
	if next.GoalSlug != slug {
		return fmt.Errorf("goal_slug mismatch: next work package has %q, target is %q", next.GoalSlug, slug)
	}
	if amendment.GoalSlug == "" {
		amendment.GoalSlug = slug
	} else if amendment.GoalSlug != slug {
		return fmt.Errorf("goal_slug mismatch: amendment has %q, target is %q", amendment.GoalSlug, slug)
	}
	if amendment.SchemaVersion == "" {
		amendment.SchemaVersion = TaskAmendmentSchemaVersion
	}
	if amendment.ApprovedAt == "" {
		amendment.ApprovedAt = time.Now().UTC().Format(time.RFC3339)
	}
	if amendment.AffectedTaskIDs == nil {
		amendment.AffectedTaskIDs = []string{}
	}
	if amendment.AffectedCaseIDs == nil {
		amendment.AffectedCaseIDs = []string{}
	}
	if amendment.RetainedReceipts == nil {
		amendment.RetainedReceipts = []string{}
	}
	if amendment.InvalidatedReceipts == nil {
		amendment.InvalidatedReceipts = []string{}
	}

	rootDir = resolveGoalsRootDir(rootDir)
	goalDir := filepath.Join(rootDir, "goals", slug)

	unlock, err := acquireGoalLock(goalDir)
	if err != nil {
		return fmt.Errorf("acquire goal lock: %w", err)
	}
	defer unlock()

	// 1. Load current stored WorkPackage
	currentPkg, err := LoadWorkPackage(rootDir, slug)
	if err != nil {
		return fmt.Errorf("load current work package: %w", err)
	}

	// 2. Validate revision progression
	if amendment.OldRevision != currentPkg.Revision {
		return fmt.Errorf("revision conflict: amendment old_revision %d does not match current stored revision %d",
			amendment.OldRevision, currentPkg.Revision)
	}
	if amendment.NewRevision != currentPkg.Revision+1 {
		return fmt.Errorf("revision conflict: amendment new_revision %d must be %d",
			amendment.NewRevision, currentPkg.Revision+1)
	}
	if next.Revision != amendment.NewRevision {
		return fmt.Errorf("revision conflict: next work package revision %d does not match amendment new_revision %d",
			next.Revision, amendment.NewRevision)
	}

	// 3. Validate digest progression
	oldDigest, err := WorkPackageDigest(currentPkg)
	if err != nil {
		return fmt.Errorf("compute old package digest: %w", err)
	}
	if amendment.OldDigest != "" && amendment.OldDigest != oldDigest {
		return fmt.Errorf("digest conflict: amendment old_digest %s does not match computed old digest %s",
			amendment.OldDigest, oldDigest)
	}
	amendment.OldDigest = oldDigest

	newDigest, err := WorkPackageDigest(next)
	if err != nil {
		return fmt.Errorf("compute next package digest: %w", err)
	}
	if amendment.NewDigest != "" && amendment.NewDigest != newDigest {
		return fmt.Errorf("digest conflict: amendment new_digest %s does not match computed next digest %s",
			amendment.NewDigest, newDigest)
	}
	amendment.NewDigest = newDigest

	// 4. Validate next work package structure and path boundaries
	if err := ValidateWorkPackage(next); err != nil {
		return fmt.Errorf("validate next work package: %w", err)
	}
	if err := validatePackagePaths(rootDir, next); err != nil {
		return fmt.Errorf("validate next package paths: %w", err)
	}

	// 5. Recompute actual changes between currentPkg and next
	changedTasks := make(map[string]bool)
	deletedTasks := make(map[string]bool)
	oldTasksMap := make(map[string]*TaskContract)
	for i := range currentPkg.Tasks {
		oldTasksMap[currentPkg.Tasks[i].ID] = &currentPkg.Tasks[i]
	}
	newTasksMap := make(map[string]*TaskContract)
	for i := range next.Tasks {
		newTasksMap[next.Tasks[i].ID] = &next.Tasks[i]
	}

	for id, oldT := range oldTasksMap {
		newT, exists := newTasksMap[id]
		if !exists {
			changedTasks[id] = true
			deletedTasks[id] = true
		} else if !contractsEqual(oldT, newT) {
			changedTasks[id] = true
		}
	}
	for id := range newTasksMap {
		if _, exists := oldTasksMap[id]; !exists {
			changedTasks[id] = true
		}
	}

	changedCases := make(map[string]bool)
	deletedCases := make(map[string]bool)
	oldCasesMap := make(map[string]*AcceptanceCase)
	for i := range currentPkg.Acceptance {
		oldCasesMap[currentPkg.Acceptance[i].ID] = &currentPkg.Acceptance[i]
	}
	newCasesMap := make(map[string]*AcceptanceCase)
	for i := range next.Acceptance {
		newCasesMap[next.Acceptance[i].ID] = &next.Acceptance[i]
	}

	for id, oldC := range oldCasesMap {
		newC, exists := newCasesMap[id]
		if !exists {
			changedCases[id] = true
			deletedCases[id] = true
		} else if !acceptanceCasesEqual(oldC, newC) {
			changedCases[id] = true
		}
	}
	for id := range newCasesMap {
		if _, exists := oldCasesMap[id]; !exists {
			changedCases[id] = true
		}
	}

	// 6. AC-004: Reject underdeclared impact
	declaredTasks := make(map[string]bool)
	for _, id := range amendment.AffectedTaskIDs {
		declaredTasks[id] = true
	}
	declaredCases := make(map[string]bool)
	for _, id := range amendment.AffectedCaseIDs {
		declaredCases[id] = true
	}

	for taskID := range changedTasks {
		if !declaredTasks[taskID] {
			return fmt.Errorf("underdeclared impact: task %s was modified in work package but omitted from amendment affected_task_ids", taskID)
		}
	}
	for caseID := range changedCases {
		if !declaredCases[caseID] {
			return fmt.Errorf("underdeclared impact: acceptance case %s was modified in work package but omitted from amendment affected_case_ids", caseID)
		}
	}

	// 7. AC-006: Retired IDs retention and reuse prevention
	nextRetiredTasks := make(map[string]bool)
	for _, r := range next.RetiredTaskIDs {
		nextRetiredTasks[r] = true
	}
	for taskID := range deletedTasks {
		if !nextRetiredTasks[taskID] {
			return fmt.Errorf("deleted task %s must be recorded in retired_task_ids", taskID)
		}
	}
	for _, r := range currentPkg.RetiredTaskIDs {
		if !nextRetiredTasks[r] {
			return fmt.Errorf("previously retired task %s was omitted from next retired_task_ids", r)
		}
	}

	nextRetiredCases := make(map[string]bool)
	for _, r := range next.RetiredAcceptanceIDs {
		nextRetiredCases[r] = true
	}
	for caseID := range deletedCases {
		if !nextRetiredCases[caseID] {
			return fmt.Errorf("deleted acceptance case %s must be recorded in retired_acceptance_ids", caseID)
		}
	}
	for _, r := range currentPkg.RetiredAcceptanceIDs {
		if !nextRetiredCases[r] {
			return fmt.Errorf("previously retired acceptance case %s was omitted from next retired_acceptance_ids", r)
		}
	}

	// 8. Revalidate retained receipts: receipts for changed cases cannot be retained
	for _, recPath := range amendment.RetainedReceipts {
		for caseID := range changedCases {
			if strings.Contains(recPath, caseID) {
				return fmt.Errorf("cannot retain receipt %s for modified acceptance case %s without fresh execution", recPath, caseID)
			}
		}
	}

	// Validate complete amendment struct
	if err := ValidateTaskAmendment(&amendment); err != nil {
		return fmt.Errorf("validate amendment: %w", err)
	}

	// 9. AC-005: Archive previous contract bytes before publishing next revision
	amendmentsDir := filepath.Join(goalDir, "amendments")
	if err := os.MkdirAll(amendmentsDir, 0755); err != nil {
		return fmt.Errorf("create amendments directory: %w", err)
	}

	wpPath := WorkPackagePath(rootDir, slug)
	currentData, err := os.ReadFile(wpPath)
	if err != nil {
		return fmt.Errorf("read current work-package.json: %w", err)
	}

	archivePkgPath := filepath.Join(amendmentsDir, fmt.Sprintf("work-package-rev%d.json", currentPkg.Revision))
	if err := AtomicWriteFile(archivePkgPath, currentData, 0644); err != nil {
		return fmt.Errorf("archive previous work package: %w", err)
	}

	amendmentData, err := json.MarshalIndent(amendment, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal amendment record: %w", err)
	}
	amendmentData = append(amendmentData, '\n')
	amendmentRecordPath := filepath.Join(amendmentsDir, fmt.Sprintf("amendment-rev%d.json", next.Revision))
	if err := AtomicWriteFile(amendmentRecordPath, amendmentData, 0644); err != nil {
		return fmt.Errorf("archive amendment record: %w", err)
	}

	// Invalidate stale spec-review.json if present
	specReviewPath := filepath.Join(goalDir, "spec-review.json")
	if fileExists(specReviewPath) {
		if srData, err := os.ReadFile(specReviewPath); err == nil {
			archiveSRPath := filepath.Join(amendmentsDir, fmt.Sprintf("spec-review-rev%d.json", currentPkg.Revision))
			_ = AtomicWriteFile(archiveSRPath, srData, 0644)
			_ = os.Remove(specReviewPath)
		}
	}

	// 10. Atomically publish next work-package.json
	nextData, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal next work package: %w", err)
	}
	nextData = append(nextData, '\n')

	if err := AtomicWriteFile(wpPath, nextData, 0644); err != nil {
		// Roll back by restoring previous package bytes
		_ = AtomicWriteFile(wpPath, currentData, 0644)
		return fmt.Errorf("atomic write next work package: %w", err)
	}

	return nil
}

func contractsEqual(a, b *TaskContract) bool {
	aBytes, errA := json.Marshal(a)
	bBytes, errB := json.Marshal(b)
	if errA != nil || errB != nil {
		return false
	}
	return bytes.Equal(aBytes, bBytes)
}

func acceptanceCasesEqual(a, b *AcceptanceCase) bool {
	aBytes, errA := json.Marshal(a)
	bBytes, errB := json.Marshal(b)
	if errA != nil || errB != nil {
		return false
	}
	return bytes.Equal(aBytes, bBytes)
}
