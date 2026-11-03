package goals

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func setupRunnerTestRepo(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	slug := "runner-test-goal"

	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\nOutput: %s", args, err, string(out))
		}
	}

	run("init", "-b", "main")
	run("config", "user.name", "Runner Tester")
	run("config", "user.email", "runner@sila.local")

	coreSrc := "package core\nfunc Run() int { return 0 }\n"
	if err := os.WriteFile(filepath.Join(dir, "core.go"), []byte(coreSrc), 0644); err != nil {
		t.Fatal(err)
	}
	hCore := sha256.Sum256([]byte(coreSrc))
	coreSHA := hex.EncodeToString(hCore[:])

	run("add", "core.go")
	run("commit", "-m", "init core")

	goalDir := filepath.Join(dir, "goals", slug)
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		t.Fatal(err)
	}

	pkg := &WorkPackage{
		SchemaVersion: WorkPackageSchemaVersion,
		GoalSlug:      slug,
		Revision:      1,
		Purpose:       "Verify check runner assertions, cancellation, and capabilities",
		Constraints:   []string{"pure-Go stdlib"},
		NonGoals:      []string{"no external deps"},
		Tasks: []TaskContract{
			{
				ID:        "T-001",
				Title:     "Run Assertions",
				Context:   "Testing assertion types",
				Why:       "Verify assertion runner",
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
						Symbol:  "Run",
						Excerpt: "func Run() int",
						SHA256:  coreSHA,
					},
				},
				Decisions:     []Decision{},
				Steps:         []Step{{ID: "S-001", Action: "Test", Expected: "Pass", OnFailure: "Halt"}},
				Interfaces:    []string{},
				Constraints:   []string{},
				Examples:      []Example{},
				AcceptanceIDs: []string{"AC-001", "AC-002", "AC-003"},
				Checks: []Check{
					{
						ID:               "C-001",
						Executable:       "echo",
						Args:             []string{`{"Action":"pass","Package":"github.com/pkg","Test":"TestPass"}`},
						Cwd:              ".",
						TimeoutSeconds:   10,
						ExpectedExitCode: 0,
						Scope:            ScopeUnit,
						Assertions: []Assertion{
							{Kind: AssertionGoTestPass, Target: "github.com/pkg/TestPass", Expected: true},
						},
						Capability: CapabilityLocal,
					},
					{
						ID:               "C-002",
						Executable:       "sh",
						Args:             []string{"-c", "echo 'failed' >&2; exit 2"},
						Cwd:              ".",
						TimeoutSeconds:   10,
						ExpectedExitCode: 2,
						Scope:            ScopeUnit,
						Assertions: []Assertion{
							{Kind: AssertionStdoutExact, Target: "stdout", Expected: "failed"},
						},
						Capability: CapabilityLocal,
					},
					{
						ID:               "C-003",
						Executable:       "echo",
						Args:             []string{`{"status":"ok","count":42}`},
						Cwd:              ".",
						TimeoutSeconds:   10,
						ExpectedExitCode: 0,
						Scope:            ScopeUnit,
						Assertions: []Assertion{
							{Kind: AssertionJSONPointerEquals, Target: "/status", Expected: "ok"},
							{Kind: AssertionJSONPointerEquals, Target: "/count", Expected: float64(42)},
						},
						Capability: CapabilityLocal,
					},
				},
				StopConditions: []string{"timeout"},
				ReturnFormat:   "sila/task-result/v1",
			},
			{
				ID:        "T-002",
				Title:     "Dependent Task",
				Context:   "Dependent task context",
				Why:       "Verify prerequisite blocker",
				DependsOn: []string{"T-001"},
				Files: TaskFiles{
					Read:   []string{"core.go"},
					Edit:   []string{"core.go"},
					Create: []string{},
					Forbid: []string{"research/*"},
				},
				References: []Reference{
					{
						Path:    "core.go",
						Symbol:  "Run",
						Excerpt: "func Run() int",
						SHA256:  coreSHA,
					},
				},
				Decisions:     []Decision{},
				Steps:         []Step{{ID: "S-002", Action: "Dependent test", Expected: "Pass", OnFailure: "Halt"}},
				Interfaces:    []string{},
				Constraints:   []string{},
				Examples:      []Example{},
				AcceptanceIDs: []string{"AC-004"},
				Checks: []Check{
					{
						ID:               "C-004",
						Executable:       "echo",
						Args:             []string{"done"},
						Cwd:              ".",
						TimeoutSeconds:   10,
						ExpectedExitCode: 0,
						Scope:            ScopeUnit,
						Assertions: []Assertion{
							{Kind: AssertionStdoutExact, Target: "stdout", Expected: "done"},
						},
						Capability: CapabilityLocal,
					},
				},
				StopConditions: []string{"timeout"},
				ReturnFormat:   "sila/task-result/v1",
			},
			{
				ID:        "T-003",
				Title:     "External Capabilities",
				Context:   "External capabilities context",
				Why:       "Verify capabilities",
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
						Symbol:  "Run",
						Excerpt: "func Run() int",
						SHA256:  coreSHA,
					},
				},
				Decisions:     []Decision{},
				Steps:         []Step{{ID: "S-003", Action: "Cap test", Expected: "Pass", OnFailure: "Halt"}},
				Interfaces:    []string{},
				Constraints:   []string{},
				Examples:      []Example{},
				AcceptanceIDs: []string{"AC-005"},
				Checks: []Check{
					{
						ID:               "C-005",
						Executable:       "curl",
						Args:             []string{"https://api.example.com"},
						Cwd:              ".",
						TimeoutSeconds:   10,
						ExpectedExitCode: 0,
						Scope:            ScopeProvider,
						Assertions:       []Assertion{},
						Capability:       CapabilityProvider,
					},
				},
				StopConditions: []string{"timeout"},
				ReturnFormat:   "sila/task-result/v1",
			},
		},
		Acceptance: []AcceptanceCase{
			{
				ID:            "AC-001",
				Requirement:   "Go test pass event",
				Given:         "Go test runner",
				Action:        "Check event",
				Expected:      "Pass",
				RequiredScope: ScopeUnit,
				Mandatory:     true,
				CheckIDs:      []string{"C-001"},
			},
			{
				ID:            "AC-002",
				Requirement:   "Negative exit code with error message",
				Given:         "Negative case",
				Action:        "Execute error check",
				Expected:      "Exit 2",
				RequiredScope: ScopeUnit,
				Mandatory:     true,
				CheckIDs:      []string{"C-002"},
			},
			{
				ID:            "AC-003",
				Requirement:   "JSON pointer assertions",
				Given:         "JSON output",
				Action:        "Verify pointer",
				Expected:      "Equal",
				RequiredScope: ScopeUnit,
				Mandatory:     true,
				CheckIDs:      []string{"C-003"},
			},
			{
				ID:            "AC-004",
				Requirement:   "Dependent task requirement",
				Given:         "Prerequisite",
				Action:        "Check",
				Expected:      "Pass",
				RequiredScope: ScopeUnit,
				Mandatory:     true,
				CheckIDs:      []string{"C-004"},
			},
			{
				ID:            "AC-005",
				Requirement:   "Provider requirement",
				Given:         "Remote service",
				Action:        "Check",
				Expected:      "Pass",
				RequiredScope: ScopeProvider,
				Mandatory:     true,
				CheckIDs:      []string{"C-005"},
			},
		},
		DependencyConditions: []DependencyCondition{},
		UnresolvedDecisions:  []string{},
		RetiredTaskIDs:       []string{},
		RetiredAcceptanceIDs: []string{},
	}

	pkgBytes, err := json.MarshalIndent(pkg, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goalDir, "work-package.json"), pkgBytes, 0644); err != nil {
		t.Fatal(err)
	}
	run("add", "goals/"+slug+"/work-package.json")
	run("commit", "-m", "add work package")

	return dir, slug
}

func TestTaskCheckRunnerAssertions(t *testing.T) {
	dir, slug := setupRunnerTestRepo(t)
	ctx := context.Background()

	// 1. Run T-001 checks
	results, err := RunTaskChecks(ctx, dir, slug, "T-001")
	if err != nil {
		t.Fatalf("RunTaskChecks T-001: %v", err)
	}

	if len(results) != 3 {
		t.Fatalf("expected 3 evidence results for T-001, got %d", len(results))
	}

	// AC-007: C-001 - All named tests passed yields pass
	r1 := results[0]
	if r1.CheckID != "C-001" || r1.Outcome != OutcomePass {
		t.Errorf("AC-007 failed: expected C-001 pass, got check=%s outcome=%s fail_code=%s", r1.CheckID, r1.Outcome, r1.FailureCode)
	}

	// AC-008: C-002 - Expected exit 2 plus matching declared error assertion passes a negative case
	r2 := results[1]
	if r2.CheckID != "C-002" || r2.Outcome != OutcomePass {
		t.Errorf("AC-008 failed: expected C-002 pass for expected exit 2, got outcome=%s fail_code=%s", r2.Outcome, r2.FailureCode)
	}
	if r2.ExitCode == nil || *r2.ExitCode != 2 {
		t.Errorf("AC-008 failed: expected ExitCode 2, got %v", r2.ExitCode)
	}

	// C-003: JSON pointer assertion passes
	r3 := results[2]
	if r3.CheckID != "C-003" || r3.Outcome != OutcomePass {
		t.Errorf("expected C-003 JSON pointer pass, got outcome=%s fail_code=%s", r3.Outcome, r3.FailureCode)
	}

	// AC-007 Negative Case: Exit 0 with zero expected test events fails with ASSERTION_NOT_OBSERVED
	evalObs, failCode := evaluateAssertion(Assertion{
		Kind:     AssertionGoTestPass,
		Target:   "github.com/pkg/TestNonExistent",
		Expected: true,
	}, []byte(`{"Action":"pass","Test":"TestOther"}`), 0)

	if evalObs.Passed {
		t.Errorf("AC-007 failed: expected assertion to fail when target test not observed")
	}
	if failCode != FailureCodeAssertionNotObserved {
		t.Errorf("AC-007 failed: expected failCode %q, got %q", FailureCodeAssertionNotObserved, failCode)
	}
}

func TestTaskCheckRunnerCancellation(t *testing.T) {
	dir, slug := setupRunnerTestRepo(t)

	// Update work package with timeout check and missing executable check
	pkgPath := filepath.Join(dir, "goals", slug, "work-package.json")
	raw, err := os.ReadFile(pkgPath)
	if err != nil {
		t.Fatal(err)
	}
	var pkg WorkPackage
	if err := json.Unmarshal(raw, &pkg); err != nil {
		t.Fatal(err)
	}

	pkg.Tasks = []TaskContract{pkg.Tasks[0]}

	pkg.Tasks[0].Checks = []Check{
		{
			ID:               "C-010",
			Executable:       "sleep",
			Args:             []string{"10"},
			Cwd:              ".",
			TimeoutSeconds:   1, // 1-second timeout
			ExpectedExitCode: 0,
			Scope:            ScopeUnit,
			Assertions:       []Assertion{},
			Capability:       CapabilityLocal,
		},
		{
			ID:               "C-011",
			Executable:       "missing_non_existent_binary_xyz_123",
			Args:             []string{},
			Cwd:              ".",
			TimeoutSeconds:   5,
			ExpectedExitCode: 0,
			Scope:            ScopeUnit,
			Assertions:       []Assertion{},
			Capability:       CapabilityLocal,
		},
	}
	pkg.Tasks[0].AcceptanceIDs = []string{"AC-001"}
	pkg.Acceptance = []AcceptanceCase{
		{
			ID:            "AC-001",
			Requirement:   "Timeout and missing executable",
			Given:         "Test suite",
			Action:        "Execute checks",
			Expected:      "Failure codes",
			RequiredScope: ScopeUnit,
			Mandatory:     true,
			CheckIDs:      []string{"C-010", "C-011"},
		},
	}

	updatedBytes, _ := json.MarshalIndent(pkg, "", "  ")
	_ = os.WriteFile(pkgPath, updatedBytes, 0644)
	exec.Command("git", "-C", dir, "add", "goals/"+slug+"/work-package.json").Run()
	exec.Command("git", "-C", dir, "commit", "-m", "update with timeout and missing checks").Run()

	ctx := context.Background()
	results, err := RunTaskChecks(ctx, dir, slug, "T-001")
	if err != nil {
		t.Fatalf("RunTaskChecks: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	// AC-009: Timeout check outcome fails with TIMEOUT
	rTimeout := results[0]
	if rTimeout.Outcome == OutcomePass {
		t.Errorf("AC-009 failed: expected timeout check to fail, got pass")
	}
	if rTimeout.FailureCode != FailureCodeTimeout {
		t.Errorf("AC-009 failed: expected FailureCode %q, got %q", FailureCodeTimeout, rTimeout.FailureCode)
	}

	// AC-009: Missing executable fails with LAUNCH_FAILED
	rMissing := results[1]
	if rMissing.Outcome == OutcomePass {
		t.Errorf("AC-009 failed: expected missing executable check to fail, got pass")
	}
	if rMissing.FailureCode != FailureCodeLaunchFailed {
		t.Errorf("AC-009 failed: expected FailureCode %q, got %q", FailureCodeLaunchFailed, rMissing.FailureCode)
	}
}

func TestTaskCheckRunnerCapabilities(t *testing.T) {
	dir, slug := setupRunnerTestRepo(t)
	ctx := context.Background()

	// 1. Dependent task (T-002) returns stage-1 blocker DEPENDENT_TASKS_UNSUPPORTED
	resDep, err := RunTaskChecks(ctx, dir, slug, "T-002")
	if err != nil {
		t.Fatalf("RunTaskChecks T-002: %v", err)
	}
	if len(resDep) == 0 {
		t.Fatalf("expected evidence result for T-002")
	}
	if resDep[0].Outcome != OutcomeBlocked || resDep[0].FailureCode != FailureCodeDependentTaskBlocker {
		t.Errorf("expected outcome blocked with %s for dependent task, got outcome=%s fail_code=%s",
			FailureCodeDependentTaskBlocker, resDep[0].Outcome, resDep[0].FailureCode)
	}

	// 2. Non-local capability (T-003, provider check) returns OutcomeBlocked with UNMET_CAPABILITY
	resCap, err := RunTaskChecks(ctx, dir, slug, "T-003")
	if err != nil {
		t.Fatalf("RunTaskChecks T-003: %v", err)
	}
	if len(resCap) == 0 {
		t.Fatalf("expected evidence result for T-003")
	}
	if resCap[0].Outcome != OutcomeBlocked || resCap[0].FailureCode != FailureCodeUnmetCapability {
		t.Errorf("expected outcome blocked with %s for provider check, got outcome=%s fail_code=%s",
			FailureCodeUnmetCapability, resCap[0].Outcome, resCap[0].FailureCode)
	}
}
