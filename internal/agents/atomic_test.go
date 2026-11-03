package agents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeployTargetsOversizedAgentsWritesNothing(t *testing.T) {
	repo := t.TempDir()
	original := "# AGENTS.md\n\n" + strings.Repeat("oversized rule line\n", 1400)
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := DeployTargets(repo, CompileOptions{RepoRoot: repo, ProjectName: "demo"}, []string{"cursor"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Atomic {
		t.Fatal("oversized plan must not be atomic")
	}
	if len(res.Errors) == 0 {
		t.Fatal("expected a size-ceiling error")
	}
	if len(res.FilesWritten) != 0 || len(res.Symlinks) != 0 {
		t.Fatalf("failed atomic project wrote files: %+v", res)
	}
	got, err := os.ReadFile(filepath.Join(repo, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != original {
		t.Fatal("oversized AGENTS.md was modified")
	}
	if _, err := os.Stat(filepath.Join(repo, ".cursor", "rules", "000-sila-core.mdc")); !os.IsNotExist(err) {
		t.Fatal("cursor projection was written despite the failed plan")
	}
}

func TestDeployTargetsRollbackOnCommitFailure(t *testing.T) {
	repo := t.TempDir()
	original := "# AGENTS.md\n\nkeep this content\n"
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	// A regular file at .cursor makes MkdirAll(.cursor/rules) fail after the
	// AGENTS.md write, exercising rollback.
	if err := os.WriteFile(filepath.Join(repo, ".cursor"), []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := DeployTargets(repo, CompileOptions{RepoRoot: repo, ProjectName: "demo"}, []string{"cursor"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Errors) == 0 || !res.RolledBack {
		t.Fatalf("expected a rolled-back commit failure: %+v", res)
	}
	if len(res.FilesWritten) != 0 {
		t.Fatalf("rolled-back project reports partial writes: %v", res.FilesWritten)
	}
	got, err := os.ReadFile(filepath.Join(repo, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != original {
		t.Fatalf("AGENTS.md was not restored:\n%s", got)
	}
}

func TestSyncAllProjectsIsolatesAtomicFailure(t *testing.T) {
	normal := t.TempDir()
	broken := t.TempDir()
	if err := os.WriteFile(filepath.Join(normal, "AGENTS.md"), []byte("# Normal\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(broken, "AGENTS.md"), []byte("# Broken\n"+strings.Repeat("x\n", 24000)), 0o644); err != nil {
		t.Fatal(err)
	}
	res := SyncAllProjects([]CandidateProject{
		{ID: "normal", Path: normal},
		{ID: "broken", Path: broken},
	}, CompileOptions{ProjectName: "demo"})
	if res.Failed != 1 || res.AtomicFailures != 1 || res.Succeeded != 1 {
		t.Fatalf("unexpected multi-sync counts: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(normal, "AGENTS.md")); err != nil {
		t.Fatalf("normal project was not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(broken, ".cursor")); !os.IsNotExist(err) {
		t.Fatal("broken project received a partial projection")
	}
}

func TestDeployTargetsIdempotentAndPreview(t *testing.T) {
	repo := t.TempDir()
	opts := CompileOptions{RepoRoot: repo, ProjectName: "demo"}
	first, err := DeployTargets(repo, opts, []string{"cursor"})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.FilesWritten) == 0 {
		t.Fatal("fresh sync wrote nothing")
	}
	second, err := DeployTargets(repo, opts, []string{"cursor"})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.FilesWritten) != 0 || len(second.Errors) != 0 {
		t.Fatalf("second sync was not idle: %+v", second)
	}
	preview, err := DeployTargets(repo, CompileOptions{RepoRoot: repo, ProjectName: "demo", Preview: true}, []string{"cursor"})
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.FilesWritten) != 0 {
		t.Fatalf("dry-run wrote files: %v", preview.FilesWritten)
	}
}
