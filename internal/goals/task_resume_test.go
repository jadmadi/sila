package goals

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setupTestResumeRepo(t *testing.T, slug string) (string, *WorkPackage) {
	t.Helper()
	tempDir := t.TempDir()

	goalDir := filepath.Join(tempDir, "goals", slug)
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		t.Fatalf("create goal dir: %v", err)
	}

	// Create pinned source file
	sourceFile := filepath.Join(tempDir, "service.go")
	if err := os.WriteFile(sourceFile, []byte("package main\n\nfunc Run() {}\n"), 0644); err != nil {
		t.Fatalf("write source file: %v", err)
	}

	pkg := &WorkPackage{
		SchemaVersion: WorkPackageSchemaVersion,
		GoalSlug:      slug,
		Revision:      1,
		Purpose:       "Resume test purpose",
		Constraints:   []string{"No external calls"},
		NonGoals:      []string{"No UI changes"},
		Tasks: []TaskContract{
			{
				ID:      "T-001",
				Title:   "Implement resume task",
				Context: "Context for resume",
				Why:     "Why resume",
				Files: TaskFiles{
					Read:   []string{},
					Edit:   []string{"service.go"},
					Create: []string{},
					Forbid: []string{},
				},
				Steps: []Step{
					{ID: "S-001", Action: "Scaffold interfaces", Expected: "Interfaces ready", OnFailure: "Halt"},
					{ID: "S-002", Action: "Implement logic", Expected: "Logic ready", OnFailure: "Halt"},
					{ID: "S-003", Action: "Add verification", Expected: "Verification ready", OnFailure: "Halt"},
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
							{Kind: AssertionGoTestPass, Target: "github.com/jadmadi/sila/internal/goals/TestResume", Expected: true},
						},
					},
				},
				ReturnFormat: "sila/task-result/v1",
			},
		},
		Acceptance: []AcceptanceCase{
			{
				ID:            "AC-001",
				Requirement:   "Resume must work",
				Given:         "Clean repo",
				Action:        "Execute checks",
				Expected:      "Pass",
				RequiredScope: ScopeUnit,
				Mandatory:     true,
				CheckIDs:      []string{"C-001"},
			},
		},
		DependencyConditions: []DependencyCondition{},
		UnresolvedDecisions:  []string{},
		RetiredTaskIDs:       []string{},
		RetiredAcceptanceIDs: []string{},
	}

	if err := SaveWorkPackage(tempDir, slug, pkg); err != nil {
		t.Fatalf("SaveWorkPackage failed: %v", err)
	}

	return tempDir, pkg
}

// AC-007: A clean checkpoint yields the same next action after process restart.
func TestTaskResumePacket(t *testing.T) {
	slug := "test-resume-clean"
	taskID := "T-001"
	tempDir, pkg := setupTestResumeRepo(t, slug)

	specDigest, err := WorkPackageDigest(pkg)
	if err != nil {
		t.Fatalf("WorkPackageDigest failed: %v", err)
	}

	// Save clean checkpoint having finished S-001, next action is S-002
	cp := TaskCheckpoint{
		SchemaVersion:  TaskCheckpointSchemaVersion,
		GoalSlug:       slug,
		TaskID:         taskID,
		Sequence:       1,
		SpecDigest:     specDigest,
		CompletedSteps: []string{"S-001"},
		NextStep:       "S-002",
		Status:         TaskCheckpointStatusInProgress,
		Attempts: []CheckpointAttempt{
			{Executable: "go", Args: []string{"test"}, ExitCode: 0, FailureSignature: ""},
		},
	}
	if err := SaveTaskCheckpoint(tempDir, slug, cp); err != nil {
		t.Fatalf("SaveTaskCheckpoint failed: %v", err)
	}

	// Build resume packet
	packet, err := BuildTaskResumePacket(tempDir, slug, taskID)
	if err != nil {
		t.Fatalf("BuildTaskResumePacket failed: %v", err)
	}
	if packet == nil {
		t.Fatalf("expected non-nil packet")
	}

	if !packet.Executable {
		t.Errorf("expected clean packet to be executable, got diagnostics: %+v", packet.Diagnostics)
	}
	if packet.Resume == nil {
		t.Fatalf("expected packet.Resume to be populated")
	}
	if packet.Resume.NextStep != "S-002" {
		t.Errorf("expected next action S-002, got %q", packet.Resume.NextStep)
	}
	if len(packet.Resume.CompletedSteps) != 1 || packet.Resume.CompletedSteps[0] != "S-001" {
		t.Errorf("unexpected completed steps: %+v", packet.Resume.CompletedSteps)
	}
}

// AC-008: Changed source or revised spec yields an inspection-only packet with concrete differences.
func TestTaskResumeDrift(t *testing.T) {
	// Part A: Spec drift
	slugA := "test-resume-spec-drift"
	taskIDA := "T-001"
	tempDirA, pkgA := setupTestResumeRepo(t, slugA)

	specDigestA, _ := WorkPackageDigest(pkgA)

	cpA := TaskCheckpoint{
		SchemaVersion:  TaskCheckpointSchemaVersion,
		GoalSlug:       slugA,
		TaskID:         taskIDA,
		Sequence:       1,
		SpecDigest:     specDigestA,
		CompletedSteps: []string{"S-001"},
		NextStep:       "S-002",
		Status:         TaskCheckpointStatusInProgress,
	}
	if err := SaveTaskCheckpoint(tempDirA, slugA, cpA); err != nil {
		t.Fatalf("SaveTaskCheckpoint A: %v", err)
	}

	// Amend spec: revision 2 modifies purpose
	pkgA2 := *pkgA
	pkgA2.Revision = 2
	pkgA2.Purpose = "Amended purpose"
	if err := SaveWorkPackage(tempDirA, slugA, &pkgA2); err != nil {
		t.Fatalf("SaveWorkPackage A rev 2: %v", err)
	}

	packetA, err := BuildTaskResumePacket(tempDirA, slugA, taskIDA)
	if err != nil {
		t.Fatalf("BuildTaskResumePacket A should not error out on drift: %v", err)
	}
	if packetA == nil {
		t.Fatalf("expected inspection-only packet for spec drift")
	}
	if packetA.Executable {
		t.Errorf("expected packet to be non-executable on spec drift")
	}

	hasSpecDriftDiag := false
	for _, d := range packetA.Diagnostics {
		if d.Code == DiagPacketSpecStale {
			hasSpecDriftDiag = true
			if !strings.Contains(d.Message, "specification has drifted") {
				t.Errorf("unexpected spec drift diagnostic message: %s", d.Message)
			}
		}
	}
	if !hasSpecDriftDiag {
		t.Errorf("expected DiagPacketSpecStale diagnostic, got: %+v", packetA.Diagnostics)
	}

	// Part B: Source drift
	slugB := "test-resume-source-drift"
	taskIDB := "T-001"
	tempDirB, pkgB := setupTestResumeRepo(t, slugB)

	specDigestB, _ := WorkPackageDigest(pkgB)
	initialPacketB, err := BuildTaskPacket(tempDirB, slugB, taskIDB)
	if err != nil {
		t.Fatalf("BuildTaskPacket B: %v", err)
	}
	initialPacketDigestB, err := ComputeTaskPacketDigest(initialPacketB)
	if err != nil {
		t.Fatalf("ComputeTaskPacketDigest B: %v", err)
	}

	cpB := TaskCheckpoint{
		SchemaVersion:  TaskCheckpointSchemaVersion,
		GoalSlug:       slugB,
		TaskID:         taskIDB,
		Sequence:       1,
		SpecDigest:     specDigestB,
		PacketDigest:   initialPacketDigestB,
		CompletedSteps: []string{"S-001"},
		NextStep:       "S-002",
		Status:         TaskCheckpointStatusInProgress,
	}
	if err := SaveTaskCheckpoint(tempDirB, slugB, cpB); err != nil {
		t.Fatalf("SaveTaskCheckpoint B: %v", err)
	}

	// Modify pinned source file on disk
	sourceFileB := filepath.Join(tempDirB, "service.go")
	if err := os.WriteFile(sourceFileB, []byte("package main\n\n// MODIFIED CONTENT\nfunc Run() {}\n"), 0644); err != nil {
		t.Fatalf("modify source file: %v", err)
	}

	packetB, err := BuildTaskResumePacket(tempDirB, slugB, taskIDB)
	if err != nil {
		t.Fatalf("BuildTaskResumePacket B should not error out on source drift: %v", err)
	}
	if packetB.Executable {
		t.Errorf("expected packet to be non-executable on source drift")
	}

	hasSourceDriftDiag := false
	for _, d := range packetB.Diagnostics {
		if d.Code == DiagPacketSourceStale {
			hasSourceDriftDiag = true
		}
	}
	if !hasSourceDriftDiag {
		t.Errorf("expected DiagPacketSourceStale diagnostic, got: %+v", packetB.Diagnostics)
	}
}

// AC-009: A finished implementation with missing acceptance resumes at verification, not at an invented accepted state.
func TestTaskResumeBlocked(t *testing.T) {
	// Part A: Finished steps with missing acceptance -> resumes at verification
	slugA := "test-resume-verification"
	taskIDA := "T-001"
	tempDirA, pkgA := setupTestResumeRepo(t, slugA)

	specDigestA, _ := WorkPackageDigest(pkgA)

	// All implementation steps (S-001, S-002, S-003) completed, but acceptance unverified
	cpA := TaskCheckpoint{
		SchemaVersion:  TaskCheckpointSchemaVersion,
		GoalSlug:       slugA,
		TaskID:         taskIDA,
		Sequence:       1,
		SpecDigest:     specDigestA,
		CompletedSteps: []string{"S-001", "S-002", "S-003"},
		Status:         TaskCheckpointStatusInProgress,
	}
	if err := SaveTaskCheckpoint(tempDirA, slugA, cpA); err != nil {
		t.Fatalf("SaveTaskCheckpoint A: %v", err)
	}

	packetA, err := BuildTaskResumePacket(tempDirA, slugA, taskIDA)
	if err != nil {
		t.Fatalf("BuildTaskResumePacket A: %v", err)
	}

	if packetA.Resume.NextStep != "VERIFY_ACCEPTANCE" {
		t.Errorf("expected NextStep VERIFY_ACCEPTANCE, got %q", packetA.Resume.NextStep)
	}
	if packetA.Resume.Status != TaskCheckpointStatusImplementedUnverified {
		t.Errorf("expected status implemented_unverified, got %q", packetA.Resume.Status)
	}

	// Part B: Checkpoint is blocked -> packet is inspection-only with DiagTaskCheckpointBlocked
	slugB := "test-resume-blocked"
	taskIDB := "T-001"
	tempDirB, pkgB := setupTestResumeRepo(t, slugB)

	specDigestB, _ := WorkPackageDigest(pkgB)

	cpB := TaskCheckpoint{
		SchemaVersion:  TaskCheckpointSchemaVersion,
		GoalSlug:       slugB,
		TaskID:         taskIDB,
		Sequence:       1,
		SpecDigest:     specDigestB,
		CompletedSteps: []string{"S-001"},
		NextStep:       "S-002",
		Status:         TaskCheckpointStatusBlocked,
		Blocker: &CheckpointBlocker{
			Code:           "PLAN_ASSUMPTION_FAILED",
			Assumption:     "database schema exists",
			Observed:       "missing table users",
			DecisionNeeded: "approve database migration",
		},
	}
	if err := SaveTaskCheckpoint(tempDirB, slugB, cpB); err != nil {
		t.Fatalf("SaveTaskCheckpoint B: %v", err)
	}

	packetB, err := BuildTaskResumePacket(tempDirB, slugB, taskIDB)
	if err != nil {
		t.Fatalf("BuildTaskResumePacket B: %v", err)
	}
	if packetB.Executable {
		t.Errorf("expected blocked packet to be non-executable")
	}

	hasBlockedDiag := false
	for _, d := range packetB.Diagnostics {
		if d.Code == DiagTaskCheckpointBlocked {
			hasBlockedDiag = true
			if !strings.Contains(d.Message, "missing table users") {
				t.Errorf("expected diagnostic to cite blocker, got %s", d.Message)
			}
		}
	}
	if !hasBlockedDiag {
		t.Errorf("expected DiagTaskCheckpointBlocked diagnostic, got: %+v", packetB.Diagnostics)
	}
}
