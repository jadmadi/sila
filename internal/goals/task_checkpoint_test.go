package goals

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// AC-001: A fresh process loads the exact next step and failed attempts without transcript access.
func TestTaskCheckpointResume(t *testing.T) {
	tempDir := t.TempDir()
	slug := "test-resume-goal"
	taskID := "T-001"

	goalDir := filepath.Join(tempDir, "goals", slug)
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		t.Fatalf("create goal dir: %v", err)
	}

	checkpoint := TaskCheckpoint{
		SchemaVersion:   TaskCheckpointSchemaVersion,
		GoalSlug:        slug,
		TaskID:          taskID,
		Sequence:        1,
		SpecDigest:      "1111222233334444555566667777888899990000aaaabbbbccccddddeeeeffff",
		PacketDigest:    "aaaabbbbccccddddeeeeffff1111222233334444555566667777888899990000",
		CandidateDigest: "99990000aaaabbbbccccddddeeeeffff11112222333344445555666677778888",
		CompletedSteps:  []string{"S-001", "S-002"},
		EvidencePaths:   []string{"goals/" + slug + "/evidence/" + taskID + "/att-1/receipt.json"},
		Attempts: []CheckpointAttempt{
			{
				Executable:       "go",
				Args:             []string{"test", "./internal/goals", "-run", "TestSomething"},
				ExitCode:         1,
				FailureSignature: "FAIL_ASSERTION_MISMATCH",
			},
		},
		ChangedFiles: []string{"internal/goals/task_checkpoint.go"},
		NextStep:     "S-003",
		Status:       TaskCheckpointStatusInProgress,
		Blocker:      nil,
	}

	if err := SaveTaskCheckpoint(tempDir, slug, checkpoint); err != nil {
		t.Fatalf("SaveTaskCheckpoint failed: %v", err)
	}

	// Load checkpoint in a fresh call simulating a separate process
	loaded, err := LoadTaskCheckpoint(tempDir, slug, taskID)
	if err != nil {
		t.Fatalf("LoadTaskCheckpoint failed: %v", err)
	}
	if loaded == nil {
		t.Fatalf("expected non-nil checkpoint")
	}

	if loaded.NextStep != "S-003" {
		t.Errorf("expected NextStep S-003, got %q", loaded.NextStep)
	}
	if len(loaded.CompletedSteps) != 2 || loaded.CompletedSteps[0] != "S-001" || loaded.CompletedSteps[1] != "S-002" {
		t.Errorf("unexpected CompletedSteps: %+v", loaded.CompletedSteps)
	}
	if len(loaded.Attempts) != 1 {
		t.Fatalf("expected 1 attempt, got %d", len(loaded.Attempts))
	}
	att := loaded.Attempts[0]
	if att.Executable != "go" || att.ExitCode != 1 || att.FailureSignature != "FAIL_ASSERTION_MISMATCH" {
		t.Errorf("unexpected attempt fields: %+v", att)
	}
	if len(att.Args) != 4 || att.Args[0] != "test" {
		t.Errorf("unexpected attempt args: %+v", att.Args)
	}
	if loaded.Status != TaskCheckpointStatusInProgress {
		t.Errorf("expected status in_progress, got %q", loaded.Status)
	}
	if loaded.Sequence != 1 {
		t.Errorf("expected sequence 1, got %d", loaded.Sequence)
	}
	if loaded.SpecDigest != checkpoint.SpecDigest || loaded.PacketDigest != checkpoint.PacketDigest || loaded.CandidateDigest != checkpoint.CandidateDigest {
		t.Errorf("digest mismatch in loaded checkpoint")
	}
	if len(loaded.ChangedFiles) != 1 || loaded.ChangedFiles[0] != "internal/goals/task_checkpoint.go" {
		t.Errorf("unexpected changed files: %+v", loaded.ChangedFiles)
	}
}

// AC-003: Stale sequence writes fail without overwriting newer work; truncated latest pointers recover from the last valid immutable checkpoint with a diagnostic.
func TestTaskCheckpointConflict(t *testing.T) {
	tempDir := t.TempDir()
	slug := "test-conflict-goal"
	taskID := "T-001"

	goalDir := filepath.Join(tempDir, "goals", slug)
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		t.Fatalf("create goal dir: %v", err)
	}

	// 1. Initial checkpoint must have sequence 1; non-1 fails
	badInitial := TaskCheckpoint{
		SchemaVersion: TaskCheckpointSchemaVersion,
		GoalSlug:      slug,
		TaskID:        taskID,
		Sequence:      2,
		Status:        TaskCheckpointStatusInProgress,
	}
	if err := SaveTaskCheckpoint(tempDir, slug, badInitial); err == nil || !strings.Contains(err.Error(), "sequence conflict") {
		t.Fatalf("expected sequence conflict for initial non-1 sequence, got: %v", err)
	}

	// 2. Save sequence 1
	cp1 := TaskCheckpoint{
		SchemaVersion: TaskCheckpointSchemaVersion,
		GoalSlug:      slug,
		TaskID:        taskID,
		Sequence:      1,
		Status:        TaskCheckpointStatusInProgress,
		NextStep:      "S-001",
		Attempts: []CheckpointAttempt{
			{Executable: "go", Args: []string{"test"}, ExitCode: 1, FailureSignature: "FAIL_1"},
		},
	}
	if err := SaveTaskCheckpoint(tempDir, slug, cp1); err != nil {
		t.Fatalf("SaveTaskCheckpoint seq 1 failed: %v", err)
	}

	// 3. Stale sequence write (re-saving sequence 1) fails
	if err := SaveTaskCheckpoint(tempDir, slug, cp1); err == nil || !strings.Contains(err.Error(), "sequence conflict") {
		t.Fatalf("expected sequence conflict for stale write seq 1, got: %v", err)
	}

	// 4. Sequence gap (saving sequence 3 instead of 2) fails
	cp3Skip := TaskCheckpoint{
		SchemaVersion: TaskCheckpointSchemaVersion,
		GoalSlug:      slug,
		TaskID:        taskID,
		Sequence:      3,
		Status:        TaskCheckpointStatusInProgress,
		NextStep:      "S-002",
		Attempts: []CheckpointAttempt{
			{Executable: "go", Args: []string{"test"}, ExitCode: 1, FailureSignature: "FAIL_1"},
			{Executable: "go", Args: []string{"build"}, ExitCode: 0, FailureSignature: ""},
		},
	}
	if err := SaveTaskCheckpoint(tempDir, slug, cp3Skip); err == nil || !strings.Contains(err.Error(), "sequence conflict") {
		t.Fatalf("expected sequence conflict for skipped sequence 3, got: %v", err)
	}

	// 5. Append-only violation: modifying historical attempt fails
	cp2Mutated := TaskCheckpoint{
		SchemaVersion: TaskCheckpointSchemaVersion,
		GoalSlug:      slug,
		TaskID:        taskID,
		Sequence:      2,
		Status:        TaskCheckpointStatusInProgress,
		NextStep:      "S-002",
		Attempts: []CheckpointAttempt{
			{Executable: "go", Args: []string{"test-modified"}, ExitCode: 1, FailureSignature: "FAIL_1"},
		},
	}
	if err := SaveTaskCheckpoint(tempDir, slug, cp2Mutated); err == nil || !strings.Contains(err.Error(), "append-only violation") {
		t.Fatalf("expected append-only violation when mutating history, got: %v", err)
	}

	// 6. Save valid sequence 2
	cp2 := TaskCheckpoint{
		SchemaVersion: TaskCheckpointSchemaVersion,
		GoalSlug:      slug,
		TaskID:        taskID,
		Sequence:      2,
		Status:        TaskCheckpointStatusInProgress,
		NextStep:      "S-002",
		Attempts: []CheckpointAttempt{
			{Executable: "go", Args: []string{"test"}, ExitCode: 1, FailureSignature: "FAIL_1"},
			{Executable: "go", Args: []string{"build"}, ExitCode: 0, FailureSignature: ""},
		},
	}
	if err := SaveTaskCheckpoint(tempDir, slug, cp2); err != nil {
		t.Fatalf("SaveTaskCheckpoint seq 2 failed: %v", err)
	}

	// 7. Verify stale write does not overwrite sequence 2
	if err := SaveTaskCheckpoint(tempDir, slug, cp1); err == nil {
		t.Fatalf("expected error on saving stale sequence 1 over sequence 2")
	}

	loadedAfterStale, err := LoadTaskCheckpoint(tempDir, slug, taskID)
	if err != nil {
		t.Fatalf("load after stale write: %v", err)
	}
	if loadedAfterStale.Sequence != 2 || loadedAfterStale.NextStep != "S-002" {
		t.Fatalf("stale write corrupted sequence 2: %+v", loadedAfterStale)
	}

	// 8. Truncated latest pointer recovery
	latestPath := TaskCheckpointLatestPath(tempDir, slug, taskID)
	if err := os.WriteFile(latestPath, []byte{}, 0644); err != nil {
		t.Fatalf("truncate latest.json: %v", err)
	}

	recovered, diags, err := LoadTaskCheckpointWithDiagnostics(tempDir, slug, taskID)
	if err != nil {
		t.Fatalf("LoadTaskCheckpointWithDiagnostics after truncate failed: %v", err)
	}
	if recovered == nil || recovered.Sequence != 2 {
		t.Fatalf("failed to recover sequence 2, got: %+v", recovered)
	}
	if len(diags) == 0 || diags[0].Code != DiagCheckpointPointerRecovered {
		t.Fatalf("expected DiagCheckpointPointerRecovered diagnostic, got: %+v", diags)
	}

	// Verify latest.json was repaired on disk
	repairedData, err := os.ReadFile(latestPath)
	if err != nil || len(repairedData) == 0 {
		t.Fatalf("expected latest.json to be restored on disk: %v", err)
	}

	// LoadTaskCheckpoint should now succeed seamlessly
	loadedNormal, err := LoadTaskCheckpoint(tempDir, slug, taskID)
	if err != nil {
		t.Fatalf("LoadTaskCheckpoint failed after repair: %v", err)
	}
	if loadedNormal.Sequence != 2 {
		t.Errorf("expected sequence 2 after repair, got %d", loadedNormal.Sequence)
	}
}

// AC-002: Two identical failures require planner action; successful intervening progress resets the consecutive counter.
func TestTaskCheckpointCircuitBreaker(t *testing.T) {
	tempDir := t.TempDir()
	slug := "test-breaker-goal"
	taskID := "T-001"

	goalDir := filepath.Join(tempDir, "goals", slug)
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		t.Fatalf("create goal dir: %v", err)
	}

	specDigest := "222233334444555566667777888899990000aaaabbbbccccddddeeeeffff1111"

	// 1. First failure -> status remains in_progress
	cp1 := TaskCheckpoint{
		SchemaVersion: TaskCheckpointSchemaVersion,
		GoalSlug:      slug,
		TaskID:        taskID,
		Sequence:      1,
		SpecDigest:    specDigest,
		Status:        TaskCheckpointStatusInProgress,
		NextStep:      "S-001",
		Attempts: []CheckpointAttempt{
			{Executable: "go", Args: []string{"test"}, ExitCode: 1, FailureSignature: "PANIC_NIL_POINTER"},
		},
	}
	if err := SaveTaskCheckpoint(tempDir, slug, cp1); err != nil {
		t.Fatalf("SaveTaskCheckpoint seq 1 failed: %v", err)
	}
	loaded1, err := LoadTaskCheckpoint(tempDir, slug, taskID)
	if err != nil || loaded1.Status != TaskCheckpointStatusInProgress {
		t.Fatalf("expected seq 1 in_progress, got status %q (err %v)", loaded1.Status, err)
	}

	// 2. Second consecutive failure with identical signature -> circuit breaker marks blocked
	cp2 := TaskCheckpoint{
		SchemaVersion: TaskCheckpointSchemaVersion,
		GoalSlug:      slug,
		TaskID:        taskID,
		Sequence:      2,
		SpecDigest:    specDigest,
		Status:        TaskCheckpointStatusInProgress, // Caller attempts in_progress
		NextStep:      "S-001",
		Attempts: []CheckpointAttempt{
			{Executable: "go", Args: []string{"test"}, ExitCode: 1, FailureSignature: "PANIC_NIL_POINTER"},
			{Executable: "go", Args: []string{"test"}, ExitCode: 1, FailureSignature: "PANIC_NIL_POINTER"},
		},
	}
	if err := SaveTaskCheckpoint(tempDir, slug, cp2); err != nil {
		t.Fatalf("SaveTaskCheckpoint seq 2 failed: %v", err)
	}
	loaded2, err := LoadTaskCheckpoint(tempDir, slug, taskID)
	if err != nil {
		t.Fatalf("LoadTaskCheckpoint seq 2 failed: %v", err)
	}
	if loaded2.Status != TaskCheckpointStatusBlocked {
		t.Errorf("expected circuit breaker to mark status blocked, got %q", loaded2.Status)
	}
	if loaded2.Blocker == nil || loaded2.Blocker.Code != "CIRCUIT_BREAKER_TRIGGERED" {
		t.Errorf("expected CIRCUIT_BREAKER_TRIGGERED blocker, got %+v", loaded2.Blocker)
	}

	// 3. Renamed attempt (different executable/args) with same failure signature does not bypass circuit breaker
	taskIDRenamed := "T-002"
	cpRenamed1 := TaskCheckpoint{
		SchemaVersion: TaskCheckpointSchemaVersion,
		GoalSlug:      slug,
		TaskID:        taskIDRenamed,
		Sequence:      1,
		SpecDigest:    specDigest,
		Status:        TaskCheckpointStatusInProgress,
		NextStep:      "S-001",
		Attempts: []CheckpointAttempt{
			{Executable: "go", Args: []string{"test"}, ExitCode: 1, FailureSignature: "PANIC_DEADLOCK"},
		},
	}
	if err := SaveTaskCheckpoint(tempDir, slug, cpRenamed1); err != nil {
		t.Fatalf("SaveTaskCheckpoint renamed seq 1: %v", err)
	}

	cpRenamed2 := TaskCheckpoint{
		SchemaVersion: TaskCheckpointSchemaVersion,
		GoalSlug:      slug,
		TaskID:        taskIDRenamed,
		Sequence:      2,
		SpecDigest:    specDigest,
		Status:        TaskCheckpointStatusInProgress,
		NextStep:      "S-001",
		Attempts: []CheckpointAttempt{
			{Executable: "go", Args: []string{"test"}, ExitCode: 1, FailureSignature: "PANIC_DEADLOCK"},
			{Executable: "gotest", Args: []string{"test", "-v", "--timeout=10s"}, ExitCode: 1, FailureSignature: "PANIC_DEADLOCK"},
		},
	}
	if err := SaveTaskCheckpoint(tempDir, slug, cpRenamed2); err != nil {
		t.Fatalf("SaveTaskCheckpoint renamed seq 2: %v", err)
	}
	loadedRenamed, err := LoadTaskCheckpoint(tempDir, slug, taskIDRenamed)
	if err != nil || loadedRenamed.Status != TaskCheckpointStatusBlocked {
		t.Errorf("renamed attempt with identical failure signature should trip circuit breaker, got status %q", loadedRenamed.Status)
	}

	// 4. Intervening successful attempt resets consecutive failure counter
	taskIDReset := "T-003"
	cpReset1 := TaskCheckpoint{
		SchemaVersion: TaskCheckpointSchemaVersion,
		GoalSlug:      slug,
		TaskID:        taskIDReset,
		Sequence:      1,
		SpecDigest:    specDigest,
		Status:        TaskCheckpointStatusInProgress,
		NextStep:      "S-001",
		Attempts: []CheckpointAttempt{
			{Executable: "go", Args: []string{"test"}, ExitCode: 1, FailureSignature: "ERR_IO"},
		},
	}
	if err := SaveTaskCheckpoint(tempDir, slug, cpReset1); err != nil {
		t.Fatalf("SaveTaskCheckpoint reset seq 1: %v", err)
	}

	// Intervening success
	cpReset2 := TaskCheckpoint{
		SchemaVersion: TaskCheckpointSchemaVersion,
		GoalSlug:      slug,
		TaskID:        taskIDReset,
		Sequence:      2,
		SpecDigest:    specDigest,
		Status:        TaskCheckpointStatusInProgress,
		NextStep:      "S-001",
		Attempts: []CheckpointAttempt{
			{Executable: "go", Args: []string{"test"}, ExitCode: 1, FailureSignature: "ERR_IO"},
			{Executable: "go", Args: []string{"build"}, ExitCode: 0, FailureSignature: ""},
		},
	}
	if err := SaveTaskCheckpoint(tempDir, slug, cpReset2); err != nil {
		t.Fatalf("SaveTaskCheckpoint reset seq 2: %v", err)
	}

	// Failure after intervening success -> should NOT trigger circuit breaker
	cpReset3 := TaskCheckpoint{
		SchemaVersion: TaskCheckpointSchemaVersion,
		GoalSlug:      slug,
		TaskID:        taskIDReset,
		Sequence:      3,
		SpecDigest:    specDigest,
		Status:        TaskCheckpointStatusInProgress,
		NextStep:      "S-001",
		Attempts: []CheckpointAttempt{
			{Executable: "go", Args: []string{"test"}, ExitCode: 1, FailureSignature: "ERR_IO"},
			{Executable: "go", Args: []string{"build"}, ExitCode: 0, FailureSignature: ""},
			{Executable: "go", Args: []string{"test"}, ExitCode: 1, FailureSignature: "ERR_IO"},
		},
	}
	if err := SaveTaskCheckpoint(tempDir, slug, cpReset3); err != nil {
		t.Fatalf("SaveTaskCheckpoint reset seq 3: %v", err)
	}
	loadedReset, err := LoadTaskCheckpoint(tempDir, slug, taskIDReset)
	if err != nil || loadedReset.Status != TaskCheckpointStatusInProgress {
		t.Errorf("intervening success should have reset counter, got status: %q", loadedReset.Status)
	}

	// 5. Intervening completed step progress resets consecutive failure counter
	taskIDStep := "T-004"
	cpStep1 := TaskCheckpoint{
		SchemaVersion:  TaskCheckpointSchemaVersion,
		GoalSlug:       slug,
		TaskID:         taskIDStep,
		Sequence:       1,
		SpecDigest:     specDigest,
		Status:         TaskCheckpointStatusInProgress,
		CompletedSteps: []string{},
		NextStep:       "S-001",
		Attempts: []CheckpointAttempt{
			{Executable: "go", Args: []string{"test"}, ExitCode: 1, FailureSignature: "ERR_STEP"},
		},
	}
	if err := SaveTaskCheckpoint(tempDir, slug, cpStep1); err != nil {
		t.Fatalf("SaveTaskCheckpoint step seq 1: %v", err)
	}

	cpStep2 := TaskCheckpoint{
		SchemaVersion:  TaskCheckpointSchemaVersion,
		GoalSlug:       slug,
		TaskID:         taskIDStep,
		Sequence:       2,
		SpecDigest:     specDigest,
		Status:         TaskCheckpointStatusInProgress,
		CompletedSteps: []string{"S-001"}, // Intervening step completed
		NextStep:       "S-002",
		Attempts: []CheckpointAttempt{
			{Executable: "go", Args: []string{"test"}, ExitCode: 1, FailureSignature: "ERR_STEP"},
			{Executable: "go", Args: []string{"test"}, ExitCode: 1, FailureSignature: "ERR_STEP"},
		},
	}
	if err := SaveTaskCheckpoint(tempDir, slug, cpStep2); err != nil {
		t.Fatalf("SaveTaskCheckpoint step seq 2: %v", err)
	}
	loadedStep, err := LoadTaskCheckpoint(tempDir, slug, taskIDStep)
	if err != nil || loadedStep.Status != TaskCheckpointStatusInProgress {
		t.Errorf("intervening step completed should have reset counter, got status: %q", loadedStep.Status)
	}

	// 6. Planner action / materially changed input unblocks and resets circuit breaker
	// Resume T-001 with amended specDigest
	newSpecDigest := "33334444555566667777888899990000aaaabbbbccccddddeeeeffff11112222"
	cp3Amended := TaskCheckpoint{
		SchemaVersion: TaskCheckpointSchemaVersion,
		GoalSlug:      slug,
		TaskID:        taskID,
		Sequence:      3,
		SpecDigest:    newSpecDigest, // Material change by planner
		Status:        TaskCheckpointStatusInProgress,
		NextStep:      "S-001",
		Attempts: []CheckpointAttempt{
			{Executable: "go", Args: []string{"test"}, ExitCode: 1, FailureSignature: "PANIC_NIL_POINTER"},
			{Executable: "go", Args: []string{"test"}, ExitCode: 1, FailureSignature: "PANIC_NIL_POINTER"},
			{Executable: "go", Args: []string{"test", "-amended"}, ExitCode: 1, FailureSignature: "PANIC_NIL_POINTER"},
		},
	}
	if err := SaveTaskCheckpoint(tempDir, slug, cp3Amended); err != nil {
		t.Fatalf("SaveTaskCheckpoint seq 3 after planner amendment failed: %v", err)
	}
	loaded3, err := LoadTaskCheckpoint(tempDir, slug, taskID)
	if err != nil {
		t.Fatalf("LoadTaskCheckpoint seq 3 failed: %v", err)
	}
	if loaded3.Status != TaskCheckpointStatusInProgress {
		t.Errorf("planner amendment should reset circuit breaker, got status %q", loaded3.Status)
	}
}
