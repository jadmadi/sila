package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jadmadi/sila/internal/store"
)

func TestAppendToolchainDoctorCheck(t *testing.T) {
	rep := &store.DoctorReport{}
	appendToolchainDoctorCheck(rep, false)

	if !rep.HasCheck("Toolchain") {
		t.Fatalf("expected DoctorReport to have Toolchain check appended")
	}

	// Calling it a second time must be idempotent
	prevLen := len(rep.Checks)
	appendToolchainDoctorCheck(rep, false)
	if len(rep.Checks) != prevLen {
		t.Fatalf("appendToolchainDoctorCheck duplicated checks: before %d, after %d", prevLen, len(rep.Checks))
	}
}

func TestAppendToolchainDoctorCheckRepair(t *testing.T) {
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origWd)

	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "go.mod"), []byte("module test\n\ngo 1.26\n"), 0644); err != nil {
		t.Fatal(err)
	}
	wfDir := filepath.Join(tmp, ".github", "workflows")
	if err := os.MkdirAll(wfDir, 0755); err != nil {
		t.Fatal(err)
	}
	ciFile := filepath.Join(wfDir, "ci.yml")
	if err := os.WriteFile(ciFile, []byte("name: CI\njobs:\n  t:\n    steps:\n      - uses: actions/setup-go@v5\n        with:\n          go-version: '1.24'\n"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}

	rep := &store.DoctorReport{}
	appendToolchainDoctorCheck(rep, true)

	if !rep.RepairExecuted {
		t.Fatalf("expected RepairExecuted = true")
	}
	if len(rep.RepairedItems) == 0 {
		t.Fatalf("expected at least 1 repaired item")
	}
}
