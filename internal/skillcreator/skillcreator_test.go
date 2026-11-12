package skillcreator

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/skills"
)

func TestParseTopic(t *testing.T) {
	cases := []struct {
		input        []string
		expectedSlug string
	}{
		{
			input:        []string{"production", "readiness", "scorecard", "map/roadmap"},
			expectedSlug: "production-readiness-scorecard-map-roadmap",
		},
		{
			input:        []string{"API", "Contract", "Testing"},
			expectedSlug: "api-contract-testing",
		},
		{
			input:        []string{},
			expectedSlug: "custom-skill",
		},
	}

	for _, tc := range cases {
		topic := ParseTopic(tc.input)
		if topic.Slug != tc.expectedSlug {
			t.Errorf("ParseTopic(%v) = slug %q, want %q", tc.input, topic.Slug, tc.expectedSlug)
		}
		if topic.Title == "" {
			t.Errorf("expected non-empty Title for %v", tc.input)
		}
	}
}

func TestCollectProjectGrounding(t *testing.T) {
	tempDir := t.TempDir()

	// Create fake go.mod and AGENTS.md
	_ = os.WriteFile(filepath.Join(tempDir, "go.mod"), []byte("module testproject\n\ngo 1.26\n"), 0644)
	_ = os.WriteFile(filepath.Join(tempDir, "AGENTS.md"), []byte("# Project Invariants\n- Must pass go test -race\n"), 0644)

	grounding, err := CollectProjectGrounding(nil, tempDir)
	if err != nil {
		t.Fatalf("CollectProjectGrounding failed: %v", err)
	}

	foundGo := false
	for _, stack := range grounding.TechStack {
		if stack == "Go" {
			foundGo = true
		}
	}
	if !foundGo {
		t.Errorf("expected Go in TechStack, got %v", grounding.TechStack)
	}

	if !strings.Contains(grounding.RulesSummary, "Must pass go test -race") {
		t.Errorf("expected AGENTS.md rules in RulesSummary, got %s", grounding.RulesSummary)
	}
}

func TestAssembleGrillPrompt(t *testing.T) {
	grounding := &ProjectGrounding{
		ProjectDir:   "/path/to/project",
		ProjectID:    "sila-test",
		TechStack:    []string{"Go", "SQLite"},
		RulesSummary: "Enforce CGO_ENABLED=0",
	}

	topic := ParseTopic([]string{"production", "readiness", "scorecard"})
	prompt := AssembleGrillPrompt(grounding, topic)

	if !strings.Contains(prompt, "production-readiness-scorecard") {
		t.Errorf("expected slug in prompt, got %s", prompt)
	}
	if !strings.Contains(prompt, "grill-me") || !strings.Contains(prompt, "skill-creator") || !strings.Contains(prompt, "plannotator") {
		t.Errorf("expected three-methodology references in prompt, got %s", prompt)
	}
	if !strings.Contains(prompt, "Enforce CGO_ENABLED=0") {
		t.Errorf("expected project rules in prompt, got %s", prompt)
	}
	if !strings.Contains(prompt, "Self-Improvement Mechanism") {
		t.Errorf("expected self-improvement mechanism in prompt, got %s", prompt)
	}
}

func TestScaffoldSkill(t *testing.T) {
	tempDir := t.TempDir()

	draft := SkillDraft{
		Slug:        "test-scorecard",
		Name:        "Test Scorecard",
		Description: "Audit production readiness for testproject",
		Overview:    "Evaluates release boundaries",
		Triggers:    []string{"Running release pre-flight"},
		Workflows:   []string{"1. Check gates", "2. Run race tests"},
		Invariants:  []string{"Never skip tests"},
		Rubric:      []string{"All tests pass"},
	}

	res, err := ScaffoldSkill(tempDir, draft, false)
	if err != nil {
		t.Fatalf("ScaffoldSkill failed: %v", err)
	}

	if _, err := os.Stat(res.SkillMDPath); err != nil {
		t.Fatalf("expected SKILL.md to exist at %s", res.SkillMDPath)
	}

	data, err := os.ReadFile(res.SkillMDPath)
	if err != nil {
		t.Fatalf("read SKILL.md failed: %v", err)
	}
	content := string(data)

	if !strings.Contains(content, "name: test-scorecard") {
		t.Errorf("expected frontmatter name, got %s", content)
	}
	if !strings.Contains(content, "## Verification & Self-Improvement Rubric") {
		t.Errorf("expected self-improvement rubric header, got %s", content)
	}
	if !strings.Contains(content, "sila lesson") {
		t.Errorf("expected sila lesson hook in rubric, got %s", content)
	}

	// Should error if called again without force
	_, err = ScaffoldSkill(tempDir, draft, false)
	if err == nil {
		t.Errorf("expected error on duplicate scaffold without force")
	}

	// Should succeed with force
	_, err = ScaffoldSkill(tempDir, draft, true)
	if err != nil {
		t.Errorf("expected success with force, got %v", err)
	}
}

func TestRunInteractiveGrill(t *testing.T) {
	grounding := &ProjectGrounding{
		ProjectDir: "/path/to/project",
		ProjectID:  "sila-test",
		TechStack:  []string{"Go"},
	}
	topic := ParseTopic([]string{"api", "auditor"})

	// Simulate user typing answers to 4 questions
	input := "User triggers API audit\nStep 1: Check routes; Step 2: Validate envelopes\nNever mutate routes\n100% route coverage\n"
	in := strings.NewReader(input)
	var out bytes.Buffer

	ui := render.New(os.Stdout, true)
	draft, err := RunInteractiveGrill(ui, in, &out, grounding, topic)
	if err != nil {
		t.Fatalf("RunInteractiveGrill failed: %v", err)
	}

	if len(draft.Triggers) == 0 || draft.Triggers[0] != "User triggers API audit" {
		t.Errorf("unexpected triggers: %v", draft.Triggers)
	}
	if len(draft.Workflows) != 2 {
		t.Errorf("expected 2 workflows, got %v", draft.Workflows)
	}
}

func TestListAndLoadProjectSkills(t *testing.T) {
	tempDir := t.TempDir()

	// 1. Initially empty
	skills, err := ListProjectSkills(tempDir)
	if err != nil {
		t.Fatalf("ListProjectSkills empty failed: %v", err)
	}
	if len(skills) != 0 {
		t.Errorf("expected 0 skills, got %d", len(skills))
	}

	// 2. Scaffold two skills
	draft1 := SkillDraft{
		Slug:        "beta-tester",
		Name:        "Beta Tester",
		Description: "Perform beta testing on CLI commands.",
		Triggers:    []string{"User asks to beta test", "Release pre-flight"},
	}
	_, err = ScaffoldSkill(tempDir, draft1, false)
	if err != nil {
		t.Fatalf("ScaffoldSkill draft1 failed: %v", err)
	}

	draft2 := SkillDraft{
		Slug:        "alpha-linter",
		Name:        "Alpha Linter",
		Description: "Run strict linters across source files.",
		Triggers:    []string{"Linting source code"},
		Scripts:     map[string]string{"lint.sh": "#!/bin/bash\necho ok\n"},
		References:  map[string]string{"rules.md": "# Linter Rules\n"},
	}
	_, err = ScaffoldSkill(tempDir, draft2, false)
	if err != nil {
		t.Fatalf("ScaffoldSkill draft2 failed: %v", err)
	}

	// 3. List should return sorted by slug (alpha-linter, then beta-tester)
	skills, err = ListProjectSkills(tempDir)
	if err != nil {
		t.Fatalf("ListProjectSkills failed: %v", err)
	}
	if len(skills) != 2 {
		t.Fatalf("expected 2 skills, got %d", len(skills))
	}
	if skills[0].Slug != "alpha-linter" || skills[1].Slug != "beta-tester" {
		t.Errorf("unexpected skill ordering: %s, %s", skills[0].Slug, skills[1].Slug)
	}
	if !skills[0].HasScripts || !skills[0].HasRefs {
		t.Errorf("expected alpha-linter to have scripts and references")
	}
	if len(skills[1].Triggers) != 2 {
		t.Errorf("expected beta-tester to have 2 triggers, got %v", skills[1].Triggers)
	}

	// 4. Load valid skill
	skill, err := LoadProjectSkill(tempDir, "beta-tester")
	if err != nil {
		t.Fatalf("LoadProjectSkill failed: %v", err)
	}
	if skill.Name != "Beta Tester" || skill.Description != "Perform beta testing on CLI commands." {
		t.Errorf("unexpected loaded skill data: %+v", skill)
	}

	// 5. Load invalid or non-existent skill
	_, err = LoadProjectSkill(tempDir, "non-existent")
	if err == nil {
		t.Errorf("expected error for non-existent skill, got nil")
	}
	_, err = LoadProjectSkill(tempDir, "../escape")
	if err == nil {
		t.Errorf("expected error for traversal skill name, got nil")
	}
}

func TestScaffoldSkill_QuotesColonAndHashDescription(t *testing.T) {
	draft := SkillDraft{
		Slug: "quoted-skill", Name: "Quoted Skill",
		Description: "Fix: parser # issue",
	}
	res, err := ScaffoldSkill(t.TempDir(), draft, false)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(res.SkillMDPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, `description: "Fix: parser # issue"`) {
		t.Fatalf("description was not quoted: %s", text)
	}
	pf := skills.ParseSkillContent(draft.Slug, text)
	if pf.Description != draft.Description {
		t.Fatalf("round-trip description = %q, want %q", pf.Description, draft.Description)
	}

	quoted := SkillDraft{Slug: "already-quoted", Name: "Already", Description: `"Fix: parser"`}
	res2, err := ScaffoldSkill(t.TempDir(), quoted, false)
	if err != nil {
		t.Fatal(err)
	}
	data2, err := os.ReadFile(res2.SkillMDPath)
	if err != nil {
		t.Fatal(err)
	}
	text2 := string(data2)
	if strings.Contains(text2, `description: ""`) {
		t.Fatalf("already-quoted description was double-quoted: %s", text2)
	}
	if !strings.Contains(text2, `description: "Fix: parser"`) {
		t.Fatalf("already-quoted description changed: %s", text2)
	}
}
