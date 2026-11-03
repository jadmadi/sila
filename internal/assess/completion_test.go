package assess

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jadmadi/sila/internal/goals"
)

func TestAuditWholeTree(t *testing.T) {
	tempDir := t.TempDir()

	// 1. Create a mini workspace with packages pkgA and pkgB
	pkgA := filepath.Join(tempDir, "pkgA")
	pkgB := filepath.Join(tempDir, "pkgB")
	_ = os.MkdirAll(pkgA, 0755)
	_ = os.MkdirAll(pkgB, 0755)
	_ = os.WriteFile(filepath.Join(pkgA, "a.go"), []byte("package pkgA\n"), 0644)
	_ = os.WriteFile(filepath.Join(pkgB, "b.go"), []byte("package pkgB\n"), 0644)

	// Incomplete test stream missing pkgB
	incompleteOutput := "ok  \tgithub.com/example/repo/pkgA\t0.05s\n"
	resIncomplete := AuditWholeTree(tempDir, incompleteOutput)
	if resIncomplete.Passed {
		t.Fatalf("expected whole-tree audit to fail for missing pkgB")
	}
	if len(resIncomplete.SkippedPackages) != 1 || resIncomplete.SkippedPackages[0] != "pkgB" {
		t.Errorf("expected SkippedPackages=[pkgB], got %v", resIncomplete.SkippedPackages)
	}

	// Complete test stream covering both
	completeOutput := "ok  \tgithub.com/example/repo/pkgA\t0.05s\nok  \tgithub.com/example/repo/pkgB\t0.08s\n"
	resComplete := AuditWholeTree(tempDir, completeOutput)
	if !resComplete.Passed {
		t.Fatalf("expected whole-tree audit to pass for complete run: %s", resComplete.Details)
	}
	if resComplete.PackagesTested != 2 {
		t.Errorf("expected 2 packages tested, got %d", resComplete.PackagesTested)
	}
}

func TestInspectStreams_SkipsAndSuppressionDetection(t *testing.T) {
	// Clean output
	cleanStdout := "=== RUN   TestClean\n--- PASS: TestClean (0.01s)\nPASS\nok  \tpkg\t0.02s\n"
	cleanStderr := ""
	cleanRes := InspectStreams(cleanStdout, cleanStderr)
	if !cleanRes.Passed {
		t.Fatalf("expected clean stream to pass, got: %s", cleanRes.Details)
	}

	// Output with skipped tests
	skipStdout := "=== RUN   TestFeature\n--- SKIP: TestFeature (0.00s)\n    feature_test.go:12: skipped for now\nPASS\n"
	skipRes := InspectStreams(skipStdout, "")
	if skipRes.Passed {
		t.Fatalf("expected stream inspect to fail on skipped test")
	}
	if skipRes.SkippedTests != 1 {
		t.Errorf("expected 1 skipped test, got %d", skipRes.SkippedTests)
	}

	// Output with linter suppressions and silent warnings
	lintStderr := "internal/assess/eval.go:10: //nolint:errcheck\nwarning: deprecated API called in pkg/old.go\n"
	lintRes := InspectStreams("", lintStderr)
	if lintRes.Passed {
		t.Fatalf("expected stream inspect to fail on linter suppression and warning")
	}
	if lintRes.LinterIgnores != 1 {
		t.Errorf("expected 1 linter ignore, got %d", lintRes.LinterIgnores)
	}
	if lintRes.SilentWarnings != 1 {
		t.Errorf("expected 1 silent warning, got %d", lintRes.SilentWarnings)
	}
}

func TestReconcileRequirements_HollowStubsAndMissingFiles(t *testing.T) {
	tempDir := t.TempDir()
	goalSlug := "test-reconciliation-goal"
	goalDir := filepath.Join(tempDir, "goals", goalSlug)
	_ = os.MkdirAll(goalDir, 0755)

	goalMd := `# Goal: Test Reconciliation Goal

## Files to Touch
- pkg/feature.go
- pkg/missing.go

## Strict Acceptance Criteria
1. First capability implemented without stubs.
2. Second capability fully tested.
`
	_ = os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte(goalMd), 0644)

	// Create pkg/feature.go with a hollow stub
	pkgDir := filepath.Join(tempDir, "pkg")
	_ = os.MkdirAll(pkgDir, 0755)

	stubContent := `package pkg

func StubMethod() {
	panic("TODO: implement me")
}
`
	_ = os.WriteFile(filepath.Join(pkgDir, "feature.go"), []byte(stubContent), 0644)

	res := ReconcileRequirements(tempDir, goalSlug, []string{filepath.Join(pkgDir, "feature.go")})
	if res.Passed {
		t.Fatalf("expected reconciliation to fail due to missing file and hollow stub")
	}
	if len(res.MissingTouchedFiles) != 1 || res.MissingTouchedFiles[0] != "pkg/missing.go" {
		t.Errorf("expected missing touched file pkg/missing.go, got %v", res.MissingTouchedFiles)
	}
	if len(res.HollowStubsDetected) == 0 {
		t.Errorf("expected hollow stubs detected, got 0")
	}
}

func TestAuditBlastRadius_CollateralLeakDetection(t *testing.T) {
	tempDir := t.TempDir()
	goalSlug := "test-blast-radius-goal"
	goalDir := filepath.Join(tempDir, "goals", goalSlug)
	_ = os.MkdirAll(goalDir, 0755)

	goalMd := `# Goal: Test Blast Radius

## Files to Touch
- internal/core/handler.go
`
	_ = os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte(goalMd), 0644)

	// Modified files within boundary + auxiliary files
	cleanFiles := []string{
		"internal/core/handler.go",
		"CHANGELOG.md",
		"goals/test-blast-radius-goal/facts.md",
	}
	cleanRes := AuditBlastRadius(tempDir, goalSlug, cleanFiles)
	if !cleanRes.Passed {
		t.Fatalf("expected clean blast radius to pass, got: %s", cleanRes.Details)
	}

	// Modified files with collateral leak into unrelated module
	leakyFiles := []string{
		"internal/core/handler.go",
		"worker/src/index.ts", // Collateral leak!
	}
	leakyRes := AuditBlastRadius(tempDir, goalSlug, leakyFiles)
	if leakyRes.Passed {
		t.Fatalf("expected blast radius audit to fail for collateral leak into worker/src/index.ts")
	}
	if len(leakyRes.CollateralLeaks) != 1 || leakyRes.CollateralLeaks[0] != "worker/src/index.ts" {
		t.Errorf("expected CollateralLeaks=[worker/src/index.ts], got %v", leakyRes.CollateralLeaks)
	}
}

func TestAuditIncompletenessDisclosure_HonestVsSilentOmission(t *testing.T) {
	tempDir := t.TempDir()
	goalSlug := "test-disclosure-goal"
	goalDir := filepath.Join(tempDir, "goals", goalSlug)
	_ = os.MkdirAll(goalDir, 0755)

	factsPath := filepath.Join(goalDir, "facts.md")
	goalMdPath := filepath.Join(goalDir, "goal.md")

	_ = os.WriteFile(goalMdPath, []byte("# Goal\n"), 0644)

	// 1. Silent omission: defect exists without disclosure in facts.md
	silentFacts := "# Facts\n## Invariants\n- Standard invariant\n"
	_ = os.WriteFile(factsPath, []byte(silentFacts), 0644)

	defects := []string{"skipped test: TestEdgeLatency"}
	silentRes := AuditIncompletenessDisclosure(factsPath, goalMdPath, defects)
	if silentRes.Passed {
		t.Fatalf("expected silent omission to fail disclosure audit")
	}
	if len(silentRes.UndisclosedSkips) != 1 {
		t.Errorf("expected 1 undisclosed skip, got %d", len(silentRes.UndisclosedSkips))
	}

	// 2. Honest disclosure: explicitly documented in facts.md
	honestFacts := `# Facts

## Known Limitations
- Skipped test: TestEdgeLatency is deferred due to offline sandbox network isolation.
`
	_ = os.WriteFile(factsPath, []byte(honestFacts), 0644)

	honestRes := AuditIncompletenessDisclosure(factsPath, goalMdPath, defects)
	if !honestRes.Passed {
		t.Fatalf("expected honest disclosure to pass audit, got: %s", honestRes.Details)
	}
}

func TestVerifyRuleOfFive_EndToEnd(t *testing.T) {
	tempDir := t.TempDir()
	goalSlug := "test-rule-of-five-e2e"
	goalDir := filepath.Join(tempDir, "goals", goalSlug)
	_ = os.MkdirAll(goalDir, 0755)

	pkgDir := filepath.Join(tempDir, "pkg")
	_ = os.MkdirAll(pkgDir, 0755)

	codeContent := `package pkg

// ProperFunction implements non-trivial verified logic.
func ProperFunction() string {
	return "verified"
}
`
	_ = os.WriteFile(filepath.Join(pkgDir, "feature.go"), []byte(codeContent), 0644)

	goalMd := `# Goal: E2E Rule of Five

## Files to Touch
- pkg/feature.go

## Strict Acceptance Criteria
1. ProperFunction returns verified string.
`
	_ = os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte(goalMd), 0644)
	_ = os.WriteFile(filepath.Join(goalDir, "facts.md"), []byte("# Facts\n"), 0644)

	stdout := "ok  \tpkg\t0.05s\n"
	stderr := ""
	modified := []string{"pkg/feature.go"}

	rep := VerifyRuleOfFive(tempDir, goalSlug, stdout, stderr, modified, RuleOfFiveOptions{})
	if !rep.Clean {
		t.Fatalf("expected clean Rule of Five report, got violations: %v", rep.Violations)
	}
	if !rep.WholeTree.Passed || !rep.StreamInspect.Passed || !rep.Reconciliation.Passed ||
		!rep.BlastRadius.Passed || !rep.Disclosure.Passed {
		t.Errorf("expected all 5 points to pass, got %+v", rep)
	}
}

func TestAuditBlastRadius_GlobAllowPaths(t *testing.T) {
	tempDir := t.TempDir()
	slug := "sila-onboarding"
	goalDir := filepath.Join(tempDir, "goals", slug)
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		t.Fatal(err)
	}
	ch := &goals.GoalCharter{
		GoalSlug:   slug,
		Agent:      "command-code",
		AllowPaths: []string{"AGENTS.md", ".gitignore", "goals/*", "tests/*"},
	}
	if err := goals.SaveCharter(tempDir, ch); err != nil {
		t.Fatal(err)
	}

	res := AuditBlastRadius(tempDir, slug, []string{"AGENTS.md", ".gitignore", "tests/test_telemetry_helpers.py", "goals/sila-onboarding/evidence.md"})
	if !res.Passed {
		t.Fatalf("expected glob allow_paths to pass AuditBlastRadius, got leaks: %v", res.CollateralLeaks)
	}
}
