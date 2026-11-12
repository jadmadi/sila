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
		{"example-cli", "/projects/example/cli"},
		{"example-worker", "/projects/example/worker"},
		{"example-research", "/projects/example/research"},
		{"example-front", "/projects/example/frontend"},
	} {
		if _, err := s.DB.Exec(`INSERT INTO projects(path, id, created_at) VALUES(?, ?, ?)`, p.path, p.id, "2026-09-01T00:00:00Z"); err != nil {
			t.Fatal(err)
		}
	}

	// 1. Exact ID match
	p, err := s.ResolveProjectFuzzy("example-cli")
	if err != nil || p == nil || p.ID != "example-cli" {
		t.Fatalf("expected exact match example-cli, got %v, err=%v", p, err)
	}

	// 2. Exact Path match
	p, err = s.ResolveProjectFuzzy("/projects/example/worker")
	if err != nil || p == nil || p.ID != "example-worker" {
		t.Fatalf("expected path match example-worker, got %v, err=%v", p, err)
	}

	// 3. Prefix match on basename
	p, err = s.ResolveProjectFuzzy("front")
	if err != nil || p == nil || p.ID != "example-front" {
		t.Fatalf("expected prefix match on basename frontend -> example-front, got %v, err=%v", p, err)
	}

	// 4. Prefix match on ID (unique prefix)
	p, err = s.ResolveProjectFuzzy("example-f")
	if err != nil || p == nil || p.ID != "example-front" {
		t.Fatalf("expected prefix match on ID example-f -> example-front, got %v, err=%v", p, err)
	}

	// 5. Substring match (unique)
	p, err = s.ResolveProjectFuzzy("resea")
	if err != nil || p == nil || p.ID != "example-research" {
		t.Fatalf("expected substring match resea -> example-research, got %v, err=%v", p, err)
	}

	// 6. Ambiguous prefix
	_, err = s.ResolveProjectFuzzy("example")
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("expected ambiguous error for prefix 'example', got err=%v", err)
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
