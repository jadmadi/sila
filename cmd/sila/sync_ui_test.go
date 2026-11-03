package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/ingest"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func TestDistillWithProgressCallback(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "k.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// Ensure a project exists and write a file on disk
	p, err := st.EnsureProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	pkgDir := filepath.Join(dir, "pkg")
	if err := os.MkdirAll(pkgDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "db.go"), []byte("package pkg\n"), 0644); err != nil {
		t.Fatal(err)
	}

	content := "We must always use pure-go modernc.org/sqlite in pkg/db.go and never cgo."
	_, _, err = st.WriteRun("", "codex", "", []store.LoadMessage{
		{
			ProjectID:   p.ID,
			ContentHash: store.ContentHash("assistant", content),
			TS:          time.Now().UTC().Format(time.RFC3339),
			Role:        "assistant",
			Content:     content,
			Tool:        "codex",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	var progressEvents []DistillProgress
	produced, verified, projCount, active, err := distillWithProgress(st, "", false, func(dp DistillProgress) {
		progressEvents = append(progressEvents, dp)
	})
	if err != nil {
		t.Fatalf("distillWithProgress error: %v", err)
	}
	if produced == 0 {
		t.Errorf("expected at least 1 fact distilled, got 0")
	}
	if verified == 0 {
		t.Errorf("expected verified facts > 0, got 0")
	}
	if projCount != 1 {
		t.Errorf("expected projCount 1, got %d", projCount)
	}
	if len(active) != 1 || active[0] != p.ID {
		t.Errorf("expected active project %s, got %+v", p.ID, active)
	}

	// Should have at least start and done events
	if len(progressEvents) < 2 {
		t.Fatalf("expected at least 2 progress events (start and done), got %d: %+v", len(progressEvents), progressEvents)
	}
	if progressEvents[0].Phase != "start" {
		t.Errorf("expected first event phase 'start', got %s", progressEvents[0].Phase)
	}
	last := progressEvents[len(progressEvents)-1]
	if last.Phase != "done" {
		t.Errorf("expected last event phase 'done', got %s", last.Phase)
	}
	if last.Facts != produced {
		t.Errorf("expected last event facts %d, got %d", produced, last.Facts)
	}
}

func TestSyncProgressVerboseOutput(t *testing.T) {
	ui := render.New(os.Stdout, true)

	out := captureWorktreeOutput(func() {
		prog := newSyncProgress(ui, false, true, false, true, "")
		prog.OnToolProgress(ingest.ToolProgress{
			Tool:       "devin",
			Index:      1,
			Total:      2,
			Phase:      "committed",
			Messages:   100,
			NewRecords: 5,
		})
		prog.OnToolProgress(ingest.ToolProgress{
			Tool:       "git",
			Index:      2,
			Total:      2,
			Phase:      "committed",
			Messages:   50,
			NewRecords: 0,
		})
		prog.StartDistill(1)
		prog.OnDistillProgress(DistillProgress{
			ProjectID: "proj-1",
			Index:     1,
			Total:     1,
			Phase:     "done",
			Facts:     3,
			Verified:  3,
		})
		prog.StartVacuum()
		prog.Finish()
	})

	if !strings.Contains(out, "Phase 1/3: Scanning & Ingesting AI sources") {
		t.Errorf("expected Phase 1 header, got %q", out)
	}
	if !strings.Contains(out, "devin:") || !strings.Contains(out, "5 new records") {
		t.Errorf("expected devin progress line, got %q", out)
	}
	if !strings.Contains(out, "git:") || !strings.Contains(out, "0 new records") {
		t.Errorf("expected git progress line, got %q", out)
	}
	if !strings.Contains(out, "Phase 2/3: Distilling facts") {
		t.Errorf("expected Phase 2 header, got %q", out)
	}
	if !strings.Contains(out, "proj-1: 3 facts (3 verified)") {
		t.Errorf("expected project distillation line, got %q", out)
	}
	if !strings.Contains(out, "Phase 3/3: Database maintenance & auto-vacuum") {
		t.Errorf("expected Phase 3 header, got %q", out)
	}
	if !strings.Contains(out, "Summary:") {
		t.Errorf("expected Summary header, got %q", out)
	}
}

func TestRunSingleSyncVerboseIntegration(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "k.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	p, err := st.EnsureProject(dir)
	if err != nil {
		t.Fatal(err)
	}

	ui := render.New(os.Stdout, true)
	out := captureWorktreeOutput(func() {
		runSingleSync(ui, st, p.ID, false, false, false, true, time.Time{})
	})

	if !strings.Contains(out, "sila · sync") {
		t.Errorf("expected sync banner, got %q", out)
	}
	if !strings.Contains(out, "Phase 1/3") {
		t.Errorf("expected Phase 1, got %q", out)
	}
	if !strings.Contains(out, "Phase 2/3") {
		t.Errorf("expected Phase 2, got %q", out)
	}
	if !strings.Contains(out, "Phase 3/3") {
		t.Errorf("expected Phase 3, got %q", out)
	}
	if !strings.Contains(out, "Summary:") {
		t.Errorf("expected Summary section, got %q", out)
	}
	if !strings.Contains(out, "nothing new to ingest") && !strings.Contains(out, "ingested") {
		t.Errorf("expected 'nothing new to ingest' or 'ingested', got %q", out)
	}
}

// TestDistillationSkipsUnchangedProjects is the acceptance check for the
// delta-sync cost: distillation is proportional to total history, so a sync
// with nothing new must not re-derive every project's facts.
func TestDistillationSkipsUnchangedProjects(t *testing.T) {
	tmp := t.TempDir()
	st, err := store.Open(filepath.Join(tmp, "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	p, err := st.EnsureProject(filepath.Join(tmp, "proj"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.WriteRun("", "devin", "1", []store.LoadMessage{
		{ProjectID: p.ID, ContentHash: "d1", TS: "2026-09-01T10:00:00Z", Role: "assistant",
			Content: "Decision: always use pure-Go SQLite in " + filepath.Join(p.Path, "store.go"), Tool: "devin"},
	}); err != nil {
		t.Fatal(err)
	}

	// First pass distills the project and records what it covered.
	produced, _, _, _, err := distillWithProgress(st, "", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if produced == 0 {
		t.Fatal("the first pass must distill the project's facts")
	}
	wm, err := st.DistillWatermark(p.ID)
	if err != nil || wm == 0 {
		t.Fatalf("distillation watermark not recorded: %d (err=%v)", wm, err)
	}

	// Nothing changed: the second pass must do no work at all.
	var starts, skips int
	produced2, _, _, _, err := distillWithProgress(st, "", false, func(dp DistillProgress) {
		switch dp.Phase {
		case "start":
			starts++
		case "done":
			if dp.Skipped {
				skips++
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if starts != 0 {
		t.Fatalf("an unchanged project was distilled again (%d starts)", starts)
	}
	if skips != 1 {
		t.Fatalf("expected the project to be reported skipped, got %d", skips)
	}
	if produced2 != 0 {
		t.Fatalf("a no-change pass produced %d facts", produced2)
	}

	// A new message makes it eligible again, and --full/force always is.
	if _, _, err := st.WriteRun("", "devin", "1", []store.LoadMessage{
		{ProjectID: p.ID, ContentHash: "d2", TS: "2026-09-01T11:00:00Z", Role: "assistant",
			Content: "Decision: never write into another tool's database in " + filepath.Join(p.Path, "loaders.go"), Tool: "devin"},
	}); err != nil {
		t.Fatal(err)
	}
	var restarts int
	if _, _, _, _, err := distillWithProgress(st, "", false, func(dp DistillProgress) {
		if dp.Phase == "start" {
			restarts++
		}
	}); err != nil {
		t.Fatal(err)
	}
	if restarts != 1 {
		t.Fatalf("a project with a new message must be distilled again, got %d starts", restarts)
	}

	var forced int
	if _, _, _, _, err := distillWithProgress(st, "", true, func(dp DistillProgress) {
		if dp.Phase == "start" {
			forced++
		}
	}); err != nil {
		t.Fatal(err)
	}
	if forced != 1 {
		t.Fatalf("a forced pass must distill regardless, got %d starts", forced)
	}
}
