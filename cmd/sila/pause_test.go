package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func TestHookStopWritesNothingWhilePaused(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "s.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.PauseCapture("p", "stop capture", "test", false); err != nil {
		t.Fatal(err)
	}
	ui := render.New(os.Stdout, true)
	captureStdout(t, func() {
		runHookWithOptions(ui, dbPath, "p", "", "claude", "s1", dir, "", false, false, false, 0, []string{"session-end"}, hookOptions{})
	})
	var sessions, events int
	if err := st.DB.QueryRow(`SELECT COUNT(*) FROM agent_sessions`).Scan(&sessions); err != nil {
		t.Fatal(err)
	}
	if err := st.DB.QueryRow(`SELECT COUNT(*) FROM tool_events`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if sessions != 0 || events != 0 {
		t.Fatalf("paused hook wrote sessions=%d events=%d", sessions, events)
	}
}

func TestHookEventWritesClassification(t *testing.T) {
	for _, event := range []string{"session-start", "session-end", "stop", "post-tool-use"} {
		if !hookEventWrites(event) {
			t.Fatalf("%s should write", event)
		}
	}
	for _, event := range []string{"pre-commit", "pre-push", "status", "drain", "install", "uninstall"} {
		if hookEventWrites(event) {
			t.Fatalf("%s should not be blocked", event)
		}
	}
}
