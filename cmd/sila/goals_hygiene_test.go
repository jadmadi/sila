package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func TestGoalsReleaseStaleCLIDryRun(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "hygiene_cli.db")

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.EnsureProject(tmp); err != nil {
		st.Close()
		t.Fatal(err)
	}
	st.Close()

	goalDir := filepath.Join(tmp, "goals", "stale-cli-goal")
	if err := os.MkdirAll(goalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	host, _ := os.Hostname()
	claim := goals.GoalClaim{
		GoalSlug:  "stale-cli-goal",
		ClaimedBy: "opencode",
		Agent:     "opencode",
		ClaimedAt: time.Now().UTC().Add(-5 * 24 * time.Hour),
		UpdatedAt: time.Now().UTC().Add(-5 * 24 * time.Hour),
		Hostname:  host,
		PID:       99999999,
		Status:    "in_progress",
	}
	data, _ := json.MarshalIndent(claim, "", "  ")
	if err := os.WriteFile(filepath.Join(goalDir, "claim.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	ui := render.New(os.Stdout, true)
	out := captureStdout(t, func() {
		runGoals(ui, dbPath, tmp, "agent", "", "", "", false, false, "", "", "", "", "", 0,
			false, false, false, false, false, false, false, false, true, false, false, false, true,
			"", "", "", false, []string{"release", "--stale"},
			goalsRunOptions{stale: true, olderThan: "72h", dryRun: true})
	})

	var res struct {
		DryRun   bool                 `json:"dry_run"`
		Count    int                  `json:"count"`
		Released []goals.StaleRelease `json:"released"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("release --stale output is not JSON: %q: %v", out, err)
	}
	if !res.DryRun || res.Count != 1 || len(res.Released) != 1 {
		t.Fatalf("payload = %+v, want one dry-run release", res)
	}
	if res.Released[0].Goal != "stale-cli-goal" || res.Released[0].Reason == "" {
		t.Fatalf("release = %+v, want goal and reason", res.Released[0])
	}
	if _, err := os.Stat(filepath.Join(goalDir, "claim.json")); err != nil {
		t.Fatal("dry run removed the claim file")
	}
}
