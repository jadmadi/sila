package goals

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// setupTaskPacketFixture creates a disk-backed environment with valid files, work package, and spec review.
func setupTaskPacketFixture(t *testing.T) (string, string, *WorkPackage) {
	t.Helper()
	tmpDir := t.TempDir()
	slug := "test-packet-fixture"

	// 1. Write source files
	goSrc := "package engine\n\ntype Core struct { Capacity int }\n"
	err := os.WriteFile(filepath.Join(tmpDir, "engine.go"), []byte(goSrc), 0644)
	if err != nil {
		t.Fatalf("write engine.go: %v", err)
	}
	hGo := sha256.Sum256([]byte(goSrc))
	engineSHA := hex.EncodeToString(hGo[:])

	docSrc := "# Engine Spec\nPrecision fuel injection system.\n"
	err = os.WriteFile(filepath.Join(tmpDir, "doc.txt"), []byte(docSrc), 0644)
	if err != nil {
		t.Fatalf("write doc.txt: %v", err)
	}
	hDoc := sha256.Sum256([]byte(docSrc))
	docSHA := hex.EncodeToString(hDoc[:])

	// 2. Build WorkPackage
	pkg := validWorkPackageFixture()
	pkg.GoalSlug = slug
	pkg.Purpose = "Deliver deterministic engine execution packets"
	pkg.Constraints = []string{"pure-Go stdlib", "zero external dependencies"}
	pkg.NonGoals = []string{"no GUI", "no cloud services"}
	pkg.UnresolvedDecisions = []string{}

	pkg.Tasks = []TaskContract{
		{
			ID:      "T-001",
			Title:   "Setup Engine Core",
			Context: "Initialize engine data structures",
			Why:     "Foundational data layer",
			Files: TaskFiles{
				Read:   []string{"doc.txt"},
				Edit:   []string{"engine.go"},
				Create: []string{"engine_output.go"},
				Forbid: []string{"research/*"},
			},
			References: []Reference{
				{
					Path:    "doc.txt",
					Excerpt: "Precision fuel injection system.",
					SHA256:  docSHA,
				},
			},
			Decisions: []Decision{
				{
					ID:        "D-001",
					Chosen:    "Capacity struct field",
					Rationale: "Simplest integer representation",
				},
			},
			Steps: []Step{
				{
					ID:        "S-001",
					Action:    "Create engine output",
					Expected:  "Output struct defined",
					OnFailure: "Abort and report",
				},
			},
			Interfaces:    []string{"type Core struct"},
			Constraints:   []string{"CGO_ENABLED=0"},
			Examples:      []Example{{Input: "Init()", Output: "Core", Explanation: "Zero value"}},
			AcceptanceIDs: []string{"AC-001", "AC-002"},
			Checks: []Check{
				{
					ID:               "C-001",
					Executable:       "go",
					Args:             []string{"test", "./internal/goals"},
					Cwd:              "cli",
					TimeoutSeconds:   30,
					ExpectedExitCode: 0,
					Scope:            ScopeUnit,
					Assertions: []Assertion{
						{
							Kind:     AssertionGoTestPass,
							Target:   "github.com/jadmadi/sila/internal/goals/TestTaskPacketCompleteness",
							Expected: true,
						},
					},
					Capability:  CapabilityLocal,
					Environment: map[string]string{"CGO_ENABLED": "0"},
				},
				{
					ID:               "C-002",
					Executable:       "go",
					Args:             []string{"test", "./internal/goals"},
					Cwd:              "cli",
					TimeoutSeconds:   30,
					ExpectedExitCode: 0,
					Scope:            ScopeUnit,
					Assertions: []Assertion{
						{
							Kind:     AssertionGoTestPass,
							Target:   "github.com/jadmadi/sila/internal/goals/TestTaskPacketCompleteness",
							Expected: true,
						},
					},
					Capability:  CapabilityLocal,
					Environment: map[string]string{"CGO_ENABLED": "0"},
				},
			},
			StopConditions: []string{"Any panic or memory leak"},
			ReturnFormat:   "sila/task-result/v1",
		},
		{
			ID:        "T-002",
			Title:     "Add Engine Diagnostics",
			Context:   "Inspect telemetry from Engine Core",
			Why:       "Monitoring and safety diagnostics",
			DependsOn: []string{"T-001"},
			Files: TaskFiles{
				Read:   []string{"engine.go"},
				Edit:   []string{"engine.go"},
				Create: []string{"engine_diag.go"},
				Forbid: []string{"research/*"},
			},
			References: []Reference{
				{
					Path:    "engine.go",
					Symbol:  "Core",
					Excerpt: "type Core struct",
					SHA256:  engineSHA,
				},
			},
			Decisions:     []Decision{},
			Steps:         []Step{{ID: "S-002", Action: "Add telemetry", Expected: "Telemetry ready", OnFailure: "Halt"}},
			Interfaces:    []string{"func Diagnostics()"},
			Constraints:   []string{"thread-safe"},
			Examples:      []Example{},
			AcceptanceIDs: []string{"AC-003"},
			Checks: []Check{
				{
					ID:               "C-003",
					Executable:       "go",
					Args:             []string{"test", "./internal/goals"},
					Cwd:              "cli",
					TimeoutSeconds:   30,
					ExpectedExitCode: 0,
					Scope:            ScopeUnit,
					Assertions: []Assertion{
						{
							Kind:     AssertionGoTestPass,
							Target:   "github.com/jadmadi/sila/internal/goals/TestTaskPacketScope",
							Expected: true,
						},
					},
					Capability:  CapabilityLocal,
					Environment: map[string]string{"CGO_ENABLED": "0"},
				},
			},
			StopConditions: []string{"Lock contention"},
			ReturnFormat:   "sila/task-result/v1",
		},
	}

	pkg.Acceptance = []AcceptanceCase{
		{
			ID:            "AC-001",
			Requirement:   "Core capacity is initialized to default",
			Given:         "Fresh initialization",
			Action:        "Inspect Core.Capacity",
			Expected:      "Capacity == 0",
			RequiredScope: "unit",
			Mandatory:     true,
			CheckIDs:      []string{"C-001"},
		},
		{
			ID:            "AC-002",
			Requirement:   "Engine output can be generated",
			Given:         "Valid Core instance",
			Action:        "Generate output",
			Expected:      "Exit code 0",
			RequiredScope: "unit",
			Mandatory:     true,
			CheckIDs:      []string{"C-002"},
		},
		{
			ID:            "AC-003",
			Requirement:   "Telemetry returns healthy status",
			Given:         "Running engine",
			Action:        "Query diagnostics",
			Expected:      "Status == Healthy",
			RequiredScope: "unit",
			Mandatory:     true,
			CheckIDs:      []string{"C-003"},
		},
	}

	// 3. Save WorkPackage to disk
	goalDir := filepath.Join(tmpDir, "goals", slug)
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		t.Fatalf("mkdir goal dir: %v", err)
	}
	pkgData, err := json.MarshalIndent(pkg, "", "  ")
	if err != nil {
		t.Fatalf("marshal work package: %v", err)
	}
	if err := os.WriteFile(filepath.Join(goalDir, "work-package.json"), pkgData, 0644); err != nil {
		t.Fatalf("write work-package.json: %v", err)
	}

	// 4. Save valid SpecReview
	specDigest, err := WorkPackageDigest(pkg)
	if err != nil {
		t.Fatalf("compute spec digest: %v", err)
	}
	review := SpecReview{
		SchemaVersion: SpecReviewSchemaVersion,
		SpecDigest:    specDigest,
		Reviewer:      "test-architect",
		ReviewedAt:    time.Now().UTC().Format(time.RFC3339),
		Verdict:       VerdictReady,
		Shortcuts: []ShortcutCounter{
			{
				IncorrectImplementation: "Hollow stub returns default struct",
				ExcludedByIDs:           []string{"AC-001"},
				Resolution:              "Assert exact struct fields",
			},
		},
		UnresolvedBlockers: []string{},
	}
	if err := SaveSpecReview(tmpDir, slug, review); err != nil {
		t.Fatalf("save spec review: %v", err)
	}

	return tmpDir, slug, pkg
}

func TestTaskPacketCompleteness(t *testing.T) {
	tmpDir, slug, pkg := setupTaskPacketFixture(t)

	// 1. Build packet for independent task T-001
	packet, err := BuildTaskPacket(tmpDir, slug, "T-001")
	if err != nil {
		t.Fatalf("BuildTaskPacket returned unexpected error: %v", err)
	}

	if packet.SchemaVersion != TaskPacketSchemaVersion {
		t.Errorf("expected schema version %s, got %s", TaskPacketSchemaVersion, packet.SchemaVersion)
	}
	if packet.GoalSlug != slug {
		t.Errorf("expected goal slug %s, got %s", slug, packet.GoalSlug)
	}
	if packet.TaskID != "T-001" {
		t.Errorf("expected task ID T-001, got %s", packet.TaskID)
	}
	if packet.Purpose != pkg.Purpose {
		t.Errorf("expected purpose %q, got %q", pkg.Purpose, packet.Purpose)
	}
	if packet.SpecDigest == "" {
		t.Errorf("expected non-empty SpecDigest")
	}
	if packet.PacketDigest == "" {
		t.Errorf("expected non-empty PacketDigest")
	}
	if !packet.Executable {
		t.Errorf("expected independent task T-001 to be executable, got false (diags: %+v)", packet.Diagnostics)
	}

	// Verify acceptance cases completeness for T-001
	if len(packet.Acceptance) != 2 {
		t.Fatalf("expected 2 acceptance cases, got %d", len(packet.Acceptance))
	}
	if packet.Acceptance[0].ID != "AC-001" || packet.Acceptance[1].ID != "AC-002" {
		t.Errorf("unexpected acceptance cases: %+v", packet.Acceptance)
	}

	// Verify source pins
	if len(packet.SourcePins) == 0 {
		t.Fatalf("expected source pins to be populated")
	}
	var foundDoc, foundEngine, foundCreate bool
	for _, pin := range packet.SourcePins {
		if pin.Path == "doc.txt" {
			foundDoc = true
			if pin.SHA256 == "" || pin.MustBeAbsent {
				t.Errorf("doc.txt pin invalid: %+v", pin)
			}
		}
		if pin.Path == "engine.go" {
			foundEngine = true
			if pin.SHA256 == "" || pin.MustBeAbsent {
				t.Errorf("engine.go pin invalid: %+v", pin)
			}
		}
		if pin.Path == "engine_output.go" {
			foundCreate = true
			if !pin.MustBeAbsent {
				t.Errorf("engine_output.go must have MustBeAbsent=true, got %+v", pin)
			}
		}
	}
	if !foundDoc || !foundEngine || !foundCreate {
		t.Errorf("missing expected source pins: doc=%v, engine=%v, create=%v", foundDoc, foundEngine, foundCreate)
	}

	// Strict JSON roundtrip verification
	data, err := json.Marshal(packet)
	if err != nil {
		t.Fatalf("marshal packet failed: %v", err)
	}
	decoded, err := DecodeTaskPacket(data)
	if err != nil {
		t.Fatalf("DecodeTaskPacket failed: %v", err)
	}
	if decoded.PacketDigest != packet.PacketDigest {
		t.Errorf("decoded packet digest mismatch: expected %s, got %s", packet.PacketDigest, decoded.PacketDigest)
	}

	// 2. Negative test: missing referenced file fails generation
	if err := os.Remove(filepath.Join(tmpDir, "doc.txt")); err != nil {
		t.Fatalf("remove doc.txt: %v", err)
	}
	_, err = BuildTaskPacket(tmpDir, slug, "T-001")
	if err == nil {
		t.Fatalf("expected BuildTaskPacket to fail when referenced file is missing, got nil error")
	}

	// 3. Negative test: non-existent task ID fails generation
	_, err = BuildTaskPacket(tmpDir, slug, "T-999")
	if err == nil {
		t.Fatalf("expected BuildTaskPacket to fail for non-existent task ID, got nil error")
	}
}

func TestTaskPacketDeterminism(t *testing.T) {
	tmpDir, slug, _ := setupTaskPacketFixture(t)

	// Build the packet twice from unchanged inputs
	packet1, err := BuildTaskPacket(tmpDir, slug, "T-001")
	if err != nil {
		t.Fatalf("build packet 1: %v", err)
	}

	packet2, err := BuildTaskPacket(tmpDir, slug, "T-001")
	if err != nil {
		t.Fatalf("build packet 2: %v", err)
	}

	// Assert digests are identical
	if packet1.PacketDigest != packet2.PacketDigest {
		t.Fatalf("determinism violation: digest 1 (%s) != digest 2 (%s)", packet1.PacketDigest, packet2.PacketDigest)
	}

	// Assert serialized JSON payload bytes are identical
	bytes1, err := json.Marshal(packet1)
	if err != nil {
		t.Fatalf("marshal packet 1: %v", err)
	}
	bytes2, err := json.Marshal(packet2)
	if err != nil {
		t.Fatalf("marshal packet 2: %v", err)
	}
	if string(bytes1) != string(bytes2) {
		t.Fatalf("determinism violation: JSON bytes mismatch")
	}
}

func TestTaskPacketScope(t *testing.T) {
	tmpDir, slug, _ := setupTaskPacketFixture(t)

	// 1. T-001 has no prerequisites -> executable packet
	packet1, err := BuildTaskPacket(tmpDir, slug, "T-001")
	if err != nil {
		t.Fatalf("build T-001: %v", err)
	}
	if !packet1.Executable {
		t.Errorf("expected T-001 to be executable")
	}
	if len(packet1.Acceptance) != 2 {
		t.Fatalf("expected T-001 to have 2 acceptance cases, got %d", len(packet1.Acceptance))
	}
	for _, ac := range packet1.Acceptance {
		if ac.ID == "AC-003" {
			t.Errorf("scope leak: T-001 packet contains AC-003 which belongs to T-002")
		}
	}

	// 2. T-002 depends on T-001 -> Stage-1 inspection-only (executable=false, ACCEPTANCE_NOT_AVAILABLE)
	packet2, err := BuildTaskPacket(tmpDir, slug, "T-002")
	if err != nil {
		t.Fatalf("build T-002: %v", err)
	}
	if packet2.Executable {
		t.Errorf("expected dependent task T-002 to be non-executable in Stage 1")
	}

	var foundDiag bool
	for _, d := range packet2.Diagnostics {
		if d.Code == DiagAcceptanceNotAvailable {
			foundDiag = true
			break
		}
	}
	if !foundDiag {
		t.Errorf("expected ACCEPTANCE_NOT_AVAILABLE diagnostic on T-002, got diagnostics: %+v", packet2.Diagnostics)
	}

	if len(packet2.Acceptance) != 1 || packet2.Acceptance[0].ID != "AC-003" {
		t.Errorf("expected T-002 to have exactly AC-003, got: %+v", packet2.Acceptance)
	}
}
