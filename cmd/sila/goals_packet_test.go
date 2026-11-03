package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/render"
)

// Subprocess entrypoint for goals packet CLI execution testing
func TestGoalsPacketSubprocess(t *testing.T) {
	if os.Getenv("GO_WANT_GOALS_PACKET_SUBPROCESS") != "1" {
		return
	}
	targetDir := os.Getenv("SUBPROCESS_TARGET_DIR")
	rawArgs := os.Getenv("SUBPROCESS_ARGS")
	actionArgs := strings.Split(rawArgs, "||")
	jsonOut := os.Getenv("SUBPROCESS_JSON") == "1"

	ui := render.New(os.Stdout, false)
	runGoals(ui, "", targetDir, "", "", "", "", false, false, "", "", "", "", "", 0, false, false, false, false, false, false, false, false, jsonOut, false, false, false, false, "", "", "", false, actionArgs)
	os.Exit(0)
}

func runPacketCLI(t *testing.T, targetDir string, jsonOut bool, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestGoalsPacketSubprocess$")
	cmd.Env = append(os.Environ(),
		"GO_WANT_GOALS_PACKET_SUBPROCESS=1",
		"SUBPROCESS_TARGET_DIR="+targetDir,
		"SUBPROCESS_ARGS="+strings.Join(args, "||"),
	)
	if jsonOut {
		cmd.Env = append(cmd.Env, "SUBPROCESS_JSON=1")
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			t.Fatalf("runPacketCLI failed to execute: %v", err)
		}
	}
	return stdout.String(), stderr.String(), exitCode
}

func setupCLIPacketFixture(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	slug := "cli-packet-fixture"

	goSrc := "package core\n\ntype Unit struct{}\n"
	err := os.WriteFile(filepath.Join(dir, "core.go"), []byte(goSrc), 0644)
	if err != nil {
		t.Fatalf("write core.go: %v", err)
	}
	hGo := sha256.Sum256([]byte(goSrc))
	coreSHA := hex.EncodeToString(hGo[:])

	goalDir := filepath.Join(dir, "goals", slug)
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		t.Fatalf("mkdir goal dir: %v", err)
	}

	pkg := &goals.WorkPackage{
		SchemaVersion: goals.WorkPackageSchemaVersion,
		GoalSlug:      slug,
		Revision:      1,
		Purpose:       "Verify CLI packet generation and exit codes",
		Constraints:   []string{"pure-Go stdlib"},
		NonGoals:      []string{"no external dependencies"},
		Tasks: []goals.TaskContract{
			{
				ID:        "T-001",
				Title:     "Initialize Core",
				Context:   "Core setup context",
				Why:       "Foundational initialization",
				DependsOn: []string{},
				Files: goals.TaskFiles{
					Read:   []string{"core.go"},
					Edit:   []string{"core.go"},
					Create: []string{"core_output.go"},
					Forbid: []string{"research/*"},
				},
				References: []goals.Reference{
					{
						Path:    "core.go",
						Symbol:  "Unit",
						Excerpt: "type Unit struct{}",
						SHA256:  coreSHA,
					},
				},
				Decisions:     []goals.Decision{},
				Steps:         []goals.Step{{ID: "S-001", Action: "Build core", Expected: "Success", OnFailure: "Halt"}},
				Interfaces:    []string{"type Unit struct{}"},
				Constraints:   []string{"zero alloc"},
				Examples:      []goals.Example{},
				AcceptanceIDs: []string{"AC-001"},
				Checks: []goals.Check{
					{
						ID:               "C-001",
						Executable:       "go",
						Args:             []string{"test", "./..."},
						Cwd:              "cli",
						TimeoutSeconds:   30,
						ExpectedExitCode: 0,
						Scope:            goals.ScopeUnit,
						Assertions: []goals.Assertion{
							{Kind: goals.AssertionGoTestPass, Target: "github.com/jadmadi/sila/cmd/sila/TestGoalsPacketCLI", Expected: true},
						},
						Capability: goals.CapabilityLocal,
					},
				},
				StopConditions: []string{"Timeout"},
				ReturnFormat:   "sila/task-result/v1",
			},
			{
				ID:        "T-002",
				Title:     "Extend Core",
				Context:   "Dependent task context",
				Why:       "Extension layer",
				DependsOn: []string{"T-001"},
				Files: goals.TaskFiles{
					Read:   []string{"core.go"},
					Edit:   []string{"core.go"},
					Create: []string{"core_ext.go"},
					Forbid: []string{"research/*"},
				},
				References: []goals.Reference{
					{
						Path:    "core.go",
						Symbol:  "Unit",
						Excerpt: "type Unit struct{}",
						SHA256:  coreSHA,
					},
				},
				Decisions:     []goals.Decision{},
				Steps:         []goals.Step{{ID: "S-002", Action: "Extend core", Expected: "Extended", OnFailure: "Halt"}},
				Interfaces:    []string{},
				Constraints:   []string{},
				Examples:      []goals.Example{},
				AcceptanceIDs: []string{"AC-002"},
				Checks: []goals.Check{
					{
						ID:               "C-002",
						Executable:       "go",
						Args:             []string{"test", "./..."},
						Cwd:              "cli",
						TimeoutSeconds:   30,
						ExpectedExitCode: 0,
						Scope:            goals.ScopeUnit,
						Assertions: []goals.Assertion{
							{Kind: goals.AssertionGoTestPass, Target: "github.com/jadmadi/sila/cmd/sila/TestGoalsPacketCLI", Expected: true},
						},
						Capability: goals.CapabilityLocal,
					},
				},
				StopConditions: []string{"Timeout"},
				ReturnFormat:   "sila/task-result/v1",
			},
		},
		Acceptance: []goals.AcceptanceCase{
			{
				ID:            "AC-001",
				Requirement:   "Core initializes cleanly",
				Given:         "Fresh environment",
				Action:        "Initialize",
				Expected:      "Clean exit",
				RequiredScope: goals.ScopeUnit,
				Mandatory:     true,
				CheckIDs:      []string{"C-001"},
			},
			{
				ID:            "AC-002",
				Requirement:   "Core extension succeeds",
				Given:         "Core initialized",
				Action:        "Extend",
				Expected:      "Extension ready",
				RequiredScope: goals.ScopeUnit,
				Mandatory:     true,
				CheckIDs:      []string{"C-002"},
			},
		},
		DependencyConditions: []goals.DependencyCondition{},
		UnresolvedDecisions:  []string{},
	}

	pkgData, err := json.MarshalIndent(pkg, "", "  ")
	if err != nil {
		t.Fatalf("marshal work package: %v", err)
	}
	if err := os.WriteFile(filepath.Join(goalDir, "work-package.json"), pkgData, 0644); err != nil {
		t.Fatalf("write work-package.json: %v", err)
	}

	specDigest, err := goals.WorkPackageDigest(pkg)
	if err != nil {
		t.Fatalf("compute spec digest: %v", err)
	}

	review := goals.SpecReview{
		SchemaVersion:      goals.SpecReviewSchemaVersion,
		SpecDigest:         specDigest,
		Reviewer:           "cli-tester",
		ReviewedAt:         time.Now().UTC().Format(time.RFC3339),
		Verdict:            goals.VerdictReady,
		Shortcuts:          []goals.ShortcutCounter{},
		UnresolvedBlockers: []string{},
	}
	if err := goals.SaveSpecReview(dir, slug, review); err != nil {
		t.Fatalf("save spec review: %v", err)
	}

	return dir, slug
}

func TestGoalsPacketCLI(t *testing.T) {
	dir, slug := setupCLIPacketFixture(t)

	// 1. Invalid arguments: missing required slug or task-id -> exit code 2
	_, _, exitCode := runPacketCLI(t, dir, false, "packet")
	if exitCode != 2 {
		t.Errorf("expected exit code 2 for missing arguments, got %d", exitCode)
	}

	_, _, exitCode = runPacketCLI(t, dir, false, "packet", slug)
	if exitCode != 2 {
		t.Errorf("expected exit code 2 for missing task-id, got %d", exitCode)
	}

	_, _, exitCode = runPacketCLI(t, dir, false, "packet", slug, "T-001", "--unknown-flag")
	if exitCode != 2 {
		t.Errorf("expected exit code 2 for unknown flag, got %d", exitCode)
	}

	// 2. Unknown task ID -> exit code 1
	_, _, exitCode = runPacketCLI(t, dir, false, "packet", slug, "T-999")
	if exitCode != 1 {
		t.Errorf("expected exit code 1 for unknown task ID, got %d", exitCode)
	}

	// 3. Valid independent ready task T-001 (Markdown mode) -> exit code 0
	stdout, _, exitCode := runPacketCLI(t, dir, false, "packet", slug, "T-001")
	if exitCode != 0 {
		t.Fatalf("expected exit code 0 for valid ready task, got %d", exitCode)
	}
	if !strings.Contains(stdout, "# Task: T-001 Initialize Core") {
		t.Errorf("expected stdout to contain task header, got: %s", stdout)
	}
	if !strings.Contains(stdout, "## Context") || !strings.Contains(stdout, "## Acceptance criteria") {
		t.Errorf("expected required sections in Markdown output")
	}

	// 4. Valid independent ready task T-001 (JSON mode) -> exit code 0, clean stdout
	stdout, _, exitCode = runPacketCLI(t, dir, true, "packet", slug, "T-001", "--json")
	if exitCode != 0 {
		t.Fatalf("expected exit code 0 for valid ready task with --json, got %d", exitCode)
	}
	var packet goals.TaskPacket
	if err := json.Unmarshal([]byte(stdout), &packet); err != nil {
		t.Fatalf("failed to decode JSON stdout: %v\nSTDOUT:\n%s", err, stdout)
	}
	if packet.TaskID != "T-001" || !packet.Executable {
		t.Errorf("unexpected packet state: task_id=%s, executable=%v", packet.TaskID, packet.Executable)
	}

	// 5. Dependent unready task T-002 -> exit code 1 (blocked)
	stdout, _, exitCode = runPacketCLI(t, dir, false, "packet", slug, "T-002")
	if exitCode != 1 {
		t.Errorf("expected exit code 1 for unready task, got %d", exitCode)
	}
	if !strings.Contains(stdout, "T-002") {
		t.Errorf("expected output to contain T-002, got: %s", stdout)
	}

	// 6. Dependent unready task T-002 (JSON mode) -> exit code 1 (blocked), clean stdout
	stdout, _, exitCode = runPacketCLI(t, dir, true, "packet", slug, "T-002", "--json")
	if exitCode != 1 {
		t.Errorf("expected exit code 1 for unready task with --json, got %d", exitCode)
	}
	var unreadyPacket goals.TaskPacket
	if err := json.Unmarshal([]byte(stdout), &unreadyPacket); err != nil {
		t.Fatalf("failed to decode JSON stdout for unready packet: %v\nSTDOUT:\n%s", err, stdout)
	}
	if unreadyPacket.TaskID != "T-002" || unreadyPacket.Executable {
		t.Errorf("expected unready packet with executable=false, got executable=%v", unreadyPacket.Executable)
	}
}
