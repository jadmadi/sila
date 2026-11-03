package goals

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func validWorkPackageFixture() *WorkPackage {
	return &WorkPackage{
		SchemaVersion: WorkPackageSchemaVersion,
		GoalSlug:      "test-goal",
		Revision:      1,
		Purpose:       "Verify work package decoder and canonical digest behavior",
		Constraints:   []string{"zero dependencies", "deterministic output"},
		NonGoals:      []string{"no cloud execution"},
		Tasks: []TaskContract{
			{
				ID:        "T-001",
				Title:     "First Task",
				Context:   "Background context for task 1",
				Why:       "Rationale for task 1",
				DependsOn: []string{},
				Files: TaskFiles{
					Read:   []string{"internal/goals/types.go"},
					Edit:   []string{"internal/goals/work_package.go"},
					Create: []string{"internal/goals/work_package_test.go"},
					Forbid: []string{".git/*"},
				},
				References: []Reference{
					{
						Path:    "internal/goals/types.go",
						Symbol:  "GoalState",
						Excerpt: "type GoalState string",
						SHA256:  "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
					},
				},
				Decisions: []Decision{
					{
						ID:        "D-001",
						Chosen:    "Standard encoding/json",
						Rationale: "No third party dependencies",
						Rejected:  []string{"gojsonschema", "fastjson"},
					},
				},
				Steps: []Step{
					{
						ID:        "S-001",
						Action:    "Run unit tests",
						Expected:  "Tests pass cleanly",
						OnFailure: "Fix syntax error",
					},
					{
						ID:        "S-002",
						Action:    "Run race tests",
						Expected:  "Zero race warnings",
						OnFailure: "Add synchronization",
					},
				},
				Interfaces:  []string{"DecodeWorkPackage", "WorkPackageDigest"},
				Constraints: []string{"pure-Go", "local-first"},
				Examples: []Example{
					{
						Input:       `{"schema_version":"sila/work-package/v1"}`,
						Output:      "error",
						Explanation: "Incomplete package",
					},
				},
				AcceptanceIDs: []string{"AC-001"},
				Checks: []Check{
					{
						ID:               "C-001",
						Executable:       "go",
						Args:             []string{"test", "./internal/goals"},
						Cwd:              "cli",
						TimeoutSeconds:   60,
						ExpectedExitCode: 0,
						Scope:            ScopeUnit,
						Assertions: []Assertion{
							{
								Kind:     AssertionGoTestPass,
								Target:   "github.com/jadmadi/sila/internal/goals/TestWorkPackageDecode",
								Expected: true,
							},
						},
						Capability:  CapabilityLocal,
						Environment: map[string]string{"CGO_ENABLED": "0"},
					},
				},
				StopConditions: []string{"two repeated test failures"},
				ReturnFormat:   TaskResultReturnFormat,
			},
		},
		Acceptance: []AcceptanceCase{
			{
				ID:            "AC-001",
				Requirement:   "Decode complete work package",
				Given:         "Valid JSON bytes",
				Action:        "DecodeWorkPackage(data)",
				Expected:      "WorkPackage struct with matching fields",
				RequiredScope: ScopeUnit,
				Mandatory:     true,
				CheckIDs:      []string{"C-001"},
			},
		},
		DependencyConditions: []DependencyCondition{
			{
				Kind:     DepConditionAcceptedGoal,
				GoalSlug: "foundation-goal",
			},
		},
		UnresolvedDecisions:  []string{},
		RetiredTaskIDs:       []string{"T-999"},
		RetiredAcceptanceIDs: []string{"AC-999"},
	}
}

func TestWorkPackageDecode(t *testing.T) {
	// AC-001: Complete package decodes successfully
	pkg := validWorkPackageFixture()
	data, err := json.Marshal(pkg)
	if err != nil {
		t.Fatalf("Marshal fixture: %v", err)
	}

	decoded, err := DecodeWorkPackage(data)
	if err != nil {
		t.Fatalf("DecodeWorkPackage valid fixture: %v", err)
	}
	if decoded.GoalSlug != pkg.GoalSlug {
		t.Errorf("expected goal_slug %q, got %q", pkg.GoalSlug, decoded.GoalSlug)
	}
	if len(decoded.Tasks) != 1 || decoded.Tasks[0].ID != "T-001" {
		t.Fatalf("expected task T-001, got %v", decoded.Tasks)
	}

	// AC-002: Unknown fields rejection
	unknownFieldJSON := `{"schema_version":"sila/work-package/v1","unknown_key":"value"}`
	if _, err := DecodeWorkPackage([]byte(unknownFieldJSON)); err == nil {
		t.Errorf("expected error on unknown field, got nil")
	} else if !strings.Contains(err.Error(), "unknown field") {
		t.Errorf("expected 'unknown field' error, got %v", err)
	}

	// AC-002: Duplicate object keys rejection
	dupKeyJSON := `{
		"schema_version":"sila/work-package/v1",
		"goal_slug":"dup-test",
		"revision":1,
		"purpose":"test",
		"purpose":"duplicate purpose",
		"constraints":[],
		"non_goals":[],
		"tasks":[],
		"acceptance":[],
		"dependency_conditions":[],
		"unresolved_decisions":[],
		"retired_task_ids":[],
		"retired_acceptance_ids":[]
	}`
	if _, err := DecodeWorkPackage([]byte(dupKeyJSON)); err == nil {
		t.Errorf("expected error on duplicate object key, got nil")
	} else if !strings.Contains(err.Error(), "duplicate object key") {
		t.Errorf("expected 'duplicate object key' in error, got %v", err)
	}

	// AC-002: Unsupported schema version
	badVersion := *pkg
	badVersion.SchemaVersion = "sila/work-package/v2-preview"
	badVersionData, _ := json.Marshal(badVersion)
	if _, err := DecodeWorkPackage(badVersionData); err == nil {
		t.Errorf("expected error on unsupported schema_version, got nil")
	} else if !strings.Contains(err.Error(), "unsupported schema_version") {
		t.Errorf("expected 'unsupported schema_version' in error, got %v", err)
	}

	// AC-002: Missing required values (empty purpose)
	noPurpose := *pkg
	noPurpose.Purpose = ""
	noPurposeData, _ := json.Marshal(noPurpose)
	if _, err := DecodeWorkPackage(noPurposeData); err == nil {
		t.Errorf("expected error on missing purpose, got nil")
	}

	// AC-002: Empty tasks array
	noTasks := *pkg
	noTasks.Tasks = []TaskContract{}
	noTasksData, _ := json.Marshal(noTasks)
	if _, err := DecodeWorkPackage(noTasksData); err == nil {
		t.Errorf("expected error on empty tasks, got nil")
	}

	// AC-002: Duplicate task IDs
	dupTasks := *pkg
	dupTasks.Tasks = []TaskContract{pkg.Tasks[0], pkg.Tasks[0]}
	dupTasksData, _ := json.Marshal(dupTasks)
	if _, err := DecodeWorkPackage(dupTasksData); err == nil {
		t.Errorf("expected error on duplicate task id, got nil")
	} else if !strings.Contains(err.Error(), "duplicate task id") {
		t.Errorf("expected 'duplicate task id' error, got %v", err)
	}

	// AC-002: Retired ID reuse
	reusedRetired := *pkg
	reusedRetired.RetiredTaskIDs = []string{"T-001"}
	reusedData, _ := json.Marshal(reusedRetired)
	if _, err := DecodeWorkPackage(reusedData); err == nil {
		t.Errorf("expected error on retired task ID reuse, got nil")
	} else if !strings.Contains(err.Error(), "reused retired task id") {
		t.Errorf("expected 'reused retired task id' in error, got %v", err)
	}

	// AC-002: Trailing JSON data
	trailingJSON := string(data) + ` {"extra":"trailing"}`
	if _, err := DecodeWorkPackage([]byte(trailingJSON)); err == nil {
		t.Errorf("expected error on trailing JSON data, got nil")
	} else if !strings.Contains(err.Error(), "trailing data") {
		t.Errorf("expected 'trailing data' in error, got %v", err)
	}

	// AC-002: Invalid enums
	badScope := *pkg
	badScope.Tasks[0].Checks[0].Scope = "invalid-scope"
	badScopeData, _ := json.Marshal(badScope)
	if _, err := DecodeWorkPackage(badScopeData); err == nil {
		t.Errorf("expected error on invalid check scope, got nil")
	}

	// AC-002: Manual capability with non-empty executable
	badManual := *pkg
	badManual.Tasks[0].Checks[0].Capability = CapabilityManual
	badManual.Tasks[0].Checks[0].Executable = "curl"
	badManualData, _ := json.Marshal(badManual)
	if _, err := DecodeWorkPackage(badManualData); err == nil {
		t.Errorf("expected error on invalid manual check executable, got nil")
	}
}

func TestWorkPackageDigest(t *testing.T) {
	pkgA := validWorkPackageFixture()

	// Add second task
	t2 := pkgA.Tasks[0]
	t2.ID = "T-002"
	t2.Title = "Second Task"
	t2.AcceptanceIDs = []string{"AC-002"}
	t2.Checks[0].ID = "C-002"

	ac2 := pkgA.Acceptance[0]
	ac2.ID = "AC-002"
	ac2.CheckIDs = []string{"C-002"}

	pkgA.Tasks = append(pkgA.Tasks, t2)
	pkgA.Acceptance = append(pkgA.Acceptance, ac2)

	digestA, err := WorkPackageDigest(pkgA)
	if err != nil {
		t.Fatalf("WorkPackageDigest pkgA: %v", err)
	}
	if len(digestA) != 64 {
		t.Errorf("expected 64-char hex SHA256 digest, got %d chars: %s", len(digestA), digestA)
	}

	// AC-001: Reordering tasks preserves the canonical digest
	pkgB := cloneWorkPackage(pkgA)
	pkgB.Tasks[0], pkgB.Tasks[1] = pkgB.Tasks[1], pkgB.Tasks[0]
	pkgB.Acceptance[0], pkgB.Acceptance[1] = pkgB.Acceptance[1], pkgB.Acceptance[0]

	digestB, err := WorkPackageDigest(pkgB)
	if err != nil {
		t.Fatalf("WorkPackageDigest pkgB: %v", err)
	}
	if digestA != digestB {
		t.Errorf("reordered tasks altered digest! digestA=%s, digestB=%s", digestA, digestB)
	}

	// AC-001: Reordering ordered execution steps MUST alter the digest
	pkgC := cloneWorkPackage(pkgA)
	pkgC.Tasks[0].Steps[0], pkgC.Tasks[0].Steps[1] = pkgC.Tasks[0].Steps[1], pkgC.Tasks[0].Steps[0]
	digestC, err := WorkPackageDigest(pkgC)
	if err != nil {
		t.Fatalf("WorkPackageDigest pkgC: %v", err)
	}
	if digestA == digestC {
		t.Errorf("reordering execution steps did NOT alter digest! expected different digest")
	}

	// AC-003: Digest changes when constraint changes
	pkgConstraint := cloneWorkPackage(pkgA)
	pkgConstraint.Constraints = append(pkgConstraint.Constraints, "new-constraint")
	digestConstraint, err := WorkPackageDigest(pkgConstraint)
	if err != nil {
		t.Fatalf("WorkPackageDigest: %v", err)
	}
	if digestA == digestConstraint {
		t.Errorf("digest did not change when constraint was added")
	}

	// AC-003: Digest changes when case expectation changes
	pkgExpect := cloneWorkPackage(pkgA)
	pkgExpect.Acceptance[0].Expected = "Modified expectation"
	digestExpect, err := WorkPackageDigest(pkgExpect)
	if err != nil {
		t.Fatalf("WorkPackageDigest: %v", err)
	}
	if digestA == digestExpect {
		t.Errorf("digest did not change when acceptance expectation changed")
	}

	// AC-003: Digest changes when command argument changes
	pkgArgs := cloneWorkPackage(pkgA)
	pkgArgs.Tasks[0].Checks[0].Args = append(pkgArgs.Tasks[0].Checks[0].Args, "-v")
	digestArgs, err := WorkPackageDigest(pkgArgs)
	if err != nil {
		t.Fatalf("WorkPackageDigest: %v", err)
	}
	if digestA == digestArgs {
		t.Errorf("digest did not change when check argument changed")
	}

	// Positional argument reordering MUST alter the digest
	pkgArgReorder := cloneWorkPackage(pkgA)
	pkgArgReorder.Tasks[0].Checks[0].Args = []string{"./internal/goals", "test"}
	digestArgReorder, err := WorkPackageDigest(pkgArgReorder)
	if err != nil {
		t.Fatalf("WorkPackageDigest: %v", err)
	}
	if digestA == digestArgReorder {
		t.Errorf("reordering check arguments did not change digest! expected order sensitivity")
	}

	// AC-003: Digest changes when required reference changes
	pkgRef := cloneWorkPackage(pkgA)
	pkgRef.Tasks[0].References[0].SHA256 = "0000000000000000000000000000000000000000000000000000000000000000"
	digestRef, err := WorkPackageDigest(pkgRef)
	if err != nil {
		t.Fatalf("WorkPackageDigest: %v", err)
	}
	if digestA == digestRef {
		t.Errorf("digest did not change when reference SHA256 changed")
	}
}

func TestWorkPackageValidationEdges(t *testing.T) {
	pkg := validWorkPackageFixture()

	// 1. Task cannot depend on unknown task
	badDep := cloneWorkPackage(pkg)
	badDep.Tasks[0].DependsOn = []string{"T-999"}
	if err := ValidateWorkPackage(badDep); err == nil || !strings.Contains(err.Error(), "unknown task") {
		t.Errorf("expected unknown task dependency error, got %v", err)
	}

	// 2. Task cannot depend on itself
	selfDep := cloneWorkPackage(pkg)
	selfDep.Tasks[0].DependsOn = []string{"T-001"}
	if err := ValidateWorkPackage(selfDep); err == nil || !strings.Contains(err.Error(), "cannot depend on itself") {
		t.Errorf("expected self dependency error, got %v", err)
	}

	// 3. Task dependency cycles are rejected
	cyclePkg := cloneWorkPackage(pkg)
	t2 := cyclePkg.Tasks[0]
	t2.ID = "T-002"
	t2.Title = "Task 2"
	t2.Checks = []Check{
		{
			ID:               "C-002",
			Executable:       "go",
			Args:             []string{"test"},
			Cwd:              "cli",
			TimeoutSeconds:   30,
			ExpectedExitCode: 0,
			Scope:            ScopeUnit,
			Capability:       CapabilityLocal,
			Assertions: []Assertion{
				{Kind: AssertionGoTestPass, Target: "TestFoo", Expected: true},
			},
		},
	}
	t2.AcceptanceIDs = []string{"AC-002"}
	t2.DependsOn = []string{"T-001"}

	ac2 := cyclePkg.Acceptance[0]
	ac2.ID = "AC-002"
	ac2.CheckIDs = []string{"C-002"}

	cyclePkg.Tasks = append(cyclePkg.Tasks, t2)
	cyclePkg.Acceptance = append(cyclePkg.Acceptance, ac2)
	cyclePkg.Tasks[0].DependsOn = []string{"T-002"} // Cycle: T-001 -> T-002 -> T-001

	if err := ValidateWorkPackage(cyclePkg); err == nil || !strings.Contains(err.Error(), "cycle detected") {
		t.Errorf("expected dependency cycle error, got %v", err)
	}

	// 4. Duplicate check ID across tasks is rejected
	dupCheckPkg := cloneWorkPackage(pkg)
	t2Dup := dupCheckPkg.Tasks[0]
	t2Dup.ID = "T-002"
	t2Dup.Title = "Task 2"
	t2Dup.Checks = []Check{
		{
			ID:               "C-001", // Duplicate C-001 from T-001
			Executable:       "go",
			Args:             []string{"test"},
			Cwd:              "cli",
			TimeoutSeconds:   30,
			ExpectedExitCode: 0,
			Scope:            ScopeUnit,
			Capability:       CapabilityLocal,
			Assertions: []Assertion{
				{Kind: AssertionGoTestPass, Target: "TestFoo", Expected: true},
			},
		},
	}
	t2Dup.AcceptanceIDs = []string{"AC-002"}
	ac2Dup := dupCheckPkg.Acceptance[0]
	ac2Dup.ID = "AC-002"
	ac2Dup.CheckIDs = []string{"C-001"}
	dupCheckPkg.Tasks = append(dupCheckPkg.Tasks, t2Dup)
	dupCheckPkg.Acceptance = append(dupCheckPkg.Acceptance, ac2Dup)

	if err := ValidateWorkPackage(dupCheckPkg); err == nil || !strings.Contains(err.Error(), "duplicate check id") {
		t.Errorf("expected duplicate check id error across tasks, got %v", err)
	}

	// 5. Acceptance case referencing unknown check ID is rejected
	badCheckRefPkg := cloneWorkPackage(pkg)
	badCheckRefPkg.Acceptance[0].CheckIDs = []string{"C-999"}
	if err := ValidateWorkPackage(badCheckRefPkg); err == nil || !strings.Contains(err.Error(), "unknown check_id") {
		t.Errorf("expected unknown check_id error, got %v", err)
	}

	// 6. Deeply nested JSON is rejected by recursion depth guard
	nested := strings.Repeat(`{"a":`, 35) + `"leaf"` + strings.Repeat(`}`, 35)
	if _, err := DecodeWorkPackage([]byte(nested)); err == nil || !strings.Contains(err.Error(), "maximum JSON nesting depth") {
		t.Errorf("expected nesting depth error for 35 levels, got %v", err)
	}
}

func TestWorkPackageSchemaParity(t *testing.T) {
	schemaPath := filepath.Join("..", "..", "docs", "schemas", "goal-work-package-v1.schema.json")
	schemaData, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatalf("ReadFile schema: %v", err)
	}

	var schema map[string]any
	if err := json.Unmarshal(schemaData, &schema); err != nil {
		t.Fatalf("Unmarshal schema: %v", err)
	}

	if schema["$schema"] != "http://json-schema.org/draft-07/schema#" {
		t.Errorf("expected draft-07 $schema, got %v", schema["$schema"])
	}
	if schema["additionalProperties"] != false {
		t.Errorf("root schema must enforce additionalProperties: false")
	}

	defs, ok := schema["definitions"].(map[string]any)
	if !ok {
		t.Fatalf("schema missing definitions object")
	}

	expectedDefs := []string{
		"TaskContract", "TaskFiles", "Reference", "Decision",
		"Step", "Example", "AcceptanceCase", "Check",
		"Assertion", "DependencyCondition",
	}

	for _, d := range expectedDefs {
		defObj, exists := defs[d].(map[string]any)
		if !exists {
			t.Errorf("missing definition %q in schema", d)
			continue
		}
		if defObj["additionalProperties"] != false {
			t.Errorf("definition %q must enforce additionalProperties: false", d)
		}
	}
}
