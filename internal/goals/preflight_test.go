package goals

import (
	"slices"
	"testing"
)

func TestPreFlightGoalCheckClean(t *testing.T) {
	tmp := t.TempDir()

	report, err := PreFlightGoalCheck(tmp, CreateGoalParams{
		Slug:        "brand-new-goal",
		Title:       "Brand New Goal",
		Description: "A brand new goal with no conflicts",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !report.Valid {
		t.Errorf("expected valid report, got errors=%v", report.Errors)
	}
	if len(report.Errors) > 0 || len(report.Warnings) > 0 || len(report.Collisions) > 0 {
		t.Errorf("expected empty errors/warnings, got errors=%v, warnings=%v, collisions=%v", report.Errors, report.Warnings, report.Collisions)
	}
}

func TestPreFlightFileTouchCollision(t *testing.T) {
	tmp := t.TempDir()

	// 1. Create first goal touching "internal/mcp/server.go"
	g1, err := CreateGoal(CreateGoalParams{
		RootDir:      tmp,
		Slug:         "first-feature",
		Title:        "First Feature",
		FilesToTouch: []string{"internal/mcp/server.go"},
		Sequence:     3,
	})
	if err != nil {
		t.Fatalf("failed to create first-feature: %v", err)
	}
	if g1 == nil {
		t.Fatal("first goal was nil")
	}

	// 2. Preflight second goal touching the same file without declaring dependency
	report, err := PreFlightGoalCheck(tmp, CreateGoalParams{
		RootDir:      tmp,
		Slug:         "second-feature",
		Title:        "Second Feature",
		FilesToTouch: []string{"internal/mcp/server.go"},
	})
	if err != nil {
		t.Fatalf("PreFlightGoalCheck error: %v", err)
	}
	if !report.Valid {
		t.Errorf("expected valid=true for non-strict preflight, got false")
	}
	if len(report.Collisions) == 0 {
		t.Fatalf("expected collisions, got none")
	}
	if report.Collisions[0].ConflictingGoal != "first-feature" {
		t.Errorf("expected conflicting goal first-feature, got %s", report.Collisions[0].ConflictingGoal)
	}
	if len(report.SuggestedDependsOn) == 0 || report.SuggestedDependsOn[0] != "first-feature" {
		t.Errorf("expected suggested depends_on to include first-feature, got %v", report.SuggestedDependsOn)
	}
	if report.SuggestedSequence != 4 {
		t.Errorf("expected suggested sequence 4 (max dep seq 3 + 1), got %d", report.SuggestedSequence)
	}

	// 3. If second goal declares dependency on first-feature, collision is resolved!
	reportWithDep, err := PreFlightGoalCheck(tmp, CreateGoalParams{
		RootDir:      tmp,
		Slug:         "second-feature",
		Title:        "Second Feature",
		FilesToTouch: []string{"internal/mcp/server.go"},
		DependsOn:    []string{"first-feature"},
	})
	if err != nil {
		t.Fatalf("PreFlightGoalCheck with dep error: %v", err)
	}
	if len(reportWithDep.Collisions) != 0 {
		t.Errorf("expected zero collisions when dependency is declared, got %v", reportWithDep.Collisions)
	}
}

func TestPreFlightScoutExemption(t *testing.T) {
	tmp := t.TempDir()

	_, err := CreateGoal(CreateGoalParams{
		RootDir:      tmp,
		Slug:         "existing-ship",
		Title:        "Existing Ship",
		FilesToTouch: []string{"cmd/sila/main.go"},
	})
	if err != nil {
		t.Fatalf("CreateGoal failed: %v", err)
	}

	// Scout goal touching the same file
	report, err := PreFlightGoalCheck(tmp, CreateGoalParams{
		RootDir:      tmp,
		Slug:         "investigative-scout",
		Title:        "Investigative Scout",
		FilesToTouch: []string{"cmd/sila/main.go"},
		Shape:        "scout",
	})
	if err != nil {
		t.Fatalf("PreFlightGoalCheck error: %v", err)
	}
	if len(report.Collisions) != 0 {
		t.Errorf("expected scout goal to be exempt from collisions, got %v", report.Collisions)
	}
}

func TestPreFlightSelfDependencyAndCycles(t *testing.T) {
	tmp := t.TempDir()

	// 1. Self dependency
	reportSelf, _ := PreFlightGoalCheck(tmp, CreateGoalParams{
		Slug:      "ouroboros",
		DependsOn: []string{"ouroboros"},
	})
	if reportSelf.Valid {
		t.Errorf("expected invalid report for self dependency")
	}
	hasSelfErr := slices.Contains(reportSelf.Errors, `goal "ouroboros" cannot depend on itself`)
	if !hasSelfErr {
		t.Errorf("expected self-dependency error, got %v", reportSelf.Errors)
	}

	// 2. Cycle prevention
	_, err := CreateGoal(CreateGoalParams{
		RootDir:   tmp,
		Slug:      "cycle-a",
		DependsOn: []string{"cycle-b"},
	})
	if err != nil {
		t.Fatalf("creating cycle-a: %v", err)
	}

	reportCycle, _ := PreFlightGoalCheck(tmp, CreateGoalParams{
		RootDir:   tmp,
		Slug:      "cycle-b",
		DependsOn: []string{"cycle-a"},
	})
	if reportCycle.Valid {
		t.Errorf("expected cycle detection to invalidate preflight")
	}
}

func TestPreFlightInvariantWarnings(t *testing.T) {
	tmp := t.TempDir()

	report, err := PreFlightGoalCheck(tmp, CreateGoalParams{
		Slug:        "cgo-test",
		Title:       "CGO Test",
		Invariants:  []string{"enable cgo bindings for sqlite"},
		Description: "Use GPL library in engine",
	})
	if err != nil {
		t.Fatalf("PreFlightGoalCheck error: %v", err)
	}
	if len(report.Warnings) < 2 {
		t.Errorf("expected at least 2 invariant warnings (CGO and copyleft), got %v", report.Warnings)
	}
}

func TestPreFlightStrictModeRejection(t *testing.T) {
	tmp := t.TempDir()

	_, err := CreateGoal(CreateGoalParams{
		RootDir:      tmp,
		Slug:         "base-goal",
		FilesToTouch: []string{"internal/store/store.go"},
	})
	if err != nil {
		t.Fatalf("failed to create base goal: %v", err)
	}

	// In non-strict mode, goal creation succeeds with warnings attached
	g, err := CreateGoal(CreateGoalParams{
		RootDir:      tmp,
		Slug:         "conflicting-goal-lenient",
		FilesToTouch: []string{"internal/store/store.go"},
		Strict:       false,
	})
	if err != nil {
		t.Fatalf("expected non-strict creation to succeed, got error: %v", err)
	}
	if g.PreFlight == nil || len(g.PreFlight.Collisions) == 0 {
		t.Errorf("expected preflight collisions on lenient goal, got %+v", g.PreFlight)
	}

	// In strict mode, goal creation is rejected
	_, errStrict := CreateGoal(CreateGoalParams{
		RootDir:      tmp,
		Slug:         "conflicting-goal-strict",
		FilesToTouch: []string{"internal/store/store.go"},
		Strict:       true,
	})
	if errStrict == nil {
		t.Fatal("expected strict goal creation to fail on collision, but it succeeded")
	}
}

func TestPreFlightNonexistentPrerequisiteWarning(t *testing.T) {
	tmp := t.TempDir()

	report, err := PreFlightGoalCheck(tmp, CreateGoalParams{
		Slug:      "prereq-check",
		DependsOn: []string{"ghost-goal"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	foundGhostWarning := slices.Contains(report.Warnings, `declared prerequisite "ghost-goal" does not exist in goals/`)
	if !foundGhostWarning {
		t.Errorf("expected ghost-goal prerequisite warning, got %v", report.Warnings)
	}
}
