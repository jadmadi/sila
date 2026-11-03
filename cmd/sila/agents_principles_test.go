package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/agents"
	"github.com/jadmadi/sila/internal/render"
)

func TestCLIAgentsPrinciplesSyncAndReview(t *testing.T) {
	tmp := t.TempDir()

	// Initial AGENTS.md with user content
	initialAgents := "# User Custom Project\n\nAlways enforce strict testing.\n"
	if err := os.WriteFile(filepath.Join(tmp, "AGENTS.md"), []byte(initialAgents), 0644); err != nil {
		t.Fatal(err)
	}

	ui := render.New(os.Stdout, true)

	// 1. Review --principles should preview diffs without modifying files
	reviewOutput := captureStdout(t, func() {
		runAgents(ui, "", "test-proj", "", "", "", "", false, false, true, "go", false, []string{"review", "--principles"})
	})

	if !strings.Contains(reviewOutput, "sila · agents review (principles)") {
		t.Errorf("expected review header in output: %s", reviewOutput)
	}

	// Verify no files were created during review
	if _, err := os.Stat(filepath.Join(tmp, ".windsurfrules")); !os.IsNotExist(err) {
		t.Fatal("review command unexpectedly created .windsurfrules")
	}

	// 2. Sync --principles applies changes
	// Change cwd for runAgentsSync
	origCwd, _ := os.Getwd()
	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origCwd)

	_ = captureStdout(t, func() {
		runAgents(ui, "", "test-proj", "", "", "", "", false, false, true, "go", false, []string{"sync", "--principles", "--stack=go"})
	})

	// Verify all 5 harness files exist and contain principles delimiters
	expectedTargets := []string{
		"AGENTS.md",
		filepath.Join(".cursor", "rules", "000-sila-principles.mdc"),
		".windsurfrules",
		filepath.Join(".github", "copilot-instructions.md"),
	}

	for _, target := range expectedTargets {
		p := filepath.Join(tmp, target)
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("expected file %s to exist: %v", target, err)
		}
		content := string(data)
		if !strings.Contains(content, agents.PrinciplesBlockBegin) {
			t.Errorf("file %s missing PrinciplesBlockBegin", target)
		}
		if !strings.Contains(content, "Radical Simplicity") {
			t.Errorf("file %s missing Radical Simplicity", target)
		}
		if !strings.Contains(content, "Pure-Go CGO-Free Portability") {
			t.Errorf("file %s missing Pure-Go CGO-Free Portability", target)
		}
	}

	// Verify user content was preserved in AGENTS.md
	agentsData, _ := os.ReadFile(filepath.Join(tmp, "AGENTS.md"))
	if !strings.Contains(string(agentsData), "Always enforce strict testing.") {
		t.Fatal("custom user content was destroyed in AGENTS.md")
	}

	// 3. Compile --principles output test
	compileOutput := captureStdout(t, func() {
		runAgents(ui, "", "test-proj", "", "", "", "", false, false, true, "go", false, []string{"compile", "--principles"})
	})

	if !strings.Contains(compileOutput, agents.PrinciplesBlockBegin) {
		t.Fatal("compile --principles missing PrinciplesBlockBegin")
	}
	if !strings.Contains(compileOutput, "Layer 1: Radical Simplicity") {
		t.Fatal("compile --principles missing Layer 1")
	}
}
