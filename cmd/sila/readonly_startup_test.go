package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/heal"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/salam"
	"github.com/jadmadi/sila/internal/store"
)

func TestSalam_ReadOnlyStore(t *testing.T) {
	tmp := t.TempDir()
	storeDir := filepath.Join(tmp, "store-dir")
	dbPath := filepath.Join(storeDir, "k.db")
	repo := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(repo, 0755); err != nil {
		t.Fatal(err)
	}

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	p, err := st.EnsureProject(repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.InsertHandoff(store.Handoff{
		ProjectID: p.ID,
		Agent:     "antigravity",
		Summary:   "Read-only startup verified",
		CreatedAt: "2026-09-25T01:00:00Z",
		Status:    "open",
	}); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()

	// Add a ready goal
	indep := true
	if _, err := goals.CreateGoal(goals.CreateGoalParams{
		RootDir:     repo,
		Slug:        "ro-goal",
		Title:       "RO Goal",
		Description: "A goal for read-only test",
		Independent: &indep,
	}); err != nil {
		t.Fatal(err)
	}

	// Make store dir and file read-only
	if err := os.Chmod(dbPath, 0444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(storeDir, 0555); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.Chmod(storeDir, 0755)
		_ = os.Chmod(dbPath, 0644)
	}()

	// 1. JSON output
	ui := render.New(os.Stdout, true)
	outJSON := captureStdout(t, func() {
		runSalam(ui, dbPath, repo, false, true, []string{"--full"})
	})

	var brief salam.SalamBrief
	if err := json.Unmarshal([]byte(outJSON), &brief); err != nil {
		t.Fatalf("runSalam output not JSON: %s: %v", outJSON, err)
	}
	if brief.MemoryUnavailable {
		t.Errorf("expected MemoryUnavailable=false for read-only store with handoff")
	}
	if brief.LastHandoff == nil || brief.LastHandoff.Summary != "Read-only startup verified" {
		t.Errorf("expected handoff summary to match, got %+v", brief.LastHandoff)
	}
	if brief.Project != p.ID {
		t.Errorf("expected project %q, got %q", p.ID, brief.Project)
	}

	// 2. Text output
	uiText := render.New(os.Stdout, false)
	outText := captureStdout(t, func() {
		runSalam(uiText, dbPath, repo, false, false, []string{"--full"})
	})
	if !strings.Contains(outText, "Live state:") {
		t.Errorf("text output missing live state: %s", outText)
	}
	if !strings.Contains(outText, "Last handoff: @antigravity") {
		t.Errorf("text output missing last handoff: %s", outText)
	}
	if !strings.Contains(outText, "Read-only startup verified") {
		t.Errorf("text output missing handoff summary: %s", outText)
	}
}

func TestSalam_AbsentStore(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "absent", "k.db")
	repo := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(repo, 0755); err != nil {
		t.Fatal(err)
	}

	indep := true
	if _, err := goals.CreateGoal(goals.CreateGoalParams{
		RootDir:     repo,
		Slug:        "absent-store-goal",
		Title:       "Absent Store Goal",
		Description: "A goal for absent store test",
		Independent: &indep,
	}); err != nil {
		t.Fatal(err)
	}

	// 1. JSON output
	ui := render.New(os.Stdout, true)
	outJSON := captureStdout(t, func() {
		runSalam(ui, dbPath, repo, false, true, []string{"--full"})
	})

	var brief salam.SalamBrief
	if err := json.Unmarshal([]byte(outJSON), &brief); err != nil {
		t.Fatalf("runSalam output not JSON: %s: %v", outJSON, err)
	}
	if !brief.MemoryUnavailable {
		t.Errorf("expected MemoryUnavailable=true for absent store")
	}
	if brief.MemoryStatus != "unavailable" {
		t.Errorf("expected MemoryStatus='unavailable', got %q", brief.MemoryStatus)
	}
	if brief.LastHandoff != nil {
		t.Errorf("expected LastHandoff=nil, got %+v", brief.LastHandoff)
	}
	if len(brief.Ready) != 1 || brief.Ready[0].Slug != "absent-store-goal" {
		t.Errorf("expected 1 ready goal, got %+v", brief.Ready)
	}

	// 2. Text output
	uiText := render.New(os.Stdout, false)
	outText := captureStdout(t, func() {
		runSalam(uiText, dbPath, repo, false, false, []string{"--full"})
	})
	if !strings.Contains(outText, "Memory: unavailable") {
		t.Errorf("text output missing unavailable memory notice: %s", outText)
	}
	if !strings.Contains(outText, "absent-store-goal") {
		t.Errorf("text output missing ready goal: %s", outText)
	}

	// Verify no database file was created
	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		t.Errorf("absent store path should not exist on disk, got err=%v", err)
	}
}

func TestSalam_SubprocessFailures(t *testing.T) {
	mode := os.Getenv("TEST_SALAM_MODE")
	if mode != "" {
		dbPath := os.Getenv("TEST_SALAM_DB")
		repo := os.Getenv("TEST_SALAM_REPO")
		ui := render.New(os.Stdout, false)
		switch mode {
		case "future_schema":
			runSalam(ui, dbPath, repo, false, false, nil)
		case "corrupt_file":
			runSalam(ui, dbPath, repo, false, false, nil)
		case "explicit_assist_ro_fs":
			runSalam(ui, dbPath, repo, true, false, nil)
		case "heal_apply_ro_fs":
			runHeal(ui, dbPath, repo, false, false, nil)
		}
		return
	}

	tmp := t.TempDir()

	// 1. Future schema fails with actionable error and non-zero exit
	t.Run("FutureSchema", func(t *testing.T) {
		dbPath := filepath.Join(tmp, "future.db")
		st, err := store.Open(dbPath)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.DB.Exec("PRAGMA user_version = 999"); err != nil {
			st.Close()
			t.Fatal(err)
		}
		_ = st.Close()

		cmd := exec.Command(os.Args[0], "-test.run=TestSalam_SubprocessFailures")
		cmd.Env = append(os.Environ(),
			"TEST_SALAM_MODE=future_schema",
			"TEST_SALAM_DB="+dbPath,
			"TEST_SALAM_REPO="+tmp,
		)
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatalf("expected non-zero exit code for future schema, got output: %s", out)
		}
		if !strings.Contains(string(out), "newer than this sila build supports") {
			t.Fatalf("expected future schema error message, got: %s", out)
		}
	})

	// 2. Corrupt file fails with non-zero exit
	t.Run("CorruptFile", func(t *testing.T) {
		dbPath := filepath.Join(tmp, "corrupt.db")
		if err := os.WriteFile(dbPath, []byte("NOT A SQLITE DATABASE"), 0644); err != nil {
			t.Fatal(err)
		}

		cmd := exec.Command(os.Args[0], "-test.run=TestSalam_SubprocessFailures")
		cmd.Env = append(os.Environ(),
			"TEST_SALAM_MODE=corrupt_file",
			"TEST_SALAM_DB="+dbPath,
			"TEST_SALAM_REPO="+tmp,
		)
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatalf("expected non-zero exit code for corrupt file, got output: %s", out)
		}
		if !strings.Contains(string(out), "sila:") {
			t.Fatalf("expected sila error, got: %s", out)
		}
	})

	// 3. Explicit assistance enablement retains permission enforcement
	t.Run("ExplicitAssist_ReadOnlyRepo", func(t *testing.T) {
		roRepo := filepath.Join(tmp, "ro-repo")
		initSalamTestRepository(t, roRepo)
		dbPath := filepath.Join(tmp, "ok.db")
		st, _ := store.Open(dbPath)
		_, _ = st.EnsureProject(roRepo)
		_ = st.Close()

		// Make repo read-only so saving .sila/config.json fails
		if err := os.Chmod(roRepo, 0555); err != nil {
			t.Fatal(err)
		}
		defer os.Chmod(roRepo, 0755)

		cmd := exec.Command(os.Args[0], "-test.run=TestSalam_SubprocessFailures")
		cmd.Env = append(os.Environ(),
			"HOME="+tmp,
			"XDG_CONFIG_HOME="+tmp,
			"TEST_SALAM_MODE=explicit_assist_ro_fs",
			"TEST_SALAM_DB="+dbPath,
			"TEST_SALAM_REPO="+roRepo,
		)
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatalf("expected non-zero exit code when saving config on read-only fs, got output: %s", out)
		}
		if !strings.Contains(string(out), "failed to save project config") {
			t.Fatalf("expected failed to save project config message, got: %s", out)
		}
	})

	// 4. Heal mutation retains permission enforcement on read-only store
	t.Run("HealApply_ReadOnlyStore", func(t *testing.T) {
		storeDir := filepath.Join(tmp, "ro-store-dir")
		dbPath := filepath.Join(storeDir, "k.db")
		st, err := store.Open(dbPath)
		if err != nil {
			t.Fatal(err)
		}
		_ = st.Close()

		// Chmod dir 0555 and file 0444
		if err := os.Chmod(dbPath, 0444); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(storeDir, 0555); err != nil {
			t.Fatal(err)
		}
		defer func() {
			_ = os.Chmod(storeDir, 0755)
			_ = os.Chmod(dbPath, 0644)
		}()

		cmd := exec.Command(os.Args[0], "-test.run=TestSalam_SubprocessFailures")
		cmd.Env = append(os.Environ(),
			"TEST_SALAM_MODE=heal_apply_ro_fs",
			"TEST_SALAM_DB="+dbPath,
			"TEST_SALAM_REPO="+tmp,
		)
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatalf("expected non-zero exit code for heal mutation on read-only store, got output: %s", out)
		}
		if !strings.Contains(string(out), "sila:") {
			t.Fatalf("expected sila error output, got: %s", out)
		}
	})
}

func TestHealDryRun_ReadOnlyStore(t *testing.T) {
	tmp := t.TempDir()
	storeDir := filepath.Join(tmp, "store-dir")
	dbPath := filepath.Join(storeDir, "k.db")
	repo := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0755); err != nil {
		t.Fatal(err)
	}

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.EnsureProject(repo)
	if err != nil {
		t.Fatal(err)
	}
	_ = st.Close()

	// Chmod dir 0555 and file 0444
	if err := os.Chmod(dbPath, 0444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(storeDir, 0555); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.Chmod(storeDir, 0755)
		_ = os.Chmod(dbPath, 0644)
	}()

	// 1. Dry-run JSON output
	ui := render.New(os.Stdout, true)
	outJSON := captureStdout(t, func() {
		runHeal(ui, dbPath, repo, true, true, nil)
	})

	var plan heal.Plan
	if err := json.Unmarshal([]byte(outJSON), &plan); err != nil {
		t.Fatalf("runHeal dry-run output not JSON: %s: %v", outJSON, err)
	}

	// Verify automatic steps name affected files
	hasFiles := false
	for _, s := range plan.Steps {
		if s.Automatic && len(s.Files) > 0 {
			hasFiles = true
		}
	}
	if !hasFiles {
		t.Errorf("expected dry-run plan steps to name affected files")
	}

	// 2. Dry-run text output
	uiText := render.New(os.Stdout, false)
	outText := captureStdout(t, func() {
		runHeal(uiText, dbPath, repo, true, false, nil)
	})
	if !strings.Contains(outText, "preview only") {
		t.Errorf("text output missing preview only notice: %s", outText)
	}
	if !strings.Contains(outText, "Affected files:") {
		t.Errorf("text output missing Affected files line: %s", outText)
	}

	// Verify no files were actually modified or created (e.g. .cursor/mcp.json does not exist)
	if _, err := os.Stat(filepath.Join(repo, ".cursor", "mcp.json")); !os.IsNotExist(err) {
		t.Errorf("dry-run should not have applied fixes or created files")
	}
}

func TestSandboxResilientFallback_HandoffAndSync(t *testing.T) {
	tmp := t.TempDir()
	globalDir := filepath.Join(tmp, "global_share", "sila")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("XDG_DATA_HOME", filepath.Join(tmp, "global_share"))
	t.Setenv("SILA_DB", "")
	store.SetFallbackAllowed(true)
	store.ResetFallbackNoticeOnce()

	// Make global directory read-only
	if err := os.Chmod(globalDir, 0o555); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.Chmod(globalDir, 0o755)
	}()

	repo := filepath.Join(tmp, "workspace-repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repo)

	ui := render.New(os.Stdout, true)

	// 1. Run handoff in sandboxed mock environment
	runHandoff(ui, store.DefaultDBPath(), "", "main", "codex", "task 1", "decision 1", "", false, false, []string{"Session completed in sandbox"})

	// Verify .sila/sila.db was created in workspace
	wsDB := filepath.Join(repo, ".sila", "sila.db")
	if _, err := os.Stat(wsDB); os.IsNotExist(err) {
		t.Fatalf("expected workspace store %s to exist on disk", wsDB)
	}

	// Verify .gitignore was populated with .sila/
	gitIgnore, err := os.ReadFile(filepath.Join(repo, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore failed: %v", err)
	}
	if !strings.Contains(string(gitIgnore), ".sila/") {
		t.Fatalf("expected .gitignore to contain .sila/, got:\n%s", string(gitIgnore))
	}

	// Verify handoff was recorded in fallback store
	st, err := store.OpenRO(wsDB)
	if err != nil {
		t.Fatalf("open workspace store: %v", err)
	}
	defer st.Close()

	projID := store.ProjectID(repo)
	h, err := st.LatestHandoff(projID)
	if err != nil || h == nil {
		t.Fatalf("LatestHandoff failed: %v", err)
	}
	if h.Summary != "Session completed in sandbox" {
		t.Fatalf("expected summary 'Session completed in sandbox', got %q", h.Summary)
	}

	// 2. Run sync in sandboxed mock environment
	runSingleSync(ui, st, "", false, false, false, false, time.Time{})

	// 3. Restore global store write permissions (simulating host environment)
	_ = os.Chmod(globalDir, 0o755)
	primarySt, err := store.Open(store.DefaultDBPath())
	if err != nil {
		t.Fatalf("open primary store: %v", err)
	}
	defer primarySt.Close()

	// Running sync against primary global store should drain .sila/sila.db
	runSingleSync(ui, primarySt, "", false, false, false, false, time.Time{})

	// Verify handoff now exists in the primary global store
	hPrimary, err := primarySt.LatestHandoff(projID)
	if err != nil || hPrimary == nil {
		t.Fatalf("expected drained handoff in primary store, got %v", hPrimary)
	}
	if hPrimary.Summary != "Session completed in sandbox" {
		t.Fatalf("expected drained summary, got %q", hPrimary.Summary)
	}
}
