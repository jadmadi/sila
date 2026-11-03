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

	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func TestGoalsGlobalStatsCLI(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "sila.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}

	p1 := filepath.Join(tmp, "repo-one")
	p2 := filepath.Join(tmp, "repo-two")
	os.MkdirAll(p1, 0755)
	os.MkdirAll(p2, 0755)

	if _, err := st.EnsureProject(p1); err != nil {
		t.Fatal(err)
	}
	if _, err := st.EnsureProject(p2); err != nil {
		t.Fatal(err)
	}
	st.Close()

	// Create goals in repo-one
	goals.CreateGoal(goals.CreateGoalParams{
		RootDir: p1,
		Slug:    "goal-a",
		Title:   "Goal Alpha",
	})
	goals.CreateGoal(goals.CreateGoalParams{
		RootDir: p1,
		Slug:    "goal-b",
		Title:   "Goal Beta",
	})

	// Create goal in repo-two
	goals.CreateGoal(goals.CreateGoalParams{
		RootDir: p2,
		Slug:    "goal-c",
		Title:   "Goal Gamma",
	})

	// Test 1: sila goals global stats (human table)
	ui := render.New(os.Stdout, true)
	output := captureStdout(t, func() {
		runGoals(ui, dbPath, "", "", "", "", "", false, false, "", "", "", "", "", 0, false, false, false, false, false, false, false, false, false, false, false, false, false, "", "", "", false, []string{"global", "stats"})
	})

	for _, expected := range []string{"Global Multi-Project Statistics", "repo-one", "repo-two", "ready", "wip", "done", "blocked", "total", "TOTAL (2)"} {
		if !strings.Contains(output, expected) {
			t.Errorf("expected output to contain %q, got:\n%s", expected, output)
		}
	}

	// Test 2: sila goals global stats --json
	out2 := captureStdout(t, func() {
		runGoals(ui, dbPath, "", "", "", "", "", false, false, "", "", "", "", "", 0, false, false, false, false, false, false, false, false, true, false, false, false, false, "", "", "", false, []string{"global", "stats"})
	})

	var payload goals.GlobalGoalsStatsPayload
	if err := json.Unmarshal([]byte(out2), &payload); err != nil {
		t.Fatalf("failed to parse JSON payload: %v\nOutput: %s", err, out2)
	}
	if payload.TotalProjects != 2 {
		t.Errorf("expected 2 projects, got %d", payload.TotalProjects)
	}
	if payload.TotalGoals != 3 {
		t.Errorf("expected 3 total goals, got %d", payload.TotalGoals)
	}
	if len(payload.Projects) != 2 {
		t.Errorf("expected 2 project rows, got %d", len(payload.Projects))
	}

	// Test 3: project-scoped sila goals stats
	singleOut := captureStdout(t, func() {
		runGoals(ui, dbPath, p1, "", "", "", "", false, false, "", "", "", "", "", 0, false, false, false, false, false, false, false, false, false, false, false, false, false, "", "", "", false, []string{"stats"})
	})

	if !strings.Contains(singleOut, "repo-one") {
		t.Errorf("expected project statistics header for repo-one, got:\n%s", singleOut)
	}
}

// statsCLIFixture builds a store with one project holding two ready goals.
func statsCLIFixture(t *testing.T) (dbPath, projectDir string) {
	t.Helper()
	tmp := t.TempDir()
	dbPath = filepath.Join(tmp, "sila.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	projectDir = filepath.Join(tmp, "repo-one")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := st.EnsureProject(projectDir); err != nil {
		t.Fatal(err)
	}
	st.Close()
	for _, slug := range []string{"goal-a", "goal-b"} {
		goals.CreateGoal(goals.CreateGoalParams{RootDir: projectDir, Slug: slug, Title: slug})
	}
	return dbPath, projectDir
}

func runStatsArgs(dbPath, project string, isGlobal bool, args []string, opts goalsRunOptions) {
	ui := render.New(os.Stdout, true)
	runGoals(ui, dbPath, project, "", "", "", "", false, false, "", "", "", "", "", 0, false, isGlobal, false, false, false, false, false, false, false, false, false, false, false, "", "", "", false, args, opts)
}

func TestGoalsStatsLineCLI(t *testing.T) {
	dbPath, p1 := statsCLIFixture(t)

	out := captureStdout(t, func() {
		runStatsArgs(dbPath, p1, false, []string{"stats"}, goalsRunOptions{line: true})
	})
	if out != "Goals: Ready 2 · WIP 0 · Blocked 0 · Done 0\n" {
		t.Errorf("unexpected --line output: %q", out)
	}
	if strings.Count(out, "\n") != 1 {
		t.Errorf("--line must print exactly one line, got %q", out)
	}

	global := captureStdout(t, func() {
		runStatsArgs(dbPath, "", true, []string{"stats"}, goalsRunOptions{line: true})
	})
	if !strings.HasPrefix(global, "Goals: Ready ") || strings.Count(global, "\n") != 1 {
		t.Errorf("unexpected --global --line output: %q", global)
	}
}

func TestGoalsStatsLineEmptyProject(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "sila.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(tmp, "empty")
	os.MkdirAll(empty, 0o755)
	if _, err := st.EnsureProject(empty); err != nil {
		t.Fatal(err)
	}
	st.Close()

	out := captureStdout(t, func() {
		runStatsArgs(dbPath, empty, false, []string{"stats"}, goalsRunOptions{line: true})
	})
	if out != "Goals: none\n" {
		t.Errorf("empty project output = %q, want %q", out, "Goals: none\n")
	}
}

func TestGoalsStatsDefaultViewIsCompact(t *testing.T) {
	dbPath, p1 := statsCLIFixture(t)
	out := captureStdout(t, func() {
		runStatsArgs(dbPath, p1, false, []string{"stats"}, goalsRunOptions{})
	})
	if n := strings.Count(out, "\n"); n > 3 {
		t.Errorf("default project stats should be at most 3 lines, got %d:\n%s", n, out)
	}
}

// TestGoalsStatsFlagConflicts re-runs the test binary so os.Exit(2) can be observed.
func TestGoalsStatsFlagConflicts(t *testing.T) {
	if mode := os.Getenv("SILA_TEST_STATS_CONFLICT"); mode != "" {
		dbPath, p1 := statsCLIFixture(t)
		opts := goalsRunOptions{}
		jsonOut := false
		switch mode {
		case "line-json":
			opts.line, jsonOut = true, true
		case "live-json":
			opts.live, jsonOut = true, true
		case "full-without-live":
			opts.full = true
		case "line-full":
			opts.line, opts.full = true, true
		}
		ui := render.New(os.Stdout, true)
		runGoals(ui, dbPath, p1, "", "", "", "", false, false, "", "", "", "", "", 0, false, false, false, false, false, false, false, false, jsonOut, false, false, false, false, "", "", "", false, []string{"stats"}, opts)
		return
	}
	for _, mode := range []string{"line-json", "live-json", "full-without-live", "line-full"} {
		t.Run(mode, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^TestGoalsStatsFlagConflicts$")
			cmd.Env = append(os.Environ(), "SILA_TEST_STATS_CONFLICT="+mode)
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			err := cmd.Run()
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) || exitErr.ExitCode() != 2 {
				t.Fatalf("want exit code 2, got err=%v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
			}
			if !strings.Contains(stderr.String(), "sila goals stats:") {
				t.Errorf("usage error should be on stderr, got %q", stderr.String())
			}
			if strings.Contains(stdout.String(), "Goals:") {
				t.Errorf("stdout must stay clean on a usage error, got %q", stdout.String())
			}
		})
	}
}
