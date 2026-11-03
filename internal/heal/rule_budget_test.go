package heal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHealRespectsProjectRuleBounds(t *testing.T) {
	root := t.TempDir()

	// Scaffold a minimal Node.js project (package.json)
	packageJSON := `{"name": "my-service", "version": "1.0.0"}`
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(packageJSON), 0644); err != nil {
		t.Fatal(err)
	}

	// Create custom AGENTS.md
	customRules := "# My Service\n\nAlways run pnpm test before submitting changes.\n"
	agentsPath := filepath.Join(root, "AGENTS.md")
	if err := os.WriteFile(agentsPath, []byte(customRules), 0644); err != nil {
		t.Fatal(err)
	}

	// Run ApplyHeal
	plan, err := ApplyHeal(root)
	if err != nil {
		t.Fatalf("ApplyHeal failed: %v", err)
	}
	if plan == nil {
		t.Fatal("expected non-nil plan")
	}

	data1, err := os.ReadFile(agentsPath)
	if err != nil {
		t.Fatal(err)
	}
	content1 := string(data1)

	// 1. Budget check: project AGENTS.md must remain concise (< 3,500 bytes)
	if len(content1) > 3500 {
		t.Errorf("AGENTS.md size %d bytes exceeds 3,500 byte budget", len(content1))
	}

	// 2. Custom rules preserved
	if !strings.Contains(content1, "Always run pnpm test before submitting changes.") {
		t.Errorf("custom rules were lost during heal")
	}

	// 3. Delimited block contains concise trigger contract & safety rails
	if !strings.Contains(content1, "## 0. Safety Rails") {
		t.Errorf("missing Safety Rails in managed block")
	}
	if !strings.Contains(content1, "## Sila Trigger Contract") {
		t.Errorf("missing Sila Trigger Contract in managed block")
	}

	// 4. Must NOT inject unrelated language stack directives or global hub
	if strings.Contains(content1, "CGO_ENABLED=0") {
		t.Errorf("injected Go CGO directive into Node.js project")
	}
	if strings.Contains(content1, "## Plain English") {
		t.Errorf("injected global Plain English guide into project rules")
	}
	if strings.Contains(content1, "## Working with Git") {
		t.Errorf("injected global Git guide into project rules")
	}

	// 5. Must NOT create unprompted harness files
	for _, unprompted := range []string{"CLAUDE.md", "GEMINI.md"} {
		p := filepath.Join(root, unprompted)
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			t.Errorf("unprompted harness file %s was created", unprompted)
		}
	}

	// 6. Idempotence: running ApplyHeal a second time produces identical file
	_, err = ApplyHeal(root)
	if err != nil {
		t.Fatalf("second ApplyHeal failed: %v", err)
	}

	data2, err := os.ReadFile(agentsPath)
	if err != nil {
		t.Fatal(err)
	}
	content2 := string(data2)

	if content1 != content2 {
		t.Errorf("ApplyHeal is not idempotent; content changed on second run")
	}
}
