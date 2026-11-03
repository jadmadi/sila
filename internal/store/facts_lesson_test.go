package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLessonInsertQueryAndGrounding(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(filepath.Join(dir, "lesson.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	proj, err := st.EnsureProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "fix.go"), []byte("package fix"), 0o644); err != nil {
		t.Fatal(err)
	}

	content := FormatLesson("xlsx export hangs", "shared strings", "stream rows", "xlsx,export")
	fact, err := st.InsertLesson(proj.ID, content, "agent", dir, []string{"fix.go"})
	if err != nil {
		t.Fatal(err)
	}
	if fact.Kind != "lesson" {
		t.Fatalf("kind = %q, want lesson", fact.Kind)
	}
	if !fact.IsLesson() {
		t.Fatal("IsLesson = false, want true")
	}
	if !fact.FileRefsExist {
		t.Fatal("FileRefsExist = false, want true for an existing evidence file")
	}

	symptom, cause, fix, tags := ParseLesson(content)
	if symptom != "xlsx export hangs" || cause != "shared strings" || fix != "stream rows" || tags != "xlsx,export" {
		t.Fatalf("parse = %q/%q/%q/%q", symptom, cause, fix, tags)
	}

	lessons, err := st.LessonFacts(proj.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(lessons) != 1 || lessons[0].ID != fact.ID {
		t.Fatalf("LessonFacts = %d rows, want the inserted lesson", len(lessons))
	}

	all, err := st.LessonFacts("", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Fatalf("cross-project LessonFacts = %d rows, want 1", len(all))
	}

	ungrounded, err := st.InsertLesson(proj.ID, FormatLesson("missing evidence", "", "", ""), "agent", dir, []string{"nope.go"})
	if err != nil {
		t.Fatal(err)
	}
	if ungrounded.FileRefsExist {
		t.Fatal("FileRefsExist = true for a missing evidence file, want false")
	}
}
