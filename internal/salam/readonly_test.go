package salam

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/store"
)

func TestBuildBrief_WithoutStore(t *testing.T) {
	tmp := t.TempDir()
	repoDir := filepath.Join(tmp, "repo-no-store")
	if err := os.MkdirAll(repoDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create a ready goal on disk
	indep := true
	if _, err := goals.CreateGoal(goals.CreateGoalParams{
		RootDir:     repoDir,
		Slug:        "offline-goal",
		Title:       "Offline Goal",
		Description: "A goal ready without knowledge store",
		Independent: &indep,
	}); err != nil {
		t.Fatal(err)
	}

	brief, err := BuildBrief(nil, repoDir, "")
	if err != nil {
		t.Fatalf("BuildBrief failed with nil store: %v", err)
	}

	if !brief.MemoryUnavailable {
		t.Errorf("expected MemoryUnavailable=true when store is nil")
	}
	if brief.MemoryStatus != "unavailable" {
		t.Errorf("expected MemoryStatus='unavailable', got %q", brief.MemoryStatus)
	}
	if brief.LastHandoff != nil {
		t.Errorf("expected LastHandoff=nil when store is nil, got %+v", brief.LastHandoff)
	}
	if len(brief.Loop) != 7 {
		t.Errorf("expected 7 loop steps, got %d", len(brief.Loop))
	}
	if len(brief.Ready) != 1 || brief.Ready[0].Slug != "offline-goal" {
		t.Errorf("expected 1 ready goal 'offline-goal', got %+v", brief.Ready)
	}

	md := brief.Markdown
	if !strings.Contains(md, "Memory unavailable: no knowledge store found") {
		t.Errorf("expected markdown to clearly label memory unavailable: %s", md)
	}
	if !strings.Contains(md, "offline-goal") {
		t.Errorf("expected markdown to contain ready goal: %s", md)
	}
}

func TestBuildBrief_WithReadOnlyStore(t *testing.T) {
	tmp := t.TempDir()
	storeDir := filepath.Join(tmp, "store-dir")
	dbPath := filepath.Join(storeDir, "test.db")
	repoDir := filepath.Join(tmp, "repo-ro-store")
	if err := os.MkdirAll(repoDir, 0755); err != nil {
		t.Fatal(err)
	}

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	p, err := st.EnsureProject(repoDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.InsertHandoff(store.Handoff{
		ProjectID: p.ID,
		Agent:     "antigravity",
		Summary:   "Read-only orientation verified",
		CreatedAt: "2026-09-25T01:00:00Z",
		Status:    "open",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
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

	roSt, exists, err := store.OpenInspection(dbPath)
	if err != nil {
		t.Fatalf("OpenInspection failed: %v", err)
	}
	if !exists || roSt == nil {
		t.Fatalf("expected store to exist")
	}
	defer roSt.Close()

	brief, err := BuildBrief(roSt, repoDir, p.ID)
	if err != nil {
		t.Fatalf("BuildBrief failed with read-only store: %v", err)
	}

	if brief.MemoryUnavailable {
		t.Errorf("expected MemoryUnavailable=false when readable store is provided")
	}
	if brief.LastHandoff == nil || brief.LastHandoff.Summary != "Read-only orientation verified" {
		t.Errorf("expected LastHandoff to match inserted handoff, got %+v", brief.LastHandoff)
	}
	if strings.Contains(brief.Markdown, "Memory unavailable") {
		t.Errorf("markdown should not label memory unavailable when store is present: %s", brief.Markdown)
	}
	if !strings.Contains(brief.Markdown, "Read-only orientation verified") {
		t.Errorf("markdown should contain handoff summary: %s", brief.Markdown)
	}
}
