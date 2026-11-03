package learn

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateManagedRulesBlock(t *testing.T) {
	learnings := []OperationalLearning{
		{
			Domain:        DomainGo,
			Title:         "Gofmt Formatting Discipline",
			GuardrailRule: "Always format Go source code with gofmt -w prior to tests",
		},
		{
			Domain:        DomainD1,
			Title:         "Cloudflare D1 Query Batching",
			GuardrailRule: "Always batch D1 queries using env.DB.batch",
		},
		{
			Domain:        DomainTypeScript,
			Title:         "ESM Import Extensions",
			GuardrailRule: "Ensure relative imports include explicit .js extension in ESM",
		},
	}

	// 1. Export all domains
	blockAll := GenerateManagedRulesBlock(learnings, nil)
	if !strings.Contains(blockAll, ManagedBlockBegin) || !strings.Contains(blockAll, ManagedBlockEnd) {
		t.Fatal("missing managed block delimiters")
	}
	if !strings.Contains(blockAll, "Golang Engineering Disciplines") ||
		!strings.Contains(blockAll, "Cloudflare D1 & SQLite Data Tier") ||
		!strings.Contains(blockAll, "TypeScript & Modern Frontend") {
		t.Errorf("block missing domain sections: %s", blockAll)
	}

	// 2. Export with domain filter
	blockGoOnly := GenerateManagedRulesBlock(learnings, []string{DomainGo})
	if !strings.Contains(blockGoOnly, "Golang Engineering Disciplines") {
		t.Errorf("expected golang section in filtered block: %s", blockGoOnly)
	}
	if strings.Contains(blockGoOnly, "Cloudflare D1 & SQLite Data Tier") {
		t.Errorf("expected D1 to be filtered out: %s", blockGoOnly)
	}
}

func TestInjectManagedRules_IdempotentAndNonDestructive(t *testing.T) {
	original := `# Project Readme

This is a custom project guideline.

<!-- sila:begin (operational-rules) -->
old rules here
<!-- sila:end -->

Footer text that must be preserved.
`
	newBlock := "<!-- sila:begin (operational-rules) -->\nnew rules\n<!-- sila:end -->"

	injected := InjectManagedRules(original, newBlock)
	if !strings.HasPrefix(injected, "# Project Readme\n\nThis is a custom project guideline.\n\n") {
		t.Errorf("preamble was mutated: %s", injected)
	}
	if !strings.HasSuffix(injected, "\n\nFooter text that must be preserved.\n") {
		t.Errorf("suffix was mutated: %s", injected)
	}
	if !strings.Contains(injected, "new rules") || strings.Contains(injected, "old rules here") {
		t.Errorf("managed block replacement failed: %s", injected)
	}
}

func TestExportRulesToFile_EndToEnd(t *testing.T) {
	tmpDir := t.TempDir()
	targetPath := filepath.Join(tmpDir, "AGENTS.md")

	initialContent := "# Canonical Rules\n\nStay focused.\n"
	if err := os.WriteFile(targetPath, []byte(initialContent), 0644); err != nil {
		t.Fatal(err)
	}

	learnings := BuiltinFoundationalLearnings()

	// 1. Initial export
	if err := ExportRulesToFile(targetPath, learnings, []string{DomainSecurity, DomainGo}); err != nil {
		t.Fatalf("export error: %v", err)
	}

	content1, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(content1), "# Canonical Rules\n\nStay focused.\n\n<!-- sila:begin (operational-rules) -->") {
		t.Fatalf("unexpected content after initial export: %s", string(content1))
	}
	if !strings.Contains(string(content1), "Synthetic Secret Sanitization in Test Fixtures") {
		t.Fatalf("expected security rule in export: %s", string(content1))
	}

	// 2. Re-export update (idempotent replacement)
	if err := ExportRulesToFile(targetPath, learnings, []string{DomainCICD}); err != nil {
		t.Fatalf("re-export error: %v", err)
	}

	content2, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	// Verify only one begin/end block exists
	if strings.Count(string(content2), ManagedBlockBegin) != 1 || strings.Count(string(content2), ManagedBlockEnd) != 1 {
		t.Fatalf("expected exactly one managed block after re-export: %s", string(content2))
	}
	if !strings.Contains(string(content2), "Two-Stage Atomic Commit Protocol") {
		t.Fatalf("expected updated CICD rule: %s", string(content2))
	}
}
