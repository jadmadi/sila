package store

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestScopeKeyOrderIndependentAndDeduped(t *testing.T) {
	a := ScopeKey([]string{"b.go", "a.go", "a.go", "./a.go"})
	b := ScopeKey([]string{"a.go", "b.go"})
	if a == "" || a != b {
		t.Fatalf("keys differ: %q %q", a, b)
	}
	if ScopeKey(nil) != "" {
		t.Fatal("empty files must have an empty key")
	}
}

func TestScopeBackfill(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if version, err := st.SchemaVersion(); err != nil || version != CurrentSchemaVersion() {
		t.Fatalf("version = %d err=%v, want %d", version, err, CurrentSchemaVersion())
	}
	tx, err := st.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	exec := func(id, repoJSON string) {
		t.Helper()
		if _, err := tx.Exec(`INSERT INTO facts (id, project_id, claim, confidence, verified, repo_files, tools, session_ref, first_seen, promoted_at, kind)
			VALUES (?, 'p', ?, 'grounded', 0, ?, '[]', 's', '2026-11-06T00:00:00Z', '2026-11-06T00:00:00Z', 'memo')`, id, "claim-"+id, repoJSON); err != nil {
			t.Fatal(err)
		}
	}
	exec("m1", `["a.go","b.go"]`)
	exec("m2", `["b.go","a.go"]`)
	exec("m3", `[]`)
	if err := backfillScopeKeys(tx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var key1, key2, key3 string
	_ = st.DB.QueryRow(`SELECT scope_key FROM facts WHERE id='m1'`).Scan(&key1)
	_ = st.DB.QueryRow(`SELECT scope_key FROM facts WHERE id='m2'`).Scan(&key2)
	_ = st.DB.QueryRow(`SELECT scope_key FROM facts WHERE id='m3'`).Scan(&key3)
	if key1 == "" || key1 != key2 || key3 != "" {
		t.Fatalf("keys = %q %q %q", key1, key2, key3)
	}
	// Re-running is a no-op.
	tx2, _ := st.DB.Begin()
	if err := backfillScopeKeys(tx2); err != nil {
		t.Fatal(err)
	}
	_ = tx2.Commit()
	var after string
	_ = st.DB.QueryRow(`SELECT scope_key FROM facts WHERE id='m1'`).Scan(&after)
	if after != key1 {
		t.Fatalf("rerun changed key: %q -> %q", key1, after)
	}
}

// TestFactVisibilityScope proves the migration default, explicit global
// persistence, and rejection of invalid visibility values.
func TestFactVisibilityScope(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "visibility.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	const project = "alpha"

	t.Run("defaults to project", func(t *testing.T) {
		f, err := st.InsertMemoWithProvenanceAndVisibility(project, "default scope claim", "agent", "", nil, "", "", Provenance{})
		if err != nil {
			t.Fatalf("insert memo: %v", err)
		}
		if f.Visibility != VisibilityProject {
			t.Fatalf("returned visibility = %q, want %q", f.Visibility, VisibilityProject)
		}
		stored, err := st.FactByID(f.ID)
		if err != nil || stored == nil {
			t.Fatalf("read back fact: fact=%v err=%v", stored, err)
		}
		if stored.Visibility != VisibilityProject {
			t.Fatalf("stored visibility = %q, want %q", stored.Visibility, VisibilityProject)
		}
	})

	t.Run("stores explicit global", func(t *testing.T) {
		f, err := st.InsertMemoWithProvenanceAndVisibility(project, "global scope claim", "agent", "", nil, "", VisibilityGlobal, Provenance{})
		if err != nil {
			t.Fatalf("insert memo: %v", err)
		}
		if f.Visibility != VisibilityGlobal {
			t.Fatalf("returned visibility = %q, want %q", f.Visibility, VisibilityGlobal)
		}
		stored, err := st.FactByID(f.ID)
		if err != nil || stored == nil {
			t.Fatalf("read back fact: fact=%v err=%v", stored, err)
		}
		if stored.Visibility != VisibilityGlobal {
			t.Fatalf("stored visibility = %q, want %q", stored.Visibility, VisibilityGlobal)
		}
	})

	t.Run("rejects invalid visibility", func(t *testing.T) {
		_, err := st.InsertMemoWithProvenanceAndVisibility(project, "invalid scope claim", "agent", "", nil, "", "public", Provenance{})
		if err == nil {
			t.Fatal("expected invalid visibility error")
		}
		want := `invalid visibility "public": must be project or global`
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want substring %q", err.Error(), want)
		}

		err = st.SaveFact(Fact{ProjectID: project, Claim: "direct invalid scope", Visibility: "shared"})
		if err == nil {
			t.Fatal("expected invalid visibility error from SaveFact")
		}
		if !strings.Contains(err.Error(), `invalid visibility "shared": must be project or global`) {
			t.Fatalf("SaveFact error = %q", err.Error())
		}
	})
}
