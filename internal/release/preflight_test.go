package release

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/goals"
)

func TestCalculateGrade(t *testing.T) {
	tests := []struct {
		score float64
		grade string
	}{
		{100.0, "A+"},
		{95.0, "A+"},
		{94.9, "A"},
		{90.0, "A"},
		{89.9, "B"},
		{80.0, "B"},
		{79.9, "C"},
		{70.0, "C"},
		{69.9, "D"},
		{60.0, "D"},
		{59.9, "F"},
		{0.0, "F"},
	}

	for _, tc := range tests {
		if got := calculateGrade(tc.score); got != tc.grade {
			t.Errorf("calculateGrade(%v) = %v; want %v", tc.score, got, tc.grade)
		}
	}
}

func TestModeLabel(t *testing.T) {
	if got := modeLabel(true, false); got != "Forced Offline (--offline)" {
		t.Errorf("expected forced offline label, got %q", got)
	}
	if got := modeLabel(false, true); got != "Offline Resilient (Auto-Detected)" {
		t.Errorf("expected auto-detected offline label, got %q", got)
	}
	if got := modeLabel(false, false); got != "Connected Online" {
		t.Errorf("expected connected online label, got %q", got)
	}
}

func TestRunPreflight_OfflineAndAllowDirty(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	// If in internal/release, walk up to cli root
	repoDir := cwd
	if filepath.Base(repoDir) == "release" {
		repoDir = filepath.Dir(filepath.Dir(repoDir))
	}

	opts := PreflightOptions{
		AllowDirty: true,
		Offline:    true,
		Strict:     false,
		RepoDir:    repoDir,
	}

	report, err := RunPreflight(opts)
	if err != nil {
		t.Fatalf("RunPreflight failed: %v", err)
	}

	if !report.OfflineMode {
		t.Errorf("expected OfflineMode to be true")
	}

	if len(report.Checks) == 0 {
		t.Fatalf("expected checks to be evaluated, got 0")
	}

	// Verify that remote probes were skipped in offline mode
	skippedCount := 0
	for _, c := range report.Checks {
		if c.Status == StatusSkipped {
			skippedCount++
		}
	}

	if skippedCount < 2 {
		t.Errorf("expected at least 2 skipped remote checks in offline mode, got %d", skippedCount)
	}

	// Grade should be passing
	if report.Grade != "A+" && report.Grade != "A" && report.Grade != "B" {
		t.Errorf("expected passing grade, got %s (score: %.1f%%)", report.Grade, report.Score)
	}
}

// newGoalPolicyRepo builds a one-goal git repository whose goal.md was added on
// commitDate, so the cutoff policy can be exercised without touching the real
// repository history.
func newGoalPolicyRepo(t *testing.T, slug, commitDate string) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		filepath.Join("goals", slug, "goal.md"):       "# Goal: Policy\n\n- **Files To Touch**: internal/app/policy.go\n",
		filepath.Join("goals", slug, "plan.md"):       "# Plan\n\n- [x] done\n",
		filepath.Join("goals", slug, "facts.md"):      "# Facts\n",
		filepath.Join("internal", "app", "policy.go"): "package app\n\nfunc Policy() {}\n",
	}
	for rel, body := range files {
		p := filepath.Join(root, rel)
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
	commit := exec.Command("git", "-C", root, "commit", "-q", "-m",
		"feat: implement policy goal (goals/"+slug+"/goal.md)")
	commit.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.test",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.test",
		"GIT_AUTHOR_DATE="+commitDate, "GIT_COMMITTER_DATE="+commitDate)
	if out, err := commit.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}
	return root
}

func TestCheckGoalVerificationLevels(t *testing.T) {
	const slug = "policy-goal"
	cases := []struct {
		name       string
		commitDate string
		level      string
		record     bool
		want       CheckStatus
	}{
		{"pre-cutoff unbound is a warning", "2020-01-01T00:00:00Z", "", false, StatusWarn},
		{"post-cutoff unbound fails", "2027-01-01T00:00:00Z", "", false, StatusFail},
		{"post-cutoff L1 fails", "2027-01-01T00:00:00Z", goals.LevelL1, true, StatusFail},
		{"post-cutoff L2 passes", "2027-01-01T00:00:00Z", goals.LevelL2, true, StatusPass},
		{"post-cutoff L3 passes", "2027-01-01T00:00:00Z", goals.LevelL3, true, StatusPass},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := newGoalPolicyRepo(t, slug, tc.commitDate)
			if tc.record {
				if _, err := goals.RecordVerification(root, slug, tc.level, "verify"); err != nil {
					t.Fatalf("record verification: %v", err)
				}
			}
			res := checkGoalVerificationLevels(root)
			if res.Status != tc.want {
				t.Fatalf("status = %s, want %s (detail: %s)", res.Status, tc.want, res.Detail)
			}
			if tc.want == StatusFail && !strings.Contains(res.Detail, slug) {
				t.Errorf("failure detail should name the goal: %s", res.Detail)
			}
		})
	}
}

func TestCheckGoalVerificationLevelsCutoffOverride(t *testing.T) {
	const slug = "policy-goal"
	root := newGoalPolicyRepo(t, slug, "2027-01-01T00:00:00Z")
	if err := os.MkdirAll(filepath.Join(root, ".sila"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".sila", "verification-cutoff.json"),
		[]byte(`{"cutoff":"2030-01-01"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	res := checkGoalVerificationLevels(root)
	if res.Status != StatusWarn {
		t.Fatalf("status = %s, want WARN after moving the cutoff past the goal (detail: %s)", res.Status, res.Detail)
	}
}

func TestCheckGoalVerificationLevelsWithoutGoals(t *testing.T) {
	res := checkGoalVerificationLevels(t.TempDir())
	if res.Status != StatusPass {
		t.Fatalf("status = %s, want PASS for a repository without goals (detail: %s)", res.Status, res.Detail)
	}
}
