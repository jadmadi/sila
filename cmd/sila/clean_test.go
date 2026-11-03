package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/jadmadi/sila/internal/render"
)

func TestCLIClean_PreviewAndJSON(t *testing.T) {
	tempDir := t.TempDir()

	// Create dummy repo residue
	covFile := filepath.Join(tempDir, "coverage.out")
	if err := os.WriteFile(covFile, []byte("mode: set\nmain.go:1.1,1.10 1 1\n"), 0644); err != nil {
		t.Fatalf("failed to create dummy file: %v", err)
	}

	ui := render.New(os.Stdout, true)

	// 1. Test cleanRunOptions with repoClean and dryRun
	opts := cleanRunOptions{
		repoClean: true,
		dryRun:    true,
		jsonOut:   false,
	}

	out := captureStdout(t, func() {
		runClean(ui, tempDir, nil, opts)
	})

	if out == "" {
		t.Errorf("expected clean preview output, got empty")
	}

	// Verify coverage file still exists after dry-run
	if _, err := os.Stat(covFile); os.IsNotExist(err) {
		t.Errorf("expected coverage.out to still exist after dry-run")
	}

	// 2. Test execution with --yes
	optsExec := cleanRunOptions{
		repoClean: true,
		dryRun:    false,
		yes:       true,
	}

	_ = captureStdout(t, func() {
		runClean(ui, tempDir, nil, optsExec)
	})

	// Verify coverage file was cleaned
	if _, err := os.Stat(covFile); !os.IsNotExist(err) {
		t.Errorf("expected coverage.out to be deleted after clean --yes")
	}
}

func TestCLIClean_JSONReport(t *testing.T) {
	tempDir := t.TempDir()
	ui := render.New(os.Stdout, true)

	opts := cleanRunOptions{
		repoClean: true,
		dryRun:    true,
		jsonOut:   true,
	}

	out := captureStdout(t, func() {
		runClean(ui, tempDir, nil, opts)
	})

	var res map[string]any
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("failed to unmarshal JSON output: %v\nOutput: %s", err, out)
	}

	if res["status"] != "clean" && res["dry_run"] != true {
		t.Errorf("unexpected json response: %+v", res)
	}
}
