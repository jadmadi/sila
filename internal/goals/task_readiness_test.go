package goals

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setupValidReadinessFixture builds a fully populated, disk-backed WorkPackage fixture.
func setupValidReadinessFixture(t *testing.T) (string, *WorkPackage) {
	t.Helper()
	tmpDir := t.TempDir()

	// Create referenced source files
	goSrc := `package sample

type Engine struct {
	Speed int
}

func (e *Engine) Accelerate() {
	e.Speed += 10
}

func NewEngine() *Engine {
	return &Engine{Speed: 0}
}
`
	err := os.WriteFile(filepath.Join(tmpDir, "engine.go"), []byte(goSrc), 0644)
	if err != nil {
		t.Fatalf("write engine.go: %v", err)
	}

	h := sha256.Sum256([]byte(goSrc))
	engineSHA := hex.EncodeToString(h[:])

	docSrc := "# System Architecture\nThis service manages high-speed engine states."
	err = os.WriteFile(filepath.Join(tmpDir, "doc.txt"), []byte(docSrc), 0644)
	if err != nil {
		t.Fatalf("write doc.txt: %v", err)
	}
	hDoc := sha256.Sum256([]byte(docSrc))
	docSHA := hex.EncodeToString(hDoc[:])

	// Create edit target
	err = os.WriteFile(filepath.Join(tmpDir, "handler.go"), []byte("package sample\n"), 0644)
	if err != nil {
		t.Fatalf("write handler.go: %v", err)
	}

	pkg := validWorkPackageFixture()
	pkg.GoalSlug = "test-readiness-fixture"
	pkg.UnresolvedDecisions = []string{}
	pkg.Purpose = "Implement robust engine control"

	pkg.Tasks = []TaskContract{
		{
			ID:        "T-001",
			Title:     "Initialize engine components",
			Context:   "Core engine pipeline initialization",
			Why:       "Support engine scaling",
			DependsOn: []string{},
			Files: TaskFiles{
				Read:   []string{"doc.txt"},
				Edit:   []string{"handler.go"},
				Create: []string{"new_handler.go"},
			},
			References: []Reference{
				{
					Path:    "engine.go",
					Symbol:  "Engine.Accelerate",
					Excerpt: "func (e *Engine) Accelerate()",
					SHA256:  engineSHA,
				},
				{
					Path:    "doc.txt",
					Excerpt: "high-speed engine states",
					SHA256:  docSHA,
				},
			},
			Decisions: []Decision{
				{ID: "D-001", Chosen: "Local memory", Rationale: "Low latency", Rejected: []string{"Redis"}},
			},
			Steps: []Step{
				{ID: "S-001", Action: "Construct engine", Expected: "Engine instance active", OnFailure: "Halt startup"},
			},
			Checks: []Check{
				{
					ID:               "C-001",
					Executable:       "go",
					Args:             []string{"test", "./..."},
					TimeoutSeconds:   10,
					Scope:            ScopeUnit,
					Capability:       CapabilityLocal,
					ExpectedExitCode: 0,
					Assertions: []Assertion{
						{Kind: AssertionGoTestPass, Target: "github.com/sample/TestEngine", Expected: true},
					},
				},
			},
			AcceptanceIDs: []string{"AC-001"},
			Examples: []Example{
				{Input: "init", Output: "ok", Explanation: "clean start"},
			},
			ReturnFormat: TaskResultReturnFormat,
		},
	}

	pkg.Acceptance = []AcceptanceCase{
		{
			ID:            "AC-001",
			Requirement:   "Engine activates cleanly",
			Given:         "Valid configuration",
			Action:        "Invoke NewEngine",
			Expected:      "Engine running without errors",
			RequiredScope: ScopeUnit,
			Mandatory:     true,
			CheckIDs:      []string{"C-001"},
		},
	}

	return tmpDir, pkg
}

// TestTaskReadinessCoverage tests requirement coverage, dependencies, cycles, and deterministic sorting.
func TestTaskReadinessCoverage(t *testing.T) {
	tmpDir, pkg := setupValidReadinessFixture(t)

	// AC-001: Complete valid contract produces zero error diagnostics
	diags := ValidateTaskReadiness(tmpDir, pkg)
	if len(diags) != 0 {
		t.Fatalf("expected 0 diagnostics for complete valid contract, got %d: %+v", len(diags), diags)
	}

	// AC-002: Unowned AC-003 produces ACCEPTANCE_UNOWNED
	unownedPkg := cloneWorkPackage(pkg)
	unownedPkg.Acceptance = append(unownedPkg.Acceptance, AcceptanceCase{
		ID:            "AC-003",
		Requirement:   "Unowned requirement",
		Given:         "Given condition",
		Action:        "Action step",
		Expected:      "Expected result",
		RequiredScope: ScopeUnit,
		Mandatory:     true,
		CheckIDs:      []string{"C-001"},
	})
	diags = ValidateTaskReadiness(tmpDir, unownedPkg)
	foundUnowned := false
	for _, d := range diags {
		if d.Code == DiagAcceptanceUnowned && strings.Contains(d.Path, "AC-003") {
			foundUnowned = true
			break
		}
	}
	if !foundUnowned {
		t.Errorf("expected ACCEPTANCE_UNOWNED for AC-003, got: %+v", diags)
	}

	// AC-002: Unknown task dependency produces TASK_DEPENDENCY_MISSING
	missingDepPkg := cloneWorkPackage(pkg)
	missingDepPkg.Tasks[0].DependsOn = []string{"T-999"}
	diags = ValidateTaskReadiness(tmpDir, missingDepPkg)
	foundMissingDep := false
	for _, d := range diags {
		if d.Code == DiagTaskDependencyMissing && strings.Contains(d.Message, "T-999") {
			foundMissingDep = true
			break
		}
	}
	if !foundMissingDep {
		t.Errorf("expected TASK_DEPENDENCY_MISSING for T-999, got: %+v", diags)
	}

	// AC-002: T-001 / T-002 cycle produces TASK_DEPENDENCY_CYCLE
	cyclePkg := cloneWorkPackage(pkg)
	t2 := cyclePkg.Tasks[0]
	t2.ID = "T-002"
	t2.Title = "Secondary task"
	t2.DependsOn = []string{"T-001"}
	t2.AcceptanceIDs = []string{"AC-002"}
	ac2 := cyclePkg.Acceptance[0]
	ac2.ID = "AC-002"
	cyclePkg.Acceptance = append(cyclePkg.Acceptance, ac2)
	cyclePkg.Tasks = append(cyclePkg.Tasks, t2)
	cyclePkg.Tasks[0].DependsOn = []string{"T-002"} // Cycle T-001 -> T-002 -> T-001

	diags = ValidateTaskReadiness(tmpDir, cyclePkg)
	foundCycle := false
	for _, d := range diags {
		if d.Code == DiagTaskDependencyCycle {
			foundCycle = true
			break
		}
	}
	if !foundCycle {
		t.Errorf("expected TASK_DEPENDENCY_CYCLE, got: %+v", diags)
	}

	// AC-001: Diagnostics sort deterministically by task ID, field, code
	multiDiagPkg := cloneWorkPackage(cyclePkg)
	multiDiagPkg.UnresolvedDecisions = []string{"D-999"}
	diags = ValidateTaskReadiness(tmpDir, multiDiagPkg)
	if len(diags) < 2 {
		t.Fatalf("expected at least 2 diagnostics, got %d", len(diags))
	}
	// Verify package-level path (unresolved_decisions) precedes task-level paths
	if extractTaskIDFromPath(diags[0].Path) != "" && extractTaskIDFromPath(diags[len(diags)-1].Path) == "" {
		t.Errorf("expected package-level diagnostic to precede task-level, got: %+v", diags)
	}
}

// TestTaskReadinessReferences tests reference existence, symbol resolution, digests, and excerpt matching.
func TestTaskReadinessReferences(t *testing.T) {
	tmpDir, pkg := setupValidReadinessFixture(t)

	// Nonexistent referenced file produces REFERENCE_MISSING
	missingRefPkg := cloneWorkPackage(pkg)
	missingRefPkg.Tasks[0].References = []Reference{
		{Path: "nonexistent.go", Symbol: "Foo", SHA256: "0000000000000000000000000000000000000000000000000000000000000000"},
	}
	diags := ValidateTaskReadiness(tmpDir, missingRefPkg)
	foundMissing := false
	for _, d := range diags {
		if d.Code == DiagReferenceMissing {
			foundMissing = true
			break
		}
	}
	if !foundMissing {
		t.Errorf("expected REFERENCE_MISSING for nonexistent file, got: %+v", diags)
	}

	// AC-002: Nonexistent symbol in Go file produces REFERENCE_SYMBOL_MISSING
	badSymPkg := cloneWorkPackage(pkg)
	badSymPkg.Tasks[0].References[0].Symbol = "NonExistentMethod"
	diags = ValidateTaskReadiness(tmpDir, badSymPkg)
	foundBadSym := false
	for _, d := range diags {
		if d.Code == DiagReferenceSymbolMissing && strings.Contains(d.Message, "NonExistentMethod") {
			foundBadSym = true
			break
		}
	}
	if !foundBadSym {
		t.Errorf("expected REFERENCE_SYMBOL_MISSING for NonExistentMethod, got: %+v", diags)
	}

	// Modified file content / stale sha256 produces REFERENCE_STALE
	staleDigestPkg := cloneWorkPackage(pkg)
	staleDigestPkg.Tasks[0].References[0].SHA256 = "1111111111111111111111111111111111111111111111111111111111111111"
	diags = ValidateTaskReadiness(tmpDir, staleDigestPkg)
	foundStaleDigest := false
	for _, d := range diags {
		if d.Code == DiagReferenceStale {
			foundStaleDigest = true
			break
		}
	}
	if !foundStaleDigest {
		t.Errorf("expected REFERENCE_STALE for mismatched sha256, got: %+v", diags)
	}

	// Stale excerpt produces REFERENCE_STALE
	staleExcerptPkg := cloneWorkPackage(pkg)
	staleExcerptPkg.Tasks[0].References[0].Excerpt = "this snippet is not in engine.go"
	diags = ValidateTaskReadiness(tmpDir, staleExcerptPkg)
	foundStaleExcerpt := false
	for _, d := range diags {
		if d.Code == DiagReferenceStale && strings.Contains(d.Message, "excerpt") {
			foundStaleExcerpt = true
			break
		}
	}
	if !foundStaleExcerpt {
		t.Errorf("expected REFERENCE_STALE for missing excerpt, got: %+v", diags)
	}
}

// TestTaskReadinessScope tests task file scope limits, placeholders vs examples, and unresolved decisions.
func TestTaskReadinessScope(t *testing.T) {
	tmpDir, pkg := setupValidReadinessFixture(t)

	// AC-002: Task with more than five unique edit/create files produces TASK_SCOPE_TOO_LARGE
	sixFilePkg := cloneWorkPackage(pkg)
	sixFilePkg.Tasks[0].Files.Create = []string{
		"f1.go", "f2.go", "f3.go", "f4.go", "f5.go", "f6.go",
	}
	diags := ValidateTaskReadiness(tmpDir, sixFilePkg)
	foundScope := false
	for _, d := range diags {
		if d.Code == DiagTaskScopeTooLarge {
			foundScope = true
			break
		}
	}
	if !foundScope {
		t.Errorf("expected TASK_SCOPE_TOO_LARGE for 6 files, got: %+v", diags)
	}

	// AC-003: Legitimate prose mention 'the input string is TODO' inside an example is NOT a blocker
	exampleProsePkg := cloneWorkPackage(pkg)
	exampleProsePkg.Tasks[0].Examples = []Example{
		{
			Input:       "the input string is TODO",
			Output:      "processed",
			Explanation: "TODO in example explanation is not a normative blocker",
		},
	}
	diags = ValidateTaskReadiness(tmpDir, exampleProsePkg)
	if len(diags) != 0 {
		t.Fatalf("expected 0 diagnostics when TODO is in an example, got %d: %+v", len(diags), diags)
	}

	// Normative placeholder in why produces SPEC_NOT_READY
	placeholderPkg := cloneWorkPackage(pkg)
	placeholderPkg.Tasks[0].Why = "TODO: determine why this task is needed"
	diags = ValidateTaskReadiness(tmpDir, placeholderPkg)
	foundPlaceholder := false
	for _, d := range diags {
		if d.Code == DiagSpecNotReady && strings.Contains(d.Path, "why") {
			foundPlaceholder = true
			break
		}
	}
	if !foundPlaceholder {
		t.Errorf("expected SPEC_NOT_READY for TODO in why, got: %+v", diags)
	}

	// Unresolved decisions produce UNRESOLVED_DECISION
	unresolvedPkg := cloneWorkPackage(pkg)
	unresolvedPkg.UnresolvedDecisions = []string{"D-002: Database selection pending"}
	diags = ValidateTaskReadiness(tmpDir, unresolvedPkg)
	foundUnresolved := false
	for _, d := range diags {
		if d.Code == DiagUnresolvedDecision {
			foundUnresolved = true
			break
		}
	}
	if !foundUnresolved {
		t.Errorf("expected UNRESOLVED_DECISION, got: %+v", diags)
	}

	// Check with no assertions produces CHECK_ASSERTIONS_MISSING
	noAssertionsPkg := cloneWorkPackage(pkg)
	noAssertionsPkg.Tasks[0].Checks[0].Assertions = []Assertion{}
	diags = ValidateTaskReadiness(tmpDir, noAssertionsPkg)
	foundNoAssert := false
	for _, d := range diags {
		if d.Code == DiagCheckAssertionsMissing {
			foundNoAssert = true
			break
		}
	}
	if !foundNoAssert {
		t.Errorf("expected CHECK_ASSERTIONS_MISSING, got: %+v", diags)
	}
}
