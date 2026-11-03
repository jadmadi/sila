package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func TestApproveCLI_FullLifecycle(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	dbPath := filepath.Join(tempHome, "test_approvals.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer st.Close()

	ui := render.New(os.Stdout, true)

	// 1. Empty list
	runApprove(ui, dbPath, "test-proj", false, "", "", false, false, false, []string{"list"})
	runApprove(ui, dbPath, "test-proj", false, "", "", false, true, false, []string{"list"})

	// 2. Insert test approval requests
	appr1 := &store.Approval{
		ID:             "appr_test01",
		Project:        "test-proj",
		Agent:          "claude-code",
		Prompt:         "Drop legacy table user_sessions",
		RiskLevel:      "high",
		Metadata:       map[string]string{"table": "user_sessions"},
		TimeoutSeconds: 300,
		Status:         "pending",
		CreatedAt:      time.Now(),
	}
	if err := st.InsertApproval(appr1); err != nil {
		t.Fatalf("failed to insert approval 1: %v", err)
	}

	appr2 := &store.Approval{
		ID:             "appr_test02",
		Project:        "test-proj",
		Agent:          "devin",
		Prompt:         "Deploy canary version v2.4.0",
		RiskLevel:      "critical",
		Metadata:       map[string]string{"version": "v2.4.0"},
		TimeoutSeconds: 600,
		Status:         "pending",
		CreatedAt:      time.Now(),
	}
	if err := st.InsertApproval(appr2); err != nil {
		t.Fatalf("failed to insert approval 2: %v", err)
	}

	// 3. List pending approvals
	runApprove(ui, dbPath, "test-proj", false, "", "", true, false, false, []string{"list"})
	runApprove(ui, dbPath, "test-proj", false, "", "", true, true, false, []string{"list"})

	// 4. Show approval
	runApprove(ui, dbPath, "test-proj", false, "", "", false, false, false, []string{"show", "appr_test01"})
	runApprove(ui, dbPath, "test-proj", false, "", "", false, true, false, []string{"show", "appr_test01"})

	// 5. Approve appr_test01 via direct ID argument
	runApprove(ui, dbPath, "test-proj", false, "alice", "Verified schema migration plan", false, false, false, []string{"appr_test01"})

	// Verify appr_test01 in store
	got1, err := st.GetApproval("appr_test01")
	if err != nil {
		t.Fatalf("failed to get approval 1: %v", err)
	}
	if got1.Status != "approved" {
		t.Fatalf("expected status approved, got %s", got1.Status)
	}
	if got1.DecidedBy != "alice" {
		t.Fatalf("expected decided_by alice, got %s", got1.DecidedBy)
	}

	// 6. Reject appr_test02 via reject flag
	runApprove(ui, dbPath, "test-proj", true, "security-lead", "Production freeze in effect", false, false, false, []string{"appr_test02"})

	// Verify appr_test02 in store
	got2, err := st.GetApproval("appr_test02")
	if err != nil {
		t.Fatalf("failed to get approval 2: %v", err)
	}
	if got2.Status != "rejected" {
		t.Fatalf("expected status rejected, got %s", got2.Status)
	}
	if got2.DecisionReason != "Production freeze in effect" {
		t.Fatalf("expected reason 'Production freeze in effect', got %s", got2.DecisionReason)
	}

	// 7. Insert appr3 and test explicit approve / reject subcommands
	appr3 := &store.Approval{
		ID:             "appr_test03",
		Project:        "test-proj",
		Agent:          "agy",
		Prompt:         "Restart gateway daemon",
		RiskLevel:      "low",
		TimeoutSeconds: 300,
		Status:         "pending",
		CreatedAt:      time.Now(),
	}
	if err := st.InsertApproval(appr3); err != nil {
		t.Fatalf("failed to insert approval 3: %v", err)
	}

	runApprove(ui, dbPath, "test-proj", false, "bob", "Restart acknowledged", false, true, false, []string{"approve", "appr_test03"})
	got3, _ := st.GetApproval("appr_test03")
	if got3.Status != "approved" {
		t.Fatalf("expected status approved, got %s", got3.Status)
	}

	// 8. Test gateway approve routing
	runGateway(ui, dbPath, "test-proj", false, false, []string{"approve", "list"})
}
