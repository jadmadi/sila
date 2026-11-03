package agents

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// mockStateStore implements StateStore in memory for tests.
type mockStateStore struct {
	state map[string]string
}

func newMockStateStore() *mockStateStore {
	return &mockStateStore{state: make(map[string]string)}
}

func (m *mockStateStore) GetState(key string) (string, error) {
	return m.state[key], nil
}

func (m *mockStateStore) SetState(key, value string) error {
	m.state[key] = value
	return nil
}

func TestSyncReceiptStorage(t *testing.T) {
	store := newMockStateStore()

	// Initial load should be nil
	rec, err := LoadLastSyncReceipt(store)
	if err != nil {
		t.Fatalf("unexpected error loading empty receipt: %v", err)
	}
	if rec != nil {
		t.Fatalf("expected nil receipt on empty store, got %+v", rec)
	}

	// Create a receipt
	now := time.Now().UTC().Format(time.RFC3339)
	sample := &SyncReceipt{
		Timestamp:     now,
		HubCommit:     "abc1234",
		Scope:         "all",
		Initiator:     "@antigravity",
		TotalProjects: 2,
		Succeeded:     2,
		Failed:        0,
		FilesWritten:  4,
		Symlinks:      2,
		Projects: []ProjectReceipt{
			{
				ProjectID:    "proj1",
				Path:         "/path/to/proj1",
				FilesWritten: []string{"AGENTS.md", ".cursorrules"},
				Symlinks:     []string{"CLAUDE.md"},
				Success:      true,
			},
			{
				ProjectID:    "proj2",
				Path:         "/path/to/proj2",
				FilesWritten: []string{"AGENTS.md"},
				Success:      true,
			},
		},
	}

	if err := SaveSyncReceipt(store, sample); err != nil {
		t.Fatalf("SaveSyncReceipt failed: %v", err)
	}

	// Load back and verify
	loaded, err := LoadLastSyncReceipt(store)
	if err != nil {
		t.Fatalf("LoadLastSyncReceipt failed: %v", err)
	}
	if loaded == nil {
		t.Fatal("expected non-nil receipt")
	}
	if loaded.HubCommit != "abc1234" {
		t.Errorf("expected HubCommit abc1234, got %s", loaded.HubCommit)
	}
	if loaded.Initiator != "@antigravity" {
		t.Errorf("expected Initiator @antigravity, got %s", loaded.Initiator)
	}
	if loaded.TotalProjects != 2 {
		t.Errorf("expected 2 projects, got %d", loaded.TotalProjects)
	}
	if len(loaded.Projects) != 2 {
		t.Errorf("expected 2 project receipts, got %d", len(loaded.Projects))
	}

	// Verify per-project load
	p1, err := LoadProjectSyncReceipt(store, "proj1")
	if err != nil {
		t.Fatalf("LoadProjectSyncReceipt(proj1) failed: %v", err)
	}
	if p1 == nil || !p1.Success {
		t.Errorf("expected successful p1 receipt, got %+v", p1)
	}
	if len(p1.FilesWritten) != 2 {
		t.Errorf("expected 2 files written for proj1, got %d", len(p1.FilesWritten))
	}
}

func TestNewSyncReceiptFromMulti(t *testing.T) {
	multi := &MultiSyncResult{
		Total:     2,
		Succeeded: 1,
		Failed:    1,
		Projects: []ProjectSyncResult{
			{
				ProjectID: "alpha",
				Path:      "/repos/alpha",
				Result: &SyncResult{
					RepoRoot:     "/repos/alpha",
					FilesWritten: []string{"AGENTS.md"},
					Symlinks:     []string{"CLAUDE.md"},
				},
			},
			{
				ProjectID: "beta",
				Path:      "/repos/beta",
				Error:     "permission denied",
			},
		},
	}

	rec := NewSyncReceiptFromMulti(multi, "def5678", "test-agent")
	if rec == nil {
		t.Fatal("expected non-nil receipt")
	}
	if rec.HubCommit != "def5678" {
		t.Errorf("expected hub commit def5678, got %s", rec.HubCommit)
	}
	if rec.Initiator != "test-agent" {
		t.Errorf("expected initiator test-agent, got %s", rec.Initiator)
	}
	if rec.TotalProjects != 2 || rec.Succeeded != 1 || rec.Failed != 1 {
		t.Errorf("unexpected counts: total=%d, succ=%d, fail=%d", rec.TotalProjects, rec.Succeeded, rec.Failed)
	}
	if rec.FilesWritten != 1 || rec.Symlinks != 1 {
		t.Errorf("unexpected writes: files=%d, symlinks=%d", rec.FilesWritten, rec.Symlinks)
	}
}

func TestNewSyncReceiptFromSingle(t *testing.T) {
	res := &SyncResult{
		RepoRoot:     "/repos/single",
		FilesWritten: []string{"AGENTS.md"},
		Symlinks:     []string{"CLAUDE.md"},
	}

	rec := NewSyncReceiptFromSingle(res, "single", "commit123", "user1")
	if rec == nil {
		t.Fatal("expected non-nil receipt")
	}
	if rec.Scope != "single" {
		t.Errorf("expected scope single, got %s", rec.Scope)
	}
	if rec.Succeeded != 1 || rec.Failed != 0 {
		t.Errorf("expected succ=1 fail=0, got succ=%d fail=%d", rec.Succeeded, rec.Failed)
	}
	if len(rec.Projects) != 1 || rec.Projects[0].ProjectID != "single" {
		t.Errorf("unexpected projects in single receipt: %+v", rec.Projects)
	}
}

func TestFormatRelativeAge(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		timestamp string
		expected  string
	}{
		{now.Format(time.RFC3339), "just now"},
		{now.Add(-30 * time.Second).Format(time.RFC3339), "just now"},
		{now.Add(-10 * time.Minute).Format(time.RFC3339), "10m ago"},
		{now.Add(-3 * time.Hour).Format(time.RFC3339), "3h ago"},
		{now.Add(-48 * time.Hour).Format(time.RFC3339), "2d ago"},
		{"invalid", "unknown"},
	}

	for _, tc := range cases {
		got := FormatRelativeAge(tc.timestamp, now)
		if got != tc.expected {
			t.Errorf("FormatRelativeAge(%q) = %q; want %q", tc.timestamp, got, tc.expected)
		}
	}
}

func TestResolveHubCommitFallback(t *testing.T) {
	tmpDir := t.TempDir()
	latestPath := filepath.Join(tmpDir, "AGENTS.latest.md")
	if err := os.WriteFile(latestPath, []byte("universal rules content"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	commit := ResolveHubCommit(tmpDir)
	if !strings.HasPrefix(commit, "sha256:") {
		t.Errorf("expected sha256 prefix for non-git directory, got %q", commit)
	}

	// Empty dir fallback
	emptyCommit := ResolveHubCommit("")
	if emptyCommit != "embedded" {
		t.Errorf("expected 'embedded' for empty dir, got %q", emptyCommit)
	}
}

func TestSyncReceiptManagedHashRoundTrip(t *testing.T) {
	store := newMockStateStore()
	hash := strings.Repeat("a", 16)
	res := &SyncResult{
		RepoRoot:        "/repos/managed",
		ManagedHash:     hash,
		ManagedRevision: ManagedRevision,
		HashSkips:       1,
		Plan: []PlannedWrite{
			{Path: "AGENTS.md", Action: "skip", HashMatch: true, ManagedHash: hash, ManagedRevision: ManagedRevision},
			{Path: "CLAUDE.md", Action: "skip", Detail: "symlink to AGENTS.md already in place"},
		},
	}

	rec := NewSyncReceiptFromSingle(res, "managed", "abc1234", "tester")
	if rec.ManagedHash != hash || rec.ManagedRevision != ManagedRevision {
		t.Fatalf("run-level managed fields = %q rev=%d", rec.ManagedHash, rec.ManagedRevision)
	}
	if len(rec.Projects) != 1 {
		t.Fatalf("projects = %d", len(rec.Projects))
	}
	pr := rec.Projects[0]
	if pr.ManagedHash != hash || pr.ManagedRevision != ManagedRevision || pr.HashSkips != 1 {
		t.Fatalf("project managed fields = %+v", pr)
	}
	if len(pr.SkippedFiles) != 1 || pr.SkippedFiles[0] != "AGENTS.md" {
		t.Fatalf("skipped files = %v", pr.SkippedFiles)
	}

	if err := SaveSyncReceipt(store, rec); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadLastSyncReceipt(store)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ManagedHash != hash || loaded.ManagedRevision != ManagedRevision {
		t.Fatalf("reloaded run-level fields = %q rev=%d", loaded.ManagedHash, loaded.ManagedRevision)
	}
	if len(loaded.Projects) != 1 || loaded.Projects[0].HashSkips != 1 || len(loaded.Projects[0].SkippedFiles) != 1 {
		t.Fatalf("reloaded project = %+v", loaded.Projects)
	}

	data, err := json.Marshal(loaded)
	if err != nil {
		t.Fatal(err)
	}
	var decoded SyncReceipt
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.ManagedHash != hash || decoded.ManagedRevision != ManagedRevision {
		t.Fatalf("json round-trip lost managed fields: %+v", decoded)
	}
}

func TestNewSyncReceiptFromMultiManagedFields(t *testing.T) {
	hash := strings.Repeat("b", 16)
	multi := &MultiSyncResult{
		Total:     2,
		Succeeded: 2,
		Projects: []ProjectSyncResult{
			{
				ProjectID: "alpha",
				Path:      "/repos/alpha",
				Result: &SyncResult{
					RepoRoot:        "/repos/alpha",
					ManagedHash:     hash,
					ManagedRevision: ManagedRevision,
					HashSkips:       1,
					Plan:            []PlannedWrite{{Path: "AGENTS.md", Action: "skip", HashMatch: true}},
				},
			},
			{
				ProjectID: "beta",
				Path:      "/repos/beta",
				Result: &SyncResult{
					RepoRoot:        "/repos/beta",
					ManagedHash:     strings.Repeat("c", 16),
					ManagedRevision: ManagedRevision,
				},
			},
		},
	}

	rec := NewSyncReceiptFromMulti(multi, "def5678", "tester")
	if rec.ManagedHash != hash || rec.ManagedRevision != ManagedRevision {
		t.Fatalf("run-level managed fields = %q rev=%d", rec.ManagedHash, rec.ManagedRevision)
	}
	if rec.Projects[0].ManagedHash != hash || rec.Projects[0].HashSkips != 1 {
		t.Fatalf("alpha project = %+v", rec.Projects[0])
	}
	if len(rec.Projects[0].SkippedFiles) != 1 || rec.Projects[0].SkippedFiles[0] != "AGENTS.md" {
		t.Fatalf("alpha skipped files = %v", rec.Projects[0].SkippedFiles)
	}
	if rec.Projects[1].ManagedHash != strings.Repeat("c", 16) {
		t.Fatalf("beta project = %+v", rec.Projects[1])
	}
}

func TestOldSyncReceiptLoadsWithoutManagedFields(t *testing.T) {
	store := newMockStateStore()
	store.state[LastSyncReceiptKey] = `{
		"timestamp": "2026-10-01T12:00:00Z",
		"hub_commit": "abc1234",
		"scope": "all",
		"initiator": "legacy",
		"total_projects": 1,
		"succeeded": 1,
		"failed": 0,
		"files_written": 1,
		"symlinks": 0,
		"projects": [
			{"project_id": "old", "path": "/repos/old", "files_written": ["AGENTS.md"], "success": true}
		]
	}`

	rec, err := LoadLastSyncReceipt(store)
	if err != nil {
		t.Fatalf("old receipt failed to load: %v", err)
	}
	if rec == nil || rec.HubCommit != "abc1234" {
		t.Fatalf("old receipt = %+v", rec)
	}
	if rec.ManagedHash != "" || rec.ManagedRevision != 0 {
		t.Fatalf("old receipt invented managed fields: %+v", rec)
	}
	if len(rec.Projects) != 1 || rec.Projects[0].HashSkips != 0 || len(rec.Projects[0].SkippedFiles) != 0 {
		t.Fatalf("old project receipt = %+v", rec.Projects)
	}

	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "managed_hash") || strings.Contains(string(data), "managed_revision") {
		t.Fatalf("empty managed fields must be omitted for old receipts: %s", data)
	}
}
