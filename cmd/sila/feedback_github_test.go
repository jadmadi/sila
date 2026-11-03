package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func TestRunFeedback_GitHubDispatch(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "sila.db")

	ui := render.New(nil, true)

	// 1. Record feedback with issueFlag=true and yesConfirm=true, noOpen=true, jsonOut=true
	runFeedback(ui, dbPath, "test-proj", "antigravity", "idea", "", "", "", "", true, 10, false, false, true, false, true, true, []string{"Add automated telemetry sentry to gate"})

	st, err := store.OpenRO(dbPath)
	if err != nil {
		t.Fatalf("OpenRO: %v", err)
	}
	defer st.Close()

	items, err := st.ListFeedback("test-proj", "", 10)
	if err != nil {
		t.Fatalf("ListFeedback: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 feedback item, got %d", len(items))
	}
	fb := items[0]
	if !strings.Contains(fb.ResolutionNote, "GitHub Issue:") {
		t.Errorf("expected ResolutionNote to link to GitHub Issue, got: %q", fb.ResolutionNote)
	}

	// 2. Test submit subcommand on existing feedback
	runFeedback(ui, dbPath, "test-proj", "antigravity", "", "", "", "", "", true, 10, false, false, false, false, true, true, []string{"submit", fb.ID})
}
