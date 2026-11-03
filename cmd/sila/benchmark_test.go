package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func TestRunBenchmarkOutputsTable(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "k.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	_, _, err = st.WriteRun("", "devin", "1", []store.LoadMessage{
		{ProjectID: "p1", ContentHash: "h1", TS: "2026-09-01T00:00:00Z", Role: "assistant", Content: "msg", Tool: "devin"},
	})
	st.Close()
	if err != nil {
		t.Fatalf("WriteRun: %v", err)
	}

	origWd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(origWd)

	ui := render.New(os.Stdout, true)
	out := captureStdout(t, func() {
		runBenchmark(ui, dbPath, "p1", 0, false, false)
	})
	if !strings.Contains(out, "sila · benchmark") {
		t.Errorf("expected benchmark header, got %q", out)
	}
	if !strings.Contains(out, "devin") {
		t.Errorf("expected devin in table, got %q", out)
	}
}

func TestRunBenchmarkJSON(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "k.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	st.Close()

	origWd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(origWd)

	ui := render.New(os.Stdout, true)
	out := captureStdout(t, func() {
		runBenchmark(ui, dbPath, "p1", 0, true, false)
	})
	if !strings.Contains(out, "\"total_tools\"") {
		t.Errorf("expected JSON with total_tools, got %q", out)
	}
}

func TestRunBenchmarkTOON(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "k.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	_, _, err = st.WriteRun("", "devin", "1", []store.LoadMessage{
		{ProjectID: "p1", ContentHash: "h1", TS: "2026-09-01T00:00:00Z", Role: "assistant", Content: "msg", Tool: "devin"},
	})
	st.Close()
	if err != nil {
		t.Fatalf("WriteRun: %v", err)
	}

	origWd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(origWd)

	ui := render.New(os.Stdout, true)
	out := captureStdout(t, func() {
		runBenchmark(ui, dbPath, "p1", 0, false, true)
	})
	if !strings.Contains(out, "benchmark[1]") {
		t.Errorf("expected TOON benchmark[1] header, got %q", out)
	}
}

func TestRunBenchmarkMinSessionsFilter(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "k.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	// devin has 1 project, claude has 1 project
	_, _, err = st.WriteRun("", "devin", "1", []store.LoadMessage{
		{ProjectID: "p1", ContentHash: "h1", TS: "2026-09-01T00:00:00Z", Role: "assistant", Content: "msg", Tool: "devin"},
	})
	if err != nil {
		t.Fatalf("WriteRun: %v", err)
	}
	_, _, err = st.WriteRun("", "claude", "1", []store.LoadMessage{
		{ProjectID: "p1", ContentHash: "h2", TS: "2026-09-01T00:00:00Z", Role: "assistant", Content: "msg", Tool: "claude"},
	})
	st.Close()
	if err != nil {
		t.Fatalf("WriteRun: %v", err)
	}

	origWd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(origWd)

	ui := render.New(os.Stdout, true)
	// min-sessions=5 should filter out both tools (each has 1 project).
	out := captureStdout(t, func() {
		runBenchmark(ui, dbPath, "", 5, false, false)
	})
	if !strings.Contains(out, "No tools") {
		t.Errorf("expected empty state with min-sessions=5, got %q", out)
	}
}
