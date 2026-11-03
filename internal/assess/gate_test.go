package assess

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func initGateTestRepo(t *testing.T) string {
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

	// Branch out
	run("checkout", "-b", "feat/my-goal")
	return dir
}

func TestBaselinePatchPersistence(t *testing.T) {
	tmp := t.TempDir()
	slug := "test-goal"

	// 1. Initial load should return nil
	rec, err := LoadBaselinePatch(tmp, slug)
	if err != nil {
		t.Fatalf("unexpected error loading non-existent baseline: %v", err)
	}
	if rec != nil {
		t.Fatalf("expected nil baseline record, got %+v", rec)
	}

	// 2. Store baseline
	stored, err := StoreBaselinePatch(tmp, slug, "patch-hash-12345", "main", "@antigravity")
	if err != nil {
		t.Fatalf("StoreBaselinePatch failed: %v", err)
	}
	if stored.PatchID != "patch-hash-12345" || stored.GoalSlug != slug {
		t.Fatalf("stored record mismatch: %+v", stored)
	}

	// 3. Load baseline back
	loaded, err := LoadBaselinePatch(tmp, slug)
	if err != nil {
		t.Fatalf("LoadBaselinePatch failed: %v", err)
	}
	if loaded == nil || loaded.PatchID != "patch-hash-12345" {
		t.Fatalf("loaded record mismatch: %+v", loaded)
	}
}

func TestCheckRebaseDrift_Lifecycle(t *testing.T) {
	dir := initGateTestRepo(t)
	ctx := context.Background()
	slug := "test-goal"

	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\nOutput: %s", args, err, string(out))
		}
	}

	// Add a change on branch
	f := filepath.Join(dir, "feature.go")
	if err := os.WriteFile(f, []byte("package main\n\nfunc Foo() int { return 1 }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	run("add", "feature.go")
	run("commit", "-m", "feat: initial goal work")

	// 1. Initial baseline calculation
	rep1, err := CheckRebaseDrift(ctx, dir, slug, "main", nil)
	if err != nil {
		t.Fatalf("CheckRebaseDrift initial failed: %v", err)
	}
	if rep1.DriftDetected {
		t.Errorf("expected DriftDetected: false on initial recording")
	}
	if rep1.BaselinePatch == "" {
		t.Errorf("expected non-empty BaselinePatch")
	}
	initialPatch := rep1.BaselinePatch

	// 2. Unchanged state: matches baseline
	rep2, err := CheckRebaseDrift(ctx, dir, slug, "main", nil)
	if err != nil {
		t.Fatalf("CheckRebaseDrift second run failed: %v", err)
	}
	if rep2.DriftDetected {
		t.Errorf("expected DriftDetected: false on identical tree")
	}
	if rep2.BaselinePatch != initialPatch || rep2.CurrentPatch != initialPatch {
		t.Errorf("patch mismatch: baseline=%s current=%s", rep2.BaselinePatch, rep2.CurrentPatch)
	}

	// 3. Semantic code change (divergence) with passing test runner
	if err := os.WriteFile(f, []byte("package main\n\nfunc Foo() int { return 2 }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	run("add", "feature.go")
	run("commit", "-m", "feat: modified logic")

	mockRunnerCalled := false
	mockPassRunner := func(ctx context.Context, rootDir string) (bool, string, error) {
		mockRunnerCalled = true
		return true, "ok", nil
	}

	rep3, err := CheckRebaseDrift(ctx, dir, slug, "main", mockPassRunner)
	if err != nil {
		t.Fatalf("CheckRebaseDrift drift run failed: %v", err)
	}
	if !mockRunnerCalled {
		t.Errorf("expected testRunner to be invoked upon drift")
	}
	if !rep3.DriftDetected {
		t.Errorf("expected DriftDetected: true on code change")
	}
	if !rep3.ReTestPassed {
		t.Errorf("expected ReTestPassed: true")
	}
	if rep3.CurrentPatch == initialPatch {
		t.Errorf("expected CurrentPatch to differ from initialPatch")
	}

	// Baseline should now be updated to rep3.CurrentPatch
	loaded, _ := LoadBaselinePatch(dir, slug)
	if loaded == nil || loaded.PatchID != rep3.CurrentPatch {
		t.Errorf("expected baseline to be updated to %s, got %+v", rep3.CurrentPatch, loaded)
	}

	// 4. Another change with failing test runner
	if err := os.WriteFile(f, []byte("package main\n\nfunc Foo() int { return 3 }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	run("add", "feature.go")
	run("commit", "-m", "feat: breaking change")

	mockFailRunner := func(ctx context.Context, rootDir string) (bool, string, error) {
		return false, "tests failed", errors.New("assertion failed")
	}

	rep4, err := CheckRebaseDrift(ctx, dir, slug, "main", mockFailRunner)
	if err != nil {
		t.Fatalf("CheckRebaseDrift failing test run returned err: %v", err)
	}
	if !rep4.DriftDetected {
		t.Errorf("expected DriftDetected: true")
	}
	if rep4.ReTestPassed {
		t.Errorf("expected ReTestPassed: false for failing test runner")
	}
}

func TestRebaseDriftFailurePreservesBaseline(t *testing.T) {
	dir := initGateTestRepo(t)
	ctx := context.Background()
	slug := "test-goal"

	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\nOutput: %s", args, err, string(out))
		}
	}

	// 1. Initial change on branch to establish baseline
	f := filepath.Join(dir, "feature.go")
	if err := os.WriteFile(f, []byte("package main\n\nfunc Value() int { return 10 }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	run("add", "feature.go")
	run("commit", "-m", "feat: initial feature commit")

	repInit, err := CheckRebaseDrift(ctx, dir, slug, "main", nil)
	if err != nil {
		t.Fatalf("initial CheckRebaseDrift failed: %v", err)
	}
	if repInit.BaselinePatch == "" {
		t.Fatalf("expected non-empty baseline patch")
	}
	initialPatch := repInit.BaselinePatch

	// Read initial baseline bytes to verify they stay byte-for-byte identical on failed/cancelled retests
	baselinePath := BaselinePatchPath(dir, slug)
	initialBytes, err := os.ReadFile(baselinePath)
	if err != nil {
		t.Fatalf("read baseline file: %v", err)
	}

	// 2. Introduce semantic code change causing drift
	if err := os.WriteFile(f, []byte("package main\n\nfunc Value() int { return 20 }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	run("add", "feature.go")
	run("commit", "-m", "feat: drift change 1")

	// Test Case A: Test runner fails
	mockFailRunner := func(ctx context.Context, rootDir string) (bool, string, error) {
		return false, "tests failed", errors.New("unit test failure")
	}
	repFail, err := CheckRebaseDrift(ctx, dir, slug, "main", mockFailRunner)
	if err != nil {
		t.Fatalf("CheckRebaseDrift with failing test runner failed: %v", err)
	}
	if !repFail.DriftDetected || repFail.ReTestPassed {
		t.Fatalf("expected DriftDetected=true and ReTestPassed=false, got drift=%v passed=%v", repFail.DriftDetected, repFail.ReTestPassed)
	}
	// Verify baseline was NOT modified
	bytesAfterFail, err := os.ReadFile(baselinePath)
	if err != nil {
		t.Fatalf("read baseline file: %v", err)
	}
	if string(bytesAfterFail) != string(initialBytes) {
		t.Errorf("baseline file bytes modified on failing retest; expected:\n%s\ngot:\n%s", string(initialBytes), string(bytesAfterFail))
	}

	// Test Case B: Cancelled / timed-out context during re-test
	cancelCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mockCancelledRunner := func(ctx context.Context, rootDir string) (bool, string, error) {
		cancel() // cancel during test execution
		return false, "context cancelled", cancelCtx.Err()
	}
	repCancel, err := CheckRebaseDrift(cancelCtx, dir, slug, "main", mockCancelledRunner)
	if err != nil {
		t.Fatalf("CheckRebaseDrift with cancelled context failed: %v", err)
	}
	if repCancel.ReTestPassed {
		t.Fatalf("expected ReTestPassed=false on cancelled context")
	}
	bytesAfterCancel, err := os.ReadFile(baselinePath)
	if err != nil {
		t.Fatalf("read baseline file: %v", err)
	}
	if string(bytesAfterCancel) != string(initialBytes) {
		t.Errorf("baseline file bytes modified on cancelled retest")
	}

	// Test Case C: Inconsistent test runner (passed=true but testErr != nil)
	mockInconsistentRunner := func(ctx context.Context, rootDir string) (bool, string, error) {
		return true, "inconsistent", errors.New("spurious error with passed=true")
	}
	repInconsistent, err := CheckRebaseDrift(ctx, dir, slug, "main", mockInconsistentRunner)
	if err != nil {
		t.Fatalf("CheckRebaseDrift with inconsistent runner failed: %v", err)
	}
	if repInconsistent.ReTestPassed {
		t.Fatalf("expected ReTestPassed=false on inconsistent test runner output")
	}
	bytesAfterInconsistent, err := os.ReadFile(baselinePath)
	if err != nil {
		t.Fatalf("read baseline file: %v", err)
	}
	if string(bytesAfterInconsistent) != string(initialBytes) {
		t.Errorf("baseline file bytes modified on inconsistent retest")
	}

	// Test Case D: Passing test runner, but baseline target is unwritable
	if err := os.Chmod(baselinePath, 0400); err != nil {
		t.Fatalf("chmod baseline file: %v", err)
	}
	mockPassRunner := func(ctx context.Context, rootDir string) (bool, string, error) {
		return true, "tests passed", nil
	}
	_, errWriteFail := CheckRebaseDrift(ctx, dir, slug, "main", mockPassRunner)
	if errWriteFail == nil {
		t.Fatalf("expected CheckRebaseDrift to fail when baseline target is unwritable, got nil error")
	}
	// Restore write permissions
	_ = os.Chmod(baselinePath, 0644)

	// Test Case E: Passing test runner successfully updates baseline
	repPass, err := CheckRebaseDrift(ctx, dir, slug, "main", mockPassRunner)
	if err != nil {
		t.Fatalf("CheckRebaseDrift passing retest failed: %v", err)
	}
	if !repPass.DriftDetected || !repPass.ReTestPassed {
		t.Fatalf("expected DriftDetected=true and ReTestPassed=true, got drift=%v passed=%v", repPass.DriftDetected, repPass.ReTestPassed)
	}
	loadedPass, err := LoadBaselinePatch(dir, slug)
	if err != nil {
		t.Fatalf("load baseline after passing retest: %v", err)
	}
	if loadedPass.PatchID == initialPatch {
		t.Errorf("expected baseline patch to be updated to %s, still got initial %s", repPass.CurrentPatch, initialPatch)
	}
	if loadedPass.PatchID != repPass.CurrentPatch {
		t.Errorf("loaded baseline %s does not match current patch %s", loadedPass.PatchID, repPass.CurrentPatch)
	}
}
