package agents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSyncForceRepairsModeAndSymlink(t *testing.T) {
	repo := t.TempDir()
	opts := CompileOptions{RepoRoot: repo, ProjectName: "demo"}
	if _, err := DeployTargets(repo, opts, []string{"claude"}); err != nil {
		t.Fatal(err)
	}
	agentsPath := filepath.Join(repo, "AGENTS.md")
	claudePath := filepath.Join(repo, "CLAUDE.md")
	if err := os.Chmod(agentsPath, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(claudePath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "other.md"), []byte("other"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("other.md", claudePath); err != nil {
		t.Fatal(err)
	}

	force := opts
	force.Force = true
	res, err := DeployTargets(repo, force, []string{"claude"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Forced || res.Written == 0 {
		t.Fatalf("force result = %+v, want forced writes", res)
	}
	info, err := os.Stat(agentsPath)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o644 {
		t.Fatalf("AGENTS.md mode = %o, want 644", perm)
	}
	target, err := os.Readlink(claudePath)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Clean(target) != "AGENTS.md" {
		t.Fatalf("CLAUDE.md -> %q, want AGENTS.md", target)
	}
}

func TestSyncForceNeverReplacesForeignCursor(t *testing.T) {
	repo := t.TempDir()
	dir := filepath.Join(repo, ".cursor", "rules")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	foreignPath := filepath.Join(dir, "000-sila-core.mdc")
	foreign := []byte("user-owned cursor rule\n")
	if err := os.WriteFile(foreignPath, foreign, 0o644); err != nil {
		t.Fatal(err)
	}

	opts := CompileOptions{RepoRoot: repo, ProjectName: "demo", Force: true}
	res, err := DeployTargets(repo, opts, []string{"cursor"})
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(foreignPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(foreign) {
		t.Fatalf("force replaced a foreign Cursor file without --replace:\n%s", after)
	}
	found := false
	for _, p := range res.Plan {
		if p.Path == ".cursor/rules/000-sila-core.mdc" && p.Action == "skip" {
			found = true
			if !strings.Contains(p.SkipReason, "--replace") {
				t.Fatalf("skip reason = %q", p.SkipReason)
			}
		}
	}
	if !found {
		t.Fatalf("foreign Cursor file was not reported as skipped: %+v", res.Plan)
	}
}

func TestSyncForceDryRunWritesNothing(t *testing.T) {
	repo := t.TempDir()
	opts := CompileOptions{RepoRoot: repo, ProjectName: "demo"}
	if _, err := DeployTargets(repo, opts, []string{"claude"}); err != nil {
		t.Fatal(err)
	}
	agentsPath := filepath.Join(repo, "AGENTS.md")
	if err := os.Chmod(agentsPath, 0o600); err != nil {
		t.Fatal(err)
	}
	preview := opts
	preview.Force = true
	preview.Preview = true
	res, err := DeployTargets(repo, preview, []string{"claude"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.FilesWritten) != 0 || len(res.Symlinks) != 0 {
		t.Fatalf("dry-run wrote: %+v", res)
	}
	info, _ := os.Stat(agentsPath)
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("dry-run changed mode to %o", perm)
	}
}

func TestSyncChangedOnlyCountsAndSummary(t *testing.T) {
	repo := t.TempDir()
	opts := CompileOptions{RepoRoot: repo, ProjectName: "demo"}
	first, err := DeployTargets(repo, opts, []string{"claude"})
	if err != nil {
		t.Fatal(err)
	}
	if first.Written == 0 || !first.ChangedOnly {
		t.Fatalf("first sync = %+v", first)
	}
	second, err := DeployTargets(repo, opts, []string{"claude"})
	if err != nil {
		t.Fatal(err)
	}
	if second.Written != 0 || second.Skipped == 0 || second.Failed != 0 {
		t.Fatalf("second sync was not idle: %+v", second)
	}
	if second.ChangedOnly != true || second.Forced {
		t.Fatalf("changed-only flags = %+v", second)
	}
}
