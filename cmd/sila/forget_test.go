package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func TestForgetArchivesAndResumeClears(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "f.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`INSERT INTO memory_records (id, project_id, fact_id, kind, title, summary, digest, envelope_json, created_at, updated_at)
		VALUES ('m1','p','f1','memo','t','s','d1','{}','2026-11-06T09:00:00Z','2026-11-06T09:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	st.Close()
	captureStdout(t, func() {
		runForget(render.New(os.Stdout, true), dbPath, "p", true, true, false)
	})
	st, err = store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	state, _ := st.CaptureState("p")
	if !state.Forgotten || !state.ExcludeTranscripts {
		t.Fatalf("state = %+v", state)
	}
	var archived int
	_ = st.DB.QueryRow(`SELECT COUNT(*) FROM memory_records WHERE state='archived'`).Scan(&archived)
	if archived != 1 {
		t.Fatalf("archived = %d", archived)
	}
}

func TestCaptureLineStates(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "c.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if got := captureLine(captureStateFor(st, "p")); got != "ACTIVE" {
		t.Fatalf("active = %q", got)
	}
	if err := st.PauseCapture("p", "maintenance", "test", false); err != nil {
		t.Fatal(err)
	}
	line := captureLine(captureStateFor(st, "p"))
	if !strings.Contains(line, "PAUSED") || !strings.Contains(line, "maintenance") {
		t.Fatalf("paused = %q", line)
	}
	if err := st.ResumeCapture("p", false); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ForgetProject(t.Context(), "p", false); err != nil {
		t.Fatal(err)
	}
	if got := captureLine(captureStateFor(st, "p")); got != "FORGOTTEN" {
		t.Fatalf("forgotten = %q", got)
	}
}
