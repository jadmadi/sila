package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jadmadi/sila/internal/store"
)

func TestWorkspaceStoreScope(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".sila"), 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(root, ".sila", "sila.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	got, ok := workspaceStoreScope(st, "", true)
	if !ok || filepath.Clean(got) != filepath.Clean(root) {
		t.Fatalf("scope = %q, ok = %v", got, ok)
	}
	got, ok = workspaceStoreScope(st, "foreign-project", false)
	if !ok || filepath.Clean(got) != filepath.Clean(root) {
		t.Fatalf("scope override = %q, ok = %v", got, ok)
	}
}
