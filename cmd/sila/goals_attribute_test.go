package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/render"
)

func TestExtractAttributeArgs(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		commitFlag string
		wantSlug   string
		wantCommit string
		wantErr    string
	}{
		{
			name:       "standard flag after slug",
			args:       []string{"attribute", "goal-one", "--commit=abc1234"},
			commitFlag: "",
			wantSlug:   "goal-one",
			wantCommit: "abc1234",
		},
		{
			name:       "short flag -c after slug",
			args:       []string{"attribute", "goal-two", "-c=def5678"},
			commitFlag: "",
			wantSlug:   "goal-two",
			wantCommit: "def5678",
		},
		{
			name:       "single hyphen -commit after slug",
			args:       []string{"attribute", "goal-three", "-commit=ghi9012"},
			commitFlag: "",
			wantSlug:   "goal-three",
			wantCommit: "ghi9012",
		},
		{
			name:       "space separated --commit flag",
			args:       []string{"attribute", "goal-four", "--commit", "jkl3456"},
			commitFlag: "",
			wantSlug:   "goal-four",
			wantCommit: "jkl3456",
		},
		{
			name:       "space separated -c flag",
			args:       []string{"attribute", "goal-five", "-c", "mno7890"},
			commitFlag: "",
			wantSlug:   "goal-five",
			wantCommit: "mno7890",
		},
		{
			name:       "outer flag forwarding from flagSet",
			args:       []string{"attribute", "goal-six"},
			commitFlag: "pqr1122",
			wantSlug:   "goal-six",
			wantCommit: "pqr1122",
		},
		{
			name:       "flag before slug syntax",
			args:       []string{"attribute", "--commit=stu3344", "goal-seven"},
			commitFlag: "",
			wantSlug:   "goal-seven",
			wantCommit: "stu3344",
		},
		{
			name:       "positional commit argument without flag",
			args:       []string{"attribute", "goal-eight", "vwx5566"},
			commitFlag: "",
			wantSlug:   "goal-eight",
			wantCommit: "vwx5566",
		},
		{
			name:       "missing commit hash returns error",
			args:       []string{"attribute", "goal-nine"},
			commitFlag: "",
			wantErr:    "--commit=<hash> is required",
		},
		{
			name:       "missing slug returns error",
			args:       []string{"attribute"},
			commitFlag: "hash9999",
			wantErr:    "goal slug is required",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			slug, commitOpt, err := extractAttributeArgs(tc.args, tc.commitFlag)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("expected error %q to contain %q", err.Error(), tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if slug != tc.wantSlug {
				t.Errorf("got slug %q, want %q", slug, tc.wantSlug)
			}
			if commitOpt != tc.wantCommit {
				t.Errorf("got commit %q, want %q", commitOpt, tc.wantCommit)
			}
		})
	}
}

func TestGoalsAttributeSubprocess(t *testing.T) {
	if os.Getenv("GO_WANT_GOALS_ATTRIBUTE_SUBPROCESS") != "1" {
		return
	}
	targetDir := os.Getenv("SUBPROCESS_TARGET_DIR")
	rawArgs := os.Getenv("SUBPROCESS_ARGS")
	var actionArgs []string
	if rawArgs != "" {
		actionArgs = strings.Split(rawArgs, "||")
	}
	commitFlag := os.Getenv("SUBPROCESS_COMMIT_FLAG")
	jsonOut := os.Getenv("SUBPROCESS_JSON") == "1"

	ui := render.New(os.Stdout, false)
	runGoals(
		ui, "", targetDir, "", "", "", "", false, false,
		commitFlag, "", "", "", "", 0, false, false, false, false,
		false, false, false, false, jsonOut, false, false,
		false, false, "", "", "", false, actionArgs,
	)
	os.Exit(0)
}

func runAttributeSubprocess(
	t *testing.T, targetDir string, commitFlag string, jsonOut bool, args ...string,
) (string, string, int) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestGoalsAttributeSubprocess$")
	cmd.Env = append(
		os.Environ(),
		"GO_WANT_GOALS_ATTRIBUTE_SUBPROCESS=1",
		"SUBPROCESS_TARGET_DIR="+targetDir,
		"SUBPROCESS_ARGS="+strings.Join(args, "||"),
		"SUBPROCESS_COMMIT_FLAG="+commitFlag,
	)
	if jsonOut {
		cmd.Env = append(cmd.Env, "SUBPROCESS_JSON=1")
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			t.Fatalf("runAttributeSubprocess failed: %v", err)
		}
	}
	return stdout.String(), stderr.String(), exitCode
}

func setupTestRepoWithCommit(t *testing.T, dir string) string {
	t.Helper()
	if err := exec.Command("git", "-C", dir, "init", "-b", "main").Run(); err != nil {
		t.Fatalf("git init: %v", err)
	}
	_ = exec.Command("git", "-C", dir, "config", "user.name", "Sila Tester").Run()
	_ = exec.Command("git", "-C", dir, "config", "user.email", "tester@sila.local").Run()

	readme := filepath.Join(dir, "README.md")
	if err := os.WriteFile(readme, []byte("# Test Repo\n"), 0644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	_ = exec.Command("git", "-C", dir, "add", "README.md").Run()
	if err := exec.Command("git", "-C", dir, "commit", "-m", "chore: initial commit").Run(); err != nil {
		t.Fatalf("git commit: %v", err)
	}

	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("git rev-parse: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func setupTestGoal(t *testing.T, dir, slug string) string {
	t.Helper()
	goalDir := filepath.Join(dir, "goals", slug)
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		t.Fatalf("mkdir goalDir: %v", err)
	}

	goalMD := "# Goal: " + slug + "\n\n- Tier: immediate\n- Shape: ship\n"
	planMD := "# Plan: " + slug + "\n\n- [ ] Step 1\n"
	factsMD := "# Facts: " + slug + "\n"

	if err := os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte(goalMD), 0644); err != nil {
		t.Fatalf("write goal.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(goalDir, "plan.md"), []byte(planMD), 0644); err != nil {
		t.Fatalf("write plan.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(goalDir, "facts.md"), []byte(factsMD), 0644); err != nil {
		t.Fatalf("write facts.md: %v", err)
	}
	return goalDir
}

func verifyRetrospectiveCommit(t *testing.T, dir, slug, wantPrefix string) {
	t.Helper()
	retroPath := filepath.Join(dir, "goals", slug, "retrospective.json")
	data, err := os.ReadFile(retroPath)
	if err != nil {
		t.Fatalf("read retrospective.json: %v", err)
	}
	var rec struct {
		Commit string `json:"commit"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(data, &rec); err != nil {
		t.Fatalf("unmarshal retrospective.json: %v", err)
	}
	if rec.Status != "implemented" {
		t.Errorf("got status %q, want implemented", rec.Status)
	}
	if !strings.HasPrefix(rec.Commit, wantPrefix[:min(len(wantPrefix), len(rec.Commit))]) {
		t.Errorf("got commit %q, want prefix %q", rec.Commit, wantPrefix)
	}
}

func TestGoalsAttributeCLI(t *testing.T) {
	tmp := t.TempDir()
	commitHash := setupTestRepoWithCommit(t, tmp)
	shortHash := commitHash[:7]

	t.Run("outer flag forwarding", func(t *testing.T) {
		slug := "attr-test-outer"
		setupTestGoal(t, tmp, slug)

		stdout, stderr, exitCode := runAttributeSubprocess(t, tmp, commitHash, false, "attribute", slug)
		if exitCode != 0 {
			t.Fatalf("expected exit 0, got %d. stderr: %s", exitCode, stderr)
		}
		if !strings.Contains(stdout, "successfully attributed") {
			t.Fatalf("expected success message in stdout, got: %s", stdout)
		}
		verifyRetrospectiveCommit(t, tmp, slug, shortHash)
	})

	t.Run("inline flag syntax", func(t *testing.T) {
		slug := "attr-test-inline"
		setupTestGoal(t, tmp, slug)

		stdout, stderr, exitCode := runAttributeSubprocess(t, tmp, "", false, "attribute", slug, "--commit="+commitHash)
		if exitCode != 0 {
			t.Fatalf("expected exit 0, got %d. stderr: %s", exitCode, stderr)
		}
		if !strings.Contains(stdout, "successfully attributed") {
			t.Fatalf("expected success message, got: %s", stdout)
		}
		verifyRetrospectiveCommit(t, tmp, slug, shortHash)
	})

	t.Run("short flag -c syntax", func(t *testing.T) {
		slug := "attr-test-short"
		setupTestGoal(t, tmp, slug)

		_, stderr, exitCode := runAttributeSubprocess(t, tmp, "", false, "attribute", slug, "-c="+commitHash)
		if exitCode != 0 {
			t.Fatalf("expected exit 0, got %d. stderr: %s", exitCode, stderr)
		}
		verifyRetrospectiveCommit(t, tmp, slug, shortHash)
	})

	t.Run("positional commit argument", func(t *testing.T) {
		slug := "attr-test-pos"
		setupTestGoal(t, tmp, slug)

		_, stderr, exitCode := runAttributeSubprocess(t, tmp, "", false, "attribute", slug, commitHash)
		if exitCode != 0 {
			t.Fatalf("expected exit 0, got %d. stderr: %s", exitCode, stderr)
		}
		verifyRetrospectiveCommit(t, tmp, slug, shortHash)
	})

	t.Run("flag before slug syntax", func(t *testing.T) {
		slug := "attr-test-flag-first"
		setupTestGoal(t, tmp, slug)

		_, stderr, exitCode := runAttributeSubprocess(t, tmp, "", false, "attribute", "--commit="+commitHash, slug)
		if exitCode != 0 {
			t.Fatalf("expected exit 0, got %d. stderr: %s", exitCode, stderr)
		}
		verifyRetrospectiveCommit(t, tmp, slug, shortHash)
	})

	t.Run("missing commit exits 2 with required error message", func(t *testing.T) {
		slug := "attr-test-missing"
		setupTestGoal(t, tmp, slug)

		_, stderr, exitCode := runAttributeSubprocess(t, tmp, "", false, "attribute", slug)
		if exitCode != 2 {
			t.Fatalf("expected exit 2 for missing commit, got %d", exitCode)
		}
		if !strings.Contains(stderr, "--commit=<hash> is required") {
			t.Fatalf("expected error message in stderr, got: %s", stderr)
		}
	})

	t.Run("json output formatting", func(t *testing.T) {
		slug := "attr-test-json"
		setupTestGoal(t, tmp, slug)

		stdout, stderr, exitCode := runAttributeSubprocess(t, tmp, commitHash, true, "attribute", slug)
		if exitCode != 0 {
			t.Fatalf("expected exit 0, got %d. stderr: %s", exitCode, stderr)
		}

		var res struct {
			Slug    string `json:"slug"`
			Commit  string `json:"commit"`
			Success bool   `json:"success"`
		}
		if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &res); err != nil {
			t.Fatalf("unmarshal json failed: %v. Output: %s", err, stdout)
		}
		if !res.Success || res.Commit != shortHash || res.Slug != slug {
			t.Fatalf("unexpected json payload: %+v", res)
		}
	})
}
