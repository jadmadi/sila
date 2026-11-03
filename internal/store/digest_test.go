package store

import (
	"path/filepath"
	"testing"
	"time"
)

func TestStoreDigestQueriesGlobal(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	if _, err := st.EnsureProject("/workspace/p1"); err != nil {
		t.Fatalf("EnsureProject p1: %v", err)
	}
	if _, err := st.EnsureProject("/workspace/p2"); err != nil {
		t.Fatalf("EnsureProject p2: %v", err)
	}

	now := time.Now().UTC()
	sinceTime := now.Add(-2 * time.Hour).Format(time.RFC3339)

	// Seed messages
	_, _, err = st.WriteRun("", "devin", "s1", []LoadMessage{
		{ProjectID: "p1", ContentHash: "h1", TS: now.Add(-1 * time.Hour).Format(time.RFC3339), Role: "assistant", Content: "work on p1", Tool: "devin"},
	})
	if err != nil {
		t.Fatalf("WriteRun p1: %v", err)
	}
	_, _, err = st.WriteRun("", "claude", "s2", []LoadMessage{
		{ProjectID: "p2", ContentHash: "h2", TS: now.Add(-30 * time.Minute).Format(time.RFC3339), Role: "assistant", Content: "work on p2", Tool: "claude-code"},
	})
	if err != nil {
		t.Fatalf("WriteRun p2: %v", err)
	}

	// Seed facts
	if err := st.UpsertFact(Fact{
		ID: "f1", ProjectID: "p1", Claim: "Decision on p1", Kind: "invariant",
		PromotedAt: now.Add(-1 * time.Hour).Format(time.RFC3339), DistillStatus: "skipped",
	}); err != nil {
		t.Fatalf("UpsertFact f1: %v", err)
	}
	if err := st.UpsertFact(Fact{
		ID: "f2", ProjectID: "p2", Claim: "Decision on p2", Kind: "invariant",
		PromotedAt: now.Add(-40 * time.Minute).Format(time.RFC3339), DistillStatus: "skipped",
	}); err != nil {
		t.Fatalf("UpsertFact f2: %v", err)
	}

	// Seed handoffs
	if err := st.InsertHandoff(Handoff{
		ID: "h1", ProjectID: "p1", Agent: "devin", Branch: "main",
		Summary: "handoff p1", OpenItems: []string{"task p1"},
		CreatedAt: now.Add(-50 * time.Minute).Format(time.RFC3339), Status: "open",
	}); err != nil {
		t.Fatalf("InsertHandoff h1: %v", err)
	}
	if err := st.InsertHandoff(Handoff{
		ID: "h2", ProjectID: "p2", Agent: "claude", Branch: "feat/ui",
		Summary: "handoff p2", OpenItems: []string{"task p2"},
		CreatedAt: now.Add(-20 * time.Minute).Format(time.RFC3339), Status: "open",
	}); err != nil {
		t.Fatalf("InsertHandoff h2: %v", err)
	}

	// 1. Test MessagesSince global (projectSel = "")
	allMsgs, err := st.MessagesSince("", sinceTime, 10)
	if err != nil {
		t.Fatalf("MessagesSince global: %v", err)
	}
	if len(allMsgs) != 2 {
		t.Errorf("MessagesSince global returned %d messages, want 2", len(allMsgs))
	}

	// Single project
	p1Msgs, err := st.MessagesSince("p1", sinceTime, 10)
	if err != nil {
		t.Fatalf("MessagesSince p1: %v", err)
	}
	if len(p1Msgs) != 1 || p1Msgs[0].ProjectID != "p1" {
		t.Errorf("MessagesSince p1 returned %v, want 1 message from p1", p1Msgs)
	}

	// 2. Test FactsSince global (projectSel = "")
	allFacts, err := st.FactsSince("", sinceTime, 10)
	if err != nil {
		t.Fatalf("FactsSince global: %v", err)
	}
	if len(allFacts) != 2 {
		t.Errorf("FactsSince global returned %d facts, want 2", len(allFacts))
	}

	// Single project
	p2Facts, err := st.FactsSince("p2", sinceTime, 10)
	if err != nil {
		t.Fatalf("FactsSince p2: %v", err)
	}
	if len(p2Facts) != 1 || p2Facts[0].ProjectID != "p2" {
		t.Errorf("FactsSince p2 returned %v, want 1 fact from p2", p2Facts)
	}

	// 3. Test HandoffsSince global (projectSel = "")
	allHandoffs, err := st.HandoffsSince("", sinceTime, 10)
	if err != nil {
		t.Fatalf("HandoffsSince global: %v", err)
	}
	if len(allHandoffs) != 2 {
		t.Errorf("HandoffsSince global returned %d handoffs, want 2", len(allHandoffs))
	}

	// Single project
	p1Handoffs, err := st.HandoffsSince("p1", sinceTime, 10)
	if err != nil {
		t.Fatalf("HandoffsSince p1: %v", err)
	}
	if len(p1Handoffs) != 1 || p1Handoffs[0].ProjectID != "p1" {
		t.Errorf("HandoffsSince p1 returned %v, want 1 handoff from p1", p1Handoffs)
	}
}
