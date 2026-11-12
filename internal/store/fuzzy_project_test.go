package store

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveProjectFuzzy(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "fuzzy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// Register projects
	for _, p := range []struct {
		id   string
		path string
	}{
		{"sila-cli", "/mnt/projects/sila/cli"},
		{"sila-worker", "/mnt/projects/sila/worker"},
		{"research", "/mnt/projects/sila/research"},
		{"marof-app", "/mnt/projects/marof/frontend"},
	} {
		if _, err := s.DB.Exec(`INSERT INTO projects(path, id, created_at) VALUES(?, ?, ?)`, p.path, p.id, "2026-09-01T00:00:00Z"); err != nil {
			t.Fatal(err)
		}
	}

	// 1. Exact ID match
	p, err := s.ResolveProjectFuzzy("sila-cli")
	if err != nil || p == nil || p.ID != "sila-cli" {
		t.Fatalf("expected exact match sila-cli, got %v, err=%v", p, err)
	}

	// 2. Exact Path match
	p, err = s.ResolveProjectFuzzy("/mnt/projects/sila/worker")
	if err != nil || p == nil || p.ID != "sila-worker" {
		t.Fatalf("expected path match sila-worker, got %v, err=%v", p, err)
	}

	// 3. Prefix match on basename
	p, err = s.ResolveProjectFuzzy("front")
	if err != nil || p == nil || p.ID != "marof-app" {
		t.Fatalf("expected prefix match on basename frontend -> marof-app, got %v, err=%v", p, err)
	}

	// 4. Prefix match on ID (unique prefix)
	p, err = s.ResolveProjectFuzzy("marof")
	if err != nil || p == nil || p.ID != "marof-app" {
		t.Fatalf("expected prefix match on ID marof -> marof-app, got %v, err=%v", p, err)
	}

	// 5. Substring match (unique)
	p, err = s.ResolveProjectFuzzy("resea")
	if err != nil || p == nil || p.ID != "research" {
		t.Fatalf("expected substring match resea -> research, got %v, err=%v", p, err)
	}

	// 6. Ambiguous prefix
	_, err = s.ResolveProjectFuzzy("sila")
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("expected ambiguous error for prefix 'sila', got err=%v", err)
	}

	// 7. Non-matching alias
	_, err = s.ResolveProjectFuzzy("nonexistent")
	if err == nil || !strings.Contains(err.Error(), "no registered project matches") {
		t.Fatalf("expected not found error, got err=%v", err)
	}

	// 8. Empty alias
	_, err = s.ResolveProjectFuzzy("   ")
	if err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("expected required error for empty alias, got err=%v", err)
	}
}
