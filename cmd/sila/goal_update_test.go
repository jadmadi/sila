package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func TestGoalsUpdateAndDependCLI(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "goals_test.db")

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	p, err := st.EnsureProject(tmp)
	if err != nil {
		t.Fatal(err)
	}
	st.Close()

	ui := render.New(os.Stdout, true)

	// Create goal
	runGoals(ui, dbPath, tmp, "agent", "", "", "", false, false, "", "CLI Update Target", "Description", "", "internal/update/target.go", 1, true, false, false, false, false, false, false, false, true, false, false, false, false, "", "", "", false, []string{"create", "cli-target"})

	// Update with --add-dep
	runGoals(ui, dbPath, tmp, "agent", "", "", "", false, false, "", "", "", "", "", 0, false, false, false, false, false, false, false, false, true, false, false, false, false, "", "dep-1,dep-2", "", false, []string{"update", "cli-target"})

	// Depend add
	runGoals(ui, dbPath, tmp, "agent", "", "", "", false, false, "", "", "", "", "", 0, false, false, false, false, false, false, false, false, true, false, false, false, false, "", "", "", false, []string{"depend", "cli-target", "add", "dep-3"})

	// Depend view
	runGoals(ui, dbPath, tmp, "agent", "", "", "", false, false, "", "", "", "", "", 0, false, false, false, false, false, false, false, false, true, false, false, false, false, "", "", "", false, []string{"depend", "cli-target"})

	// Depend remove
	runGoals(ui, dbPath, tmp, "agent", "", "", "", false, false, "", "", "", "", "", 0, false, false, false, false, false, false, false, false, true, false, false, false, false, "", "", "", false, []string{"depend", "cli-target", "remove", "dep-2"})

	// Depend set
	runGoals(ui, dbPath, tmp, "agent", "", "", "", false, false, "", "", "", "", "", 0, false, false, false, false, false, false, false, false, true, false, false, false, false, "", "", "", false, []string{"depend", "cli-target", "set", "dep-only"})

	// Depend clear
	runGoals(ui, dbPath, tmp, "agent", "", "", "", false, false, "", "", "", "", "", 0, false, false, false, false, false, false, false, false, true, false, false, false, false, "", "", "", false, []string{"depend", "cli-target", "clear"})

	// Verify target is independent again
	audit, err := goals.ScanGoals(tmp)
	if err != nil {
		t.Fatal(err)
	}
	var found *goals.Goal
	for _, g := range audit.Goals {
		if g.Slug == "cli-target" {
			found = &g
			break
		}
	}
	if found == nil {
		t.Fatal("cli-target not found")
	}
	if !found.Independent || len(found.DependsOn) != 0 {
		t.Fatalf("expected independent goal with 0 deps after clear, got indep=%v, deps=%v", found.Independent, found.DependsOn)
	}
	_ = p
}
