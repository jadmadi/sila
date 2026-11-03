package sentry

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/store"
)

func TestParseAttribution(t *testing.T) {
	cases := []struct {
		subject   string
		slug      string
		closure   bool
		docsOnly  bool
		ambiguous bool
	}{
		{"feat: add x (goals/demo/goal.md)", "demo", true, false, false},
		{"fix(scope): add x [goal: demo]", "demo", true, false, false},
		{"feat!: breaking (goals/demo/goal.md)", "demo", true, false, false},
		{"docs: note (goals/demo/goal.md)", "demo", false, true, false},
		{"test: note [goal: demo]", "demo", false, true, false},
		{"chore(goals): register demo", "", false, true, false},
		{"feat: two (goals/a/goal.md) [goal: b]", "", true, false, true},
		{"chore: release", "", true, false, false},
	}
	for _, tc := range cases {
		slug, closure, docs, ambiguous := ParseAttribution(tc.subject)
		if slug != tc.slug || closure != tc.closure || docs != tc.docsOnly || ambiguous != tc.ambiguous {
			t.Fatalf("%q = (%q,%v,%v,%v), want (%q,%v,%v,%v)", tc.subject, slug, closure, docs, ambiguous, tc.slug, tc.closure, tc.docsOnly, tc.ambiguous)
		}
	}
}

func writeMemoryGoal(t *testing.T, repo, slug string) {
	t.Helper()
	dir := filepath.Join(repo, "goals", slug)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "# Goal: " + slug + "\n\n## Files to Touch\n- demo.go\n"
	if err := os.WriteFile(filepath.Join(dir, "goal.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "facts.md"), []byte("# Facts\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plan.md"), []byte("# Plan\n\n- [x] demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCheckMemoryWriteSkipPaths(t *testing.T) {
	repo := t.TempDir()
	res := CheckMemoryWrite(MemoryWriteOptions{RepoRoot: repo, Subject: "feat: x (goals/demo/goal.md)"})
	if res.Status != "skip" || !strings.Contains(res.Detail, "no goals") {
		t.Fatalf("missing goals should skip: %+v", res)
	}
	writeMemoryGoal(t, repo, "demo")
	res = CheckMemoryWrite(MemoryWriteOptions{RepoRoot: repo, Subject: "chore: release"})
	if res.Status != "skip" {
		t.Fatalf("unattributed should skip: %+v", res)
	}
	res = CheckMemoryWrite(MemoryWriteOptions{RepoRoot: repo, Subject: "docs: note (goals/demo/goal.md)"})
	if res.Status != "skip" {
		t.Fatalf("docs should skip: %+v", res)
	}
}

func TestCheckMemoryWriteGroundedMemoPassesAndMissingFails(t *testing.T) {
	repo := t.TempDir()
	writeMemoryGoal(t, repo, "demo")
	dbPath := filepath.Join(t.TempDir(), "memory.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	p, err := st.EnsureProject(repo)
	if err != nil {
		t.Fatal(err)
	}
	st.Close()

	res := CheckMemoryWrite(MemoryWriteOptions{
		RepoRoot: repo,
		Subject:  "feat: demo (goals/demo/goal.md)",
		Staged:   []string{"demo.go"},
		DBPath:   dbPath,
	})
	if res.Status != "fail" || res.Code != "MEMORY_WRITE_MISSING" {
		t.Fatalf("missing write should fail: %+v", res)
	}

	st, err = store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.InsertMemo(p.ID, "demo architecture decision", "tester", repo, []string{"demo.go"}); err != nil {
		st.Close()
		t.Fatal(err)
	}
	st.Close()

	res = CheckMemoryWrite(MemoryWriteOptions{
		RepoRoot: repo,
		Subject:  "feat: demo (goals/demo/goal.md)",
		Staged:   []string{"demo.go"},
		DBPath:   dbPath,
	})
	if res.Status != "pass" || res.Memo != 1 {
		t.Fatalf("grounded write should pass: %+v", res)
	}
}

func TestCheckMemoryWriteWaivers(t *testing.T) {
	repo := t.TempDir()
	writeMemoryGoal(t, repo, "demo")
	for _, args := range [][]string{
		{"init"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
		{"add", "."},
		{"commit", "-m", "feat: demo (goals/demo/goal.md)"},
	} {
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	head, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	sha := strings.TrimSpace(string(head))

	writeWaiver := func(body string) {
		dir := filepath.Join(repo, ".sila")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "memory-write-waiver.json"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	valid := `{"schema":"sila/memory-write-waiver/v1","goal":"demo","reason":"pure refactor with no durable decision","approved_by":"test","expires_commit":"` + sha + `"}`
	writeWaiver(valid)
	res := CheckMemoryWrite(MemoryWriteOptions{RepoRoot: repo, Subject: "feat: demo (goals/demo/goal.md)", Now: time.Now()})
	if res.Status != "pass" || !res.Waived {
		t.Fatalf("valid waiver should pass: %+v", res)
	}

	writeWaiver(strings.Replace(valid, sha, "0000000000000000000000000000000000000000", 1))
	res = CheckMemoryWrite(MemoryWriteOptions{RepoRoot: repo, Subject: "feat: demo (goals/demo/goal.md)"})
	if res.Status != "fail" || res.Code != "MEMORY_WAIVER_EXPIRED" {
		t.Fatalf("expired waiver should fail: %+v", res)
	}

	writeWaiver(`{"schema":"sila/memory-write-waiver/v1","goal":"other","reason":"short","expires_commit":"` + sha + `"}`)
	res = CheckMemoryWrite(MemoryWriteOptions{RepoRoot: repo, Subject: "feat: demo (goals/demo/goal.md)"})
	if res.Status != "fail" || res.Code != "MEMORY_WAIVER_INVALID" {
		t.Fatalf("invalid waiver should fail: %+v", res)
	}
}

func TestCheckMemoryWriteCorruptStoreFailsClosed(t *testing.T) {
	repo := t.TempDir()
	writeMemoryGoal(t, repo, "demo")
	dbPath := filepath.Join(t.TempDir(), "corrupt.db")
	if err := os.WriteFile(dbPath, []byte("not a sqlite database"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := CheckMemoryWrite(MemoryWriteOptions{
		RepoRoot: repo,
		Subject:  "feat: demo (goals/demo/goal.md)",
		Staged:   []string{"demo.go"},
		DBPath:   dbPath,
	})
	if res.Status != "fail" || res.Code != "MEMORY_STORE_UNAVAILABLE" {
		t.Fatalf("corrupt store should fail closed: %+v", res)
	}
}

func TestCheckMemoryWritePreClaimSessionGraceWindow(t *testing.T) {
	repo := t.TempDir()
	writeMemoryGoal(t, repo, "demo")
	dbPath := filepath.Join(t.TempDir(), "grace.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	p, err := st.EnsureProject(repo)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.InsertMemo(p.ID, "grounded memo recorded before claim in same session", "tester", repo, []string{"demo.go"}); err != nil {
		st.Close()
		t.Fatal(err)
	}
	st.Close()

	// Claimed 30 minutes AFTER the memo was written (within 2h PreClaimMemoryGraceWindow)
	claimedAt := time.Now().UTC().Add(30 * time.Minute).Format(time.RFC3339)
	claimPath := filepath.Join(repo, "goals", "demo", "claim.json")
	if err := os.WriteFile(claimPath, []byte(`{"agent":"tester","claimed_at":"`+claimedAt+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	res := CheckMemoryWrite(MemoryWriteOptions{
		RepoRoot: repo,
		Subject:  "feat: demo (goals/demo/goal.md)",
		Staged:   []string{"demo.go"},
		DBPath:   dbPath,
	})
	if res.Status != "pass" {
		t.Fatalf("expected memo written 30m before claim_at to pass within 2h grace window, got: %+v", res)
	}

	// Claimed 3 hours AFTER the memo was written (outside 2h PreClaimMemoryGraceWindow)
	staleClaimedAt := time.Now().UTC().Add(3 * time.Hour).Format(time.RFC3339)
	if err := os.WriteFile(claimPath, []byte(`{"agent":"tester","claimed_at":"`+staleClaimedAt+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	resStale := CheckMemoryWrite(MemoryWriteOptions{
		RepoRoot: repo,
		Subject:  "feat: demo (goals/demo/goal.md)",
		Staged:   []string{"demo.go"},
		DBPath:   dbPath,
	})
	if resStale.Status != "fail" || resStale.Code != "MEMORY_WRITE_MISSING" {
		t.Fatalf("expected memo written 3h before claim_at to fail outside 2h grace window, got: %+v", resStale)
	}
}

func TestCheckMemoryWriteWaiverSchemaHint(t *testing.T) {
	hint := MemoryWaiverSchemaHint("my-goal")
	for _, want := range []string{`"schema":"sila/memory-write-waiver/v1"`, `"goal":"my-goal"`, `"expires_commit":"<HEAD>"`} {
		if !strings.Contains(hint, want) {
			t.Fatalf("MemoryWaiverSchemaHint missing %s: %s", want, hint)
		}
	}
}

func TestCheckMemoryWriteStagedIntersectionRule(t *testing.T) {
	repo := t.TempDir()
	writeMemoryGoal(t, repo, "sila-onboarding")
	if err := os.WriteFile(filepath.Join(repo, "SPEC.md"), []byte("# Spec\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "cmd", "fs-relay"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "cmd", "fs-relay", "serve.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "goals", "README.md"), []byte("# Goals\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	dbPath := filepath.Join(t.TempDir(), "intersection.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	p, err := st.EnsureProject(repo)
	if err != nil {
		t.Fatal(err)
	}
	// Branch A (AC-F1-a): fresh memo grounded to existing files not in the staged closure diff.
	if _, err := st.InsertMemo(p.ID, "Errno travels as a number in error.code.", "opencode", repo, []string{"SPEC.md", "cmd/fs-relay/serve.go"}); err != nil {
		st.Close()
		t.Fatal(err)
	}
	st.Close()

	res := CheckMemoryWrite(MemoryWriteOptions{
		RepoRoot: repo,
		Subject:  "chore: initial commit",
		Staged:   []string{"goals/sila-onboarding/evidence.md", "goals/sila-onboarding/plan.md"},
		DBPath:   dbPath,
	})
	if res.Status != "fail" || res.Code != "MEMORY_WRITE_MISSING" {
		t.Fatalf("expected fail when memo files do not intersect staged diff, got: %+v", res)
	}
	if !strings.Contains(res.Detail, "none of the staged files is referenced by a memory write in the window") {
		t.Fatalf("expected staged-intersection rule in diagnostic, got: %q", res.Detail)
	}
	if !strings.Contains(res.Detail, "SPEC.md") {
		t.Fatalf("expected unmatched window file in diagnostic, got: %q", res.Detail)
	}

	// Branch B (AC-F1-b): memo grounded to a staged file passes.
	st, err = store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.InsertMemo(p.ID, "goals/README.md is generated by sila goals.", "opencode", repo, []string{"goals/README.md"}); err != nil {
		st.Close()
		t.Fatal(err)
	}
	st.Close()

	resPass := CheckMemoryWrite(MemoryWriteOptions{
		RepoRoot: repo,
		Subject:  "chore: initial commit",
		Staged:   []string{"goals/sila-onboarding/evidence.md", "goals/sila-onboarding/plan.md", "goals/README.md"},
		DBPath:   dbPath,
	})
	if resPass.Status != "pass" || resPass.Memo != 1 {
		t.Fatalf("expected memo grounded to staged file to pass, got: %+v", resPass)
	}
}
