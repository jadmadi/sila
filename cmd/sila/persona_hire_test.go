package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func TestPersonaListAndGroupCLI(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test_sila.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	st.Close()

	ui := render.New(os.Stdout, true)

	// 1. Full grouped listing
	out := captureStdout(t, func() {
		runPersona(ui, dbPath, false, false, []string{"list"})
	})

	if !strings.Contains(out, "[Group 1] AI Agent Readiness") {
		t.Errorf("expected Group 1 header, got: %s", out)
	}
	if !strings.Contains(out, "[1] agent-readiness-architect") {
		t.Errorf("expected [1] agent-readiness-architect, got: %s", out)
	}
	if !strings.Contains(out, "[Group 2] Core Systems, Architecture & QA") {
		t.Errorf("expected Group 2 header, got: %s", out)
	}
	if !strings.Contains(out, "sila hire group 1") {
		t.Errorf("expected hire command suggestions, got: %s", out)
	}
	if !strings.Contains(out, "sila hire 1,4,6,3,8") {
		t.Errorf("expected hire mesh suggestion, got: %s", out)
	}

	// 2. Filter by group ID: --group=1
	outG1 := captureStdout(t, func() {
		runPersona(ui, dbPath, false, false, []string{"list", "--group=1"})
	})

	if !strings.Contains(outG1, "Group 1: AI Agent Readiness") {
		t.Errorf("expected Group 1 heading, got: %s", outG1)
	}
	if !strings.Contains(outG1, "agent-readiness-architect") {
		t.Errorf("expected agent-readiness-architect in group 1, got: %s", outG1)
	}
	if strings.Contains(outG1, "[Group 2]") {
		t.Errorf("group 1 filter should not contain Group 2, got: %s", outG1)
	}

	// 3. Filter by positional group slug: "ui-ux"
	outUIUX := captureStdout(t, func() {
		runPersona(ui, dbPath, false, false, []string{"list", "ui-ux"})
	})

	if !strings.Contains(outUIUX, "Group 3: UI, UX & Design Ergonomics") {
		t.Errorf("expected Group 3 heading, got: %s", outUIUX)
	}
	if !strings.Contains(outUIUX, "ui-craftsperson") {
		t.Errorf("expected ui-craftsperson in ui-ux group, got: %s", outUIUX)
	}

	// 4. Get by numeric ID: 1
	outGet := captureStdout(t, func() {
		runPersona(ui, dbPath, false, false, []string{"get", "1"})
	})

	if !strings.Contains(outGet, "[1] agent-readiness-architect") {
		t.Errorf("expected get '1' to return agent-readiness-architect, got: %s", outGet)
	}
	if !strings.Contains(outGet, "Group:    [1] AI Agent Readiness") {
		t.Errorf("expected Group: [1] metadata in get, got: %s", outGet)
	}
}

func TestHireSquadAndAuditCLI(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test_sila.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	st.Close()

	ui := render.New(os.Stdout, true)

	// 1. Hire briefing by group: "group 1"
	outBriefing := captureStdout(t, func() {
		runHire(ui, dbPath, false, false, auditOptions{}, []string{"group", "1"})
	})

	if !strings.Contains(outBriefing, "hired audit squad (4 personas ready)") {
		t.Errorf("expected 4 personas ready for group 1, got: %s", outBriefing)
	}
	if !strings.Contains(outBriefing, "Principal AI Agent Readiness") {
		t.Errorf("expected Principal AI Agent Readiness in squad briefing, got: %s", outBriefing)
	}
	if !strings.Contains(outBriefing, "sila hire group 1 https://target.app") {
		t.Errorf("expected audit invocation hint, got: %s", outBriefing)
	}

	// 2. Hire briefing by custom mesh: "1,4,6,3,8"
	outMesh := captureStdout(t, func() {
		runHire(ui, dbPath, false, false, auditOptions{}, []string{"1,4,6,3,8"})
	})

	if !strings.Contains(outMesh, "hired audit squad (5 personas ready)") {
		t.Errorf("expected 5 personas ready for mesh, got: %s", outMesh)
	}
	if !strings.Contains(outMesh, "[1]") || !strings.Contains(outMesh, "[4]") || !strings.Contains(outMesh, "[6]") || !strings.Contains(outMesh, "[3]") || !strings.Contains(outMesh, "[8]") {
		t.Errorf("expected all 5 IDs in mesh briefing, got: %s", outMesh)
	}

	// 3. Hire with target URL -> runs audit directly
	outAudit := captureStdout(t, func() {
		runHire(ui, dbPath, false, false, auditOptions{}, []string{"1,4,6,3,8", "https://app.example.com"})
	})

	if !strings.Contains(outAudit, "autonomous groupthink audit with hired squad") {
		t.Errorf("expected audit execution header, got: %s", outAudit)
	}
	if !strings.Contains(outAudit, "https://app.example.com") {
		t.Errorf("expected target URL in audit output, got: %s", outAudit)
	}
	if !strings.Contains(outAudit, "Synthesized Consensus Proposals") {
		t.Errorf("expected consensus proposals in audit output, got: %s", outAudit)
	}

	// 4. Hire with --prompt-only
	outPrompt := captureStdout(t, func() {
		runHire(ui, dbPath, false, false, auditOptions{promptOnly: true}, []string{"group", "1"})
	})

	if !strings.Contains(outPrompt, "Autonomous Multi-Persona Groupthink Audit Protocol") {
		t.Errorf("expected groupthink prompt header, got: %s", outPrompt)
	}
	if !strings.Contains(outPrompt, "Agent Readiness & Synthetic Interoperability Architect") {
		t.Errorf("expected persona role in prompt, got: %s", outPrompt)
	}
}

func TestHireAssessAndReportsCLI(t *testing.T) {
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

	dbPath := filepath.Join(tempDir, "test_sila.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	st.Close()

	ui := render.New(os.Stdout, true)

	// 1. Test `sila hire assess` (general)
	outGenAssess := captureStdout(t, func() {
		runHire(ui, dbPath, false, false, auditOptions{}, []string{"assess"})
	})
	if !strings.Contains(outGenAssess, "squad balance & execution assessment") {
		t.Errorf("expected assess header, got: %s", outGenAssess)
	}
	if !strings.Contains(outGenAssess, "Disjoint Execution Schedule") {
		t.Errorf("expected disjoint execution schedule, got: %s", outGenAssess)
	}

	// 2. Test `sila hire assess 1,4,6,3,8 https://target.app`
	outMeshAssess := captureStdout(t, func() {
		runHire(ui, dbPath, false, false, auditOptions{}, []string{"assess", "1,4,6,3,8", "https://target.app"})
	})
	if !strings.Contains(outMeshAssess, "Target URL:     https://target.app") {
		t.Errorf("expected target URL in mesh assess, got: %s", outMeshAssess)
	}
	if !strings.Contains(outMeshAssess, "Detected Domain Blindspots") {
		t.Errorf("expected detected blindspots, got: %s", outMeshAssess)
	}
	if !strings.Contains(outMeshAssess, "Actionable Squad Recommendations") {
		t.Errorf("expected squad recommendations, got: %s", outMeshAssess)
	}
	if !strings.Contains(outMeshAssess, "Phase 1: Protocol & Network Recon") {
		t.Errorf("expected Phase 1 in schedule, got: %s", outMeshAssess)
	}

	// 3. Test `sila hire 10,12,7,14 https://target.app --register-goals`
	outAudit := captureStdout(t, func() {
		runHire(ui, dbPath, false, false, auditOptions{registerGoals: true}, []string{"10,12,7,14", "https://target.app"})
	})
	if !strings.Contains(outAudit, "Saved 4 evaluation reports under .sila/reports/") {
		t.Errorf("expected report persistence notification, got: %s", outAudit)
	}
	if !strings.Contains(outAudit, "Linked reports to registered Sila Goal packages") {
		t.Errorf("expected goal linking notification, got: %s", outAudit)
	}

	// 4. Test `sila hire reports`
	outReports := captureStdout(t, func() {
		runHire(ui, dbPath, false, false, auditOptions{}, []string{"reports"})
	})
	if !strings.Contains(outReports, "evaluation reports in .sila/reports/ (4 reports)") {
		t.Errorf("expected 4 reports listed, got: %s", outReports)
	}
	if !strings.Contains(outReports, "REPORT ID") || !strings.Contains(outReports, "ASSOCIATED GOAL") {
		t.Errorf("expected table columns, got: %s", outReports)
	}
	if !strings.Contains(outReports, "LAST WRITTEN") || !strings.Contains(outReports, "LAST UPDATED") {
		t.Errorf("expected timestamp columns, got: %s", outReports)
	}

	// 5. Test top-level `sila reports`
	outTopReports := captureStdout(t, func() {
		runReports(ui, dbPath, false, false, []string{})
	})
	if !strings.Contains(outTopReports, "evaluation reports in .sila/reports/ (4 reports)") {
		t.Errorf("expected top-level reports to list 4 reports, got: %s", outTopReports)
	}

	// 6. Test `sila hire report <id>` detail view
	// Extract a report ID from outReports
	lines := strings.Split(outReports, "\n")
	var sampleReportID string
	for _, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "rep_") {
			parts := strings.Fields(l)
			if len(parts) > 0 {
				sampleReportID = parts[0]
				break
			}
		}
	}
	if sampleReportID == "" {
		t.Fatalf("failed to extract report ID from list output: %s", outReports)
	}

	outDetail := captureStdout(t, func() {
		runHire(ui, dbPath, false, false, auditOptions{}, []string{"report", sampleReportID})
	})
	if !strings.Contains(outDetail, "evaluation report ["+sampleReportID+"]") {
		t.Errorf("expected report detail header for %s, got: %s", sampleReportID, outDetail)
	}
	if !strings.Contains(outDetail, "Last Written:") || !strings.Contains(outDetail, "Last Updated:") {
		t.Errorf("expected timestamps in report detail, got: %s", outDetail)
	}
	if !strings.Contains(outDetail, "Executive Summary:") {
		t.Errorf("expected Executive Summary in report detail, got: %s", outDetail)
	}
}

func TestHireAssessGoalsCLI(t *testing.T) {
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

	// Scaffold mock goals directory
	uiGoalDir := filepath.Join(tempDir, "goals", "mock-ui-goal")
	if err := os.MkdirAll(uiGoalDir, 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	_ = os.WriteFile(filepath.Join(uiGoalDir, "goal.md"), []byte("# Goal: Mock UI Accessibility\n\n## Description\nImplement accessible WCAG contrast and CSS layout.\n\n- **Mode**: independent\n- **Shape**: ship\n- **Tier**: roadmap\n- **Touch**: web/styles/theme.css\n"), 0644)
	_ = os.WriteFile(filepath.Join(uiGoalDir, "facts.md"), []byte("# Facts\n- Invariants\n"), 0644)
	_ = os.WriteFile(filepath.Join(uiGoalDir, "plan.md"), []byte("# Plan\n- [ ] Step 1\n"), 0644)

	dbPath := filepath.Join(tempDir, "test_sila.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	st.Close()

	ui := render.New(os.Stdout, true)

	// 1. Test `sila hire assess --goals`
	outGoals := captureStdout(t, func() {
		runHire(ui, dbPath, false, false, auditOptions{}, []string{"assess", "--goals"})
	})
	if !strings.Contains(outGoals, "Roadmap Goal Coverage Matrix") {
		t.Errorf("expected Roadmap Goal Coverage Matrix, got: %s", outGoals)
	}
	if !strings.Contains(outGoals, "mock-ui-goal") {
		t.Errorf("expected mock-ui-goal in output, got: %s", outGoals)
	}
	if !strings.Contains(outGoals, "Roadmap Squad Alignment:") {
		t.Errorf("expected Roadmap Squad Alignment in output, got: %s", outGoals)
	}

	// 2. Test `sila hire assess --goal=mock-ui-goal`
	outSingleGoal := captureStdout(t, func() {
		runHire(ui, dbPath, false, false, auditOptions{}, []string{"assess", "--goal=mock-ui-goal"})
	})
	if !strings.Contains(outSingleGoal, "mock-ui-goal") {
		t.Errorf("expected mock-ui-goal in output, got: %s", outSingleGoal)
	}

	// 3. Test `sila hire assess mock-ui-goal` (positional slug)
	outPositional := captureStdout(t, func() {
		runHire(ui, dbPath, false, false, auditOptions{}, []string{"assess", "mock-ui-goal"})
	})
	if !strings.Contains(outPositional, "mock-ui-goal") {
		t.Errorf("expected mock-ui-goal in positional output, got: %s", outPositional)
	}

	// 4. Test `sila hire assess 1,5,8 --goals` (squad spec combined with --goals)
	outSquadGoals := captureStdout(t, func() {
		runHire(ui, dbPath, false, false, auditOptions{}, []string{"assess", "1,5,8", "--goals"})
	})
	if !strings.Contains(outSquadGoals, "mock-ui-goal") {
		t.Errorf("expected mock-ui-goal in squad goals output, got: %s", outSquadGoals)
	}
	if !strings.Contains(outSquadGoals, "Squad Size:     3 personas") {
		t.Errorf("expected Squad Size 3, got: %s", outSquadGoals)
	}

	// 5. Test JSON output: `sila hire assess --goals --json`
	outJSON := captureStdout(t, func() {
		runHire(ui, dbPath, true, false, auditOptions{}, []string{"assess", "--goals"})
	})
	if !strings.Contains(outJSON, `"goals_coverage"`) {
		t.Errorf("expected JSON to contain goals_coverage, got: %s", outJSON)
	}
	if !strings.Contains(outJSON, `"roadmap_alignment"`) {
		t.Errorf("expected JSON to contain roadmap_alignment, got: %s", outJSON)
	}

	// 6. Test TOON output: `sila hire assess --goals --toon`
	outTOON := captureStdout(t, func() {
		runHire(ui, dbPath, false, true, auditOptions{}, []string{"assess", "--goals"})
	})
	if !strings.Contains(outTOON, "goals_coverage[") {
		t.Errorf("expected TOON to contain goals_coverage[, got: %s", outTOON)
	}
}
