package audit

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/persona"
)

func TestReportStorageLifecycle(t *testing.T) {
	tempDir := t.TempDir()
	if out, err := exec.Command("git", "-C", tempDir, "init", "-b", "main").CombinedOutput(); err != nil {
		t.Fatalf("initialize isolated repository: %v: %s", err, out)
	}

	// Initial save
	rep := &Report{
		ID:             "rep_test_001",
		AssociatedGoal: "contrast-cta-wcag",
		PersonaID:      "accessibility-sentinel",
		PersonaNumID:   12,
		PersonaName:    "Accessibility & WCAG 2.2 Sentinel",
		Domain:         "access",
		TargetURL:      "https://example.com/checkout",
		Status:         "critical",
		Phase:          4,
		Summary:        "Primary CTA button fails contrast standards",
		Findings: []Finding{
			{
				ID:          "f1",
				PersonaID:   "accessibility-sentinel",
				Category:    "Accessibility",
				Severity:    SeverityCritical,
				PageURL:     "https://example.com/checkout",
				Evidence:    "#3b82f6 on white has 3.8:1 contrast",
				Description: "Fails WCAG 2.2 AA",
				Impact:      "Low vision users cannot find submit button",
			},
		},
		Suggestions: []Suggestion{
			{
				Title:       "Darken button to #2563eb",
				PersonaID:   "accessibility-sentinel",
				Action:      "Update Tailwind color class",
				Priority:    "P1",
				ExpectedROI: "100% WCAG AA compliance",
			},
		},
		SkillsUsed: []string{"browser-testing-with-devtools"},
	}

	err := SaveReport(tempDir, rep)
	if err != nil {
		t.Fatalf("SaveReport failed: %v", err)
	}

	// Verify file was written to .sila/reports/
	expectedFile := filepath.Join(tempDir, ".sila", "reports", "rep_test_001.json")
	if _, err := os.Stat(expectedFile); err != nil {
		t.Fatalf("expected report file at %s: %v", expectedFile, err)
	}

	// Read report back
	loaded, err := GetReport(tempDir, "rep_test_001")
	if err != nil {
		t.Fatalf("GetReport failed: %v", err)
	}
	if loaded.ID != rep.ID || loaded.AssociatedGoal != "contrast-cta-wcag" {
		t.Errorf("loaded report mismatch: %+v", loaded)
	}
	if loaded.CreatedAt.IsZero() || loaded.UpdatedAt.IsZero() {
		t.Errorf("expected non-zero timestamps on report, got created=%v updated=%v", loaded.CreatedAt, loaded.UpdatedAt)
	}

	// List reports
	all, err := ListReports(tempDir)
	if err != nil {
		t.Fatalf("ListReports failed: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("expected 1 report, got %d", len(all))
	}

	// Find by goal
	byGoal, err := FindReportsForGoal(tempDir, "contrast-cta-wcag")
	if err != nil {
		t.Fatalf("FindReportsForGoal failed: %v", err)
	}
	if len(byGoal) != 1 {
		t.Fatalf("expected 1 matching report for goal, got %d", len(byGoal))
	}

	// Find by goal with goals/ prefix
	byGoalPrefix, err := FindReportsForGoal(tempDir, "goals/contrast-cta-wcag")
	if err != nil {
		t.Fatalf("FindReportsForGoal with prefix failed: %v", err)
	}
	if len(byGoalPrefix) != 1 {
		t.Fatalf("expected 1 matching report with prefix, got %d", len(byGoalPrefix))
	}

	// Link to another goal and verify update time
	time.Sleep(10 * time.Millisecond)
	err = LinkReportToGoal(tempDir, "rep_test_001", "new-checkout-flow")
	if err != nil {
		t.Fatalf("LinkReportToGoal failed: %v", err)
	}

	reloaded, err := GetReport(tempDir, "rep_test_001")
	if err != nil {
		t.Fatalf("GetReport after link failed: %v", err)
	}
	if reloaded.AssociatedGoal != "new-checkout-flow" {
		t.Errorf("expected associated_goal new-checkout-flow, got %s", reloaded.AssociatedGoal)
	}
	if !reloaded.UpdatedAt.After(loaded.UpdatedAt) {
		t.Errorf("expected UpdatedAt to advance after link: loaded=%v reloaded=%v", loaded.UpdatedAt, reloaded.UpdatedAt)
	}
}

func TestAssessSquadAndPhasedScheduling(t *testing.T) {
	all := persona.FoundationPersonas
	all = persona.EnrichPersonas(all)

	// Test 1: Assess unbalanced squad (only UI personas)
	unbalancedMesh, err := persona.ResolvePersonas(all, "10,11,12,13")
	if err != nil {
		t.Fatalf("ResolvePersonas failed: %v", err)
	}

	assessment := AssessSquad(all, unbalancedMesh, "https://test.io", "")
	if assessment.SquadSize != 4 {
		t.Fatalf("expected squad size 4, got %d", assessment.SquadSize)
	}
	if len(assessment.Blindspots) == 0 {
		t.Errorf("expected blindspots for unbalanced squad, got none")
	}
	if len(assessment.Redundancies) == 0 {
		t.Errorf("expected redundancy warning for 4 personas in Group 3, got none")
	}
	if len(assessment.Suggestions) == 0 {
		t.Errorf("expected persona suggestions to add missing domains, got none")
	}

	// All 4 personas are in UI/UX domain, so they should all schedule into Phase 4
	if len(assessment.Schedule) != 1 || assessment.Schedule[0].Phase != 4 {
		t.Errorf("expected 1 phase (Phase 4), got schedule %+v", assessment.Schedule)
	}

	// Test 2: General assess for agent-ready context
	genAssess := GeneralAssess(all, "https://target.app", "scan for MCP and llms.txt")
	if genAssess.SquadSize < 4 {
		t.Errorf("expected at least 4 personas in general assessment, got %d", genAssess.SquadSize)
	}
	if len(genAssess.Schedule) < 2 {
		t.Errorf("expected multi-phase schedule in balanced assessment, got %d phases", len(genAssess.Schedule))
	}

	// Verify all 31 personas map deterministically to phases 1..6
	for _, p := range all {
		ph := AssignPhase(p)
		if ph < 1 || ph > 6 {
			t.Errorf("persona %s assigned invalid phase %d", p.ID, ph)
		}
	}
}
