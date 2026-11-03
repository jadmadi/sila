package goals

import (
	"reflect"
	"strings"
	"testing"
)

func TestNormalizeGoalPath(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"", ""},
		{"./foo/bar.go", "foo/bar.go"},
		{"/foo/bar.go", "foo/bar.go"},
		{"foo/bar.go/", "foo/bar.go"},
		{`internal\goals\claim.go`, "internal/goals/claim.go"},
		{"./internal/../internal/goals/", "internal/goals"},
		{"`internal/goals/claim.go`", "internal/goals/claim.go"},
		{`"internal/goals/claim.go"`, "internal/goals/claim.go"},
		{"'", ""},
		{".", "."},
	}

	for _, tc := range tests {
		got := NormalizeGoalPath(tc.input)
		if got != tc.expected {
			t.Errorf("NormalizeGoalPath(%q) = %q, expected %q", tc.input, got, tc.expected)
		}
	}
}

func TestPathsOverlap(t *testing.T) {
	tests := []struct {
		p1       string
		p2       string
		expected bool
	}{
		// Exact matches
		{"internal/goals/claim.go", "internal/goals/claim.go", true},
		{"./internal/goals/claim.go", "internal/goals/claim.go", true},

		// Directory containment
		{"internal/goals", "internal/goals/claim.go", true},
		{"internal/goals/claim.go", "internal/goals", true},
		{"internal/goals/", "internal/goals/sub/nested.go", true},

		// Root directory
		{".", "internal/goals/claim.go", true},
		{"internal/goals/claim.go", ".", true},

		// Siblings (must NOT overlap despite string prefix)
		{"internal/goals", "internal/dream", false},
		{"cmd/sila/main.go", "cmd/sila/main_test.go", false},
		{"pkg/auth", "pkg/author", false},

		// Empty
		{"", "internal/goals", false},
		{"internal/goals", "", false},
	}

	for _, tc := range tests {
		got := PathsOverlap(tc.p1, tc.p2)
		if got != tc.expected {
			t.Errorf("PathsOverlap(%q, %q) = %v, expected %v", tc.p1, tc.p2, got, tc.expected)
		}
	}
}

func TestOverlappingPaths(t *testing.T) {
	set1 := []string{"internal/goals/claim.go", "cmd/sila/main.go", "docs/api.md"}
	set2 := []string{"internal/goals/", "cmd/sila/version.go", "README.md"}

	overlaps := OverlappingPaths(set1, set2)
	expected := []string{"internal/goals/claim.go"}

	if !reflect.DeepEqual(overlaps, expected) {
		t.Fatalf("OverlappingPaths() = %v, expected %v", overlaps, expected)
	}
}

func TestGoalTouchParsing_FrontmatterAndMarkdown(t *testing.T) {
	contentFM := `---
title: "Concurrency Protection"
files_to_touch:
  - internal/goals/claim.go
  - cmd/sila/main.go
---
# Goal
`
	_, _, _, _, _, touchFM, _ := parseGoalMetadata(contentFM)
	expectedFM := []string{"internal/goals/claim.go", "cmd/sila/main.go"}
	if !reflect.DeepEqual(touchFM, expectedFM) {
		t.Fatalf("parseGoalMetadata frontmatter touch = %v, expected %v", touchFM, expectedFM)
	}

	contentBody := `# Goal: Concurrency Protection

## Goal Description
Prevents collision.

## Files to Touch
- internal/goals/claim.go
- cmd/sila/main.go
- [pkg/store](internal/store/store.go)

## Done Condition
Done.
`
	_, _, _, _, _, touchBody, _ := parseGoalMetadata(contentBody)
	expectedBody := []string{"internal/goals/claim.go", "cmd/sila/main.go", "internal/store/store.go"}
	if !reflect.DeepEqual(touchBody, expectedBody) {
		t.Fatalf("parseGoalMetadata body touch = %v, expected %v", touchBody, expectedBody)
	}
}

func TestGoalCollision_ClaimAndContestedQueue(t *testing.T) {
	tmp := t.TempDir()

	// 1. Create Goal A (touches internal/goals/claim.go)
	gA, err := CreateGoal(CreateGoalParams{
		RootDir:      tmp,
		Slug:         "goal-a",
		Title:        "Goal A",
		FilesToTouch: []string{"internal/goals/claim.go"},
	})
	if err != nil {
		t.Fatalf("create goal-a: %v", err)
	}
	if len(gA.FilesToTouch) != 1 || gA.FilesToTouch[0] != "internal/goals/claim.go" {
		t.Fatalf("expected FilesToTouch in goal-a, got %v", gA.FilesToTouch)
	}

	// 2. Create Goal B (touches internal/goals/)
	_, err = CreateGoal(CreateGoalParams{
		RootDir:      tmp,
		Slug:         "goal-b",
		Title:        "Goal B",
		FilesToTouch: []string{"internal/goals/"},
	})
	if err != nil {
		t.Fatalf("create goal-b: %v", err)
	}

	// 3. Agent 1 claims goal-a
	claimA, err := ClaimGoal(tmp, "goal-a", "agent-alpha", "starting work on claim.go", false)
	if err != nil {
		t.Fatalf("claim goal-a: %v", err)
	}
	if len(claimA.FilesToTouch) != 1 {
		t.Fatalf("expected claimA to record files_to_touch, got %v", claimA.FilesToTouch)
	}

	// 4. Audit should now show goal-b as contested!
	audit, err := ScanGoals(tmp)
	if err != nil {
		t.Fatalf("scan goals: %v", err)
	}
	if audit.ContestedCount != 1 {
		t.Errorf("expected ContestedCount == 1, got %d", audit.ContestedCount)
	}
	if audit.ReadyCount != 0 {
		t.Errorf("expected ReadyCount == 0 (since goal-b is contested), got %d", audit.ReadyCount)
	}

	// Check goal-b conflicts
	var goalB *Goal
	for i := range audit.Goals {
		if audit.Goals[i].Slug == "goal-b" {
			goalB = &audit.Goals[i]
			break
		}
	}
	if goalB == nil {
		t.Fatal("goal-b not found in audit")
	}
	if len(goalB.FileConflicts) != 1 {
		t.Fatalf("expected 1 file conflict for goal-b, got %d (%+v)", len(goalB.FileConflicts), goalB.FileConflicts)
	}
	if goalB.FileConflicts[0].ConflictingGoal != "goal-a" || goalB.FileConflicts[0].HolderAgent != "agent-alpha" {
		t.Errorf("unexpected conflict details: %+v", goalB.FileConflicts[0])
	}

	// 5. Agent 2 attempts to claim goal-b -> MUST fail with conflict error unless --force
	_, err = ClaimGoal(tmp, "goal-b", "agent-beta", "trying to claim overlapping dir", false)
	if err == nil {
		t.Fatal("expected ClaimGoal on goal-b to fail due to file collision, but it succeeded")
	}
	if !strings.Contains(err.Error(), "file conflict") || !strings.Contains(err.Error(), "goal-a") {
		t.Errorf("expected conflict message in error, got %v", err)
	}

	// 6. Claiming with force=true should succeed
	claimB, err := ClaimGoal(tmp, "goal-b", "agent-beta", "force override", true)
	if err != nil {
		t.Fatalf("force claim failed: %v", err)
	}
	if claimB.Agent != "agent-beta" {
		t.Errorf("expected claimB agent agent-beta, got %s", claimB.Agent)
	}

	// 7. Release both goals
	if err := ReleaseGoal(tmp, "goal-a"); err != nil {
		t.Fatalf("release goal-a: %v", err)
	}
	if err := ReleaseGoal(tmp, "goal-b"); err != nil {
		t.Fatalf("release goal-b: %v", err)
	}

	// 8. Re-audit: zero contested, both ready
	auditAfter, err := ScanGoals(tmp)
	if err != nil {
		t.Fatalf("scan goals after release: %v", err)
	}
	if auditAfter.ContestedCount != 0 {
		t.Errorf("expected ContestedCount == 0, got %d", auditAfter.ContestedCount)
	}
	if auditAfter.ReadyCount != 2 {
		t.Errorf("expected ReadyCount == 2, got %d", auditAfter.ReadyCount)
	}
}
