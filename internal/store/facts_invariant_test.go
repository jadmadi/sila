package store

import (
	"path/filepath"
	"testing"
)

func TestFactsSchemaInvariantRule43(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// 1. Insert a fact
	f1 := Fact{
		ID:         "fact_1",
		ProjectID:  "sila",
		Claim:      "Always use modernc.org/sqlite (pure-Go SQLite)",
		Confidence: "high",
		Verified:   true,
	}
	if err := st.UpsertFact(f1); err != nil {
		t.Fatalf("first upsert failed: %v", err)
	}

	// 2. Insert another fact with identical (project_id, claim) but different ID
	f2 := Fact{
		ID:         "fact_2",
		ProjectID:  "sila",
		Claim:      "Always use modernc.org/sqlite (pure-Go SQLite)",
		Confidence: "grounded",
		Verified:   true,
	}
	if err := st.UpsertFact(f2); err != nil {
		t.Fatalf("second upsert with same claim failed: %v", err)
	}

	// 3. Verify that only 1 fact exists for "sila"
	facts, err := st.FactsByProject("sila")
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 1 {
		t.Fatalf("expected exactly 1 fact due to UNIQUE(project_id, claim) invariant, got %d", len(facts))
	}

	// 4. Verify raw SQL unique index directly rejects duplicate raw inserts
	_, rawErr := st.DB.Exec(`INSERT INTO facts(id, project_id, claim, confidence, verified, file_refs_exist, repo_files, tools, session_ref, first_seen, promoted_at, refs_checked_at, distilled, distill_status, kind)
		VALUES("raw_dup", "sila", "Always use modernc.org/sqlite (pure-Go SQLite)", "mid", 1, 1, "[]", "[]", "s", "now", "now", "now", "", "pending", "")`)
	if rawErr == nil {
		t.Fatalf("expected raw duplicate insert to fail on idx_facts_project_claim, but it succeeded")
	}
}

func TestReconcileFactDriftPreview(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// Insert fact referencing non-existent file
	f := Fact{
		ID:            "fact_drift",
		ProjectID:     "myproject",
		Claim:         "Architecture invariant",
		RepoFiles:     []string{"non_existent_file.go"},
		Verified:      true,
		FileRefsExist: true,
	}
	if err := st.UpsertFact(f); err != nil {
		t.Fatal(err)
	}

	// Run preview
	res, err := st.ReconcileFactDriftPreview("myproject", dir)
	if err != nil {
		t.Fatal(err)
	}
	if res.TotalChecked != 1 || res.NewlyStale != 1 {
		t.Fatalf("expected 1 checked and 1 newly stale in preview, got %+v", res)
	}

	// Verify database was NOT mutated by preview
	facts, err := st.FactsByProject("myproject")
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 1 || !facts[0].FileRefsExist {
		t.Fatalf("expected fact to still have FileRefsExist=true after preview, got %+v", facts[0])
	}
}
