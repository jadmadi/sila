package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func initTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\nOutput: %s", args, err, string(out))
		}
	}

	run("init", "-b", "main")
	run("config", "user.name", "Test User")
	run("config", "user.email", "test@example.com")

	// Initial commit on main
	file1 := filepath.Join(dir, "README.md")
	if err := os.WriteFile(file1, []byte("# Main Repo\n"), 0644); err != nil {
		t.Fatal(err)
	}
	run("add", "README.md")
	run("commit", "-m", "chore: initial commit")

	return dir
}

func TestComputeTreePatchID_NoDiff(t *testing.T) {
	dir := initTestRepo(t)
	ctx := context.Background()

	res, err := ComputeTreePatchID(ctx, dir, "main", false)
	if err != nil {
		t.Fatalf("ComputeTreePatchID returned error: %v", err)
	}
	if res.HasDiff {
		t.Errorf("expected HasDiff: false for clean main, got true (patch_id: %s)", res.PatchID)
	}
	if res.PatchID != "" {
		t.Errorf("expected empty PatchID, got %s", res.PatchID)
	}
}

func TestComputeTreePatchID_FeatureBranch(t *testing.T) {
	dir := initTestRepo(t)
	ctx := context.Background()

	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\nOutput: %s", args, err, string(out))
		}
	}

	// Create branch
	run("checkout", "-b", "feature")

	// Make changes
	codeFile := filepath.Join(dir, "feature.go")
	if err := os.WriteFile(codeFile, []byte("package feature\n\nfunc Run() int { return 42 }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	run("add", "feature.go")
	run("commit", "-m", "feat: add feature")

	res, err := ComputeTreePatchID(ctx, dir, "main", false)
	if err != nil {
		t.Fatalf("ComputeTreePatchID failed: %v", err)
	}

	if !res.HasDiff {
		t.Fatalf("expected HasDiff: true")
	}
	if res.PatchID == "" {
		t.Fatalf("expected non-empty PatchID")
	}
	if len(res.PatchID) < 20 {
		t.Errorf("expected full sha hex patch_id, got %q", res.PatchID)
	}

	initialPatchID := res.PatchID

	// Amend commit message without changing content -> patch-id must stay identical!
	run("commit", "--amend", "-m", "feat: improved commit message with identical diff")
	res2, err := ComputeTreePatchID(ctx, dir, "main", false)
	if err != nil {
		t.Fatalf("ComputeTreePatchID after amend failed: %v", err)
	}
	if res2.PatchID != initialPatchID {
		t.Errorf("expected patch_id %s to remain stable after amend, got %s", initialPatchID, res2.PatchID)
	}
}

func TestComputeTreePatchIDStagingInvariant(t *testing.T) {
	dir := initTestRepo(t)
	ctx := context.Background()

	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\nOutput: %s", args, err, string(out))
		}
	}

	// 1. Modify an already-tracked source file
	readme := filepath.Join(dir, "README.md")
	if err := os.WriteFile(readme, []byte("# Main Repo\n\nAdded section.\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// AC-004: Hash before staging
	resBefore, err := ComputeTreePatchID(ctx, dir, "main", true)
	if err != nil {
		t.Fatalf("ComputeTreePatchID before staging failed: %v", err)
	}
	if !resBefore.HasDiff || resBefore.PatchID == "" {
		t.Fatalf("expected diff before staging")
	}

	// Stage the same bytes
	run("add", "README.md")

	// AC-004: Hash after staging must be identical
	resAfter, err := ComputeTreePatchID(ctx, dir, "main", true)
	if err != nil {
		t.Fatalf("ComputeTreePatchID after staging failed: %v", err)
	}
	if resAfter.PatchID != resBefore.PatchID {
		t.Errorf("staging invariant violated: before=%s after=%s", resBefore.PatchID, resAfter.PatchID)
	}
}

func TestComputeTreePatchIDBaselineExclusion(t *testing.T) {
	dir := initTestRepo(t)
	ctx := context.Background()

	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\nOutput: %s", args, err, string(out))
		}
	}

	run("checkout", "-b", "feature")

	// Baseline tracked source modification
	readme := filepath.Join(dir, "README.md")
	if err := os.WriteFile(readme, []byte("# Main Repo\n\nInitial modification.\n"), 0644); err != nil {
		t.Fatal(err)
	}
	run("add", "README.md")

	baseRes, err := ComputeTreePatchID(ctx, dir, "main", true)
	if err != nil {
		t.Fatalf("ComputeTreePatchID failed: %v", err)
	}
	baseHash := baseRes.PatchID

	// AC-005: Create and stage goals/demo/baseline.json
	demoGoalDir := filepath.Join(dir, "goals", "demo")
	if err := os.MkdirAll(demoGoalDir, 0755); err != nil {
		t.Fatal(err)
	}
	baselineFile := filepath.Join(demoGoalDir, "baseline.json")
	if err := os.WriteFile(baselineFile, []byte(`{"goal_slug":"demo","patch_id":"12345"}`+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	run("add", baselineFile)

	resWithBaseline, err := ComputeTreePatchID(ctx, dir, "main", true)
	if err != nil {
		t.Fatalf("ComputeTreePatchID with staged baseline failed: %v", err)
	}
	if resWithBaseline.PatchID != baseHash {
		t.Errorf("AC-005 violated: staging baseline.json altered candidate hash: got %s, want %s", resWithBaseline.PatchID, baseHash)
	}

	// Update baseline.json and stage again -> hash must remain unchanged
	if err := os.WriteFile(baselineFile, []byte(`{"goal_slug":"demo","patch_id":"67890","updated":true}`+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	run("add", baselineFile)

	resWithUpdatedBaseline, err := ComputeTreePatchID(ctx, dir, "main", true)
	if err != nil {
		t.Fatalf("ComputeTreePatchID with updated baseline failed: %v", err)
	}
	if resWithUpdatedBaseline.PatchID != baseHash {
		t.Errorf("AC-005 violated: updating baseline.json altered candidate hash: got %s, want %s", resWithUpdatedBaseline.PatchID, baseHash)
	}

	// AC-006: Actual source change DOES alter hash
	if err := os.WriteFile(readme, []byte("# Main Repo\n\nSecondary real modification.\n"), 0644); err != nil {
		t.Fatal(err)
	}
	resSourceChange, err := ComputeTreePatchID(ctx, dir, "main", true)
	if err != nil {
		t.Fatalf("ComputeTreePatchID after real source change failed: %v", err)
	}
	if resSourceChange.PatchID == baseHash {
		t.Errorf("AC-006 violated: real source edit did not change patch ID")
	}

	// AC-006: goal.md change DOES alter hash
	goalMD := filepath.Join(demoGoalDir, "goal.md")
	if err := os.WriteFile(goalMD, []byte("# Goal: Demo\n"), 0644); err != nil {
		t.Fatal(err)
	}
	run("add", goalMD)
	resGoalMDChange, err := ComputeTreePatchID(ctx, dir, "main", true)
	if err != nil {
		t.Fatalf("ComputeTreePatchID after goal.md change failed: %v", err)
	}
	if resGoalMDChange.PatchID == resSourceChange.PatchID {
		t.Errorf("AC-006 violated: goal.md edit did not alter patch ID")
	}

	// AC-006: Newly staged source file DOES alter hash
	newSrc := filepath.Join(dir, "new_file.go")
	if err := os.WriteFile(newSrc, []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}
	run("add", newSrc)
	resNewFileChange, err := ComputeTreePatchID(ctx, dir, "main", true)
	if err != nil {
		t.Fatalf("ComputeTreePatchID after staging new file failed: %v", err)
	}
	if resNewFileChange.PatchID == resGoalMDChange.PatchID {
		t.Errorf("AC-006 violated: newly staged source did not alter patch ID")
	}

	// AC-006: HEAD-only mode remains compatible
	run("commit", "-m", "feat: commit all changes")
	resHeadOnly, err := ComputeTreePatchID(ctx, dir, "main", false)
	if err != nil {
		t.Fatalf("ComputeTreePatchID in HEAD-only mode failed: %v", err)
	}
	if !resHeadOnly.HasDiff || resHeadOnly.PatchID == "" {
		t.Errorf("expected non-empty patch ID in HEAD-only mode")
	}
}
