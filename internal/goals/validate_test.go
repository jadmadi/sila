package goals

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func createHermeticGoal(t *testing.T, rootDir, slug, goalMD, factsMD, planMD string) {
	t.Helper()
	gDir := filepath.Join(rootDir, "goals", slug)
	if err := os.MkdirAll(gDir, 0755); err != nil {
		t.Fatalf("mkdir %s: %v", gDir, err)
	}
	if goalMD != "" {
		if err := os.WriteFile(filepath.Join(gDir, "goal.md"), []byte(goalMD), 0644); err != nil {
			t.Fatalf("write goal.md: %v", err)
		}
	}
	if factsMD != "" {
		if err := os.WriteFile(filepath.Join(gDir, "facts.md"), []byte(factsMD), 0644); err != nil {
			t.Fatalf("write facts.md: %v", err)
		}
	}
	if planMD != "" {
		if err := os.WriteFile(filepath.Join(gDir, "plan.md"), []byte(planMD), 0644); err != nil {
			t.Fatalf("write plan.md: %v", err)
		}
	}
}

func syncHermeticReadme(t *testing.T, rootDir string) {
	t.Helper()
	audit, err := ScanGoals(rootDir)
	if err != nil {
		t.Fatalf("scan goals: %v", err)
	}
	if err := SyncReadmeWithOpts(rootDir, audit, SyncReadmeOptions{}); err != nil {
		t.Fatalf("sync readme: %v", err)
	}
}

func TestValidateGoals_ValidProject(t *testing.T) {
	tmp := t.TempDir()
	createHermeticGoal(t, tmp, "goal-a",
		"# Goal A\n- **Sequence**: 1\n- **Tier**: immediate\n- **Shape**: ship\n- **Mode**: independent\n",
		"# Facts A\n",
		"# Plan A\n- [ ] Task 1\n- [ ] Task 2\n")
	createHermeticGoal(t, tmp, "goal-b",
		"# Goal B\n- **Sequence**: 2\n- **Tier**: roadmap\n- **Shape**: scout\n- **Depends On**: goal-a\n",
		"# Facts B\n",
		"# Plan B\n- [ ] Task 1\n")

	syncHermeticReadme(t, tmp)

	rep, err := ValidateGoals(tmp, ValidateOptions{})
	if err != nil {
		t.Fatalf("ValidateGoals error: %v", err)
	}
	if !rep.Valid {
		t.Fatalf("expected valid project, got errors: %+v", rep.Diagnostics)
	}
	if rep.CheckedGoals != 2 {
		t.Errorf("expected 2 checked goals, got %d", rep.CheckedGoals)
	}
	if rep.ErrorsCount != 0 {
		t.Errorf("expected 0 errors, got %d", rep.ErrorsCount)
	}
}

func TestValidateGoals_MissingFiles(t *testing.T) {
	tmp := t.TempDir()
	// goal-missing-facts lacks facts.md
	createHermeticGoal(t, tmp, "goal-missing-facts",
		"# Goal Missing Facts\n- **Tier**: immediate\n", "", "# Plan\n- [ ] Task\n")
	// goal-missing-plan lacks plan.md
	createHermeticGoal(t, tmp, "goal-missing-plan",
		"# Goal Missing Plan\n- **Tier**: immediate\n", "# Facts\n", "")

	syncHermeticReadme(t, tmp)

	rep, err := ValidateGoals(tmp, ValidateOptions{})
	if err != nil {
		t.Fatalf("ValidateGoals error: %v", err)
	}
	if rep.Valid {
		t.Fatalf("expected validation failure due to missing files")
	}

	foundFactsMissing, foundPlanMissing := false, false
	for _, d := range rep.Diagnostics {
		if d.Code == DiagMissingFile && d.GoalSlug == "goal-missing-facts" && filepath.Base(d.Path) == "facts.md" {
			foundFactsMissing = true
		}
		if d.Code == DiagMissingFile && d.GoalSlug == "goal-missing-plan" && filepath.Base(d.Path) == "plan.md" {
			foundPlanMissing = true
		}
	}
	if !foundFactsMissing {
		t.Errorf("expected missing facts.md diagnostic for goal-missing-facts")
	}
	if !foundPlanMissing {
		t.Errorf("expected missing plan.md diagnostic for goal-missing-plan")
	}
}

func TestValidateGoals_MetadataAnomalies(t *testing.T) {
	tmp := t.TempDir()

	// 1. Ambiguous Tier vs Priority
	createHermeticGoal(t, tmp, "goal-ambig",
		"# Goal Ambig\n- **Tier**: immediate\n- **Priority**: P3\n",
		"# Facts\n", "# Plan\n")

	// 2. Invalid Tier
	createHermeticGoal(t, tmp, "goal-inv-tier",
		"# Goal Inv Tier\n- **Tier**: super-urgent-asap\n",
		"# Facts\n", "# Plan\n")

	// 3. Invalid Shape
	createHermeticGoal(t, tmp, "goal-inv-shape",
		"# Goal Inv Shape\n- **Tier**: roadmap\n- **Shape**: hyper-space\n",
		"# Facts\n", "# Plan\n")

	// 4. Negative Sequence
	createHermeticGoal(t, tmp, "goal-neg-seq",
		"# Goal Neg Seq\n- **Sequence**: -5\n",
		"# Facts\n", "# Plan\n")

	// 5. Path Escape
	createHermeticGoal(t, tmp, "goal-escape",
		"# Goal Escape\n- **Files to Touch**:\n  - ../secret.key\n  - /etc/passwd\n",
		"# Facts\n", "# Plan\n")

	syncHermeticReadme(t, tmp)

	rep, err := ValidateGoals(tmp, ValidateOptions{})
	if err != nil {
		t.Fatalf("ValidateGoals error: %v", err)
	}

	codes := make(map[string]bool)
	for _, d := range rep.Diagnostics {
		codes[d.Code+"_"+d.GoalSlug] = true
	}

	if !codes[DiagAmbiguousTier+"_goal-ambig"] {
		t.Errorf("expected AMBIGUOUS_TIER for goal-ambig")
	}
	if !codes[DiagInvalidTier+"_goal-inv-tier"] {
		t.Errorf("expected INVALID_TIER for goal-inv-tier")
	}
	if !codes[DiagInvalidShape+"_goal-inv-shape"] {
		t.Errorf("expected INVALID_SHAPE for goal-inv-shape")
	}
	if !codes[DiagInvalidSequence+"_goal-neg-seq"] {
		t.Errorf("expected INVALID_SEQUENCE for goal-neg-seq")
	}
	if !codes[DiagPathEscape+"_goal-escape"] {
		t.Errorf("expected PATH_ESCAPE for goal-escape")
	}
}

func TestValidateGoals_DependenciesAndCycles(t *testing.T) {
	tmp := t.TempDir()

	// 1. Self Dependency
	createHermeticGoal(t, tmp, "goal-self",
		"# Goal Self\n- **Depends On**: goal-self\n",
		"# Facts\n", "# Plan\n")

	// 2. Missing Prerequisite
	createHermeticGoal(t, tmp, "goal-missing-dep",
		"# Goal Missing Dep\n- **Depends On**: nonexistent-parent\n",
		"# Facts\n", "# Plan\n")

	// 3. Cyclic Dependency: c1 -> c2 -> c1
	createHermeticGoal(t, tmp, "goal-c1",
		"# Goal C1\n- **Depends On**: goal-c2\n",
		"# Facts\n", "# Plan\n")
	createHermeticGoal(t, tmp, "goal-c2",
		"# Goal C2\n- **Depends On**: goal-c1\n",
		"# Facts\n", "# Plan\n")

	syncHermeticReadme(t, tmp)

	rep, err := ValidateGoals(tmp, ValidateOptions{})
	if err != nil {
		t.Fatalf("ValidateGoals error: %v", err)
	}

	foundSelf, foundMissing, foundCycle := false, false, false
	for _, d := range rep.Diagnostics {
		if d.Code == DiagSelfDependency && d.GoalSlug == "goal-self" {
			foundSelf = true
		}
		if d.Code == DiagMissingPrerequisite && d.GoalSlug == "goal-missing-dep" {
			foundMissing = true
		}
		if d.Code == DiagCycleDetected {
			foundCycle = true
		}
	}

	if !foundSelf {
		t.Errorf("expected SELF_DEPENDENCY for goal-self")
	}
	if !foundMissing {
		t.Errorf("expected MISSING_PREREQUISITE for goal-missing-dep")
	}
	if !foundCycle {
		t.Errorf("expected CYCLE_DETECTED for circular dependencies")
	}
}

func TestValidateGoals_AttributionAndProvenance(t *testing.T) {
	tmp := t.TempDir()

	// 1. Claim attribution missing
	createHermeticGoal(t, tmp, "goal-bad-claim",
		"# Goal Bad Claim\n", "# Facts\n", "# Plan\n- [ ] T1\n")
	claimPath := filepath.Join(tmp, "goals", "goal-bad-claim", "claim.json")
	_ = os.WriteFile(claimPath, []byte(`{"agent":""}`), 0644)

	// 2. Reopen provenance stale (missing agent and reason)
	createHermeticGoal(t, tmp, "goal-bad-reopen",
		"# Goal Bad Reopen\n", "# Facts\n", "# Plan\n- [ ] T1\n")
	reopenPath := filepath.Join(tmp, "goals", "goal-bad-reopen", "reopen.json")
	_ = os.WriteFile(reopenPath, []byte(`{"active":true,"reopened_by":"","reason":""}`), 0644)

	// 3. Checked tasks without commit attribution
	createHermeticGoal(t, tmp, "goal-uncertified",
		"# Goal Uncertified\n", "# Facts\n", "# Plan\n- [x] Task 1\n- [x] Task 2\n")

	syncHermeticReadme(t, tmp)

	rep, err := ValidateGoals(tmp, ValidateOptions{})
	if err != nil {
		t.Fatalf("ValidateGoals error: %v", err)
	}

	foundClaim, foundReopen, foundTask := false, false, false
	for _, d := range rep.Diagnostics {
		if d.Code == DiagClaimInvalid && d.GoalSlug == "goal-bad-claim" {
			foundClaim = true
		}
		if d.Code == DiagReopenStale && d.GoalSlug == "goal-bad-reopen" {
			foundReopen = true
		}
		if d.Code == DiagTaskInconsistent && d.GoalSlug == "goal-uncertified" {
			foundTask = true
		}
	}

	if !foundClaim {
		t.Errorf("expected CLAIM_ATTRIBUTION_INVALID for goal-bad-claim")
	}
	if !foundReopen {
		t.Errorf("expected REOPEN_PROVENANCE_STALE for goal-bad-reopen")
	}
	if !foundTask {
		t.Errorf("expected TASK_STATE_INCONSISTENT for goal-uncertified")
	}
}

func TestValidateGoals_ExpectedStateManifest(t *testing.T) {
	tmp := t.TempDir()
	createHermeticGoal(t, tmp, "target-goal",
		"# Target Goal\n- **Sequence**: 10\n- **Tier**: immediate\n- **Shape**: ship\n- **Depends On**: dep-a\n",
		"# Facts\n",
		"# Plan\n- [ ] Step 1\n- [ ] Step 2\n")
	createHermeticGoal(t, tmp, "dep-a",
		"# Dep A\n- **Sequence**: 5\n- **Tier**: immediate\n",
		"# Facts\n", "# Plan\n")

	syncHermeticReadme(t, tmp)

	// 1. Positive matching manifest
	expStatus := "pending"
	expSeq := 10
	expTier := "immediate"
	expShape := "ship"
	expTotal := 2
	expDone := 0
	expDeps := []string{"dep-a"}

	validManifest := &ExpectedStateManifest{
		Goals: map[string]ExpectedGoalState{
			"target-goal": {
				Status:       &expStatus,
				Sequence:     &expSeq,
				Tier:         &expTier,
				Shape:        &expShape,
				TotalTasks:   &expTotal,
				DoneTasks:    &expDone,
				Dependencies: &expDeps,
			},
		},
	}

	rep, err := ValidateGoals(tmp, ValidateOptions{ExpectedManifest: validManifest})
	if err != nil {
		t.Fatalf("ValidateGoals error: %v", err)
	}
	if !rep.Valid {
		t.Fatalf("expected valid match, got errors: %+v", rep.Diagnostics)
	}

	// 2. Negative mismatch manifest
	wrongStatus := "implemented"
	wrongSeq := 99
	wrongTier := "wont-fix"
	wrongManifest := &ExpectedStateManifest{
		Goals: map[string]ExpectedGoalState{
			"target-goal": {
				Status:   &wrongStatus,
				Sequence: &wrongSeq,
				Tier:     &wrongTier,
			},
			"ghost-goal": {
				Status: &wrongStatus,
			},
		},
	}

	repBad, err := ValidateGoals(tmp, ValidateOptions{ExpectedManifest: wrongManifest})
	if err != nil {
		t.Fatalf("ValidateGoals error: %v", err)
	}
	if repBad.Valid {
		t.Fatalf("expected validation failure on manifest mismatch")
	}

	mismatchCount := 0
	for _, d := range repBad.Diagnostics {
		if d.Code == DiagExpectedMismatch {
			mismatchCount++
		}
	}
	if mismatchCount < 4 { // status, sequence, tier, and ghost-goal missing
		t.Errorf("expected at least 4 EXPECTED_STATE_MISMATCH diagnostics, got %d", mismatchCount)
	}
}

func TestValidateGoals_SingleGoalMode(t *testing.T) {
	tmp := t.TempDir()
	createHermeticGoal(t, tmp, "good-goal",
		"# Good Goal\n- **Tier**: immediate\n", "# Facts\n", "# Plan\n")
	createHermeticGoal(t, tmp, "broken-goal",
		"# Broken Goal\n- **Tier**: immediate\n", "", "# Plan\n") // missing facts.md

	syncHermeticReadme(t, tmp)

	// Single goal check on good-goal succeeds
	repGood, err := ValidateGoals(tmp, ValidateOptions{Slug: "good-goal"})
	if err != nil {
		t.Fatalf("ValidateGoals error: %v", err)
	}
	if !repGood.Valid || repGood.ErrorsCount != 0 {
		t.Fatalf("expected good-goal to pass validation, got: %+v", repGood.Diagnostics)
	}

	// Single goal check on broken-goal fails
	repBad, err := ValidateGoals(tmp, ValidateOptions{Slug: "broken-goal"})
	if err != nil {
		t.Fatalf("ValidateGoals error: %v", err)
	}
	if repBad.Valid || repBad.ErrorsCount == 0 {
		t.Fatalf("expected broken-goal to fail validation")
	}

	// Nonexistent goal fails
	repNone, err := ValidateGoals(tmp, ValidateOptions{Slug: "nonexistent-goal"})
	if err != nil {
		t.Fatalf("ValidateGoals error: %v", err)
	}
	if repNone.Valid || repNone.ErrorsCount == 0 {
		t.Fatalf("expected nonexistent goal to fail validation")
	}
}

func TestValidateGoals_StrictMode(t *testing.T) {
	tmp := t.TempDir()
	createHermeticGoal(t, tmp, "goal-with-warning",
		"# Goal With Warning\n- **Tier**: immediate\n- **Priority**: P3\n", // causes AMBIGUOUS_TIER warning
		"# Facts\n", "# Plan\n")

	syncHermeticReadme(t, tmp)

	// Non-strict: warnings do not fail
	repNormal, err := ValidateGoals(tmp, ValidateOptions{Strict: false})
	if err != nil {
		t.Fatalf("ValidateGoals error: %v", err)
	}
	if !repNormal.Valid {
		t.Errorf("expected non-strict mode to pass despite warnings")
	}

	// Strict: warnings cause validation failure
	repStrict, err := ValidateGoals(tmp, ValidateOptions{Strict: true})
	if err != nil {
		t.Fatalf("ValidateGoals error: %v", err)
	}
	if repStrict.Valid {
		t.Errorf("expected strict mode to fail due to warnings")
	}
}

func TestValidateGoals_ReadOnlyInvariant(t *testing.T) {
	tmp := t.TempDir()
	createHermeticGoal(t, tmp, "immutable-goal",
		"# Immutable Goal\n- **Tier**: immediate\n", "# Facts\n", "# Plan\n")
	syncHermeticReadme(t, tmp)

	goalFile := filepath.Join(tmp, "goals", "immutable-goal", "goal.md")
	beforeStat, err := os.Stat(goalFile)
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(50 * time.Millisecond)

	rep, err := ValidateGoals(tmp, ValidateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Valid {
		t.Fatalf("validation failed: %+v", rep.Diagnostics)
	}

	afterStat, err := os.Stat(goalFile)
	if err != nil {
		t.Fatal(err)
	}

	if beforeStat.ModTime() != afterStat.ModTime() {
		t.Fatalf("read-only invariant violated: file modtime changed from %v to %v", beforeStat.ModTime(), afterStat.ModTime())
	}
}

func TestValidateGoals_AncestorIsolation(t *testing.T) {
	parent := t.TempDir()
	createHermeticGoal(t, parent, "ancestor-goal", "# Ancestor\n", "# Facts\n", "# Plan\n")

	worker := filepath.Join(parent, "worker")
	if err := os.MkdirAll(worker, 0755); err != nil {
		t.Fatal(err)
	}
	// Simulate isolated git repository in child
	if err := os.WriteFile(filepath.Join(worker, ".git"), []byte("gitdir: ../worktrees/worker\n"), 0644); err != nil {
		t.Fatal(err)
	}

	rep, err := ValidateGoals(worker, ValidateOptions{})
	if err != nil {
		t.Fatalf("ValidateGoals error: %v", err)
	}
	if rep.CheckedGoals != 0 {
		t.Fatalf("expected 0 goals in isolated worker directory, got %d", rep.CheckedGoals)
	}
}

func TestValidateGoals_SurfaceCollision(t *testing.T) {
	tmp := t.TempDir()
	createHermeticGoal(t, tmp, "goal-1",
		"# Goal 1\n- **Tier**: immediate\n- **Sequence**: 1\n## Files to Touch\n- internal/crew/crew.go\n",
		"# Facts\n", "# Plan\n")
	createHermeticGoal(t, tmp, "goal-2",
		"# Goal 2\n- **Tier**: immediate\n- **Sequence**: 1\n## Files to Touch\n- internal/crew/crew.go\n",
		"# Facts\n", "# Plan\n")

	rep, err := ValidateGoals(tmp, ValidateOptions{})
	if err != nil {
		t.Fatalf("ValidateGoals error: %v", err)
	}

	foundCollision := false
	for _, d := range rep.Diagnostics {
		if d.Code == DiagSurfaceCollision {
			foundCollision = true
			break
		}
	}
	if !foundCollision {
		t.Errorf("expected SURFACE_COLLISION diagnostic between goal-1 and goal-2")
	}
}

func TestValidateGoals_DependencyProse(t *testing.T) {
	tmp := t.TempDir()
	createHermeticGoal(t, tmp, "prereq-a",
		"# Prereq A\n- **Tier**: immediate\n",
		"# Facts\n", "# Plan\n")
	createHermeticGoal(t, tmp, "target-goal",
		"# Target Goal\n- **Tier**: immediate\n- **Depends On**: prereq-a (for fixture setup). Note here.\n",
		"# Facts\n", "# Plan\n")

	syncHermeticReadme(t, tmp)

	rep, err := ValidateGoals(tmp, ValidateOptions{})
	if err != nil {
		t.Fatalf("ValidateGoals error: %v", err)
	}

	foundProse := false
	for _, d := range rep.Diagnostics {
		if d.Code == DiagDependencyProse && d.GoalSlug == "target-goal" {
			foundProse = true
			if !strings.Contains(d.Message, "(for fixture setup). Note here.") {
				t.Errorf("diagnostic message %q does not contain remainder prose", d.Message)
			}
			break
		}
	}
	if !foundProse {
		t.Errorf("expected DEPENDENCY_PROSE warning diagnostic for target-goal")
	}

	// Verify that ScanGoals parsed the clean slug 'prereq-a' and did not create a fake dependency
	audit, err := ScanGoals(tmp)
	if err != nil {
		t.Fatalf("ScanGoals error: %v", err)
	}
	for _, g := range audit.Goals {
		if g.Slug == "target-goal" {
			if len(g.DependsOn) != 1 || g.DependsOn[0] != "prereq-a" {
				t.Errorf("expected DependsOn [prereq-a], got %v", g.DependsOn)
			}
			if len(g.DependencyWarnings) != 1 {
				t.Errorf("expected 1 DependencyWarning, got %v", g.DependencyWarnings)
			}
		}
	}
}
