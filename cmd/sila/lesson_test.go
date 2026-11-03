package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func TestLessonCLIAddListShow(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "lesson_cli.db")

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	proj, err := st.EnsureProject(tmp)
	if err != nil {
		st.Close()
		t.Fatal(err)
	}
	st.Close()

	ui := render.New(os.Stdout, true)

	addOut := captureStdout(t, func() {
		runLesson(ui, dbPath, proj.ID, "agent", "", "shared strings", "stream rows", "xlsx", "", false, false, true, false, []string{"xlsx export hangs"})
	})
	var fact store.Fact
	if err := json.Unmarshal([]byte(addOut), &fact); err != nil {
		t.Fatalf("add output is not JSON: %q: %v", addOut, err)
	}
	if fact.Kind != "lesson" || !fact.IsLesson() {
		t.Fatalf("fact kind = %q, want lesson", fact.Kind)
	}
	if symptom, cause, fix, _ := store.ParseLesson(fact.Claim); symptom != "xlsx export hangs" || cause != "shared strings" || fix != "stream rows" {
		t.Fatalf("recorded content = %q", fact.Claim)
	}

	listOut := captureStdout(t, func() {
		runLesson(ui, dbPath, proj.ID, "agent", "", "", "", "", "", false, false, true, false, []string{"list"})
	})
	var list struct {
		Count   int          `json:"count"`
		Lessons []store.Fact `json:"lessons"`
	}
	if err := json.Unmarshal([]byte(listOut), &list); err != nil {
		t.Fatalf("list output is not JSON: %q: %v", listOut, err)
	}
	if list.Count != 1 || len(list.Lessons) != 1 {
		t.Fatalf("list = %d lessons, want 1", list.Count)
	}

	showOut := captureStdout(t, func() {
		runLesson(ui, dbPath, proj.ID, "agent", "", "", "", "", "", false, false, true, false, []string{"show", fact.ID})
	})
	var shown store.Fact
	if err := json.Unmarshal([]byte(showOut), &shown); err != nil {
		t.Fatalf("show output is not JSON: %q: %v", showOut, err)
	}
	if shown.ID != fact.ID {
		t.Fatalf("show id = %q, want %q", shown.ID, fact.ID)
	}
}

func TestLessonDuplicates(t *testing.T) {
	existing := []store.Fact{{ID: "lesson-1", Claim: "Lesson: xlsx export hangs | cause: shared strings | fix: stream rows"}}

	exact, similar := lessonDuplicates(existing, "Lesson: xlsx export hangs | cause: shared strings | fix: stream rows")
	if exact == nil || exact.ID != "lesson-1" {
		t.Fatalf("exact = %+v, want lesson-1", exact)
	}
	if len(similar) != 0 {
		t.Fatalf("similar = %d, want 0 for an exact duplicate", len(similar))
	}

	exact, similar = lessonDuplicates(existing, "Lesson: xlsx export hangs | cause: shared strings | fix: stream rows and flush")
	if exact != nil {
		t.Fatalf("exact = %+v, want nil for a near match", exact)
	}
	if len(similar) == 0 {
		t.Fatal("similar = 0, want at least one near match")
	}
}

func TestLessonCLIDistill(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "distill_cli.db")

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	proj, err := st.EnsureProject(tmp)
	if err != nil {
		st.Close()
		t.Fatal(err)
	}

	_, err = st.DB.Exec(`INSERT INTO messages(project_id, content_hash, ts, role, content, source_tools, first_seen)
		VALUES(?, 'h_distill', '2026-09-21T10:00:00Z', 'assistant', 'Problem: deadlock on concurrent map writes\nCause: concurrent goroutines accessing state map without sync.RWMutex\nFix: wrap map with RWMutex in store.go', '["claude-code"]', '2026-09-21T10:00:00Z')`, proj.ID)
	if err != nil {
		st.Close()
		t.Fatal(err)
	}
	st.Close()

	ui := render.New(os.Stdout, true)

	// 1. Preview mode (without --save)
	previewOut := captureStdout(t, func() {
		runLesson(ui, dbPath, proj.ID, "agent", "", "", "", "", "", false, false, true, false, []string{"distill"})
	})
	var preview struct {
		Project    string `json:"project"`
		Count      int    `json:"count"`
		Candidates []struct {
			Symptom string `json:"symptom"`
			Cause   string `json:"cause"`
			Fix     string `json:"fix"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal([]byte(previewOut), &preview); err != nil {
		t.Fatalf("preview output not valid JSON: %q: %v", previewOut, err)
	}
	if preview.Count != 1 || len(preview.Candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", preview.Count)
	}
	if preview.Candidates[0].Symptom != "deadlock on concurrent map writes" {
		t.Fatalf("unexpected candidate symptom: %s", preview.Candidates[0].Symptom)
	}

	// 2. Save mode (--save)
	saveOut := captureStdout(t, func() {
		runLesson(ui, dbPath, proj.ID, "agent", "", "", "", "", "", false, true, true, false, []string{"distill"})
	})
	var savedResult struct {
		Project         string       `json:"project"`
		SavedCount      int          `json:"saved_count"`
		CandidatesCount int          `json:"candidates_count"`
		Saved           []store.Fact `json:"saved"`
	}
	if err := json.Unmarshal([]byte(saveOut), &savedResult); err != nil {
		t.Fatalf("save output not valid JSON: %q: %v", saveOut, err)
	}
	if savedResult.SavedCount != 1 || len(savedResult.Saved) != 1 {
		t.Fatalf("expected 1 saved lesson, got %d", savedResult.SavedCount)
	}
}

func TestLessonCLIReadOnly(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "readonly_lesson.db")

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	proj, err := st.EnsureProject(tmp)
	if err != nil {
		st.Close()
		t.Fatal(err)
	}
	fact, err := st.InsertLesson(proj.ID, "db lock", "concurrent writes", "retry with jitter", nil)
	if err != nil {
		st.Close()
		t.Fatal(err)
	}
	st.Close()

	// Make database file and directory read-only
	if err := os.Chmod(dbPath, 0444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(tmp, 0555); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.Chmod(tmp, 0755)
		_ = os.Chmod(dbPath, 0644)
	}()

	ui := render.New(os.Stdout, true)

	// 1. lesson list on read-only store succeeds
	listOut := captureStdout(t, func() {
		runLesson(ui, dbPath, proj.ID, "agent", "", "", "", "", "", false, false, true, false, []string{"list"})
	})
	var list struct {
		Count int `json:"count"`
	}
	if err := json.Unmarshal([]byte(listOut), &list); err != nil {
		t.Fatalf("list output is not JSON: %q: %v", listOut, err)
	}
	if list.Count != 1 {
		t.Fatalf("expected 1 lesson, got %d", list.Count)
	}

	// 2. lesson show on read-only store succeeds
	showOut := captureStdout(t, func() {
		runLesson(ui, dbPath, proj.ID, "agent", "", "", "", "", "", false, false, true, false, []string{"show", fact.ID})
	})
	var shown store.Fact
	if err := json.Unmarshal([]byte(showOut), &shown); err != nil {
		t.Fatalf("show output is not JSON: %q: %v", showOut, err)
	}
	if shown.ID != fact.ID {
		t.Fatalf("shown ID = %q, want %q", shown.ID, fact.ID)
	}

	// 3. lesson distill preview on read-only store succeeds
	distillOut := captureStdout(t, func() {
		runLesson(ui, dbPath, proj.ID, "agent", "", "", "", "", "", false, false, true, false, []string{"distill"})
	})
	var distill struct {
		Count int `json:"count"`
	}
	if err := json.Unmarshal([]byte(distillOut), &distill); err != nil {
		t.Fatalf("distill output is not JSON: %q: %v", distillOut, err)
	}
}
