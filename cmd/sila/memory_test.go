package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/dream"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func TestMemoryReportEmptyStore(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "memory-report.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	out := captureStdout(t, func() {
		runMemoryReport(render.New(os.Stdout, true), st, "", "", true, false)
	})
	var report map[string]any
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("report JSON: %v\n%s", err, out)
	}
	if report["schema"] != "sila/memory-adoption/v1" {
		t.Fatalf("schema: %v", report["schema"])
	}
}

const memoryCLIFixture = `{
  "schema_version": "sila/memory/v1",
  "project_id": "cli-proj",
  "kind": "root_cause",
  "title": "CLI envelope",
  "summary": "The CLI writes a typed envelope.",
  "assertions": [{
    "id": "A-001",
    "assertion_type": "descriptive",
    "predicate": "causes",
    "statement": "The CLI writes a typed envelope.",
    "subject": {"context_entity": "project", "value": "cli-proj"},
    "object": {"literal": {"type": "string", "value": "fix"}},
    "confidence": 0.8,
    "importance": 4,
    "evidence_refs": ["E-001"]
  }],
  "evidence": [{"id": "E-001", "kind": "agent_initiated", "locator": {}, "summary": "Recorded by the CLI test."}],
  "idempotency_key": "cli-fixture-key-0001"
}`

const memoryCLIInvalidFixture = `{"schema_version":"sila/memory/v1","project_id":"cli-proj","kind":"guess","title":"x","summary":"y","assertions":[],"evidence":[],"extra":true}`

func TestMemoryCLIAddValidate(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "cli.db")
	file := filepath.Join(dir, "memory.json")
	if err := os.WriteFile(file, []byte(memoryCLIFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	ui := render.New(os.Stdout, true)
	out := captureStdout(t, func() {
		runMemory(ui, db, "", "", "72h", file, "", "tester", 50, false, false, false, memoryLifecycleOptions{}, []string{"add"})
	})
	if !strings.Contains(out, "memory mem_") || !strings.Contains(out, "digest") {
		t.Fatalf("add output: %s", out)
	}
	replay := captureStdout(t, func() {
		runMemory(ui, db, "", "", "72h", file, "", "tester", 50, false, true, false, memoryLifecycleOptions{}, []string{"add"})
	})
	var replayPayload struct {
		Created bool `json:"created"`
	}
	if err := json.Unmarshal([]byte(replay), &replayPayload); err != nil {
		t.Fatalf("replay JSON: %v\n%s", err, replay)
	}
	if replayPayload.Created {
		t.Fatal("second add was not idempotent")
	}
	valid := captureStdout(t, func() {
		runMemory(ui, db, "", "", "72h", file, "", "tester", 50, false, true, false, memoryLifecycleOptions{}, []string{"validate"})
	})
	if !strings.Contains(valid, `"valid": true`) {
		t.Fatalf("validate output: %s", valid)
	}
	list := captureStdout(t, func() {
		runMemory(ui, db, "", "", "72h", "", "", "tester", 50, false, true, false, memoryLifecycleOptions{}, []string{"list"})
	})
	var listPayload struct {
		Schema   string `json:"schema"`
		Memories []struct {
			ID string `json:"id"`
		} `json:"memories"`
		Summary struct {
			Total int `json:"total"`
		} `json:"summary"`
	}
	if err := json.Unmarshal([]byte(list), &listPayload); err != nil {
		t.Fatalf("list JSON: %v\n%s", err, list)
	}
	if listPayload.Schema != "sila/memory-list/v1" || listPayload.Summary.Total != 1 || len(listPayload.Memories) != 1 {
		t.Fatalf("list payload: %+v", listPayload)
	}
	id := listPayload.Memories[0].ID
	show := captureStdout(t, func() {
		runMemory(ui, db, "", "", "72h", "", "", "tester", 50, false, true, false, memoryLifecycleOptions{}, []string{"show", id})
	})
	if !strings.Contains(show, `"id": "`+id+`"`) {
		t.Fatalf("show output: %s", show)
	}
	digest := captureStdout(t, func() {
		runMemory(ui, db, "", "", "72h", "", "", "tester", 50, false, true, false, memoryLifecycleOptions{}, []string{"digest", id})
	})
	if !strings.Contains(digest, `"digest"`) || !strings.Contains(digest, `"canonical"`) {
		t.Fatalf("digest output: %s", digest)
	}
}

func TestMemoryCLIInvalidInput(t *testing.T) {
	if os.Getenv("SILA_MEMORY_CLI_SUB") == "1" {
		db := os.Getenv("SILA_MEMORY_DB")
		file := os.Getenv("SILA_MEMORY_FILE")
		runMemory(render.New(os.Stdout, true), db, "", "", "72h", file, "", "tester", 50, false, false, false, memoryLifecycleOptions{}, []string{"add"})
		return
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "invalid.json")
	if err := os.WriteFile(file, []byte(memoryCLIInvalidFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestMemoryCLIInvalidInput")
	cmd.Env = append(os.Environ(),
		"SILA_MEMORY_CLI_SUB=1",
		"SILA_MEMORY_DB="+filepath.Join(dir, "cli.db"),
		"SILA_MEMORY_FILE="+file,
	)
	var out strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected a non-zero exit, got %v (%s)", err, out.String())
	}
	if exitErr.ExitCode() != 2 {
		t.Fatalf("exit code = %d, want 2 (%s)", exitErr.ExitCode(), out.String())
	}
	if !strings.Contains(out.String(), "MEMORY_FIELD_UNKNOWN") && !strings.Contains(out.String(), "MEMORY_LIMIT") {
		t.Fatalf("missing stable memory code: %s", out.String())
	}
}

func seedCLILifecycleMemory(t *testing.T, db, summary string) *store.MemoryRecord {
	t.Helper()
	env, err := store.DecodeMemoryEnvelope([]byte(strings.Replace(memoryCLIFixture, "The CLI writes a typed envelope.", summary, -1)))
	if err != nil {
		t.Fatal(err)
	}
	env.IdempotencyKey = ""
	env.MemoryID = ""
	st, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	rec, _, err := st.SaveMemoryEnvelope(context.Background(), "cli-proj", env, store.Provenance{})
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

func TestMemoryArchiveCLI(t *testing.T) {
	db := filepath.Join(t.TempDir(), "cli-archive.db")
	rec := seedCLILifecycleMemory(t, db, "The CLI archives a typed envelope.")
	ui := render.New(os.Stdout, true)
	out := captureStdout(t, func() {
		runMemory(ui, db, "", "", "72h", "", "", "tester", 50, false, true, false, memoryLifecycleOptions{
			reason: "wrong", authority: "user_request", note: "superseded fact",
		}, []string{"archive", rec.ID})
	})
	var payload struct {
		OK     bool `json:"ok"`
		Memory struct {
			State     string `json:"state"`
			Reason    string `json:"reason"`
			Authority string `json:"authority"`
			ArchiveID string `json:"archive_id"`
		} `json:"memory"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("archive JSON: %v\n%s", err, out)
	}
	if !payload.OK || payload.Memory.State != "archived" || payload.Memory.Reason != "wrong" || payload.Memory.ArchiveID == "" {
		t.Fatalf("archive payload: %+v", payload)
	}
	list := captureStdout(t, func() {
		runMemory(ui, db, "", "", "72h", "", "", "tester", 50, false, true, false, memoryLifecycleOptions{}, []string{"archive", "list"})
	})
	if !strings.Contains(list, `"schema": "sila/memory-archive-list/v1"`) || !strings.Contains(list, rec.ID) {
		t.Fatalf("archive list output: %s", list)
	}
	storeHandle, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer storeHandle.Close()
	if _, err := storeHandle.MemoryRecordByID(rec.ID); err != nil {
		t.Fatal(err)
	}
}

func TestMemoryRestoreCLI(t *testing.T) {
	db := filepath.Join(t.TempDir(), "cli-restore.db")
	rec := seedCLILifecycleMemory(t, db, "The CLI restores a typed envelope.")
	st, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	archived, err := st.ArchiveMemory(context.Background(), "cli-proj", rec.ID, store.ArchiveAuthorityUserRequest, store.ArchiveReasonOutdated, "", "user", "")
	if err != nil {
		t.Fatal(err)
	}
	_ = st.Close()
	ui := render.New(os.Stdout, true)
	out := captureStdout(t, func() {
		runMemory(ui, db, "", "", "72h", "", "", "tester", 50, false, true, false, memoryLifecycleOptions{}, []string{"restore", archived.ArchiveID})
	})
	var payload struct {
		OK      bool `json:"ok"`
		Restore struct {
			Restored bool   `json:"restored"`
			State    string `json:"state"`
		} `json:"restore"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("restore JSON: %v\n%s", err, out)
	}
	if !payload.OK || !payload.Restore.Restored || payload.Restore.State != "current" {
		t.Fatalf("restore payload: %+v", payload)
	}
	second := captureStdout(t, func() {
		runMemory(ui, db, "", "", "72h", "", "", "tester", 50, false, true, false, memoryLifecycleOptions{}, []string{"restore", rec.ID})
	})
	if !strings.Contains(second, `"restored": false`) {
		t.Fatalf("repeat restore was not idempotent: %s", second)
	}
}

func TestMemoryHistoryCLI(t *testing.T) {
	if os.Getenv("SILA_MEMORY_HISTORY_TAMPER_SUB") == "1" {
		db := os.Getenv("SILA_MEMORY_DB")
		id := os.Getenv("SILA_MEMORY_ID")
		st, err := store.Open(db)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.DB.Exec(`UPDATE memory_lifecycle SET note='tampered' WHERE memory_id=?`, id); err != nil {
			t.Fatal(err)
		}
		_ = st.Close()
		runMemory(render.New(os.Stdout, true), db, "", "", "72h", "", "", "tester", 50, false, false, false, memoryLifecycleOptions{verify: true}, []string{"history", id})
		return
	}
	db := filepath.Join(t.TempDir(), "cli-history.db")
	rec := seedCLILifecycleMemory(t, db, "The CLI verifies lifecycle history.")
	st, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ArchiveMemory(context.Background(), "cli-proj", rec.ID, store.ArchiveAuthorityAgentVerified, store.ArchiveReasonWrong, "", "agent", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RestoreMemory(context.Background(), "cli-proj", rec.ID, "user", store.ArchiveAuthorityUserRequest, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	history, err := st.VerifyMemoryHistory(context.Background(), "cli-proj", rec.ID)
	if err != nil || !history.Verified {
		t.Fatalf("fixture history: %+v err=%v", history, err)
	}
	_ = st.Close()
	ui := render.New(os.Stdout, true)
	out := captureStdout(t, func() {
		runMemory(ui, db, "", "", "72h", "", "", "tester", 50, false, true, false, memoryLifecycleOptions{verify: true}, []string{"history", rec.ID})
	})
	var payload struct {
		OK      bool `json:"ok"`
		History struct {
			Verified bool  `json:"verified"`
			Events   []any `json:"events"`
		} `json:"history"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("history JSON: %v\n%s", err, out)
	}
	if !payload.OK || !payload.History.Verified || len(payload.History.Events) != 2 {
		t.Fatalf("history payload: %+v", payload)
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestMemoryHistoryCLI")
	cmd.Env = append(os.Environ(),
		"SILA_MEMORY_HISTORY_TAMPER_SUB=1",
		"SILA_MEMORY_DB="+db,
		"SILA_MEMORY_ID="+rec.ID,
	)
	var tamperOut strings.Builder
	cmd.Stdout = &tamperOut
	cmd.Stderr = &tamperOut
	err = cmd.Run()
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected a non-zero exit, got %v (%s)", err, tamperOut.String())
	}
	if exitErr.ExitCode() != 1 {
		t.Fatalf("exit code = %d, want 1 (%s)", exitErr.ExitCode(), tamperOut.String())
	}
	if !strings.Contains(tamperOut.String(), store.MemoryCodeChainBroken) {
		t.Fatalf("missing chain-broken code: %s", tamperOut.String())
	}
}

func TestMemoryHardDeleteRefused(t *testing.T) {
	if os.Getenv("SILA_MEMORY_HARD_DELETE_SUB") == "1" {
		db := os.Getenv("SILA_MEMORY_DB")
		id := os.Getenv("SILA_MEMORY_ID")
		runMemory(render.New(os.Stdout, true), db, "", "", "72h", "", "", "tester", 50, false, false, false, memoryLifecycleOptions{hardDelete: true}, []string{"archive", id})
		return
	}
	db := filepath.Join(t.TempDir(), "cli-hard-delete.db")
	rec := seedCLILifecycleMemory(t, db, "Hard delete is refused.")
	cmd := exec.Command(os.Args[0], "-test.run=TestMemoryHardDeleteRefused")
	cmd.Env = append(os.Environ(),
		"SILA_MEMORY_HARD_DELETE_SUB=1",
		"SILA_MEMORY_DB="+db,
		"SILA_MEMORY_ID="+rec.ID,
	)
	var out strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected a non-zero exit, got %v (%s)", err, out.String())
	}
	if exitErr.ExitCode() != 2 {
		t.Fatalf("exit code = %d, want 2 (%s)", exitErr.ExitCode(), out.String())
	}
	if !strings.Contains(out.String(), store.MemoryCodeHardDeleteRefused) {
		t.Fatalf("missing refusal code: %s", out.String())
	}
}

func TestParseMemoryFeedbackArgs(t *testing.T) {
	positionals, opts, err := parseMemoryFeedbackArgs([]string{"mem_1", "--signal", "helped", "--retrieval=ret_1", "--unbound", "--note", "shaped the work"})
	if err != nil {
		t.Fatal(err)
	}
	if len(positionals) != 1 || positionals[0] != "mem_1" {
		t.Fatalf("positionals = %v", positionals)
	}
	if opts.signal != "helped" || opts.retrieval != "ret_1" || !opts.unbound || opts.note != "shaped the work" {
		t.Fatalf("opts = %+v", opts)
	}
	if _, _, err := parseMemoryFeedbackArgs([]string{"--bogus"}); err == nil {
		t.Fatal("unknown flag was accepted")
	}
}

func TestMemoryFeedbackCLI(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "cli-feedback.db")
	file := filepath.Join(dir, "memory.json")
	if err := os.WriteFile(file, []byte(memoryCLIFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	ui := render.New(os.Stdout, true)
	addOut := captureStdout(t, func() {
		runMemory(ui, db, "cli-proj", "", "72h", file, "", "tester", 50, false, true, false, memoryLifecycleOptions{}, []string{"add"})
	})
	var added struct {
		Memory struct {
			ID string `json:"id"`
		} `json:"memory"`
	}
	if err := json.Unmarshal([]byte(addOut), &added); err != nil || added.Memory.ID == "" {
		t.Fatalf("add JSON: %v\n%s", err, addOut)
	}

	st, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	retrieval, err := st.RecordRetrieval(context.Background(), "cli-proj", "tester", "", "cli feedback query",
		[]store.MemoryRetrievalItem{{MemoryID: added.Memory.ID, Rank: 1, Kind: "root_cause", Score: 500}})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	boundOut := captureStdout(t, func() {
		runMemory(ui, db, "cli-proj", "", "", "", "", "tester", 50, false, false, false, memoryLifecycleOptions{},
			[]string{"feedback", added.Memory.ID, "--signal=helped", "--retrieval=" + retrieval})
	})
	if !strings.Contains(boundOut, "helped") || !strings.Contains(boundOut, "bound") {
		t.Fatalf("bound feedback output: %s", boundOut)
	}
	if !strings.Contains(boundOut, "+40.0") {
		t.Fatalf("bound weight missing: %s", boundOut)
	}

	unboundOut := captureStdout(t, func() {
		runMemory(ui, db, "cli-proj", "", "", "", "", "tester", 50, false, false, false, memoryLifecycleOptions{},
			[]string{"feedback", added.Memory.ID, "--signal=less_important", "--unbound"})
	})
	if !strings.Contains(unboundOut, "unbound") || !strings.Contains(unboundOut, "-10.0") {
		t.Fatalf("unbound feedback output: %s", unboundOut)
	}

	listOut := captureStdout(t, func() {
		runMemory(ui, db, "cli-proj", "", "", "", "", "tester", 50, false, true, false, memoryLifecycleOptions{},
			[]string{"feedback", "list", "--memory=" + added.Memory.ID})
	})
	var summary store.MemoryFeedbackSummary
	if err := json.Unmarshal([]byte(listOut), &summary); err != nil {
		t.Fatalf("list JSON: %v\n%s", err, listOut)
	}
	if summary.Total != 2 || summary.Accepted != 2 || summary.Bound != 1 || summary.Unbound != 1 {
		t.Fatalf("feedback summary = %+v", summary)
	}
	if summary.Counts["helped"] != 1 || summary.Counts["less_important"] != 1 {
		t.Fatalf("signal counts = %+v", summary.Counts)
	}

	textList := captureStdout(t, func() {
		runMemory(ui, db, "cli-proj", "", "", "", "", "tester", 50, false, false, false, memoryLifecycleOptions{},
			[]string{"feedback", "list"})
	})
	if !strings.Contains(textList, "helped") || !strings.Contains(textList, "top adjusted") {
		t.Fatalf("text list output: %s", textList)
	}
}

func TestMemoryFeedbackCLIValidation(t *testing.T) {
	if os.Getenv("MEMORY_FEEDBACK_TEST_BAD_SIGNAL") == "1" {
		runMemory(render.New(os.Stdout, true), os.Getenv("MEMORY_FEEDBACK_TEST_DB"), "cli-proj", "", "", "", "", "tester", 50, false, false, false, memoryLifecycleOptions{},
			[]string{"feedback", "mem_missing", "--signal=useful"})
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestMemoryFeedbackCLIValidation")
	cmd.Env = append(os.Environ(),
		"MEMORY_FEEDBACK_TEST_BAD_SIGNAL=1",
		"MEMORY_FEEDBACK_TEST_DB="+filepath.Join(t.TempDir(), "bad-signal.db"))
	if err := cmd.Run(); err == nil {
		t.Fatal("expected exit 2 for an unknown signal")
	} else if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() != 2 {
		t.Fatalf("exit = %v, want 2", err)
	}
}

func TestMemoryUnmergeRollback(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "unmerge.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	proj, err := st.EnsureProject(tempDir)
	if err != nil {
		t.Fatal(err)
	}

	original := store.Fact{
		ID: "fact_orig", ProjectID: proj.ID, Claim: "Enable read-only mode in worker",
		Kind: "code_claim", Confidence: "grounded", Corroboration: "grounded",
		Verified: true, FileRefsExist: true, RepoFiles: []string{"worker.go"},
		SessionRef: "s", FirstSeen: "2026-11-01T00:00:00Z", PromotedAt: "2026-11-01T00:00:00Z",
		RefsCheckedAt: "2026-11-01T00:00:00Z", ReviewStatus: store.ReviewApproved,
		ScopeKey: "scope-worker",
	}
	if err := st.UpsertFact(original); err != nil {
		t.Fatal(err)
	}
	merged := store.Fact{
		ID: "fact_merged", ProjectID: proj.ID, Claim: "Disable read-only mode in worker",
		Kind: "code_claim", Confidence: "grounded", Corroboration: "grounded",
		Verified: true, FileRefsExist: true, RepoFiles: []string{"worker.go"},
		SessionRef: "s", FirstSeen: "2026-11-02T00:00:00Z", PromotedAt: "2026-11-02T00:00:00Z",
		RefsCheckedAt: "2026-11-02T00:00:00Z", ReviewStatus: store.ReviewApproved,
		ScopeKey: "scope-worker", SupersedesID: "fact_orig",
	}
	if err := st.UpsertFact(merged); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkFactStale("fact_orig"); err != nil {
		t.Fatal(err)
	}

	ui := render.New(os.Stdout, true)
	_ = captureStdout(t, func() {
		memoryUnmerge(ui, dbPath, proj.ID, "fact_merged", "agent", true)
	})
	restored, err := st.FactByID("fact_orig")
	if err != nil || restored == nil {
		t.Fatalf("read restored fact: %v", err)
	}
	if restored.IsStaleFlag {
		t.Fatal("original fact is still stale after unmerge")
	}
	archived, err := st.FactByID("fact_merged")
	if err != nil || archived == nil {
		t.Fatalf("read merged fact: %v", err)
	}
	if archived.ReviewStatus != "archived" || !archived.IsStaleFlag {
		t.Fatalf("merged fact state = %q stale=%v, want archived and stale", archived.ReviewStatus, archived.IsStaleFlag)
	}
	if archived.SupersedesID != "" {
		t.Fatalf("merged fact still supersedes %q after unmerge", archived.SupersedesID)
	}

	original2 := store.Fact{
		ID: "fact_orig2", ProjectID: proj.ID, Claim: "Enable beta flag",
		Kind: "code_claim", Confidence: "grounded", Corroboration: "grounded",
		Verified: true, FileRefsExist: true, RepoFiles: []string{"beta.go"},
		SessionRef: "s", FirstSeen: "2026-11-01T00:00:00Z", PromotedAt: "2026-11-01T00:00:00Z",
		RefsCheckedAt: "2026-11-01T00:00:00Z", ReviewStatus: store.ReviewApproved,
		ScopeKey: "scope-beta",
	}
	if err := st.UpsertFact(original2); err != nil {
		t.Fatal(err)
	}
	merged2 := store.Fact{
		ID: "fact_merged2", ProjectID: proj.ID, Claim: "Disable beta flag",
		Kind: "code_claim", Confidence: "grounded", Corroboration: "grounded",
		Verified: true, FileRefsExist: true, RepoFiles: []string{"beta.go"},
		SessionRef: "s", FirstSeen: "2026-11-02T00:00:00Z", PromotedAt: "2026-11-02T00:00:00Z",
		RefsCheckedAt: "2026-11-03T00:00:00Z", ReviewStatus: store.ReviewApproved,
		ScopeKey: "scope-beta", SupersedesID: "fact_orig2",
	}
	if err := st.UpsertFact(merged2); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkFactStale("fact_orig2"); err != nil {
		t.Fatal(err)
	}
	if _, err := dream.UnmergeConsolidatedFact(context.Background(), st, "fact_merged2", time.Now().UTC()); err == nil {
		t.Fatal("expected unmerge to refuse a post-merge modification")
	} else if !strings.Contains(err.Error(), "modified after the merge") {
		t.Fatalf("unmerge error = %q, want modified-after-merge refusal", err.Error())
	}
	stillStale, err := st.FactByID("fact_orig2")
	if err != nil || stillStale == nil {
		t.Fatalf("read original2: %v", err)
	}
	if !stillStale.IsStaleFlag {
		t.Fatal("refused unmerge mutated the original fact")
	}
}
