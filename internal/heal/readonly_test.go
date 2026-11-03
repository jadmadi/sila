package heal

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jadmadi/sila/internal/store"
)

func TestBuildPlan_WithoutStore(t *testing.T) {
	tmp := t.TempDir()
	repoDir := filepath.Join(tmp, "repo-no-store")
	if err := os.MkdirAll(filepath.Join(repoDir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}

	plan, err := BuildPlan(nil, repoDir)
	if err != nil {
		t.Fatalf("BuildPlan failed with nil store: %v", err)
	}

	if plan.RepoRoot != repoDir {
		t.Errorf("expected RepoRoot=%q, got %q", repoDir, plan.RepoRoot)
	}
	if plan.Automatic == 0 && plan.Judgment == 0 {
		t.Errorf("expected plan to have findings for unwired repo")
	}

	// Verify automatic steps name affected files
	for _, s := range plan.Steps {
		if s.Automatic && s.ID == "provision" {
			if len(s.Files) == 0 {
				t.Errorf("expected provision step to name affected files")
			}
		}
	}
}

func TestBuildPlan_WithReadOnlyStore(t *testing.T) {
	tmp := t.TempDir()
	storeDir := filepath.Join(tmp, "store-dir")
	dbPath := filepath.Join(storeDir, "test.db")
	repoDir := filepath.Join(tmp, "repo-ro-store")
	if err := os.MkdirAll(filepath.Join(repoDir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.EnsureProject(repoDir)
	if err != nil {
		t.Fatal(err)
	}
	_ = st.Close()

	// Chmod dir 0555 and file 0444
	if err := os.Chmod(dbPath, 0444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(storeDir, 0555); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.Chmod(storeDir, 0755)
		_ = os.Chmod(dbPath, 0644)
	}()

	roSt, exists, err := store.OpenInspection(dbPath)
	if err != nil {
		t.Fatalf("OpenInspection failed: %v", err)
	}
	if !exists || roSt == nil {
		t.Fatalf("expected store to exist")
	}
	defer roSt.Close()

	plan, err := BuildPlan(roSt, repoDir)
	if err != nil {
		t.Fatalf("BuildPlan failed with read-only store: %v", err)
	}

	for _, s := range plan.Steps {
		if s.Automatic && s.ID == "provision" {
			if len(s.Files) == 0 {
				t.Errorf("expected provision step to name affected files")
			}
		}
	}
}

// TestBuildPlanReadOnlyStoreDoesNotRegister pins the write boundary: healing a
// repository registers it, but an inspection handle (dry runs, salam) must
// never write a projects row.
func TestBuildPlanReadOnlyStoreDoesNotRegister(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "readonly.db")
	repoDir := filepath.Join(tmp, "repo-unregistered")
	if err := os.MkdirAll(filepath.Join(repoDir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	roSt, exists, err := store.OpenInspection(dbPath)
	if err != nil || !exists || roSt == nil {
		t.Fatalf("OpenInspection failed: exists=%v err=%v", exists, err)
	}
	defer roSt.Close()

	plan, err := BuildPlan(roSt, repoDir)
	if err != nil {
		t.Fatalf("BuildPlan failed: %v", err)
	}
	if plan.Project != store.ProjectID(repoDir) {
		t.Errorf("read-only plan project = %q, want derived %q", plan.Project, store.ProjectID(repoDir))
	}
	var projects int
	if err := roSt.DB.QueryRow(`SELECT COUNT(*) FROM projects`).Scan(&projects); err != nil {
		t.Fatal(err)
	}
	if projects != 0 {
		t.Errorf("read-only heal wrote %d project rows", projects)
	}
}
