package gate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuditRuleBudget_Clean(t *testing.T) {
	dir := t.TempDir()

	agentsContent := "# Test Rules\n\n- rule 1\n- rule 2\n"
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(agentsContent), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("AGENTS.md", filepath.Join(dir, "GEMINI.md")); err != nil {
		t.Fatal(err)
	}

	res := AuditRuleBudget(dir)
	if !res.Clean {
		t.Fatalf("expected clean result, got errors: %v", res.Errors)
	}
	if len(res.Errors) > 0 {
		t.Fatalf("expected 0 errors, got %v", res.Errors)
	}
}

func TestAuditRuleBudget_OversizedAgents(t *testing.T) {
	dir := t.TempDir()

	// Create 25 KB AGENTS.md (exceeds 24,000 bytes)
	oversized := strings.Repeat("A", 25000)
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(oversized), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("AGENTS.md", filepath.Join(dir, "GEMINI.md")); err != nil {
		t.Fatal(err)
	}

	res := AuditRuleBudget(dir)
	if res.Clean {
		t.Fatal("expected failure on 25KB AGENTS.md, got clean")
	}

	foundTruncationError := false
	for _, e := range res.Errors {
		if strings.Contains(e, "exceeds Antigravity 24 KB ceiling") {
			foundTruncationError = true
			break
		}
	}
	if !foundTruncationError {
		t.Fatalf("expected truncation error, got: %v", res.Errors)
	}
}

func TestAuditRuleBudget_NearCapWarning(t *testing.T) {
	dir := t.TempDir()

	// Create 21 KB AGENTS.md (between 20,000 and 24,000 bytes)
	nearCap := strings.Repeat("B", 21000)
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(nearCap), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("AGENTS.md", filepath.Join(dir, "GEMINI.md")); err != nil {
		t.Fatal(err)
	}

	res := AuditRuleBudget(dir)
	if !res.Clean {
		t.Fatalf("expected clean result (warnings only), got errors: %v", res.Errors)
	}

	foundWarning := false
	for _, w := range res.Warnings {
		if strings.Contains(w, "is approaching 24 KB ceiling") {
			foundWarning = true
			break
		}
	}
	if !foundWarning {
		t.Fatalf("expected near-cap warning, got: %v", res.Warnings)
	}
}

func TestAuditRuleBudget_BrokenSymlink(t *testing.T) {
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("# Rules\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// Symlink to nonexistent target
	if err := os.Symlink("NONEXISTENT.md", filepath.Join(dir, "GEMINI.md")); err != nil {
		t.Fatal(err)
	}

	res := AuditRuleBudget(dir)
	if res.Clean {
		t.Fatal("expected failure on broken GEMINI.md symlink, got clean")
	}

	foundBrokenLink := false
	for _, e := range res.Errors {
		if strings.Contains(e, "broken symlink") {
			foundBrokenLink = true
			break
		}
	}
	if !foundBrokenLink {
		t.Fatalf("expected broken symlink error, got: %v", res.Errors)
	}
}

func TestAuditRuleBudget_IncludeDirectives(t *testing.T) {
	dir := t.TempDir()

	helperPath := filepath.Join(dir, "SUB_RULES.md")
	if err := os.WriteFile(helperPath, []byte("# Sub\n"), 0644); err != nil {
		t.Fatal(err)
	}

	content := "# Rules\n\n@[Valid Helper](SUB_RULES.md)\n@[Missing Helper](MISSING.md)\n"
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	res := AuditRuleBudget(dir)
	if res.Clean {
		t.Fatal("expected error on missing include directive, got clean")
	}

	foundMissingInclude := false
	for _, e := range res.Errors {
		if strings.Contains(e, "included file \"MISSING.md\" does not exist") {
			foundMissingInclude = true
			break
		}
	}
	if !foundMissingInclude {
		t.Fatalf("expected missing include error, got: %v", res.Errors)
	}
}

func TestRepairRuleBudget(t *testing.T) {
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("# Canonical Rules\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// 1. On clean repo without Gemini or Claude integration, RepairRuleBudget does not create GEMINI.md unprompted
	repaired, err := RepairRuleBudget(dir)
	if err != nil {
		t.Fatalf("RepairRuleBudget failed: %v", err)
	}
	if len(repaired) != 0 {
		t.Fatalf("expected 0 repairs on clean non-gemini repository, got %v", repaired)
	}
	if _, err := os.Lstat(filepath.Join(dir, "GEMINI.md")); !os.IsNotExist(err) {
		t.Fatalf("GEMINI.md should not be created unprompted")
	}

	// 2. When Gemini integration is present (.gemini directory exists), RepairRuleBudget creates GEMINI.md
	if err := os.Mkdir(filepath.Join(dir, ".gemini"), 0755); err != nil {
		t.Fatal(err)
	}
	repairedWithGemini, err := RepairRuleBudget(dir)
	if err != nil {
		t.Fatalf("RepairRuleBudget failed with .gemini: %v", err)
	}
	if len(repairedWithGemini) == 0 {
		t.Fatal("expected at least 1 repair with .gemini, got 0")
	}
	geminiTarget, err := os.Readlink(filepath.Join(dir, "GEMINI.md"))
	if err != nil || geminiTarget != "AGENTS.md" {
		t.Fatalf("expected GEMINI.md -> AGENTS.md, got target %q, err %v", geminiTarget, err)
	}

	// 3. Repair broken symlink
	_ = os.Remove(filepath.Join(dir, "GEMINI.md"))
	_ = os.Symlink("BROKEN.md", filepath.Join(dir, "GEMINI.md"))

	repaired2, err := RepairRuleBudget(dir)
	if err != nil {
		t.Fatalf("RepairRuleBudget failed: %v", err)
	}
	if len(repaired2) == 0 {
		t.Fatal("expected repair on broken symlink, got 0")
	}
	geminiTarget2, _ := os.Readlink(filepath.Join(dir, "GEMINI.md"))
	if geminiTarget2 != "AGENTS.md" {
		t.Fatalf("expected repaired GEMINI.md -> AGENTS.md, got %q", geminiTarget2)
	}
}
