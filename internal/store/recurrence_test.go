package store

import (
	"path/filepath"
	"testing"
	"time"
)

func TestNormalizeSymptom(t *testing.T) {
	cases := map[string]string{
		"Docker build fails!":       "docker build fails",
		"docker   build   fails.":   "docker build fails",
		"---":                       "",
		"Go test: timeout (again)?": "go test timeout again",
	}
	for input, want := range cases {
		if got := NormalizeSymptom(input); got != want {
			t.Fatalf("%q -> %q, want %q", input, got, want)
		}
	}
}

func TestLessonRecurrencePromotion(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "r.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	dir := t.TempDir()
	first, err := st.InsertLesson("p", "Docker build fails!", "agent", dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if first.RecurrenceCount != 1 || first.AutoPromoted {
		t.Fatalf("first = count %d promoted %v", first.RecurrenceCount, first.AutoPromoted)
	}
	second, err := st.InsertLesson("p", "docker build fails", "agent", dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID || second.RecurrenceCount != 2 || second.Claim != first.Claim {
		t.Fatalf("second = %+v", second)
	}
	third, err := st.InsertLesson("p", "DOCKER   BUILD   FAILS.", "agent", dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if third.ID != first.ID || third.RecurrenceCount != 3 || !third.AutoPromoted {
		t.Fatalf("third = count %d promoted %v id %s", third.RecurrenceCount, third.AutoPromoted, third.ID)
	}
	facts, err := st.ListFactsByKind("lesson", "p", time.Time{}, 10)
	if err != nil || len(facts) != 1 {
		t.Fatalf("lessons = %d err=%v", len(facts), err)
	}
}

func TestLessonDifferentSymptomDoesNotIncrement(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "r2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	dir := t.TempDir()
	if _, err := st.InsertLesson("p", "docker build fails", "agent", dir, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := st.InsertLesson("p", "docker push fails", "agent", dir, nil); err != nil {
		t.Fatal(err)
	}
	facts, _ := st.ListFactsByKind("lesson", "p", time.Time{}, 10)
	if len(facts) != 2 {
		t.Fatalf("lessons = %d", len(facts))
	}
}

func TestArchivedLessonDoesNotIncrement(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "r3.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	dir := t.TempDir()
	first, err := st.InsertLesson("p", "docker build fails", "agent", dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`UPDATE facts SET review_status = 'archived' WHERE id = ?`, first.ID); err != nil {
		t.Fatal(err)
	}
	second, err := st.InsertLesson("p", "docker build fails", "agent", dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	// The archived row keeps the count from its first save; the second insert
	// must not increment it (1 stays 1) and must not promote it.
	if second.RecurrenceCount != 1 || second.AutoPromoted {
		t.Fatalf("archived lesson incremented: count=%d promoted=%v", second.RecurrenceCount, second.AutoPromoted)
	}
	active, _ := st.ListFactsByKind("lesson", "p", time.Time{}, 10)
	if len(active) != 0 {
		t.Fatalf("archived lesson listed as active: %d", len(active))
	}
}

func TestPromotedLessonsOnly(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	dir := t.TempDir()
	for _, body := range []string{"alpha failure", "beta failure", "gamma failure"} {
		if _, err := st.InsertLesson("p", body, "agent", dir, nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.DB.Exec(`UPDATE facts SET auto_promoted = 1 WHERE claim = 'beta failure'`); err != nil {
		t.Fatal(err)
	}
	promoted, err := st.PromotedLessons("p", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(promoted) != 1 || promoted[0].Claim != "beta failure" {
		t.Fatalf("promoted = %+v", promoted)
	}
}
