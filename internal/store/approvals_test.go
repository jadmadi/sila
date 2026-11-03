package store_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/store"
)

func TestStore_ApprovalsLifecycle(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "approvals_test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// 1. Insert an approval request
	appr := &store.Approval{
		ID:             "appr_12345",
		Project:        "project-alpha",
		Agent:          "antigravity",
		Prompt:         "Migrate users table and drop obsolete legacy_pass column",
		RiskLevel:      "high",
		Metadata:       map[string]string{"tool": "run_gate_pipeline", "target": "production"},
		TimeoutSeconds: 120,
		Status:         "pending",
	}

	if err := st.InsertApproval(appr); err != nil {
		t.Fatalf("InsertApproval failed: %v", err)
	}

	// 2. Retrieve by ID
	got, err := st.GetApproval("appr_12345")
	if err != nil {
		t.Fatalf("GetApproval failed: %v", err)
	}
	if got == nil {
		t.Fatalf("expected approval, got nil")
	}
	if got.ID != "appr_12345" || got.Project != "project-alpha" || got.RiskLevel != "high" {
		t.Errorf("field mismatch: got %+v", got)
	}
	if got.Metadata["tool"] != "run_gate_pipeline" {
		t.Errorf("metadata mismatch: got %+v", got.Metadata)
	}

	// 3. List pending approvals
	pending, err := st.ListPendingApprovals("project-alpha")
	if err != nil {
		t.Fatalf("ListPendingApprovals failed: %v", err)
	}
	if len(pending) != 1 || pending[0].ID != "appr_12345" {
		t.Fatalf("expected 1 pending approval, got %d", len(pending))
	}

	// 4. Update decision (approve)
	if err := st.UpdateApprovalDecision("appr_12345", "approved", "telegram:@jadmadi", "Looks safe to proceed"); err != nil {
		t.Fatalf("UpdateApprovalDecision failed: %v", err)
	}

	// 5. Verify updated state
	decided, err := st.GetApproval("appr_12345")
	if err != nil {
		t.Fatalf("GetApproval after decision failed: %v", err)
	}
	if decided.Status != "approved" || decided.DecidedBy != "telegram:@jadmadi" || decided.DecisionReason != "Looks safe to proceed" {
		t.Errorf("decision mismatch: got %+v", decided)
	}
	if decided.DecidedAt.IsZero() {
		t.Errorf("expected non-zero DecidedAt timestamp")
	}

	// 6. Verify it is no longer pending
	pendingAfter, err := st.ListPendingApprovals("project-alpha")
	if err != nil {
		t.Fatalf("ListPendingApprovals failed: %v", err)
	}
	if len(pendingAfter) != 0 {
		t.Fatalf("expected 0 pending approvals, got %d", len(pendingAfter))
	}

	// 7. Verify re-decision fails
	if err := st.UpdateApprovalDecision("appr_12345", "rejected", "cli:local", ""); err == nil {
		t.Errorf("expected error when deciding already decided approval, got nil")
	}
}

func TestStore_ApprovalsListing(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "approvals_list_test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	for i := 1; i <= 3; i++ {
		_ = st.InsertApproval(&store.Approval{
			ID:             "appr_" + string(rune('0'+i)),
			Project:        "project-beta",
			Agent:          "test-agent",
			Prompt:         "Test prompt",
			TimeoutSeconds: 60,
			CreatedAt:      time.Now().Add(time.Duration(i) * time.Minute),
		})
	}

	list, err := st.ListApprovals("project-beta", 10)
	if err != nil {
		t.Fatalf("ListApprovals failed: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("expected 3 approvals, got %d", len(list))
	}
}
