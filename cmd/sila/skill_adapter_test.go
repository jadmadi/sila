package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/skills"
)

// Subprocess entrypoint for CLI failure and exit code tests
func TestSkillAdapter_SubprocessHelper(t *testing.T) {
	if os.Getenv("GO_WANT_SKILL_ADAPTER_SUBPROCESS") != "1" {
		return
	}
	action := os.Getenv("SUBPROCESS_ACTION")
	targetDir := os.Getenv("SUBPROCESS_TARGET_DIR")
	ui := render.New(os.Stdout, false)

	switch action {
	case "apply_json":
		runSkills(ui, "", true, false, skillCreateOptions{outDir: targetDir}, []string{"apply", "--json"}, "skill")
	case "apply_noninteractive":
		runSkills(ui, "", false, false, skillCreateOptions{outDir: targetDir}, []string{"apply"}, "skill")
	case "apply_interactive":
		runSkills(ui, "", false, false, skillCreateOptions{outDir: targetDir, interactive: true}, []string{"apply", "-i"}, "skill")
	case "apply_force":
		runSkills(ui, "", false, false, skillCreateOptions{outDir: targetDir, force: true}, []string{"apply", "--force"}, "skill")
	case "apply_yes":
		runSkills(ui, "", false, false, skillCreateOptions{outDir: targetDir}, []string{"apply", "--yes"}, "skill")
	case "apply_base_opts_yes":
		runSkills(ui, "", false, false, skillCreateOptions{outDir: targetDir, yes: true}, []string{"apply"}, "skill")
	case "apply_force_interactive":
		runSkills(ui, "", false, false, skillCreateOptions{outDir: targetDir, force: true, interactive: true}, []string{"apply", "--force", "--interactive"}, "skill")
	}
	os.Exit(0)
}

// setupCollidingWorkspace creates a valid Sila configuration and modifies it out-of-band.
func setupCollidingWorkspace(t *testing.T) (string, string) {
	t.Helper()
	tempDir := t.TempDir()
	opts := skills.AdapterOptions{
		Harness: "antigravity",
		Skills:  []string{"test-skill-alpha"},
	}
	res, err := skills.ApplyHarnessConfig(tempDir, opts)
	if err != nil || !res.Applied {
		t.Fatalf("failed initial apply: %v", err)
	}

	configPath := filepath.Join(tempDir, ".agents", "skills.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}

	// External modification: add unmanaged top-level key and modify skill regex
	mutated := strings.Replace(string(data), `"^test-skill-alpha$"`, `"^test-skill-beta$"`, 1)
	mutated = strings.Replace(mutated, "{\n", "{\n  \"corp_policy\": \"custom-v1\",\n", 1)
	if err := os.WriteFile(configPath, []byte(mutated), 0644); err != nil {
		t.Fatalf("write mutated: %v", err)
	}

	return tempDir, configPath
}

// TestSkillAdapter_ApplyCLI_JSONCollision verifies that --json emits structured JSON with collision details and exits 1.
func TestSkillAdapter_ApplyCLI_JSONCollision(t *testing.T) {
	tempDir, _ := setupCollidingWorkspace(t)

	cmd := exec.Command(os.Args[0], "-test.run=TestSkillAdapter_SubprocessHelper")
	cmd.Env = append(os.Environ(),
		"GO_WANT_SKILL_ADAPTER_SUBPROCESS=1",
		"SUBPROCESS_ACTION=apply_json",
		"SUBPROCESS_TARGET_DIR="+tempDir,
	)

	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected non-zero exit code on collision, got exit 0. Output:\n%s", string(out))
	}

	var payload map[string]any
	if jsonErr := json.Unmarshal(out, &payload); jsonErr != nil {
		t.Fatalf("expected valid JSON output on collision, got error: %v. Output:\n%s", jsonErr, string(out))
	}

	if payload["collision"] != true {
		t.Errorf("expected collision: true, got %v", payload["collision"])
	}
	if payload["action"] != "apply" {
		t.Errorf("expected action: 'apply', got %v", payload["action"])
	}
	if payload["diff"] == "" || payload["diff"] == nil {
		t.Errorf("expected non-empty diff in JSON collision output")
	}
	diverged, ok := payload["diverged_keys"].([]any)
	if !ok || len(diverged) == 0 {
		t.Errorf("expected non-empty diverged_keys array, got: %v", payload["diverged_keys"])
	}
}

// TestSkillAdapter_ApplyCLI_NonInteractiveFailClosed verifies that in non-interactive environments,
// collisions fail closed with exit code 1 and zero file corruption.
func TestSkillAdapter_ApplyCLI_NonInteractiveFailClosed(t *testing.T) {
	tempDir, configPath := setupCollidingWorkspace(t)

	cmd := exec.Command(os.Args[0], "-test.run=TestSkillAdapter_SubprocessHelper")
	cmd.Env = append(os.Environ(),
		"GO_WANT_SKILL_ADAPTER_SUBPROCESS=1",
		"SUBPROCESS_ACTION=apply_noninteractive",
		"SUBPROCESS_TARGET_DIR="+tempDir,
	)

	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected failure exit code in non-interactive collision, got success")
	}

	outputStr := string(out)
	if !strings.Contains(outputStr, "collision") && !strings.Contains(outputStr, "modified externally") {
		t.Errorf("expected output to mention collision, got:\n%s", outputStr)
	}

	// Verify on-disk file was NOT overwritten
	diskContent, _ := os.ReadFile(configPath)
	if !strings.Contains(string(diskContent), "corp_policy") {
		t.Fatalf("FAIL-CLOSED VIOLATION: on-disk file was overwritten despite non-interactive abort")
	}
}

// TestSkillAdapter_ApplyCLI_InteractiveDecline verifies that declining the prompt aborts without disk mutation.
func TestSkillAdapter_ApplyCLI_InteractiveDecline(t *testing.T) {
	tempDir, configPath := setupCollidingWorkspace(t)

	cmd := exec.Command(os.Args[0], "-test.run=TestSkillAdapter_SubprocessHelper")
	cmd.Env = append(os.Environ(),
		"GO_WANT_SKILL_ADAPTER_SUBPROCESS=1",
		"SUBPROCESS_ACTION=apply_interactive",
		"SUBPROCESS_TARGET_DIR="+tempDir,
	)
	cmd.Stdin = strings.NewReader("n\n") // decline prompt

	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected failure exit code when user declines confirmation, got success")
	}

	outputStr := string(out)
	if !strings.Contains(outputStr, "cancelled") && !strings.Contains(outputStr, "canceled") {
		t.Errorf("expected output to mention cancellation, got:\n%s", outputStr)
	}

	// Verify on-disk file was NOT modified
	diskContent, _ := os.ReadFile(configPath)
	if !strings.Contains(string(diskContent), "corp_policy") {
		t.Fatalf("FAIL-CLOSED VIOLATION: on-disk file was modified despite decline")
	}
}

// TestSkillAdapter_ApplyCLI_InteractiveConfirm verifies that confirming the prompt with 'y\n' applies with force.
func TestSkillAdapter_ApplyCLI_InteractiveConfirm(t *testing.T) {
	tempDir, configPath := setupCollidingWorkspace(t)

	cmd := exec.Command(os.Args[0], "-test.run=TestSkillAdapter_SubprocessHelper")
	cmd.Env = append(os.Environ(),
		"GO_WANT_SKILL_ADAPTER_SUBPROCESS=1",
		"SUBPROCESS_ACTION=apply_interactive",
		"SUBPROCESS_TARGET_DIR="+tempDir,
	)
	cmd.Stdin = strings.NewReader("y\n") // confirm prompt

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected success exit code when user confirms overwrite, got err: %v\nOutput:\n%s", err, string(out))
	}

	outputStr := string(out)
	if !strings.Contains(outputStr, "applied successfully") {
		t.Errorf("expected success message, got:\n%s", outputStr)
	}

	// Verify on-disk file was resealed with fresh Sila envelope
	diskContent, _ := os.ReadFile(configPath)
	var doc skills.ManagedDocument
	if err := json.Unmarshal(diskContent, &doc); err != nil {
		t.Fatalf("failed to parse updated config: %v", err)
	}
	hasCol, _, _, _ := skills.DetectCollision(&doc)
	if hasCol {
		t.Fatalf("expected collision to be resolved and resealed after confirmed apply")
	}
	if doc.Envelope == nil || !doc.Envelope.Managed || doc.Envelope.ContentHash == "" {
		t.Fatalf("expected valid managed envelope after confirmed apply")
	}
}

// TestSkillAdapter_ApplyCLI_ForceFlag verifies that --force bypasses prompt and succeeds in non-interactive mode.
func TestSkillAdapter_ApplyCLI_ForceFlag(t *testing.T) {
	tempDir, configPath := setupCollidingWorkspace(t)

	cmd := exec.Command(os.Args[0], "-test.run=TestSkillAdapter_SubprocessHelper")
	cmd.Env = append(os.Environ(),
		"GO_WANT_SKILL_ADAPTER_SUBPROCESS=1",
		"SUBPROCESS_ACTION=apply_force",
		"SUBPROCESS_TARGET_DIR="+tempDir,
	)

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected success with --force, got: %v\nOutput:\n%s", err, string(out))
	}

	// Verify on-disk file was resealed with fresh Sila envelope
	diskContent, _ := os.ReadFile(configPath)
	var doc skills.ManagedDocument
	if err := json.Unmarshal(diskContent, &doc); err != nil {
		t.Fatalf("failed to parse updated config: %v", err)
	}
	hasCol, _, _, _ := skills.DetectCollision(&doc)
	if hasCol {
		t.Fatalf("expected collision to be resolved and resealed after --force apply")
	}
	if doc.Envelope == nil || !doc.Envelope.Managed || doc.Envelope.ContentHash == "" {
		t.Fatalf("expected valid managed envelope after --force apply")
	}
}

// TestSkillAdapter_ApplyCLI_YesFlag verifies that --yes auto-approves prompt without hanging.
func TestSkillAdapter_ApplyCLI_YesFlag(t *testing.T) {
	tempDir, configPath := setupCollidingWorkspace(t)

	cmd := exec.Command(os.Args[0], "-test.run=TestSkillAdapter_SubprocessHelper")
	cmd.Env = append(os.Environ(),
		"GO_WANT_SKILL_ADAPTER_SUBPROCESS=1",
		"SUBPROCESS_ACTION=apply_yes",
		"SUBPROCESS_TARGET_DIR="+tempDir,
	)

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected success with --yes, got: %v\nOutput:\n%s", err, string(out))
	}

	// Verify on-disk file was resealed with fresh Sila envelope
	diskContent, _ := os.ReadFile(configPath)
	var doc skills.ManagedDocument
	if err := json.Unmarshal(diskContent, &doc); err != nil {
		t.Fatalf("failed to parse updated config: %v", err)
	}
	hasCol, _, _, _ := skills.DetectCollision(&doc)
	if hasCol {
		t.Fatalf("expected collision to be resolved and resealed after --yes apply")
	}
	if doc.Envelope == nil || !doc.Envelope.Managed || doc.Envelope.ContentHash == "" {
		t.Fatalf("expected valid managed envelope after --yes apply")
	}
}

// TestSkillAdapter_ApplyCLI_BaseOptsYesFlag verifies that setting yes: true in skillCreateOptions auto-approves prompt without hanging.
func TestSkillAdapter_ApplyCLI_BaseOptsYesFlag(t *testing.T) {
	tempDir, configPath := setupCollidingWorkspace(t)

	cmd := exec.Command(os.Args[0], "-test.run=TestSkillAdapter_SubprocessHelper")
	cmd.Env = append(os.Environ(),
		"GO_WANT_SKILL_ADAPTER_SUBPROCESS=1",
		"SUBPROCESS_ACTION=apply_base_opts_yes",
		"SUBPROCESS_TARGET_DIR="+tempDir,
	)

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected success with baseOpts.yes, got: %v\nOutput:\n%s", err, string(out))
	}

	// Verify on-disk file was resealed with fresh Sila envelope
	diskContent, _ := os.ReadFile(configPath)
	var doc skills.ManagedDocument
	if err := json.Unmarshal(diskContent, &doc); err != nil {
		t.Fatalf("failed to parse updated config: %v", err)
	}
	hasCol, _, _, _ := skills.DetectCollision(&doc)
	if hasCol {
		t.Fatalf("expected collision to be resolved and resealed after baseOpts.yes apply")
	}
	if doc.Envelope == nil || !doc.Envelope.Managed || doc.Envelope.ContentHash == "" {
		t.Fatalf("expected valid managed envelope after baseOpts.yes apply")
	}
}

// TestSkillAdapter_ApplyCLI_ForceInteractive verifies that --force --interactive prompts for confirmation:
// 1. Declining with 'n\n' aborts with code 1 and leaves on-disk configuration untouched.
// 2. Confirming with 'y\n' succeeds with code 0 and reseals on-disk configuration.
func TestSkillAdapter_ApplyCLI_ForceInteractive(t *testing.T) {
	t.Run("Decline", func(t *testing.T) {
		tempDir, configPath := setupCollidingWorkspace(t)

		cmd := exec.Command(os.Args[0], "-test.run=TestSkillAdapter_SubprocessHelper")
		cmd.Env = append(os.Environ(),
			"GO_WANT_SKILL_ADAPTER_SUBPROCESS=1",
			"SUBPROCESS_ACTION=apply_force_interactive",
			"SUBPROCESS_TARGET_DIR="+tempDir,
		)
		cmd.Stdin = strings.NewReader("n\n")

		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatalf("expected non-zero exit code when user declines confirmation under --force --interactive, got exit 0. Output:\n%s", string(out))
		}

		outputStr := string(out)
		if !strings.Contains(outputStr, "cancelled") && !strings.Contains(outputStr, "canceled") {
			t.Errorf("expected output to mention cancellation, got:\n%s", outputStr)
		}

		// Verify on-disk file was NOT overwritten
		diskContent, err := os.ReadFile(configPath)
		if err != nil {
			t.Fatalf("read config: %v", err)
		}
		if !strings.Contains(string(diskContent), "corp_policy") {
			t.Fatalf("FAIL-CLOSED VIOLATION: on-disk file was modified despite user decline 'n' under --force --interactive")
		}
	})

	t.Run("Confirm", func(t *testing.T) {
		tempDir, configPath := setupCollidingWorkspace(t)

		cmd := exec.Command(os.Args[0], "-test.run=TestSkillAdapter_SubprocessHelper")
		cmd.Env = append(os.Environ(),
			"GO_WANT_SKILL_ADAPTER_SUBPROCESS=1",
			"SUBPROCESS_ACTION=apply_force_interactive",
			"SUBPROCESS_TARGET_DIR="+tempDir,
		)
		cmd.Stdin = strings.NewReader("y\n")

		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("expected success exit code when user confirms overwrite under --force --interactive, got err: %v\nOutput:\n%s", err, string(out))
		}

		outputStr := string(out)
		if !strings.Contains(outputStr, "applied successfully") {
			t.Errorf("expected success message, got:\n%s", outputStr)
		}

		// Verify on-disk file was resealed with fresh Sila envelope
		diskContent, err := os.ReadFile(configPath)
		if err != nil {
			t.Fatalf("read config: %v", err)
		}
		var doc skills.ManagedDocument
		if err := json.Unmarshal(diskContent, &doc); err != nil {
			t.Fatalf("failed to parse updated config: %v", err)
		}
		hasCol, _, _, _ := skills.DetectCollision(&doc)
		if hasCol {
			t.Fatalf("expected collision to be resolved and resealed after confirmed apply")
		}
		if doc.Envelope == nil || !doc.Envelope.Managed || doc.Envelope.ContentHash == "" {
			t.Fatalf("expected valid managed envelope after confirmed apply")
		}
	})
}
