package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCanonicalProjectPathResolvesSymlinkAlias(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(real, alias); err != nil {
		t.Fatal(err)
	}
	if got := CanonicalProjectPath(alias); got != real {
		t.Fatalf("CanonicalProjectPath(alias) = %q, want %q", got, real)
	}
	sub := filepath.Join(real, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := CanonicalProjectPath(filepath.Join(alias, "sub")); got != sub {
		t.Fatalf("CanonicalProjectPath(alias/sub) = %q, want %q", got, sub)
	}
}

func TestMergeProjectsMovesDataAndRemovesDrop(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(filepath.Join(dir, "merge.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	keep, err := st.EnsureProject(filepath.Join(dir, "keep"))
	if err != nil {
		t.Fatal(err)
	}
	drop, err := st.EnsureProject(filepath.Join(dir, "drop"))
	if err != nil {
		t.Fatal(err)
	}

	if _, _, err := st.WriteRun("", "opencode", "1", []LoadMessage{{
		ProjectID:   drop.ID,
		ContentHash: "merge-msg",
		TS:          "2026-09-20T08:00:00Z",
		Role:        "assistant",
		Content:     "drop project data",
		Tool:        "opencode",
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.InsertMemo(drop.ID, "drop memo", "agent", "", nil); err != nil {
		t.Fatal(err)
	}

	if err := st.MergeProjects(keep.ID, drop.ID); err != nil {
		t.Fatal(err)
	}

	projects, err := st.Projects()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range projects {
		if p.ID == drop.ID {
			t.Fatalf("dropped project %s still registered", drop.ID)
		}
	}

	var msgs int
	if err := st.DB.QueryRow(`SELECT COUNT(*) FROM messages WHERE project_id=?`, keep.ID).Scan(&msgs); err != nil {
		t.Fatal(err)
	}
	if msgs != 1 {
		t.Fatalf("kept project messages = %d, want 1", msgs)
	}
	facts, err := st.FactsByProject(keep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 1 {
		t.Fatalf("kept project facts = %d, want 1", len(facts))
	}
}

func TestAliasDuplicatePairsFindSymlinkedRegistration(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(filepath.Join(dir, "aliases.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	real := filepath.Join(dir, "repo")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(dir, "repo-alias")
	if err := os.Symlink(real, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := st.EnsureProject(real); err != nil {
		t.Fatal(err)
	}
	// Registration itself now resolves aliases, so simulate pre-fix data with a
	// second row that points at the symlink path.
	if _, err := st.DB.Exec(`INSERT INTO projects (path, id, created_at) VALUES (?,?,?)`, alias, "repo-alias-legacy", "2026-01-02T00:00:00Z"); err != nil {
		t.Fatal(err)
	}

	pairs, err := st.AliasDuplicatePairs()
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) != 1 {
		t.Fatalf("pairs = %d, want 1", len(pairs))
	}
	if pairs[0].KeepPath != real || pairs[0].DropPath != alias {
		t.Fatalf("pair = %+v, want keep %s and drop %s", pairs[0], real, alias)
	}
	merged, err := st.MergeAliasDuplicates()
	if err != nil {
		t.Fatal(err)
	}
	if len(merged) != 1 {
		t.Fatalf("merged = %d, want 1", len(merged))
	}
	if pairsAfter, err := st.AliasDuplicatePairs(); err != nil || len(pairsAfter) != 0 {
		t.Fatalf("pairs after merge = %d (err %v), want 0", len(pairsAfter), err)
	}
}
