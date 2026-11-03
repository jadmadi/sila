package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/skills"
)

// Subprocess helper for challenger flag and concurrency tests
func TestChallenger_SubprocessHelper(t *testing.T) {
	if os.Getenv("GO_WANT_CHALLENGER_SUBPROCESS") != "1" {
		return
	}
	action := os.Getenv("CHALLENGER_ACTION")
	targetDir := os.Getenv("CHALLENGER_TARGET_DIR")
	ui := render.New(os.Stdout, false)

	switch action {
	case "apply_force_only":
		runSkills(ui, "", false, false, skillCreateOptions{outDir: targetDir, force: true}, []string{"apply", "--force"}, "skill")
	case "apply_yes_only":
		runSkills(ui, "", false, false, skillCreateOptions{outDir: targetDir}, []string{"apply", "--yes"}, "skill")
	case "apply_noninteractive_closed_stdin":
		runSkills(ui, "", false, false, skillCreateOptions{outDir: targetDir}, []string{"apply"}, "skill")
	case "apply_force_interactive":
		runSkills(ui, "", false, false, skillCreateOptions{outDir: targetDir, force: true, interactive: true}, []string{"apply", "--force", "--interactive"}, "skill")
	case "apply_concurrent_worker":
		workerSkill := os.Getenv("CHALLENGER_WORKER_SKILL")
		runSkills(ui, "", false, false, skillCreateOptions{outDir: targetDir, force: true}, []string{"apply", "--force", "--skill=" + workerSkill}, "skill")
	}
	os.Exit(0)
}

// setupChallengerCollidingWorkspace sets up a workspace with an out-of-band edit.
func setupChallengerCollidingWorkspace(t *testing.T) (string, string) {
	t.Helper()
	tempDir := t.TempDir()
	opts := skills.AdapterOptions{
		Harness: "antigravity",
		Skills:  []string{"test-skill-alpha"},
	}
	res, err := skills.ApplyHarnessConfig(tempDir, opts)
	if err != nil || !res.Applied {
		t.Fatalf("setup apply failed: %v", err)
	}

	configPath := filepath.Join(tempDir, ".agents", "skills.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config failed: %v", err)
	}

	// External modification: add corp_policy and modify skill
	mutated := strings.Replace(string(data), `"^test-skill-alpha$"`, `"^test-skill-external$"`, 1)
	mutated = strings.Replace(mutated, "{\n", "{\n  \"corp_external_key\": \"external-v1\",\n", 1)
	if err := os.WriteFile(configPath, []byte(mutated), 0644); err != nil {
		t.Fatalf("write mutated failed: %v", err)
	}

	return tempDir, configPath
}

// TestChallenger_Flag_ForceOverridesWithoutPrompt tests:
// --force overrides collision without prompt.
func TestChallenger_Flag_ForceOverridesWithoutPrompt(t *testing.T) {
	tempDir, configPath := setupChallengerCollidingWorkspace(t)

	cmd := exec.Command(os.Args[0], "-test.run=TestChallenger_SubprocessHelper")
	cmd.Env = append(os.Environ(),
		"GO_WANT_CHALLENGER_SUBPROCESS=1",
		"CHALLENGER_ACTION=apply_force_only",
		"CHALLENGER_TARGET_DIR="+tempDir,
	)
	// Stdin is empty / closed
	cmd.Stdin = strings.NewReader("")

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected --force to succeed without prompt, got: %v\nOutput: %s", err, string(out))
	}

	diskContent, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}

	var doc skills.ManagedDocument
	if err := json.Unmarshal(diskContent, &doc); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	hasCol, _, _, _ := skills.DetectCollision(&doc)
	if hasCol {
		t.Errorf("expected collision to be resolved by --force")
	}
}

// TestChallenger_Flag_YesBypassesInteractivePrompt tests:
// --yes bypasses interactive prompt.
func TestChallenger_Flag_YesBypassesInteractivePrompt(t *testing.T) {
	tempDir, configPath := setupChallengerCollidingWorkspace(t)

	cmd := exec.Command(os.Args[0], "-test.run=TestChallenger_SubprocessHelper")
	cmd.Env = append(os.Environ(),
		"GO_WANT_CHALLENGER_SUBPROCESS=1",
		"CHALLENGER_ACTION=apply_yes_only",
		"CHALLENGER_TARGET_DIR="+tempDir,
	)
	cmd.Stdin = strings.NewReader("") // Stdin closed

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected --yes to succeed without prompt, got: %v\nOutput: %s", err, string(out))
	}

	diskContent, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}

	var doc skills.ManagedDocument
	if err := json.Unmarshal(diskContent, &doc); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	hasCol, _, _, _ := skills.DetectCollision(&doc)
	if hasCol {
		t.Errorf("expected collision to be resolved by --yes")
	}
}

// TestChallenger_Flag_NonInteractiveClosedStdinFailsClosed tests:
// Non-interactive (pipe/stdin closed) fails closed immediately with code 1.
func TestChallenger_Flag_NonInteractiveClosedStdinFailsClosed(t *testing.T) {
	tempDir, configPath := setupChallengerCollidingWorkspace(t)

	cmd := exec.Command(os.Args[0], "-test.run=TestChallenger_SubprocessHelper")
	cmd.Env = append(os.Environ(),
		"GO_WANT_CHALLENGER_SUBPROCESS=1",
		"CHALLENGER_ACTION=apply_noninteractive_closed_stdin",
		"CHALLENGER_TARGET_DIR="+tempDir,
	)
	cmd.Stdin = strings.NewReader("") // Stdin closed (non-interactive pipe)

	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected non-interactive closed stdin to FAIL with code 1, but succeeded. Output:\n%s", string(out))
	}

	outputStr := string(out)
	if !strings.Contains(outputStr, "collision") && !strings.Contains(outputStr, "modified externally") {
		t.Errorf("expected collision error in output, got:\n%s", outputStr)
	}

	// Verify disk file was NOT overwritten
	diskContent, _ := os.ReadFile(configPath)
	if !strings.Contains(string(diskContent), "corp_external_key") {
		t.Fatalf("FAIL-CLOSED VIOLATION: on-disk file was modified despite non-interactive closed stdin")
	}
}

// TestChallenger_Flag_ForceInteractive_PromptsForConfirmation tests:
// --force --interactive prompts for confirmation:
// 1. If user declines with 'n\n', it must abort with code 1 and preserve disk.
// 2. If user confirms with 'y\n', it must succeed with code 0 and overwrite disk.
func TestChallenger_Flag_ForceInteractive_PromptsForConfirmation(t *testing.T) {
	t.Run("DeclineWithN", func(t *testing.T) {
		tempDir, configPath := setupChallengerCollidingWorkspace(t)

		cmd := exec.Command(os.Args[0], "-test.run=TestChallenger_SubprocessHelper")
		cmd.Env = append(os.Environ(),
			"GO_WANT_CHALLENGER_SUBPROCESS=1",
			"CHALLENGER_ACTION=apply_force_interactive",
			"CHALLENGER_TARGET_DIR="+tempDir,
		)
		cmd.Stdin = strings.NewReader("n\n") // User declines confirmation

		out, err := cmd.CombinedOutput()
		outputStr := string(out)

		// EMPIRICAL CHALLENGE CHECK:
		// If --force --interactive prompts for confirmation, then 'n\n' MUST abort with non-zero exit.
		if err == nil {
			t.Errorf("EMBEDDED FLAW CONFIRMED: 'apply --force --interactive' ignored user decline 'n' and exited 0 without prompting! Output:\n%s", outputStr)
		}

		diskContent, _ := os.ReadFile(configPath)
		if !strings.Contains(string(diskContent), "corp_external_key") {
			t.Errorf("EMBEDDED FLAW CONFIRMED: 'apply --force --interactive' clobbered disk content despite user entering 'n'!")
		}
	})

	t.Run("ConfirmWithY", func(t *testing.T) {
		tempDir, configPath := setupChallengerCollidingWorkspace(t)

		cmd := exec.Command(os.Args[0], "-test.run=TestChallenger_SubprocessHelper")
		cmd.Env = append(os.Environ(),
			"GO_WANT_CHALLENGER_SUBPROCESS=1",
			"CHALLENGER_ACTION=apply_force_interactive",
			"CHALLENGER_TARGET_DIR="+tempDir,
		)
		cmd.Stdin = strings.NewReader("y\n") // User confirms overwrite

		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("expected success when confirming with 'y', got: %v\nOutput: %s", err, string(out))
		}

		diskContent, _ := os.ReadFile(configPath)
		var doc skills.ManagedDocument
		if err := json.Unmarshal(diskContent, &doc); err != nil {
			t.Fatalf("failed to unmarshal: %v", err)
		}
		hasCol, _, _, _ := skills.DetectCollision(&doc)
		if hasCol {
			t.Errorf("expected collision to be resolved after confirmed apply")
		}
	})
}

// TestChallenger_Concurrency_MultipleApplyProcesses tests:
// Multiple concurrent apply processes contending on advisory flock. Verify 0 deadlocks, 0 races, 0 corruption.
func TestChallenger_Concurrency_MultipleApplyProcesses(t *testing.T) {
	tempDir := t.TempDir()

	// Initial setup
	initOpts := skills.AdapterOptions{
		Harness: "antigravity",
		Skills:  []string{"initial-skill"},
	}
	_, err := skills.ApplyHarnessConfig(tempDir, initOpts)
	if err != nil {
		t.Fatalf("initial setup failed: %v", err)
	}

	numProcesses := 12
	var wg sync.WaitGroup
	errCh := make(chan error, numProcesses)
	startBarrier := make(chan struct{})

	for i := 0; i < numProcesses; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			<-startBarrier // synchronize start

			skillName := fmt.Sprintf("concurrent-skill-%d", workerID)
			cmd := exec.Command(os.Args[0], "-test.run=TestChallenger_SubprocessHelper")
			cmd.Env = append(os.Environ(),
				"GO_WANT_CHALLENGER_SUBPROCESS=1",
				"CHALLENGER_ACTION=apply_concurrent_worker",
				"CHALLENGER_TARGET_DIR="+tempDir,
				"CHALLENGER_WORKER_SKILL="+skillName,
			)

			out, err := cmd.CombinedOutput()
			if err != nil {
				errCh <- fmt.Errorf("worker %d failed: %w (output: %s)", workerID, err, string(out))
			}
		}(i)
	}

	// Release all processes simultaneously
	close(startBarrier)

	// Wait with timeout to verify 0 deadlocks
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Succeeded without deadlock
	case <-time.After(30 * time.Second):
		t.Fatalf("DEADLOCK DETECTED: concurrent apply processes failed to complete within 30s")
	}

	close(errCh)
	var processErrors []string
	for e := range errCh {
		processErrors = append(processErrors, e.Error())
	}
	if len(processErrors) > 0 {
		t.Fatalf("Concurrent process errors (%d/%d):\n%s", len(processErrors), numProcesses, strings.Join(processErrors, "\n"))
	}

	// Verify 0 corruption: final config must be valid JSON with valid envelope
	configPath := filepath.Join(tempDir, ".agents", "skills.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read final config: %v", err)
	}

	var finalDoc skills.ManagedDocument
	if err := json.Unmarshal(data, &finalDoc); err != nil {
		t.Fatalf("CORRUPTION DETECTED: final config is invalid JSON: %v\nData:\n%s", err, string(data))
	}

	if finalDoc.Envelope == nil || !finalDoc.Envelope.Managed {
		t.Fatalf("CORRUPTION DETECTED: missing or invalid _sila envelope")
	}

	hasCol, _, _, err := skills.DetectCollision(&finalDoc)
	if err != nil || hasCol {
		t.Fatalf("CORRUPTION DETECTED: final document has unresolved collision: %v", err)
	}

	// Verify 0 orphaned lock files
	lockPath := filepath.Join(tempDir, ".agents", ".skills.json.lock")
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Errorf("ORPHANED LOCK DETECTED: lockfile still exists at %s", lockPath)
	}

	// Verify 0 orphaned .tmp files
	dirEntries, _ := os.ReadDir(filepath.Join(tempDir, ".agents"))
	for _, entry := range dirEntries {
		if strings.HasSuffix(entry.Name(), ".tmp") {
			t.Errorf("ORPHANED TEMP FILE DETECTED: %s", entry.Name())
		}
	}
}
