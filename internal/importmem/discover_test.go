package importmem

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func baseOptions(home, repo string) ImportOptions {
	return ImportOptions{ProjectID: "proj", HomeDir: home, RepoRoot: repo, From: "all"}
}

func labels(files []DiscoveredFile) []string {
	out := make([]string, len(files))
	for i, file := range files {
		out[i] = file.Label
	}
	return out
}

func TestImportTypes(t *testing.T) {
	if len(SourceOrder) != 3 || SourceOrder[0] != SourceClaude || SourceOrder[1] != SourceCodex || SourceOrder[2] != SourceGrok {
		t.Fatalf("source order: %v", SourceOrder)
	}
	for _, from := range []string{"claude", "codex", "grok", "all", ""} {
		if _, err := ParseSources(from); err != nil {
			t.Fatalf("ParseSources(%q): %v", from, err)
		}
	}
	if _, err := ParseSources("cursor"); err == nil || CodeOf(err) != CodeProject {
		t.Fatalf("invalid source error: %v", err)
	}
	opts := ImportOptions{}.WithDefaults()
	if opts.MaxFiles != DefaultMaxFiles || opts.MaxFileBytes != DefaultMaxFileBytes ||
		opts.MaxTotalBytes != DefaultMaxTotalBytes || opts.MaxCandidates != DefaultMaxCandidates {
		t.Fatalf("defaults: %+v", opts)
	}
	for _, code := range []string{CodeInstructionsRefused, CodeBound, CodePathUnsafe, CodeRedactionFailed, CodeMalformedFile, CodeProject, CodeDuplicate, CodeSimilar, CodeStore} {
		if code == "" {
			t.Fatal("empty error code")
		}
	}
	if code := CodeOf(&ImportError{Code: CodeBound}); code != CodeBound {
		t.Fatalf("CodeOf = %q", code)
	}
}

func TestDiscoverMemoryFiles(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	repo := filepath.Join(root, "repo")
	writeFixture(t, filepath.Join(home, ".claude", "projects", "alpha", "memory", "notes.md"), "## Decisions\n- one\n")
	writeFixture(t, filepath.Join(home, ".claude", "projects", "alpha", "memory", "nested", "deep.md"), "## Lessons\n- two\n")
	writeFixture(t, filepath.Join(home, ".codex", "memories", "codex.md"), "## Conventions\n- three\n")
	writeFixture(t, filepath.Join(home, ".grok", "memory", "grok.md"), "## Invariants\n- four\n")
	writeFixture(t, filepath.Join(home, ".grok", "topics", "topic.md"), "## Fixes\n- five\n")
	writeFixture(t, filepath.Join(repo, ".claude", "memory", "project.md"), "## Preferences\n- six\n")
	writeFixture(t, filepath.Join(repo, ".codex", "memory", "codex.md"), "## Procedures\n- seven\n")
	writeFixture(t, filepath.Join(repo, ".grok", "memory", "grok.md"), "## Failed approaches\n- eight\n")

	files, sources, skipped, bound, err := Discover(baseOptions(home, repo))
	if err != nil {
		t.Fatal(err)
	}
	if bound {
		t.Fatal("unexpected bound")
	}
	if len(skipped) != 0 {
		t.Fatalf("unexpected skips: %+v", skipped)
	}
	want := []string{
		"claude:home:projects/alpha/memory/nested/deep.md",
		"claude:home:projects/alpha/memory/notes.md",
		"claude:repo:memory/project.md",
		"codex:home:memories/codex.md",
		"codex:repo:memory/codex.md",
		"grok:home:memory/grok.md",
		"grok:home:topics/topic.md",
		"grok:repo:memory/grok.md",
	}
	got := labels(files)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("labels:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for _, source := range sources {
		if source.Status != "found" || source.Files == 0 {
			t.Fatalf("source report: %+v", source)
		}
	}
}

func TestDiscoverSkipsInstructions(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	repo := filepath.Join(root, "repo")
	writeFixture(t, filepath.Join(home, ".claude", "CLAUDE.md"), "## Decisions\n- never\n")
	writeFixture(t, filepath.Join(home, ".claude", "rules", "r.md"), "## Decisions\n- never\n")
	writeFixture(t, filepath.Join(home, ".codex", "AGENTS.md"), "## Decisions\n- never\n")
	writeFixture(t, filepath.Join(home, ".grok", "memory", "GEMINI.md"), "## Decisions\n- never\n")
	writeFixture(t, filepath.Join(repo, ".claude", "memory", "CLAUDE.md"), "## Decisions\n- never\n")
	writeFixture(t, filepath.Join(repo, ".claude", "memory", "rules", "r.md"), "## Decisions\n- never\n")
	writeFixture(t, filepath.Join(repo, ".claude", "memory", "instructions", "i.md"), "## Decisions\n- never\n")
	writeFixture(t, filepath.Join(repo, ".claude", "memory", "copilot-instructions.md"), "## Decisions\n- never\n")
	writeFixture(t, filepath.Join(repo, ".claude", "memory", "keep.md"), "## Decisions\n- yes\n")

	files, _, skipped, _, err := Discover(baseOptions(home, repo))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Label != "claude:repo:memory/keep.md" {
		t.Fatalf("files: %+v", labels(files))
	}
	refused := 0
	for _, skip := range skipped {
		if skip.Code == CodeInstructionsRefused {
			refused++
			if strings.Contains(skip.Label, "keep.md") {
				t.Fatalf("keep.md was refused: %+v", skip)
			}
		}
	}
	if refused != 5 {
		t.Fatalf("refused instructions = %d, skips: %+v", refused, skipped)
	}
}

func TestDiscoverMissingRoots(t *testing.T) {
	root := t.TempDir()
	files, sources, skipped, bound, err := Discover(baseOptions(filepath.Join(root, "home"), filepath.Join(root, "repo")))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 || len(skipped) != 0 || bound {
		t.Fatalf("files=%v skipped=%v bound=%v", files, skipped, bound)
	}
	if len(sources) != 3 {
		t.Fatalf("sources: %+v", sources)
	}
	for _, source := range sources {
		if source.Status != "not_found" || source.Files != 0 {
			t.Fatalf("source report: %+v", source)
		}
	}
}

func TestDiscoverPathSafety(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	repo := filepath.Join(root, "repo")
	outside := filepath.Join(root, "outside", "stolen.md")
	writeFixture(t, outside, "## Decisions\n- stolen\n")
	writeFixture(t, filepath.Join(repo, ".claude", "memory", "good.md"), "## Decisions\n- good\n")
	writeFixture(t, filepath.Join(repo, ".claude", "memory", ".hidden.md"), "## Decisions\n- hidden\n")
	writeFixture(t, filepath.Join(repo, ".claude", "memory", ".hidden", "nested.md"), "## Decisions\n- hidden\n")
	writeFixture(t, filepath.Join(repo, ".claude", "memory", ".env"), "SECRET=1\n")
	if err := os.MkdirAll(filepath.Join(repo, ".claude", "memory", "dir.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(repo, ".claude", "memory", "link.md")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	files, _, skipped, _, err := Discover(baseOptions(home, repo))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Label != "claude:repo:memory/good.md" {
		t.Fatalf("files: %+v", labels(files))
	}
	codes := map[string]int{}
	for _, skip := range skipped {
		codes[skip.Code]++
		if strings.Contains(skip.Label, "stolen") {
			t.Fatalf("outside file leaked into skip label: %+v", skip)
		}
	}
	if codes[CodePathUnsafe] < 2 {
		t.Fatalf("path skips: %+v", skipped)
	}
}

func TestDiscoverBounds(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	repo := filepath.Join(root, "repo")
	for _, name := range []string{"a.md", "b.md", "c.md"} {
		writeFixture(t, filepath.Join(repo, ".claude", "memory", name), "## Decisions\n- "+name+"\n")
	}

	opts := baseOptions(home, repo)
	opts.MaxFiles = 2
	files, _, skipped, bound, err := Discover(opts)
	if err != nil {
		t.Fatal(err)
	}
	if !bound || len(files) != 2 {
		t.Fatalf("bound=%v files=%v", bound, labels(files))
	}
	foundBound := false
	for _, skip := range skipped {
		if skip.Code == CodeBound {
			foundBound = true
		}
	}
	if !foundBound {
		t.Fatalf("missing bound skip: %+v", skipped)
	}

	opts = baseOptions(home, repo)
	opts.MaxFileBytes = 8
	files, _, skipped, _, err = Discover(opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Fatalf("large files imported: %v", labels(files))
	}
	foundBound = false
	for _, skip := range skipped {
		if skip.Code == CodeBound {
			foundBound = true
		}
	}
	if !foundBound {
		t.Fatalf("missing per-file bound skip: %+v", skipped)
	}
}
