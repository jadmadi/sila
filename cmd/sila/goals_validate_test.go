package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/render"
)

func createTestGoalPkg(t *testing.T, root, slug, goalMD, factsMD, planMD string) {
	t.Helper()
	gDir := filepath.Join(root, "goals", slug)
	if err := os.MkdirAll(gDir, 0755); err != nil {
		t.Fatal(err)
	}
	if goalMD != "" {
		_ = os.WriteFile(filepath.Join(gDir, "goal.md"), []byte(goalMD), 0644)
	}
	if factsMD != "" {
		_ = os.WriteFile(filepath.Join(gDir, "facts.md"), []byte(factsMD), 0644)
	}
	if planMD != "" {
		_ = os.WriteFile(filepath.Join(gDir, "plan.md"), []byte(planMD), 0644)
	}
}

func TestCLICreateAndUpdateSpecReview(t *testing.T) {
	dir := initClosureGitRepo(t)
	slug := "cli-spec-review-goal"
	wp := makeTestWorkPackage(slug)

	wpData, err := json.MarshalIndent(wp, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "work-package.json"), wpData, 0644); err != nil {
		t.Fatal(err)
	}

	digest, err := goals.WorkPackageDigest(wp)
	if err != nil {
		t.Fatal(err)
	}
	review := goals.SpecReview{
		SchemaVersion: goals.SpecReviewSchemaVersion,
		SpecDigest:    digest,
		Reviewer:      "strong-planner-agent",
		ReviewedAt:    "2026-10-20T00:00:00Z",
		Verdict:       goals.VerdictReady,
		Shortcuts: []goals.ShortcutCounter{
			{IncorrectImplementation: "swallow errors", ExcludedByIDs: []string{"T-001"}, Resolution: "T-001 requires explicit error returns"},
			{IncorrectImplementation: "skip validation", ExcludedByIDs: []string{"T-001", "AC-001"}, Resolution: "AC-001 validates the output"},
			{IncorrectImplementation: "hardcode paths", ExcludedByIDs: []string{"AC-001"}, Resolution: "AC-001 checks repository-relative paths"},
		},
		UnresolvedBlockers: []string{},
	}
	reviewData, err := json.MarshalIndent(review, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "spec-review.json"), reviewData, 0644); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := runGoalsClosureCLI(t, dir,
		"goals", "--project="+dir, "create", slug,
		"--work-package=work-package.json",
		"--files=README.md",
		"--spec-review=spec-review.json",
		"--json",
	)
	if code != 0 {
		t.Fatalf("create with spec review failed: %d, stderr: %s, stdout: %s", code, stderr, stdout)
	}
	var createResp map[string]any
	if err := json.Unmarshal([]byte(stdout), &createResp); err != nil {
		t.Fatalf("parse create JSON: %v, raw: %s", err, stdout)
	}
	if createResp["created"] != true {
		t.Fatalf("expected created=true, got %v", createResp)
	}
	if _, err := os.Stat(filepath.Join(dir, "goals", slug, "spec-review.json")); err != nil {
		t.Fatalf("spec-review.json was not persisted on create: %v", err)
	}

	stdout, stderr, code = runGoalsClosureCLI(t, dir,
		"goals", "--project="+dir, "update", slug,
		"--spec-review=spec-review.json",
		"--json",
	)
	if code != 0 {
		t.Fatalf("update with spec review failed: %d, stderr: %s, stdout: %s", code, stderr, stdout)
	}
	var updateResp map[string]any
	if err := json.Unmarshal([]byte(stdout), &updateResp); err != nil {
		t.Fatalf("parse update JSON: %v, raw: %s", err, stdout)
	}
	if updateResp["updated"] != true {
		t.Fatalf("expected updated=true, got %v", updateResp)
	}
	loaded, err := goals.LoadSpecReview(dir, slug)
	if err != nil || loaded == nil {
		t.Fatalf("LoadSpecReview: %v", err)
	}
	if loaded.SpecDigest != digest {
		t.Errorf("expected spec digest %s, got %s", digest, loaded.SpecDigest)
	}
}

func TestCLI_GoalsValidate_JSONMode(t *testing.T) {
	tmp := t.TempDir()
	createTestGoalPkg(t, tmp, "cli-goal-1",
		"# CLI Goal 1\n- **Sequence**: 1\n- **Tier**: immediate\n- **Shape**: ship\n",
		"# Facts\n", "# Plan\n- [ ] Task\n")
	createTestGoalPkg(t, tmp, "cli-goal-2",
		"# CLI Goal 2\n- **Sequence**: 2\n- **Tier**: roadmap\n- **Depends On**: cli-goal-1\n",
		"# Facts\n", "# Plan\n- [ ] Task\n")

	audit, err := goals.ScanGoals(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if err := goals.SyncReadmeWithOpts(tmp, audit, goals.SyncReadmeOptions{}); err != nil {
		t.Fatal(err)
	}

	// Capture stdout
	ui := render.New(os.Stdout, true)
	out := captureStdout(t, func() {
		runGoalsValidate(ui, tmp, []string{"validate", "--json"}, true, false, false, "", false)
	})

	var rep goals.ValidationReport
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("failed to parse JSON validation report: %v, raw: %s", err, out)
	}

	if !rep.Valid {
		t.Errorf("expected valid report, got invalid: %+v", rep)
	}
	if rep.CheckedGoals != 2 {
		t.Errorf("expected 2 checked goals, got %d", rep.CheckedGoals)
	}
	if rep.ErrorsCount != 0 {
		t.Errorf("expected 0 errors, got %d", rep.ErrorsCount)
	}
	if rep.Version != "sila/goals-validate/v1" {
		t.Errorf("expected version sila/goals-validate/v1, got %q", rep.Version)
	}
}

func TestCLI_GoalsValidate_HumanMode(t *testing.T) {
	tmp := t.TempDir()
	createTestGoalPkg(t, tmp, "human-goal",
		"# Human Goal\n- **Tier**: immediate\n",
		"# Facts\n", "# Plan\n- [ ] Task\n")

	audit, err := goals.ScanGoals(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if err := goals.SyncReadmeWithOpts(tmp, audit, goals.SyncReadmeOptions{}); err != nil {
		t.Fatal(err)
	}

	ui := render.New(os.Stdout, true)
	out := captureStdout(t, func() {
		runGoalsValidate(ui, tmp, []string{"validate"}, false, false, false, "", false)
	})

	if !strings.Contains(out, "sila · goals validate") {
		t.Errorf("expected output to contain header, got: %s", out)
	}
	if !strings.Contains(out, "All goal package checks passed") {
		t.Errorf("expected success message, got: %s", out)
	}
}

func TestCLI_GoalsValidate_SingleGoal(t *testing.T) {
	tmp := t.TempDir()
	createTestGoalPkg(t, tmp, "target-goal",
		"# Target Goal\n- **Tier**: immediate\n",
		"# Facts\n", "# Plan\n- [ ] Task\n")
	createTestGoalPkg(t, tmp, "other-goal",
		"# Other Goal\n- **Tier**: roadmap\n",
		"# Facts\n", "# Plan\n")

	audit, err := goals.ScanGoals(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if err := goals.SyncReadmeWithOpts(tmp, audit, goals.SyncReadmeOptions{}); err != nil {
		t.Fatal(err)
	}

	ui := render.New(os.Stdout, true)
	out := captureStdout(t, func() {
		runGoalsValidate(ui, tmp, []string{"validate", "target-goal", "--json"}, true, false, false, "", false)
	})

	var rep goals.ValidationReport
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("failed to parse JSON: %v, raw: %s", err, out)
	}
	if !rep.Valid {
		t.Errorf("expected valid single-goal check, got: %+v", rep)
	}
	if rep.TargetSlug != "target-goal" {
		t.Errorf("expected target_slug 'target-goal', got %q", rep.TargetSlug)
	}
	if rep.CheckedGoals != 1 {
		t.Errorf("expected 1 checked goal, got %d", rep.CheckedGoals)
	}
}

func TestCLI_GoalsValidate_WithManifest(t *testing.T) {
	tmp := t.TempDir()
	createTestGoalPkg(t, tmp, "manifest-target",
		"# Manifest Target\n- **Sequence**: 5\n- **Tier**: immediate\n",
		"# Facts\n", "# Plan\n- [ ] T1\n")

	audit, err := goals.ScanGoals(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if err := goals.SyncReadmeWithOpts(tmp, audit, goals.SyncReadmeOptions{}); err != nil {
		t.Fatal(err)
	}

	manifestPath := filepath.Join(tmp, "manifest.json")
	manifestContent := `{
		"goals": {
			"manifest-target": {
				"status": "pending",
				"sequence": 5,
				"tier": "immediate"
			}
		}
	}`
	if err := os.WriteFile(manifestPath, []byte(manifestContent), 0644); err != nil {
		t.Fatal(err)
	}

	ui := render.New(os.Stdout, true)
	out := captureStdout(t, func() {
		runGoalsValidate(ui, tmp, []string{"validate", "--manifest=" + manifestPath, "--json"}, true, false, false, "", false)
	})

	var rep goals.ValidationReport
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("failed to parse JSON: %v, raw: %s", err, out)
	}
	if !rep.Valid {
		t.Errorf("expected valid manifest check, got diagnostics: %+v", rep.Diagnostics)
	}
}
