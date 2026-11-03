package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/render"
)

func TestAdrNewCreatesFile(t *testing.T) {
	dir := t.TempDir()
	origWd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(origWd)

	ui := render.New(os.Stdout, true)
	out := captureStdout(t, func() {
		runAdr(ui, filepath.Join(dir, "k.db"), "", "devin", "internal/store/store.go", "", false,
			[]string{"new", "Adopt", "pure-Go", "SQLite"})
	})
	if !strings.Contains(out, "Created ADR draft") {
		t.Errorf("expected creation message, got %q", out)
	}

	entries, err := os.ReadDir(filepath.Join(dir, "docs/adr"))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 ADR file, got %d", len(entries))
	}
	data, err := os.ReadFile(filepath.Join(dir, "docs/adr", entries[0].Name()))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	body := string(data)
	if !strings.Contains(body, "# ADR-0001: Adopt pure-Go SQLite") {
		t.Errorf("body missing header:\n%s", body)
	}
	if !strings.Contains(body, "internal/store/store.go") {
		t.Errorf("body missing seeded reference")
	}
	if !strings.Contains(body, "- **Deciders**: devin") {
		t.Errorf("body missing deciders")
	}
}

func TestAdrListEmpty(t *testing.T) {
	dir := t.TempDir()
	origWd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(origWd)

	ui := render.New(os.Stdout, true)
	out := captureStdout(t, func() {
		runAdr(ui, filepath.Join(dir, "k.db"), "", "", "", "", false, []string{"list"})
	})
	if !strings.Contains(out, "No ADRs found") {
		t.Errorf("expected empty message, got %q", out)
	}
}

func TestAdrDoctorEmpty(t *testing.T) {
	dir := t.TempDir()
	origWd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(origWd)

	ui := render.New(os.Stdout, true)
	out := captureStdout(t, func() {
		runAdr(ui, filepath.Join(dir, "k.db"), "", "", "", "", false, []string{"doctor"})
	})
	if !strings.Contains(out, "No docs/adr directory found") {
		t.Errorf("expected missing-dir message, got %q", out)
	}
}

func TestAdrUnknownActionExits(t *testing.T) {
	if os.Getenv("ADR_TEST_UNKNOWN") == "1" {
		dir, _ := os.Getwd()
		ui := render.New(os.Stdout, true)
		runAdr(ui, filepath.Join(dir, "k.db"), "", "", "", "", false, []string{"bogus"})
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestAdrUnknownActionExits")
	cmd.Env = append(os.Environ(), "ADR_TEST_UNKNOWN=1")
	cmd.Dir = t.TempDir()
	if err := cmd.Run(); err == nil {
		t.Fatal("expected non-zero exit for unknown adr action")
	}
}

func TestAdrCompileWritesIndex(t *testing.T) {
	dir := t.TempDir()
	origWd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(origWd)

	// Seed an ADR on disk first.
	adrDir := filepath.Join(dir, "docs/adr")
	if err := os.MkdirAll(adrDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "# ADR-0001: Test\n\n- **Status**: Accepted\n- **Date**: 2026-01-01\n"
	if err := os.WriteFile(filepath.Join(adrDir, "ADR-0001-test.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	ui := render.New(os.Stdout, true)
	out := captureStdout(t, func() {
		runAdr(ui, filepath.Join(dir, "k.db"), "", "", "", "", false, []string{"compile"})
	})
	if !strings.Contains(out, "Compiled ADR index") {
		t.Errorf("expected compile message, got %q", out)
	}
	idx, err := os.ReadFile(filepath.Join(adrDir, "README.md"))
	if err != nil {
		t.Fatalf("README.md not written: %v", err)
	}
	if !strings.Contains(string(idx), "ADR-0001: Test") {
		t.Errorf("index missing ADR-0001:\n%s", string(idx))
	}
}
