package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func TestRunVisionCLI(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "vision_cli.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	st.Close()

	ui := render.New(os.Stdout, true)

	// 1. Add non-goal
	out := captureStdout(t, func() {
		runVision(ui, dbPath, "sila", "", "agent", false, false, []string{"add-nongoal", "No cloud databases allowed"})
	})
	if !strings.Contains(out, "Recorded non-goal") {
		t.Fatalf("expected non-goal recording message, got: %s", out)
	}

	// 2. Add milestone
	out = captureStdout(t, func() {
		runVision(ui, dbPath, "sila", "", "agent", false, false, []string{"add-milestone", "Multi-Agent Morning Standup Digest"})
	})
	if !strings.Contains(out, "Recorded milestone") {
		t.Fatalf("expected milestone recording message, got: %s", out)
	}

	// 3. List items in TOON
	out = captureStdout(t, func() {
		runVision(ui, dbPath, "sila", "", "agent", false, true, []string{"list"})
	})
	if !strings.Contains(out, "vision_items[2]{id,claim,kind}:") {
		t.Fatalf("expected TOON vision_items table, got: %s", out)
	}

	// 4. Triage conflicting proposal
	out = captureStdout(t, func() {
		runVision(ui, dbPath, "sila", "", "agent", false, false, []string{"triage", "Add cloud database Postgres support"})
	})
	if !strings.Contains(out, "REJECTED") {
		t.Fatalf("expected REJECTED triage output, got: %s", out)
	}

	// 5. Triage approved proposal
	out = captureStdout(t, func() {
		runVision(ui, dbPath, "sila", "", "agent", false, false, []string{"triage", "Add performance metrics logging"})
	})
	if !strings.Contains(out, "APPROVED") {
		t.Fatalf("expected APPROVED triage output, got: %s", out)
	}

	// 6. Compile vision
	out = captureStdout(t, func() {
		runVision(ui, dbPath, "sila", "", "agent", false, false, []string{"compile"})
	})
	if !strings.Contains(out, "# VISION.md — sila") {
		t.Fatalf("expected VISION.md header, got: %s", out)
	}
	if !strings.Contains(out, "No cloud databases allowed") {
		t.Fatalf("expected non-goal in compiled vision, got: %s", out)
	}

	// 7. Tournament triage compare
	out = captureStdout(t, func() {
		runVision(ui, dbPath, "sila", "", "agent", false, false, []string{"triage", "--compare", "Add cloud database Postgres support", "Add performance metrics logging"})
	})
	if !strings.Contains(out, "Tournament Summary") || !strings.Contains(out, "Candidate 2") {
		t.Fatalf("expected Tournament Summary with Candidate 2 winning, got: %s", out)
	}
}
