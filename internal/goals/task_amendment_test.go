package goals

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func createTestWorkPackage(slug string) *WorkPackage {
	return &WorkPackage{
		SchemaVersion: WorkPackageSchemaVersion,
		GoalSlug:      slug,
		Revision:      1,
		Purpose:       "Amendments test goal",
		Constraints:   []string{"No external network"},
		NonGoals:      []string{"No database changes"},
		Tasks: []TaskContract{
			{
				ID:      "T-001",
				Title:   "First Task",
				Context: "Context for T-001",
				Why:     "Why T-001",
				Files: TaskFiles{
					Edit:   []string{"internal/goals/task_amendment.go"},
					Create: []string{},
					Read:   []string{"internal/goals/task_checkpoint.go"},
					Forbid: []string{},
				},
				Steps: []Step{
					{ID: "S-001", Action: "Do initial step", Expected: "Success", OnFailure: "Halt"},
				},
				AcceptanceIDs: []string{"AC-001"},
				Checks: []Check{
					{
						ID:               "C-001",
						Executable:       "go",
						Args:             []string{"test", "./..."},
						Cwd:              "cli",
						TimeoutSeconds:   10,
						ExpectedExitCode: 0,
						Scope:            ScopeUnit,
						Capability:       CapabilityLocal,
						Assertions: []Assertion{
							{Kind: AssertionGoTestPass, Target: "github.com/jadmadi/sila/internal/goals/TestFoo", Expected: true},
						},
					},
				},
				ReturnFormat: "sila/task-result/v1",
			},
			{
				ID:      "T-002",
				Title:   "Second Task",
				Context: "Context for T-002",
				Why:     "Why T-002",
				Files: TaskFiles{
					Edit:   []string{"internal/goals/task_amendment.go"},
					Create: []string{},
					Read:   []string{},
					Forbid: []string{},
				},
				Steps: []Step{
					{ID: "S-001", Action: "Step 2", Expected: "Success", OnFailure: "Halt"},
				},
				AcceptanceIDs: []string{"AC-002"},
				Checks: []Check{
					{
						ID:               "C-002",
						Executable:       "go",
						Args:             []string{"test", "./..."},
						Cwd:              "cli",
						TimeoutSeconds:   5,
						ExpectedExitCode: 0,
						Scope:            ScopeUnit,
						Capability:       CapabilityLocal,
						Assertions: []Assertion{
							{Kind: AssertionGoTestPass, Target: "github.com/jadmadi/sila/internal/goals/TestBar", Expected: true},
						},
					},
				},
				ReturnFormat: "sila/task-result/v1",
			},
		},
		Acceptance: []AcceptanceCase{
			{
				ID:            "AC-001",
				Requirement:   "T-001 must pass",
				Given:         "Clean environment",
				Action:        "Run unit tests",
				Expected:      "Pass",
				RequiredScope: ScopeUnit,
				Mandatory:     true,
				CheckIDs:      []string{"C-001"},
			},
			{
				ID:            "AC-002",
				Requirement:   "T-002 must pass within timeout",
				Given:         "Clean environment",
				Action:        "Run timeout check",
				Expected:      "timeout 5s",
				RequiredScope: ScopeUnit,
				Mandatory:     true,
				CheckIDs:      []string{"C-002"},
			},
		},
		DependencyConditions: []DependencyCondition{},
		UnresolvedDecisions:  []string{},
		RetiredTaskIDs:       []string{},
		RetiredAcceptanceIDs: []string{},
	}
}

// AC-004: A changed expected result underdeclared as cosmetic rejects; a valid amendment increments revision once.
func TestTaskAmendmentImpact(t *testing.T) {
	tempDir := t.TempDir()
	slug := "test-impact-goal"

	// 1. Initial package revision 1
	pkg1 := createTestWorkPackage(slug)
	if err := SaveWorkPackage(tempDir, slug, pkg1); err != nil {
		t.Fatalf("SaveWorkPackage rev 1: %v", err)
	}

	oldDigest, err := WorkPackageDigest(pkg1)
	if err != nil {
		t.Fatalf("WorkPackageDigest: %v", err)
	}

	// 2. Prepare next package revision 2 modifying AC-002's Expected from 5s to 10s
	pkg2 := createTestWorkPackage(slug)
	pkg2.Revision = 2
	pkg2.Acceptance[1].Expected = "timeout 10s"

	newDigest, err := WorkPackageDigest(pkg2)
	if err != nil {
		t.Fatalf("WorkPackageDigest pkg2: %v", err)
	}

	// 3. Underdeclared amendment: lists Reason as cosmetic, but omits AC-002 from AffectedCaseIDs
	underdeclared := TaskAmendment{
		SchemaVersion:       TaskAmendmentSchemaVersion,
		GoalSlug:            slug,
		OldRevision:         1,
		NewRevision:         2,
		OldDigest:           oldDigest,
		NewDigest:           newDigest,
		Reason:              "cosmetic update",
		Authority:           "lead-planner",
		AffectedTaskIDs:     []string{},
		AffectedCaseIDs:     []string{}, // Omitted AC-002!
		RetainedReceipts:    []string{},
		InvalidatedReceipts: []string{},
		ApprovedAt:          time.Now().UTC().Format(time.RFC3339),
	}

	err = ApplyTaskAmendment(tempDir, slug, underdeclared, pkg2)
	if err == nil || !strings.Contains(err.Error(), "underdeclared impact") {
		t.Fatalf("expected underdeclared impact error for omitted AC-002, got: %v", err)
	}

	// Stored package must remain unchanged at revision 1
	stored1, err := LoadWorkPackage(tempDir, slug)
	if err != nil || stored1.Revision != 1 {
		t.Fatalf("stored work package was corrupted or modified on rejected amendment: %v (rev %d)", err, stored1.Revision)
	}

	// 4. Valid amendment declaring AC-002
	validAmendment := TaskAmendment{
		SchemaVersion:       TaskAmendmentSchemaVersion,
		GoalSlug:            slug,
		OldRevision:         1,
		NewRevision:         2,
		OldDigest:           oldDigest,
		NewDigest:           newDigest,
		Reason:              "Relax timeout from 5s to 10s for heavy runner",
		Authority:           "lead-planner",
		AffectedTaskIDs:     []string{},
		AffectedCaseIDs:     []string{"AC-002"},
		RetainedReceipts:    []string{"goals/" + slug + "/evidence/T-001/receipt.json"},
		InvalidatedReceipts: []string{"goals/" + slug + "/evidence/T-002/receipt.json"},
		ApprovedAt:          time.Now().UTC().Format(time.RFC3339),
	}

	if err := ApplyTaskAmendment(tempDir, slug, validAmendment, pkg2); err != nil {
		t.Fatalf("ApplyTaskAmendment failed for valid amendment: %v", err)
	}

	// Stored package must now be revision 2
	stored2, err := LoadWorkPackage(tempDir, slug)
	if err != nil {
		t.Fatalf("LoadWorkPackage after amendment: %v", err)
	}
	if stored2.Revision != 2 {
		t.Errorf("expected revision 2, got %d", stored2.Revision)
	}
	if stored2.Acceptance[1].Expected != "timeout 10s" {
		t.Errorf("expected modified expectation, got %q", stored2.Acceptance[1].Expected)
	}

	// Verify archived revision 1 and amendment record
	archivePath := filepath.Join(tempDir, "goals", slug, "amendments", "work-package-rev1.json")
	if !fileExists(archivePath) {
		t.Errorf("expected archive of revision 1 at %s", archivePath)
	}
	amendmentRecordPath := TaskAmendmentPath(tempDir, slug, 2)
	if !fileExists(amendmentRecordPath) {
		t.Errorf("expected amendment record at %s", amendmentRecordPath)
	}
}

// AC-005: Crash/failure during write leaves either the complete old revision or complete new revision, with recoverable archive state.
func TestTaskAmendmentAtomicity(t *testing.T) {
	tempDir := t.TempDir()
	slug := "test-atomicity-goal"

	pkg1 := createTestWorkPackage(slug)
	if err := SaveWorkPackage(tempDir, slug, pkg1); err != nil {
		t.Fatalf("SaveWorkPackage rev 1: %v", err)
	}

	oldDigest, _ := WorkPackageDigest(pkg1)

	// Build invalid next package (missing tasks)
	pkgBad := createTestWorkPackage(slug)
	pkgBad.Revision = 2
	pkgBad.Tasks = []TaskContract{} // Invalid: work package must contain at least one task

	newDigest, _ := WorkPackageDigest(pkgBad)

	amendment := TaskAmendment{
		SchemaVersion: TaskAmendmentSchemaVersion,
		GoalSlug:      slug,
		OldRevision:   1,
		NewRevision:   2,
		OldDigest:     oldDigest,
		NewDigest:     newDigest,
		Reason:        "remove all tasks",
		Authority:     "admin",
		ApprovedAt:    time.Now().UTC().Format(time.RFC3339),
	}

	err := ApplyTaskAmendment(tempDir, slug, amendment, pkgBad)
	if err == nil {
		t.Fatalf("expected failure for invalid next work package")
	}

	// Verify work-package.json on disk is completely untouched and decodable
	stored, err := LoadWorkPackage(tempDir, slug)
	if err != nil {
		t.Fatalf("stored work package corrupted after failed amendment: %v", err)
	}
	if stored.Revision != 1 || len(stored.Tasks) != 2 {
		t.Errorf("stored package state corrupted: %+v", stored)
	}
}

// AC-006: Removing then reusing T-003 rejects; unaffected acceptance remains historical and can be explicitly revalidated.
func TestTaskAmendmentIDRetention(t *testing.T) {
	tempDir := t.TempDir()
	slug := "test-retention-goal"

	// 1. Initial package with T-001, T-002, T-003
	pkg1 := createTestWorkPackage(slug)
	task3 := TaskContract{
		ID:      "T-003",
		Title:   "Third Task to be retired",
		Context: "Temporary task",
		Why:     "Why temporary",
		Files: TaskFiles{
			Edit:   []string{"internal/goals/task_amendment.go"},
			Create: []string{},
			Read:   []string{},
			Forbid: []string{},
		},
		Steps: []Step{
			{ID: "S-001", Action: "Action", Expected: "Result", OnFailure: "Halt"},
		},
		AcceptanceIDs: []string{"AC-003"},
		Checks: []Check{
			{
				ID:               "C-003",
				Executable:       "go",
				Args:             []string{"test", "./..."},
				Cwd:              "cli",
				TimeoutSeconds:   5,
				ExpectedExitCode: 0,
				Scope:            ScopeUnit,
				Capability:       CapabilityLocal,
				Assertions: []Assertion{
					{Kind: AssertionGoTestPass, Target: "github.com/jadmadi/sila/internal/goals/TestBaz", Expected: true},
				},
			},
		},
		ReturnFormat: "sila/task-result/v1",
	}
	pkg1.Tasks = append(pkg1.Tasks, task3)
	pkg1.Acceptance = append(pkg1.Acceptance, AcceptanceCase{
		ID:            "AC-003",
		Requirement:   "T-003 must pass",
		Given:         "Clean env",
		Action:        "Run check",
		Expected:      "Pass",
		RequiredScope: ScopeUnit,
		Mandatory:     true,
		CheckIDs:      []string{"C-003"},
	})

	if err := SaveWorkPackage(tempDir, slug, pkg1); err != nil {
		t.Fatalf("SaveWorkPackage rev 1: %v", err)
	}

	oldDigest, _ := WorkPackageDigest(pkg1)

	// 2. Remove T-003 without adding to retired lists -> must reject
	pkg2Bad := createTestWorkPackage(slug)
	pkg2Bad.Revision = 2
	newDigestBad, _ := WorkPackageDigest(pkg2Bad)

	amBad := TaskAmendment{
		SchemaVersion:       TaskAmendmentSchemaVersion,
		GoalSlug:            slug,
		OldRevision:         1,
		NewRevision:         2,
		OldDigest:           oldDigest,
		NewDigest:           newDigestBad,
		Reason:              "Delete T-003 without retirement",
		Authority:           "planner",
		AffectedTaskIDs:     []string{"T-003"},
		AffectedCaseIDs:     []string{"AC-003"},
		RetainedReceipts:    []string{},
		InvalidatedReceipts: []string{},
		ApprovedAt:          time.Now().UTC().Format(time.RFC3339),
	}

	err := ApplyTaskAmendment(tempDir, slug, amBad, pkg2Bad)
	if err == nil || !strings.Contains(err.Error(), "must be recorded in retired_task_ids") {
		t.Fatalf("expected error requiring T-003 in retired_task_ids, got: %v", err)
	}

	// 3. Remove T-003 and AC-003 with explicit retirement in retired lists -> succeeds
	pkg2Good := createTestWorkPackage(slug)
	pkg2Good.Revision = 2
	pkg2Good.RetiredTaskIDs = []string{"T-003"}
	pkg2Good.RetiredAcceptanceIDs = []string{"AC-003"}

	newDigestGood, _ := WorkPackageDigest(pkg2Good)
	amGood := TaskAmendment{
		SchemaVersion:       TaskAmendmentSchemaVersion,
		GoalSlug:            slug,
		OldRevision:         1,
		NewRevision:         2,
		OldDigest:           oldDigest,
		NewDigest:           newDigestGood,
		Reason:              "Retire T-003 and AC-003 properly",
		Authority:           "planner",
		AffectedTaskIDs:     []string{"T-003"},
		AffectedCaseIDs:     []string{"AC-003"},
		RetainedReceipts:    []string{"goals/" + slug + "/evidence/T-001/receipt.json"},
		InvalidatedReceipts: []string{"goals/" + slug + "/evidence/T-003/receipt.json"},
		ApprovedAt:          time.Now().UTC().Format(time.RFC3339),
	}

	if err := ApplyTaskAmendment(tempDir, slug, amGood, pkg2Good); err != nil {
		t.Fatalf("ApplyTaskAmendment with retirement failed: %v", err)
	}

	// 4. Try to re-use retired T-003 in revision 3 -> must reject
	pkg3Reused := createTestWorkPackage(slug)
	pkg3Reused.Revision = 3
	pkg3Reused.RetiredTaskIDs = []string{"T-003"}
	pkg3Reused.RetiredAcceptanceIDs = []string{"AC-003"}

	reusedTask := TaskContract{
		ID:      "T-003", // Reused!
		Title:   "Reincarnated T-003",
		Context: "Context",
		Why:     "Why",
		Files: TaskFiles{
			Edit:   []string{"internal/goals/task_amendment.go"},
			Create: []string{},
			Read:   []string{},
			Forbid: []string{},
		},
		Steps: []Step{
			{ID: "S-001", Action: "Do X", Expected: "Y", OnFailure: "Halt"},
		},
		AcceptanceIDs: []string{"AC-001"},
		Checks: []Check{
			{
				ID:               "C-001",
				Executable:       "go",
				Args:             []string{"test", "./..."},
				Cwd:              "cli",
				TimeoutSeconds:   10,
				ExpectedExitCode: 0,
				Scope:            ScopeUnit,
				Capability:       CapabilityLocal,
				Assertions: []Assertion{
					{Kind: AssertionGoTestPass, Target: "github.com/jadmadi/sila/internal/goals/TestFoo", Expected: true},
				},
			},
		},
		ReturnFormat: "sila/task-result/v1",
	}
	pkg3Reused.Tasks = append(pkg3Reused.Tasks, reusedTask)

	digest2, _ := WorkPackageDigest(pkg2Good)
	digest3, _ := WorkPackageDigest(pkg3Reused)

	amReuse := TaskAmendment{
		SchemaVersion:   TaskAmendmentSchemaVersion,
		GoalSlug:        slug,
		OldRevision:     2,
		NewRevision:     3,
		OldDigest:       digest2,
		NewDigest:       digest3,
		Reason:          "Attempt to reincarnate T-003",
		Authority:       "planner",
		AffectedTaskIDs: []string{"T-003"},
		ApprovedAt:      time.Now().UTC().Format(time.RFC3339),
	}

	err = ApplyTaskAmendment(tempDir, slug, amReuse, pkg3Reused)
	if err == nil || (!strings.Contains(err.Error(), "reused retired task id") && !strings.Contains(err.Error(), "retired")) {
		t.Fatalf("expected error rejecting reuse of retired task ID T-003, got: %v", err)
	}
}
