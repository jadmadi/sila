package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func TestFeedbackOpenClosedFiltering(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "feedback_test.db")

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}

	fbOpen := &store.Feedback{
		ProjectID: "proj-1",
		Agent:     "agent-1",
		Category:  "idea",
		Content:   "An open suggestion",
	}
	if err := st.SaveFeedback(fbOpen); err != nil {
		t.Fatal(err)
	}

	fbClosed := &store.Feedback{
		ProjectID: "proj-1",
		Agent:     "agent-2",
		Category:  "issue",
		Content:   "A resolved issue",
	}
	if err := st.SaveFeedback(fbClosed); err != nil {
		t.Fatal(err)
	}
	if err := st.ResolveFeedback(fbClosed.ID, "agent-2", "fix-goal", "Resolved cleanly"); err != nil {
		t.Fatal(err)
	}
	st.Close()

	ui := render.New(os.Stdout, true)

	// Test 1: Subcommand "open"
	runFeedback(ui, dbPath, "", "", "idea", "", "", "", "", true, 10, false, false, false, false, false, false, []string{"open"})

	// Test 2: Subcommand "closed"
	runFeedback(ui, dbPath, "", "", "idea", "", "", "", "", true, 10, false, false, false, false, false, false, []string{"closed"})

	// Test 3: Flag --open
	runFeedback(ui, dbPath, "", "", "idea", "", "", "", "", true, 10, true, false, false, false, false, false, nil)

	// Test 4: Flag --closed
	runFeedback(ui, dbPath, "", "", "idea", "", "", "", "", true, 10, false, true, false, false, false, false, nil)
}

func TestFeedbackProjectResolutionRegisteredAndUnregisteredCwd(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "feedback_cwd.db")
	regRepo := filepath.Join(tmp, "fs-relay")
	unregDir := filepath.Join(tmp, "outside-repo")
	if err := os.MkdirAll(regRepo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(unregDir, 0o755); err != nil {
		t.Fatal(err)
	}

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	proj, err := st.EnsureProject(regRepo)
	if err != nil {
		st.Close()
		t.Fatal(err)
	}
	st.Close()

	origCwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(origCwd) }()

	ui := render.New(os.Stdout, true)

	// AC-F4-a: inside a registered repo, feedback without -p carries the project id and list -p <project> --json finds it.
	if err := os.Chdir(regRepo); err != nil {
		t.Fatal(err)
	}
	_ = captureStdout(t, func() {
		runFeedback(ui, dbPath, "", "opencode", "tip", "", "", "", "", true, 10, false, false, false, false, false, false, []string{"Registered repo tip"})
	})
	listOut := captureStdout(t, func() {
		runFeedback(ui, dbPath, "fs-relay", "", "idea", "", "", "", "", true, 10, false, false, false, false, false, false, []string{"list"})
	})
	if !strings.Contains(listOut, `"total": 1`) || !strings.Contains(listOut, proj.ID) {
		t.Fatalf("expected feedback list -p fs-relay --json to show entry for %s, got:\n%s", proj.ID, listOut)
	}

	// AC-F4-b: outside any registered repo, project field is null and no error.
	if err := os.Chdir(unregDir); err != nil {
		t.Fatal(err)
	}
	unregOut := captureStdout(t, func() {
		runFeedback(ui, dbPath, "", "opencode", "tip", "", "", "", "", true, 10, false, false, false, false, false, false, []string{"Unregistered cwd tip"})
	})
	if !strings.Contains(unregOut, `"project_id": null`) || !strings.Contains(unregOut, `"project": null`) {
		t.Fatalf("expected null project_id and project outside registered repo, got:\n%s", unregOut)
	}
}
