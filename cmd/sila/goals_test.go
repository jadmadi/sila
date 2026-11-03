package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGoalsClosureCLIProbe(t *testing.T) {
	if probe := os.Getenv("SILA_GOALS_CLOSURE_PROBE"); probe != "" {
		os.Args = strings.Split(probe, "\x1f")
		main()
		os.Exit(0)
	}
}

func runGoalsClosureCLI(t *testing.T, cwd string, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestGoalsClosureCLIProbe$")
	probe := strings.Join(append([]string{"sila"}, args...), "\x1f")
	cmd.Env = append(os.Environ(), "SILA_GOALS_CLOSURE_PROBE="+probe)
	cmd.Dir = cwd

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			t.Fatalf("failed to run helper subprocess: %v", err)
		}
	}
	return stdout.String(), stderr.String(), exitCode
}

func initClosureGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.name", "Closure Tester"},
		{"config", "user.email", "closure@sila.local"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, out)
		}
	}
	return dir
}

func commitClosureFixture(t *testing.T, dir, msg string) string {
	t.Helper()
	if out, err := exec.Command("git", "-C", dir, "add", "-A").CombinedOutput(); err != nil {
		t.Fatalf("git add failed: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "-C", dir, "commit", "-m", msg).CombinedOutput(); err != nil {
		t.Fatalf("git commit failed: %v\n%s", err, out)
	}
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("git rev-parse HEAD failed: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func writeClosureFixtureGoal(t *testing.T, dir, slug string) {
	t.Helper()
	goalDir := filepath.Join(dir, "goals", slug)
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "pkg"), 0755); err != nil {
		t.Fatal(err)
	}

	files := map[string]string{
		filepath.Join(goalDir, "goal.md"):        "# Goal: " + slug + "\n\n## Files to Touch\n- pkg/demo.go\n",
		filepath.Join(goalDir, "facts.md"):       "# Facts: " + slug + "\n",
		filepath.Join(goalDir, "plan.md"):        "# Plan: " + slug + "\n\n- [x] Phase 1: Complete\n",
		filepath.Join(dir, "pkg", "demo.go"):     "package pkg\n",
		filepath.Join(dir, "goals", "README.md"): "# Goals\nInitial registry\n",
	}
	for path, content := range files {
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestGoalsJSONSyncsRegistryAndClearsClosedClaims(t *testing.T) {
	dir := initClosureGitRepo(t)
	slug := "json-sync-goal"
	writeClosureFixtureGoal(t, dir, slug)

	goalDir := filepath.Join(dir, "goals", slug)
	claim := `{"goal_slug":"` + slug + `","agent":"tester","claimed_at":"2026-01-01T00:00:00Z","status":"in_progress"}`
	if err := os.WriteFile(filepath.Join(goalDir, "claim.json"), []byte(claim), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goalDir, ".claim.lock"), []byte("lock"), 0644); err != nil {
		t.Fatal(err)
	}

	commitClosureFixture(t, dir, "feat: implement json sync goal (goals/"+slug+"/goal.md)")

	// Machine reads are read-only by default: --json must not rewrite the
	// registry or clear the closed claim.
	stdout, stderr, code := runGoalsClosureCLI(t, dir, "goals", "--project="+dir, "--json")
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d, stderr: %s, stdout: %s", code, stderr, stdout)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("expected valid JSON output, got %v: %s", err, stdout)
	}
	readmeBytes, err := os.ReadFile(filepath.Join(dir, "goals", "README.md"))
	if err != nil {
		t.Fatalf("read goals/README.md: %v", err)
	}
	if string(readmeBytes) != "# Goals\nInitial registry\n" {
		t.Errorf("--json rewrote goals/README.md:\n%s", string(readmeBytes))
	}
	if _, err := os.Stat(filepath.Join(goalDir, "claim.json")); err != nil {
		t.Errorf("--json cleared a closed claim without --sync: %v", err)
	}

	// --sync explicitly opts a machine read into reconciliation.
	stdout, stderr, code = runGoalsClosureCLI(t, dir, "goals", "--project="+dir, "--json", "--sync")
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d, stderr: %s, stdout: %s", code, stderr, stdout)
	}
	payload = nil
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("expected valid envelope output, got %v: %s", err, stdout)
	}
	if payload["schema"] != "sila/json/v1" || payload["ok"] != true {
		t.Fatalf("expected a success envelope, got: %s", stdout)
	}
	mutations, _ := payload["mutations"].([]any)
	foundReadme := false
	for _, m := range mutations {
		if m == "goals/README.md" {
			foundReadme = true
		}
	}
	if !foundReadme {
		t.Fatalf("mutations does not report goals/README.md: %s", stdout)
	}

	readmeBytes, err = os.ReadFile(filepath.Join(dir, "goals", "README.md"))
	if err != nil {
		t.Fatalf("read goals/README.md: %v", err)
	}
	readme := string(readmeBytes)
	if !strings.Contains(readme, "# Goals Status & Pragmatic Execution Roadmap") {
		t.Errorf("expected --json --sync to sync goals/README.md, got:\n%s", readme)
	}
	if !strings.Contains(readme, slug) {
		t.Errorf("expected synced README to include %q, got:\n%s", slug, readme)
	}

	if _, err := os.Stat(filepath.Join(goalDir, "claim.json")); !os.IsNotExist(err) {
		t.Errorf("expected --json --sync to clear closed claim.json, stat err: %v", err)
	}
}

func TestCertifyMarkdownEvidenceReleasesVerifiedClaim(t *testing.T) {
	dir := initClosureGitRepo(t)
	slug := "markdown-verified-claim"
	writeClosureFixtureGoal(t, dir, slug)

	goalDir := filepath.Join(dir, "goals", slug)
	claim := `{"goal_slug":"` + slug + `","agent":"tester","claimed_at":"2026-01-01T00:00:00Z","status":"in_progress"}`
	if err := os.WriteFile(filepath.Join(goalDir, "claim.json"), []byte(claim), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goalDir, ".claim.lock"), []byte("lock"), 0644); err != nil {
		t.Fatal(err)
	}

	evidenceRel := filepath.Join("goals", slug, "evidence.md")
	if err := os.WriteFile(filepath.Join(dir, evidenceRel), []byte("# Review\n\nVerdict: PASS\n"), 0644); err != nil {
		t.Fatal(err)
	}

	commitClosureFixture(t, dir, "feat: implement verified claim (goals/"+slug+"/goal.md)")

	stdout, stderr, code := runGoalsClosureCLI(t, dir,
		"goals", "--project="+dir, "certify", slug,
		"--evidence="+evidenceRel,
		"--agent=auditor-bot",
		"--json",
	)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d, stderr: %s, stdout: %s", code, stderr, stdout)
	}
	if _, err := os.Stat(filepath.Join(goalDir, "claim.json")); !os.IsNotExist(err) {
		t.Errorf("expected verified claim.json to be released, stat err: %v", err)
	}
	if _, err := os.Stat(filepath.Join(goalDir, ".claim.lock")); !os.IsNotExist(err) {
		t.Errorf("expected .claim.lock to be released, stat err: %v", err)
	}
}

func TestCertifyMarkdownEvidenceBindsCommitSHA(t *testing.T) {
	dir := initClosureGitRepo(t)
	slug := "markdown-commit-binding"
	writeClosureFixtureGoal(t, dir, slug)

	evidenceRel := filepath.Join("goals", slug, "evidence.md")
	evidence := `# Review Report: Closure Binding

Verdict: ACCEPTED WITH FINDINGS

## Current acceptance
| case_id | status |
|---------|--------|
| AC-001  | PASS   |
`
	if err := os.WriteFile(filepath.Join(dir, evidenceRel), []byte(evidence), 0644); err != nil {
		t.Fatal(err)
	}

	head := commitClosureFixture(t, dir, "chore: fixture for markdown certification")

	stdout, stderr, code := runGoalsClosureCLI(t, dir,
		"goals", "--project="+dir, "certify", slug,
		"--evidence="+evidenceRel,
		"--agent=auditor-bot",
		"--json",
	)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d, stderr: %s, stdout: %s", code, stderr, stdout)
	}

	var resp map[string]any
	if err := json.Unmarshal([]byte(stdout), &resp); err != nil {
		t.Fatalf("parse certify JSON: %v, raw: %s", err, stdout)
	}
	cert, ok := resp["certification"].(map[string]any)
	if !ok {
		t.Fatalf("expected certification object, got: %v", resp)
	}
	if cert["commit_sha"] != head {
		t.Errorf("expected certification commit_sha=%s, got %v", head, cert["commit_sha"])
	}

	diskBytes, err := os.ReadFile(filepath.Join(dir, "goals", slug, "certification.json"))
	if err != nil {
		t.Fatalf("read certification.json: %v", err)
	}
	var disk map[string]any
	if err := json.Unmarshal(diskBytes, &disk); err != nil {
		t.Fatalf("parse certification.json: %v", err)
	}
	if disk["commit_sha"] != head {
		t.Errorf("expected persisted commit_sha=%s, got %v", head, disk["commit_sha"])
	}
}

func TestGoalsCheckJSONAndUpdateDependsOn(t *testing.T) {
	dir := initClosureGitRepo(t)
	writeClosureFixtureGoal(t, dir, "prereq-goal")
	writeClosureFixtureGoal(t, dir, "target-goal")
	commitClosureFixture(t, dir, "chore: initial two-goal fixture")

	// 1. Out-of-sync goals/README.md with check --json must exit 1 and report in_sync=false.
	stdout, stderr, code := runGoalsClosureCLI(t, dir, "goals", "--project="+dir, "check", "--json")
	if code != 1 {
		t.Fatalf("expected exit code 1 for out-of-sync README, got %d (stderr=%s, stdout=%s)", code, stderr, stdout)
	}
	var checkOut map[string]any
	if err := json.Unmarshal([]byte(stdout), &checkOut); err != nil {
		t.Fatalf("unmarshal check --json output: %v (raw=%s)", err, stdout)
	}
	if checkOut["in_sync"] != false {
		t.Fatalf("expected in_sync=false, got %v", checkOut)
	}

	// 2. Update target-goal via --depends-on=prereq-goal and verify dependency persists.
	stdout, stderr, code = runGoalsClosureCLI(t, dir, "goals", "--project="+dir, "update", "target-goal", "--depends-on=prereq-goal", "--json")
	if code != 0 {
		t.Fatalf("goals update --depends-on failed: code=%d stderr=%s stdout=%s", code, stderr, stdout)
	}
	var updated map[string]any
	if err := json.Unmarshal([]byte(stdout), &updated); err != nil {
		t.Fatalf("unmarshal update --json: %v (raw=%s)", err, stdout)
	}
	goalObj, _ := updated["goal"].(map[string]any)
	deps, _ := goalObj["depends_on"].([]any)
	if len(deps) != 1 || deps[0] != "prereq-goal" {
		t.Fatalf("expected depends_on=[prereq-goal], got %v", goalObj["depends_on"])
	}

	// 3. After update synced goals/README.md, check --json must exit 0 with in_sync=true.
	stdout, stderr, code = runGoalsClosureCLI(t, dir, "goals", "--project="+dir, "check", "--json")
	if code != 0 {
		t.Fatalf("expected exit code 0 after sync, got %d (stderr=%s, stdout=%s)", code, stderr, stdout)
	}
	checkOut = nil
	if err := json.Unmarshal([]byte(stdout), &checkOut); err != nil {
		t.Fatalf("unmarshal passing check --json: %v (raw=%s)", err, stdout)
	}
	if checkOut["in_sync"] != true {
		t.Fatalf("expected in_sync=true, got %v", checkOut)
	}
}
