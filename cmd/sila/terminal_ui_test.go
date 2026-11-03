package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func TestTerminalUIHumanFormatting(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "ui_test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	project := "test-ui-project"
	_, err = st.EnsureProject("/workspace/test-ui")
	if err != nil {
		t.Fatal(err)
	}

	// Record a handoff with markdown formatting
	err = st.InsertHandoff(store.Handoff{
		ProjectID: project,
		Agent:     "@test-agent",
		Summary:   "Shipped **v1.0** with `zero-dependency` terminal formatting.\n\n```go\nvar ready = true\n```",
		OpenItems: []string{"Audit `cmd/sila`", "Run full `go test -race`"},
		Decisions: []string{"Use **ANSI TrueColor** themes", "Preserve clean `--json`"},
		CreatedAt: "2026-09-22T12:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}

	// 1. Plain UI (Non-TTY / Piped)
	uiPlain := render.New(os.Stdout, true) // noColor = true
	outPlain := captureStdout(t, func() {
		runHandoff(uiPlain, dbPath, project, "", "", "", "", "", false, false, nil)
	})

	if strings.Contains(outPlain, "\x1b[") {
		t.Errorf("plain output must not contain ANSI escape codes, got:\n%s", outPlain)
	}
	if !strings.Contains(outPlain, "Shipped v1.0 with `zero-dependency` terminal formatting.") {
		t.Errorf("expected clean stripped markdown in summary, got:\n%s", outPlain)
	}
	if !strings.Contains(outPlain, "Audit `cmd/sila`") {
		t.Errorf("expected open items in output")
	}

	// 2. Machine JSON Output (--json)
	outJSON := captureStdout(t, func() {
		runHandoff(uiPlain, dbPath, project, "", "", "", "", "", true, false, nil)
	})

	if strings.Contains(outJSON, "\x1b[") {
		t.Errorf("JSON output must never contain ANSI escape codes")
	}
	var h store.Handoff
	if err := json.Unmarshal([]byte(outJSON), &h); err != nil {
		t.Fatalf("failed to parse JSON handoff: %v\nJSON:\n%s", err, outJSON)
	}
	if h.ProjectID != project {
		t.Errorf("expected project %s, got %s", project, h.ProjectID)
	}
	if len(h.OpenItems) != 2 {
		t.Errorf("expected 2 open items, got %d", len(h.OpenItems))
	}
}

func TestStandupBriefingMachineSeparation(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "briefing.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	ui := render.New(os.Stdout, true)

	// Test JSON mode
	outJSON := captureStdout(t, func() {
		runHumanBriefing(ui, dbPath, "empty-proj", "24h", true, false, false)
	})
	if strings.Contains(outJSON, "\x1b[") {
		t.Errorf("JSON standup must not contain ANSI escape codes")
	}

	// Test TOON mode
	outTOON := captureStdout(t, func() {
		runHumanBriefing(ui, dbPath, "empty-proj", "24h", false, true, false)
	})
	if strings.Contains(outTOON, "\x1b[") {
		t.Errorf("TOON standup must not contain ANSI escape codes")
	}
	if !strings.Contains(outTOON, "standup_summary:") {
		t.Errorf("expected standup_summary header in TOON output, got:\n%s", outTOON)
	}
}
