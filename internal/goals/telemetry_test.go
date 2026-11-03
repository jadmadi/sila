package goals

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func TestFormatProjectGoalsTelemetry(t *testing.T) {
	ui := render.New(os.Stdout, false)
	tmp := t.TempDir()

	// 1. Non-goals directory
	sumEmpty, lineEmpty := FormatProjectGoalsTelemetry(ui, tmp)
	if sumEmpty == nil || sumEmpty.HasGoals || lineEmpty != "" {
		t.Fatalf("expected HasGoals=false for empty directory, got %+v", sumEmpty)
	}

	// 2. Scaffold goal
	_, err := CreateGoal(CreateGoalParams{
		RootDir: tmp,
		Slug:    "feature-alpha",
		Title:   "Feature Alpha",
	})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}

	// 3. Claim goal
	_, err = ClaimGoal(tmp, "feature-alpha", "devin", "Working on schema", false)
	if err != nil {
		t.Fatalf("claim goal: %v", err)
	}

	// 4. Mark 2 tasks completed in plan.md
	_, err = ToggleGoalTask(tmp, "feature-alpha", 1, true)
	if err != nil {
		t.Fatalf("toggle task 1: %v", err)
	}
	_, err = ToggleGoalTask(tmp, "feature-alpha", 2, true)
	if err != nil {
		t.Fatalf("toggle task 2: %v", err)
	}

	sum, line := FormatProjectGoalsTelemetry(ui, tmp)
	if sum == nil || !sum.HasGoals {
		t.Fatalf("expected HasGoals=true, got %+v", sum)
	}
	if sum.TotalGoals != 1 || sum.Claimed != 1 || sum.TotalTasks != 4 || sum.CompletedTasks != 2 {
		t.Errorf("unexpected telemetry metrics: %+v", sum)
	}
	if sum.TaskPercent != 50 {
		t.Errorf("expected 50%% task progress, got %d%%", sum.TaskPercent)
	}
	if len(sum.ActiveWorkers) != 1 || !strings.Contains(sum.ActiveWorkers[0], "@devin on feature-alpha") || !strings.Contains(sum.ActiveWorkers[0], "Task #3") {
		t.Errorf("unexpected active workers (expected Task #3): %v", sum.ActiveWorkers)
	}
	if !strings.Contains(line, "2/4 tasks (50%)") || !strings.Contains(line, "@devin") || !strings.Contains(line, "Task #3") {
		t.Errorf("unexpected summary line: %q", line)
	}
}

func TestFormatGlobalGoalsTelemetry(t *testing.T) {
	ui := render.New(os.Stdout, false)
	dbPath := filepath.Join(t.TempDir(), "k.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	repoDir := filepath.Join(t.TempDir(), "repo-a")
	p, err := st.EnsureProject(repoDir)
	if err != nil {
		t.Fatal(err)
	}

	_, err = CreateGoal(CreateGoalParams{
		RootDir: p.Path,
		Slug:    "global-goal",
		Title:   "Global Goal Test",
	})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}

	sum, line := FormatGlobalGoalsTelemetry(ui, st)
	if sum == nil || !sum.HasGoals {
		t.Fatalf("expected global HasGoals=true, got %+v", sum)
	}
	if sum.TotalGoals != 1 || sum.TotalTasks != 4 {
		t.Errorf("unexpected global telemetry metrics: %+v", sum)
	}
	if !strings.Contains(line, "workspace tasks") || strings.TrimSpace(line) == "" {
		t.Errorf("unexpected global summary line: %q", line)
	}
}
