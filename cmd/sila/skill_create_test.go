package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/skillcreator"
	"github.com/jadmadi/sila/internal/store"
)

func TestSkillCreateCLI(t *testing.T) {
	tempDir := t.TempDir()
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd failed: %v", err)
	}
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("chdir tempDir failed: %v", err)
	}
	defer func() {
		_ = os.Chdir(origWd)
	}()

	// Create fake go.mod and AGENTS.md in tempDir
	_ = os.WriteFile(filepath.Join(tempDir, "go.mod"), []byte("module testskillproject\n\ngo 1.26\n"), 0644)
	_ = os.WriteFile(filepath.Join(tempDir, "AGENTS.md"), []byte("# Rules\n- Strictly enforce CGO_ENABLED=0\n"), 0644)

	dbPath := filepath.Join(tempDir, "test_sila.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	st.Close()

	ui := render.New(os.Stdout, true)

	// 1. Usage display when no topic provided
	outUsage := captureStdout(t, func() {
		runSkillCreate(ui, dbPath, false, false, skillCreateOptions{}, []string{})
	})
	if !strings.Contains(outUsage, "bespoke project skill authoring & management") {
		t.Errorf("expected usage header, got: %s", outUsage)
	}
	if !strings.Contains(outUsage, "sila skill create production readiness scorecard map/roadmap") {
		t.Errorf("expected example in usage, got: %s", outUsage)
	}

	// 2. Prompt-only generation
	outPrompt := captureStdout(t, func() {
		runSkillCreate(ui, dbPath, false, false, skillCreateOptions{}, []string{"production", "readiness", "scorecard", "--prompt-only"})
	})
	if !strings.Contains(outPrompt, "Master Prompt: Interactive Tailored Project Skill Creator") {
		t.Errorf("expected master prompt header, got: %s", outPrompt)
	}
	if !strings.Contains(outPrompt, "production-readiness-scorecard") {
		t.Errorf("expected slug in prompt, got: %s", outPrompt)
	}
	if !strings.Contains(outPrompt, "Strictly enforce CGO_ENABLED=0") {
		t.Errorf("expected AGENTS.md rule in prompt, got: %s", outPrompt)
	}
	if !strings.Contains(outPrompt, "Self-Improvement Mechanism") {
		t.Errorf("expected self-improvement mechanism in prompt, got: %s", outPrompt)
	}

	// 3. Direct scaffolding with project grounding defaults
	outCreate := captureStdout(t, func() {
		runSkillCreate(ui, dbPath, false, false, skillCreateOptions{}, []string{"production", "readiness", "scorecard"})
	})
	if !strings.Contains(outCreate, "tailored agent skill created successfully") {
		t.Errorf("expected creation success, got: %s", outCreate)
	}

	expectedSkillMD := filepath.Join(tempDir, ".agents", "skills", "production-readiness-scorecard", "SKILL.md")
	if _, err := os.Stat(expectedSkillMD); err != nil {
		t.Fatalf("expected SKILL.md to exist at %s: %v", expectedSkillMD, err)
	}

	data, err := os.ReadFile(expectedSkillMD)
	if err != nil {
		t.Fatalf("failed to read created SKILL.md: %v", err)
	}
	skillContent := string(data)
	if !strings.Contains(skillContent, "name: production-readiness-scorecard") {
		t.Errorf("expected frontmatter name, got: %s", skillContent)
	}
	if !strings.Contains(skillContent, "## Verification & Self-Improvement Rubric") {
		t.Errorf("expected self-improvement rubric header, got: %s", skillContent)
	}
	if !strings.Contains(skillContent, "sila lesson") {
		t.Errorf("expected sila lesson hook, got: %s", skillContent)
	}

	// 4. Scaffolding again with --force
	outForce := captureStdout(t, func() {
		runSkillCreate(ui, dbPath, false, false, skillCreateOptions{}, []string{"production", "readiness", "scorecard", "--force"})
	})
	if !strings.Contains(outForce, "tailored agent skill created successfully") {
		t.Errorf("expected success with force overwrite, got: %s", outForce)
	}

	// 5. Test plural "sila skills" listing
	outSkillsList := captureStdout(t, func() {
		runSkills(ui, dbPath, false, false, skillCreateOptions{}, []string{}, "skills")
	})
	if !strings.Contains(outSkillsList, "project agent skills (.agents/skills/)") {
		t.Errorf("expected skills header, got: %s", outSkillsList)
	}
	if !strings.Contains(outSkillsList, "production-readiness-scorecard") {
		t.Errorf("expected production-readiness-scorecard in skills list, got: %s", outSkillsList)
	}
	if !strings.Contains(outSkillsList, "sila skill show <name>") {
		t.Errorf("expected inspect command in footer, got: %s", outSkillsList)
	}

	// 6. Test singular "sila skill show <name>"
	outSkillShow := captureStdout(t, func() {
		runSkills(ui, dbPath, false, false, skillCreateOptions{}, []string{"show", "production-readiness-scorecard"}, "skill")
	})
	if !strings.Contains(outSkillShow, "project skill: production-readiness-scorecard") {
		t.Errorf("expected skill show header, got: %s", outSkillShow)
	}
	if !strings.Contains(outSkillShow, "## Overview") {
		t.Errorf("expected raw markdown content in show, got: %s", outSkillShow)
	}

	// 7. Test singular "sila skill create <topic...>"
	outSkillCreate := captureStdout(t, func() {
		runSkills(ui, dbPath, false, false, skillCreateOptions{}, []string{"create", "api", "contract", "test"}, "skill")
	})
	if !strings.Contains(outSkillCreate, "tailored agent skill created successfully") {
		t.Errorf("expected skill creation via 'skill create', got: %s", outSkillCreate)
	}
	if !strings.Contains(outSkillCreate, "api-contract-test") {
		t.Errorf("expected api-contract-test slug, got: %s", outSkillCreate)
	}

	// 8. Test "sila skill" no-args usage display
	outSkillNoArgs := captureStdout(t, func() {
		runSkills(ui, dbPath, false, false, skillCreateOptions{}, []string{}, "skill")
	})
	if !strings.Contains(outSkillNoArgs, "bespoke project skill authoring & management") {
		t.Errorf("expected usage guide header, got: %s", outSkillNoArgs)
	}
	if !strings.Contains(outSkillNoArgs, "sila skills") || !strings.Contains(outSkillNoArgs, "sila skill create") {
		t.Errorf("expected singular and plural commands in usage, got: %s", outSkillNoArgs)
	}

	// 9. Test "sila skills list --compact" progressive disclosure table
	outSkillsCompact := captureStdout(t, func() {
		runSkills(ui, dbPath, false, false, skillCreateOptions{compact: true}, []string{"list"}, "skills")
	})
	if !strings.Contains(outSkillsCompact, "compact skill manifest (progressive disclosure)") {
		t.Errorf("expected compact manifest header, got: %s", outSkillsCompact)
	}
	if !strings.Contains(outSkillsCompact, "SLUG") || !strings.Contains(outSkillsCompact, "PROMPT TRIGGER") {
		t.Errorf("expected SLUG and PROMPT TRIGGER table headers, got: %s", outSkillsCompact)
	}
	if !strings.Contains(outSkillsCompact, "production-readiness-scorecard") {
		t.Errorf("expected production-readiness-scorecard in compact table, got: %s", outSkillsCompact)
	}

	// 10. Test "sila skills list --compact --json"
	outSkillsCompactJSON := captureStdout(t, func() {
		runSkills(ui, dbPath, true, false, skillCreateOptions{compact: true}, []string{"list"}, "skills")
	})
	if !strings.Contains(outSkillsCompactJSON, `"compact": true`) || !strings.Contains(outSkillsCompactJSON, `"slug": "production-readiness-scorecard"`) {
		t.Errorf("expected JSON compact output, got: %s", outSkillsCompactJSON)
	}
}

// TestSkillCandidateStagingAndPromote proves candidate staging stays out of the
// active tree, promote validates and moves a skill, and malformed frontmatter
// is rejected without creating an active skill.
func TestSkillCandidateStagingAndPromote(t *testing.T) {
	if os.Getenv("SKILL_PROMOTE_BAD") == "1" {
		ui := render.New(os.Stdout, true)
		runSkillPromote(ui, false, false, os.Getenv("SKILL_PROMOTE_DIR"), "bad-skill")
		return
	}

	projectDir := t.TempDir()
	draft := skillcreator.SkillDraft{
		Slug:        "candidate-skill",
		Name:        "Candidate Skill",
		Description: "Use when testing isolated candidate skill staging",
		Overview:    "A staged skill that is not active until promoted.",
		Triggers:    []string{"candidate staging test"},
	}

	res, err := scaffoldCandidateSkill(projectDir, draft, false)
	if err != nil {
		t.Fatalf("scaffold candidate: %v", err)
	}
	wantMD := filepath.Join(projectDir, ".sila", "candidates", "skills", "candidate-skill", "SKILL.md")
	if res.SkillMDPath != wantMD {
		t.Fatalf("SkillMDPath = %q, want %q", res.SkillMDPath, wantMD)
	}
	if _, err := os.Stat(wantMD); err != nil {
		t.Fatalf("candidate SKILL.md missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(projectDir, ".agents", "skills", "candidate-skill")); !os.IsNotExist(err) {
		t.Fatalf("candidate staging wrote into .agents/skills: err=%v", err)
	}
	if _, err := scaffoldCandidateSkill(projectDir, draft, false); err == nil {
		t.Fatal("expected duplicate candidate staging to fail without --force")
	}
	if _, err := scaffoldCandidateSkill(projectDir, draft, true); err != nil {
		t.Fatalf("expected --force to replace the candidate: %v", err)
	}

	dest, err := promoteCandidateSkill(projectDir, "candidate-skill")
	if err != nil {
		t.Fatalf("promote candidate: %v", err)
	}
	if _, err := os.Stat(dest); err != nil {
		t.Fatalf("promoted SKILL.md missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(projectDir, ".sila", "candidates", "skills", "candidate-skill")); !os.IsNotExist(err) {
		t.Fatalf("candidate directory still exists after promote: err=%v", err)
	}

	badDir := filepath.Join(projectDir, ".sila", "candidates", "skills", "bad-skill")
	if err := os.MkdirAll(badDir, 0o755); err != nil {
		t.Fatal(err)
	}
	badMD := filepath.Join(badDir, "SKILL.md")
	if err := os.WriteFile(badMD, []byte("---\nname: bad-skill\ndescription: Fix: unquoted colon\n---\n# Bad\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := promoteCandidateSkill(projectDir, "bad-skill"); err == nil {
		t.Fatal("expected malformed frontmatter to be rejected")
	}
	if _, err := os.Stat(filepath.Join(projectDir, ".agents", "skills", "bad-skill")); !os.IsNotExist(err) {
		t.Fatalf("malformed candidate became active: err=%v", err)
	}

	if err := os.WriteFile(badMD, []byte("---\nname: bad-skill\n---\n# Bad\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := promoteCandidateSkill(projectDir, "bad-skill"); err == nil {
		t.Fatal("expected missing description to be rejected")
	}

	t.Run("nonzero_exit_on_malformed", func(t *testing.T) {
		if err := os.WriteFile(badMD, []byte("---\nname: bad-skill\ndescription: Fix: unquoted colon\n---\n# Bad\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(os.Args[0], "-test.run=TestSkillCandidateStagingAndPromote")
		cmd.Env = append(os.Environ(), "SKILL_PROMOTE_BAD=1", "SKILL_PROMOTE_DIR="+projectDir)
		if err := cmd.Run(); err == nil {
			t.Fatal("expected non-zero exit for malformed candidate promote")
		}
	})
}
