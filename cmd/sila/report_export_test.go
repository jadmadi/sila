package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/dream"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func reportFixtureForCLI(t *testing.T) dream.ContinuityReport {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	rep, err := dream.BuildContinuityReport(t.Context(), st, "p", time.Now().Add(-48*time.Hour), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func TestReportMarkdownFileMode(t *testing.T) {
	dir := t.TempDir()
	rep := reportFixtureForCLI(t)
	path := filepath.Join(dir, "report.md")
	got, err := writeContinuityMarkdown(rep, path)
	if err != nil || got != path {
		t.Fatalf("path=%q err=%v", got, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v", info.Mode().Perm())
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "# Continuity report") {
		t.Fatalf("content = %s", data)
	}
}

func TestReportMarkdownDefaultPathAndDeterminism(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	rep := reportFixtureForCLI(t)
	path, err := writeContinuityMarkdown(rep, "")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, ".sila", "reports", "continuity-"+rep.Until.Format("2006-01-02")+".md")
	abs, _ := filepath.Abs(path)
	if abs != want {
		t.Fatalf("path = %q want %q", abs, want)
	}
	first, _ := os.ReadFile(path)
	if _, err := writeContinuityMarkdown(rep, ""); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(path)
	if string(first) != string(second) {
		t.Fatal("same window produced different bytes")
	}
}

func TestReportMarkdownRetention(t *testing.T) {
	dir := t.TempDir()
	rep := reportFixtureForCLI(t)
	for i := 0; i < 13; i++ {
		path := filepath.Join(dir, "continuity-2026-11-"+string(rune('a'+i))+".md")
		if _, err := writeContinuityMarkdown(rep, path); err != nil {
			t.Fatal(err)
		}
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "continuity-*.md"))
	if len(matches) != continuityReportKeep {
		t.Fatalf("kept %d files", len(matches))
	}
}

func TestReportMarkdownUnwritable(t *testing.T) {
	dir := t.TempDir()
	locked := filepath.Join(dir, "locked")
	if err := os.Mkdir(locked, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })
	rep := reportFixtureForCLI(t)
	if _, err := writeContinuityMarkdown(rep, filepath.Join(locked, "x.md")); err == nil {
		t.Fatal("expected a write error")
	}
}

func TestReportHumanHasNoEscapeBytes(t *testing.T) {
	rep := reportFixtureForCLI(t)
	ui := render.New(os.Stdout, true)
	out := captureStdout(t, func() { printContinuityHuman(ui, rep, "last 7 days") })
	if strings.Contains(out, "\x1b") {
		t.Fatal("human output contains escape bytes")
	}
	for _, heading := range []string{"Harnesses", "Continuity", "Knowledge", "Health", "Open handoffs", "Verdict"} {
		if !strings.Contains(out, heading) {
			t.Fatalf("missing %q", heading)
		}
	}
}
