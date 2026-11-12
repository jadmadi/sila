package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/mcp"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

// Subprocess entrypoint for goals resume, checkpoint, and amend CLI execution testing
func TestGoalsResumeSubprocess(t *testing.T) {
	if os.Getenv("GO_WANT_GOALS_RESUME_SUBPROCESS") != "1" {
		return
	}
	targetDir := os.Getenv("SUBPROCESS_TARGET_DIR")
	rawArgs := os.Getenv("SUBPROCESS_ARGS")
	actionArgs := strings.Split(rawArgs, "||")
	jsonOut := os.Getenv("SUBPROCESS_JSON") == "1"
	agent := os.Getenv("SUBPROCESS_AGENT")

	ui := render.New(os.Stdout, false)
	runGoals(ui, "", targetDir, agent, "", "", "", false, false, "", "", "", "", "", 0, false, false, false, false, false, false, false, false, jsonOut, false, false, false, false, "", "", "", false, actionArgs)
	os.Exit(0)
}

func runResumeCLI(t *testing.T, targetDir string, agent string, jsonOut bool, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestGoalsResumeSubprocess$")
	cmd.Env = append(os.Environ(),
		"GO_WANT_GOALS_RESUME_SUBPROCESS=1",
		"SUBPROCESS_TARGET_DIR="+targetDir,
		"SUBPROCESS_ARGS="+strings.Join(args, "||"),
	)
	if agent != "" {
		cmd.Env = append(cmd.Env, "SUBPROCESS_AGENT="+agent, "SILA_AGENT="+agent)
	}
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
			t.Fatalf("runResumeCLI failed to execute: %v", err)
		}
	}
	return stdout.String(), stderr.String(), exitCode
}

func setupCLIResumeFixture(t *testing.T, slug, claimAgent string) (string, *goals.WorkPackage) {
	t.Helper()
	dir := t.TempDir()

	goSrc := "package resume\n\ntype Engine struct{}\n"
	if err := os.WriteFile(filepath.Join(dir, "engine.go"), []byte(goSrc), 0644); err != nil {
		t.Fatalf("write engine.go: %v", err)
	}
	hGo := sha256.Sum256([]byte(goSrc))
	engineSHA := hex.EncodeToString(hGo[:])

	goalDir := filepath.Join(dir, "goals", slug)
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		t.Fatalf("mkdir goal dir: %v", err)
	}

	pkg := &goals.WorkPackage{
		SchemaVersion: goals.WorkPackageSchemaVersion,
		GoalSlug:      slug,
		Revision:      1,
		Purpose:       "Verify resume and amendment operations",
		Constraints:   []string{"pure-Go stdlib"},
		NonGoals:      []string{"no external dependencies"},
		Tasks: []goals.TaskContract{
			{
				ID:        "T-001",
				Title:     "Build Engine Core",
				Context:   "Core engine initialization",
				Why:       "Foundational component",
				DependsOn: []string{},
				Files: goals.TaskFiles{
					Read:   []string{"engine.go"},
					Edit:   []string{"engine.go"},
					Create: []string{"engine_output.go"},
					Forbid: []string{"research/*"},
				},
				References: []goals.Reference{
					{
						Path:    "engine.go",
						Symbol:  "Engine",
						Excerpt: "type Engine struct{}",
						SHA256:  engineSHA,
					},
				},
				Decisions: []goals.Decision{},
				Steps: []goals.Step{
					{ID: "S-001", Action: "Scaffold interface", Expected: "Interface declared", OnFailure: "Halt"},
					{ID: "S-002", Action: "Implement logic", Expected: "Logic running", OnFailure: "Halt"},
					{ID: "S-003", Action: "Verify behavior", Expected: "Tests pass", OnFailure: "Halt"},
				},
				Interfaces:    []string{"type Engine struct{}"},
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
							{Kind: goals.AssertionGoTestPass, Target: "github.com/jadmadi/sila/cmd/sila/TestGoalsResumeCLI", Expected: true},
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
				Requirement:   "Engine runs cleanly",
				Given:         "Fresh environment",
				Action:        "Start engine",
				Expected:      "Clean exit",
				RequiredScope: goals.ScopeUnit,
				Mandatory:     true,
				CheckIDs:      []string{"C-001"},
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
		Verdict:            "ready",
		Shortcuts:          []goals.ShortcutCounter{},
		UnresolvedBlockers: []string{},
	}
	revData, _ := json.MarshalIndent(review, "", "  ")
	_ = os.WriteFile(filepath.Join(goalDir, "spec-review.json"), revData, 0644)

	// Save active claim
	if claimAgent != "" {
		claim := &goals.GoalClaim{
			GoalSlug:  slug,
			Agent:     claimAgent,
			ClaimedBy: claimAgent,
			ClaimedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
			Note:      "executing resume task",
		}
		claimData, _ := json.MarshalIndent(claim, "", "  ")
		if err := os.WriteFile(filepath.Join(goalDir, "claim.json"), claimData, 0644); err != nil {
			t.Fatalf("write claim.json: %v", err)
		}
	}

	return dir, pkg
}

func TestGoalsResumeCLI(t *testing.T) {
	slug := "test-resume-cli-goal"
	claimAgent := "antigravity"
	dir, pkg := setupCLIResumeFixture(t, slug, claimAgent)

	specDigest, err := goals.WorkPackageDigest(pkg)
	if err != nil {
		t.Fatalf("compute spec digest: %v", err)
	}

	packet, err := goals.BuildTaskPacket(dir, slug, "T-001")
	if err != nil {
		t.Fatalf("build initial task packet: %v", err)
	}
	packetDigest, err := goals.ComputeTaskPacketDigest(packet)
	if err != nil {
		t.Fatalf("compute packet digest: %v", err)
	}

	// 1. AC-010: CLI checkpoint -> fresh invocation resume produces exact continuation fields.
	cp := goals.TaskCheckpoint{
		SchemaVersion:   goals.TaskCheckpointSchemaVersion,
		GoalSlug:        slug,
		TaskID:          "T-001",
		Sequence:        1,
		SpecDigest:      specDigest,
		PacketDigest:    packetDigest,
		CandidateDigest: "",
		CompletedSteps:  []string{"S-001"},
		EvidencePaths:   []string{"evidence/T-001/receipt-step1.json"},
		Attempts: []goals.CheckpointAttempt{
			{
				Executable:       "go",
				Args:             []string{"test", "./..."},
				ExitCode:         0,
				FailureSignature: "",
			},
		},
		ChangedFiles: []string{"engine.go"},
		NextStep:     "S-002",
		Status:       goals.TaskCheckpointStatusInProgress,
		Blocker:      nil,
	}

	cpFile := filepath.Join(dir, "checkpoint-input.json")
	cpBytes, err := json.MarshalIndent(cp, "", "  ")
	if err != nil {
		t.Fatalf("marshal checkpoint: %v", err)
	}
	if err := os.WriteFile(cpFile, cpBytes, 0644); err != nil {
		t.Fatalf("write checkpoint file: %v", err)
	}

	// Save checkpoint via CLI
	stdout, stderr, exitCode := runResumeCLI(t, dir, claimAgent, true, "checkpoint", slug, "T-001", "--from="+cpFile)
	if exitCode != 0 {
		t.Fatalf("checkpoint command failed with code %d; stderr: %s; stdout: %s", exitCode, stderr, stdout)
	}

	var cpRes map[string]any
	if err := json.Unmarshal([]byte(stdout), &cpRes); err != nil {
		t.Fatalf("failed to decode checkpoint response: %v; raw: %s", err, stdout)
	}
	if saved, ok := cpRes["saved"].(bool); !ok || !saved {
		t.Fatalf("expected saved: true, got %v", cpRes["saved"])
	}

	// Fresh invocation: resume task via CLI
	resStdout, resStderr, resExitCode := runResumeCLI(t, dir, claimAgent, true, "resume", slug, "T-001")
	if resExitCode != 0 {
		t.Fatalf("resume command failed with code %d; stderr: %s; stdout: %s", resExitCode, resStderr, resStdout)
	}

	var resumedPacket goals.TaskPacket
	if err := json.Unmarshal([]byte(resStdout), &resumedPacket); err != nil {
		t.Fatalf("failed to decode resume packet: %v; raw: %s", err, resStdout)
	}

	if resumedPacket.Resume == nil {
		t.Fatalf("expected non-nil resume envelope in resumed task packet")
	}
	if resumedPacket.Resume.Sequence != 1 {
		t.Errorf("resume sequence = %d, expected 1", resumedPacket.Resume.Sequence)
	}
	if resumedPacket.Resume.NextStep != "S-002" {
		t.Errorf("resume next_step = %q, expected 'S-002'", resumedPacket.Resume.NextStep)
	}
	if len(resumedPacket.Resume.CompletedSteps) != 1 || resumedPacket.Resume.CompletedSteps[0] != "S-001" {
		t.Errorf("resume completed_steps = %v, expected ['S-001']", resumedPacket.Resume.CompletedSteps)
	}
	if len(resumedPacket.Resume.ChangedFiles) != 1 || resumedPacket.Resume.ChangedFiles[0] != "engine.go" {
		t.Errorf("resume changed_files = %v, expected ['engine.go']", resumedPacket.Resume.ChangedFiles)
	}
	if resumedPacket.Resume.Status != goals.TaskCheckpointStatusInProgress {
		t.Errorf("resume status = %q, expected %q", resumedPacket.Resume.Status, goals.TaskCheckpointStatusInProgress)
	}

	// 2. AC-012: Wrong active owner, stale revision, and bad path reject; no force-release side effect occurs.

	// Case 1: Wrong active owner on checkpoint write
	wrongOwnerStdout, wrongOwnerStderr, wrongOwnerExit := runResumeCLI(t, dir, "imposter-agent", true, "checkpoint", slug, "T-001", "--from="+cpFile)
	if wrongOwnerExit == 0 {
		t.Fatalf("expected nonzero exit code for wrong active owner checkpoint write")
	}
	combined := wrongOwnerStdout + wrongOwnerStderr
	if !strings.Contains(combined, "claim owner mismatch") {
		t.Errorf("expected claim owner mismatch error, got: %s", combined)
	}

	// Verify claim is preserved and not force-released
	claimOnDisk, err := goals.LoadGoalClaim(dir, slug)
	if err != nil || claimOnDisk == nil {
		t.Fatalf("active claim was unexpectedly lost or corrupted: %v", err)
	}
	if claimOnDisk.Agent != claimAgent {
		t.Errorf("claim agent changed: got %q, want %q", claimOnDisk.Agent, claimAgent)
	}

	// Case 2: Bad path on checkpoint write
	badPathStdout, badPathStderr, badPathExit := runResumeCLI(t, dir, claimAgent, true, "checkpoint", slug, "T-001", "--from=/no/such/file.json")
	if badPathExit == 0 {
		t.Fatalf("expected nonzero exit code for nonexistent checkpoint path")
	}
	combinedBad := badPathStdout + badPathStderr
	if !strings.Contains(combinedBad, "no such file") && !strings.Contains(combinedBad, "reading checkpoint") {
		t.Errorf("expected read error, got: %s", combinedBad)
	}

	// Verify claim is still intact
	claimOnDisk, err = goals.LoadGoalClaim(dir, slug)
	if err != nil || claimOnDisk == nil {
		t.Fatalf("active claim was lost after bad path: %v", err)
	}

	// Case 3: Stale revision on task amendment
	nextPkg := *pkg
	nextPkg.Revision = 3 // Stale/skipping revision (expected 2)
	nextPkgBytes, _ := json.MarshalIndent(nextPkg, "", "  ")
	nextPkgFile := filepath.Join(dir, "next-pkg.json")
	_ = os.WriteFile(nextPkgFile, nextPkgBytes, 0644)

	nextDigest, _ := goals.WorkPackageDigest(&nextPkg)
	staleAmendment := goals.TaskAmendment{
		SchemaVersion:       goals.TaskAmendmentSchemaVersion,
		GoalSlug:            slug,
		OldRevision:         1,
		NewRevision:         3, // Stale/invalid revision increment
		OldDigest:           specDigest,
		NewDigest:           nextDigest,
		Reason:              "Stale revision test",
		Authority:           "operator",
		AffectedTaskIDs:     []string{"T-001"},
		AffectedCaseIDs:     []string{"AC-001"},
		RetainedReceipts:    []string{},
		InvalidatedReceipts: []string{},
		ApprovedAt:          time.Now().UTC().Format(time.RFC3339),
	}
	amendFile := filepath.Join(dir, "amend-stale.json")
	amendBytes, _ := json.MarshalIndent(staleAmendment, "", "  ")
	_ = os.WriteFile(amendFile, amendBytes, 0644)

	staleStdout, staleStderr, staleExit := runResumeCLI(t, dir, claimAgent, true, "amend", slug, "--from="+amendFile, "--package="+nextPkgFile)
	if staleExit == 0 {
		t.Fatalf("expected nonzero exit code for stale amendment revision")
	}
	combinedStale := staleStdout + staleStderr
	if !strings.Contains(combinedStale, "revision conflict") && !strings.Contains(combinedStale, "invalid new_revision") {
		t.Errorf("expected revision conflict, got: %s", combinedStale)
	}

	// Verify work package was NOT modified
	persistedPkg, err := goals.LoadWorkPackage(dir, slug)
	if err != nil {
		t.Fatalf("load work package: %v", err)
	}
	if persistedPkg.Revision != 1 {
		t.Errorf("work package revision changed: got %d, want 1", persistedPkg.Revision)
	}

	// Case 4: Wrong active owner on amend
	validNextPkg := *pkg
	validNextPkg.Revision = 2
	validNextPkgBytes, _ := json.MarshalIndent(validNextPkg, "", "  ")
	validNextPkgFile := filepath.Join(dir, "valid-next-pkg.json")
	_ = os.WriteFile(validNextPkgFile, validNextPkgBytes, 0644)

	validNextDigest, _ := goals.WorkPackageDigest(&validNextPkg)
	validAmendment := goals.TaskAmendment{
		SchemaVersion:       goals.TaskAmendmentSchemaVersion,
		GoalSlug:            slug,
		OldRevision:         1,
		NewRevision:         2,
		OldDigest:           specDigest,
		NewDigest:           validNextDigest,
		Reason:              "Valid revision test",
		Authority:           "operator",
		AffectedTaskIDs:     []string{"T-001"},
		AffectedCaseIDs:     []string{"AC-001"},
		RetainedReceipts:    []string{},
		InvalidatedReceipts: []string{},
		ApprovedAt:          time.Now().UTC().Format(time.RFC3339),
	}
	validAmendFile := filepath.Join(dir, "amend-valid.json")
	validAmendBytes, _ := json.MarshalIndent(validAmendment, "", "  ")
	_ = os.WriteFile(validAmendFile, validAmendBytes, 0644)

	amendOwnerStdout, amendOwnerStderr, amendOwnerExit := runResumeCLI(t, dir, "imposter-agent", true, "amend", slug, "--from="+validAmendFile, "--package="+validNextPkgFile)
	if amendOwnerExit == 0 {
		t.Fatalf("expected nonzero exit code for imposter amend attempt")
	}
	combinedOwner := amendOwnerStdout + amendOwnerStderr
	if !strings.Contains(combinedOwner, "claim owner mismatch") {
		t.Errorf("expected claim owner mismatch for amend, got: %s", combinedOwner)
	}

	// Verify claim still held
	claimOnDisk, err = goals.LoadGoalClaim(dir, slug)
	if err != nil || claimOnDisk == nil || claimOnDisk.Agent != claimAgent {
		t.Fatalf("claim corrupted after rejected amend attempt")
	}

	// Case 5: Valid amend by true owner succeeds
	amendStdout, amendStderr, amendExit := runResumeCLI(t, dir, claimAgent, true, "amend", slug, "--from="+validAmendFile, "--package="+validNextPkgFile)
	if amendExit != 0 {
		t.Fatalf("valid amend failed with code %d; stderr: %s; stdout: %s", amendExit, amendStderr, amendStdout)
	}

	var amendRes map[string]any
	if err := json.Unmarshal([]byte(amendStdout), &amendRes); err != nil {
		t.Fatalf("failed to decode amend response: %v; raw: %s", err, amendStdout)
	}
	if amended, ok := amendRes["amended"].(bool); !ok || !amended {
		t.Fatalf("expected amended: true in response, got %v", amendRes["amended"])
	}

	// Verify revision increment on disk
	amendedPkg, err := goals.LoadWorkPackage(dir, slug)
	if err != nil {
		t.Fatalf("load amended package: %v", err)
	}
	if amendedPkg.Revision != 2 {
		t.Errorf("expected amended revision 2, got %d", amendedPkg.Revision)
	}
}

func TestReportGoalCheckpoint(t *testing.T) {
	tmp := t.TempDir()
	st, err := store.Open(filepath.Join(tmp, "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if _, err := st.EnsureProject(tmp); err != nil {
		t.Fatalf("ensure project: %v", err)
	}

	slug := "test-mcp-report-checkpoint"
	goalDir := filepath.Join(tmp, "goals", slug)
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		t.Fatalf("mkdir goal dir: %v", err)
	}

	// Create goal.md
	goalMD := "# Goal: Test MCP Report Checkpoint\n\n## Status\nclaimed\n"
	if err := os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte(goalMD), 0644); err != nil {
		t.Fatalf("write goal.md: %v", err)
	}

	// Establish active claim
	claim := &goals.GoalClaim{
		GoalSlug:  slug,
		Agent:     "test-agent",
		ClaimedBy: "test-agent",
		ClaimedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
		Note:      "initial active note",
	}
	claimData, _ := json.MarshalIndent(claim, "", "  ")
	if err := os.WriteFile(filepath.Join(goalDir, "claim.json"), claimData, 0644); err != nil {
		t.Fatalf("write claim.json: %v", err)
	}

	callTool := func(toolName string, args map[string]any) (map[string]any, *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    any    `json:"data"`
	}) {
		reqArgs, _ := json.Marshal(args)
		req := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":%q,"arguments":%s}}`+"\n", toolName, reqArgs)
		in := strings.NewReader(req)
		var out bytes.Buffer
		srv := mcp.NewServer(st, in, &out)
		if err := srv.Run(); err != nil {
			t.Fatalf("server.Run() failed: %v", err)
		}
		var resp struct {
			Result map[string]any `json:"result"`
			Error  *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
				Data    any    `json:"data"`
			} `json:"error"`
		}
		if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
			t.Fatalf("failed to decode response: %v; raw: %s", err, out.String())
		}
		return resp.Result, resp.Error
	}

	// 1. Save an initial valid checkpoint via report_goal
	initCP := map[string]any{
		"schema_version":   goals.TaskCheckpointSchemaVersion,
		"goal_slug":        slug,
		"task_id":          "T-001",
		"sequence":         1,
		"spec_digest":      "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		"packet_digest":    "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789",
		"candidate_digest": "",
		"completed_steps":  []string{"S-001"},
		"evidence_paths":   []string{},
		"attempts": []map[string]any{
			{
				"executable":        "go",
				"args":              []string{"test", "./..."},
				"exit_code":         0,
				"failure_signature": "",
			},
		},
		"changed_files": []string{"file1.go"},
		"next_step":     "S-002",
		"status":        goals.TaskCheckpointStatusInProgress,
	}

	res, rErr := callTool("report_goal", map[string]any{
		"project":         tmp,
		"slug":            slug,
		"note":            "step 1 completed",
		"task_checkpoint": initCP,
	})
	if rErr != nil {
		t.Fatalf("initial report_goal returned JSON-RPC error: %v", rErr)
	}
	if isErr, ok := res["isError"].(bool); ok && isErr {
		t.Fatalf("initial report_goal returned tool error: %v", res)
	}

	// Verify checkpoint sequence 1 is on disk
	cpOnDisk, err := goals.LoadTaskCheckpoint(tmp, slug, "T-001")
	if err != nil {
		t.Fatalf("load initial checkpoint: %v", err)
	}
	if cpOnDisk.Sequence != 1 || cpOnDisk.NextStep != "S-002" {
		t.Fatalf("unexpected checkpoint on disk: %+v", cpOnDisk)
	}

	// 2. AC-011: Call report_goal with INVALID checkpoint:
	// Sequence 1 again (stale sequence conflict: sequence must be monotonic)
	invalidCP := map[string]any{
		"schema_version":   goals.TaskCheckpointSchemaVersion,
		"goal_slug":        slug,
		"task_id":          "T-001",
		"sequence":         1, // Stale sequence, already sequence 1 exists!
		"spec_digest":      "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		"packet_digest":    "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789",
		"candidate_digest": "",
		"completed_steps":  []string{"S-001", "S-002"},
		"evidence_paths":   []string{},
		"attempts":         []map[string]any{},
		"changed_files":    []string{"file1.go"},
		"next_step":        "S-003",
		"status":           goals.TaskCheckpointStatusInProgress,
	}

	invRes, invErr := callTool("report_goal", map[string]any{
		"project":         tmp,
		"slug":            slug,
		"note":            "trying invalid checkpoint",
		"task_checkpoint": invalidCP,
	})

	isErrorResult := false
	if invErr != nil {
		isErrorResult = true
	} else if isErr, ok := invRes["isError"].(bool); ok && isErr {
		isErrorResult = true
	}

	if !isErrorResult {
		t.Fatalf("expected error for report_goal with invalid checkpoint, got success: %v", invRes)
	}

	// Verify prior checkpoint on disk is PRESERVED intact
	preservedCP, err := goals.LoadTaskCheckpoint(tmp, slug, "T-001")
	if err != nil {
		t.Fatalf("load preserved checkpoint: %v", err)
	}
	if preservedCP.Sequence != 1 {
		t.Errorf("checkpoint sequence altered: got %d, want 1", preservedCP.Sequence)
	}
	if preservedCP.NextStep != "S-002" {
		t.Errorf("checkpoint next_step altered: got %q, want 'S-002'", preservedCP.NextStep)
	}

	// 3. AC-011: Ordinary note reports still work without task_checkpoint
	noteRes, noteErr := callTool("report_goal", map[string]any{
		"project": tmp,
		"slug":    slug,
		"note":    "ordinary progress update without checkpoint",
		"status":  "evaluating",
	})
	if noteErr != nil {
		t.Fatalf("ordinary report_goal returned error: %v", noteErr)
	}
	if isErr, ok := noteRes["isError"].(bool); ok && isErr {
		t.Fatalf("ordinary report_goal returned tool error: %v", noteRes)
	}

	// Verify claim note was updated on disk
	updatedClaim, err := goals.LoadGoalClaim(tmp, slug)
	if err != nil {
		t.Fatalf("load updated claim: %v", err)
	}
	if updatedClaim.Note != "ordinary progress update without checkpoint" {
		t.Errorf("claim note = %q, want 'ordinary progress update without checkpoint'", updatedClaim.Note)
	}

	// Verify prior checkpoint is still preserved
	finalCP, err := goals.LoadTaskCheckpoint(tmp, slug, "T-001")
	if err != nil {
		t.Fatalf("load final checkpoint: %v", err)
	}
	if finalCP.Sequence != 1 || finalCP.NextStep != "S-002" {
		t.Errorf("checkpoint was unexpectedly modified by note update: %+v", finalCP)
	}
}

func TestGoalStopAtTask(t *testing.T) {
	slug := "test-stop-at-cli"
	agent := "antigravity"
	dir, _ := setupCLIResumeFixture(t, slug, agent)

	out, stderr, code := runResumeCLI(t, dir, agent, true, "checkpoint", slug, "--stop-at=T-001")
	if code != 0 {
		t.Fatalf("stop-at exit %d stderr=%s out=%s", code, stderr, out)
	}
	claim, err := goals.LoadGoalClaim(dir, slug)
	if err != nil {
		t.Fatal(err)
	}
	if claim.StopAtTaskID != "T-001" {
		t.Fatalf("stop_at_task_id = %q, want T-001", claim.StopAtTaskID)
	}

	out, stderr, code = runResumeCLI(t, dir, agent, true, "checkpoint", slug, "--clear-stop")
	if code != 0 {
		t.Fatalf("clear-stop exit %d stderr=%s out=%s", code, stderr, out)
	}
	claim, err = goals.LoadGoalClaim(dir, slug)
	if err != nil {
		t.Fatal(err)
	}
	if claim.StopAtTaskID != "" {
		t.Fatalf("stop_at_task_id = %q after clear, want empty", claim.StopAtTaskID)
	}
}
