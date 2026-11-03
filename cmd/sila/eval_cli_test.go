package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/eval"
	"github.com/jadmadi/sila/internal/render"
)

func TestEvalCLI_JSONAndReportSingleJSONValue(t *testing.T) {
	tmpDir := t.TempDir()
	caseDir, err := eval.InitCaseTemplate(tmpDir, "json-report-case", false)
	if err != nil {
		t.Fatalf("InitCaseTemplate failed: %v", err)
	}

	// Update sila_contract.md to echo ok
	contractFile := filepath.Join(caseDir, "graders", "sila_contract.md")
	_ = os.WriteFile(contractFile, []byte(`---
type: sila_contract
action: command
command: "echo ok"
weight: 2.0
---
`), 0644)

	outputCheck := filepath.Join(caseDir, "graders", "output_check.md")
	_ = os.WriteFile(outputCheck, []byte(`---
type: regex
target: message
match_type: contains
pattern: "Mock response"
weight: 1.0
---
`), 0644)

	ui := render.New(os.Stdout, true)
	stdout := captureStdout(t, func() {
		runEval(ui, "", "", "mock", "", caseDir, "", "", "", "", false, true, false, false, true, false, nil)
	})

	trimmed := strings.TrimSpace(stdout)
	if !strings.HasPrefix(trimmed, "{") || !strings.HasSuffix(trimmed, "}") {
		t.Fatalf("expected stdout to be pure JSON value, got:\n%s", stdout)
	}

	var res eval.SuiteResult
	if err := json.Unmarshal([]byte(trimmed), &res); err != nil {
		t.Fatalf("failed to parse stdout JSON: %v; raw:\n%s", err, trimmed)
	}

	if !res.Synthetic {
		t.Errorf("expected Synthetic=true on mock suite result")
	}
	if !res.Passed {
		t.Errorf("expected test case to pass, got Passed=false")
	}

	// Verify report.html was written to disk
	reportFile := filepath.Join(caseDir, "evals", "report.html")
	if _, err := os.Stat(reportFile); err != nil {
		t.Errorf("expected report.html to exist at %s: %v", reportFile, err)
	}
}

func TestEvalCLI_SubprocessFailures(t *testing.T) {
	if os.Getenv("TEST_EVAL_UNKNOWN_HARNESS") == "1" {
		ui := render.New(os.Stdout, true)
		runEval(ui, "", "", "nonexistent_harness", "", ".", "", "", "", "", false, false, false, false, false, false, nil)
		return
	}
	if os.Getenv("TEST_EVAL_FAILING_GATE") == "1" {
		dir := os.Getenv("TEST_EVAL_DIR")
		ui := render.New(os.Stdout, true)
		runEval(ui, "", "", "mock", "", dir, "", "", "", "", false, false, false, false, false, false, nil)
		return
	}

	// 1. Unknown harness must exit nonzero
	cmdUnknown := exec.Command(os.Args[0], "-test.run=TestEvalCLI_SubprocessFailures")
	cmdUnknown.Env = append(os.Environ(), "TEST_EVAL_UNKNOWN_HARNESS=1")
	if err := cmdUnknown.Run(); err == nil {
		t.Fatal("expected unknown harness to exit nonzero")
	}

	// 2. Failing evaluation gate must exit nonzero
	tmpDir := t.TempDir()
	emptyCaseDir := filepath.Join(tmpDir, "empty-case")
	_ = os.MkdirAll(emptyCaseDir, 0755)
	_ = os.WriteFile(filepath.Join(emptyCaseDir, "prompt.md"), []byte("---\nname: empty-case\n---\nPrompt"), 0644)

	cmdFail := exec.Command(os.Args[0], "-test.run=TestEvalCLI_SubprocessFailures")
	cmdFail.Env = append(os.Environ(), "TEST_EVAL_FAILING_GATE=1", "TEST_EVAL_DIR="+emptyCaseDir)
	if err := cmdFail.Run(); err == nil {
		t.Fatal("expected failing eval gate (zero criteria) to exit nonzero")
	}
}

func TestEvalCLI_JSONAndReportOnFailure(t *testing.T) {
	if os.Getenv("TEST_EVAL_JSON_REPORT_FAIL") == "1" {
		dir := os.Getenv("TEST_EVAL_DIR")
		ui := render.New(os.Stdout, true)
		// runEval with jsonOut=true, report=true
		runEval(ui, "", "", "mock", "", dir, "", "", "", "", false, true, false, false, true, false, nil)
		return
	}

	tmpDir := t.TempDir()
	caseDir, err := eval.InitCaseTemplate(tmpDir, "failing-report-case", false)
	if err != nil {
		t.Fatalf("InitCaseTemplate failed: %v", err)
	}

	// Grader with non-matching pattern to ensure the case fails
	outputCheck := filepath.Join(caseDir, "graders", "output_check.md")
	_ = os.WriteFile(outputCheck, []byte(`---
type: regex
target: message
match_type: contains
pattern: "THIS_PATTERN_DOES_NOT_MATCH_MOCK_OUTPUT"
weight: 3.0
---
`), 0644)

	cmd := exec.Command(os.Args[0], "-test.run=TestEvalCLI_JSONAndReportOnFailure")
	cmd.Env = append(os.Environ(), "TEST_EVAL_JSON_REPORT_FAIL=1", "TEST_EVAL_DIR="+caseDir)

	out, err := cmd.CombinedOutput()
	// Must exit nonzero on failed evaluation
	if err == nil {
		t.Fatalf("expected command to exit nonzero on evaluation failure, but it succeeded. Output:\n%s", string(out))
	}

	trimmed := strings.TrimSpace(string(out))
	if !strings.HasPrefix(trimmed, "{") || !strings.HasSuffix(trimmed, "}") {
		t.Fatalf("expected stdout to be pure single JSON value despite failure, got:\n%s", string(out))
	}

	var res eval.SuiteResult
	if err := json.Unmarshal([]byte(trimmed), &res); err != nil {
		t.Fatalf("failed to parse stdout JSON on failure: %v; raw:\n%s", err, trimmed)
	}

	if res.Passed {
		t.Errorf("expected suite Passed=false on failed case")
	}
	if res.TotalScore != 0.0 {
		t.Errorf("expected TotalScore=0.0 on failed case, got %f", res.TotalScore)
	}
	if res.MaxScore != 5.0 {
		t.Errorf("expected MaxScore=5.0 (retained in denominator), got %f", res.MaxScore)
	}

	// Verify report.html was written to disk despite failure
	reportFile := filepath.Join(caseDir, "evals", "report.html")
	if _, err := os.Stat(reportFile); err != nil {
		t.Errorf("expected report.html to exist at %s even on failure: %v", reportFile, err)
	}
}
