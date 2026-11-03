package gate

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/store"
)

func TestExtractDeletedPaths(t *testing.T) {
	output := `M	cmd/sila/main.go
D	internal/gate/old_sentry.go
R100	internal/old/name.go	internal/new/name.go
A	internal/gate/new_sentry.go
`
	deleted := ExtractDeletedPaths(output)
	if len(deleted) != 2 {
		t.Fatalf("expected 2 deleted/renamed paths, got %d: %+v", len(deleted), deleted)
	}
	if deleted[0] != "internal/gate/old_sentry.go" {
		t.Errorf("expected deleted[0] == 'internal/gate/old_sentry.go', got %q", deleted[0])
	}
	if deleted[1] != "internal/old/name.go" {
		t.Errorf("expected deleted[1] == 'internal/old/name.go', got %q", deleted[1])
	}
}

func TestCheckFactGrounding_WarnsOnActiveFactFileDeletion(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "sila.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// Insert an active grounded fact referencing internal/core/engine.go
	f := store.Fact{
		ID:            "fact_engine_invariant",
		ProjectID:     "cli",
		Claim:         "Core engine must remain strictly zero-allocation on hot paths.",
		Kind:          "invariant",
		Confidence:    "invariant",
		FileRefsExist: true,
		RepoFiles:     []string{"internal/core/engine.go"},
	}
	if err := st.UpsertFact(f); err != nil {
		t.Fatal(err)
	}

	// Case 1: deleted paths includes internal/core/engine.go
	res := CheckFactGrounding(st, []string{"internal/core/engine.go"})
	if len(res.Warnings) != 1 {
		t.Fatalf("expected 1 warning for deleted referenced file, got %d", len(res.Warnings))
	}
	warn := res.Warnings[0]
	if warn.FactID != "fact_engine_invariant" {
		t.Errorf("expected fact ID 'fact_engine_invariant', got %q", warn.FactID)
	}
	if !strings.Contains(warn.Message, "Core engine must remain strictly zero-allocation") {
		t.Errorf("expected message to contain claim: %s", warn.Message)
	}

	// Case 2: unrelated file deleted -> 0 warnings
	resUnrelated := CheckFactGrounding(st, []string{"docs/unrelated.md"})
	if len(resUnrelated.Warnings) != 0 {
		t.Fatalf("expected 0 warnings for unrelated file, got %d", len(resUnrelated.Warnings))
	}
}
