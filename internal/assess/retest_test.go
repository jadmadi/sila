package assess

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/dream"
)

func TestResolveReTestCommand(t *testing.T) {
	// AC-001: Go fixture resolves go test -race ./...
	t.Run("GoModuleFixture", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/test\n\ngo 1.26\n"), 0644); err != nil {
			t.Fatal(err)
		}
		cmd, err := ResolveReTestCommand(dir)
		if err != nil {
			t.Fatalf("expected success, got error: %v", err)
		}
		if cmd.Executable != "go" {
			t.Errorf("expected executable 'go', got %q", cmd.Executable)
		}
		if len(cmd.Args) < 2 || cmd.Args[0] != "test" || cmd.Args[1] != "-race" {
			t.Errorf("expected args starting with [test -race], got %v", cmd.Args)
		}
	})

	// AC-001: pnpm fixture resolves pnpm test
	t.Run("PnpmFixture", func(t *testing.T) {
		dir := t.TempDir()
		pkgJSON := `{"scripts": {"test": "vitest"}}`
		if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkgJSON), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "pnpm-lock.yaml"), []byte("lockfileVersion: '9.0'\n"), 0644); err != nil {
			t.Fatal(err)
		}
		cmd, err := ResolveReTestCommand(dir)
		if err != nil {
			t.Fatalf("expected success, got error: %v", err)
		}
		if cmd.Executable != "pnpm" {
			t.Errorf("expected executable 'pnpm', got %q", cmd.Executable)
		}
		if len(cmd.Args) != 1 || cmd.Args[0] != "test" {
			t.Errorf("expected args ['test'], got %v", cmd.Args)
		}
	})

	// AC-001: aube fixture resolves aubr test
	t.Run("AubrFixture", func(t *testing.T) {
		dir := t.TempDir()
		pkgJSON := `{"scripts": {"test": "vitest"}}`
		if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkgJSON), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "aube-lock.yaml"), []byte("lockfileVersion: '1.0'\n"), 0644); err != nil {
			t.Fatal(err)
		}
		cmd, err := ResolveReTestCommand(dir)
		if err != nil {
			t.Fatalf("expected success, got error: %v", err)
		}
		if cmd.Executable != "aubr" {
			t.Errorf("expected executable 'aubr', got %q", cmd.Executable)
		}
		if len(cmd.Args) != 1 || cmd.Args[0] != "test" {
			t.Errorf("expected args ['test'], got %v", cmd.Args)
		}
	})

	// Cargo fixture
	t.Run("CargoFixture", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte("[package]\nname = \"demo\"\n"), 0644); err != nil {
			t.Fatal(err)
		}
		cmd, err := ResolveReTestCommand(dir)
		if err != nil {
			t.Fatalf("expected success, got error: %v", err)
		}
		if cmd.Executable != "cargo" {
			t.Errorf("expected executable 'cargo', got %q", cmd.Executable)
		}
		if len(cmd.Args) != 1 || cmd.Args[0] != "test" {
			t.Errorf("expected args ['test'], got %v", cmd.Args)
		}
	})

	// Pytest in AGENTS.md
	t.Run("PytestFixture", func(t *testing.T) {
		dir := t.TempDir()
		agentsMD := "# Test Instructions\nRun `pytest -v`\n"
		if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(agentsMD), 0644); err != nil {
			t.Fatal(err)
		}
		cmd, err := ResolveReTestCommand(dir)
		if err != nil {
			t.Fatalf("expected success, got error: %v", err)
		}
		if cmd.Executable != "pytest" {
			t.Errorf("expected executable 'pytest', got %q", cmd.Executable)
		}
		if len(cmd.Args) != 1 || cmd.Args[0] != "-v" {
			t.Errorf("expected args ['-v'], got %v", cmd.Args)
		}
	})

	// AC-002: Unknown / unresolvable empty dir
	t.Run("UnresolvedEmptyDir", func(t *testing.T) {
		dir := t.TempDir()
		_, err := ResolveReTestCommand(dir)
		if err == nil {
			t.Fatalf("expected error for empty dir, got nil")
		}
		if !errors.Is(err, ErrReTestCommandUnresolved) {
			t.Errorf("expected ErrReTestCommandUnresolved, got: %v", err)
		}
	})

	// AC-002: Ambiguous multiple distinct commands
	t.Run("AmbiguousMultipleCommands", func(t *testing.T) {
		summary := &dream.RunbookSummary{
			Entries: []dream.RunbookEntry{
				{Phase: "Test", Command: "go test ./...", Source: "AGENTS.md"},
				{Phase: "Test", Command: "cargo test", Source: "Cargo.toml"},
			},
		}
		_, err := resolveReTestCommandFromSummary(summary, "/demo")
		if err == nil {
			t.Fatalf("expected error for ambiguous commands, got nil")
		}
		if !errors.Is(err, ErrReTestCommandUnresolved) {
			t.Errorf("expected ErrReTestCommandUnresolved, got: %v", err)
		}
	})

	// AC-002: Shell syntax rejections (operators, quotes, substitutions, backticks, newlines, env assignments)
	negativeCases := []struct {
		name    string
		command string
	}{
		{"Pipe", "go test ./... | grep ok"},
		{"Semicolon", "go test ./... ; rm -rf /"},
		{"Ampersand", "go test ./... && echo ok"},
		{"RedirectOut", "go test ./... > out.txt"},
		{"RedirectIn", "go test ./... < in.txt"},
		{"DoubleQuotes", "go test \"unit\""},
		{"SingleQuotes", "go test 'unit'"},
		{"SubstitutionDollar", "go test $(whoami)"},
		{"SubstitutionBraces", "go test ${TARGET}"},
		{"SubstitutionVar", "go test $TARGET"},
		{"Backticks", "go test `whoami`"},
		{"Newline", "go test\nrm -rf /"},
		{"LeadingEnvAssignment", "CI=true go test ./..."},
		{"UnsupportedCommandNpm", "npm test"},
		{"UnsupportedCommandMake", "make test"},
	}

	for _, tc := range negativeCases {
		t.Run("RejectSyntax_"+tc.name, func(t *testing.T) {
			summary := &dream.RunbookSummary{
				Entries: []dream.RunbookEntry{
					{Phase: "Test", Command: tc.command, Source: "test-manifest"},
				},
			}
			_, err := resolveReTestCommandFromSummary(summary, "/demo")
			if err == nil {
				t.Fatalf("expected error for command %q, got nil", tc.command)
			}
			if !errors.Is(err, ErrReTestCommandUnsupported) {
				t.Errorf("expected ErrReTestCommandUnsupported for %q, got: %v", tc.command, err)
			}
		})
	}
}

func TestDefaultReTestRunnerRepositoryCommand(t *testing.T) {
	// AC-003: Subprocess success, failure, cancellation preserves argv, cwd, output and false-on-error
	writeFixture := func(t *testing.T) string {
		t.Helper()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module retest.helper\n\ngo 1.26\n"), 0644); err != nil {
			t.Fatal(err)
		}
		testCode := `package retest_helper

import (
	"fmt"
	"os"
	"testing"
	"time"
)

func TestExecution(t *testing.T) {
	mode := os.Getenv("HELPER_MODE")
	switch mode {
	case "success":
		fmt.Println("retest helper passed")
	case "failure":
		fmt.Fprintln(os.Stderr, "retest helper assertion failed")
		t.Fatal("retest helper assertion failed")
	case "sleep":
		time.Sleep(3 * time.Second)
	}
}
`
		if err := os.WriteFile(filepath.Join(dir, "runner_test.go"), []byte(testCode), 0644); err != nil {
			t.Fatal(err)
		}
		return dir
	}

	t.Run("SuccessExecution", func(t *testing.T) {
		dir := writeFixture(t)
		ctx := context.Background()
		t.Setenv("HELPER_MODE", "success")

		passed, out, err := DefaultReTestRunner(ctx, dir)
		if err != nil {
			t.Fatalf("expected success, got error: %v; output: %s", err, out)
		}
		if !passed {
			t.Errorf("expected passed: true, got false; output: %s", out)
		}
		if !strings.Contains(out, "retest.helper") {
			t.Errorf("expected output to contain 'retest.helper', got: %s", out)
		}
	})

	t.Run("FailureExecution", func(t *testing.T) {
		dir := writeFixture(t)
		ctx := context.Background()
		t.Setenv("HELPER_MODE", "failure")

		passed, out, err := DefaultReTestRunner(ctx, dir)
		if err == nil {
			t.Fatalf("expected error on failure mode, got nil")
		}
		if passed {
			t.Errorf("expected passed: false on error, got true")
		}
		if !strings.Contains(out, "retest helper assertion failed") {
			t.Errorf("expected output to contain failure message, got: %s", out)
		}
	})

	t.Run("CancellationTimeout", func(t *testing.T) {
		dir := writeFixture(t)
		ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
		defer cancel()

		t.Setenv("HELPER_MODE", "sleep")

		passed, out, err := DefaultReTestRunner(ctx, dir)
		if err == nil {
			t.Fatalf("expected cancellation error, got nil")
		}
		if passed {
			t.Errorf("expected passed: false on cancellation, got true; out: %s", out)
		}
	})

	t.Run("UnresolvedReturnsError", func(t *testing.T) {
		dir := t.TempDir()
		passed, _, err := DefaultReTestRunner(context.Background(), dir)
		if err == nil {
			t.Fatalf("expected error for unresolved command, got nil")
		}
		if passed {
			t.Errorf("expected passed: false for unresolved command")
		}
		if !errors.Is(err, ErrReTestCommandUnresolved) {
			t.Errorf("expected ErrReTestCommandUnresolved, got: %v", err)
		}
	})
}
