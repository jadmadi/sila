package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/agents"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func TestAgentsCheckJSONExposesManagedFields(t *testing.T) {
	tmp := t.TempDir()
	opts := agents.CompileOptions{RepoRoot: tmp, ProjectName: "json-check", Stack: "go"}
	if _, err := agents.DeployTargets(tmp, opts, []string{"agents"}); err != nil {
		t.Fatal(err)
	}

	ui := render.New(os.Stdout, true)
	out := captureStdout(t, func() {
		runAgentsCheck(ui, tmp, opts, true)
	})

	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		t.Fatalf("expected valid JSON, got %q: %v", out, err)
	}
	for _, key := range []string{"managed_revision", "managed_hash", "hub_managed_hash", "hash_match", "legacy_block", "core_hub_hash", "current_hash"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("missing JSON field %q in %s", key, out)
		}
	}

	var report agents.DriftReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatal(err)
	}
	if report.HasDrift {
		t.Fatalf("expected in-sync report, details: %v", report.Details)
	}
	if report.ManagedRevision != agents.ManagedRevision || len(report.ManagedHash) != 16 {
		t.Fatalf("managed fields = rev=%d hash=%q", report.ManagedRevision, report.ManagedHash)
	}
	if report.ManagedHash != report.HubManagedHash || !report.HashMatch || report.LegacyBlock {
		t.Fatalf("managed hash contract = %+v", report)
	}
}

func TestAgentsSyncJSONHashSkip(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "agents-sync.db")
	ui := render.New(os.Stdout, true)

	opts := agents.CompileOptions{RepoRoot: tmp, ProjectName: "json-sync", Stack: "go"}
	out := captureStdout(t, func() {
		runAgentsSync(ui, tmp, opts, true, dbPath)
	})
	var first agents.SyncResult
	if err := json.Unmarshal([]byte(out), &first); err != nil {
		t.Fatalf("expected valid JSON, got %q: %v", out, err)
	}
	if first.ManagedRevision != agents.ManagedRevision || len(first.ManagedHash) != 16 {
		t.Fatalf("first sync managed fields = rev=%d hash=%q", first.ManagedRevision, first.ManagedHash)
	}
	if len(first.FilesWritten) != 1 || first.FilesWritten[0] != "AGENTS.md" {
		t.Fatalf("first sync writes = %v", first.FilesWritten)
	}

	dry := opts
	dry.Preview = true
	out = captureStdout(t, func() {
		runAgentsSync(ui, tmp, dry, true, dbPath)
	})
	var second agents.SyncResult
	if err := json.Unmarshal([]byte(out), &second); err != nil {
		t.Fatalf("expected valid JSON, got %q: %v", out, err)
	}
	if !second.Preview || second.HashSkips != 1 {
		t.Fatalf("dry-run result = preview=%v hash_skips=%d", second.Preview, second.HashSkips)
	}
	if second.ManagedHash != first.ManagedHash || second.ManagedRevision != first.ManagedRevision {
		t.Fatalf("dry-run hash = %q rev=%d, want %q rev=%d", second.ManagedHash, second.ManagedRevision, first.ManagedHash, first.ManagedRevision)
	}

	var plan *agents.PlannedWrite
	for i := range second.Plan {
		if second.Plan[i].Path == "AGENTS.md" {
			plan = &second.Plan[i]
		}
	}
	if plan == nil {
		t.Fatalf("AGENTS.md missing from dry-run plan: %+v", second.Plan)
	}
	if plan.Action != "skip" || !plan.HashMatch || plan.Detail != "managed hash match (rev=1)" {
		t.Fatalf("dry-run plan = %+v", plan)
	}
	if plan.ManagedHash != first.ManagedHash || plan.ExistingManagedHash != first.ManagedHash {
		t.Fatalf("dry-run plan hashes = %+v", plan)
	}

	human := captureStdout(t, func() {
		runAgentsSync(ui, tmp, dry, false, dbPath)
	})
	if !strings.Contains(human, "skip AGENTS.md — managed hash match (rev=1)") {
		t.Fatalf("human sync output missing hash skip line:\n%s", human)
	}
}

func TestAgentsStatusJSONExposesReceiptManagedFields(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "agents-status.db")
	hash := strings.Repeat("d", 16)

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	rec := &agents.SyncReceipt{
		Timestamp:       "2026-10-21T00:00:00Z",
		HubCommit:       "abc1234",
		Scope:           "json-status",
		Initiator:       "tester",
		TotalProjects:   1,
		Succeeded:       1,
		ManagedRevision: agents.ManagedRevision,
		ManagedHash:     hash,
		Projects: []agents.ProjectReceipt{
			{ProjectID: "json-status", Path: tmp, Success: true, ManagedHash: hash, ManagedRevision: agents.ManagedRevision, HashSkips: 1, SkippedFiles: []string{"AGENTS.md"}},
		},
	}
	if err := agents.SaveSyncReceipt(st, rec); err != nil {
		st.Close()
		t.Fatal(err)
	}
	st.Close()

	ui := render.New(os.Stdout, true)
	out := captureStdout(t, func() {
		runAgentsStatus(ui, dbPath, "json-status", false, true)
	})

	var payload struct {
		agents.SyncReceipt
		RelativeAge string `json:"relative_age"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("expected valid JSON, got %q: %v", out, err)
	}
	if payload.ManagedRevision != agents.ManagedRevision || payload.ManagedHash != hash {
		t.Fatalf("status managed fields = rev=%d hash=%q", payload.ManagedRevision, payload.ManagedHash)
	}
	if len(payload.Projects) != 1 || payload.Projects[0].HashSkips != 1 || len(payload.Projects[0].SkippedFiles) != 1 {
		t.Fatalf("status project fields = %+v", payload.Projects)
	}
}

func TestAgentsStatusHumanDegradesForOldReceipt(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "agents-old.db")

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	oldJSON := `{"timestamp":"2026-10-01T12:00:00Z","hub_commit":"old1234","scope":"old","initiator":"legacy","total_projects":1,"succeeded":1,"failed":0,"files_written":1,"symlinks":0,"projects":[]}`
	if err := st.SetState(agents.LastSyncReceiptKey, oldJSON); err != nil {
		st.Close()
		t.Fatal(err)
	}
	st.Close()

	ui := render.New(os.Stdout, true)
	out := captureStdout(t, func() {
		runAgentsStatus(ui, dbPath, "old", false, false)
	})
	if !strings.Contains(out, "not recorded (pre-fingerprint receipt)") {
		t.Fatalf("old receipt did not degrade cleanly:\n%s", out)
	}
}

func TestAgentsSyncJSONSummaryContract(t *testing.T) {
	tmp := t.TempDir()
	db := filepath.Join(tmp, "store.db")
	ui := render.New(os.Stdout, true)
	opts := agents.CompileOptions{RepoRoot: tmp, ProjectName: "demo"}

	firstOut := captureStdout(t, func() {
		runAgentsSync(ui, tmp, opts, true, db)
	})
	var first agents.SyncResult
	if err := json.Unmarshal([]byte(firstOut), &first); err != nil {
		t.Fatalf("first sync JSON: %v\n%s", err, firstOut)
	}
	if !first.ChangedOnly || first.Forced || first.Written == 0 || first.Failed != 0 {
		t.Fatalf("first sync summary = %+v", first)
	}

	secondOut := captureStdout(t, func() {
		runAgentsSync(ui, tmp, opts, true, db)
	})
	var second agents.SyncResult
	if err := json.Unmarshal([]byte(secondOut), &second); err != nil {
		t.Fatalf("second sync JSON: %v\n%s", err, secondOut)
	}
	if second.Written != 0 || second.Skipped == 0 || second.Failed != 0 {
		t.Fatalf("second sync summary = %+v", second)
	}

	forced := opts
	forced.Force = true
	forceOut := captureStdout(t, func() {
		runAgentsSync(ui, tmp, forced, true, db)
	})
	var forcedRes agents.SyncResult
	if err := json.Unmarshal([]byte(forceOut), &forcedRes); err != nil {
		t.Fatalf("force sync JSON: %v\n%s", err, forceOut)
	}
	if forcedRes.ChangedOnly || !forcedRes.Forced || forcedRes.Written == 0 {
		t.Fatalf("force sync summary = %+v", forcedRes)
	}
}

func TestAgentsForceChangedOnlyConflict(t *testing.T) {
	if probe := os.Getenv("SILA_AGENTS_FLAG_PROBE"); probe != "" {
		os.Args = strings.Split(probe, "\x1f")
		main()
		os.Exit(0)
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestAgentsForceChangedOnlyConflict")
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "SILA_AGENTS_FLAG_PROBE="+strings.Join([]string{"sila", "agents", "sync", "--force", "--changed-only"}, "\x1f"))
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		exitErr, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("run failed: %v", err)
		}
		code = exitErr.ExitCode()
	}
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (stdout=%q stderr=%q)", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "mutually exclusive") {
		t.Fatalf("stderr missing conflict message: %q", stderr.String())
	}
}
