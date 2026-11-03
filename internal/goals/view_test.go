package goals

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/render"
)

func TestResolveGoalByIdentifier(t *testing.T) {
	audit := &AuditResult{
		Project: "test-proj",
		Goals: []Goal{
			{
				Slug:        "alpha-one",
				Title:       "Alpha One",
				Status:      "pending",
				Independent: true,
				Sequence:    1,
			},
			{
				Slug:        "alpha-two",
				Title:       "Alpha Two",
				Status:      "pending",
				Independent: true,
				Sequence:    2,
			},
			{
				Slug:        "beta-three",
				Title:       "Beta Three",
				Status:      "claimed",
				Independent: false,
				Sequence:    3,
			},
			{
				Slug:        "gamma-four",
				Title:       "Gamma Four",
				Status:      "implemented",
				Independent: true,
				Sequence:    4,
				Commit:      "abc1234",
			},
		},
	}

	// 1. Resolve by 1-based index against active roadmap:
	// Active roadmap has alpha-one (1), alpha-two (2), beta-three (3). gamma-four is implemented.
	g1, err := ResolveGoalByIdentifier(audit, "1")
	if err != nil {
		t.Fatalf("unexpected error resolving index 1: %v", err)
	}
	if g1.Slug != "alpha-one" {
		t.Errorf("expected alpha-one, got %s", g1.Slug)
	}

	g2, err := ResolveGoalByIdentifier(audit, "2")
	if err != nil {
		t.Fatalf("unexpected error resolving index 2: %v", err)
	}
	if g2.Slug != "alpha-two" {
		t.Errorf("expected alpha-two, got %s", g2.Slug)
	}

	g3, err := ResolveGoalByIdentifier(audit, "3")
	if err != nil {
		t.Fatalf("unexpected error resolving index 3: %v", err)
	}
	if g3.Slug != "beta-three" {
		t.Errorf("expected beta-three, got %s", g3.Slug)
	}

	// 2. Resolve by sequence number when index exceeds active roadmap
	g4, err := ResolveGoalByIdentifier(audit, "4")
	if err != nil {
		t.Fatalf("unexpected error resolving sequence 4: %v", err)
	}
	if g4.Slug != "gamma-four" {
		t.Errorf("expected gamma-four for sequence 4, got %s", g4.Slug)
	}

	// 3. Out of bounds index
	_, err = ResolveGoalByIdentifier(audit, "99")
	if err == nil {
		t.Errorf("expected error for index 99, got nil")
	}

	// 4. Exact slug match
	gBeta, err := ResolveGoalByIdentifier(audit, "beta-three")
	if err != nil {
		t.Fatalf("unexpected error resolving exact slug: %v", err)
	}
	if gBeta.Slug != "beta-three" {
		t.Errorf("expected beta-three, got %s", gBeta.Slug)
	}

	// 5. Case-insensitive slug match
	gBetaUpper, err := ResolveGoalByIdentifier(audit, "BETA-THREE")
	if err != nil {
		t.Fatalf("unexpected error resolving uppercase slug: %v", err)
	}
	if gBetaUpper.Slug != "beta-three" {
		t.Errorf("expected beta-three, got %s", gBetaUpper.Slug)
	}

	// 6. Path-formatted slug
	gPath, err := ResolveGoalByIdentifier(audit, "goals/beta-three/goal.md")
	if err != nil {
		t.Fatalf("unexpected error resolving path-formatted slug: %v", err)
	}
	if gPath.Slug != "beta-three" {
		t.Errorf("expected beta-three, got %s", gPath.Slug)
	}

	// 7. Unique prefix match
	gGamma, err := ResolveGoalByIdentifier(audit, "gamma")
	if err != nil {
		t.Fatalf("unexpected error resolving unique prefix: %v", err)
	}
	if gGamma.Slug != "gamma-four" {
		t.Errorf("expected gamma-four, got %s", gGamma.Slug)
	}

	// 8. Ambiguous prefix match
	_, err = ResolveGoalByIdentifier(audit, "alpha")
	if err == nil {
		t.Errorf("expected error for ambiguous prefix 'alpha', got nil")
	}
	if !strings.Contains(err.Error(), "ambiguous goal prefix") {
		t.Errorf("expected ambiguous error message, got: %v", err)
	}

	// 9. Unknown slug
	_, err = ResolveGoalByIdentifier(audit, "delta-unknown")
	if err == nil {
		t.Errorf("expected error for delta-unknown, got nil")
	}
}

func TestBuildAndRenderGoalView(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "sila-goal-view-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	goalDir := filepath.Join(tmpDir, "goals", "sample-feature")
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		t.Fatal(err)
	}

	goalContent := `# Goal: Sample Feature System

## Goal Description
A comprehensive sample feature system providing real-time telemetry and epistemic assurance.

## Dependencies & Execution Order
- **Mode**: Independent
- **Depends On**: none
- **Shape**: ship
- **Tier**: immediate

## Files to Touch
- internal/sample/model.go
- internal/sample/service.go

## Done Condition
1. All requirements described in the goal and execution plan are implemented.
2. All automated tests pass with race detection.
3. Zero regressions observed.
`
	if err := os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte(goalContent), 0644); err != nil {
		t.Fatal(err)
	}

	planContent := `# Plan: Sample Feature System

## Execution Steps
- [x] Phase 1 — Model design: Create models and interfaces
- [ ] Phase 2 — Implementation: Build service logic
- [ ] Phase 3 — Verification: Add unit and integration tests
`
	if err := os.WriteFile(filepath.Join(goalDir, "plan.md"), []byte(planContent), 0644); err != nil {
		t.Fatal(err)
	}

	factsContent := `# Facts: Sample Feature System

## Architectural Invariants & Constraints
- Sub-50ms Latency: Fast collection avoiding CPU throttling
- Pure Go: Zero CGO dependencies
`
	if err := os.WriteFile(filepath.Join(goalDir, "facts.md"), []byte(factsContent), 0644); err != nil {
		t.Fatal(err)
	}

	audit, err := ScanGoals(tmpDir)
	if err != nil {
		t.Fatalf("scan goals failed: %v", err)
	}

	targetGoal, err := ResolveGoalByIdentifier(audit, "sample-feature")
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}

	view, err := BuildGoalView(tmpDir, targetGoal)
	if err != nil {
		t.Fatalf("build view failed: %v", err)
	}

	// Verify view properties
	if view.Slug != "sample-feature" {
		t.Errorf("expected slug sample-feature, got %s", view.Slug)
	}
	if view.Title != "Sample Feature System" {
		t.Errorf("expected title 'Sample Feature System', got %s", view.Title)
	}
	if view.Tier != "immediate" {
		t.Errorf("expected tier immediate, got %s", view.Tier)
	}
	if view.TasksTotal != 3 || view.TasksMet != 1 {
		t.Errorf("expected 1/3 tasks completed, got %d/%d", view.TasksMet, view.TasksTotal)
	}
	if len(view.Invariants) != 2 {
		t.Errorf("expected 2 invariants, got %d", len(view.Invariants))
	}
	if len(view.DoneConditions) != 3 {
		t.Errorf("expected 3 done conditions, got %d", len(view.DoneConditions))
	}
	if len(view.FilesToTouch) != 2 {
		t.Errorf("expected 2 files to touch, got %d", len(view.FilesToTouch))
	}

	// Verify Terminal Rendering
	ui := render.New(os.Stdout, true) // no color
	terminalOut := RenderGoalViewTerminal(ui, view)
	if !strings.Contains(terminalOut, "Sample Feature System") {
		t.Errorf("terminal output missing title: %s", terminalOut)
	}
	if !strings.Contains(terminalOut, "Sub-50ms Latency") {
		t.Errorf("terminal output missing invariant: %s", terminalOut)
	}
	if !strings.Contains(terminalOut, "Phase 1 — Model design") {
		t.Errorf("terminal output missing tasks: %s", terminalOut)
	}
	if !strings.Contains(terminalOut, "internal/sample/model.go") {
		t.Errorf("terminal output missing touched files: %s", terminalOut)
	}

	// Verify TOON Rendering
	toonOut := RenderGoalViewTOON(view)
	if !strings.Contains(toonOut, "slug: sample-feature") {
		t.Errorf("toon output missing slug: %s", toonOut)
	}
	if !strings.Contains(toonOut, "Sub-50ms Latency") {
		t.Errorf("toon output missing invariant: %s", toonOut)
	}

	// Verify JSON serialization
	jsonData, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("json marshal failed: %v", err)
	}
	var roundtrip GoalView
	if err := json.Unmarshal(jsonData, &roundtrip); err != nil {
		t.Fatalf("json unmarshal failed: %v", err)
	}
	if roundtrip.Slug != "sample-feature" || len(roundtrip.Tasks) != 3 {
		t.Errorf("json roundtrip mismatch: %+v", roundtrip)
	}
}
