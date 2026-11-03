package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func TestDoctorSkillsCLI(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "doctor_test.db")

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}

	p, err := st.EnsureProject("/w/testproj")
	if err != nil {
		t.Fatalf("EnsureProject: %v", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	_, _, _ = st.WriteRun("", "git", "1", []store.LoadMessage{
		{
			ProjectID:   p.ID,
			ContentHash: "h1",
			TS:          now,
			Role:        "assistant",
			Content:     `ToolName: "search_knowledge"`,
			Tool:        "devin",
		},
		{
			ProjectID:   p.ID,
			ContentHash: "h2",
			TS:          now,
			Role:        "assistant",
			Content:     `<invoke name="run_command">ls</invoke>`,
			Tool:        "claude-code",
		},
	})
	st.Close()

	ui := render.New(nil, false)

	// 1. Test doctor with skills flag in JSON mode
	jsonOut := captureStdout(t, func() {
		runDoctor(ui, dbPath, p.ID, false, true, false, true, false, false, false, false)
	})
	if !strings.Contains(jsonOut, `"total_registered":`) {
		t.Errorf("expected JSON total_registered field, got: %s", jsonOut)
	}

	// 2. Test doctor with skills flag in TOON mode
	toonOut := captureStdout(t, func() {
		runDoctor(ui, dbPath, p.ID, false, false, true, true, false, false, false, false)
	})
	if !strings.Contains(toonOut, "skill_bloat_audit:") {
		t.Errorf("expected TOON skill_bloat_audit object, got: %s", toonOut)
	}

	// 3. Test standard doctor run includes Skills bloat check and Covenant check
	stdOut := captureStdout(t, func() {
		runDoctor(ui, dbPath, p.ID, false, true, false, false, false, false, false, false)
	})
	if !strings.Contains(stdOut, "Agent Skill") || !strings.Contains(stdOut, "MCP Bloat") {
		t.Errorf("expected doctor standard checks to include Agent Skill & MCP Bloat, got: %s", stdOut)
	}
	if !strings.Contains(stdOut, "Digital Data Sovereignty") {
		t.Errorf("expected doctor standard checks to include Digital Data Sovereignty, got: %s", stdOut)
	}
}

func TestDoctorCovenantCLI(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "covenant_doctor_test.db")

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	p, _ := st.EnsureProject("/w/covproj")
	st.Close()

	ui := render.New(nil, false)

	// 1. JSON mode
	jsonOut := captureStdout(t, func() {
		runDoctor(ui, dbPath, p.ID, false, true, false, false, true, false, false, false)
	})
	if !strings.Contains(jsonOut, `"overall": "COMPLIANT"`) && !strings.Contains(jsonOut, `"overall":"COMPLIANT"`) {
		t.Errorf("expected JSON COMPLIANT, got: %s", jsonOut)
	}
	if !strings.Contains(jsonOut, `"pillars":`) {
		t.Errorf("expected JSON pillars array, got: %s", jsonOut)
	}

	// 2. TOON mode
	toonOut := captureStdout(t, func() {
		runDoctor(ui, dbPath, p.ID, false, false, true, false, true, false, false, false)
	})
	if !strings.Contains(toonOut, "covenant_summary:") {
		t.Errorf("expected TOON covenant_summary object, got: %s", toonOut)
	}
	if !strings.Contains(toonOut, "local_custody") {
		t.Errorf("expected TOON local_custody pillar, got: %s", toonOut)
	}

	// 3. Human mode
	humanOut := captureStdout(t, func() {
		runDoctor(ui, dbPath, p.ID, false, false, false, false, true, false, false, false)
	})
	if !strings.Contains(humanOut, "sila · doctor --covenant") {
		t.Errorf("expected human header, got: %s", humanOut)
	}
	if !strings.Contains(humanOut, "[COMPLIANT]") {
		t.Errorf("expected COMPLIANT badge, got: %s", humanOut)
	}
	if !strings.Contains(humanOut, "Right to Absolute Local Custody") {
		t.Errorf("expected Pillar 1, got: %s", humanOut)
	}
}

func TestDoctorSkillsHumanOutput(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "human_doctor_test.db")

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}

	p, _ := st.EnsureProject("/w/testproj")
	now := time.Now().UTC().Format(time.RFC3339)
	_, _, _ = st.WriteRun("", "git", "1", []store.LoadMessage{
		{
			ProjectID:   p.ID,
			ContentHash: "h1",
			TS:          now,
			Role:        "assistant",
			Content:     `ToolName: "prime_context"`,
			Tool:        "agy",
		},
	})

	ui := render.New(nil, false)

	humanOut := captureStdout(t, func() {
		runDoctorSkills(ui, st, p.ID, false, false)
	})
	st.Close()

	if !strings.Contains(humanOut, "sila · doctor --skills") {
		t.Errorf("output missing header: %s", humanOut)
	}
	if !strings.Contains(humanOut, "registered tools") {
		t.Errorf("output missing tools summary: %s", humanOut)
	}
}

func TestDoctor_EdgeAudit(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "edge_doctor_test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	p, _ := st.EnsureProject("/w/edgeproj")
	st.Close()

	ui := render.New(nil, false)

	// 1. JSON mode against non-worker repo (should emit SKIPPED)
	jsonOut := captureStdout(t, func() {
		runDoctor(ui, dbPath, p.ID, false, true, false, false, false, true, false, false)
	})
	if !strings.Contains(jsonOut, `"status": "SKIPPED"`) && !strings.Contains(jsonOut, `"status":"SKIPPED"`) {
		t.Errorf("expected JSON SKIPPED status for non-worker repo, got: %s", jsonOut)
	}

	// 2. TOON mode against non-worker repo
	toonOut := captureStdout(t, func() {
		runDoctor(ui, dbPath, p.ID, false, false, true, false, false, true, false, false)
	})
	if !strings.Contains(toonOut, "edge_audit_summary:") {
		t.Errorf("expected TOON edge_audit_summary, got: %s", toonOut)
	}
	if !strings.Contains(toonOut, "status: SKIPPED") {
		t.Errorf("expected TOON status: SKIPPED, got: %s", toonOut)
	}

	// 3. Human mode against non-worker repo
	humanOut := captureStdout(t, func() {
		runDoctor(ui, dbPath, p.ID, false, false, false, false, false, true, false, false)
	})
	if !strings.Contains(humanOut, "sila · doctor --edge") {
		t.Errorf("expected human header, got: %s", humanOut)
	}
	if !strings.Contains(humanOut, "[SKIPPED]") {
		t.Errorf("expected [SKIPPED] badge, got: %s", humanOut)
	}
}

func TestDoctor_RuleBudgetAudit(t *testing.T) {
	tempDir := t.TempDir()

	findCheck := func(rep *store.DoctorReport, name string) (store.CheckResult, bool) {
		for _, c := range rep.Checks {
			if c.Name == name {
				return c, true
			}
		}
		return store.CheckResult{}, false
	}

	// 1. Clean directory with valid AGENTS.md and GEMINI.md symlink
	if err := os.WriteFile(filepath.Join(tempDir, "AGENTS.md"), []byte("# Clean Rules\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("AGENTS.md", filepath.Join(tempDir, "GEMINI.md")); err != nil {
		t.Fatal(err)
	}

	rep := &store.DoctorReport{Checks: make([]store.CheckResult, 0)}
	appendRulesBudgetDoctorCheck(rep, tempDir, false)

	check, ok := findCheck(rep, "Rule Budget & Truncation Sentry")
	if !ok {
		t.Fatalf("expected 'Rule Budget & Truncation Sentry' check to exist")
	}
	if check.Status != store.StatusPass {
		t.Errorf("expected pass for clean rules, got %s: %s", check.Status, check.Message)
	}

	// 2. Oversized AGENTS.md (> 24,000 bytes) fails
	oversized := strings.Repeat("A", 25000)
	if err := os.WriteFile(filepath.Join(tempDir, "AGENTS.md"), []byte(oversized), 0644); err != nil {
		t.Fatal(err)
	}

	repFail := &store.DoctorReport{Checks: make([]store.CheckResult, 0)}
	appendRulesBudgetDoctorCheck(repFail, tempDir, false)

	checkFail, ok := findCheck(repFail, "Rule Budget & Truncation Sentry")
	if !ok {
		t.Fatalf("expected check to exist for oversized rules")
	}
	if checkFail.Status != store.StatusFail {
		t.Errorf("expected fail for oversized rules, got %s", checkFail.Status)
	}
	if !strings.Contains(checkFail.Message, "exceed 24 KB budget") {
		t.Errorf("expected message to mention 24 KB budget, got %s", checkFail.Message)
	}

	// 3. Broken symlink with repair=true gets repaired
	// Restore AGENTS.md to clean size
	if err := os.WriteFile(filepath.Join(tempDir, "AGENTS.md"), []byte("# Restored Rules\n"), 0644); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(filepath.Join(tempDir, "GEMINI.md"))
	if err := os.Symlink("non_existent_file.md", filepath.Join(tempDir, "GEMINI.md")); err != nil {
		t.Fatal(err)
	}

	repRepair := &store.DoctorReport{Checks: make([]store.CheckResult, 0)}
	appendRulesBudgetDoctorCheck(repRepair, tempDir, true)

	if !repRepair.RepairExecuted {
		t.Errorf("expected RepairExecuted to be true after repairing broken symlink")
	}
	checkRepaired, _ := findCheck(repRepair, "Rule Budget & Truncation Sentry")
	if checkRepaired.Status != store.StatusPass {
		t.Errorf("expected pass after repairing broken symlink, got %s", checkRepaired.Status)
	}
}

func TestDoctor_CFCleanAudit(t *testing.T) {
	tempDir := t.TempDir()

	findCheck := func(rep *store.DoctorReport, name string) (store.CheckResult, bool) {
		for _, c := range rep.Checks {
			if c.Name == name {
				return c, true
			}
		}
		return store.CheckResult{}, false
	}

	// 1. Non-worker project skips check
	repNonWorker := &store.DoctorReport{Checks: make([]store.CheckResult, 0)}
	appendCFCleanDoctorCheck(repNonWorker, tempDir, false)
	if _, ok := findCheck(repNonWorker, "Cloudflare Credential Sanitizer (cfclean)"); ok {
		t.Errorf("expected non-worker project not to add cfclean check")
	}

	// 2. Add wrangler.jsonc and init git
	wranglerPath := filepath.Join(tempDir, "wrangler.jsonc")
	if err := os.WriteFile(wranglerPath, []byte(`{"name": "doctor-worker"}`), 0644); err != nil {
		t.Fatal(err)
	}
	cmdInit := exec.Command("git", "init")
	cmdInit.Dir = tempDir
	if err := cmdInit.Run(); err != nil {
		t.Skip("git init not available; skipping")
	}

	// 3. Worker project with unconfigured filters warns
	repWarn := &store.DoctorReport{Checks: make([]store.CheckResult, 0)}
	appendCFCleanDoctorCheck(repWarn, tempDir, false)
	chkWarn, ok := findCheck(repWarn, "Cloudflare Credential Sanitizer (cfclean)")
	if !ok {
		t.Fatalf("expected cfclean check to be present for worker project")
	}
	if chkWarn.Status != store.StatusWarn {
		t.Errorf("expected warn for unconfigured worker repo, got %s: %s", chkWarn.Status, chkWarn.Message)
	}

	// 4. Set clean but leave smudge as cat -> warns about smudge/hydration
	_ = exec.Command("git", "-C", tempDir, "config", "filter.cfclean.clean", "cfclean").Run()
	_ = exec.Command("git", "-C", tempDir, "config", "filter.cfclean.smudge", "cat").Run()
	_ = os.WriteFile(filepath.Join(tempDir, ".gitattributes"), []byte("wrangler.jsonc filter=cfclean\n"), 0644)

	repSmudgeWarn := &store.DoctorReport{Checks: make([]store.CheckResult, 0)}
	appendCFCleanDoctorCheck(repSmudgeWarn, tempDir, false)
	chkSmudgeWarn, _ := findCheck(repSmudgeWarn, "Cloudflare Credential Sanitizer (cfclean)")
	if chkSmudgeWarn.Status != store.StatusWarn {
		t.Errorf("expected warn when smudge is set to cat, got %s", chkSmudgeWarn.Status)
	}
	if !strings.Contains(chkSmudgeWarn.Message, "smudge filter is not configured") &&
		!strings.Contains(chkSmudgeWarn.Message, "hydration disabled") {
		t.Errorf("expected message to mention smudge filter or hydration, got: %s", chkSmudgeWarn.Message)
	}

	// 5. Run with repair=true -> repairs to pass
	repRepair := &store.DoctorReport{Checks: make([]store.CheckResult, 0)}
	appendCFCleanDoctorCheck(repRepair, tempDir, true)
	if !repRepair.RepairExecuted {
		t.Errorf("expected RepairExecuted to be true")
	}
	chkPass, _ := findCheck(repRepair, "Cloudflare Credential Sanitizer (cfclean)")
	if chkPass.Status != store.StatusPass {
		t.Errorf("expected pass after repair, got %s: %s", chkPass.Status, chkPass.Message)
	}
}

func TestDoctorDebtCheck(t *testing.T) {
	tempDir := t.TempDir()

	// 1. Clean repo
	repClean := &store.DoctorReport{Checks: make([]store.CheckResult, 0)}
	appendDebtDoctorCheck(repClean, tempDir)
	if len(repClean.Checks) == 0 {
		t.Fatalf("expected debt check appended")
	}
	chkClean := repClean.Checks[len(repClean.Checks)-1]
	if chkClean.Status != store.StatusPass {
		t.Errorf("expected pass for clean repo, got %s: %s", chkClean.Status, chkClean.Message)
	}

	// 2. Repo with healthy shortcut debt
	healthyFile := filepath.Join(tempDir, "service.go")
	_ = os.WriteFile(healthyFile, []byte("// shortcut: ceiling: 100 req/sec, trigger: upgrade to redis\n"), 0644)
	repHealthy := &store.DoctorReport{Checks: make([]store.CheckResult, 0)}
	appendDebtDoctorCheck(repHealthy, tempDir)
	chkHealthy := repHealthy.Checks[len(repHealthy.Checks)-1]
	if chkHealthy.Status != store.StatusPass {
		t.Errorf("expected pass for healthy shortcut, got %s: %s", chkHealthy.Status, chkHealthy.Message)
	}

	// 3. Repo with rot risk (shortcut without upgrade trigger)
	rotFile := filepath.Join(tempDir, "legacy.py")
	_ = os.WriteFile(rotFile, []byte("# shortcut: only works on local machine\n"), 0644)
	repRot := &store.DoctorReport{Checks: make([]store.CheckResult, 0)}
	appendDebtDoctorCheck(repRot, tempDir)
	chkRot := repRot.Checks[len(repRot.Checks)-1]
	if chkRot.Status != store.StatusWarn {
		t.Errorf("expected warn for rot risk, got %s: %s", chkRot.Status, chkRot.Message)
	}
}

func TestDoctorDebtCLI(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "debt_doctor_test.db")

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	p, _ := st.EnsureProject(tempDir)
	st.Close()

	// Create sample shortcut file in tempDir
	sampleFile := filepath.Join(tempDir, "queue.go")
	_ = os.WriteFile(sampleFile, []byte("// shortcut: max 100 items, trigger: kafka streaming [goal: queue-stream]\n"), 0644)

	ui := render.New(nil, false)

	// 1. Test JSON mode
	jsonOut := captureStdout(t, func() {
		runDoctor(ui, dbPath, p.ID, false, true, false, false, false, false, true, false)
	})
	if !strings.Contains(jsonOut, `"total_items":`) || !strings.Contains(jsonOut, `"healthy_count":`) {
		t.Errorf("expected JSON debt report fields, got: %s", jsonOut)
	}

	// 2. Test TOON mode
	toonOut := captureStdout(t, func() {
		runDoctor(ui, dbPath, p.ID, false, false, true, false, false, false, true, false)
	})
	if !strings.Contains(toonOut, "debt_summary:") || !strings.Contains(toonOut, "debt_items[") {
		t.Errorf("expected TOON debt summary and items, got: %s", toonOut)
	}

	// 3. Test Human mode
	humanOut := captureStdout(t, func() {
		runDoctor(ui, dbPath, p.ID, false, false, false, false, false, false, true, false)
	})
	if !strings.Contains(humanOut, "sila · doctor --debt") {
		t.Errorf("expected human debt header, got: %s", humanOut)
	}
	if !strings.Contains(humanOut, "queue-stream") {
		t.Errorf("expected linked goal queue-stream in human output, got: %s", humanOut)
	}
}
