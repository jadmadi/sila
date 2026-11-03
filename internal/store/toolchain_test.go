package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeGoVersion(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"go1.26.0", "1.26"},
		{"1.26", "1.26"},
		{"v1.26.2", "1.26"},
		{"'1.24'", "1.24"},
		{"\"1.25.1\"", "1.25"},
		{"^1.26", "1.26"},
	}
	for _, tc := range cases {
		got := NormalizeGoVersion(tc.input)
		if got != tc.expected {
			t.Errorf("NormalizeGoVersion(%q) = %q, want %q", tc.input, got, tc.expected)
		}
	}
}

func TestAuditToolchainNoGoMod(t *testing.T) {
	tmp := t.TempDir()
	res := AuditToolchain(tmp)
	if res.Status != StatusPass {
		t.Fatalf("expected StatusPass for empty dir, got %v", res.Status)
	}
	if !strings.Contains(res.Message, "No go.mod detected") {
		t.Fatalf("expected message to mention no go.mod, got %q", res.Message)
	}
}

func TestAuditToolchainMatchingGoModAndWorkflow(t *testing.T) {
	tmp := t.TempDir()
	goModContent := "module example.com/test\n\ngo 1.26\n"
	if err := os.WriteFile(filepath.Join(tmp, "go.mod"), []byte(goModContent), 0644); err != nil {
		t.Fatal(err)
	}

	wfDir := filepath.Join(tmp, ".github", "workflows")
	if err := os.MkdirAll(wfDir, 0755); err != nil {
		t.Fatal(err)
	}

	wfContent := `
name: CI
on: [push]
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.26'
      - run: go test ./...
`
	if err := os.WriteFile(filepath.Join(wfDir, "ci.yml"), []byte(wfContent), 0644); err != nil {
		t.Fatal(err)
	}

	res := AuditToolchain(tmp)
	if res.Status != StatusPass {
		t.Fatalf("expected StatusPass for matching versions, got %v (%s)", res.Status, res.Message)
	}
	if !strings.Contains(res.Message, "synchronized") {
		t.Fatalf("expected message to mention synchronized, got %q", res.Message)
	}
}

func TestAuditToolchainGoVersionFile(t *testing.T) {
	tmp := t.TempDir()
	goModContent := "module example.com/test\n\ngo 1.26\n"
	if err := os.WriteFile(filepath.Join(tmp, "go.mod"), []byte(goModContent), 0644); err != nil {
		t.Fatal(err)
	}

	wfDir := filepath.Join(tmp, ".github", "workflows")
	if err := os.MkdirAll(wfDir, 0755); err != nil {
		t.Fatal(err)
	}

	wfContent := `
name: CI
on: [push]
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: 'go.mod'
      - run: go test ./...
`
	if err := os.WriteFile(filepath.Join(wfDir, "ci.yml"), []byte(wfContent), 0644); err != nil {
		t.Fatal(err)
	}

	res := AuditToolchain(tmp)
	if res.Status != StatusPass {
		t.Fatalf("expected StatusPass for go-version-file, got %v (%s)", res.Status, res.Message)
	}
}

func TestAuditToolchainDetectsDriftAndRepairs(t *testing.T) {
	tmp := t.TempDir()
	goModContent := "module example.com/test\n\ngo 1.26\n"
	if err := os.WriteFile(filepath.Join(tmp, "go.mod"), []byte(goModContent), 0644); err != nil {
		t.Fatal(err)
	}

	wfDir := filepath.Join(tmp, ".github", "workflows")
	if err := os.MkdirAll(wfDir, 0755); err != nil {
		t.Fatal(err)
	}

	wfContent := `
name: CI
on: [push]
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.24'
      - run: go test ./...
`
	ciFile := filepath.Join(wfDir, "ci.yml")
	if err := os.WriteFile(ciFile, []byte(wfContent), 0644); err != nil {
		t.Fatal(err)
	}

	// 1. Audit should detect drift
	res := AuditToolchain(tmp)
	if res.Status != StatusWarn {
		t.Fatalf("expected StatusWarn on version drift, got %v (%s)", res.Status, res.Message)
	}
	if !strings.Contains(res.Message, "Toolchain drift detected") {
		t.Fatalf("expected drift message, got %q", res.Message)
	}
	if !strings.Contains(res.Detail, "1.24") || !strings.Contains(res.Detail, "1.26") {
		t.Fatalf("expected detail to mention 1.24 and 1.26, got %q", res.Detail)
	}

	// 2. Repair should update the workflow file
	repaired, err := RepairToolchain(tmp)
	if err != nil {
		t.Fatalf("RepairToolchain failed: %v", err)
	}
	if len(repaired) == 0 {
		t.Fatalf("expected at least 1 repaired item")
	}

	// 3. Workflow file should now have 1.26
	updatedData, err := os.ReadFile(ciFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(updatedData), "go-version: '1.26'") {
		t.Fatalf("expected workflow to be updated with 1.26, got:\n%s", string(updatedData))
	}

	// 4. Subsequent audit should pass
	resPost := AuditToolchain(tmp)
	if resPost.Status != StatusPass {
		t.Fatalf("expected StatusPass after repair, got %v (%s)", resPost.Status, resPost.Message)
	}
}

func TestStoreDoctorIncludesToolchainCheck(t *testing.T) {
	tmp := t.TempDir()
	st, err := Open(filepath.Join(tmp, "doctor_tc.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	rep, err := st.Doctor(false)
	if err != nil {
		t.Fatal(err)
	}

	if !rep.HasCheck("Toolchain") {
		t.Fatalf("expected DoctorReport to have a Toolchain check")
	}
}
