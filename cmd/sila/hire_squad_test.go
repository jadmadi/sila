package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/audit"
	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func TestHireGoalSquadAssociationCLI(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origDir)

	dbPath := filepath.Join(tmpDir, "test_sila.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	st.Close()

	ui := render.New(os.Stdout, true)

	// Create a test goal package
	_, err = goals.CreateGoal(goals.CreateGoalParams{
		RootDir:     tmpDir,
		Slug:        "squad-test-goal",
		Title:       "Squad Test Goal",
		Description: "A goal for testing squad binding",
	})
	if err != nil {
		t.Fatalf("CreateGoal failed: %v", err)
	}

	// 1. Bind squad via `sila hire group 1 --goal=squad-test-goal`
	outBind := captureStdout(t, func() {
		runHire(ui, dbPath, false, false, auditOptions{}, []string{"group", "1", "--goal=squad-test-goal"})
	})
	if !strings.Contains(outBind, "Bound review squad (4 personas) to goal squad-test-goal") {
		t.Errorf("expected bound confirmation, got: %s", outBind)
	}

	// Verify goal.md contains personas
	goalMD, err := os.ReadFile(filepath.Join(tmpDir, "goals", "squad-test-goal", "goal.md"))
	if err != nil {
		t.Fatalf("read goal.md failed: %v", err)
	}
	personas := goals.ParseGoalPersonas(string(goalMD))
	if len(personas) != 4 {
		t.Fatalf("expected 4 personas bound in goal.md, got %v", personas)
	}

	// 2. Unbind squad via `sila hire unbind squad-test-goal`
	outUnbind := captureStdout(t, func() {
		runHire(ui, dbPath, false, false, auditOptions{}, []string{"unbind", "squad-test-goal"})
	})
	if !strings.Contains(outUnbind, "Unbound review squad from goal squad-test-goal") {
		t.Errorf("expected unbind confirmation, got: %s", outUnbind)
	}

	// Verify goal.md has 0 personas
	goalMD2, _ := os.ReadFile(filepath.Join(tmpDir, "goals", "squad-test-goal", "goal.md"))
	personas2 := goals.ParseGoalPersonas(string(goalMD2))
	if len(personas2) != 0 {
		t.Fatalf("expected 0 personas after unbind, got %v", personas2)
	}

	// 3. Bind via `sila hire 1,4 --goal=squad-test-goal`
	outBindNums := captureStdout(t, func() {
		runHire(ui, dbPath, false, false, auditOptions{}, []string{"1,4", "--goal=squad-test-goal"})
	})
	if !strings.Contains(outBindNums, "Bound review squad (2 personas) to goal squad-test-goal") {
		t.Errorf("expected bound confirmation for 1,4, got: %s", outBindNums)
	}

	// 4. Update via `sila goals update squad-test-goal --add-personas=8`
	outAdd := captureStdout(t, func() {
		runGoals(ui, dbPath, tmpDir, "agent", "", "", "", false, false, "", "", "", "", "", 0, false, false, false, false, false, false, false, false, false, false, false, false, false, "", "", "", false, []string{"update", "squad-test-goal", "--add-personas=qa-automation-lead"})
	})
	if !strings.Contains(outAdd, "Review Squad:") {
		t.Errorf("expected Review Squad in goals update output, got: %s", outAdd)
	}

	// 5. Update via `sila goals update squad-test-goal --clear-personas`
	outClear := captureStdout(t, func() {
		runGoals(ui, dbPath, tmpDir, "agent", "", "", "", false, false, "", "", "", "", "", 0, false, false, false, false, false, false, false, false, false, false, false, false, false, "", "", "", false, []string{"update", "squad-test-goal", "--clear-personas"})
	})
	if strings.Contains(outClear, "Review Squad:") {
		t.Errorf("expected Review Squad to be cleared, got: %s", outClear)
	}

	// 6. Test `sila hire reports --goal=squad-test-goal`
	// Seed a report for squad-test-goal and another for other-goal
	rep1 := &audit.Report{
		ID:             "rep_goal_match",
		AssociatedGoal: "squad-test-goal",
		PersonaID:      "qa-automation-lead",
		Status:         "pass",
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
	}
	rep2 := &audit.Report{
		ID:             "rep_other_goal",
		AssociatedGoal: "other-goal",
		PersonaID:      "performance-oracle",
		Status:         "warning",
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
	}
	_ = audit.SaveReport(tmpDir, rep1)
	_ = audit.SaveReport(tmpDir, rep2)

	outReportsFilter := captureStdout(t, func() {
		runReports(ui, dbPath, false, false, []string{"--goal=squad-test-goal"})
	})
	if !strings.Contains(outReportsFilter, "rep_goal_match") {
		t.Errorf("expected rep_goal_match in filtered reports, got: %s", outReportsFilter)
	}
	if strings.Contains(outReportsFilter, "rep_other_goal") {
		t.Errorf("did not expect rep_other_goal in filtered reports, got: %s", outReportsFilter)
	}

	// 7. Verify `sila goals view squad-test-goal` lists the linked report
	outView := captureStdout(t, func() {
		runGoals(ui, dbPath, tmpDir, "agent", "", "", "", false, false, "", "", "", "", "", 0, false, false, false, false, false, false, false, false, false, false, false, false, false, "", "", "", false, []string{"view", "squad-test-goal"})
	})
	if !strings.Contains(outView, "Linked Evaluation Reports") {
		t.Errorf("expected Linked Evaluation Reports in goal view, got: %s", outView)
	}
	if !strings.Contains(outView, "rep_goal_match") {
		t.Errorf("expected rep_goal_match in goal view, got: %s", outView)
	}
}

func TestHireGoalSquadJSONBinding(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origDir)

	dbPath := filepath.Join(tmpDir, "test_sila.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	st.Close()

	ui := render.New(os.Stdout, true)

	_, err = goals.CreateGoal(goals.CreateGoalParams{
		RootDir:     tmpDir,
		Slug:        "json-squad-goal",
		Title:       "JSON Squad Goal",
		Description: "A goal for testing JSON squad binding",
	})
	if err != nil {
		t.Fatalf("CreateGoal failed: %v", err)
	}

	// Test JSON bind
	outJSONBind := captureStdout(t, func() {
		runHire(ui, dbPath, true, false, auditOptions{}, []string{"1,2", "--goal=json-squad-goal"})
	})
	var bindRes map[string]any
	if err := json.Unmarshal([]byte(outJSONBind), &bindRes); err != nil {
		t.Fatalf("unmarshal bind response failed: %v", err)
	}
	if bindRes["bound"] != true || bindRes["goal"] != "json-squad-goal" {
		t.Fatalf("unexpected JSON bind response: %v", bindRes)
	}

	// Test JSON unbind
	outJSONUnbind := captureStdout(t, func() {
		runHire(ui, dbPath, true, false, auditOptions{}, []string{"unbind", "json-squad-goal"})
	})
	var unbindRes map[string]any
	if err := json.Unmarshal([]byte(outJSONUnbind), &unbindRes); err != nil {
		t.Fatalf("unmarshal unbind response failed: %v", err)
	}
	if unbindRes["unbound"] != true {
		t.Fatalf("unexpected JSON unbind response: %v", unbindRes)
	}
}
