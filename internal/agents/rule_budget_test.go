package agents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectRuleBudgetCeiling(t *testing.T) {
	tmp := t.TempDir()
	agentsPath := filepath.Join(tmp, "AGENTS.md")

	// Create custom content of ~23,500 bytes so adding managed block exceeds 24,000 bytes.
	custom := "# Custom Project Directives\n\n" + strings.Repeat("Key project instruction that must never be discarded.\n", 430)
	if len(custom) < 23000 || len(custom) > 23800 {
		t.Fatalf("custom content length unexpected: %d", len(custom))
	}

	if err := os.WriteFile(agentsPath, []byte(custom), 0644); err != nil {
		t.Fatal(err)
	}

	opts := CompileOptions{
		RepoRoot:    tmp,
		ProjectName: "budget-test",
	}

	// 1. Regular run: must abort and preserve file untouched
	res, err := DeployTargets(tmp, opts, []string{"all"})
	if err != nil {
		t.Fatalf("unexpected DeployTargets hard failure: %v", err)
	}

	if len(res.Errors) == 0 {
		t.Fatalf("expected budget error in res.Errors, got none")
	}

	foundBudgetErr := false
	for _, e := range res.Errors {
		if strings.Contains(e, "exceeds Antigravity 24 KB ceiling") {
			foundBudgetErr = true
			break
		}
	}
	if !foundBudgetErr {
		t.Errorf("expected 24 KB budget error message, got: %v", res.Errors)
	}

	diskData, err := os.ReadFile(agentsPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(diskData) != custom {
		t.Errorf("file on disk was modified despite budget violation! len before: %d, len after: %d", len(custom), len(diskData))
	}

	// 2. Preview run: must report error action in plan
	opts.Preview = true
	resPrev, err := DeployTargets(tmp, opts, []string{"all"})
	if err != nil {
		t.Fatalf("unexpected preview hard failure: %v", err)
	}
	if len(resPrev.Errors) == 0 {
		t.Fatalf("expected budget error in preview resPrev.Errors")
	}

	foundPlanError := false
	for _, p := range resPrev.Plan {
		if p.Path == "AGENTS.md" && p.Action == "error" {
			foundPlanError = true
			if !strings.Contains(p.Detail, "exceeds Antigravity 24 KB ceiling") {
				t.Errorf("plan detail missing ceiling info: %s", p.Detail)
			}
			break
		}
	}
	if !foundPlanError {
		t.Errorf("expected plan action 'error' for AGENTS.md, got plan: %+v", resPrev.Plan)
	}
}

func TestDeployTargetsRuleBudgetGuard(t *testing.T) {
	// Alias for T-002 verification command
	TestProjectRuleBudgetCeiling(t)
}

func TestDeployTargetsConciseTriggerBlock(t *testing.T) {
	tmp := t.TempDir()
	opts := CompileOptions{
		RepoRoot:    tmp,
		ProjectName: "concise-test",
	}

	res, err := DeployTargets(tmp, opts, []string{"all"})
	if err != nil {
		t.Fatalf("DeployTargets failed: %v", err)
	}
	if len(res.Errors) > 0 {
		t.Fatalf("unexpected errors: %v", res.Errors)
	}

	data, err := os.ReadFile(filepath.Join(tmp, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	if len(content) > 3500 {
		t.Errorf("project AGENTS.md size %d bytes exceeds 3,500 byte budget", len(content))
	}

	// Must contain concise trigger contract & safety rails
	if !strings.Contains(content, "## 0. Safety Rails") {
		t.Errorf("missing Safety Rails section")
	}
	if !strings.Contains(content, "## 1. Working Relationship & Discipline") {
		t.Errorf("missing Working Relationship & Discipline section")
	}
	if !strings.Contains(content, "## Sila Trigger Contract") {
		t.Errorf("missing Sila Trigger Contract section")
	}

	// Must NOT contain global engineering hub bloat
	if strings.Contains(content, "## Plain English") {
		t.Errorf("contains global Plain English guide which should be excluded from project block")
	}
	if strings.Contains(content, "## Working with Node.js and npm") {
		t.Errorf("contains global Node.js rules which should be excluded from project block")
	}
	if strings.Contains(content, "## Working with Git") {
		t.Errorf("contains global Git rules which should be excluded from project block")
	}
}

func TestDeployTargetsNoUnpromptedHarnessFiles(t *testing.T) {
	tmp := t.TempDir()
	opts := CompileOptions{
		RepoRoot:    tmp,
		ProjectName: "clean-test",
	}

	res, err := DeployTargets(tmp, opts, []string{"all"})
	if err != nil {
		t.Fatalf("DeployTargets failed: %v", err)
	}
	if len(res.Errors) > 0 {
		t.Fatalf("unexpected errors: %v", res.Errors)
	}

	// AGENTS.md must exist
	if _, err := os.Stat(filepath.Join(tmp, "AGENTS.md")); err != nil {
		t.Errorf("AGENTS.md missing: %v", err)
	}

	// CLAUDE.md and GEMINI.md must NOT exist
	if _, err := os.Lstat(filepath.Join(tmp, "CLAUDE.md")); !os.IsNotExist(err) {
		t.Errorf("unprompted CLAUDE.md was created on clean repo")
	}
	if _, err := os.Lstat(filepath.Join(tmp, "GEMINI.md")); !os.IsNotExist(err) {
		t.Errorf("unprompted GEMINI.md was created on clean repo")
	}

	// Plan should explicitly record skip for both
	claudeSkipped := false
	geminiSkipped := false
	for _, p := range res.Plan {
		if p.Path == "CLAUDE.md" && p.Action == "skip" {
			claudeSkipped = true
		}
		if p.Path == "GEMINI.md" && p.Action == "skip" {
			geminiSkipped = true
		}
	}
	if !claudeSkipped {
		t.Errorf("expected plan skip for CLAUDE.md")
	}
	if !geminiSkipped {
		t.Errorf("expected plan skip for GEMINI.md")
	}
}

func TestDeployTargetsSkipsUnpromptedHarnesses(t *testing.T) {
	// Alias for T-003 verification command
	TestDeployTargetsNoUnpromptedHarnessFiles(t)
}

func TestDeployTargetsIdempotence(t *testing.T) {
	tmp := t.TempDir()
	userRules := "# My Project\n\nStrict test coverage required.\n"
	if err := os.WriteFile(filepath.Join(tmp, "AGENTS.md"), []byte(userRules), 0644); err != nil {
		t.Fatal(err)
	}

	opts := CompileOptions{
		RepoRoot:    tmp,
		ProjectName: "idemp-test",
	}

	// Run 1: initial deployment
	res1, err := DeployTargets(tmp, opts, []string{"all"})
	if err != nil {
		t.Fatalf("run 1 failed: %v", err)
	}
	if len(res1.Errors) > 0 {
		t.Fatalf("run 1 had errors: %v", res1.Errors)
	}

	data1, err := os.ReadFile(filepath.Join(tmp, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}

	// Run 2: consecutive deployment with same options
	res2, err := DeployTargets(tmp, opts, []string{"all"})
	if err != nil {
		t.Fatalf("run 2 failed: %v", err)
	}
	if len(res2.Errors) > 0 {
		t.Fatalf("run 2 had errors: %v", res2.Errors)
	}

	data2, err := os.ReadFile(filepath.Join(tmp, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}

	if string(data1) != string(data2) {
		t.Errorf("idempotency violation: content changed between run 1 and run 2")
	}

	if len(res2.FilesWritten) != 0 {
		t.Errorf("expected 0 files written on second run, got: %v", res2.FilesWritten)
	}

	// AGENTS.md action should be "skip" on run 2
	for _, p := range res2.Plan {
		if p.Path == "AGENTS.md" && p.Action != "skip" {
			t.Errorf("expected AGENTS.md action 'skip' on run 2, got %q", p.Action)
		}
	}
}

func TestHasHarnessIntegrations(t *testing.T) {
	if HasGeminiIntegration("") {
		t.Errorf("HasGeminiIntegration(\"\") should be false")
	}
	if HasClaudeIntegration("") {
		t.Errorf("HasClaudeIntegration(\"\") should be false")
	}

	cleanDir := t.TempDir()
	if HasGeminiIntegration(cleanDir) {
		t.Errorf("clean directory should not have Gemini integration")
	}
	if HasClaudeIntegration(cleanDir) {
		t.Errorf("clean directory should not have Claude integration")
	}

	geminiDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(geminiDir, ".gemini"), 0755); err != nil {
		t.Fatal(err)
	}
	if !HasGeminiIntegration(geminiDir) {
		t.Errorf("expected HasGeminiIntegration to be true with .gemini dir")
	}
	if HasClaudeIntegration(geminiDir) {
		t.Errorf("expected HasClaudeIntegration to be false with only .gemini dir")
	}

	claudeDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(claudeDir, "CLAUDE.md"), []byte("# Claude\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if !HasClaudeIntegration(claudeDir) {
		t.Errorf("expected HasClaudeIntegration to be true with CLAUDE.md file")
	}
	if HasGeminiIntegration(claudeDir) {
		t.Errorf("expected HasGeminiIntegration to be false with only CLAUDE.md file")
	}
}

func TestComposeHelpersDirect(t *testing.T) {
	if !hasLeadingH1("# Title\nContent") {
		t.Errorf("expected hasLeadingH1 to be true for '# Title'")
	}
	if hasLeadingH1("Intro\n# Title") {
		t.Errorf("expected hasLeadingH1 to be false when first line is not H1")
	}
	if hasLeadingH1("") {
		t.Errorf("expected hasLeadingH1 to be false for empty string")
	}

	stripped := stripLeadingH1("# Heading\nBody text\n")
	if strings.Contains(stripped, "# Heading") {
		t.Errorf("stripLeadingH1 did not remove H1, got: %q", stripped)
	}
	if !strings.Contains(stripped, "Body text") {
		t.Errorf("stripLeadingH1 removed body text")
	}
	if stripLeadingH1("No heading") != "No heading" {
		t.Errorf("stripLeadingH1 modified text without H1")
	}

	marked := "Line 1\n<!-- sila:begin (foo) -->\nLine 2\n<!-- sila:end -->\nLine 3"
	unmarked := stripOwnedMarkers(marked)
	if strings.Contains(unmarked, "sila:begin") || strings.Contains(unmarked, "sila:end") {
		t.Errorf("stripOwnedMarkers did not strip markers, got: %q", unmarked)
	}
	if !strings.Contains(unmarked, "Line 1") || !strings.Contains(unmarked, "Line 2") || !strings.Contains(unmarked, "Line 3") {
		t.Errorf("stripOwnedMarkers dropped content lines")
	}
}
