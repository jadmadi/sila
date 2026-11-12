package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func TestGoalsRetrospectiveCLI(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "goals_retro_test.db")

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.EnsureProject(tmp)
	if err != nil {
		t.Fatal(err)
	}
	st.Close()

	ui := render.New(os.Stdout, true)

	// Create goal with --status=implemented and --commit=7a6b5c4 via CLI args
	runGoals(ui, dbPath, tmp, "agent", "", "", "", false, false, "", "CLI Retro Goal", "Retro Description", "", "internal/retro/goal.go", 1, true, false, false, false, false, false, false, false, true, false, false, false, false, "", "", "", false,
		[]string{
			"create",
			"cli-retro-goal",
			"--status=implemented",
			"--commit=7a6b5c4",
		})

	goalDir := filepath.Join(tmp, "goals", "cli-retro-goal")

	goalBytes, err := os.ReadFile(filepath.Join(goalDir, "goal.md"))
	if err != nil {
		t.Fatalf("goal.md not found: %v", err)
	}
	if !strings.Contains(string(goalBytes), "- **Status**: implemented") {
		t.Errorf("goal.md missing status: %s", string(goalBytes))
	}
	if !strings.Contains(string(goalBytes), "- **Commit**: 7a6b5c4") {
		t.Errorf("goal.md missing commit: %s", string(goalBytes))
	}

	audit, err := goals.ScanGoals(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(audit.Goals) != 1 {
		t.Fatalf("expected 1 goal, got %d", len(audit.Goals))
	}
	if audit.Goals[0].Status != "implemented" || audit.Goals[0].Commit != "7a6b5c4" {
		t.Errorf("expected goal status 'implemented' with commit '7a6b5c4', got %+v", audit.Goals[0])
	}
}
