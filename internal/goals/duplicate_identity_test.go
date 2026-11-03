package goals

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestComputeGlobalStatsSeparatesAliasesFromLookalikes(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "repo")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(dir, "repo-alias")
	if err := os.Symlink(real, alias); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(dir, "other")
	other2 := filepath.Join(dir, "other2")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(other2, 0o755); err != nil {
		t.Fatal(err)
	}

	projects := map[string]*AuditResult{
		"repo":       {Project: "repo", ProjectDir: real, Total: 1, Goals: []Goal{{Slug: "shared-one"}}},
		"repo-alias": {Project: "repo-alias", ProjectDir: alias, Total: 1, Goals: []Goal{{Slug: "shared-one"}}},
		"other":      {Project: "other", ProjectDir: other, Total: 1, Goals: []Goal{{Slug: "shared-two"}}},
		"other2":     {Project: "other2", ProjectDir: other2, Total: 1, Goals: []Goal{{Slug: "shared-two"}}},
	}
	stats := computeGlobalStats(projects, time.Now())

	if len(stats.DuplicateRegistrations) != 1 {
		t.Fatalf("duplicates = %d, want 1 (the symlinked pair)", len(stats.DuplicateRegistrations))
	}
	dup := stats.DuplicateRegistrations[0]
	if dup.Project != "repo" || dup.Other != "repo-alias" || dup.Overlap != 1 {
		t.Fatalf("duplicate = %+v, want repo ↔ repo-alias via a real directory", dup)
	}

	if len(stats.LookalikeRegistrations) != 1 {
		t.Fatalf("lookalikes = %d, want 1 (the shared-slug pair)", len(stats.LookalikeRegistrations))
	}
	look := stats.LookalikeRegistrations[0]
	if look.Project != "other" || look.Other != "other2" {
		t.Fatalf("lookalike = %+v, want other ↔ other2", look)
	}
	if len(look.SharedGoals) != 1 || look.SharedGoals[0] != "shared-two" {
		t.Fatalf("shared goals = %v, want [shared-two]", look.SharedGoals)
	}
}
