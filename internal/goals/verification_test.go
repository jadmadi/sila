package goals

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// setupVerificationRepo builds a small git repo with one goal package.
func setupVerificationRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"goals/test-goal/goal.md":  "# Goal: Test\n\n- **Files To Touch**: internal/app/main.go, go.mod\n",
		"goals/test-goal/facts.md": "# Facts\n",
		"goals/test-goal/plan.md":  "# Plan\n\n- [x] done\n",
		"internal/app/main.go":     "package app\n\nfunc Run() {}\n",
		"go.mod":                   "module example.test\n\ngo 1.27\n",
	}
	for rel, body := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.test",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.test")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	run("add", ".")
	run("commit", "-q", "-m", "init")
	return root
}

func commitFile(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "-C", root, "add", rel)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
	cmd = exec.Command("git", "-C", root, "commit", "-q", "-m", "change")
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.test",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.test")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}
}

func TestRecordVerificationBindsContent(t *testing.T) {
	root := setupVerificationRepo(t)
	rec, err := RecordVerification(root, "test-goal", LevelL2, "verify")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Subject.Hash == "" || rec.SpecHash == "" {
		t.Fatal("expected spec and subject hashes")
	}
	state, changed, err := ComputeVerificationState(root, "test-goal", rec)
	if err != nil {
		t.Fatal(err)
	}
	if state != StateVerified || len(changed) != 0 {
		t.Fatalf("state=%s changed=%v, want verified and none", state, changed)
	}
}

func TestVerificationSurvivesHistoryRewrite(t *testing.T) {
	root := setupVerificationRepo(t)
	rec, err := RecordVerification(root, "test-goal", LevelL2, "verify")
	if err != nil {
		t.Fatal(err)
	}
	// Amend the commit: same content, new commit hash.
	cmd := exec.Command("git", "-C", root, "commit", "--amend", "-q", "--no-edit")
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.test",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.test")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("amend: %v\n%s", err, out)
	}
	state, _, err := ComputeVerificationState(root, "test-goal", rec)
	if err != nil {
		t.Fatal(err)
	}
	if state != StateVerified {
		t.Fatalf("state = %s after amend, want verified", state)
	}
}

func TestVerificationDetectsContentDrift(t *testing.T) {
	root := setupVerificationRepo(t)
	if _, err := RecordVerification(root, "test-goal", LevelL2, "verify"); err != nil {
		t.Fatal(err)
	}
	rec, _ := LoadVerification(root, "test-goal")
	commitFile(t, root, "internal/app/main.go", "package app\n\nfunc Run() { println(1) }\n")
	state, changed, err := ComputeVerificationState(root, "test-goal", rec)
	if err != nil {
		t.Fatal(err)
	}
	if state != StateStale {
		t.Fatalf("state = %s, want stale", state)
	}
	if len(changed) == 0 || !strings.Contains(strings.Join(changed, ","), "internal/app/main.go") {
		t.Fatalf("changed = %v, want internal/app/main.go", changed)
	}
}

func TestVerificationDetectsSpecDrift(t *testing.T) {
	root := setupVerificationRepo(t)
	if _, err := RecordVerification(root, "test-goal", LevelL2, "verify"); err != nil {
		t.Fatal(err)
	}
	rec, _ := LoadVerification(root, "test-goal")
	commitFile(t, root, "goals/test-goal/plan.md", "# Plan\n\n- [x] done\n- [ ] new scope\n")
	state, _, err := ComputeVerificationState(root, "test-goal", rec)
	if err != nil {
		t.Fatal(err)
	}
	if state != StateStaleSpec {
		t.Fatalf("state = %s, want stale_spec", state)
	}
}

func TestReverifyRefusesChangedContent(t *testing.T) {
	root := setupVerificationRepo(t)
	if _, err := RecordVerification(root, "test-goal", LevelL2, "verify"); err != nil {
		t.Fatal(err)
	}
	commitFile(t, root, "internal/app/main.go", "package app\n\nfunc Run() { println(2) }\n")
	if _, err := Reverify(root, "test-goal"); err == nil {
		t.Fatal("expected re-anchor to be refused after content change")
	}
}

func TestReverifyAnchorsUnchangedContent(t *testing.T) {
	root := setupVerificationRepo(t)
	if _, err := RecordVerification(root, "test-goal", LevelL2, "verify"); err != nil {
		t.Fatal(err)
	}
	rec, err := Reverify(root, "test-goal")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Method != "reanchor" {
		t.Fatalf("method = %s, want reanchor", rec.Method)
	}
	if rec.PreviousHash == "" {
		t.Fatal("expected a receipt chain hash")
	}
}

func TestRecordVerificationRefusesMissingPaths(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "goals", "no-paths")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "goal.md"), []byte("# Goal: No paths\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := RecordVerification(root, "no-paths", LevelL1, "evidence"); err == nil {
		t.Fatal("expected an error when no Files To Touch are declared")
	}
}

func TestSubjectIgnoresGoalArtifacts(t *testing.T) {
	root := setupVerificationRepo(t)
	subj, err := HashSubject(root, []string{"goals/test-goal"})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range subj.Paths {
		for _, artifact := range []string{"audit-receipt.json", "certification.json", "verification.json", "defect-dossier.json"} {
			if strings.Contains(p.Path, artifact) {
				t.Fatalf("subject includes goal artifact %q", p.Path)
			}
		}
	}
}

func TestSubjectIgnoresReleaseNotes(t *testing.T) {
	root := setupVerificationRepo(t)
	for _, rel := range []string{"README.md", "CHANGELOG.md", "project.jsonc"} {
		commitFile(t, root, rel, "v1\n")
	}
	paths := []string{"internal/app/main.go", "README.md", "CHANGELOG.md", "project.jsonc"}
	first, err := HashSubject(root, paths)
	if err != nil {
		t.Fatal(err)
	}
	commitFile(t, root, "README.md", "v2 with new notes\n")
	second, err := HashSubject(root, paths)
	if err != nil {
		t.Fatal(err)
	}
	if first.Hash != second.Hash {
		t.Fatalf("release notes changed the subject hash: %s -> %s", first.Hash, second.Hash)
	}
}
