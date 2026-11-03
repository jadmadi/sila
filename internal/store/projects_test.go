package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/config"
)

func TestResolveScope_CustomIDDiffersFromBasename(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	repoDir := filepath.Join(t.TempDir(), "real-repo")
	if err := os.MkdirAll(filepath.Join(repoDir, "sub", "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Register with ID that differs from basename "real-repo"
	if _, err := s.DB.Exec(`INSERT INTO projects (id, path, created_at) VALUES (?, ?, datetime('now'))`, "custom-service", repoDir); err != nil {
		t.Fatal(err)
	}

	// 1. Resolve for repo root
	p1, err := s.ResolveScope("", repoDir)
	if err != nil {
		t.Fatalf("ResolveScope failed: %v", err)
	}
	if p1.ID != "custom-service" {
		t.Errorf("expected ID custom-service, got %q", p1.ID)
	}

	// 2. Resolve for subdirectory inside repo
	p2, err := s.ResolveScope("", filepath.Join(repoDir, "sub", "pkg"))
	if err != nil {
		t.Fatalf("ResolveScope for subdir failed: %v", err)
	}
	if p2.ID != "custom-service" {
		t.Errorf("expected ID custom-service for subdir, got %q", p2.ID)
	}
}

func TestResolveScope_CollidingBasenames(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	baseA := filepath.Join(t.TempDir(), "groupA", "widget")
	baseB := filepath.Join(t.TempDir(), "groupB", "widget")
	if err := os.MkdirAll(baseA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(baseB, 0o755); err != nil {
		t.Fatal(err)
	}

	// Register groupA/widget as widget-a
	if _, err := s.DB.Exec(`INSERT INTO projects (id, path, created_at) VALUES (?, ?, datetime('now'))`, "widget-a", baseA); err != nil {
		t.Fatal(err)
	}

	// groupB/widget is unregistered: must NOT match widget-a simply because folder name is "widget"
	pB, err := s.ResolveScope("", baseB)
	if err != nil {
		t.Fatalf("ResolveScope for unregistered colliding basename failed: %v", err)
	}
	if pB.ID == "widget-a" {
		t.Errorf("unregistered repo %q must not adopt unrelated registered project %q with same basename", baseB, pB.ID)
	}
	if pB.ID != "widget" {
		t.Errorf("expected derived ID widget, got %q", pB.ID)
	}

	// groupA/widget must resolve to widget-a
	pA, err := s.ResolveScope("", baseA)
	if err != nil {
		t.Fatalf("ResolveScope for groupA failed: %v", err)
	}
	if pA.ID != "widget-a" {
		t.Errorf("expected ID widget-a, got %q", pA.ID)
	}
}

func TestResolveScope_SiblingCollisionDisambiguation(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	dirA := filepath.Join(t.TempDir(), "repoA", "service")
	dirB := filepath.Join(t.TempDir(), "repoB", "service")
	if err := os.MkdirAll(dirA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dirB, 0o755); err != nil {
		t.Fatal(err)
	}

	// Register dirA with the pure slug ID "service"
	if _, err := s.DB.Exec(`INSERT INTO projects (id, path, created_at) VALUES (?, ?, datetime('now'))`, "service", dirA); err != nil {
		t.Fatal(err)
	}

	// dirB is unregistered and shares basename "service": must NOT return "service" because dirA owns it!
	pB, err := s.ResolveScope("", dirB)
	if err != nil {
		t.Fatalf("ResolveScope for dirB failed: %v", err)
	}
	if pB.ID == "service" {
		t.Fatalf("dirB must not receive collided ID 'service' owned by dirA")
	}
	if !strings.HasPrefix(pB.ID, "service-") {
		t.Errorf("expected disambiguated ID prefixed with 'service-', got %q", pB.ID)
	}

	// Now configure a mock git remote on dirB
	gitDirB := filepath.Join(dirB, ".git")
	if err := os.MkdirAll(gitDirB, 0o755); err != nil {
		t.Fatal(err)
	}
	gitConfig := `[core]
	repositoryformatversion = 0
[remote "origin"]
	url = git@github.com:example-org/service-repo.git
	fetch = +refs/heads/*:refs/remotes/origin/*
`
	if err := os.WriteFile(filepath.Join(gitDirB, "config"), []byte(gitConfig), 0o644); err != nil {
		t.Fatal(err)
	}

	pBRemote, err := s.ResolveScope("", dirB)
	if err != nil {
		t.Fatalf("ResolveScope with remote failed: %v", err)
	}
	expectedHash := RemoteHash("git@github.com:example-org/service-repo.git")
	expectedID := "service-" + expectedHash
	if pBRemote.ID != expectedID {
		t.Errorf("expected remote hash disambiguated ID %q, got %q", expectedID, pBRemote.ID)
	}
}

func TestResolveScope_ExplicitSelectors(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	repoDir := filepath.Join(t.TempDir(), "my-project")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`INSERT INTO projects (id, path, created_at) VALUES (?, ?, datetime('now'))`, "my-proj-id", repoDir); err != nil {
		t.Fatal(err)
	}

	// 1. Exact ID
	p1, err := s.ResolveScope("my-proj-id", "")
	if err != nil {
		t.Fatalf("ResolveScope exact ID failed: %v", err)
	}
	if p1.ID != "my-proj-id" {
		t.Errorf("expected my-proj-id, got %q", p1.ID)
	}

	// 2. Exact Path
	p2, err := s.ResolveScope(repoDir, "")
	if err != nil {
		t.Fatalf("ResolveScope exact path failed: %v", err)
	}
	if p2.ID != "my-proj-id" {
		t.Errorf("expected my-proj-id, got %q", p2.ID)
	}

	// 3. Unknown selector: must return actionable error without fallback
	_, err = s.ResolveScope("nonexistent-slug", "")
	if err == nil {
		t.Fatal("expected error for nonexistent selector, got nil")
	}
	if !strings.Contains(err.Error(), "no registered project matches") {
		t.Errorf("expected 'no registered project matches' error, got %q", err.Error())
	}
}

func TestResolveScope_BareDerivedIDWithUnregisteredDir(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	dir := filepath.Join(t.TempDir(), "unregistered-repo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	derived := ProjectID(dir)

	p, err := s.ResolveScope(derived, dir)
	if err != nil {
		t.Fatalf("ResolveScope with derived ID and unregistered dir failed: %v", err)
	}
	if p.ID != derived {
		t.Errorf("expected ID %q, got %q", derived, p.ID)
	}
	if !samePath(p.Path, dir) {
		t.Errorf("expected path %q, got %q", dir, p.Path)
	}

	// A different slug with the same dir must still be rejected.
	if _, err := s.ResolveScope("other-project", dir); err == nil {
		t.Fatal("expected error for non-matching slug with dir")
	}
	// A bare derived ID without a dir must still be rejected while unregistered.
	if _, err := s.ResolveScope(derived, ""); err == nil {
		t.Fatal("expected error for unregistered derived ID without dir")
	}
}

func TestResolveScope_ContainingProject(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	parentDir := filepath.Join(t.TempDir(), "workspace")
	subRepo := filepath.Join(parentDir, "cli")
	if err := os.MkdirAll(subRepo, 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := s.DB.Exec(`INSERT INTO projects (id, path, created_at) VALUES (?, ?, datetime('now'))`, "workspace-root", parentDir); err != nil {
		t.Fatal(err)
	}

	p, err := s.ResolveScope("", subRepo)
	if err != nil {
		t.Fatalf("ResolveScope failed: %v", err)
	}
	if p.ID != "workspace-root" {
		t.Errorf("expected parent project workspace-root, got %q", p.ID)
	}
}

func TestResolveScope_SymlinkResolution(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	tmp := t.TempDir()
	targetDir := filepath.Join(tmp, "real-location")
	linkDir := filepath.Join(tmp, "symlink-location")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(targetDir, linkDir); err != nil {
		t.Skip("symlinks not supported on this filesystem")
	}

	if _, err := s.DB.Exec(`INSERT INTO projects (id, path, created_at) VALUES (?, ?, datetime('now'))`, "linked-proj", targetDir); err != nil {
		t.Fatal(err)
	}

	p, err := s.ResolveScope("", linkDir)
	if err != nil {
		t.Fatalf("ResolveScope for symlink failed: %v", err)
	}
	if p.ID != "linked-proj" {
		t.Errorf("expected linked-proj, got %q", p.ID)
	}
}

func TestWindowsPathNormalization(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "win_paths.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// 1. EnsureProject normalizes backslashes to forward slashes
	winPath := "C:\\Users\\Developer\\Projects\\my-service"
	p, err := s.EnsureProject(winPath)
	if err != nil {
		t.Fatalf("EnsureProject with Windows path failed: %v", err)
	}
	expectedPath := config.NormalizePath(winPath)
	if p.Path != expectedPath {
		t.Errorf("expected stored path %q, got %q", expectedPath, p.Path)
	}

	// 2. Querying with forward-slash version resolves to the same project
	forwardPath := "c:/users/developer/projects/my-service"
	pForward, err := s.EnsureProject(forwardPath)
	if err != nil {
		t.Fatalf("EnsureProject with forward-slash path failed: %v", err)
	}
	if pForward.ID != p.ID {
		t.Errorf("expected same project ID %q, got %q", p.ID, pForward.ID)
	}

	// 3. samePath recognizes mixed slash and case variants
	if !samePath("C:\\repo\\dir", "c:/repo/dir") {
		t.Errorf("samePath failed on Windows backslash vs forward slash")
	}
	if !samePath("D:\\Work\\App\\", "d:/work/app") {
		t.Errorf("samePath failed on trailing slash variant")
	}
}

func TestSameBasenameIsolation(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "isolation.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	tmp := t.TempDir()
	dirA := filepath.Join(tmp, "a", "project")
	dirB := filepath.Join(tmp, "b", "project")
	if err := os.MkdirAll(dirA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dirB, 0o755); err != nil {
		t.Fatal(err)
	}

	// 1. Unregistered same-basename isolation:
	// Record memo in dirA without prior registration
	memoA, err := s.InsertMemo("project", "DirA memo directive", "agent", dirA, nil)
	if err != nil {
		t.Fatalf("InsertMemo failed: %v", err)
	}
	if memoA == nil {
		t.Fatal("expected non-nil memo")
	}

	// Resolve scope for dirB: must NOT resolve to dirA's project ID or leak dirA's memo
	pB, err := s.ResolveScope("", dirB)
	if err != nil {
		t.Fatalf("ResolveScope for dirB failed: %v", err)
	}
	if pB.ID == "project" {
		t.Fatalf("unregistered dirB must not receive collided project ID 'project' holding dirA records")
	}

	factsB, err := s.FactsByProject(pB.ID)
	if err != nil {
		t.Fatalf("FactsByProject failed: %v", err)
	}
	if len(factsB) != 0 {
		t.Fatalf("dirB received %d facts belonging to dirA; expected 0 (cross-repo context leakage)", len(factsB))
	}

	// 2. Registered A vs Unregistered B isolation:
	regA, err := s.EnsureProject(dirA)
	if err != nil {
		t.Fatalf("EnsureProject dirA failed: %v", err)
	}
	if regA.ID != "project" && !strings.HasPrefix(regA.ID, "project") {
		t.Fatalf("unexpected regA ID: %q", regA.ID)
	}

	pBAfter, err := s.ResolveScope("", dirB)
	if err != nil {
		t.Fatalf("ResolveScope dirB after registration failed: %v", err)
	}
	if pBAfter.ID == regA.ID {
		t.Fatalf("unregistered dirB must not receive registered dirA ID %q", regA.ID)
	}
	if !strings.HasPrefix(pBAfter.ID, "project-") {
		t.Fatalf("expected disambiguated prefix 'project-', got %q", pBAfter.ID)
	}
}
