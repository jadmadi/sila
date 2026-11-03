package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/render"
)

func TestGoalsCLI_CharterDisplayAndFlags(t *testing.T) {
	tempDir := t.TempDir()
	goalsDir := filepath.Join(tempDir, "goals", "my-feature")
	if err := os.MkdirAll(goalsDir, 0755); err != nil {
		t.Fatal(err)
	}
	goalContent := "# Goal: my-feature\n\n## Tasks To Touch\n- `cmd/sila/main.go`\n"
	if err := os.WriteFile(filepath.Join(goalsDir, "goal.md"), []byte(goalContent), 0644); err != nil {
		t.Fatal(err)
	}

	ui := render.New(os.Stdout, true)

	// 1. Claim with custom charter flags
	args := []string{"claim", "my-feature", "--agent=@devin", "--allow=cmd/sila/main.go", "--duration=2h"}
	runGoals(ui, "", tempDir, "devin", "starting", "", "", false, false, "", "", "", "", "", 0, false, false, false, false, false, false, false, false, false, false, false, false, false, "", "", "", false, args)

	// 2. Verify charter was written
	charter, err := goals.LoadCharter(tempDir, "my-feature")
	if err != nil {
		t.Fatalf("failed to load charter: %v", err)
	}
	if charter.Agent != "devin" {
		t.Fatalf("expected agent devin, got %s", charter.Agent)
	}
	if len(charter.AllowPaths) != 1 || charter.AllowPaths[0] != "cmd/sila/main.go" {
		t.Fatalf("unexpected allow paths: %v", charter.AllowPaths)
	}

	// 3. Test TOON rendering
	toon := goals.RenderCharterTOON(charter)
	if !strings.Contains(toon, "slug: my-feature") || !strings.Contains(toon, "agent: devin") {
		t.Fatalf("unexpected TOON output:\n%s", toon)
	}

	// 4. Test Release cleans up charter
	if err := goals.ReleaseGoal(tempDir, "my-feature"); err != nil {
		t.Fatalf("release goal failed: %v", err)
	}
	if _, err := goals.LoadCharter(tempDir, "my-feature"); err == nil {
		t.Fatalf("expected charter to be removed after release")
	}
}

func TestGate_CharterSentryEnforcement(t *testing.T) {
	charter := &goals.GoalCharter{

		GoalSlug:   "gate-test-goal",
		Agent:      "sentry-bot",
		AllowPaths: []string{"cmd/sila/main.go"},
		DenyPaths:  []string{"research/*"},
		CreatedAt:  time.Now().UTC(),
	}

	// Clean staged file
	err := goals.ValidateStagedFilesAgainstCharter(charter, []string{"cmd/sila/main.go"})
	if err != nil {
		t.Fatalf("expected clean validation, got: %v", err)
	}

	// Denied file
	err = goals.ValidateStagedFilesAgainstCharter(charter, []string{"research/secret.md"})
	if err == nil {
		t.Fatalf("expected failure on denied path, got nil")
	}
	if !strings.Contains(err.Error(), "Gate rejected") || !strings.Contains(err.Error(), "denied path") {
		t.Fatalf("unexpected gate rejection message: %v", err)
	}

	// Outside allow paths
	err = goals.ValidateStagedFilesAgainstCharter(charter, []string{"worker/routes.ts"})
	if err == nil {
		t.Fatalf("expected failure on outside allow paths, got nil")
	}
	if !strings.Contains(err.Error(), "outside authorized allow_paths") {
		t.Fatalf("unexpected gate rejection message: %v", err)
	}
}

func TestGoalsCLI_CharterJSONOutput(t *testing.T) {
	tempDir := t.TempDir()
	charter := &goals.GoalCharter{
		GoalSlug:       "json-goal",
		Agent:          "antigravity",
		AllowPaths:     []string{"cmd/sila/gate.go"},
		DenyPaths:      []string{"research/*"},
		RequiredChecks: []string{"sila gate"},
		MaxDuration:    time.Hour,
		LockToken:      "tok-123",
		CreatedAt:      time.Now().UTC(),
	}
	if err := goals.SaveCharter(tempDir, charter); err != nil {
		t.Fatal(err)
	}

	loaded, err := goals.LoadCharter(tempDir, "json-goal")
	if err != nil {
		t.Fatal(err)
	}

	data, err := json.Marshal(loaded)
	if err != nil {
		t.Fatal(err)
	}
	var outMap map[string]any
	if err := json.Unmarshal(data, &outMap); err != nil {
		t.Fatal(err)
	}
	if outMap["goal_slug"] != "json-goal" || outMap["agent"] != "antigravity" {
		t.Fatalf("unexpected json output: %+v", outMap)
	}
}
