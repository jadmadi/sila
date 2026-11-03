package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/skills"
)

// TestSkillCLI_Preview_Flags verifies CLI preview behavior across --out, --json, --toon, and --harness
func TestSkillCLI_Preview_Flags(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")
	ui := render.New(nil, true)

	// 1. Preview default output (human terminal) with --out
	stdoutText := captureStdout(t, func() {
		runSkills(ui, dbPath, false, false, skillCreateOptions{outDir: tempDir}, []string{"preview", "--skills=skill-a,skill-b"}, "skill")
	})

	if !strings.Contains(stdoutText, "project skill preview") {
		t.Errorf("expected preview banner, got:\n%s", stdoutText)
	}
	if !strings.Contains(stdoutText, "Zero disk writes performed") {
		t.Errorf("expected zero disk write statement, got:\n%s", stdoutText)
	}

	// Verify zero disk writes
	targetFile := filepath.Join(tempDir, ".agents", "skills.json")
	if _, err := os.Stat(targetFile); !os.IsNotExist(err) {
		t.Fatalf("ZERO-WRITE VIOLATION: preview created file at %s", targetFile)
	}

	// 2. Preview with --json
	jsonOutput := captureStdout(t, func() {
		runSkills(ui, dbPath, true, false, skillCreateOptions{outDir: tempDir}, []string{"preview", "--skills=skill-a,skill-b", "--json"}, "skill")
	})

	var res map[string]interface{}
	if err := json.Unmarshal([]byte(jsonOutput), &res); err != nil {
		t.Fatalf("preview --json did not emit valid JSON: %v\nOutput:\n%s", err, jsonOutput)
	}
	if res["action"] != "preview" {
		t.Errorf("expected action=preview, got: %v", res["action"])
	}
	if res["zero_write"] != true {
		t.Errorf("expected zero_write=true, got: %v", res["zero_write"])
	}
	if res["has_changes"] != true {
		t.Errorf("expected has_changes=true, got: %v", res["has_changes"])
	}

	// 3. Preview with --toon
	toonOutput := captureStdout(t, func() {
		runSkills(ui, dbPath, false, true, skillCreateOptions{outDir: tempDir}, []string{"preview", "--skills=skill-a", "--toon"}, "skill")
	})

	if !strings.HasPrefix(toonOutput, "skill_preview[") {
		t.Errorf("expected TOON output starting with 'skill_preview[', got:\n%s", toonOutput)
	}

	// 4. Preview with Gemini harness
	geminiOutput := captureStdout(t, func() {
		runSkills(ui, dbPath, true, false, skillCreateOptions{outDir: tempDir}, []string{"preview", "--harness=gemini", "--json"}, "skill")
	})

	var geminiRes map[string]interface{}
	if err := json.Unmarshal([]byte(geminiOutput), &geminiRes); err != nil {
		t.Fatalf("gemini preview did not emit valid JSON: %v", err)
	}
	targetPath := geminiRes["target_path"].(string)
	if !strings.Contains(targetPath, ".gemini/skills.json") {
		t.Errorf("expected target_path in .gemini, got: %s", targetPath)
	}
}

// TestSkillCLI_Apply_DryRun_NoWrite verifies that apply with --dry-run or -n delegates to preview and writes 0 bytes
func TestSkillCLI_Apply_DryRun_NoWrite(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")
	ui := render.New(nil, true)

	// Invocations with --dry-run and -n
	for _, flag := range []string{"--dry-run", "-n", "--preview"} {
		stdoutText := captureStdout(t, func() {
			runSkills(ui, dbPath, false, false, skillCreateOptions{outDir: tempDir, dryRun: true}, []string{"apply", "--skills=test-skill", flag}, "skill")
		})

		if !strings.Contains(stdoutText, "Zero disk writes performed") && !strings.Contains(stdoutText, "project skill preview") {
			t.Errorf("flag %s failed to trigger dry-run preview, got:\n%s", flag, stdoutText)
		}

		targetFile := filepath.Join(tempDir, ".agents", "skills.json")
		if _, err := os.Stat(targetFile); !os.IsNotExist(err) {
			t.Fatalf("ZERO-WRITE VIOLATION: apply %s created file on disk!", flag)
		}
	}
}

// TestSkillCLI_Apply_And_Remove_Lifecycle verifies CLI apply and remove end-to-end with flags
func TestSkillCLI_Apply_And_Remove_Lifecycle(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")
	ui := render.New(nil, true)
	targetFile := filepath.Join(tempDir, ".agents", "skills.json")

	// 1. Apply via CLI
	applyOutput := captureStdout(t, func() {
		runSkills(ui, dbPath, false, false, skillCreateOptions{outDir: tempDir}, []string{"apply", "--skills=cli-skill-1,cli-skill-2"}, "skill")
	})

	if !strings.Contains(applyOutput, "applied successfully") {
		t.Fatalf("expected successful apply output, got:\n%s", applyOutput)
	}

	// Verify file was written
	data, err := os.ReadFile(targetFile)
	if err != nil {
		t.Fatalf("read target file after apply: %v", err)
	}
	var doc skills.ManagedDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal applied file: %v", err)
	}
	if doc.Envelope == nil || !doc.Envelope.Managed {
		t.Fatalf("missing _sila envelope in applied file")
	}

	// 2. Idempotent re-apply via CLI
	reapplyOutput := captureStdout(t, func() {
		runSkills(ui, dbPath, false, false, skillCreateOptions{outDir: tempDir}, []string{"apply", "--skills=cli-skill-1,cli-skill-2"}, "skill")
	})
	if !strings.Contains(reapplyOutput, "already optimal and up-to-date") {
		t.Errorf("expected idempotent no-op message, got:\n%s", reapplyOutput)
	}

	// 3. Apply with --json format
	jsonApplyOutput := captureStdout(t, func() {
		runSkills(ui, dbPath, true, false, skillCreateOptions{outDir: tempDir}, []string{"apply", "--skills=cli-skill-1,cli-skill-2,cli-skill-3", "--json"}, "skill")
	})
	var applyJSON map[string]interface{}
	if err := json.Unmarshal([]byte(jsonApplyOutput), &applyJSON); err != nil {
		t.Fatalf("apply --json did not emit valid JSON: %v\nOutput:\n%s", err, jsonApplyOutput)
	}
	if applyJSON["action"] != "apply" || applyJSON["status"] != "applied" {
		t.Errorf("unexpected apply JSON: %v", applyJSON)
	}

	// 4. Remove with --json format (Contract A)
	jsonRemoveOutput := captureStdout(t, func() {
		runSkills(ui, dbPath, true, false, skillCreateOptions{outDir: tempDir}, []string{"remove", "--json"}, "skill")
	})
	var removeJSON map[string]interface{}
	if err := json.Unmarshal([]byte(jsonRemoveOutput), &removeJSON); err != nil {
		t.Fatalf("remove --json did not emit valid JSON: %v\nOutput:\n%s", err, jsonRemoveOutput)
	}
	if removeJSON["action"] != "remove" || removeJSON["status"] != "removed" {
		t.Errorf("unexpected remove JSON: %v", removeJSON)
	}
	if removeJSON["file_deleted"] != true {
		t.Errorf("expected file_deleted=true, got: %v", removeJSON["file_deleted"])
	}
	if removeJSON["parent_dir_removed"] != true {
		t.Errorf("expected parent_dir_removed=true, got: %v", removeJSON["parent_dir_removed"])
	}

	// Verify file and .agents/ deleted
	if _, err := os.Stat(targetFile); !os.IsNotExist(err) {
		t.Errorf("target file still exists after removal")
	}
	if _, err := os.Stat(filepath.Dir(targetFile)); !os.IsNotExist(err) {
		t.Errorf(".agents directory still exists after Contract A removal")
	}
}

// TestSkillCLI_FlagAliasesAndPositionalSkills verifies flag aliases and positional skills parsing
func TestSkillCLI_FlagAliasesAndPositionalSkills(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")
	ui := render.New(nil, true)

	// Test positional skills: `sila skill preview alpha beta gamma`
	posOutput := captureStdout(t, func() {
		runSkills(ui, dbPath, true, false, skillCreateOptions{outDir: tempDir}, []string{"preview", "alpha", "beta", "gamma", "--json"}, "skill")
	})

	var posRes map[string]interface{}
	if err := json.Unmarshal([]byte(posOutput), &posRes); err != nil {
		t.Fatalf("unmarshal posOutput: %v", err)
	}
	selected := posRes["selected_skills"].([]interface{})
	if len(selected) != 3 {
		t.Errorf("expected 3 selected skills from positional args, got %d: %v", len(selected), selected)
	}

	// Test --skill singular alias: `sila skill preview --skill=alpha,beta`
	aliasOutput := captureStdout(t, func() {
		runSkills(ui, dbPath, true, false, skillCreateOptions{outDir: tempDir}, []string{"preview", "--skill=alpha,beta", "--json"}, "skill")
	})

	var aliasRes map[string]interface{}
	if err := json.Unmarshal([]byte(aliasOutput), &aliasRes); err != nil {
		t.Fatalf("unmarshal aliasOutput: %v", err)
	}
	aliasSelected := aliasRes["selected_skills"].([]interface{})
	if len(aliasSelected) != 2 {
		t.Errorf("expected 2 selected skills from --skill alias, got %d: %v", len(aliasSelected), aliasSelected)
	}
}

// TestSkillCLI_HelpFlags verifies that --help, -h, and help flags display help without error
func TestSkillCLI_HelpFlags(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")
	ui := render.New(nil, true)

	subcommands := []string{"preview", "apply", "remove"}
	helpFlags := []string{"--help", "-h", "help"}

	for _, sub := range subcommands {
		for _, hf := range helpFlags {
			out := captureStdout(t, func() {
				runSkills(ui, dbPath, false, false, skillCreateOptions{outDir: tempDir}, []string{sub, hf}, "skill")
			})
			expectedHeader := "sila skill " + sub
			if !strings.Contains(out, expectedHeader) {
				t.Errorf("expected help output for 'sila skill %s %s' to contain %q, got:\n%s", sub, hf, expectedHeader, out)
			}
		}
	}
}
