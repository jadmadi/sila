package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/agents"
	"github.com/jadmadi/sila/internal/render"
)

// TestAgentsSyncDryRunWritesNothing guards the codex-eval F04 contract on the
// `sila agents sync` path: a dry run reports the plan and mutates no file.
func TestAgentsSyncDryRunWritesNothing(t *testing.T) {
	dir := t.TempDir()
	custom := "# user rules\n\nDo not touch my file.\n"
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(custom), 0o644); err != nil {
		t.Fatal(err)
	}
	mdcDir := filepath.Join(dir, ".cursor", "rules")
	if err := os.MkdirAll(mdcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mdcDir, "000-sila-core.mdc"), []byte("# my cursor rules\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	before := readAll(t, dir)
	ui := render.New(os.Stdout, true)

	opts := agents.CompileOptions{RepoRoot: dir, ProjectName: "dryrun", Preview: true}
	runAgentsSync(ui, dir, opts, true)

	after := readAll(t, dir)
	if before != after {
		t.Fatalf("dry run modified files:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	for _, p := range []string{"AGENTS.md", ".cursor/rules/000-sila-core.mdc"} {
		if _, err := os.Stat(filepath.Join(dir, p)); err != nil {
			t.Fatalf("%s disappeared: %v", p, err)
		}
	}
}

// TestAgentsSyncPreservesUserFilesWithoutReplace verifies that a real sync
// keeps the user's AGENTS.md content and leaves a foreign Cursor rules file
// alone unless --replace is given.
func TestAgentsSyncPreservesUserFilesWithoutReplace(t *testing.T) {
	dir := t.TempDir()
	custom := "# user rules\n\nDo not touch my file.\n"
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(custom), 0o644); err != nil {
		t.Fatal(err)
	}
	mdcDir := filepath.Join(dir, ".cursor", "rules")
	if err := os.MkdirAll(mdcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mdcPath := filepath.Join(mdcDir, "000-sila-core.mdc")
	cursorCustom := "# my cursor rules\n"
	if err := os.WriteFile(mdcPath, []byte(cursorCustom), 0o644); err != nil {
		t.Fatal(err)
	}

	ui := render.New(os.Stdout, true)
	runAgentsSync(ui, dir, agents.CompileOptions{RepoRoot: dir, ProjectName: "keep"}, true)

	// AGENTS.md: custom content survives, sila block added.
	agentsData, err := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(agentsData), "Do not touch my file.") {
		t.Fatalf("AGENTS.md user content lost: %s", agentsData)
	}
	if !strings.Contains(string(agentsData), agents.OwnedBlockBegin) {
		t.Fatalf("AGENTS.md sila block missing: %s", agentsData)
	}

	// Cursor rules: foreign file untouched without --replace.
	mdcData, err := os.ReadFile(mdcPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(mdcData) != cursorCustom {
		t.Fatalf("foreign cursor rules overwritten without --replace: %q", mdcData)
	}

	// With --replace it becomes sila-managed.
	runAgentsSync(ui, dir, agents.CompileOptions{RepoRoot: dir, ProjectName: "keep", Replace: true}, true)
	mdcData, err = os.ReadFile(mdcPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mdcData), agents.OwnedBlockBegin) {
		t.Fatalf("--replace did not adopt the cursor rules file: %s", mdcData)
	}
}

func readAll(t *testing.T, dir string) string {
	t.Helper()
	var sb strings.Builder
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sb.WriteString(path)
		sb.WriteString("\x00")
		sb.Write(data)
		sb.WriteString("\n---\n")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return sb.String()
}
