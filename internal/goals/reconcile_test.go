package goals

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func newReconcileRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	runGit(t, root, "init")
	runGit(t, root, "config", "user.email", "test@example.com")
	runGit(t, root, "config", "user.name", "Test")
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	commitAll(t, root, "fixture")
	return root
}

func runGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func commitAll(t *testing.T, root, msg string) {
	t.Helper()
	runGit(t, root, "add", "-A")
	runGit(t, root, "commit", "-m", msg)
}

func writeGoalFixture(t *testing.T, root, slug string, touch []string, checked bool) {
	t.Helper()
	dir := filepath.Join(root, "goals", slug)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var goal strings.Builder
	goal.WriteString("files_to_touch:\n")
	for _, p := range touch {
		fmt.Fprintf(&goal, "  - %s\n", p)
	}
	fmt.Fprintf(&goal, "\n# Goal: %s\n\n## Description\nFixture goal.\n", slug)
	if err := os.WriteFile(filepath.Join(dir, "goal.md"), []byte(goal.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	plan := "# Plan\n\n- [x] one\n- [x] two\n"
	if !checked {
		plan = "# Plan\n\n- [ ] one\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "plan.md"), []byte(plan), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "facts.md"), []byte("# Facts\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func findCandidate(report *TreeReconcileReport, slug string) *TreeReconcileCandidate {
	for i := range report.Candidates {
		if report.Candidates[i].Slug == slug {
			return &report.Candidates[i]
		}
	}
	return nil
}

func TestReconcileTreeProposesCompleteGoals(t *testing.T) {
	root := newReconcileRepo(t)
	writeGoalFixture(t, root, "alpha", []string{"src/a.go"}, true)
	writeGoalFixture(t, root, "missing", []string{"src/missing.go"}, true)
	writeGoalFixture(t, root, "unchecked", []string{"src/a.go"}, false)
	commitAll(t, root, "fixture goals")

	report, err := ReconcileTree(root, false, false)
	if err != nil {
		t.Fatal(err)
	}
	alpha := findCandidate(report, "alpha")
	if alpha == nil {
		t.Fatalf("alpha not proposed: %+v", report)
	}
	if alpha.PlanChecked != 2 || alpha.PlanTotal != 2 {
		t.Fatalf("plan counts = %d/%d", alpha.PlanChecked, alpha.PlanTotal)
	}
	if len(alpha.MatchedFiles) != 1 || alpha.MatchedFiles[0] != "src/a.go" {
		t.Fatalf("matched files = %+v", alpha.MatchedFiles)
	}
	if alpha.Action != "propose" || alpha.Head == "" {
		t.Fatalf("candidate = %+v", alpha)
	}
	if findCandidate(report, "missing") != nil {
		t.Fatal("goal with a missing touch file was proposed")
	}
	if findCandidate(report, "unchecked") != nil {
		t.Fatal("goal with an unchecked plan was proposed")
	}
}

func TestReconcileTreeApplyWritesReceiptNotCertification(t *testing.T) {
	root := newReconcileRepo(t)
	writeGoalFixture(t, root, "alpha", []string{"src/a.go"}, true)
	commitAll(t, root, "fixture goals")

	report, err := ReconcileTree(root, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Applied) != 1 || report.Applied[0].Slug != "alpha" {
		t.Fatalf("applied = %+v", report.Applied)
	}
	dir := filepath.Join(root, "goals", "alpha")
	retro, err := GetActiveRetrospective(root, "alpha")
	if err != nil || retro == nil || retro.Status != "implemented" || retro.Commit == "" {
		t.Fatalf("retrospective = %+v err=%v", retro, err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "tree-attribution.json"))
	if err != nil {
		t.Fatal(err)
	}
	var attr TreeAttribution
	if err := json.Unmarshal(raw, &attr); err != nil {
		t.Fatal(err)
	}
	if attr.Version != "sila/tree-attribution/v1" || attr.AttributedBy != "tree-reconcile" || attr.TreeHash == "" {
		t.Fatalf("attribution = %+v", attr)
	}
	if len(attr.MatchedFiles) != 1 || attr.MatchedFiles[0] != "src/a.go" {
		t.Fatalf("matched files = %+v", attr.MatchedFiles)
	}
	if _, err := os.Stat(filepath.Join(dir, "certification.json")); !os.IsNotExist(err) {
		t.Fatal("tree reconcile must not create certification.json")
	}
	rawReport, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"candidates":[`, `"applied":[`, `"matched_files"`, `"plan_checked"`} {
		if !strings.Contains(string(rawReport), key) {
			t.Fatalf("report JSON missing %s: %s", key, rawReport)
		}
	}
	again, err := ReconcileTree(root, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if findCandidate(again, "alpha") != nil {
		t.Fatal("goal with a current tree attribution was proposed again")
	}
	audit, err := ScanGoals(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range audit.Goals {
		if g.Slug == "alpha" && g.Status != "implemented" {
			t.Fatalf("status = %q, want implemented", g.Status)
		}
	}
}

func TestReconcileTreeSkipsVerifiedAndRequiresCleanApply(t *testing.T) {
	root := newReconcileRepo(t)
	writeGoalFixture(t, root, "verified", []string{"src/a.go"}, true)
	cert := `{"goal_slug":"verified","verified":true,"commit_sha":"deadbeef"}`
	if err := os.WriteFile(filepath.Join(root, "goals", "verified", "certification.json"), []byte(cert), 0o644); err != nil {
		t.Fatal(err)
	}
	commitAll(t, root, "fixture goals")

	report, err := ReconcileTree(root, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if findCandidate(report, "verified") != nil {
		t.Fatal("verified goal was proposed")
	}

	if err := os.WriteFile(filepath.Join(root, "src", "dirty.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReconcileTree(root, true, false); err == nil {
		t.Fatal("apply should refuse a dirty work tree")
	}
	if _, err := ReconcileTree(root, true, true); err != nil {
		t.Fatalf("apply with --allow-dirty: %v", err)
	}
	if _, err := GetActiveRetrospective(root, "verified"); err != nil {
		t.Fatal(err)
	}
	if retro, _ := GetActiveRetrospective(root, "verified"); retro != nil {
		t.Fatal("verified goal received a tree retrospective")
	}
}
