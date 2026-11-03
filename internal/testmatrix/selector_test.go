package testmatrix

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setupTestMatrixRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	goModSrc := "module testcalc\n\ngo 1.22\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goModSrc), 0644); err != nil {
		t.Fatal(err)
	}

	pkgDir := filepath.Join(dir, "pkg", "calc")
	if err := os.MkdirAll(pkgDir, 0755); err != nil {
		t.Fatal(err)
	}

	calcSrc := `package calc

func Add(a, b int) int {
	return a + b
}

func Multiply(a, b int) int {
	return a * b
}

func UntestedFunction() string {
	return "nobody tests me"
}
`
	if err := os.WriteFile(filepath.Join(pkgDir, "calc.go"), []byte(calcSrc), 0644); err != nil {
		t.Fatal(err)
	}

	testSrc := `package calc

import "testing"

func TestAdd(t *testing.T) {
	if Add(2, 3) != 5 {
		t.Fail()
	}
}

func TestMultiply(t *testing.T) {
	if Multiply(2, 3) != 6 {
		t.Fail()
	}
}
`
	if err := os.WriteFile(filepath.Join(pkgDir, "calc_test.go"), []byte(testSrc), 0644); err != nil {
		t.Fatal(err)
	}

	return dir
}

func TestBuildTestCallGraph(t *testing.T) {
	repo := setupTestMatrixRepo(t)

	graph, err := BuildTestCallGraph(repo)
	if err != nil {
		t.Fatalf("BuildTestCallGraph failed: %v", err)
	}

	// Check that Add maps to TestAdd
	addTargets, ok := graph.SymbolToTests["Add"]
	if !ok || len(addTargets) == 0 {
		t.Fatalf("expected Add to be mapped to tests, got none")
	}
	if addTargets[0].TestFunction != "TestAdd" {
		t.Errorf("expected TestAdd, got %s", addTargets[0].TestFunction)
	}

	// Check that Multiply maps to TestMultiply
	mulTargets, ok := graph.SymbolToTests["Multiply"]
	if !ok || len(mulTargets) == 0 {
		t.Fatalf("expected Multiply to be mapped to tests, got none")
	}
	if mulTargets[0].TestFunction != "TestMultiply" {
		t.Errorf("expected TestMultiply, got %s", mulTargets[0].TestFunction)
	}

	// Check that UntestedFunction maps to nothing
	untestedTargets := graph.SymbolToTests["UntestedFunction"]
	if len(untestedTargets) > 0 {
		t.Errorf("expected 0 tests for UntestedFunction, got %d", len(untestedTargets))
	}
}

func TestBuildGoTestCommand(t *testing.T) {
	targets := []TestTarget{
		{TestFunction: "TestInsertFact", TestFile: "store_test.go", Package: "./internal/store"},
		{TestFunction: "TestQueryFact", TestFile: "query_test.go", Package: "./internal/store"},
	}

	cmd := BuildGoTestCommand(targets)
	expected := "go test -race -run '^(TestInsertFact|TestQueryFact)$' ./internal/store"
	if cmd != expected {
		t.Errorf("got %q, want %q", cmd, expected)
	}
}

func TestRunTargeted_Execution(t *testing.T) {
	repo := setupTestMatrixRepo(t)
	ctx := context.Background()

	// 1. Single test execution with quotes around regex
	cmd := "go test -v -run '^TestAdd$' ./pkg/calc"
	out, code, err := RunTargeted(ctx, repo, cmd)
	if err != nil {
		t.Fatalf("RunTargeted failed with err: %v\nOutput: %s", err, out)
	}
	if code != 0 {
		t.Errorf("expected exit code 0, got %d", code)
	}
	if strings.Contains(out, "[no tests to run]") {
		t.Errorf("false positive: tests were not actually executed! Output:\n%s", out)
	}
	if !strings.Contains(out, "=== RUN   TestAdd") || !strings.Contains(out, "--- PASS: TestAdd") {
		t.Errorf("expected TestAdd to execute and pass, got:\n%s", out)
	}

	// 2. Multiple tests with group regex and quotes
	cmdMulti := "go test -v -run '^(TestAdd|TestMultiply)$' ./pkg/calc"
	outMulti, codeMulti, errMulti := RunTargeted(ctx, repo, cmdMulti)
	if errMulti != nil {
		t.Fatalf("RunTargeted failed for multi with err: %v\nOutput: %s", errMulti, outMulti)
	}
	if codeMulti != 0 {
		t.Errorf("expected exit code 0, got %d", codeMulti)
	}
	if !strings.Contains(outMulti, "=== RUN   TestAdd") || !strings.Contains(outMulti, "=== RUN   TestMultiply") {
		t.Errorf("expected both TestAdd and TestMultiply to execute, got:\n%s", outMulti)
	}

	// 3. Split command args unit checks
	parsed := splitCommandArgs("go test -race -run '^(TestA|TestB)$' ./pkg")
	expected := []string{"go", "test", "-race", "-run", "^(TestA|TestB)$", "./pkg"}
	if len(parsed) != len(expected) {
		t.Fatalf("splitCommandArgs returned %d args, want %d: %v", len(parsed), len(expected), parsed)
	}
	for i := range parsed {
		if parsed[i] != expected[i] {
			t.Errorf("arg %d: got %q, want %q", i, parsed[i], expected[i])
		}
	}
}
