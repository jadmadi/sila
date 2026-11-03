package agents

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFilterValidProjects(t *testing.T) {
	tempDir := t.TempDir()

	// 1. Repo with .git
	gitRepo := filepath.Join(tempDir, "repo-git")
	if err := os.MkdirAll(filepath.Join(gitRepo, ".git"), 0755); err != nil {
		t.Fatal(err)
	}

	// 2. Project with AGENTS.md
	agentsRepo := filepath.Join(tempDir, "repo-agents")
	if err := os.MkdirAll(agentsRepo, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentsRepo, "AGENTS.md"), []byte("# Rules\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// 3. Regular folder without .git or AGENTS.md (e.g. scratch dir)
	scratchDir := filepath.Join(tempDir, "scratch")
	if err := os.MkdirAll(scratchDir, 0755); err != nil {
		t.Fatal(err)
	}

	// 4. Non-existent path
	nonExistent := filepath.Join(tempDir, "does-not-exist")

	// 5. Symlink to gitRepo
	symlinkRepo := filepath.Join(tempDir, "repo-symlink")
	if err := os.Symlink(gitRepo, symlinkRepo); err != nil {
		t.Logf("symlink not supported on this platform/filesystem: %v", err)
	}

	candidates := []CandidateProject{
		{ID: "git", Path: gitRepo},
		{ID: "agents", Path: agentsRepo},
		{ID: "scratch", Path: scratchDir},
		{ID: "missing", Path: nonExistent},
		{ID: "root", Path: "/"},
		{ID: "empty", Path: ""},
	}
	if _, err := os.Stat(symlinkRepo); err == nil {
		candidates = append(candidates, CandidateProject{ID: "symlink", Path: symlinkRepo})
	}

	valid := FilterValidProjects(candidates)

	// Should include git and agents (and symlink should be de-duplicated against git)
	if len(valid) != 2 {
		t.Fatalf("expected 2 valid projects, got %d: %+v", len(valid), valid)
	}

	foundGit := false
	foundAgents := false
	for _, v := range valid {
		if v.ID == "git" {
			foundGit = true
		}
		if v.ID == "agents" {
			foundAgents = true
		}
	}
	if !foundGit || !foundAgents {
		t.Fatalf("missing expected project: foundGit=%v foundAgents=%v", foundGit, foundAgents)
	}
}

func TestSyncAndCheckAllProjects(t *testing.T) {
	tempDir := t.TempDir()

	repo1 := filepath.Join(tempDir, "proj1")
	if err := os.MkdirAll(filepath.Join(repo1, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	repo2 := filepath.Join(tempDir, "proj2")
	if err := os.MkdirAll(filepath.Join(repo2, ".git"), 0755); err != nil {
		t.Fatal(err)
	}

	projects := []CandidateProject{
		{ID: "proj1", Path: repo1},
		{ID: "proj2", Path: repo2},
	}

	// 1. Dry run preview
	previewResult := SyncAllProjects(projects, CompileOptions{Preview: true})
	if !previewResult.Preview {
		t.Errorf("expected Preview: true")
	}
	if previewResult.Total != 2 {
		t.Errorf("expected Total 2, got %d", previewResult.Total)
	}
	if previewResult.Succeeded != 2 {
		t.Errorf("expected Succeeded 2, got %d", previewResult.Succeeded)
	}
	// Assert no files written
	if _, err := os.Stat(filepath.Join(repo1, "AGENTS.md")); err == nil {
		t.Errorf("dry-run wrote AGENTS.md in repo1")
	}

	// 2. Real sync execution
	syncResult := SyncAllProjects(projects, CompileOptions{Preview: false})
	if syncResult.Succeeded != 2 {
		t.Fatalf("expected Succeeded 2, got %d (failed: %d)", syncResult.Succeeded, syncResult.Failed)
	}
	if _, err := os.Stat(filepath.Join(repo1, "AGENTS.md")); err != nil {
		t.Fatalf("AGENTS.md not created in repo1: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo2, "AGENTS.md")); err != nil {
		t.Fatalf("AGENTS.md not created in repo2: %v", err)
	}

	// 3. Drift check
	checkResult := CheckAllProjects(projects, CompileOptions{})
	if checkResult.InSync != 2 {
		t.Fatalf("expected InSync 2, got in_sync=%d, drifted=%d, failed=%d",
			checkResult.InSync, checkResult.Drifted, checkResult.Failed)
	}

	// 4. Introduce drift in repo1
	if err := os.WriteFile(filepath.Join(repo1, "AGENTS.md"), []byte("# Mutated\n"), 0644); err != nil {
		t.Fatal(err)
	}
	checkResult2 := CheckAllProjects(projects, CompileOptions{})
	if checkResult2.Drifted != 1 || checkResult2.InSync != 1 {
		t.Fatalf("expected 1 drifted and 1 in-sync, got drifted=%d, in_sync=%d",
			checkResult2.Drifted, checkResult2.InSync)
	}
}
