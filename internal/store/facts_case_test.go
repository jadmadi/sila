package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCaseInsertQueryAndGrounding(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(filepath.Join(dir, "case.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	proj, err := st.EnsureProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	testFile := filepath.Join(dir, "pool.go")
	if err := os.WriteFile(testFile, []byte("package pool"), 0o644); err != nil {
		t.Fatal(err)
	}

	content := FormatCase("SQLite connection pooling", "single-writer WAL mode with busy timeout", "only one write transaction at a time", "sqlite,wal,concurrency")
	fact, err := st.InsertCase(proj.ID, content, "agent", dir, []string{"pool.go"})
	if err != nil {
		t.Fatal(err)
	}
	if fact.Kind != "case" {
		t.Fatalf("kind = %q, want case", fact.Kind)
	}
	if !fact.IsCase() {
		t.Fatal("IsCase = false, want true")
	}
	if !fact.FileRefsExist {
		t.Fatal("FileRefsExist = false, want true for an existing evidence file")
	}
	if !fact.IsFact() {
		t.Fatal("IsFact = false, want true for approved grounded case")
	}

	intent, approach, invariant, tags := ParseCase(content)
	if intent != "SQLite connection pooling" || approach != "single-writer WAL mode with busy timeout" || invariant != "only one write transaction at a time" || tags != "sqlite,wal,concurrency" {
		t.Fatalf("parse = %q/%q/%q/%q", intent, approach, invariant, tags)
	}

	cases, err := st.CaseFacts(proj.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) != 1 || cases[0].ID != fact.ID {
		t.Fatalf("CaseFacts = %d rows, want the inserted case", len(cases))
	}

	all, err := st.CaseFacts("", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Fatalf("cross-project CaseFacts = %d rows, want 1", len(all))
	}

	ungrounded, err := st.InsertCase(proj.ID, FormatCase("missing evidence", "", "", ""), "agent", dir, []string{"nonexistent.go"})
	if err != nil {
		t.Fatal(err)
	}
	if ungrounded.FileRefsExist {
		t.Fatal("FileRefsExist = true for a missing evidence file, want false")
	}

	// Test SaveCase helper
	savedFact, err := st.SaveCase(proj.ID, "HTTP middleware pattern", "standard http.Handler wrapping", "order matters", "http", "agent", dir, []string{"pool.go"})
	if err != nil {
		t.Fatal(err)
	}
	if !savedFact.IsCase() || !savedFact.FileRefsExist {
		t.Fatalf("SaveCase failed: isCase=%v, fileRefsExist=%v", savedFact.IsCase(), savedFact.FileRefsExist)
	}

	// Test Drift Reconciliation when evidence file is deleted
	if err := os.Remove(testFile); err != nil {
		t.Fatal(err)
	}
	res, err := st.ReconcileFactDrift(proj.ID, dir)
	if err != nil {
		t.Fatal(err)
	}
	if res.NewlyStale < 2 {
		t.Fatalf("res.NewlyStale = %d, want at least 2 after deleting evidence file", res.NewlyStale)
	}

	// Verify case is now ungrounded and no longer an active authority fact
	updatedCases, err := st.CaseFacts(proj.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range updatedCases {
		if c.ID == fact.ID {
			if c.FileRefsExist {
				t.Fatal("c.FileRefsExist = true after drift reconciliation, want false")
			}
			if c.IsFact() {
				t.Fatal("c.IsFact() = true after drift reconciliation, want false")
			}
		}
	}
}
