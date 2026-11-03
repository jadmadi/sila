package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/goals"
)

func TestGoalsCreateWithCustomTemplateCLI(t *testing.T) {
	tmpDir := t.TempDir()

	// Setup custom templates under .sila/templates/goals/
	tmplDir := filepath.Join(tmpDir, ".sila", "templates", "goals")
	if err := os.MkdirAll(tmplDir, 0755); err != nil {
		t.Fatal(err)
	}

	customGoalTmpl := `# Goal: {{.Title}}

<!-- repo-governance: active -->
Reviewed on: {{.Date}}

## Goal Description
{{.Description}}

## Architecture Compliance
- Strict SLSA standards verified: true

## Dependencies & Execution Order
- **Mode**: {{.Mode}}
- **Depends On**: {{.DependsOnStr}}
- **Sequence**: {{.Sequence}}
- **Shape**: {{.Shape}}
- **Tier**: {{.Tier}}

## References
- **Shared Understanding & Fact Sheet**: [` + "`" + `goals/{{.Slug}}/facts.md` + "`" + `](facts.md)
- **Execution Plan**: [` + "`" + `goals/{{.Slug}}/plan.md` + "`" + `](plan.md)

## Done Condition
1. Clean exit code 0.
`
	if err := os.WriteFile(filepath.Join(tmplDir, "goal.md"), []byte(customGoalTmpl), 0644); err != nil {
		t.Fatal(err)
	}

	gc := &goalsContext{
		targetDir: tmpDir,
		args:      []string{"create", "auto-templated", "--title=Auto Templated Goal", "--desc=Auto template test"},
		title:     "Auto Templated Goal",
		desc:      "Auto template test",
	}

	gc.handleCreate(tmpDir, "auto-templated")

	createdGoalFile := filepath.Join(tmpDir, "goals", "auto-templated", "goal.md")
	contentBytes, err := os.ReadFile(createdGoalFile)
	if err != nil {
		t.Fatalf("failed to read created goal file: %v", err)
	}
	content := string(contentBytes)

	if !strings.Contains(content, "<!-- repo-governance: active -->") {
		t.Errorf("expected auto-detected custom template header, got:\n%s", content)
	}
	if !strings.Contains(content, "## Architecture Compliance") {
		t.Errorf("expected custom section from template, got:\n%s", content)
	}
}

func TestGoalsCreateWithExplicitTemplateDirFlag(t *testing.T) {
	tmpDir := t.TempDir()
	customTmplDir := filepath.Join(tmpDir, "custom-templates")
	if err := os.MkdirAll(customTmplDir, 0755); err != nil {
		t.Fatal(err)
	}

	customGoalTmpl := `# Goal: {{.Title}}

<!-- explicit-template-dir: verified -->
Team: Core Platform

## Goal Description
{{.Description}}

## Dependencies & Execution Order
- **Mode**: {{.Mode}}
- **Depends On**: {{.DependsOnStr}}
- **Sequence**: {{.Sequence}}
- **Shape**: {{.Shape}}
- **Tier**: {{.Tier}}

## References
- **Shared Understanding & Fact Sheet**: [` + "`" + `goals/{{.Slug}}/facts.md` + "`" + `](facts.md)
- **Execution Plan**: [` + "`" + `goals/{{.Slug}}/plan.md` + "`" + `](plan.md)

## Done Condition
1. Verified.
`
	if err := os.WriteFile(filepath.Join(customTmplDir, "goal.md"), []byte(customGoalTmpl), 0644); err != nil {
		t.Fatal(err)
	}

	gc := &goalsContext{
		targetDir: tmpDir,
		args: []string{
			"create",
			"flag-templated",
			"--title=Flag Templated Goal",
			"--desc=Testing explicit template flag",
			"--template-dir=" + customTmplDir,
		},
		title: "Flag Templated Goal",
		desc:  "Testing explicit template flag",
	}

	gc.handleCreate(tmpDir, "flag-templated")

	createdGoalFile := filepath.Join(tmpDir, "goals", "flag-templated", "goal.md")
	contentBytes, err := os.ReadFile(createdGoalFile)
	if err != nil {
		t.Fatalf("failed to read created goal file: %v", err)
	}
	content := string(contentBytes)

	if !strings.Contains(content, "<!-- explicit-template-dir: verified -->") {
		t.Errorf("expected explicit template dir header, got:\n%s", content)
	}
	if !strings.Contains(content, "Team: Core Platform") {
		t.Errorf("expected custom team section, got:\n%s", content)
	}

	// Verify goals engine can parse this created goal
	parsed, err := goals.ScanGoals(tmpDir)
	if err != nil {
		t.Fatalf("ScanGoals failed on templated goal: %v", err)
	}
	if len(parsed.Goals) != 1 {
		t.Fatalf("expected 1 scanned goal, got %d", len(parsed.Goals))
	}
	if parsed.Goals[0].Slug != "flag-templated" {
		t.Errorf("scanned slug = %s, want flag-templated", parsed.Goals[0].Slug)
	}
}
