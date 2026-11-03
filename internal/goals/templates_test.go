package goals

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFindGoalTemplate(t *testing.T) {
	tmp := t.TempDir()

	// 1. None found when empty
	p, err := FindGoalTemplate(tmp, "", "goal.md")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p != "" {
		t.Errorf("expected empty string, got %s", p)
	}

	// 2. Found in .sila/templates/goals/goal.md
	silaGoalsDir := filepath.Join(tmp, ".sila", "templates", "goals")
	if err := os.MkdirAll(silaGoalsDir, 0755); err != nil {
		t.Fatal(err)
	}
	expectedFile := filepath.Join(silaGoalsDir, "goal.md")
	if err := os.WriteFile(expectedFile, []byte("custom goal template"), 0644); err != nil {
		t.Fatal(err)
	}

	p, err = FindGoalTemplate(tmp, "", "goal.md")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p != expectedFile {
		t.Errorf("expected %s, got %s", expectedFile, p)
	}

	// 3. Fallback to .sila/templates/facts.md.tmpl
	silaTmplDir := filepath.Join(tmp, ".sila", "templates")
	factsTmpl := filepath.Join(silaTmplDir, "facts.md.tmpl")
	if err := os.WriteFile(factsTmpl, []byte("custom facts tmpl"), 0644); err != nil {
		t.Fatal(err)
	}

	p, err = FindGoalTemplate(tmp, "", "facts.md")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p != factsTmpl {
		t.Errorf("expected %s, got %s", factsTmpl, p)
	}

	// 4. Custom template directory override
	customDir := filepath.Join(tmp, "custom-templates")
	if err := os.MkdirAll(customDir, 0755); err != nil {
		t.Fatal(err)
	}
	customGoal := filepath.Join(customDir, "goal.md")
	if err := os.WriteFile(customGoal, []byte("override goal template"), 0644); err != nil {
		t.Fatal(err)
	}

	p, err = FindGoalTemplate(tmp, customDir, "goal.md")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p != customGoal {
		t.Errorf("expected %s, got %s", customGoal, p)
	}
}

func TestRenderTemplateFile(t *testing.T) {
	tmp := t.TempDir()
	tmplFile := filepath.Join(tmp, "goal.md")
	content := `# Goal: {{.Title}}

Last touch base: {{.Date}}
Author agent: {{.PersonasStr}}

## Goal Description
{{.Description}}

## Governance
- Security review required: true
- Project Tier: {{upper .Tier}}

## Dependencies & Execution Order
- **Mode**: {{.Mode}}
- **Depends On**: {{.DependsOnStr}}
- **Shape**: {{.Shape}}
- **Tier**: {{.Tier}}

## Done Condition
1. Standard tests pass.
`
	if err := os.WriteFile(tmplFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	data := GoalTemplateData{
		Slug:         "test-slug",
		Title:        "Custom Templated Goal",
		Description:  "This is a custom templated goal.",
		Mode:         "Independent (disjoint, parallelizable)",
		DependsOnStr: "none",
		Shape:        "ship",
		Tier:         "roadmap",
		PersonasStr:  "@antigravity",
		Date:         "2026-10-19",
	}

	rendered, err := RenderTemplateFile(tmplFile, data)
	if err != nil {
		t.Fatalf("RenderTemplateFile error: %v", err)
	}

	if !strings.Contains(rendered, "# Goal: Custom Templated Goal") {
		t.Errorf("expected rendered title, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "Last touch base: 2026-10-19") {
		t.Errorf("expected rendered date, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "Project Tier: ROADMAP") {
		t.Errorf("expected uppercase tier from func, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "## Governance") {
		t.Errorf("expected custom Governance section, got:\n%s", rendered)
	}
}

func TestCreateGoal_WithCustomTemplates(t *testing.T) {
	tmp := t.TempDir()
	tmplDir := filepath.Join(tmp, ".sila", "templates", "goals")
	if err := os.MkdirAll(tmplDir, 0755); err != nil {
		t.Fatal(err)
	}

	goalTmpl := `# Goal: {{.Title}}

<!-- repo-governance: required -->
Last touch base: {{.Date}}

## Goal Description
{{.Description}}

## Architecture Governance
- Compliance standard: internal-v2

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
1. All custom governance gates certified.
`
	if err := os.WriteFile(filepath.Join(tmplDir, "goal.md"), []byte(goalTmpl), 0644); err != nil {
		t.Fatal(err)
	}

	factsTmpl := `# Facts: {{.Title}}

## Repository Invariants
- Enforce custom team policy.
- Date stamped: {{.Date}}

## File & Interface Contracts
- Relevant files:
{{range .FilesToTouch}}  - ` + "`{{.}}`" + `
{{end}}
`
	if err := os.WriteFile(filepath.Join(tmplDir, "facts.md"), []byte(factsTmpl), 0644); err != nil {
		t.Fatal(err)
	}

	g, err := CreateGoal(CreateGoalParams{
		RootDir:      tmp,
		Slug:         "custom-scaffold",
		Title:        "Custom Scaffold Goal",
		Description:  "Scaffolded with repo templates",
		Shape:        "ship",
		Tier:         "roadmap",
		FilesToTouch: []string{"cmd/sila/main.go", "internal/goals/goals.go"},
	})
	if err != nil {
		t.Fatalf("CreateGoal error: %v", err)
	}
	if g == nil {
		t.Fatal("expected non-nil goal")
	}

	goalMDPath := filepath.Join(tmp, "goals", "custom-scaffold", "goal.md")
	goalMDBytes, err := os.ReadFile(goalMDPath)
	if err != nil {
		t.Fatal(err)
	}
	goalMD := string(goalMDBytes)

	if !strings.Contains(goalMD, "<!-- repo-governance: required -->") {
		t.Errorf("expected custom header in goal.md, got:\n%s", goalMD)
	}
	if !strings.Contains(goalMD, "## Architecture Governance") {
		t.Errorf("expected custom Architecture Governance section, got:\n%s", goalMD)
	}
	if !strings.Contains(goalMD, "- **Shape**: ship") {
		t.Errorf("expected shape in goal.md, got:\n%s", goalMD)
	}

	factsMDPath := filepath.Join(tmp, "goals", "custom-scaffold", "facts.md")
	factsMDBytes, err := os.ReadFile(factsMDPath)
	if err != nil {
		t.Fatal(err)
	}
	factsMD := string(factsMDBytes)
	if !strings.Contains(factsMD, "## Repository Invariants") {
		t.Errorf("expected custom Repository Invariants section, got:\n%s", factsMD)
	}
	if !strings.Contains(factsMD, "cmd/sila/main.go") {
		t.Errorf("expected files to touch rendered in facts.md, got:\n%s", factsMD)
	}

	// plan.md had no custom template, so it must fall back cleanly to default plan scaffolding
	planMDPath := filepath.Join(tmp, "goals", "custom-scaffold", "plan.md")
	planMDBytes, err := os.ReadFile(planMDPath)
	if err != nil {
		t.Fatal(err)
	}
	planMD := string(planMDBytes)
	if !strings.Contains(planMD, "## Execution Steps") {
		t.Errorf("expected fallback default plan.md, got:\n%s", planMD)
	}
}
