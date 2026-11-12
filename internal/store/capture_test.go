package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func openCaptureStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestPauseKeepsSinceAndResumeClears(t *testing.T) {
	st := openCaptureStore(t)
	if err := st.PauseCapture("p", "first", "a", false); err != nil {
		t.Fatal(err)
	}
	first, err := st.CaptureState("p")
	if err != nil || !first.Paused || first.Reason != "first" {
		t.Fatalf("state = %+v err=%v", first, err)
	}
	time.Sleep(5 * time.Millisecond)
	if err := st.PauseCapture("p", "second", "a", false); err != nil {
		t.Fatal(err)
	}
	second, _ := st.CaptureState("p")
	if !second.Since.Equal(first.Since) {
		t.Fatalf("since changed: %v -> %v", first.Since, second.Since)
	}
	if second.Reason != "second" {
		t.Fatalf("reason = %q", second.Reason)
	}
	if err := st.ResumeCapture("p", false); err != nil {
		t.Fatal(err)
	}
	resumed, _ := st.CaptureState("p")
	if resumed.Paused {
		t.Fatal("resume did not clear the pause")
	}
}

func TestGlobalPauseCoversProject(t *testing.T) {
	st := openCaptureStore(t)
	if err := st.PauseCapture("p", "project", "a", false); err != nil {
		t.Fatal(err)
	}
	if err := st.PauseCapture("", "global", "a", true); err != nil {
		t.Fatal(err)
	}
	state, err := st.CaptureState("p")
	if err != nil || !state.Paused || !state.Global {
		t.Fatalf("state = %+v err=%v", state, err)
	}
	if err := st.ResumeCapture("p", false); err != nil {
		t.Fatal(err)
	}
	state, _ = st.CaptureState("p")
	if !state.Paused || !state.Global {
		t.Fatal("project resume cleared the global pause")
	}
}

func TestForgetArchivesKnowledge(t *testing.T) {
	st := openCaptureStore(t)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := st.DB.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	for i, kind := range []string{"memo", "lesson", "case"} {
		exec(`INSERT INTO memory_records (id, project_id, fact_id, kind, title, summary, digest, envelope_json, created_at, updated_at)
			VALUES (?, 'p', 'f', ?, 't', 's', ?, '{}', '2026-11-06T09:00:00Z', '2026-11-06T09:00:00Z')`,
			"m"+string(rune('1'+i)), kind, "d"+string(rune('1'+i)))
	}
	exec(`INSERT INTO facts (id, project_id, claim, confidence, verified, repo_files, tools, session_ref, first_seen, promoted_at)
		VALUES ('f1','p','c','low',0,'[]','x','s','2026-11-06T09:00:00Z','2026-11-06T09:00:00Z')`)
	exec(`INSERT INTO handoffs (id, project_id, agent, summary, created_at, status) VALUES ('h1','p','a','s','2026-11-06T09:00:00Z','open')`)
	counts, err := st.ForgetProject(context.Background(), "p", true)
	if err != nil {
		t.Fatal(err)
	}
	if counts.Memos != 1 || counts.Lessons != 1 || counts.Cases != 1 || counts.Facts != 1 || counts.Handoffs != 1 {
		t.Fatalf("counts = %+v", counts)
	}
	state, _ := st.CaptureState("p")
	if !state.Forgotten || !state.ExcludeTranscripts {
		t.Fatalf("state = %+v", state)
	}
	var current int
	_ = st.DB.QueryRow(`SELECT COUNT(*) FROM memory_records WHERE project_id='p' AND state='current'`).Scan(&current)
	if current != 0 {
		t.Fatalf("current rows = %d", current)
	}
	if err := st.ResumeCapture("p", false); err != nil {
		t.Fatal(err)
	}
	state, _ = st.CaptureState("p")
	if state.Forgotten {
		t.Fatal("resume did not clear the exclusion")
	}
	var archived int
	_ = st.DB.QueryRow(`SELECT COUNT(*) FROM memory_records WHERE project_id='p' AND state='archived'`).Scan(&archived)
	if archived != 3 {
		t.Fatalf("archived = %d", archived)
	}
}
