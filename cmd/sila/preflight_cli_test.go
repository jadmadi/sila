package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func TestGoalsPreFlightCLI(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "goals_preflight_test.db")

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

	// 1. Create base goal with touch
	runGoals(ui, dbPath, tmp, "agent", "", "", "", false, false, "", "CLI Base Goal", "Base Description", "", "internal/store/store.go", 1, true, false, false, false, false, false, false, false, true, false, false, false, false, "", "", "", false,
		[]string{
			"create",
			"cli-base-goal",
			"--touch=internal/store/store.go",
		})

	// 2. Create second goal in non-strict mode with file collision
	runGoals(ui, dbPath, tmp, "agent", "", "", "", false, false, "", "CLI Colliding Goal", "Colliding Description", "", "internal/store/store.go", 2, true, false, false, false, false, false, false, false, true, false, false, false, false, "", "", "", false,
		[]string{
			"create",
			"cli-colliding-goal",
			"--touch=internal/store/store.go",
		})

	audit, err := goals.ScanGoals(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(audit.Goals) != 2 {
		t.Fatalf("expected 2 goals created, got %d", len(audit.Goals))
	}
}
