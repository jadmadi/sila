package assess

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAssessProjectSkills_ZeroWritesAndGrounding(t *testing.T) {
	tempDir := t.TempDir()
	repoDir := filepath.Join(tempDir, "sample-repo")
	globalDir := filepath.Join(tempDir, "global-vault")

	_ = os.MkdirAll(repoDir, 0755)
	_ = os.MkdirAll(globalDir, 0755)

	t.Setenv("SILA_GLOBAL_SKILLS_DIR", globalDir)

	// Create manifests in repo
	_ = os.WriteFile(filepath.Join(repoDir, "go.mod"), []byte("module sample\ngo 1.27\n"), 0644)
	_ = os.WriteFile(filepath.Join(repoDir, "wrangler.jsonc"), []byte(`{"name":"edge"}`), 0644)
	_ = os.WriteFile(filepath.Join(repoDir, "AGENTS.md"), []byte("# Rules\n- Pure Go only\n"), 0644)

	// Create skills in global vault
	createMockSkill(t, globalDir, "golang-testing", "Production-ready Go tests", []string{"go", "testing"})
	createMockSkill(t, globalDir, "cf-cpu-audit", "Scan Cloudflare Workers for CPU limits", []string{"cloudflare", "workers"})
	createMockSkill(t, globalDir, "python-data", "Python data science tools", []string{"python"})

	// Record mtimes and files before
	beforeEntries, _ := os.ReadDir(repoDir)

	report, err := AssessProjectSkills(context.Background(), nil, SkillAssessOptions{
		ProjectDir: repoDir,
	})
	if err != nil {
		t.Fatalf("AssessProjectSkills failed: %v", err)
	}

	// Verify grounding
	if len(report.Grounding.TechStack) < 2 {
		t.Errorf("expected at least 2 tech stack items, got %v", report.Grounding.TechStack)
	}
	if !report.Grounding.RulesPresent {
		t.Error("expected RulesPresent to be true")
	}

	// Verify recommendations: golang-testing and cf-cpu-audit should be recommended, python-data should not
	foundGo := false
	foundCF := false
	for _, r := range report.Recommended {
		if r.Slug == "golang-testing" {
			foundGo = true
		}
		if r.Slug == "cf-cpu-audit" {
			foundCF = true
		}
		if r.Slug == "python-data" {
			t.Errorf("python-data should NOT be recommended for a Go+CF repo")
		}
	}
	if !foundGo {
		t.Error("expected golang-testing to be recommended based on go.mod")
	}
	if !foundCF {
		t.Error("expected cf-cpu-audit to be recommended based on wrangler.jsonc")
	}

	// Verify cost accounting
	if report.CostAccounting.UnfilteredTokens <= 0 {
		t.Errorf("expected positive UnfilteredTokens, got %d", report.CostAccounting.UnfilteredTokens)
	}
	if report.CostAccounting.SelectedTokens <= 0 {
		t.Errorf("expected positive SelectedTokens, got %d", report.CostAccounting.SelectedTokens)
	}

	// Verify TOON format
	toon := RenderSkillAssessmentTOON(report)
	if !strings.Contains(toon, "skill_assessment_summary:") {
		t.Errorf("TOON missing header: %s", toon)
	}
	if !strings.Contains(toon, "golang-testing") {
		t.Errorf("TOON missing recommended skill: %s", toon)
	}

	// Verify zero files created in repoDir
	afterEntries, _ := os.ReadDir(repoDir)
	if len(beforeEntries) != len(afterEntries) {
		t.Errorf("file count changed: before %d, after %d", len(beforeEntries), len(afterEntries))
	}
}

func TestAssessProjectSkills_PolicyPinsAndMissingRequired(t *testing.T) {
	tempDir := t.TempDir()
	repoDir := filepath.Join(tempDir, "policy-repo")
	globalDir := filepath.Join(tempDir, "global-vault")

	_ = os.MkdirAll(repoDir, 0755)
	_ = os.MkdirAll(globalDir, 0755)

	t.Setenv("SILA_GLOBAL_SKILLS_DIR", globalDir)

	createMockSkill(t, globalDir, "golang-testing", "Go tests", nil)
	createMockSkill(t, globalDir, "cf-cpu-audit", "CF CPU audit", nil)

	// Create .sila/config.json with pinned, required, and exclude
	silaDir := filepath.Join(repoDir, ".sila")
	_ = os.MkdirAll(silaDir, 0755)
	configJSON := `{
  "skills": {
    "pinned": ["golang-testing"],
    "required": ["custom-nonexistent-skill"],
    "exclude": ["cf-cpu-audit"]
  }
}`
	_ = os.WriteFile(filepath.Join(silaDir, "config.json"), []byte(configJSON), 0644)

	report, err := AssessProjectSkills(context.Background(), nil, SkillAssessOptions{
		ProjectDir: repoDir,
	})
	if err != nil {
		t.Fatalf("AssessProjectSkills failed: %v", err)
	}

	// Verify pinned
	foundPinned := false
	for _, r := range report.Recommended {
		if r.Slug == "golang-testing" {
			foundPinned = true
			if !r.Pinned {
				t.Errorf("expected golang-testing to be marked pinned")
			}
		}
		if r.Slug == "cf-cpu-audit" {
			t.Errorf("cf-cpu-audit should be excluded by policy")
		}
	}
	if !foundPinned {
		t.Error("expected golang-testing to be recommended as pinned")
	}

	// Verify missing pin
	if len(report.MissingPins) == 0 {
		t.Fatal("expected missing pin report for custom-nonexistent-skill")
	}
	if report.MissingPins[0].Slug != "custom-nonexistent-skill" {
		t.Errorf("got missing pin %q, want 'custom-nonexistent-skill'", report.MissingPins[0].Slug)
	}
	if !report.MissingPins[0].Required {
		t.Error("expected missing pin to be marked required")
	}
}

func createMockSkill(t *testing.T, baseDir, slug, desc string, tags []string) {
	t.Helper()
	sDir := filepath.Join(baseDir, slug)
	_ = os.MkdirAll(sDir, 0755)
	tagsYAML := ""
	if len(tags) > 0 {
		tagsYAML = "tags:\n"
		for _, tag := range tags {
			tagsYAML += "  - " + tag + "\n"
		}
	}
	content := "---\nname: " + slug + "\ndescription: " + desc + "\n" + tagsYAML + "---\n# " + slug + "\n"
	_ = os.WriteFile(filepath.Join(sDir, "SKILL.md"), []byte(content), 0644)
}
