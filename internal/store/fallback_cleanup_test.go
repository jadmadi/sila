package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWorkspaceRootFromStorePath(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".sila", "sila.db")
	if got, ok := WorkspaceRootFromStorePath(path); !ok || filepath.Clean(got) != filepath.Clean(root) {
		t.Fatalf("root = %q, ok = %v", got, ok)
	}
	if _, ok := WorkspaceRootFromStorePath(filepath.Join(root, "sila.db")); ok {
		t.Fatal("global path reported as workspace")
	}
}

func TestFindWorkspaceRootRejectsNonRepo(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, "Downloads")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	if root, ok := findWorkspaceRoot(); ok {
		t.Fatalf("bare home subdirectory accepted as workspace root: %q", root)
	}
}

func TestDrainAndRemoveWorkspaceStore(t *testing.T) {
	tmp := t.TempDir()
	wsPath := filepath.Join(tmp, "workspace", ".sila", "sila.db")
	primaryPath := filepath.Join(tmp, "global", "sila.db")
	ws, err := Open(wsPath)
	if err != nil {
		t.Fatal(err)
	}
	project, err := ws.EnsureProject(filepath.Join(tmp, "workspace"))
	if err != nil {
		t.Fatal(err)
	}
	if err := ws.InsertHandoff(Handoff{ProjectID: project.ID, Agent: "test", Summary: "ws handoff", CreatedAt: "2026-11-12T00:00:00Z", Status: "pending"}); err != nil {
		t.Fatal(err)
	}
	_ = ws.Close()
	primary, err := Open(primaryPath)
	if err != nil {
		t.Fatal(err)
	}
	defer primary.Close()
	stats, removed, err := primary.DrainAndRemoveWorkspaceStore(wsPath)
	if err != nil || !removed || stats.Handoffs != 1 {
		t.Fatalf("stats = %+v, removed = %v, err = %v", stats, removed, err)
	}
	if _, err := os.Stat(wsPath); !os.IsNotExist(err) {
		t.Fatalf("workspace store still exists: %v", err)
	}

	lockedPath := filepath.Join(tmp, "locked", ".sila", "sila.db")
	locked, err := Open(lockedPath)
	if err != nil {
		t.Fatal(err)
	}
	_ = locked.Close()
	lock, held, err := lockWorkspaceStore(lockedPath)
	if err != nil || !held {
		t.Fatalf("lock = %v, held = %v, err = %v", lock, held, err)
	}
	_, removed, err = primary.DrainAndRemoveWorkspaceStore(lockedPath)
	if err != nil || removed {
		t.Fatalf("locked store removed = %v, err = %v", removed, err)
	}
	if _, err := os.Stat(lockedPath); err != nil {
		t.Fatalf("locked store should remain: %v", err)
	}
	lock.release()
}

func TestSweepWorkspaceRubbish(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cwd := filepath.Join(home, "Downloads", "proj")
	silaDir := filepath.Join(cwd, ".sila")
	if err := os.MkdirAll(filepath.Join(silaDir, "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	ws, err := Open(filepath.Join(silaDir, "sila.db"))
	if err != nil {
		t.Fatal(err)
	}
	project, err := ws.EnsureProject(cwd)
	if err != nil {
		t.Fatal(err)
	}
	if err := ws.InsertHandoff(Handoff{ProjectID: project.ID, Agent: "test", Summary: "stray", CreatedAt: "2026-11-12T00:00:00Z", Status: "pending"}); err != nil {
		t.Fatal(err)
	}
	_ = ws.Close()
	if err := os.WriteFile(filepath.Join(silaDir, "graph.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(silaDir, "state", "stale"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	primary, err := Open(filepath.Join(t.TempDir(), "primary.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer primary.Close()
	temp := t.TempDir()
	canary := filepath.Join(temp, "sila-prime-canary-old")
	if err := os.MkdirAll(canary, 0o755); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Hour)
	_ = os.Chtimes(canary, old, old)

	report, err := primary.SweepWorkspaceRubbish(RubbishSweepOptions{CWD: cwd, HomeDir: home, TempDir: temp, Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if report.RemovedWorkspaceDBs != 1 || report.CanaryDirsRemoved != 1 || report.NonRepoStateDirsRemoved != 1 {
		t.Fatalf("report = %+v", report)
	}
	if _, err := os.Stat(filepath.Join(silaDir, "sila.db")); !os.IsNotExist(err) {
		t.Fatalf("workspace store remains: %v", err)
	}
	if _, err := os.Stat(filepath.Join(silaDir, "graph.json")); err != nil {
		t.Fatalf("graph.json should be preserved: %v", err)
	}
	if _, err := os.Stat(filepath.Join(silaDir, "state")); !os.IsNotExist(err) {
		t.Fatalf("non-repo state remains: %v", err)
	}
	if _, err := os.Stat(canary); !os.IsNotExist(err) {
		t.Fatalf("canary remains: %v", err)
	}
}
