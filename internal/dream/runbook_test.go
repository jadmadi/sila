package dream

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectRunbookFromAgentsMD(t *testing.T) {
	tmp := t.TempDir()
	agentsContent := `# AGENTS.md
## Mandatory Agent Discipline
1. Run tests:
   ` + "```bash\n   go test -race ./...\n   ```" + `
2. Rebuild:
   ` + "```bash\n   ./build.sh\n   ```" + `
`
	if err := os.WriteFile(filepath.Join(tmp, "AGENTS.md"), []byte(agentsContent), 0644); err != nil {
		t.Fatal(err)
	}

	rb := DetectRunbook(tmp)
	if rb == nil || len(rb.Entries) < 2 {
		t.Fatalf("expected at least 2 entries, got: %+v", rb)
	}

	hasTest := false
	hasBuild := false
	for _, e := range rb.Entries {
		if e.Phase == "Test" && strings.Contains(e.Command, "go test -race ./...") {
			hasTest = true
		}
		if e.Phase == "Build" && strings.Contains(e.Command, "./build.sh") {
			hasBuild = true
		}
	}

	if !hasTest {
		t.Errorf("expected test command go test -race ./..., got entries: %+v", rb.Entries)
	}
	if !hasBuild {
		t.Errorf("expected build command ./build.sh, got entries: %+v", rb.Entries)
	}
}

func TestDetectRunbookFromManifest(t *testing.T) {
	tmp := t.TempDir()
	// Create a dummy go.mod
	if err := os.WriteFile(filepath.Join(tmp, "go.mod"), []byte("module foo\n\ngo 1.26\n"), 0644); err != nil {
		t.Fatal(err)
	}

	rb := DetectRunbook(tmp)
	if rb == nil || len(rb.Entries) == 0 {
		t.Fatalf("expected detected runbook entries, got nil")
	}

	foundTest := false
	for _, e := range rb.Entries {
		if e.Phase == "Test" && e.Command == "go test -race ./..." {
			foundTest = true
		}
	}
	if !foundTest {
		t.Errorf("expected default Go test command, got: %+v", rb.Entries)
	}
}

// A runbook command whose package.json script does not exist must never be
// suggested, and the runner follows the project's lockfiles.
func TestDetectRunbookValidatesPackageScripts(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"scripts":{"test:unit":"vitest","build":"vite build"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pnpm-lock.yaml"), []byte("lockfileVersion: 9\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := detectTestCommand(dir); got != "pnpm run test:unit" {
		t.Fatalf("test command = %q, want pnpm run test:unit (no test script exists)", got)
	}
	if got := detectBuildCommand(dir); got != "pnpm build" {
		t.Fatalf("build command = %q, want pnpm build", got)
	}

	// aube lock wins over pnpm when both are present.
	if err := os.WriteFile(filepath.Join(dir, "aube-lock.yaml"), []byte("lockfileVersion: '9.0'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := detectTestCommand(dir); got != "aubr run test:unit" {
		t.Fatalf("test command with aube lock = %q, want aubr run test:unit", got)
	}

	// No matching scripts means no command, never a misleading one.
	empty := t.TempDir()
	if err := os.WriteFile(filepath.Join(empty, "package.json"), []byte(`{"scripts":{"lint":"eslint ."}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := detectTestCommand(empty); got != "" {
		t.Fatalf("test command without a test script = %q, want empty", got)
	}
	if got := detectBuildCommand(empty); got != "" {
		t.Fatalf("build command without a build script = %q, want empty", got)
	}
}

// A later declaration overrides an earlier one, and a negated mention does not
// become a runbook command.
func TestParseRunbookOverrideAndNegation(t *testing.T) {
	content := "# AGENTS.md\n\nTest: `pnpm test`\n\nDo not run `pnpm test`; use `aubr run test:unit`.\n"
	entries := parseRunbookFromMarkdown(content)
	var test string
	for _, e := range entries {
		if e.Phase == "Test" {
			test = e.Command
		}
	}
	if test != "aubr run test:unit" {
		t.Fatalf("Test command = %q, want the later override", test)
	}

	negated := parseRunbookFromMarkdown("Never run `pnpm test` and avoid `pnpm build`.\n")
	if len(negated) != 0 {
		t.Fatalf("negated mentions became commands: %#v", negated)
	}
}

// Sibling guidance in a shared multi-repo AGENTS.md must never cross-pollute:
// a Go sub-repo resolves Go commands and a Node sub-repo resolves Node commands.
func TestDetectRunbook_SiblingGuidanceAndStackGrounding(t *testing.T) {
	ws := t.TempDir()

	sharedAgentsMD := `# Master Workspace AGENTS.md

## Directory Responsibilities

### 1. cli/ — Sila Core Engine & CLI
Working directory for all Go commands MUST be cli/.
` + "```bash\ngo test -race ./...\n./build.sh\n```" + `

### 2. worker/ — Cloudflare Edge MCP Bridge
Working directory for edge tasks MUST be worker/.
` + "```bash\npnpm test\npnpm build\n```" + `

## Operating Invariants
Always run tests before commit (` + "`go test -race ./...`" + `, ` + "`pnpm test`" + `).
`
	if err := os.WriteFile(filepath.Join(ws, "AGENTS.md"), []byte(sharedAgentsMD), 0o644); err != nil {
		t.Fatal(err)
	}

	// 1. Go sub-repo (cli/)
	cliDir := filepath.Join(ws, "cli")
	if err := os.MkdirAll(cliDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cliDir, "go.mod"), []byte("module github.com/example/cli\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cliDir, "build.sh"), []byte("#!/bin/sh\necho built\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	cliRB := DetectRunbook(cliDir)
	if cliRB == nil || len(cliRB.Entries) < 2 {
		t.Fatalf("expected at least 2 entries for cliDir, got: %+v", cliRB)
	}
	for _, e := range cliRB.Entries {
		if strings.Contains(e.Command, "pnpm") {
			t.Fatalf("cliDir received sibling pnpm command: %+v", e)
		}
		if e.Phase == "Test" && !strings.Contains(e.Command, "go test") {
			t.Errorf("expected go test for cliDir, got: %s", e.Command)
		}
		if e.Phase == "Build" && !strings.Contains(e.Command, "build.sh") {
			t.Errorf("expected build.sh for cliDir, got: %s", e.Command)
		}
	}

	// 2. Node sub-repo (worker/)
	workerDir := filepath.Join(ws, "worker")
	if err := os.MkdirAll(workerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workerDir, "package.json"), []byte(`{"scripts":{"test":"vitest","build":"vite build"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workerDir, "pnpm-lock.yaml"), []byte("lockfileVersion: 9\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	workerRB := DetectRunbook(workerDir)
	if workerRB == nil || len(workerRB.Entries) < 2 {
		t.Fatalf("expected at least 2 entries for workerDir, got: %+v", workerRB)
	}
	for _, e := range workerRB.Entries {
		if strings.Contains(e.Command, "go test") || strings.Contains(e.Command, "build.sh") {
			t.Fatalf("workerDir received sibling Go command: %+v", e)
		}
		if e.Phase == "Test" && !strings.Contains(e.Command, "pnpm test") {
			t.Errorf("expected pnpm test for workerDir, got: %s", e.Command)
		}
		if e.Phase == "Build" && !strings.Contains(e.Command, "pnpm build") {
			t.Errorf("expected pnpm build for workerDir, got: %s", e.Command)
		}
	}
}

// Uncertain commands are omitted rather than invented when manifests and instructions lack scripts.
func TestDetectRunbook_UncertainCommandsOmitted(t *testing.T) {
	emptyDir := t.TempDir()
	// Only documentation, no runnable manifest or scripts
	if err := os.WriteFile(filepath.Join(emptyDir, "README.md"), []byte("# Docs only\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	rb := DetectRunbook(emptyDir)
	if rb != nil && len(rb.Entries) > 0 {
		t.Fatalf("expected no invented commands for empty repo, got: %+v", rb)
	}
}

// Explicit local command override in AGENTS.md takes precedence over default conventions.
func TestDetectRunbook_ExplicitLocalOverride(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module foo\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	agentsMD := "# AGENTS.md\n\n```bash\ngo test -tags=integration ./...\n```\n"
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(agentsMD), 0o644); err != nil {
		t.Fatal(err)
	}

	rb := DetectRunbook(dir)
	if rb == nil || len(rb.Entries) == 0 {
		t.Fatal("expected detected runbook")
	}
	foundCustom := false
	for _, e := range rb.Entries {
		if e.Phase == "Test" && e.Command == "go test -tags=integration ./..." {
			foundCustom = true
		}
	}
	if !foundCustom {
		t.Errorf("expected explicit local override to win, got: %+v", rb.Entries)
	}
}

func TestDetectRunbook_PythonSubdirAudit(t *testing.T) {
	dir := t.TempDir()
	subDir := filepath.Join(dir, "opencode-telemetry")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "telemetry.py"), []byte("print('build')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "audit.py"), []byte("print('audit')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	agentsMD := `# AGENTS.md
## Runbook
` + "```bash\ncd opencode-telemetry && python3 telemetry.py\ncd opencode-telemetry && python3 audit.py\nsila gate\n```\n"
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(agentsMD), 0o644); err != nil {
		t.Fatal(err)
	}

	rb := DetectRunbook(dir)
	if rb == nil {
		t.Fatal("expected runbook, got nil")
	}
	var testCmd, buildCmd string
	for _, e := range rb.Entries {
		switch e.Phase {
		case "Test":
			testCmd = e.Command
		case "Build":
			buildCmd = e.Command
		}
	}
	if testCmd != "python3 opencode-telemetry/audit.py" {
		t.Fatalf("Test command = %q, want python3 opencode-telemetry/audit.py", testCmd)
	}
	if buildCmd != "python3 opencode-telemetry/telemetry.py" {
		t.Fatalf("Build command = %q, want python3 opencode-telemetry/telemetry.py", buildCmd)
	}
}
