package goals

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseGoalMetadata_Shape(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name: "default ship when no shape specified",
			content: `# Goal: Test

## Goal Description
A test goal.

## Dependencies & Execution Order
- **Mode**: Independent (disjoint, parallelizable)
- **Depends On**: none
- **Sequence**: -
`,
			want: "ship",
		},
		{
			name: "scout from markdown body",
			content: `# Goal: Scout Investigation

## Goal Description
Investigate architecture.

## Dependencies & Execution Order
- **Mode**: Independent (disjoint, parallelizable)
- **Depends On**: none
- **Sequence**: -
- **Shape**: scout
`,
			want: "scout",
		},
		{
			name: "ship from markdown body",
			content: `# Goal: Ship Task

## Goal Description
Build feature.

## Dependencies & Execution Order
- **Mode**: Independent (disjoint, parallelizable)
- **Depends On**: none
- **Sequence**: -
- **Shape**: ship
`,
			want: "ship",
		},
		{
			name: "scout from frontmatter",
			content: `---
shape: scout
---
# Goal: Frontmatter Scout

## Goal Description
Investigate via frontmatter.
`,
			want: "scout",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, _, _, _, _, shape := parseGoalMetadata(tt.content)
			if shape != tt.want {
				t.Errorf("parseGoalMetadata() shape = %q, want %q", shape, tt.want)
			}
		})
	}
}

func TestCreateGoal_Shape(t *testing.T) {
	dir := t.TempDir()

	// Create a scout goal
	g, err := CreateGoal(CreateGoalParams{
		RootDir: dir,
		Slug:    "investigate-auth",
		Title:   "Investigate Auth Architecture",
		Shape:   "scout",
	})
	if err != nil {
		t.Fatalf("CreateGoal failed: %v", err)
	}
	if g.Shape != "scout" {
		t.Errorf("expected shape 'scout', got %q", g.Shape)
	}

	// Verify goal.md contains the shape line
	data, err := os.ReadFile(filepath.Join(dir, "goals", "investigate-auth", "goal.md"))
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	content := string(data)
	if !contains(content, "- **Shape**: scout") {
		t.Errorf("goal.md missing Shape line, content:\n%s", content)
	}

	// Verify ScanGoals picks up the shape
	audit, err := ScanGoals(dir)
	if err != nil {
		t.Fatalf("ScanGoals failed: %v", err)
	}
	for _, goal := range audit.Goals {
		if goal.Slug == "investigate-auth" {
			if goal.Shape != "scout" {
				t.Errorf("ScanGoals: expected shape 'scout', got %q", goal.Shape)
			}
			return
		}
	}
	t.Fatal("investigate-auth goal not found in scan results")
}

func TestCreateGoal_DefaultShapeIsShip(t *testing.T) {
	dir := t.TempDir()

	g, err := CreateGoal(CreateGoalParams{
		RootDir: dir,
		Slug:    "build-feature",
		Title:   "Build Feature",
	})
	if err != nil {
		t.Fatalf("CreateGoal failed: %v", err)
	}
	if g.Shape != "ship" {
		t.Errorf("expected default shape 'ship', got %q", g.Shape)
	}
}

func TestScoutShape_ExemptFromCollision(t *testing.T) {
	dir := t.TempDir()

	// Create two goals that touch the same files
	_, err := CreateGoal(CreateGoalParams{
		RootDir:      dir,
		Slug:         "ship-task",
		Title:        "Ship Task",
		FilesToTouch: []string{"src/main.go"},
	})
	if err != nil {
		t.Fatalf("CreateGoal 1 failed: %v", err)
	}

	_, err = CreateGoal(CreateGoalParams{
		RootDir:      dir,
		Slug:         "scout-task",
		Title:        "Scout Task",
		FilesToTouch: []string{"src/main.go"},
		Shape:        "scout",
	})
	if err != nil {
		t.Fatalf("CreateGoal 2 failed: %v", err)
	}

	// Claim g1 (ship task)
	claim1, err := ClaimGoal(dir, "ship-task", "agent-a", "working", false)
	if err != nil {
		t.Fatalf("ClaimGoal 1 failed: %v", err)
	}
	if len(claim1.FilesToTouch) == 0 {
		t.Fatal("expected claim to have files to touch")
	}

	// Claim g2 (scout task) — should succeed despite file overlap
	_, err = ClaimGoal(dir, "scout-task", "agent-b", "investigating", false)
	if err != nil {
		t.Fatalf("ClaimGoal for scout task should succeed despite collision: %v", err)
	}

	// Verify: ScanGoals should not report file conflicts for the scout task
	audit, err := ScanGoals(dir)
	if err != nil {
		t.Fatalf("ScanGoals failed: %v", err)
	}
	for _, goal := range audit.Goals {
		if goal.Slug == "scout-task" {
			if len(goal.FileConflicts) > 0 {
				t.Errorf("scout task should have no file conflicts, got %d", len(goal.FileConflicts))
			}
		}
	}
}

func TestShipShape_StillChecksCollision(t *testing.T) {
	dir := t.TempDir()

	// Create two ship goals that touch the same files
	CreateGoal(CreateGoalParams{
		RootDir:      dir,
		Slug:         "ship-task-a",
		Title:        "Ship Task A",
		FilesToTouch: []string{"src/main.go"},
	})

	CreateGoal(CreateGoalParams{
		RootDir:      dir,
		Slug:         "ship-task-b",
		Title:        "Ship Task B",
		FilesToTouch: []string{"src/main.go"},
	})

	// Claim first
	_, err := ClaimGoal(dir, "ship-task-a", "agent-a", "working", false)
	if err != nil {
		t.Fatalf("ClaimGoal A failed: %v", err)
	}

	// Claim second — should fail due to collision
	_, err = ClaimGoal(dir, "ship-task-b", "agent-b", "working", false)
	if err == nil {
		t.Fatal("expected ClaimGoal B to fail due to file collision, but it succeeded")
	}
}

func TestShapeBadge(t *testing.T) {
	if shapeBadge("scout") != "🔭 [SCOUT]" {
		t.Error("shapeBadge(scout) should return 🔭 [SCOUT]")
	}
	if shapeBadge("ship") != "🚢 [SHIP]" {
		t.Error("shapeBadge(ship) should return 🚢 [SHIP]")
	}
	if shapeBadge("") != "🚢 [SHIP]" {
		t.Error("shapeBadge('') should default to 🚢 [SHIP]")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStr(s, substr))
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
