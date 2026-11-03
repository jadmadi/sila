package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestStoreGenericQueryRowsAndQueryOne(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_query.db")
	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer st.Close()

	ctx := context.Background()

	// Ensure project
	p1, err := st.EnsureProject("/workspace/p1")
	if err != nil {
		t.Fatal(err)
	}
	p2, err := st.EnsureProject("/workspace/p2")
	if err != nil {
		t.Fatal(err)
	}

	// 1. Test QueryRows with Project struct
	projects, err := st.QueryRows(ctx, `SELECT path, id FROM projects ORDER BY path ASC`, func(rows *sql.Rows) (Project, error) {
		var p Project
		err := rows.Scan(&p.Path, &p.ID)
		return p, err
	})
	if err != nil {
		t.Fatalf("QueryRows failed: %v", err)
	}
	if len(projects) != 2 {
		t.Fatalf("expected 2 projects, got %d", len(projects))
	}
	if projects[0].ID != p1.ID || projects[1].ID != p2.ID {
		t.Fatalf("unexpected projects: %+v", projects)
	}

	// 2. Test QueryOne with existing row
	proj, found, err := st.QueryOne(ctx, `SELECT path, id FROM projects WHERE id = ?`, func(row *sql.Row) (Project, error) {
		var p Project
		err := row.Scan(&p.Path, &p.ID)
		return p, err
	}, p1.ID)
	if err != nil {
		t.Fatalf("QueryOne failed: %v", err)
	}
	if !found {
		t.Fatal("expected found=true, got false")
	}
	if proj.ID != p1.ID {
		t.Fatalf("expected proj.ID=%s, got %s", p1.ID, proj.ID)
	}

	// 3. Test QueryOne with non-existent row (sql.ErrNoRows)
	nonExistent, found, err := st.QueryOne(ctx, `SELECT path, id FROM projects WHERE id = ?`, func(row *sql.Row) (Project, error) {
		var p Project
		err := row.Scan(&p.Path, &p.ID)
		return p, err
	}, "non-existent-id")
	if err != nil {
		t.Fatalf("QueryOne on non-existent failed: %v", err)
	}
	if found {
		t.Fatalf("expected found=false, got true with %+v", nonExistent)
	}
}
