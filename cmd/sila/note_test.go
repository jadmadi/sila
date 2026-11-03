package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func TestRunNoteCLI(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	proj, err := st.EnsureProject(tmpDir)
	if err != nil {
		t.Fatalf("EnsureProject failed: %v", err)
	}
	projID := proj.ID
	st.Close()

	ui := render.New(nil, true)

	captureOutput := func(fn func()) string {
		oldStdout := os.Stdout
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatalf("pipe failed: %v", err)
		}
		os.Stdout = w

		fn()

		w.Close()
		os.Stdout = oldStdout
		var buf bytes.Buffer
		io.Copy(&buf, r)
		return buf.String()
	}

	// 1. Add note
	outAdd := captureOutput(func() {
		runNote(ui, dbPath, projID, false, []string{
			"add",
			"cli/internal/store::Open",
			"Always set busy_timeout(5000) and WAL mode",
			"--kind=invariant",
		})
	})
	if !strings.Contains(outAdd, "Recorded symbol invariant") {
		t.Errorf("expected success message, got: %s", outAdd)
	}

	// 2. List notes
	outList := captureOutput(func() {
		runNote(ui, dbPath, projID, false, []string{"list"})
	})
	if !strings.Contains(outList, "busy_timeout(5000)") {
		t.Errorf("expected listed note, got: %s", outList)
	}

	// 3. Sync export
	outSync := captureOutput(func() {
		runNote(ui, dbPath, projID, false, []string{"sync", "--export"})
	})
	if !strings.Contains(outSync, "Exported") {
		t.Errorf("expected export message, got: %s", outSync)
	}

	// 4. Recall with --symbol
	outRecall := captureOutput(func() {
		runRecall(ui, dbPath, projID, 5, false, false, false, []string{"--symbol=Open"})
	})
	if !strings.Contains(outRecall, "busy_timeout(5000)") {
		t.Errorf("expected recall hit for symbol note, got: %s", outRecall)
	}
}

func TestRunNote_FreshDBWithoutExplicitEnsureProject(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "fresh.db")
	ui := render.New(nil, true)

	captureOutput := func(fn func()) string {
		oldStdout := os.Stdout
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatalf("pipe failed: %v", err)
		}
		os.Stdout = w
		fn()
		w.Close()
		os.Stdout = oldStdout
		var buf bytes.Buffer
		io.Copy(&buf, r)
		return buf.String()
	}

	outAdd := captureOutput(func() {
		runNote(ui, dbPath, "", false, []string{
			"add",
			"pkg/service::Start",
			"Must always listen on localhost by default",
			"--kind=invariant",
		})
	})
	if !strings.Contains(outAdd, "Recorded symbol invariant") {
		t.Errorf("expected success message, got: %s", outAdd)
	}
}
