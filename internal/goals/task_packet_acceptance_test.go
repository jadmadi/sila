package goals

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func setupAcceptanceRepoFixture(t *testing.T, slug string) (string, *WorkPackage) {
	t.Helper()
	dir := t.TempDir()

	// Initialize git repo
	if err := exec.Command("git", "-C", dir, "init", "-b", "main").Run(); err != nil {
		t.Fatalf("git init: %v", err)
	}
	_ = exec.Command("git", "-C", dir, "config", "user.email", "sila@test.local").Run()
	_ = exec.Command("git", "-C", dir, "config", "user.name", "Sila Agent").Run()

	// Write basic source file
	initialSrc := "package fixture\n\nconst Version = \"1.0.0\"\n"
	if err := os.WriteFile(filepath.Join(dir, "fixture.go"), []byte(initialSrc), 0644); err != nil {
		t.Fatalf("write fixture.go: %v", err)
	}
	hSrc := sha256.Sum256([]byte(initialSrc))
	srcSHA := hex.EncodeToString(hSrc[:])

	// Create valid WorkPackage
	pkg := validWorkPackageFixture()
	pkg.GoalSlug = slug
	pkg.Purpose = "Verify prerequisite acceptance and artifact binding"
	pkg.Constraints = []string{"pure-Go stdlib", "zero external dependencies"}
	pkg.NonGoals = []string{"no network", "no telemetry"}
	pkg.UnresolvedDecisions = []string{}

	pkg.Tasks = []TaskContract{
		{
			ID:      "T-001",
			Title:   "Setup Engine Foundation",
			Context: "Initialize engine",
			Why:     "Foundation layer",
			Files: TaskFiles{
				Read:   []string{"fixture.go"},
				Edit:   []string{"fixture.go"},
				Create: []string{"engine_output.go"},
				Forbid: []string{"research/*"},
			},
			References: []Reference{
				{
					Path:    "fixture.go",
					Symbol:  "Version",
					Excerpt: "const Version = \"1.0.0\"",
					SHA256:  srcSHA,
				},
			},
			Decisions:     []Decision{},
			Steps:         []Step{{ID: "S-001", Action: "Create output", Expected: "Output created", OnFailure: "Halt"}},
			Interfaces:    []string{"const Version"},
			Constraints:   []string{"CGO_ENABLED=0"},
			Examples:      []Example{{Input: "Version", Output: "1.0.0", Explanation: "Base version"}},
			AcceptanceIDs: []string{"AC-001"},
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
						{
							Kind:     AssertionGoTestPass,
							Target:   "github.com/jadmadi/sila/internal/goals/TestTaskPacketAcceptedPrerequisites",
							Expected: true,
						},
					},
					Capability:  CapabilityLocal,
					Environment: map[string]string{"CGO_ENABLED": "0"},
				},
			},
			StopConditions: []string{"Any panic"},
			ReturnFormat:   "sila/task-result/v1",
		},
		{
			ID:        "T-002",
			Title:     "Add Engine Diagnostics",
			Context:   "Inspect engine diagnostics",
			Why:       "Monitoring diagnostics",
			DependsOn: []string{"T-001"},
			Files: TaskFiles{
				Read:   []string{"fixture.go"},
				Edit:   []string{"fixture.go"},
				Create: []string{"engine_diag.go"},
				Forbid: []string{"research/*"},
			},
			References: []Reference{
				{
					Path:    "fixture.go",
					Symbol:  "Version",
					Excerpt: "const Version = \"1.0.0\"",
					SHA256:  srcSHA,
				},
			},
			Decisions:     []Decision{},
			Steps:         []Step{{ID: "S-002", Action: "Add diag", Expected: "Diagnostics ready", OnFailure: "Halt"}},
			Interfaces:    []string{"const Version"},
			Constraints:   []string{"CGO_ENABLED=0"},
			Examples:      []Example{},
			AcceptanceIDs: []string{"AC-002"},
			Checks: []Check{
				{
					ID:               "C-002",
					Executable:       "go",
					Args:             []string{"test", "./..."},
					Cwd:              "cli",
					TimeoutSeconds:   30,
					ExpectedExitCode: 0,
					Scope:            ScopeUnit,
					Assertions: []Assertion{
						{
							Kind:     AssertionGoTestPass,
							Target:   "github.com/jadmadi/sila/internal/goals/TestTaskPacketAcceptedPrerequisites",
							Expected: true,
						},
					},
					Capability:  CapabilityLocal,
					Environment: map[string]string{"CGO_ENABLED": "0"},
				},
			},
			StopConditions: []string{"Any panic"},
			ReturnFormat:   "sila/task-result/v1",
		},
	}

	pkg.Acceptance = []AcceptanceCase{
		{
			ID:            "AC-001",
			Requirement:   "Engine foundation initialized",
			Given:         "Initial source",
			Action:        "Initialize",
			Expected:      "Clean initialization",
			RequiredScope: ScopeUnit,
			Mandatory:     true,
			CheckIDs:      []string{"C-001"},
		},
		{
			ID:            "AC-002",
			Requirement:   "Diagnostics initialized",
			Given:         "Foundation ready",
			Action:        "Inspect",
			Expected:      "Diagnostics ready",
			RequiredScope: ScopeUnit,
			Mandatory:     true,
			CheckIDs:      []string{"C-002"},
		},
	}

	// Save work package
	if err := SaveWorkPackage(dir, slug, pkg); err != nil {
		t.Fatalf("save work package: %v", err)
	}

	// Create valid SpecReview
	specDigest, err := WorkPackageDigest(pkg)
	if err != nil {
		t.Fatalf("compute spec digest: %v", err)
	}

	review := &SpecReview{
		SchemaVersion: SpecReviewSchemaVersion,
		SpecDigest:    specDigest,
		Reviewer:      "reviewer@test",
		ReviewedAt:    time.Now().UTC().Format(time.RFC3339),
		Verdict:       VerdictReady,
		Shortcuts: []ShortcutCounter{
			{
				IncorrectImplementation: "Skip validation",
				ExcludedByIDs:           []string{"AC-001"},
				Resolution:              "Always validate",
			},
			{
				IncorrectImplementation: "Mock checks",
				ExcludedByIDs:           []string{"AC-001"},
				Resolution:              "Run real checks",
			},
			{
				IncorrectImplementation: "Fake acceptance",
				ExcludedByIDs:           []string{"AC-001"},
				Resolution:              "Verify complete evidence",
			},
		},
		UnresolvedBlockers: []string{},
	}
	reviewPath := filepath.Join(dir, "goals", slug, "spec-review.json")
	_ = os.MkdirAll(filepath.Dir(reviewPath), 0755)
	revBytes, _ := json.MarshalIndent(review, "", "  ")
	if err := os.WriteFile(reviewPath, revBytes, 0644); err != nil {
		t.Fatalf("write spec review: %v", err)
	}

	// Commit initial state
	_ = exec.Command("git", "-C", dir, "add", ".").Run()
	_ = exec.Command("git", "-C", dir, "commit", "-m", "chore: initial fixture").Run()

	return dir, pkg
}

func TestTaskPacketAcceptedPrerequisites(t *testing.T) {
	slug := "acceptance-prereq-test"
	dir, pkg := setupAcceptanceRepoFixture(t, slug)

	// AC-015: Independent first task (T-001) builds an executable packet without prerequisites
	packet1, err := BuildTaskPacket(dir, slug, "T-001")
	if err != nil {
		t.Fatalf("BuildTaskPacket T-001: %v", err)
	}
	if !packet1.Executable {
		t.Errorf("AC-015 failed: expected T-001 to be executable, got diagnostics: %+v", packet1.Diagnostics)
	}
	if len(packet1.DependencyOutputs) != 0 {
		t.Errorf("AC-015 failed: expected empty dependency outputs for T-001, got %d", len(packet1.DependencyOutputs))
	}

	// AC-013: Dependent task (T-002) is blocked before T-001 is accepted
	packet2Before, err := BuildTaskPacket(dir, slug, "T-002")
	if err != nil {
		t.Fatalf("BuildTaskPacket T-002 before prerequisite acceptance: %v", err)
	}
	if packet2Before.Executable {
		t.Errorf("AC-013 failed: expected T-002 to be non-executable before T-001 is accepted")
	}
	hasIncompleteDiag := false
	for _, d := range packet2Before.Diagnostics {
		if d.Code == DiagAcceptanceIncomplete || d.Code == DiagAcceptanceNotAvailable {
			hasIncompleteDiag = true
			break
		}
	}
	if !hasIncompleteDiag {
		t.Errorf("AC-013 failed: expected ACCEPTANCE_INCOMPLETE or ACCEPTANCE_NOT_AVAILABLE diagnostic on T-002, got: %+v", packet2Before.Diagnostics)
	}

	// Now complete implementation and acceptance for T-001
	engineOutputContent := "package fixture\n\nfunc EngineOutput() string { return \"engine-v1\" }\n"
	if err := os.WriteFile(filepath.Join(dir, "engine_output.go"), []byte(engineOutputContent), 0644); err != nil {
		t.Fatalf("write engine_output.go: %v", err)
	}

	// Commit T-001 work so git repository is clean
	_ = exec.Command("git", "-C", dir, "add", ".").Run()
	_ = exec.Command("git", "-C", dir, "commit", "-m", "feat: implement T-001").Run()

	cand, err := CaptureTaskCandidate(dir, slug)
	if err != nil {
		t.Fatalf("capture candidate: %v", err)
	}
	candDigest := TaskCandidateDigest(*cand)
	specDigest, err := WorkPackageDigest(pkg)
	if err != nil {
		t.Fatalf("compute spec digest: %v", err)
	}

	// Create valid evidence receipt for AC-001
	evPath := filepath.Join(dir, "goals", slug, "evidence", "T-001", "c-001", "receipt.json")
	_ = os.MkdirAll(filepath.Dir(evPath), 0755)
	ev := TaskEvidence{
		SchemaVersion: TaskEvidenceSchemaVersion,
		AttemptID:     "att-T-001-AC-001",
		GoalSlug:      slug,
		TaskID:        "T-001",
		CaseID:        "AC-001",
		CheckID:       "C-001",
		SpecDigest:    specDigest,
		Candidate:     *cand,
		Scope:         ScopeUnit,
		Outcome:       OutcomePass,
		Origin:        OriginRunner,
		Executable:    "go",
		Args:          []string{"test", "./..."},
		Cwd:           "cli",
		ExitCode:      intPtr(0),
		Assertions: []AssertionObservation{
			{
				Kind:     AssertionGoTestPass,
				Target:   "github.com/jadmadi/sila/internal/goals/TestTaskPacketAcceptedPrerequisites",
				Expected: true,
				Observed: true,
				Passed:   true,
			},
		},
		Artifacts: []EvidenceArtifact{},
		StartedAt: time.Now().UTC().Add(-2 * time.Second),
		EndedAt:   time.Now().UTC(),
	}
	evData, _ := json.MarshalIndent(ev, "", "  ")
	if err := os.WriteFile(evPath, evData, 0644); err != nil {
		t.Fatalf("write evidence: %v", err)
	}

	relEvPath := filepath.Join("goals", slug, "evidence", "T-001", "c-001", "receipt.json")
	sel := ReceiptSelection{
		SchemaVersion:            ReceiptSelectionSchemaVersion,
		GoalSlug:                 slug,
		TaskID:                   "T-001",
		SpecDigest:               specDigest,
		CandidateDigest:          candDigest,
		ReceiptPaths:             []string{relEvPath},
		NewerFailureDispositions: []NewerFailureDisposition{},
	}

	rev := AcceptanceReview{
		SchemaVersion:   AcceptanceReviewSchemaVersion,
		Reviewer:        "reviewer@test",
		Producer:        "producer@test",
		CandidateDigest: candDigest,
		SpecDigest:      specDigest,
		Disposition:     "accept",
		CaseDispositions: []CaseDisposition{
			{
				CaseID:   "AC-001",
				Accepted: true,
				Reason:   "All unit checks verified",
			},
		},
		ReviewedAt: time.Now().UTC().Add(time.Second).Format(time.RFC3339),
	}

	// Evaluate acceptance
	accRes, err := EvaluateTaskAcceptance(dir, slug, "T-001", sel, rev)
	if err != nil {
		t.Fatalf("EvaluateTaskAcceptance: %v", err)
	}
	if !accRes.Accepted {
		t.Fatalf("expected T-001 acceptance to pass, got diagnostics: %+v", accRes.Diagnostics)
	}

	// Save task acceptance alongside selection and review envelopes
	if err := SaveTaskAcceptance(dir, slug, "T-001", accRes, &sel, &rev); err != nil {
		t.Fatalf("SaveTaskAcceptance: %v", err)
	}

	// AC-013: Build packet for T-002 now that T-001 is legitimately accepted
	packet2After, err := BuildTaskPacket(dir, slug, "T-002")
	if err != nil {
		t.Fatalf("BuildTaskPacket T-002 after acceptance: %v", err)
	}
	if !packet2After.Executable {
		t.Fatalf("AC-013 failed: expected T-002 to be executable after prerequisite acceptance, got diagnostics: %+v", packet2After.Diagnostics)
	}

	// Verify predecessor candidate/spec/artifact digests are bound in dependency_outputs
	if len(packet2After.DependencyOutputs) == 0 {
		t.Fatalf("AC-013 failed: expected non-empty DependencyOutputs in T-002 packet")
	}

	foundCandidateBound := false
	foundSpecBound := false
	foundArtifactBound := false
	for _, dep := range packet2After.DependencyOutputs {
		if dep.TaskID == "T-001" {
			if dep.Artifact == "candidate" && dep.SHA256 == candDigest {
				foundCandidateBound = true
			}
			if dep.Artifact == "spec" && dep.SHA256 == specDigest {
				foundSpecBound = true
			}
			if dep.Artifact == "engine_output.go" {
				foundArtifactBound = true
			}
		}
	}

	if !foundCandidateBound {
		t.Errorf("AC-013 failed: candidate digest %q not bound in DependencyOutputs", candDigest)
	}
	if !foundSpecBound {
		t.Errorf("AC-013 failed: spec digest %q not bound in DependencyOutputs", specDigest)
	}
	if !foundArtifactBound {
		t.Errorf("AC-013 failed: engine_output.go not bound in DependencyOutputs")
	}

	// ValidateTaskPacket must confirm zero error diagnostics
	diags := ValidateTaskPacket(dir, packet2After)
	for _, d := range diags {
		if d.Severity == "error" {
			t.Errorf("ValidateTaskPacket unexpected error diagnostic: [%s] %s", d.Code, d.Message)
		}
	}
}

func TestTaskPacketForgedAcceptance(t *testing.T) {
	slug := "forged-acceptance-test"
	dir, pkg := setupAcceptanceRepoFixture(t, slug)

	// Scenario A: Hand-authored forged accepted:true record without selection/review
	// AC-014: A forged accepted:true record keeps the dependent task blocked
	forgedRes := AcceptanceResult{
		SchemaVersion:   AcceptanceResultSchemaVersion,
		GoalSlug:        slug,
		TaskID:          "T-001",
		Accepted:        true,
		EvidenceStatus:  EvidenceStatusAccepted,
		SpecDigest:      "forged-spec-digest",
		CandidateDigest: "forged-cand-digest",
		ReceiptPaths:    []string{"goals/" + slug + "/evidence/T-001/fake/receipt.json"},
		MissingCaseIDs:  []string{},
		FailedCaseIDs:   []string{},
		Diagnostics:     []Diagnostic{},
		ReviewedAt:      time.Now().UTC().Format(time.RFC3339),
	}
	forgedPath := filepath.Join(dir, "goals", slug, "acceptance", "T-001.json")
	_ = os.MkdirAll(filepath.Dir(forgedPath), 0755)
	forgedData, _ := json.MarshalIndent(forgedRes, "", "  ")
	if err := os.WriteFile(forgedPath, forgedData, 0644); err != nil {
		t.Fatalf("write forged acceptance: %v", err)
	}

	packetForged, err := BuildTaskPacket(dir, slug, "T-002")
	if err != nil {
		t.Fatalf("BuildTaskPacket with forged acceptance: %v", err)
	}
	if packetForged.Executable {
		t.Errorf("AC-014 failed: forged accepted:true record must keep dependent task T-002 blocked")
	}
	foundIncomplete := false
	for _, d := range packetForged.Diagnostics {
		if d.Code == DiagAcceptanceIncomplete {
			foundIncomplete = true
			break
		}
	}
	if !foundIncomplete {
		t.Errorf("AC-014 failed: expected ACCEPTANCE_INCOMPLETE diagnostic for forged acceptance, got: %+v", packetForged.Diagnostics)
	}

	// Scenario B: Legitimate acceptance followed by stale artifact tampering
	// AC-014: Stale artifact keeps the dependent task blocked
	// Clean up forged acceptance
	_ = os.Remove(forgedPath)

	// Create real artifact for T-001
	engineOutputContent := "package fixture\n\nfunc EngineOutput() string { return \"engine-v1\" }\n"
	if err := os.WriteFile(filepath.Join(dir, "engine_output.go"), []byte(engineOutputContent), 0644); err != nil {
		t.Fatalf("write engine_output.go: %v", err)
	}
	_ = exec.Command("git", "-C", dir, "add", ".").Run()
	_ = exec.Command("git", "-C", dir, "commit", "-m", "feat: implement T-001 legitimately").Run()

	cand, err := CaptureTaskCandidate(dir, slug)
	if err != nil {
		t.Fatalf("capture candidate: %v", err)
	}
	candDigest := TaskCandidateDigest(*cand)
	specDigest, err := WorkPackageDigest(pkg)
	if err != nil {
		t.Fatalf("compute spec digest: %v", err)
	}

	evPath := filepath.Join(dir, "goals", slug, "evidence", "T-001", "c-001", "receipt.json")
	_ = os.MkdirAll(filepath.Dir(evPath), 0755)
	ev := TaskEvidence{
		SchemaVersion: TaskEvidenceSchemaVersion,
		AttemptID:     "att-T-001-AC-001",
		GoalSlug:      slug,
		TaskID:        "T-001",
		CaseID:        "AC-001",
		CheckID:       "C-001",
		SpecDigest:    specDigest,
		Candidate:     *cand,
		Scope:         ScopeUnit,
		Outcome:       OutcomePass,
		Origin:        OriginRunner,
		Executable:    "go",
		Args:          []string{"test", "./..."},
		Cwd:           "cli",
		ExitCode:      intPtr(0),
		Assertions: []AssertionObservation{
			{
				Kind:     AssertionGoTestPass,
				Target:   "github.com/jadmadi/sila/internal/goals/TestTaskPacketForgedAcceptance",
				Expected: true,
				Observed: true,
				Passed:   true,
			},
		},
		Artifacts: []EvidenceArtifact{},
		StartedAt: time.Now().UTC().Add(-2 * time.Second),
		EndedAt:   time.Now().UTC(),
	}
	evData, _ := json.MarshalIndent(ev, "", "  ")
	_ = os.WriteFile(evPath, evData, 0644)

	relEvPath := filepath.Join("goals", slug, "evidence", "T-001", "c-001", "receipt.json")
	sel := ReceiptSelection{
		SchemaVersion:            ReceiptSelectionSchemaVersion,
		GoalSlug:                 slug,
		TaskID:                   "T-001",
		SpecDigest:               specDigest,
		CandidateDigest:          candDigest,
		ReceiptPaths:             []string{relEvPath},
		NewerFailureDispositions: []NewerFailureDisposition{},
	}

	rev := AcceptanceReview{
		SchemaVersion:   AcceptanceReviewSchemaVersion,
		Reviewer:        "reviewer@test",
		Producer:        "producer@test",
		CandidateDigest: candDigest,
		SpecDigest:      specDigest,
		Disposition:     "accept",
		CaseDispositions: []CaseDisposition{
			{
				CaseID:   "AC-001",
				Accepted: true,
				Reason:   "All unit checks verified",
			},
		},
		ReviewedAt: time.Now().UTC().Add(time.Second).Format(time.RFC3339),
	}

	accRes, err := EvaluateTaskAcceptance(dir, slug, "T-001", sel, rev)
	if err != nil || !accRes.Accepted {
		t.Fatalf("EvaluateTaskAcceptance for real T-001 failed: %v", err)
	}
	if err := SaveTaskAcceptance(dir, slug, "T-001", accRes, &sel, &rev); err != nil {
		t.Fatalf("SaveTaskAcceptance: %v", err)
	}

	// Verify packet can be built validly
	packetValid, err := BuildTaskPacket(dir, slug, "T-002")
	if err != nil || !packetValid.Executable {
		t.Fatalf("expected valid packet before tampering, err=%v", err)
	}

	// Tamper with the artifact: modify engine_output.go
	if err := os.WriteFile(filepath.Join(dir, "engine_output.go"), []byte("// modified tampered content\n"), 0644); err != nil {
		t.Fatalf("modify artifact: %v", err)
	}

	// 1. ValidateTaskPacket on the previous packet must catch the drift
	driftDiags := ValidateTaskPacket(dir, packetValid)
	hasStaleDiag := false
	for _, d := range driftDiags {
		if d.Code == DiagPacketDependencyStale {
			hasStaleDiag = true
			break
		}
	}
	if !hasStaleDiag {
		t.Errorf("AC-014 failed: ValidateTaskPacket did not report PACKET_DEPENDENCY_STALE for modified artifact: %+v", driftDiags)
	}

	// 2. BuildTaskPacket must also detect modified artifact and keep T-002 blocked
	packetTampered, err := BuildTaskPacket(dir, slug, "T-002")
	if err != nil {
		t.Fatalf("BuildTaskPacket after artifact tampering: %v", err)
	}
	if packetTampered.Executable {
		t.Errorf("AC-014 failed: BuildTaskPacket must block dependent task when artifact is stale or modified")
	}
	foundStaleBuildDiag := false
	for _, d := range packetTampered.Diagnostics {
		if d.Code == DiagPacketDependencyStale {
			foundStaleBuildDiag = true
			break
		}
	}
	if !foundStaleBuildDiag {
		t.Errorf("AC-014 failed: expected PACKET_DEPENDENCY_STALE on BuildTaskPacket for tampered artifact: %+v", packetTampered.Diagnostics)
	}
}

func computeFileSHA(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("computeFileSHA read %s: %v", path, err)
	}
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func intPtr(i int) *int {
	return &i
}
