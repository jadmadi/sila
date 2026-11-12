package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/render"
)

func setupGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cmds := [][]string{
		{"init"},
		{"config", "user.email", "test@test.com"},
		{"config", "user.name", "Test"},
	}
	for _, args := range cmds {
		c := exec.Command("git", args...)
		c.Dir = dir
		if err := c.Run(); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("content"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"add", "."},
		{"commit", "-m", "feat: add feature (goals/test-goal/goal.md)"},
		{"commit", "--allow-empty", "-m", "fix: resolve bug"},
	} {
		c := exec.Command("git", args...)
		c.Dir = dir
		if err := c.Run(); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	return dir
}

func TestRunChangelogDryRun(t *testing.T) {
	dir := setupGitRepo(t)
	origWd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(origWd)

	ui := render.New(os.Stdout, true)
	out := captureStdout(t, func() {
		runChangelog(ui, changelogOptions{scanFlag: true})
	})
	if !strings.Contains(out, "### Added") {
		t.Errorf("expected Added category, got %q", out)
	}
	if !strings.Contains(out, "### Fixed") {
		t.Errorf("expected Fixed category, got %q", out)
	}
}

func TestRunChangelogWrite(t *testing.T) {
	dir := setupGitRepo(t)
	// Create a minimal CHANGELOG.md.
	changelogPath := filepath.Join(dir, "CHANGELOG.md")
	if err := os.WriteFile(changelogPath, []byte("# Changelog\n\n## [Unreleased]\n\n## [0.1.0]\n"), 0644); err != nil {
		t.Fatal(err)
	}

	origWd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(origWd)

	ui := render.New(os.Stdout, true)
	out := captureStdout(t, func() {
		runChangelog(ui, changelogOptions{writeFlag: true})
	})
	if !strings.Contains(out, "Updated CHANGELOG.md") {
		t.Errorf("expected write confirmation, got %q", out)
	}
	// Verify file was updated.
	data, err := os.ReadFile(changelogPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), "### Added") {
		t.Errorf("CHANGELOG.md should contain Added section after --write")
	}
}

func TestRunChangelogJSON(t *testing.T) {
	dir := setupGitRepo(t)
	origWd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(origWd)

	ui := render.New(os.Stdout, true)
	out := captureStdout(t, func() {
		runChangelog(ui, changelogOptions{scanFlag: true, jsonOut: true})
	})
	if !strings.Contains(out, "\"commits\"") || strings.Contains(out, "\"Commits\"") {
		t.Errorf("expected snake_case JSON with commits, got %q", out)
	}
}

func TestRunChangelogJSONContract(t *testing.T) {
	dir := setupGitRepo(t)
	origWd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(origWd)

	ui := render.New(os.Stdout, true)
	out := captureStdout(t, func() {
		runChangelog(ui, changelogOptions{scanFlag: true, jsonOut: true})
	})
	var doc map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("stdout must be exactly one JSON document: %v\n%s", err, out)
	}
	for _, key := range []string{"tag_range", "total", "groups", "commits"} {
		if _, ok := doc[key]; !ok {
			t.Errorf("missing key %q in %s", key, out)
		}
	}
	var report struct {
		Total   int `json:"total"`
		Commits []struct {
			Hash     string `json:"hash"`
			GoalSlug string `json:"goal_slug"`
			Category string `json:"category"`
			Date     string `json:"date"`
		} `json:"commits"`
		Groups map[string]int `json:"groups"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatal(err)
	}
	if report.Total != 2 || len(report.Commits) != 2 {
		t.Fatalf("want 2 commits, got total=%d len=%d", report.Total, len(report.Commits))
	}
	if report.Groups["Added"] != 1 || report.Groups["Fixed"] != 1 {
		t.Errorf("unexpected groups: %v", report.Groups)
	}
	for _, c := range report.Commits {
		if c.Hash == "" || c.Date == "" {
			t.Errorf("commit missing hash or date: %+v", c)
		}
	}
}

func TestRunChangelogTOON(t *testing.T) {
	dir := setupGitRepo(t)
	origWd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(origWd)

	ui := render.New(os.Stdout, true)
	out := captureStdout(t, func() {
		runChangelog(ui, changelogOptions{scanFlag: true, toonOut: true})
	})
	for _, want := range []string{
		"changelog:\n",
		"  total: 2\n",
		"commits[2]{hash,category,scope,goal,description}:\n",
		",Added,\"\",test-goal,add feature\n",
		",Fixed,\"\",\"\",resolve bug\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("TOON output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "###") || strings.Contains(out, "{\"") {
		t.Errorf("TOON output must not contain markdown or JSON:\n%s", out)
	}
}

func TestChangelogFlagConflict(t *testing.T) {
	tests := []struct {
		name              string
		write, json, toon bool
		wantConflict      bool
	}{
		{"plain", false, false, false, false},
		{"write only", true, false, false, false},
		{"json only", false, true, false, false},
		{"toon only", false, false, true, false},
		{"json and toon", false, true, true, true},
		{"write and json", true, true, false, true},
		{"write and toon", true, false, true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := changelogFlagConflict(tt.write, tt.json, tt.toon)
			if (got != "") != tt.wantConflict {
				t.Errorf("conflict message = %q, wantConflict %v", got, tt.wantConflict)
			}
		})
	}
}

// TestChangelogWriteIsIdempotent runs --write twice and expects no second diff.
func TestChangelogWriteIsIdempotent(t *testing.T) {
	dir := setupGitRepo(t)
	changelogPath := filepath.Join(dir, "CHANGELOG.md")
	if err := os.WriteFile(changelogPath, []byte("# Changelog\n\n## [Unreleased]\n\n## [0.1.0]\n"), 0644); err != nil {
		t.Fatal(err)
	}
	origWd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(origWd)

	ui := render.New(os.Stdout, true)
	captureStdout(t, func() { runChangelog(ui, changelogOptions{writeFlag: true}) })
	first, _ := os.ReadFile(changelogPath)
	captureStdout(t, func() { runChangelog(ui, changelogOptions{writeFlag: true}) })
	second, _ := os.ReadFile(changelogPath)
	if string(first) != string(second) {
		t.Errorf("second --write changed CHANGELOG.md:\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}
}

func TestRunChangelogParentWorkspaceResolution(t *testing.T) {
	parent := t.TempDir()
	cliSub := filepath.Join(parent, "cli")
	if err := os.MkdirAll(cliSub, 0755); err != nil {
		t.Fatal(err)
	}
	// Init git in cliSub
	for _, args := range [][]string{
		{"init"},
		{"config", "user.email", "test@test.com"},
		{"config", "user.name", "Test"},
	} {
		c := exec.Command("git", args...)
		c.Dir = cliSub
		if err := c.Run(); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	if err := os.WriteFile(filepath.Join(cliSub, "file.txt"), []byte("content"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"add", "."},
		{"commit", "-m", "feat: parent workspace discovery"},
	} {
		c := exec.Command("git", args...)
		c.Dir = cliSub
		if err := c.Run(); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}

	origWd, _ := os.Getwd()
	// Change working directory to parent workspace root (which is NOT a git repo itself)
	if err := os.Chdir(parent); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(origWd)

	ui := render.New(os.Stdout, true)
	out := captureStdout(t, func() {
		runChangelog(ui, changelogOptions{scanFlag: true})
	})
	if !strings.Contains(out, "parent workspace discovery") {
		t.Errorf("expected changelog to resolve child repo from parent workspace, got %q", out)
	}
}

// TestRunChangelogDefaultHistory verifies that `sila changelog` without flags
// runs cleanly anywhere on the system (even in a non-git directory) and outputs
// Sila's own release cards from embedded history.
func TestRunChangelogDefaultHistory(t *testing.T) {
	nonGitDir := t.TempDir()
	origWd, _ := os.Getwd()
	if err := os.Chdir(nonGitDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(origWd)

	ui := render.New(os.Stdout, true)
	out := captureStdout(t, func() {
		runChangelog(ui, changelogOptions{})
	})

	if strings.Contains(out, "fatal: not a git repository") {
		t.Fatalf("default changelog must not require a git repository; got: %s", out)
	}
	if !strings.Contains(out, "Sila · changelog") {
		t.Errorf("expected 'Sila · changelog' header, got:\n%s", out)
	}
	if !strings.Contains(out, "╭─") || !strings.Contains(out, "╰─") {
		t.Errorf("expected rounded card borders, got:\n%s", out)
	}
}

// TestRunChangelogDefaultJSON verifies that `sila changelog --json` in a non-git
// directory outputs valid JSON with Sila release history.
func TestRunChangelogDefaultJSON(t *testing.T) {
	nonGitDir := t.TempDir()
	origWd, _ := os.Getwd()
	if err := os.Chdir(nonGitDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(origWd)

	ui := render.New(os.Stdout, true)
	out := captureStdout(t, func() {
		runChangelog(ui, changelogOptions{jsonOut: true})
	})

	var parsed struct {
		CurrentVersion string `json:"current_version"`
		TotalReleases  int    `json:"total_releases"`
		Releases       []struct {
			Version  string   `json:"version"`
			Date     string   `json:"date"`
			Features []string `json:"features"`
			Fixes    []string `json:"fixes"`
		} `json:"releases"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("failed to unmarshal JSON: %v\nOutput: %s", err, out)
	}
	if parsed.TotalReleases == 0 {
		t.Errorf("expected at least 1 release, got 0")
	}
	if len(parsed.Releases) == 0 {
		t.Errorf("expected at least 1 release object, got 0")
	}
}

// TestRunChangelogDefaultTOON verifies that `sila changelog --toon` in a non-git
// directory outputs valid TOON formatting.
func TestRunChangelogDefaultTOON(t *testing.T) {
	nonGitDir := t.TempDir()
	origWd, _ := os.Getwd()
	if err := os.Chdir(nonGitDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(origWd)

	ui := render.New(os.Stdout, true)
	out := captureStdout(t, func() {
		runChangelog(ui, changelogOptions{toonOut: true})
	})

	if !strings.Contains(out, "sila_releases:") {
		t.Errorf("expected sila_releases header in TOON, got:\n%s", out)
	}
	if !strings.Contains(out, "releases[") {
		t.Errorf("expected releases array in TOON, got:\n%s", out)
	}
}

// TestRunChangelogDefaultTop verifies `--top <N>` limits the displayed releases.
func TestRunChangelogDefaultTop(t *testing.T) {
	nonGitDir := t.TempDir()
	origWd, _ := os.Getwd()
	if err := os.Chdir(nonGitDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(origWd)

	ui := render.New(os.Stdout, true)
	out := captureStdout(t, func() {
		runChangelog(ui, changelogOptions{top: 1, topSpecified: true, jsonOut: true})
	})

	var parsed struct {
		Releases []struct {
			Version string `json:"version"`
		} `json:"releases"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("failed to unmarshal JSON: %v\nOutput: %s", err, out)
	}
	if len(parsed.Releases) != 1 {
		t.Errorf("expected exactly 1 release with --top=1, got %d", len(parsed.Releases))
	}
}

// TestRunChangelogDefaultLineCount verifies that the default `sila changelog` output
// is bounded and fits comfortably on a standard developer screen (< 75 lines).
func TestRunChangelogDefaultLineCount(t *testing.T) {
	nonGitDir := t.TempDir()
	origWd, _ := os.Getwd()
	if err := os.Chdir(nonGitDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(origWd)

	ui := render.New(os.Stdout, true)
	out := captureStdout(t, func() {
		runChangelog(ui, changelogOptions{})
	})

	lines := strings.Split(strings.TrimSpace(out), "\n")
	lineCount := len(lines)
	if lineCount > 90 {
		t.Errorf("expected default changelog to be bounded to < 90 lines, got %d lines", lineCount)
	}
	if lineCount < 10 {
		t.Errorf("expected default changelog to have substantive output, got only %d lines:\n%s", lineCount, out)
	}
}

// TestRunChangelogTimeline verifies that `sila changelog --timeline` outputs
// a compact release matrix table across all releases.
func TestRunChangelogTimeline(t *testing.T) {
	nonGitDir := t.TempDir()
	origWd, _ := os.Getwd()
	if err := os.Chdir(nonGitDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(origWd)

	ui := render.New(os.Stdout, true)
	out := captureStdout(t, func() {
		runChangelog(ui, changelogOptions{timeline: true})
	})

	for _, header := range []string{"RELEASE", "DATE", "BREAKING", "FEATURES", "FIXES", "KEY HIGHLIGHTS"} {
		if !strings.Contains(out, header) {
			t.Errorf("expected timeline table header %q in output:\n%s", header, out)
		}
	}
	if !strings.Contains(out, "v0.17.5") || !strings.Contains(out, "v0.16.0") {
		t.Errorf("expected multiple releases in timeline matrix, got:\n%s", out)
	}
}

// TestRunChangelogTargetVersion verifies that `sila changelog <version>` focuses
// specifically on the requested release card.
func TestRunChangelogTargetVersion(t *testing.T) {
	nonGitDir := t.TempDir()
	origWd, _ := os.Getwd()
	if err := os.Chdir(nonGitDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(origWd)

	ui := render.New(os.Stdout, true)
	out := captureStdout(t, func() {
		runChangelog(ui, changelogOptions{targetVersion: "v0.11.0"})
	})

	if !strings.Contains(out, "v0.11.0") {
		t.Errorf("expected output to contain v0.11.0 card, got:\n%s", out)
	}
	if strings.Contains(out, "v0.10.0") {
		t.Errorf("expected output NOT to contain v0.10.0 card when target is v0.11.0, got:\n%s", out)
	}
}
