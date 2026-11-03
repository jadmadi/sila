package goals

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTaskEvidencePersistence(t *testing.T) {
	dir := t.TempDir()
	slug := "persistence-goal"

	// 1. Save attempt 1 (Pass)
	ev1 := &TaskEvidence{
		SchemaVersion: TaskEvidenceSchemaVersion,
		AttemptID:     "att-20261012-001",
		GoalSlug:      slug,
		TaskID:        "T-001",
		CaseID:        "AC-001",
		CheckID:       "C-001",
		SpecDigest:    strings.Repeat("1", 64),
		PacketDigest:  strings.Repeat("2", 64),
		Candidate: TaskCandidate{
			Commit:        strings.Repeat("a", 40),
			ContentDigest: strings.Repeat("b", 64),
			ToolVersions:  map[string]string{"go": "go1.26.0"},
			OS:            "linux",
			Arch:          "amd64",
		},
		Scope:      ScopeUnit,
		Outcome:    OutcomePass,
		Origin:     OriginRunner,
		Executable: "go",
		Args:       []string{"test", "./..."},
		Cwd:        ".",
		Assertions: []AssertionObservation{
			{Kind: AssertionGoTestPass, Target: "TestFoo", Expected: true, Observed: true, Passed: true},
		},
		Artifacts: []EvidenceArtifact{},
		StartedAt: time.Now().Add(-5 * time.Second),
		EndedAt:   time.Now(),
	}

	if err := SaveTaskEvidence(dir, slug, ev1); err != nil {
		t.Fatalf("SaveTaskEvidence att-001: %v", err)
	}

	// Verify file was created at expected location
	receipt1Path := filepath.Join(dir, "goals", slug, "evidence", "T-001", "att-20261012-001", "receipt.json")
	if _, err := os.Stat(receipt1Path); err != nil {
		t.Fatalf("expected receipt file at %s: %v", receipt1Path, err)
	}

	// 2. AC-010: Attempt collision rejected (attempt ID cannot be overwritten)
	errCollision := SaveTaskEvidence(dir, slug, ev1)
	if errCollision == nil {
		t.Fatalf("expected error on attempt collision, got nil")
	}
	if !strings.Contains(errCollision.Error(), "attempt collision") {
		t.Errorf("unexpected error message on collision: %v", errCollision)
	}

	// 3. AC-010: Save attempt 2 (Fail) - both receipts retained
	ev2 := &TaskEvidence{
		SchemaVersion: TaskEvidenceSchemaVersion,
		AttemptID:     "att-20261012-002",
		GoalSlug:      slug,
		TaskID:        "T-001",
		CaseID:        "AC-001",
		CheckID:       "C-001",
		SpecDigest:    strings.Repeat("1", 64),
		PacketDigest:  strings.Repeat("2", 64),
		Candidate: TaskCandidate{
			Commit:        strings.Repeat("c", 40),
			ContentDigest: strings.Repeat("d", 64),
			ToolVersions:  map[string]string{"go": "go1.26.0"},
			OS:            "linux",
			Arch:          "amd64",
		},
		Scope:       ScopeUnit,
		Outcome:     OutcomeFail,
		Origin:      OriginRunner,
		Executable:  "go",
		Args:        []string{"test", "./..."},
		Cwd:         ".",
		Assertions:  []AssertionObservation{},
		Artifacts:   []EvidenceArtifact{},
		StartedAt:   time.Now().Add(-2 * time.Second),
		EndedAt:     time.Now(),
		FailureCode: "ASSERTION_FAILED",
	}

	if err := SaveTaskEvidence(dir, slug, ev2); err != nil {
		t.Fatalf("SaveTaskEvidence att-002: %v", err)
	}

	// Verify both attempt receipts exist independently on disk
	receipt2Path := filepath.Join(dir, "goals", slug, "evidence", "T-001", "att-20261012-002", "receipt.json")
	if _, err := os.Stat(receipt2Path); err != nil {
		t.Fatalf("expected receipt 2 file at %s: %v", receipt2Path, err)
	}

	// 4. ListTaskEvidenceAttempts returns both attempts
	attempts, err := ListTaskEvidenceAttempts(dir, slug, "T-001")
	if err != nil {
		t.Fatalf("ListTaskEvidenceAttempts: %v", err)
	}
	if len(attempts) != 2 || attempts[0] != "att-20261012-001" || attempts[1] != "att-20261012-002" {
		t.Fatalf("AC-010 failed: expected both attempts [att-20261012-001, att-20261012-002], got: %v", attempts)
	}

	// 5. LoadTaskEvidence decodes accurately
	loaded1, err := LoadTaskEvidence(dir, slug, "T-001", "att-20261012-001")
	if err != nil {
		t.Fatalf("LoadTaskEvidence att-001: %v", err)
	}
	if loaded1.AttemptID != ev1.AttemptID || loaded1.Outcome != OutcomePass {
		t.Errorf("loaded1 mismatch: %+v", loaded1)
	}

	loaded2, err := LoadTaskEvidence(dir, slug, "T-001", "att-20261012-002")
	if err != nil {
		t.Fatalf("LoadTaskEvidence att-002: %v", err)
	}
	if loaded2.AttemptID != ev2.AttemptID || loaded2.Outcome != OutcomeFail {
		t.Errorf("loaded2 mismatch: %+v", loaded2)
	}
}
