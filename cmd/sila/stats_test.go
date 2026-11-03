package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func TestStats_SnapshotOutputs(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "sila-stats-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "sila.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}

	// Register project
	proj, err := st.EnsureProject(tmpDir)
	if err != nil {
		t.Fatalf("failed to ensure project: %v", err)
	}

	// Seed memo
	_, err = st.InsertMemo(proj.ID, "Test architectural invariant memo", "antigravity", tmpDir, nil)
	if err != nil {
		t.Fatalf("failed to insert memo: %v", err)
	}

	// Seed pending approval
	err = st.InsertApproval(&store.Approval{
		ID:             "app_9999",
		Project:        proj.ID,
		Agent:          "antigravity",
		Prompt:         "git push origin main",
		RiskLevel:      "high",
		Status:         "pending",
		TimeoutSeconds: 300,
	})
	if err != nil {
		t.Fatalf("failed to insert approval: %v", err)
	}
	st.Close()

	ui := render.New(nil, true)

	// 1. Test Plain text output
	outPlain := captureStdout(t, func() {
		runStats(ui, dbPath, proj.ID, false, false, true, nil)
	})

	if strings.Contains(outPlain, "\x1b[") {
		t.Errorf("expected no ANSI codes in plain mode, found some in:\n%s", outPlain)
	}
	if !strings.Contains(outPlain, "Live Agent Fleet Roster") {
		t.Errorf("expected Live Agent Fleet Roster in plain output:\n%s", outPlain)
	}
	if !strings.Contains(outPlain, "Human-in-the-Loop Steering Desk") {
		t.Errorf("expected Steering Desk in plain output:\n%s", outPlain)
	}
	if !strings.Contains(outPlain, "Active Mutation & Diff Velocity") {
		t.Errorf("expected Diff Velocity in plain output:\n%s", outPlain)
	}
	if !strings.Contains(outPlain, "LEDGER: intact") {
		t.Errorf("expected LEDGER badge in plain output:\n%s", outPlain)
	}
	if !strings.Contains(outPlain, "app_9999") {
		t.Errorf("expected approval app_9999 in plain output:\n%s", outPlain)
	}

	// 2. Test JSON output
	outJSON := captureStdout(t, func() {
		runStats(ui, dbPath, proj.ID, true, false, false, nil)
	})

	var snapMap map[string]any
	if err := json.Unmarshal([]byte(outJSON), &snapMap); err != nil {
		t.Fatalf("failed to unmarshal JSON output: %v\nOutput was:\n%s", err, outJSON)
	}

	if _, ok := snapMap["header"]; !ok {
		t.Errorf("missing header in JSON snapshot")
	}
	if _, ok := snapMap["fleet"]; !ok {
		t.Errorf("missing fleet in JSON snapshot")
	}
	if _, ok := snapMap["approvals"]; !ok {
		t.Errorf("missing approvals in JSON snapshot")
	}
	if _, ok := snapMap["ledger"]; !ok {
		t.Errorf("missing ledger in JSON snapshot")
	}

	// 3. Test TOON output
	outTOON := captureStdout(t, func() {
		runStats(ui, dbPath, proj.ID, false, true, false, nil)
	})

	if !strings.Contains(outTOON, "cockpit_summary:") {
		t.Errorf("expected cockpit_summary in TOON output:\n%s", outTOON)
	}
	if !strings.Contains(outTOON, "approvals[1]{id,agent,action,reason,risk,remaining}:") {
		t.Errorf("expected approvals table in TOON output:\n%s", outTOON)
	}
	if !strings.Contains(outTOON, "app_9999") {
		t.Errorf("expected app_9999 in TOON output:\n%s", outTOON)
	}
}
