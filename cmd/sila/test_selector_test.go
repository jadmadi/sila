package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/testmatrix"
)

func setupTestSelectorCLIRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	goModSrc := "module testselector\n\ngo 1.22\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goModSrc), 0644); err != nil {
		t.Fatal(err)
	}

	pkgDir := filepath.Join(dir, "pkg", "mathutil")
	if err := os.MkdirAll(pkgDir, 0755); err != nil {
		t.Fatal(err)
	}

	codeSrc := `package mathutil

func Square(n int) int {
	return n * n
}
`
	if err := os.WriteFile(filepath.Join(pkgDir, "math.go"), []byte(codeSrc), 0644); err != nil {
		t.Fatal(err)
	}

	testSrc := `package mathutil

import "testing"

func TestSquare(t *testing.T) {
	if Square(4) != 16 {
		t.Fail()
	}
}
`
	if err := os.WriteFile(filepath.Join(pkgDir, "math_test.go"), []byte(testSrc), 0644); err != nil {
		t.Fatal(err)
	}

	return dir
}

func TestCLI_TestSelector_Report(t *testing.T) {
	repo := setupTestSelectorCLIRepo(t)
	ui := render.New(os.Stdout, true)

	res, err := testmatrix.SelectTargetedTests(repo)
	if err != nil {
		t.Fatalf("SelectTargetedTests failed: %v", err)
	}

	// Verify report print functions execute without panic
	printTestSelectorReport(ui, res)

	data, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("json marshal failed: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("json unmarshal failed: %v", err)
	}

	if _, ok := parsed["status"]; !ok {
		t.Errorf("expected status key in json output")
	}
}
