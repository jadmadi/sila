package assess

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAgentRoleClass_ToolStripping(t *testing.T) {
	allTools := []string{
		"view_file",
		"write_to_file",
		"replace_file_content",
		"search_knowledge",
		"claim_goal",
		"run_gate_pipeline",
		"git_push",
		"salam",
	}

	// Producer retains all tools
	prodTools := StripMutationTools(RoleClassProducer, allTools)
	if len(prodTools) != len(allTools) {
		t.Fatalf("expected producer to retain all %d tools, got %d", len(allTools), len(prodTools))
	}

	// Orchestrator retains all tools
	orchTools := StripMutationTools(RoleClassOrchestrator, allTools)
	if len(orchTools) != len(allTools) {
		t.Fatalf("expected orchestrator to retain all %d tools, got %d", len(allTools), len(orchTools))
	}

	// Reviewer has all mutation tools stripped
	revTools := StripMutationTools(RoleClassReviewer, allTools)
	for _, tool := range revTools {
		if IsMutationTool(tool) {
			t.Errorf("reviewer tool list contains mutation tool %q", tool)
		}
	}

	// Ensure read tools are preserved for reviewer
	expectedReadOnly := []string{"view_file", "search_knowledge", "run_gate_pipeline", "salam"}
	for _, exp := range expectedReadOnly {
		found := false
		for _, rt := range revTools {
			if rt == exp {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected read-only tool %q to be preserved for reviewer, but it was missing", exp)
		}
	}
}

func TestAuditIndependence_Validation(t *testing.T) {
	// Case 1: Identical agent identity (self-audit)
	err := ValidateAuditIndependence(
		AgentIdentity{Agent: "@antigravity", Session: "sess-1"},
		AgentIdentity{Agent: "antigravity", Session: "sess-2"},
	)
	if err == nil {
		t.Fatalf("expected self-audit error for identical agent names, got nil")
	}

	// Case 2: Identical session ID (session bleed)
	err = ValidateAuditIndependence(
		AgentIdentity{Agent: "@agent-alice", Session: "shared-session-xyz"},
		AgentIdentity{Agent: "@agent-bob", Session: "shared-session-xyz"},
	)
	if err == nil {
		t.Fatalf("expected session bleed error for identical session IDs, got nil")
	}

	// Case 3: Completely independent producer and reviewer
	err = ValidateAuditIndependence(
		AgentIdentity{Agent: "@agent-producer", Session: "sess-prod-123"},
		AgentIdentity{Agent: "@mystery-auditor", Session: "sess-audit-456"},
	)
	if err != nil {
		t.Fatalf("expected clean pass for independent agents, got error: %v", err)
	}
}

func TestReviewerVeto_WorkflowAndResolution(t *testing.T) {
	tempDir := t.TempDir()
	goalSlug := "sample-vetoed-goal"

	// Initially no veto
	veto, err := CheckReviewerVeto(tempDir, goalSlug)
	if err != nil {
		t.Fatalf("check veto: %v", err)
	}
	if veto != nil {
		t.Fatalf("expected nil veto initially, got: %+v", veto)
	}

	// Record an active non-overrulable veto
	recorded, err := RecordReviewerVeto(tempDir, goalSlug, "@mystery-auditor", "sess-audit-1", "contract violation: missing race condition guard", "mystery.go")
	if err != nil {
		t.Fatalf("record veto: %v", err)
	}
	if !recorded.Active {
		t.Fatalf("expected recorded veto to be active")
	}

	// Check that veto is active
	activeVeto, err := CheckReviewerVeto(tempDir, goalSlug)
	if err != nil {
		t.Fatalf("check active veto: %v", err)
	}
	if activeVeto == nil || !activeVeto.Active {
		t.Fatalf("expected active veto on goal, got: %+v", activeVeto)
	}

	// Scan active vetoes across project
	allVetoes, err := ScanActiveVetoes(tempDir)
	if err != nil {
		t.Fatalf("scan active vetoes: %v", err)
	}
	if len(allVetoes) != 1 || allVetoes[0].GoalSlug != goalSlug {
		t.Fatalf("expected 1 active veto for %s, got: %+v", goalSlug, allVetoes)
	}

	// Resolve veto through reviewer receipt
	receiptPath := filepath.Join(tempDir, "goals", goalSlug, "audit-receipt.json")
	if err := os.WriteFile(receiptPath, []byte(`{"status":"passed"}`), 0644); err != nil {
		t.Fatalf("write mock receipt: %v", err)
	}

	err = ResolveReviewerVeto(tempDir, goalSlug, "@mystery-auditor", "sess-audit-2", receiptPath)
	if err != nil {
		t.Fatalf("resolve veto: %v", err)
	}

	// Check that veto is no longer active
	resolvedVeto, err := CheckReviewerVeto(tempDir, goalSlug)
	if err != nil {
		t.Fatalf("check resolved veto: %v", err)
	}
	if resolvedVeto != nil {
		t.Fatalf("expected no active veto after resolution, got: %+v", resolvedVeto)
	}

	// Scan should now return 0 active vetoes
	remaining, err := ScanActiveVetoes(tempDir)
	if err != nil {
		t.Fatalf("scan remaining: %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("expected 0 active vetoes after resolution, got: %d", len(remaining))
	}
}
