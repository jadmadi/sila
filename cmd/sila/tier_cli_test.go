package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func TestGoalsTierCLI(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "goals_tier_test.db")

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

	// 1. Create immediate goal via CLI
	runGoals(ui, dbPath, tmp, "agent", "", "", "", false, false, "", "CLI Immediate Goal", "Immediate description", "", "internal/tier/immediate.go", 1, true, false, false, false, false, false, false, false, true, false, false, false, false, "", "", "", false, []string{"create", "imm-cli", "--tier=immediate"})

	// 2. Create roadmap goal (default)
	runGoals(ui, dbPath, tmp, "agent", "", "", "", false, false, "", "CLI Roadmap Goal", "Roadmap description", "", "internal/tier/roadmap.go", 2, true, false, false, false, false, false, false, false, true, false, false, false, false, "", "", "", false, []string{"create", "road-cli"})

	// 3. Create nice-to-have goal
	runGoals(ui, dbPath, tmp, "agent", "", "", "", false, false, "", "CLI Nice-to-Have Goal", "Nice description", "", "internal/tier/nice.go", 3, true, false, false, false, false, false, false, false, true, false, false, false, false, "", "", "", false, []string{"create", "nth-cli", "--tier=nice-to-have"})

	// Verify scanned goals on disk
	audit, err := goals.ScanGoals(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if audit.Total != 3 {
		t.Fatalf("expected 3 total goals, got %d", audit.Total)
	}
	if audit.ImmediateCount != 1 || audit.RoadmapCount != 1 || audit.NiceToHaveCount != 1 {
		t.Errorf("unexpected tier counts: immediate=%d, roadmap=%d, nice_to_have=%d",
			audit.ImmediateCount, audit.RoadmapCount, audit.NiceToHaveCount)
	}

	// 4. Update road-cli to wont-fix
	runGoals(ui, dbPath, tmp, "agent", "", "", "", false, false, "", "", "", "", "", 0, false, false, false, false, false, false, false, false, true, false, false, false, false, "", "", "", false, []string{"update", "road-cli", "--tier=wont-fix"})

	auditAfterUpdate, err := goals.ScanGoals(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if auditAfterUpdate.WontFixCount != 1 || auditAfterUpdate.RoadmapCount != 0 {
		t.Errorf("expected 1 wont-fix and 0 roadmap goals, got wont-fix=%d, roadmap=%d",
			auditAfterUpdate.WontFixCount, auditAfterUpdate.RoadmapCount)
	}

	// 5. Query goals with --json and --tier=immediate
	out := captureStdout(t, func() {
		runGoals(ui, dbPath, tmp, "agent", "", "", "", false, false, "", "", "", "", "", 0, false, false, false, false, false, false, false, false, true, false, false, false, false, "", "", "", false, []string{"--tier=immediate"}, goalsRunOptions{tier: "immediate"})
	})

	var jsonRes goals.AuditResult
	if err := json.Unmarshal([]byte(out), &jsonRes); err != nil {
		t.Fatalf("failed to decode JSON audit result: %v\nOutput: %s", err, out)
	}
	if jsonRes.Total != 1 || len(jsonRes.Goals) != 1 {
		t.Fatalf("expected exactly 1 immediate goal in JSON output, got total=%d, len=%d", jsonRes.Total, len(jsonRes.Goals))
	}
	if jsonRes.Goals[0].Slug != "imm-cli" {
		t.Errorf("expected goal slug 'imm-cli', got %q", jsonRes.Goals[0].Slug)
	}
}

func TestGoalsCreateTierCLI(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "goals_create_tier.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.EnsureProject(tmp); err != nil {
		t.Fatal(err)
	}
	st.Close()

	ui := render.New(os.Stdout, true)
	out := captureStdout(t, func() {
		runGoals(ui, dbPath, tmp, "agent", "", "", "", false, false, "", "", "", "", "internal/tier/space.go", 1, false, false, false, false, false, false, false, false, false, false, false, false, false, "", "", "", false, []string{"create", "space-tier-goal", "--tier", "immediate", "--title=Space Tier Goal", "--desc=Space tier desc", "--independent"})
	})
	if !strings.Contains(out, "closure requires feat:/fix:/refactor:/perf:/chore:") {
		t.Errorf("expected closure commit prefix hint in output, got:\n%s", out)
	}
	audit, err := goals.ScanGoals(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if audit.ImmediateCount != 1 {
		t.Fatalf("expected space-separated --tier immediate to create 1 immediate goal, got %d", audit.ImmediateCount)
	}
}
