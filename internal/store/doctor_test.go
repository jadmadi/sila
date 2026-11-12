package store

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoreDoctor(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "test_doctor.db")

	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer st.Close()

	// Populate some data
	st.EnsureProject("/tmp/nonexistent-project-12345")
	_, _, _ = st.WriteRun("test-run", "claude-code", "1", []LoadMessage{
		{ProjectID: "nonexistent-project-12345", ContentHash: "hash-doc-1", TS: "2026-08-20T10:00:00Z", Role: "user", Content: "hello doctor test", Tool: "claude-code"},
	})
	_ = st.InsertDoc(Doc{
		ID:        "doc-test-1",
		ProjectID: "nonexistent-project-12345",
		Title:     "Doctor Doc",
		URL:       "https://example.com/doc",
		Content:   "Document content for doctor test",
		Format:    "markdown",
		CreatedAt: "2026-08-20T10:00:00Z",
	})

	rep, err := st.Doctor(false)
	if err != nil {
		t.Fatalf("Doctor audit failed: %v", err)
	}

	if rep.OverallStatus == StatusFail {
		t.Fatalf("expected PASS or WARN, got FAIL: %+v", rep)
	}

	if len(rep.Checks) < 6 {
		t.Fatalf("expected at least 6 diagnostic checks, got %d", len(rep.Checks))
	}

	// Verify repair mode
	repRepair, err := st.Doctor(true)
	if err != nil {
		t.Fatalf("Doctor repair failed: %v", err)
	}

	if !repRepair.RepairExecuted {
		t.Fatalf("expected RepairExecuted to be true")
	}
	if len(repRepair.RepairedItems) == 0 {
		t.Fatalf("expected at least 1 repaired item")
	}
}

// TestDoctorRepairPrunesDeletedEmptyProjects covers the repair the doctor WARN
// advertises for dead registrations. The prune query used to name a "memos"
// table that does not exist, which aborted the whole statement and left every
// dead registration in place while the run still reported success.
func TestDoctorRepairPrunesDeletedEmptyProjects(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "prune.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// Registered path is gone and nothing was ever recorded under it.
	if _, err := st.EnsureProject(filepath.Join(t.TempDir(), "gone-empty")); err != nil {
		t.Fatal(err)
	}
	// Registered path is gone but the registration holds history: it stays.
	histPath := filepath.Join(t.TempDir(), "gone-with-history")
	hist, err := st.EnsureProject(histPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.InsertMemo(hist.ID, "kept memo", "agent", histPath, nil); err != nil {
		t.Fatal(err)
	}

	if _, err := st.Doctor(true); err != nil {
		t.Fatalf("Doctor repair failed: %v", err)
	}

	var count int
	if err := st.DB.QueryRow(`SELECT COUNT(*) FROM projects WHERE id = 'gone-empty'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("deleted empty registration was not pruned")
	}
	if err := st.DB.QueryRow(`SELECT COUNT(*) FROM projects WHERE id = ?`, hist.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("registration holding a memo fact was pruned; history must survive")
	}
}

func TestStoreDoctor_FactTamperIncidentPreserved(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "doctor_tamper.db")

	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer st.Close()

	if err := st.SaveFact(Fact{ID: "f_doc_1", ProjectID: "p", Claim: "Authentic claim 1"}); err != nil {
		t.Fatal(err)
	}

	// Tamper directly in SQLite
	if _, err := st.DB.Exec(`UPDATE facts SET claim = 'Tampered malicious claim' WHERE id = 'f_doc_1'`); err != nil {
		t.Fatal(err)
	}

	// Doctor(false) detects failure
	rep, err := st.Doctor(false)
	if err != nil {
		t.Fatalf("Doctor audit failed: %v", err)
	}
	if rep.OverallStatus != StatusFail {
		t.Fatalf("expected StatusFail on tampered ledger, got %s", rep.OverallStatus)
	}

	// Doctor(true) must NOT erase the integrity incident
	repRepair, err := st.Doctor(true)
	if err != nil {
		t.Fatalf("Doctor repair failed: %v", err)
	}
	if !repRepair.RepairExecuted {
		t.Fatalf("expected RepairExecuted to be true")
	}

	// Subsequent Doctor(false) still reports StatusFail and preserves evidence
	repAfter, err := st.Doctor(false)
	if err != nil {
		t.Fatalf("Doctor audit after repair failed: %v", err)
	}
	if repAfter.OverallStatus != StatusFail {
		t.Fatalf("expected StatusFail after routine repair on tampered fact, got %s", repAfter.OverallStatus)
	}

	// Inspectable incident evidence is preserved in fact_incidents
	incidents, err := st.FactIncidents()
	if err != nil {
		t.Fatal(err)
	}
	if len(incidents) == 0 {
		t.Fatalf("expected preserved incident records, got none")
	}
}

func TestCheckMCPClientCache(t *testing.T) {
	cases := []struct {
		name      string
		files     int
		want      CheckStatus
		wantInMsg string
	}{
		{name: "stale cache", files: 6, want: StatusWarn, wantInMsg: fmt.Sprintf("has 6 tool(s); expected at least %d", DefaultContinuityToolCount)},
		{name: "complete cache", files: DefaultContinuityToolCount, want: StatusPass, wantInMsg: fmt.Sprintf("is current (%d tool schemas cached)", DefaultContinuityToolCount)},
		{name: "absent cache", files: 0, want: StatusPass, wantInMsg: "no local Antigravity schema cache directory"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			if tc.files > 0 {
				dir := filepath.Join(home, ".gemini", "antigravity", "mcp", "sila")
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				for i := 0; i < tc.files; i++ {
					name := filepath.Join(dir, fmt.Sprintf("tool_%02d.json", i))
					if err := os.WriteFile(name, []byte(`{}`), 0o644); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.WriteFile(filepath.Join(dir, "instructions.md"), []byte("ignore"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			res := CheckMCPClientCache(home, DefaultContinuityToolCount)
			if res.Category != "MCP" || res.Name != "MCP Continuity Surface & Client Cache" {
				t.Fatalf("unexpected identity: %+v", res)
			}
			if res.Status != tc.want {
				t.Fatalf("status = %q, want %q: %+v", res.Status, tc.want, res)
			}
			if !strings.Contains(res.Message, tc.wantInMsg) {
				t.Fatalf("message %q does not contain %q", res.Message, tc.wantInMsg)
			}
			if tc.want == StatusWarn && !strings.Contains(res.Repair, "Restart your AI harness") {
				t.Fatalf("missing actionable repair: %q", res.Repair)
			}
		})
	}
}

func TestDoctorIncludesMCPClientCacheCheck(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cacheDir := filepath.Join(home, ".gemini", "antigravity", "mcp", "sila")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		if err := os.WriteFile(filepath.Join(cacheDir, fmt.Sprintf("t%d.json", i)), []byte(`{}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	st, err := Open(filepath.Join(home, "doctor_mcp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	report, err := st.Doctor(false)
	if err != nil {
		t.Fatal(err)
	}
	for _, check := range report.Checks {
		if check.Name == "MCP Continuity Surface & Client Cache" {
			if check.Status != StatusWarn {
				t.Fatalf("doctor MCP cache status = %q, want warn: %+v", check.Status, check)
			}
			return
		}
	}
	t.Fatal("doctor report is missing the MCP continuity surface check")
}

func TestDoctorRepairCanonicalizesSubdirectoryProjectWithGoalEvents(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "subdir_canonical.db")
	st, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	repoRoot := filepath.Join(tmp, "repo")
	subDir := filepath.Join(repoRoot, "goals", "my-goal")
	if err := os.MkdirAll(filepath.Join(repoRoot, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Register the parent repository and directly insert a stale subdirectory
	// registration holding a foreign-key child row on projects(id).
	if _, err := st.EnsureProject(repoRoot); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`CREATE TABLE IF NOT EXISTS subdir_events (id INTEGER PRIMARY KEY AUTOINCREMENT, project_id TEXT NOT NULL REFERENCES projects(id), slug TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`INSERT INTO projects (path, id, created_at) VALUES (?, 'my-goal', '2026-11-01T00:00:00Z')`, subDir); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`INSERT INTO subdir_events (project_id, slug) VALUES ('my-goal', 'my-goal')`); err != nil {
		t.Fatal(err)
	}

	if _, err := st.Doctor(true); err != nil {
		t.Fatalf("Doctor repair failed: %v", err)
	}

	var subdirCount int
	if err := st.DB.QueryRow(`SELECT COUNT(*) FROM projects WHERE id = 'my-goal'`).Scan(&subdirCount); err != nil {
		t.Fatal(err)
	}
	if subdirCount != 0 {
		t.Fatalf("expected subdirectory registration 'my-goal' to be merged and deleted, count=%d", subdirCount)
	}

	var eventCount int
	if err := st.DB.QueryRow(`SELECT COUNT(*) FROM subdir_events WHERE project_id = 'repo' AND slug = 'my-goal'`).Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	if eventCount != 1 {
		t.Fatalf("expected subdir_events to be migrated to parent 'repo', count=%d", eventCount)
	}
}

func TestIsRepoOrWorkspaceDirExcludesHomeConfigAndDownloadDirs(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)

	nonRepoDirs := []string{
		filepath.Join(fakeHome, ".agents"),
		filepath.Join(fakeHome, ".gemini"),
		filepath.Join(fakeHome, ".config", "opencode"),
		filepath.Join(fakeHome, ".local", "share", "sila"),
		filepath.Join(fakeHome, ".var", "app", "ai.opencode.opencode", "config", "opencode"),
		filepath.Join(fakeHome, "Downloads", "Debs", "Antigravity-x64"),
		filepath.Join(fakeHome, "tmp", "empty"),
		filepath.Join(fakeHome, "temp-agy", "system-crash"),
	}
	for _, d := range nonRepoDirs {
		if err := os.MkdirAll(filepath.Join(d, "goals"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "AGENTS.md"), []byte("# rules\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if IsRepoOrWorkspaceDir(d) {
			t.Fatalf("expected %s without .git to be excluded by IsRepoOrWorkspaceDir", d)
		}
	}

	gitPlugin := filepath.Join(fakeHome, ".gemini", "config", "plugins", "agent-skills")
	if err := os.MkdirAll(filepath.Join(gitPlugin, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !IsRepoOrWorkspaceDir(gitPlugin) {
		t.Fatalf("expected %s with .git to be accepted by IsRepoOrWorkspaceDir", gitPlugin)
	}
}
