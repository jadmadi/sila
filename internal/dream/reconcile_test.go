package dream

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jadmadi/sila/internal/assist"
	"github.com/jadmadi/sila/internal/store"
)

func TestReconcileSemanticDrift(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "reconcile.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	projDir := filepath.Join(tmp, "testproj")
	if err := os.MkdirAll(projDir, 0755); err != nil {
		t.Fatal(err)
	}
	st.EnsureProject(projDir)

	// Write repo files
	if err := os.WriteFile(filepath.Join(projDir, "store.go"), []byte("package store\nimport modernc.org/sqlite"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projDir, "db.go"), []byte("package db\nthis contradicts postgresql"), 0644); err != nil {
		t.Fatal(err)
	}

	// Insert facts
	_ = st.UpsertFact(store.Fact{
		ID:            "fact-sqlite",
		ProjectID:     "testproj",
		Claim:         "Database uses modernc.org/sqlite",
		RepoFiles:     []string{"store.go"},
		FileRefsExist: true,
		Verified:      true,
	})
	_ = st.UpsertFact(store.Fact{
		ID:            "fact-postgres",
		ProjectID:     "testproj",
		Claim:         "Database uses postgresql backend",
		RepoFiles:     []string{"db.go"},
		FileRefsExist: true,
		Verified:      true,
	})

	ctx := context.Background()

	// 1. Nil provider returns physical checks without semantic pass
	resNil, err := ReconcileSemanticDrift(ctx, st, nil, "testproj", false)
	if err != nil {
		t.Fatalf("nil provider failed: %v", err)
	}
	if resNil.TotalChecked != 2 || resNil.SemanticAudited != 0 {
		t.Errorf("unexpected nil provider result: %+v", resNil)
	}

	// 2. Mock provider performs semantic verification
	mock := assist.NewMockAdapter()
	resMock, err := ReconcileSemanticDrift(ctx, st, mock, "testproj", false)
	if err != nil {
		t.Fatalf("mock provider failed: %v", err)
	}
	if resMock.SemanticAudited != 2 {
		t.Errorf("expected 2 audited, got %d", resMock.SemanticAudited)
	}
	if resMock.SemanticVerified != 1 {
		t.Errorf("expected 1 verified, got %d", resMock.SemanticVerified)
	}
	if resMock.SemanticDrift != 1 {
		t.Errorf("expected 1 drifted, got %d", resMock.SemanticDrift)
	}
	if len(resMock.DriftWarnings) != 1 {
		t.Errorf("expected 1 drift warning, got %d", len(resMock.DriftWarnings))
	}

	// 3. Subsequent physical reconcile MUST NOT erase the semantic contradiction
	facts, err := st.FactsByProject("testproj")
	if err != nil {
		t.Fatal(err)
	}
	var postgresFact *store.Fact
	for i := range facts {
		if facts[i].ID == "fact-postgres" {
			postgresFact = &facts[i]
			break
		}
	}
	if postgresFact == nil {
		t.Fatal("fact-postgres not found")
	}
	if postgresFact.Verified {
		t.Errorf("expected fact-postgres to be unverified after semantic contradiction")
	}

	// Run physical reconcile
	if _, err := st.ReconcileDrift("testproj"); err != nil {
		t.Fatalf("ReconcileDrift failed: %v", err)
	}

	factsAfter, err := st.FactsByProject("testproj")
	if err != nil {
		t.Fatal(err)
	}
	for i := range factsAfter {
		if factsAfter[i].ID == "fact-postgres" {
			if factsAfter[i].Verified {
				t.Errorf("physical reconcile erased semantic contradiction: expected Verified=false, got Verified=true")
			}
			if !factsAfter[i].FileRefsExist {
				t.Errorf("expected FileRefsExist=true because db.go physically exists")
			}
		}
	}
}

func TestDetectContradictingFacts(t *testing.T) {
	approved := store.ReviewApproved
	enable := store.Fact{
		ID: "a", ProjectID: "p", ScopeKey: "scope-1",
		Claim: "Enable read-only mode in worker", ReviewStatus: approved,
	}
	disable := store.Fact{
		ID: "b", ProjectID: "p", ScopeKey: "scope-1",
		Claim: "Disable read-only mode in worker", ReviewStatus: approved,
	}
	pairs := DetectContradictingFacts([]store.Fact{enable, disable})
	if len(pairs) != 1 {
		t.Fatalf("pairs = %d, want 1: %#v", len(pairs), pairs)
	}
	if pairs[0].FactA != "a" || pairs[0].FactB != "b" || pairs[0].ScopeKey != "scope-1" {
		t.Fatalf("pair = %#v, want a/b in scope-1", pairs[0])
	}

	same := disable
	same.ID = "c"
	same.Claim = enable.Claim
	if got := DetectContradictingFacts([]store.Fact{enable, same}); len(got) != 0 {
		t.Fatalf("identical claim produced %d pairs", len(got))
	}

	otherScope := disable
	otherScope.ID = "d"
	otherScope.ScopeKey = "scope-2"
	if got := DetectContradictingFacts([]store.Fact{enable, otherScope}); len(got) != 0 {
		t.Fatalf("different scope produced %d pairs", len(got))
	}

	superseded := disable
	superseded.ID = "e"
	superseded.SupersedesID = "a"
	if got := DetectContradictingFacts([]store.Fact{enable, superseded}); len(got) != 0 {
		t.Fatalf("supersedes edge produced %d pairs", len(got))
	}

	stale := disable
	stale.ID = "f"
	stale.IsStaleFlag = true
	if got := DetectContradictingFacts([]store.Fact{enable, stale}); len(got) != 0 {
		t.Fatalf("stale fact produced %d pairs", len(got))
	}
}
