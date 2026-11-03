package goals

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/render"
)

func TestParseGoalPersonas(t *testing.T) {
	// Case 1: Under Dependencies & Execution Order
	c1 := `# Goal: Test
## Dependencies & Execution Order
- **Mode**: Dependent
- **Depends On**: none
- **Personas**: accessibility-sentinel, performance-oracle
`
	p1 := ParseGoalPersonas(c1)
	if len(p1) != 2 || p1[0] != "accessibility-sentinel" || p1[1] != "performance-oracle" {
		t.Fatalf("expected [accessibility-sentinel, performance-oracle], got %v", p1)
	}

	// Case 2: Frontmatter style
	c2 := `---
title: Test
personas: [accessibility-sentinel, 4]
---
# Goal: Test
`
	p2 := ParseGoalPersonas(c2)
	if len(p2) != 2 || p2[0] != "accessibility-sentinel" || p2[1] != "4" {
		t.Fatalf("expected frontmatter personas [accessibility-sentinel, 4], got %v", p2)
	}

	// Case 3: Dedicated ## Review Squad section
	c3 := `# Goal: Test
## Review Squad
- accessibility-sentinel
- performance-oracle
- 12
`
	p3 := ParseGoalPersonas(c3)
	if len(p3) != 3 || p3[0] != "accessibility-sentinel" || p3[2] != "12" {
		t.Fatalf("expected Review Squad section personas, got %v", p3)
	}
}

func TestGoalPersonaLifecycle(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Create goal with assigned review squad
	g, err := CreateGoal(CreateGoalParams{
		RootDir:     tmpDir,
		Slug:        "test-squad-goal",
		Title:       "Test Squad Goal",
		Description: "A goal testing review squad binding",
		Personas:    []string{"accessibility-sentinel", "qa-automation-lead"},
	})
	if err != nil {
		t.Fatalf("CreateGoal failed: %v", err)
	}
	if len(g.Personas) != 2 || g.Personas[0] != "accessibility-sentinel" {
		t.Fatalf("expected personas on created goal, got %v", g.Personas)
	}

	// Verify on disk in goal.md
	goalMDPath := filepath.Join(tmpDir, "goals", "test-squad-goal", "goal.md")
	data, err := os.ReadFile(goalMDPath)
	if err != nil {
		t.Fatalf("read goal.md: %v", err)
	}
	parsed := ParseGoalPersonas(string(data))
	if len(parsed) != 2 {
		t.Fatalf("expected 2 personas in goal.md, got %v", parsed)
	}

	// 2. ScanGoals verifies Personas populated
	audit, err := ScanGoals(tmpDir)
	if err != nil {
		t.Fatalf("ScanGoals failed: %v", err)
	}
	scannedGoal, err := ResolveGoalByIdentifier(audit, "test-squad-goal")
	if err != nil || scannedGoal == nil || len(scannedGoal.Personas) != 2 {
		t.Fatalf("scanned goal missing personas: %v", scannedGoal)
	}

	// 3. UpdateGoal adds another persona
	updated, err := UpdateGoal(UpdateGoalParams{
		RootDir:     tmpDir,
		Slug:        "test-squad-goal",
		AddPersonas: []string{"performance-oracle"},
	})
	if err != nil {
		t.Fatalf("UpdateGoal add personas failed: %v", err)
	}
	if len(updated.Personas) != 3 {
		t.Fatalf("expected 3 personas after add, got %v", updated.Personas)
	}

	// 4. UpdateGoal overrides personas
	updated2, err := UpdateGoal(UpdateGoalParams{
		RootDir:  tmpDir,
		Slug:     "test-squad-goal",
		Personas: []string{"sec-compliance-auditor"},
	})
	if err != nil {
		t.Fatalf("UpdateGoal override personas failed: %v", err)
	}
	if len(updated2.Personas) != 1 || updated2.Personas[0] != "sec-compliance-auditor" {
		t.Fatalf("expected 1 overridden persona, got %v", updated2.Personas)
	}

	// 5. BuildGoalView and linked reports
	reportsDir := filepath.Join(tmpDir, ".sila", "reports")
	if err := os.MkdirAll(reportsDir, 0755); err != nil {
		t.Fatalf("mkdir reports: %v", err)
	}
	sampleReport := map[string]any{
		"id":              "rep_sec_12345",
		"associated_goal": "test-squad-goal",
		"persona_id":      "sec-compliance-auditor",
		"persona_name":    "Lead Security & Compliance Auditor",
		"persona_num_id":  14,
		"status":          "pass",
		"summary":         "OWASP Top 10 and GDPR verification passed",
		"updated_at":      time.Now().UTC(),
	}
	repBytes, _ := json.Marshal(sampleReport)
	_ = os.WriteFile(filepath.Join(reportsDir, "rep_sec_12345.json"), repBytes, 0644)

	gv, err := BuildGoalView(tmpDir, updated2)
	if err != nil {
		t.Fatalf("BuildGoalView failed: %v", err)
	}
	if len(gv.Personas) != 1 || gv.Personas[0] != "sec-compliance-auditor" {
		t.Fatalf("expected GoalView.Personas to contain sec-compliance-auditor, got %v", gv.Personas)
	}
	if len(gv.Reports) != 1 || gv.Reports[0].ID != "rep_sec_12345" {
		t.Fatalf("expected GoalView.Reports to contain linked report, got %v", gv.Reports)
	}

	// Verify terminal rendering
	ui := render.New(os.Stdout, true)
	renderedTerm := RenderGoalViewTerminal(ui, gv)
	if !strings.Contains(renderedTerm, "Review Squad:") || !strings.Contains(renderedTerm, "sec-compliance-auditor") {
		t.Fatalf("RenderGoalViewTerminal missing Review Squad section: %s", renderedTerm)
	}
	if !strings.Contains(renderedTerm, "Linked Evaluation Reports") || !strings.Contains(renderedTerm, "rep_sec_12345") {
		t.Fatalf("RenderGoalViewTerminal missing Linked Evaluation Reports: %s", renderedTerm)
	}

	// Verify TOON rendering
	renderedTOON := RenderGoalViewTOON(gv)
	if !strings.Contains(renderedTOON, "personas: sec-compliance-auditor") {
		t.Fatalf("RenderGoalViewTOON missing personas: %s", renderedTOON)
	}
	if !strings.Contains(renderedTOON, "reports:") || !strings.Contains(renderedTOON, "rep_sec_12345") {
		t.Fatalf("RenderGoalViewTOON missing reports: %s", renderedTOON)
	}

	// 6. UpdateGoal clears personas
	cleared, err := UpdateGoal(UpdateGoalParams{
		RootDir:       tmpDir,
		Slug:          "test-squad-goal",
		ClearPersonas: true,
	})
	if err != nil {
		t.Fatalf("UpdateGoal clear personas failed: %v", err)
	}
	if len(cleared.Personas) != 0 {
		t.Fatalf("expected personas cleared, got %v", cleared.Personas)
	}
}
