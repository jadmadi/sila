package goals

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

//go:fix inline
func boolPtr(b bool) *bool {
	return new(b)
}

func TestUpdateGoal_AddRemoveDependencies(t *testing.T) {
	tmpDir := t.TempDir()

	g, err := CreateGoal(CreateGoalParams{
		RootDir:     tmpDir,
		Slug:        "alpha-task",
		Title:       "Alpha Task",
		Independent: new(true),
	})
	if err != nil {
		t.Fatalf("CreateGoal failed: %v", err)
	}
	if !g.Independent || len(g.DependsOn) != 0 {
		t.Fatalf("expected independent goal with 0 deps, got indep=%v, deps=%v", g.Independent, g.DependsOn)
	}

	// 1. Add dep-1 and dep-2
	gUpdated, err := UpdateGoal(UpdateGoalParams{
		RootDir: tmpDir,
		Slug:    "alpha-task",
		AddDeps: []string{"dep-1", "dep-2"},
	})
	if err != nil {
		t.Fatalf("UpdateGoal add failed: %v", err)
	}
	if gUpdated.Independent {
		t.Fatalf("expected dependent goal after adding dependencies, got independent=true")
	}
	if len(gUpdated.DependsOn) != 2 || gUpdated.DependsOn[0] != "dep-1" || gUpdated.DependsOn[1] != "dep-2" {
		t.Fatalf("unexpected DependsOn: %v", gUpdated.DependsOn)
	}

	// 2. Add dep-3
	gUpdated, err = UpdateGoal(UpdateGoalParams{
		RootDir: tmpDir,
		Slug:    "alpha-task",
		AddDeps: []string{"dep-3"},
	})
	if err != nil {
		t.Fatalf("UpdateGoal add dep-3 failed: %v", err)
	}
	if len(gUpdated.DependsOn) != 3 || gUpdated.DependsOn[2] != "dep-3" {
		t.Fatalf("unexpected DependsOn after adding dep-3: %v", gUpdated.DependsOn)
	}

	// 3. Remove dep-2
	gUpdated, err = UpdateGoal(UpdateGoalParams{
		RootDir:    tmpDir,
		Slug:       "alpha-task",
		RemoveDeps: []string{"dep-2"},
	})
	if err != nil {
		t.Fatalf("UpdateGoal remove dep-2 failed: %v", err)
	}
	if len(gUpdated.DependsOn) != 2 || gUpdated.DependsOn[0] != "dep-1" || gUpdated.DependsOn[1] != "dep-3" {
		t.Fatalf("unexpected DependsOn after removing dep-2: %v", gUpdated.DependsOn)
	}

	// 4. Remove all remaining
	gUpdated, err = UpdateGoal(UpdateGoalParams{
		RootDir:    tmpDir,
		Slug:       "alpha-task",
		RemoveDeps: []string{"dep-1", "dep-3"},
	})
	if err != nil {
		t.Fatalf("UpdateGoal remove remaining failed: %v", err)
	}
	if len(gUpdated.DependsOn) != 0 || !gUpdated.Independent {
		t.Fatalf("expected independent goal with 0 deps after removing all, got deps=%v, indep=%v", gUpdated.DependsOn, gUpdated.Independent)
	}
}

func TestUpdateGoal_ExplicitDependsOnAndSelfCheck(t *testing.T) {
	tmpDir := t.TempDir()

	_, err := CreateGoal(CreateGoalParams{
		RootDir: tmpDir,
		Slug:    "beta-task",
		Title:   "Beta Task",
	})
	if err != nil {
		t.Fatalf("CreateGoal failed: %v", err)
	}

	// Replace deps
	gUpdated, err := UpdateGoal(UpdateGoalParams{
		RootDir:   tmpDir,
		Slug:      "beta-task",
		DependsOn: []string{"prereq-a", "prereq-b"},
	})
	if err != nil {
		t.Fatalf("UpdateGoal replace deps failed: %v", err)
	}
	if len(gUpdated.DependsOn) != 2 || gUpdated.DependsOn[0] != "prereq-a" || gUpdated.DependsOn[1] != "prereq-b" {
		t.Fatalf("unexpected DependsOn: %v", gUpdated.DependsOn)
	}

	// Prevent self dependency
	_, err = UpdateGoal(UpdateGoalParams{
		RootDir: tmpDir,
		Slug:    "beta-task",
		AddDeps: []string{"beta-task"},
	})
	if err == nil {
		t.Fatalf("expected error on self dependency, got nil")
	}
	if !strings.Contains(err.Error(), "itself") {
		t.Fatalf("expected error mentioning itself, got: %v", err)
	}
}

func TestUpdateGoal_CycleDetection(t *testing.T) {
	tmpDir := t.TempDir()

	// Goal 1
	_, err := CreateGoal(CreateGoalParams{
		RootDir: tmpDir,
		Slug:    "node-a",
		Title:   "Node A",
	})
	if err != nil {
		t.Fatalf("CreateGoal A failed: %v", err)
	}

	// Goal 2 depending on A
	_, err = CreateGoal(CreateGoalParams{
		RootDir:   tmpDir,
		Slug:      "node-b",
		Title:     "Node B",
		DependsOn: []string{"node-a"},
	})
	if err != nil {
		t.Fatalf("CreateGoal B failed: %v", err)
	}

	// Try updating A to depend on B (A -> B -> A)
	_, err = UpdateGoal(UpdateGoalParams{
		RootDir: tmpDir,
		Slug:    "node-a",
		AddDeps: []string{"node-b"},
	})
	if err == nil {
		t.Fatalf("expected cycle error, got nil")
	}
	if !strings.Contains(err.Error(), "circular dependency") && !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected circular dependency error, got: %v", err)
	}
}

func TestUpdateGoal_MetadataUpdates(t *testing.T) {
	tmpDir := t.TempDir()

	_, err := CreateGoal(CreateGoalParams{
		RootDir:     tmpDir,
		Slug:        "meta-task",
		Title:       "Meta Task Initial",
		Description: "Initial description",
		Sequence:    1,
		Shape:       "ship",
	})
	if err != nil {
		t.Fatalf("CreateGoal failed: %v", err)
	}

	newTitle := "Meta Task Refined"
	newDesc := "Refined description with detailed spec"
	newSeq := 42
	newShape := "scout"
	newFiles := []string{"internal/test/one.go", "internal/test/two.go"}

	gUpdated, err := UpdateGoal(UpdateGoalParams{
		RootDir:      tmpDir,
		Slug:         "meta-task",
		Title:        &newTitle,
		Description:  &newDesc,
		Sequence:     &newSeq,
		Shape:        &newShape,
		FilesToTouch: newFiles,
	})
	if err != nil {
		t.Fatalf("UpdateGoal failed: %v", err)
	}

	if gUpdated.Title != newTitle {
		t.Errorf("expected title %q, got %q", newTitle, gUpdated.Title)
	}
	if gUpdated.Description != newDesc {
		t.Errorf("expected desc %q, got %q", newDesc, gUpdated.Description)
	}
	if gUpdated.Sequence != newSeq {
		t.Errorf("expected sequence %d, got %d", newSeq, gUpdated.Sequence)
	}
	if gUpdated.Shape != newShape {
		t.Errorf("expected shape %q, got %q", newShape, gUpdated.Shape)
	}
	if len(gUpdated.FilesToTouch) != 2 {
		t.Errorf("expected 2 files to touch, got %v", gUpdated.FilesToTouch)
	}

	// Verify on-disk re-parse
	audit, err := ScanGoals(tmpDir)
	if err != nil {
		t.Fatalf("ScanGoals failed: %v", err)
	}
	var found *Goal
	for _, g := range audit.Goals {
		if g.Slug == "meta-task" {
			found = &g
			break
		}
	}
	if found == nil {
		t.Fatalf("meta-task not found in scanned goals")
	}
	if found.Title != newTitle || found.Description != newDesc || found.Sequence != newSeq || found.Shape != newShape {
		t.Errorf("scanned goal mismatch: %+v", found)
	}
}

func TestUpdateGoal_Frontmatter(t *testing.T) {
	tmpDir := t.TempDir()
	goalDir := filepath.Join(tmpDir, "goals", "fm-task")
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}

	fmContent := `---
title: "Frontmatter Goal"
description: "Original description"
independent: true
depends_on: none
sequence: 5
shape: ship
---

# Details
Some extra notes here.
`
	if err := os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte(fmContent), 0644); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	newTitle := "Updated Frontmatter Goal"
	gUpdated, err := UpdateGoal(UpdateGoalParams{
		RootDir: tmpDir,
		Slug:    "fm-task",
		Title:   &newTitle,
		AddDeps: []string{"upstream-dep"},
	})
	if err != nil {
		t.Fatalf("UpdateGoal failed: %v", err)
	}
	if gUpdated.Title != newTitle {
		t.Errorf("expected title %q, got %q", newTitle, gUpdated.Title)
	}
	if gUpdated.Independent {
		t.Errorf("expected independent=false after adding dep")
	}
	if len(gUpdated.DependsOn) != 1 || gUpdated.DependsOn[0] != "upstream-dep" {
		t.Errorf("expected [upstream-dep], got %v", gUpdated.DependsOn)
	}

	// Read content from disk to verify
	diskBytes, _ := os.ReadFile(filepath.Join(goalDir, "goal.md"))
	diskContent := string(diskBytes)
	if !strings.Contains(diskContent, "upstream-dep") {
		t.Errorf("disk content does not contain upstream-dep: %s", diskContent)
	}
	if !strings.Contains(diskContent, "Some extra notes here.") {
		t.Errorf("disk content lost body notes: %s", diskContent)
	}
}
