package goals

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func createTestWorkPackageForEvidence() *WorkPackage {
	pkg := &WorkPackage{
		SchemaVersion: WorkPackageSchemaVersion,
		GoalSlug:      "test-evidence-goal",
		Revision:      1,
		Purpose:       "Verify task evidence decoding and validation",
		Constraints:   []string{"pure-Go stdlib"},
		NonGoals:      []string{"no external deps"},
		Tasks: []TaskContract{
			{
				ID:        "T-001",
				Title:     "Build Unit Component",
				Context:   "Unit component context",
				Why:       "Foundational component",
				DependsOn: []string{},
				Files: TaskFiles{
					Read:   []string{"core.go"},
					Edit:   []string{"core.go"},
					Create: []string{},
					Forbid: []string{"research/*"},
				},
				References: []Reference{
					{
						Path:    "core.go",
						Symbol:  "Unit",
						Excerpt: "type Unit struct{}",
						SHA256:  strings.Repeat("a", 64),
					},
				},
				Decisions:     []Decision{},
				Steps:         []Step{{ID: "S-001", Action: "Build", Expected: "Success", OnFailure: "Halt"}},
				Interfaces:    []string{"type Unit struct{}"},
				Constraints:   []string{"zero alloc"},
				Examples:      []Example{},
				AcceptanceIDs: []string{"AC-001", "AC-002"},
				Checks: []Check{
					{
						ID:               "C-001",
						Executable:       "go",
						Args:             []string{"test", "./..."},
						Cwd:              "cli",
						TimeoutSeconds:   30,
						ExpectedExitCode: 0,
						Scope:            ScopeUnit,
						Assertions: []Assertion{
							{Kind: AssertionGoTestPass, Target: "github.com/jadmadi/sila/cmd/sila/TestUnit", Expected: true},
						},
						Capability: CapabilityLocal,
					},
					{
						ID:               "C-002",
						Executable:       "curl",
						Args:             []string{"https://api.example.com/health"},
						Cwd:              "cli",
						TimeoutSeconds:   30,
						ExpectedExitCode: 0,
						Scope:            ScopeProvider,
						Assertions: []Assertion{
							{Kind: AssertionStdoutExact, Target: "stdout", Expected: "ok"},
						},
						Capability: CapabilityProvider,
					},
				},
				StopConditions: []string{"timeout"},
				ReturnFormat:   "sila/task-result/v1",
			},
		},
		Acceptance: []AcceptanceCase{
			{
				ID:            "AC-001",
				Requirement:   "Unit tests pass",
				Given:         "Code base",
				Action:        "Run unit tests",
				Expected:      "Pass",
				RequiredScope: ScopeUnit,
				Mandatory:     true,
				CheckIDs:      []string{"C-001"},
			},
			{
				ID:            "AC-002",
				Requirement:   "Provider endpoint responds",
				Given:         "Cloud API",
				Action:        "Probe API",
				Expected:      "Status ok",
				RequiredScope: ScopeProvider,
				Mandatory:     true,
				CheckIDs:      []string{"C-002"},
			},
		},
		DependencyConditions: []DependencyCondition{},
		UnresolvedDecisions:  []string{},
		RetiredTaskIDs:       []string{},
		RetiredAcceptanceIDs: []string{},
	}
	return pkg
}

func createValidTaskEvidence(pkg *WorkPackage) *TaskEvidence {
	specDigest, _ := WorkPackageDigest(pkg)
	exit0 := 0
	return &TaskEvidence{
		SchemaVersion: TaskEvidenceSchemaVersion,
		AttemptID:     "att-001",
		GoalSlug:      pkg.GoalSlug,
		TaskID:        "T-001",
		CaseID:        "AC-001",
		CheckID:       "C-001",
		SpecDigest:    specDigest,
		PacketDigest:  strings.Repeat("b", 64),
		Candidate: TaskCandidate{
			Commit:        strings.Repeat("c", 40),
			ContentDigest: strings.Repeat("d", 64),
			ToolVersions:  map[string]string{"go": "go1.26.0"},
			OS:            "linux",
			Arch:          "amd64",
		},
		Scope:      ScopeUnit,
		Outcome:    OutcomePass,
		Origin:     OriginRunner,
		Executable: "go",
		Args:       []string{"test", "./..."},
		Cwd:        "cli",
		ExitCode:   &exit0,
		Assertions: []AssertionObservation{
			{
				Kind:     AssertionGoTestPass,
				Target:   "github.com/jadmadi/sila/cmd/sila/TestUnit",
				Expected: true,
				Observed: true,
				Passed:   true,
			},
		},
		Artifacts: []EvidenceArtifact{
			{
				Path:   "coverage.out",
				SHA256: strings.Repeat("e", 64),
			},
		},
		StartedAt: time.Now().Add(-10 * time.Second),
		EndedAt:   time.Now(),
	}
}

func TestTaskEvidenceDecode(t *testing.T) {
	pkg := createTestWorkPackageForEvidence()
	ev := createValidTaskEvidence(pkg)

	raw, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal valid evidence: %v", err)
	}

	// 1. Valid decoding
	decoded, err := DecodeTaskEvidence(raw)
	if err != nil {
		t.Fatalf("DecodeTaskEvidence valid: %v", err)
	}
	if decoded.GoalSlug != ev.GoalSlug || decoded.TaskID != ev.TaskID {
		t.Errorf("decoded fields mismatch: got slug=%q task=%q", decoded.GoalSlug, decoded.TaskID)
	}

	// 2. Reject unknown fields
	var rawMap map[string]any
	if err := json.Unmarshal(raw, &rawMap); err != nil {
		t.Fatal(err)
	}
	rawMap["unexpected_field"] = "bad"
	unknownBytes, _ := json.Marshal(rawMap)
	if _, err := DecodeTaskEvidence(unknownBytes); err == nil {
		t.Errorf("expected error decoding payload with unknown field")
	}

	// 3. Reject duplicate keys
	dupJSON := `{"schema_version":"` + TaskEvidenceSchemaVersion + `","schema_version":"` + TaskEvidenceSchemaVersion + `"}`
	if _, err := DecodeTaskEvidence([]byte(dupJSON)); err == nil {
		t.Errorf("expected error decoding payload with duplicate keys")
	}

	// 4. Reject trailing data
	trailingJSON := string(raw) + "\nextra content"
	if _, err := DecodeTaskEvidence([]byte(trailingJSON)); err == nil {
		t.Errorf("expected error decoding payload with trailing data")
	}

	// 5. Reject invalid schema version
	rawMap = nil
	_ = json.Unmarshal(raw, &rawMap)
	rawMap["schema_version"] = "sila/task-evidence/v2"
	badVersionBytes, _ := json.Marshal(rawMap)
	if _, err := DecodeTaskEvidence(badVersionBytes); err == nil {
		t.Errorf("expected error decoding payload with unsupported schema version")
	}
}

func TestTaskEvidenceCoverage(t *testing.T) {
	pkg := createTestWorkPackageForEvidence()

	// AC-001: Matching pass validates with zero error diagnostics
	ev := createValidTaskEvidence(pkg)
	diags := ValidateTaskEvidence(pkg, ev)
	if len(diags) != 0 {
		t.Fatalf("expected 0 diagnostics for valid matching pass, got %d: %+v", len(diags), diags)
	}

	// AC-001: Unknown case ID fails
	evBadCase := createValidTaskEvidence(pkg)
	evBadCase.CaseID = "AC-999"
	diags = ValidateTaskEvidence(pkg, evBadCase)
	foundCaseUnknown := false
	for _, d := range diags {
		if d.Code == DiagEvidenceCaseUnknown {
			foundCaseUnknown = true
			break
		}
	}
	if !foundCaseUnknown {
		t.Errorf("expected DiagEvidenceCaseUnknown for AC-999, got: %+v", diags)
	}

	// AC-001: Unknown check ID fails
	evBadCheck := createValidTaskEvidence(pkg)
	evBadCheck.CheckID = "C-999"
	diags = ValidateTaskEvidence(pkg, evBadCheck)
	foundCheckUnknown := false
	for _, d := range diags {
		if d.Code == DiagEvidenceCheckUnknown {
			foundCheckUnknown = true
			break
		}
	}
	if !foundCheckUnknown {
		t.Errorf("expected DiagEvidenceCheckUnknown for C-999, got: %+v", diags)
	}

	// AC-001: Unknown task ID fails
	evBadTask := createValidTaskEvidence(pkg)
	evBadTask.TaskID = "T-999"
	diags = ValidateTaskEvidence(pkg, evBadTask)
	foundTaskUnknown := false
	for _, d := range diags {
		if d.Code == DiagEvidenceTaskUnknown {
			foundTaskUnknown = true
			break
		}
	}
	if !foundTaskUnknown {
		t.Errorf("expected DiagEvidenceTaskUnknown for T-999, got: %+v", diags)
	}

	// AC-003: Empty assertions fails
	evEmptyAssertions := createValidTaskEvidence(pkg)
	evEmptyAssertions.Assertions = []AssertionObservation{}
	diags = ValidateTaskEvidence(pkg, evEmptyAssertions)
	foundEmptyAssertions := false
	for _, d := range diags {
		if d.Code == DiagEvidenceAssertionsEmpty {
			foundEmptyAssertions = true
			break
		}
	}
	if !foundEmptyAssertions {
		t.Errorf("expected DiagEvidenceAssertionsEmpty for empty assertions, got: %+v", diags)
	}

	// AC-003: Contradictory pass/nonzero-exit fails
	evNonZero := createValidTaskEvidence(pkg)
	code1 := 1
	evNonZero.ExitCode = &code1
	diags = ValidateTaskEvidence(pkg, evNonZero)
	foundContradictory := false
	for _, d := range diags {
		if d.Code == DiagEvidenceContradictoryExit {
			foundContradictory = true
			break
		}
	}
	if !foundContradictory {
		t.Errorf("expected DiagEvidenceContradictoryExit for pass with exit 1, got: %+v", diags)
	}

	// AC-003: Missing candidate identity fails
	evMissingCandidate := createValidTaskEvidence(pkg)
	evMissingCandidate.Candidate.Commit = ""
	diags = ValidateTaskEvidence(pkg, evMissingCandidate)
	foundMissingCandidate := false
	for _, d := range diags {
		if d.Code == DiagEvidenceCandidateMissing {
			foundMissingCandidate = true
			break
		}
	}
	if !foundMissingCandidate {
		t.Errorf("expected DiagEvidenceCandidateMissing for empty commit, got: %+v", diags)
	}

	// AC-003: Malformed artifact digest fails
	evBadArtifact := createValidTaskEvidence(pkg)
	evBadArtifact.Artifacts = []EvidenceArtifact{
		{Path: "test.out", SHA256: "not-a-valid-sha256"},
	}
	diags = ValidateTaskEvidence(pkg, evBadArtifact)
	foundBadArtifact := false
	for _, d := range diags {
		if d.Code == DiagEvidenceArtifactDigestInvalid {
			foundBadArtifact = true
			break
		}
	}
	if !foundBadArtifact {
		t.Errorf("expected DiagEvidenceArtifactDigestInvalid for invalid artifact digest, got: %+v", diags)
	}

	// Spec digest mismatch fails
	evBadSpec := createValidTaskEvidence(pkg)
	evBadSpec.SpecDigest = strings.Repeat("f", 64)
	diags = ValidateTaskEvidence(pkg, evBadSpec)
	foundSpecMismatch := false
	for _, d := range diags {
		if d.Code == DiagEvidenceSpecDigestMismatch {
			foundSpecMismatch = true
			break
		}
	}
	if !foundSpecMismatch {
		t.Errorf("expected DiagEvidenceSpecDigestMismatch for altered spec digest, got: %+v", diags)
	}
}

func TestTaskEvidenceScope(t *testing.T) {
	pkg := createTestWorkPackageForEvidence()

	// AC-002: A fixture receipt for a provider case returns EVIDENCE_SCOPE_MISMATCH even with exit 0
	evProviderCaseWithUnitScope := createValidTaskEvidence(pkg)
	evProviderCaseWithUnitScope.CaseID = "AC-002"
	evProviderCaseWithUnitScope.CheckID = "C-002"
	evProviderCaseWithUnitScope.Scope = ScopeUnit // Wrong scope! Case requires ScopeProvider
	exit0 := 0
	evProviderCaseWithUnitScope.ExitCode = &exit0

	diags := ValidateTaskEvidence(pkg, evProviderCaseWithUnitScope)
	foundScopeMismatch := false
	for _, d := range diags {
		if d.Code == DiagEvidenceScopeMismatch {
			foundScopeMismatch = true
			break
		}
	}
	if !foundScopeMismatch {
		t.Fatalf("AC-002 failed: expected EVIDENCE_SCOPE_MISMATCH for unit scope on provider case with exit 0, got: %+v", diags)
	}

	// Conversely, matching provider scope on provider case succeeds
	evProviderMatching := createValidTaskEvidence(pkg)
	evProviderMatching.CaseID = "AC-002"
	evProviderMatching.CheckID = "C-002"
	evProviderMatching.Scope = ScopeProvider
	evProviderMatching.ExitCode = &exit0
	evProviderMatching.Assertions = []AssertionObservation{
		{
			Kind:     AssertionStdoutExact,
			Target:   "stdout",
			Expected: "ok",
			Observed: "ok",
			Passed:   true,
		},
	}
	diagsMatching := ValidateTaskEvidence(pkg, evProviderMatching)
	for _, d := range diagsMatching {
		if d.Code == DiagEvidenceScopeMismatch {
			t.Errorf("unexpected DiagEvidenceScopeMismatch for matching provider scope: %+v", d)
		}
	}
}
