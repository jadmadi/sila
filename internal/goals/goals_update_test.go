package goals

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpdateGoalContent_PreservesCustomMetadataInDependencies(t *testing.T) {
	origContent := `# Goal: Capacity Release

## Goal Description
Release oxalpha capacity safely.

## Dependencies & Execution Order
- **Mode**: Independent (disjoint, parallelizable)
- **Depends On**: none
- **Sequence**: -
- **Shape**: ship
- **Tier**: roadmap
- **Priority**: P1
- **Owner**: @jadmadi
- **Status**: in-review
- **Estimated Effort**: 2 days

## References
- Facts: facts.md
`

	updated := updateGoalContent(
		origContent,
		"", false,
		"", false,
		false,
		[]string{"staging-release-verification"},
		0, false,
		"", false,
		nil, false,
		"", false,
		nil, false,
	)

	// Verify standard fields are updated
	if !strings.Contains(updated, "- **Mode**: Dependent (requires prerequisite goals)") {
		t.Errorf("expected Mode updated to Dependent, got:\n%s", updated)
	}
	if !strings.Contains(updated, "- **Depends On**: staging-release-verification") {
		t.Errorf("expected Depends On updated, got:\n%s", updated)
	}

	// Verify custom metadata fields are preserved
	if !strings.Contains(updated, "- **Priority**: P1") {
		t.Errorf("expected custom Priority preserved, got:\n%s", updated)
	}
	if !strings.Contains(updated, "- **Owner**: @jadmadi") {
		t.Errorf("expected custom Owner preserved, got:\n%s", updated)
	}
	if !strings.Contains(updated, "- **Status**: in-review") {
		t.Errorf("expected custom Status preserved, got:\n%s", updated)
	}
	if !strings.Contains(updated, "- **Estimated Effort**: 2 days") {
		t.Errorf("expected custom Estimated Effort preserved, got:\n%s", updated)
	}

	// Verify subsequent sections are intact
	if !strings.Contains(updated, "## References\n- Facts: facts.md") {
		t.Errorf("expected References section intact, got:\n%s", updated)
	}
}

func TestUpdateGoalPreflightAndCharterSync(t *testing.T) {
	dir := t.TempDir()
	slug := "update-charter-goal"
	if _, err := CreateGoal(CreateGoalParams{
		RootDir:      dir,
		Slug:         slug,
		Title:        "Update Charter Goal",
		FilesToTouch: []string{"a.go"},
	}); err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	if _, err := ClaimGoal(dir, slug, "agent-a", "working", false); err != nil {
		t.Fatalf("ClaimGoal: %v", err)
	}

	other := "other-claimed-goal"
	if _, err := CreateGoal(CreateGoalParams{
		RootDir:      dir,
		Slug:         other,
		Title:        "Other Claimed Goal",
		FilesToTouch: []string{"shared.go"},
	}); err != nil {
		t.Fatalf("CreateGoal other: %v", err)
	}
	if _, err := ClaimGoal(dir, other, "agent-b", "working", false); err != nil {
		t.Fatalf("ClaimGoal other: %v", err)
	}

	// 1. Charter collision fails closed.
	_, err := UpdateGoal(UpdateGoalParams{
		RootDir:      dir,
		Slug:         slug,
		FilesToTouch: []string{"shared.go"},
	})
	if err == nil || !strings.Contains(err.Error(), "path collision") {
		t.Fatalf("expected charter collision, got %v", err)
	}

	// 2. Clean update synchronizes claim.json and the charter allowlist.
	if _, err := UpdateGoal(UpdateGoalParams{
		RootDir:      dir,
		Slug:         slug,
		FilesToTouch: []string{"a.go", "a2.go"},
	}); err != nil {
		t.Fatalf("UpdateGoal files: %v", err)
	}
	claim, err := readClaim(filepath.Join(dir, "goals", slug, "claim.json"))
	if err != nil {
		t.Fatalf("readClaim: %v", err)
	}
	if !sliceContains(claim.FilesToTouch, "a2.go") {
		t.Errorf("expected claim FilesToTouch to include a2.go, got %v", claim.FilesToTouch)
	}
	charter, err := LoadCharter(dir, slug)
	if err != nil {
		t.Fatalf("LoadCharter: %v", err)
	}
	if !sliceContains(charter.AllowPaths, "a2.go") {
		t.Errorf("expected charter AllowPaths to include a2.go, got %v", charter.AllowPaths)
	}

	// 3. Strict preflight rejects warnings; non-strict surfaces them without failing.
	if _, err := UpdateGoal(UpdateGoalParams{
		RootDir:   dir,
		Slug:      slug,
		DependsOn: []string{"missing-prereq"},
	}); err != nil {
		t.Fatalf("non-strict UpdateGoal should not fail on warnings: %v", err)
	}
	_, err = UpdateGoal(UpdateGoalParams{
		RootDir:   dir,
		Slug:      slug,
		DependsOn: []string{"missing-prereq"},
		Strict:    true,
	})
	if err == nil || !strings.Contains(err.Error(), "strict pre-flight check failed") {
		t.Fatalf("expected strict preflight failure, got %v", err)
	}

	// 4. UpdateGoal validates and persists a spec review against the work package.
	dir2, pkg := setupValidReadinessFixture(t)
	review := validSpecReviewFixture(pkg)
	if _, err := CreateGoal(CreateGoalParams{
		RootDir:     dir2,
		Slug:        pkg.GoalSlug,
		Title:       "Spec Review Update",
		WorkPackage: pkg,
	}); err != nil {
		t.Fatalf("CreateGoal structured: %v", err)
	}
	if _, err := UpdateGoal(UpdateGoalParams{
		RootDir:    dir2,
		Slug:       pkg.GoalSlug,
		SpecReview: &review,
	}); err != nil {
		t.Fatalf("UpdateGoal spec review: %v", err)
	}
	loaded, err := LoadSpecReview(dir2, pkg.GoalSlug)
	if err != nil || loaded == nil {
		t.Fatalf("LoadSpecReview: %v", err)
	}
	if loaded.SpecDigest != review.SpecDigest {
		t.Errorf("expected saved spec digest %q, got %q", review.SpecDigest, loaded.SpecDigest)
	}
}

func TestUpdateGoal_PreservesCustomMetadataEndToEnd(t *testing.T) {
	dir := t.TempDir()
	goalDir := filepath.Join(dir, "goals", "oxalpha-capacity-release")
	_ = os.MkdirAll(goalDir, 0755)

	goalMd := `# Goal: Capacity Release

## Goal Description
Original description.

## Dependencies & Execution Order
- **Mode**: Independent (disjoint, parallelizable)
- **Depends On**: none
- **Sequence**: -
- **Shape**: ship
- **Tier**: roadmap
- **Priority**: P1
- **Owner**: @jadmadi
- **Status**: in-review

## Done Condition
1. Tests pass.
`
	if err := os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte(goalMd), 0644); err != nil {
		t.Fatalf("failed to write goal.md: %v", err)
	}

	// Call UpdateGoal with AddDeps
	_, err := UpdateGoal(UpdateGoalParams{
		RootDir: dir,
		Slug:    "oxalpha-capacity-release",
		AddDeps: []string{"staging-release-verification"},
	})
	if err != nil {
		t.Fatalf("UpdateGoal failed: %v", err)
	}

	savedBytes, err := os.ReadFile(filepath.Join(goalDir, "goal.md"))
	if err != nil {
		t.Fatalf("read goal.md failed: %v", err)
	}
	saved := string(savedBytes)

	if !strings.Contains(saved, "- **Depends On**: staging-release-verification") {
		t.Errorf("expected updated Depends On, got:\n%s", saved)
	}
	if !strings.Contains(saved, "- **Priority**: P1") {
		t.Errorf("expected Priority preserved, got:\n%s", saved)
	}
	if !strings.Contains(saved, "- **Owner**: @jadmadi") {
		t.Errorf("expected Owner preserved, got:\n%s", saved)
	}
	if !strings.Contains(saved, "- **Status**: in-review") {
		t.Errorf("expected Status preserved, got:\n%s", saved)
	}
}
