package goals

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/render"
)

func TestAuditMatrixMetricsAndSectionParity(t *testing.T) {
	tmp := t.TempDir()

	// Goal 1: Independent
	_, err := CreateGoal(CreateGoalParams{
		RootDir: tmp,
		Slug:    "goal-indep",
		Title:   "Independent Goal",
	})
	if err != nil {
		t.Fatalf("create goal-indep: %v", err)
	}

	// Goal 2: Blocked by Goal 1
	_, err = CreateGoal(CreateGoalParams{
		RootDir:   tmp,
		Slug:      "goal-blocked",
		Title:     "Blocked Goal",
		DependsOn: []string{"goal-indep"},
	})
	if err != nil {
		t.Fatalf("create goal-blocked: %v", err)
	}

	// Scan goals
	audit, err := ScanGoals(tmp)
	if err != nil {
		t.Fatalf("scan goals: %v", err)
	}

	if audit.Total != 2 {
		t.Fatalf("expected total 2, got %d", audit.Total)
	}
	if audit.ReadyCount != 1 {
		t.Errorf("expected 1 ready goal, got %d", audit.ReadyCount)
	}
	if audit.ReadyIndependentCount != 1 {
		t.Errorf("expected 1 ready independent goal, got %d", audit.ReadyIndependentCount)
	}
	if audit.ReadyUnblockedCount != 0 {
		t.Errorf("expected 0 ready unblocked goals, got %d", audit.ReadyUnblockedCount)
	}
	if audit.BlockedCount != 1 {
		t.Errorf("expected 1 blocked goal, got %d", audit.BlockedCount)
	}
	if audit.PendingCount != 2 {
		t.Errorf("expected 2 pending goals, got %d", audit.PendingCount)
	}

	// Mathematical parity check
	if audit.ImplementedCount+audit.ClaimedCount+audit.ReadyCount+audit.BlockedCount+audit.ContestedCount+audit.DraftCount != audit.Total {
		t.Errorf("mathematical sum mismatch: %d != %d",
			audit.ImplementedCount+audit.ClaimedCount+audit.ReadyCount+audit.BlockedCount+audit.ContestedCount+audit.DraftCount,
			audit.Total)
	}

	// Capture PrintAudit output
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	ui := render.New(w, false)
	PrintAuditTable(ui, audit, "")

	w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	io.Copy(&buf, r)
	output := buf.String()

	// Verify Ready to Pick header uses ReadyCount (1) and breakdown (1 independent, 0 unblocked)
	if !strings.Contains(output, "Ready to Pick:") || !strings.Contains(output, "1 (1 independent ⚡, 0 unblocked 🔗)") {
		t.Errorf("expected Ready to Pick with the independent/unblocked breakdown, got output:\n%s", output)
	}

	// Verify separation of ready vs blocked goals
	if !strings.Contains(output, "🟡 GOALS READY TO CLAIM & EXECUTE (1):") {
		t.Errorf("expected ready header '(1):', got output:\n%s", output)
	}
	if !strings.Contains(output, "⛔ BLOCKED GOALS (WAITING ON PREREQUISITES) (1):") {
		t.Errorf("expected blocked header '(1):', got output:\n%s", output)
	}

	// Ensure goal-blocked is under BLOCKED GOALS and NOT under GOALS READY TO CLAIM & EXECUTE
	readyIdx := strings.Index(output, "🟡 GOALS READY TO CLAIM & EXECUTE")
	blockedHeaderIdx := strings.Index(output, "⛔ BLOCKED GOALS")
	blockedGoalIdx := strings.Index(output, "goal-blocked")

	if blockedGoalIdx < blockedHeaderIdx || blockedGoalIdx > readyIdx {
		t.Errorf("goal-blocked was rendered outside the blocked section (blockedHeader: %d, goal: %d, readyHeader: %d)!", blockedHeaderIdx, blockedGoalIdx, readyIdx)
	}
}
