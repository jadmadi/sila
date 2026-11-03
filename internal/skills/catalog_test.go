package skills

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCatalog_SymlinkDiscoveryAndSearch(t *testing.T) {
	tempDir := t.TempDir()
	globalDir := filepath.Join(tempDir, "global_vault")
	projectDir := filepath.Join(tempDir, "my_project")
	pSkillsDir := filepath.Join(projectDir, ".agents", "skills")

	_ = os.MkdirAll(globalDir, 0755)
	_ = os.MkdirAll(pSkillsDir, 0755)

	t.Setenv("SILA_GLOBAL_SKILLS_DIR", globalDir)

	// Create real skill in external directory
	extDir := filepath.Join(tempDir, "external_skills", "symlinked-skill")
	_ = os.MkdirAll(extDir, 0755)
	_ = os.WriteFile(filepath.Join(extDir, "SKILL.md"), []byte(`---
name: symlinked-skill
description: A symlinked skill bundle
tags:
  - symlink
  - test
---
# Symlink Skill Instructions
`), 0644)

	// Symlink into project skills
	symlinkPath := filepath.Join(pSkillsDir, "symlinked-skill")
	if err := os.Symlink(extDir, symlinkPath); err != nil {
		t.Fatal(err)
	}

	// Create regular skill in global vault
	gSkillDir := filepath.Join(globalDir, "global-rare-skill")
	_ = os.MkdirAll(gSkillDir, 0755)
	_ = os.WriteFile(filepath.Join(gSkillDir, "SKILL.md"), []byte(`---
name: global-rare-skill
description: A rare global skill for performance audits
tags:
  - performance
---
# Global Rare Skill Instructions
`), 0644)

	// Test SearchCatalog
	results, err := SearchCatalog(projectDir, "symlink", nil, 10)
	if err != nil {
		t.Fatalf("SearchCatalog error: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected to find symlinked-skill, got 0 results")
	}
	if results[0].Slug != "symlinked-skill" {
		t.Errorf("got slug %q, want %q", results[0].Slug, "symlinked-skill")
	}
	if results[0].Source != "project" {
		t.Errorf("got source %q, want 'project'", results[0].Source)
	}

	// Test SearchCatalog for global
	gResults, err := SearchCatalog(projectDir, "performance", nil, 10)
	if err != nil {
		t.Fatalf("SearchCatalog error: %v", err)
	}
	if len(gResults) == 0 {
		t.Fatal("expected to find global-rare-skill, got 0 results")
	}
	if gResults[0].Slug != "global-rare-skill" {
		t.Errorf("got slug %q, want %q", gResults[0].Slug, "global-rare-skill")
	}
	if gResults[0].Source != "global" {
		t.Errorf("got source %q, want 'global'", gResults[0].Source)
	}

	// Test LoadSkill
	loaded, err := LoadSkill(projectDir, "symlinked-skill", false)
	if err != nil {
		t.Fatalf("LoadSkill error: %v", err)
	}
	if loaded.Slug != "symlinked-skill" {
		t.Errorf("loaded slug %q, want 'symlinked-skill'", loaded.Slug)
	}
	if loaded.ContentHash == "" {
		t.Error("expected non-empty ContentHash")
	}
}
