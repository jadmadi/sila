package goals

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestClassifyLegacyStates(t *testing.T) {
	root := t.TempDir()
	mk := func(slug string, files map[string]string) {
		dir := filepath.Join(root, "goals", slug)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for name, body := range files {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	mk("audit-only", map[string]string{"goal.md": "# Goal\n", "audit-receipt.json": "{}"})
	mk("cert-only", map[string]string{"goal.md": "# Goal\n", "certification.json": "{}"})
	mk("empty", map[string]string{"goal.md": "# Goal\n"})

	cases := map[string]string{
		"audit-only": ReconcileLegacyAudit,
		"cert-only":  ReconcileLegacyCert,
		"empty":      ReconcileUnbound,
	}
	for slug, want := range cases {
		if got := ClassifyGoalVerification(root, slug); got.State != want {
			t.Errorf("%s: state = %s, want %s", slug, got.State, want)
		}
	}
}

func TestReverifyAllRefusesUnverifiedGoals(t *testing.T) {
	root := setupVerificationRepo(t)
	other := filepath.Join(root, "goals", "other-goal")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"goal.md":  "# Goal: Other\n\n- **Files To Touch**: internal/app/other.go\n",
		"facts.md": "# Facts\n",
		"plan.md":  "# Plan\n\n- [x] done\n",
	} {
		if err := os.WriteFile(filepath.Join(other, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	commitFile(t, root, "internal/app/other.go", "package app\n\nfunc Other() {}\n")
	cmd := exec.Command("git", "-C", root, "commit", "--allow-empty", "-q", "-m",
		"feat: implement both goals (goals/test-goal/goal.md) (goals/other-goal/goal.md)")
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.test",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.test")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}

	if _, err := RecordVerification(root, "test-goal", LevelL2, "verify"); err != nil {
		t.Fatal(err)
	}

	rep, err := ReverifyAll(root)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Total != 2 || rep.Reanchored != 1 || rep.Refused != 1 {
		t.Fatalf("report = %+v, want total 2, reanchored 1, refused 1", rep)
	}
	for _, out := range rep.Goals {
		switch out.Slug {
		case "test-goal":
			if !out.Reanchored {
				t.Errorf("test-goal should be re-anchored: %+v", out)
			}
		case "other-goal":
			if out.Reanchored || out.State != "refused" {
				t.Errorf("other-goal should be refused: %+v", out)
			}
		}
	}
}

func TestSelectStaleAuditCandidatesIsBoundedAndResumable(t *testing.T) {
	root := setupVerificationRepo(t)
	legacy := filepath.Join(root, "goals", "legacy-goal")
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"goal.md":            "# Goal: Legacy\n\n- **Files To Touch**: internal/app/legacy.go\n",
		"facts.md":           "# Facts\n",
		"plan.md":            "# Plan\n\n- [x] done\n",
		"audit-receipt.json": "{}",
	} {
		if err := os.WriteFile(filepath.Join(legacy, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	commitFile(t, root, "internal/app/legacy.go", "package app\n\nfunc Legacy() {}\n")
	cmd := exec.Command("git", "-C", root, "commit", "--allow-empty", "-q", "-m",
		"feat: add legacy goal (goals/legacy-goal/goal.md) (goals/test-goal/goal.md)")
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.test",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.test")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}

	if _, err := RecordVerification(root, "test-goal", LevelL2, "verify"); err != nil {
		t.Fatal(err)
	}
	commitFile(t, root, "internal/app/main.go", "package app\n\nfunc Run() { println(7) }\n")

	first, err := SelectStaleAuditCandidates(root, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 || first[0].Slug != "test-goal" || first[0].State != StateStale {
		t.Fatalf("first batch = %+v, want the single changed goal", first)
	}
	unbounded, err := SelectStaleAuditCandidates(root, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(unbounded) != 2 {
		t.Fatalf("batch = %+v, want changed goal plus legacy audit", unbounded)
	}

	if _, err := RecordAuditOutcome(root, "test-goal", AuditStateInconclusive, "harness could not run"); err != nil {
		t.Fatal(err)
	}
	resumed, err := SelectStaleAuditCandidates(root, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(resumed) != 1 || resumed[0].Slug != "legacy-goal" {
		t.Fatalf("resumed batch = %+v, want only the untouched legacy goal", resumed)
	}
	if _, err := RecordAuditOutcome(root, "legacy-goal", AuditStatePassed, ""); err != nil {
		t.Fatal(err)
	}
	done, err := SelectStaleAuditCandidates(root, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(done) != 0 {
		t.Fatalf("batch = %+v, want an empty queue", done)
	}
}

func TestClassifyContentStates(t *testing.T) {
	root := setupVerificationRepo(t)
	if _, err := RecordVerification(root, "test-goal", LevelL2, "verify"); err != nil {
		t.Fatal(err)
	}
	if got := ClassifyGoalVerification(root, "test-goal"); got.State != StateVerified {
		t.Fatalf("state = %s, want verified", got.State)
	}

	commitFile(t, root, "internal/app/main.go", "package app\n\nfunc Run() { println(9) }\n")
	if got := ClassifyGoalVerification(root, "test-goal"); got.State != StateStale {
		t.Fatalf("state = %s, want stale", got.State)
	}

	commitFile(t, root, "goals/test-goal/plan.md", "# Plan\n\n- [x] done\n- [ ] extra\n")
	if got := ClassifyGoalVerification(root, "test-goal"); got.State != StateStaleSpec {
		t.Fatalf("state = %s, want stale_spec", got.State)
	}
}

func TestRequireFilesToTouch(t *testing.T) {
	if err := RequireFilesToTouch("with-files", []string{"internal/app/main.go"}); err != nil {
		t.Fatalf("declared files should pass: %v", err)
	}
	err := RequireFilesToTouch("no-files", nil)
	if err == nil {
		t.Fatal("expected the authoring guard to reject a goal without Files To Touch")
	}
	if !strings.Contains(err.Error(), "Files To Touch") || !strings.Contains(err.Error(), "--files") {
		t.Fatalf("error should name Files To Touch and the flag: %v", err)
	}
}

func TestDefaultContractTestPaths(t *testing.T) {
	root := setupVerificationRepo(t)
	commitFile(t, root, "internal/app/main_test.go", "package app\n\nimport \"testing\"\n\nfunc TestRun(t *testing.T) {}\n")

	paths, err := DefaultContractTestPaths(root, "test-goal")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 0 {
		t.Fatalf("main.go is not a test path, got %v", paths)
	}

	goalPath := filepath.Join(root, "goals", "test-goal", "goal.md")
	if err := os.WriteFile(goalPath, []byte("# Goal: Test\n\n- **Files To Touch**: internal/app, go.mod\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	paths, err = DefaultContractTestPaths(root, "test-goal")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 || paths[0] != "internal/app" {
		t.Fatalf("expected the test-bearing directory, got %v", paths)
	}
}

func TestContractBindsCommandAndTests(t *testing.T) {
	root := setupVerificationRepo(t)
	testPath := filepath.Join(root, "internal", "app", "main_test.go")
	if err := os.WriteFile(testPath, []byte("package app\n\nimport \"testing\"\n\nfunc TestRun(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	contract, err := CreateVerificationContract(root, "go test ./internal/app/", 0, 0, 0, []string{"internal/app/main_test.go"})
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveContract(root, "test-goal", contract); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadContract(root, "test-goal")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Command != contract.Command || loaded.ContractHash != contract.ContractHash {
		t.Fatalf("loaded contract drifted: %+v", loaded)
	}
	if err := VerifyContractIntegrity(root, loaded); err != nil {
		t.Fatalf("fresh contract should verify: %v", err)
	}
	if err := os.WriteFile(testPath, []byte("package app\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := VerifyContractIntegrity(root, loaded); err == nil {
		t.Fatal("expected tamper detection after the bound test changed")
	}
}

func TestDeclaredTouchPathsReadsSectionForm(t *testing.T) {
	root := setupVerificationRepo(t)
	goalPath := filepath.Join(root, "goals", "test-goal", "goal.md")
	body := "# Goal: Test\n\n## Files to Touch\n- internal/app/main.go\n- internal/app/extra.go\n"
	if err := os.WriteFile(goalPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	paths, err := DeclaredTouchPaths(root, "test-goal")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"internal/app/extra.go", "internal/app/main.go"}
	if len(paths) != len(want) || paths[0] != want[0] || paths[1] != want[1] {
		t.Fatalf("paths = %v, want %v", paths, want)
	}
}
