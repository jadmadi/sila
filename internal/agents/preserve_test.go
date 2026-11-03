package agents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDeployTargetsPreservesCustomInstructions reproduces the codex-eval F04
// fixture: a repository with ordinary custom AGENTS.md and CLAUDE.md must
// lose nothing when sila setup runs.
func TestDeployTargetsPreservesCustomInstructions(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	agentsCustom := "# My Custom Agents Instructions\n\nAlways answer in French. Never touch the Makefile.\n"
	claudeCustom := "# My CLAUDE rules\n\nPrefer table-driven tests.\n"
	if err := os.WriteFile(filepath.Join(tmp, "AGENTS.md"), []byte(agentsCustom), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "CLAUDE.md"), []byte(claudeCustom), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := DeployTargets(tmp, CompileOptions{RepoRoot: tmp, ProjectName: "atlas"}, []string{"claude"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Errors) > 0 {
		t.Fatalf("unexpected errors: %v", res.Errors)
	}

	// AGENTS.md keeps the custom content and gains the sila block.
	data, err := os.ReadFile(filepath.Join(tmp, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "Always answer in French. Never touch the Makefile.") {
		t.Fatalf("custom AGENTS.md content destroyed: %s", content)
	}
	if !strings.Contains(content, OwnedBlockBegin) || !strings.Contains(content, "Safety Rails") {
		t.Fatalf("sila owned block missing: %s", content)
	}

	// CLAUDE.md is left untouched without --replace.
	claudeData, err := os.ReadFile(filepath.Join(tmp, "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(claudeData) != claudeCustom {
		t.Fatalf("existing CLAUDE.md was modified without --replace: %s", claudeData)
	}
	for _, p := range res.Plan {
		if p.Path == "CLAUDE.md" && p.Action != "skip" {
			t.Fatalf("expected skip plan for CLAUDE.md, got %+v", p)
		}
	}
}

// TestDeployTargetsReplaceAuthorizesDestruction verifies that only --replace
// removes an existing user CLAUDE.md.
func TestDeployTargetsReplaceAuthorizesDestruction(t *testing.T) {
	tmp := t.TempDir()
	claudeCustom := "# User rules\nDo not modify.\n"
	if err := os.WriteFile(filepath.Join(tmp, "AGENTS.md"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "CLAUDE.md"), []byte(claudeCustom), 0o644); err != nil {
		t.Fatal(err)
	}

	// Without --replace: untouched. With --replace: symlinked.
	if _, err := DeployTargets(tmp, CompileOptions{RepoRoot: tmp}, []string{"claude"}); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(filepath.Join(tmp, "CLAUDE.md")); err != nil || fi.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("CLAUDE.md changed without --replace (err=%v)", err)
	}

	if _, err := DeployTargets(tmp, CompileOptions{RepoRoot: tmp, Replace: true}, []string{"claude"}); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Lstat(filepath.Join(tmp, "CLAUDE.md"))
	if err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("--replace did not install symlink (err=%v)", err)
	}
}

// TestDeployTargetsPreviewWritesNothing verifies preview and execution share
// the same plan while preview mutates nothing.
func TestDeployTargetsPreviewWritesNothing(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "AGENTS.md"), []byte("# custom\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := DeployTargets(tmp, CompileOptions{RepoRoot: tmp, Preview: true}, []string{"all"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Preview || len(res.Plan) == 0 {
		t.Fatalf("expected a non-empty preview plan: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(tmp, ".cursor", "rules")); !os.IsNotExist(err) {
		t.Fatal("preview must not create files")
	}
	claudeExists := true
	if _, err := os.Lstat(filepath.Join(tmp, "CLAUDE.md")); os.IsNotExist(err) {
		claudeExists = false
	}
	if claudeExists {
		t.Fatal("preview must not create CLAUDE.md")
	}
	foundAgentsPlan := false
	for _, p := range res.Plan {
		if p.Path == "AGENTS.md" {
			foundAgentsPlan = true
			if p.Diff == "" {
				t.Fatal("AGENTS.md preview plan missing diff")
			}
		}
	}
	if !foundAgentsPlan {
		t.Fatal("AGENTS.md missing from preview plan")
	}

	// Execution produces the planned merge.
	if _, err := DeployTargets(tmp, CompileOptions{RepoRoot: tmp}, []string{"all"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(tmp, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "# custom") {
		t.Fatalf("planned user content not preserved on execution: %s", data)
	}
}

// TestExtractUserContentStripsManagedMetadata pins the ownership rule for the
// fingerprint line: it is sila content in every position and must never leak
// into preserved user text.
func TestExtractUserContentStripsManagedMetadata(t *testing.T) {
	metaLine := "<!-- sila:meta rev=1 scope=core hash=0123456789abcdef -->"

	unpaired := "mine\n" + metaLine + "\nmore of mine\n"
	got := ExtractUserContent(unpaired)
	if strings.Contains(got, OwnedBlockMetaPrefix) {
		t.Fatalf("metadata leaked into unpaired user content: %q", got)
	}
	if !strings.Contains(got, "mine") || !strings.Contains(got, "more of mine") {
		t.Fatalf("user content lost: %q", got)
	}

	paired := "mine\n\n" + OwnedBlockBegin + "\nbody\n" + OwnedBlockEnd + "\n" + metaLine + "\nafter\n"
	got = ExtractUserContent(paired)
	if strings.Contains(got, OwnedBlockMetaPrefix) {
		t.Fatalf("metadata leaked into paired user content: %q", got)
	}
	for _, want := range []string{"mine", "after"} {
		if !strings.Contains(got, want) {
			t.Fatalf("user content %q lost: %q", want, got)
		}
	}
}

// TestComposeAgentsFileMetadataInsideBlock verifies the metadata line is the
// first content line of the owned block and never appears above it.
func TestComposeAgentsFileMetadataInsideBlock(t *testing.T) {
	composed := ComposeAgentsFile("# My Rules\n\nKeep this.\n", "# AGENTS.md — p\n\n## 0. Safety Rails\n- rule\n")
	begin := strings.Index(composed, OwnedBlockBegin)
	meta := strings.Index(composed, OwnedBlockMetaPrefix)
	if begin < 0 || meta < begin {
		t.Fatalf("metadata line must follow the begin marker:\n%s", composed)
	}
	between := composed[begin+len(OwnedBlockBegin) : meta]
	if strings.TrimSpace(between) != "" {
		t.Fatalf("metadata must be the first content line, found %q", between)
	}
	if strings.Index(composed[:begin], OwnedBlockMetaPrefix) >= 0 {
		t.Fatalf("metadata leaked above the owned block:\n%s", composed)
	}
	if !strings.Contains(composed, "Keep this.") {
		t.Fatalf("user content lost:\n%s", composed)
	}
}

// TestDeployTargetsIdempotent verifies that repeated setup converges without
// duplicating the sila block or user content.
func TestDeployTargetsIdempotent(t *testing.T) {
	tmp := t.TempDir()
	opts := CompileOptions{RepoRoot: tmp, ProjectName: "idem"}
	if _, err := DeployTargets(tmp, opts, []string{}); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(filepath.Join(tmp, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DeployTargets(tmp, opts, []string{}); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(filepath.Join(tmp, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatalf("setup not idempotent:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
	if got := strings.Count(string(second), OwnedBlockBegin); got != 1 {
		t.Fatalf("expected exactly one owned block, got %d", got)
	}
}

// TestExtractUserContentPreservesForeignFiles pins the migration contract
// across file generations.
func TestExtractUserContentPreservesForeignFiles(t *testing.T) {
	foreign := "# Totally user-owned\n\nRules here.\n"
	if got := ExtractUserContent(foreign); strings.TrimRight(got, "\n") != strings.TrimRight(foreign, "\n") {
		t.Fatalf("foreign content not preserved verbatim: %q", got)
	}

	marked := "mine\n\n" + OwnedBlockBegin + "\nsila body\n" + OwnedBlockEnd + "\n\npost\n"
	got := ExtractUserContent(marked)
	if !strings.Contains(got, "mine") || !strings.Contains(got, "post") || strings.Contains(got, "sila body") {
		t.Fatalf("marker extraction wrong: %q", got)
	}
}

// TestUnifiedDiffSanity keeps the preview diff honest.
func TestUnifiedDiffSanity(t *testing.T) {
	if d := unifiedDiff("same\n", "same\n", "f"); d != "" {
		t.Fatalf("identical inputs must produce no diff, got %q", d)
	}
	d := unifiedDiff("a\nb\nc\n", "a\nx\nc\n", "f.md")
	if !strings.Contains(d, "-b") || !strings.Contains(d, "+x") || !strings.Contains(d, "a/f.md") {
		t.Fatalf("diff missing change lines: %q", d)
	}
}

// TestDeployTargetsLegacyLayoutIsIdempotent keeps a legacy-layout file stable
// across repeated setup: the old overlay text must survive every run and never
// be duplicated.
func TestDeployTargetsLegacyLayoutIsIdempotent(t *testing.T) {
	tmp := t.TempDir()
	legacy := `# AGENTS.md — myproj

## 0. Hard Prohibitions (NEVER Do This)
- Zero GitHub Workflows / CI Sprawl

## Project-Specific Overrides & Context

- Rule: never touch the Makefile.
- Preference: always run the linter before commit.
`
	if err := os.WriteFile(filepath.Join(tmp, "AGENTS.md"), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	opts := CompileOptions{RepoRoot: tmp, ProjectName: "myproj"}

	if _, err := DeployTargets(tmp, opts, []string{"agents"}); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(filepath.Join(tmp, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(first), "never touch the Makefile") {
		t.Fatalf("run 1 lost the legacy overlay: %s", first)
	}

	if _, err := DeployTargets(tmp, opts, []string{"agents"}); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(filepath.Join(tmp, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"never touch the Makefile", "always run the linter before commit"} {
		if !strings.Contains(string(second), want) {
			t.Fatalf("run 2 lost user content %q:\n%s", want, second)
		}
	}
	if strings.Count(string(second), "never touch the Makefile") != 1 {
		t.Fatalf("legacy overlay duplicated:\n%s", second)
	}

	// A third run must be stable.
	if _, err := DeployTargets(tmp, opts, []string{"agents"}); err != nil {
		t.Fatal(err)
	}
	third, _ := os.ReadFile(filepath.Join(tmp, "AGENTS.md"))
	if string(second) != string(third) {
		t.Fatalf("not idempotent:\nsecond:\n%s\nthird:\n%s", second, third)
	}
}

// TestDeployTargetsPreservesLegacyPrefix is the re-eval F04 reproduction: a
// familiar heading does not prove ownership, so custom instructions above a
// legacy "Project-Specific Overrides" heading must survive setup too.
func TestDeployTargetsPreservesLegacyPrefix(t *testing.T) {
	tmp := t.TempDir()
	legacy := `# My Team Rules

Always deploy on Fridays. Never rename the legacy tables.
The migration notes below are ours as well.

## Project-Specific Overrides & Context

- Rule: never touch the Makefile.
`
	if err := os.WriteFile(filepath.Join(tmp, "AGENTS.md"), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	opts := CompileOptions{RepoRoot: tmp, ProjectName: "keep"}
	if _, err := DeployTargets(tmp, opts, []string{"agents"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(tmp, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	for _, want := range []string{
		"Always deploy on Fridays.",
		"Never rename the legacy tables.",
		"The migration notes below are ours as well.",
		"never touch the Makefile",
		OwnedBlockBegin,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("content above/below the legacy heading lost %q:\n%s", want, got)
		}
	}

	// Second run is stable — the preserved file is user content now.
	if _, err := DeployTargets(tmp, opts, []string{"agents"}); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(filepath.Join(tmp, "AGENTS.md"))
	if got != string(second) {
		t.Fatal("setup not idempotent after legacy preservation")
	}
	if strings.Count(string(second), "Always deploy on Fridays.") != 1 {
		t.Fatal("preserved prefix duplicated across runs")
	}
}

// TestDeployTargetsStrayMarkersConverge guards idempotency for a file whose
// markers are not a well-ordered pair: the earlier code treated it as user
// content and appended a fresh block on every run, growing the file without
// bound (observed 632 -> 1237 -> 1842 bytes).
func TestDeployTargetsStrayMarkersConverge(t *testing.T) {
	tmp := t.TempDir()
	// A stray end marker before any begin: not a managed pair.
	broken := "# My rules\n\n" + OwnedBlockEnd + "\n\nMore of my rules.\n"
	path := filepath.Join(tmp, "AGENTS.md")
	if err := os.WriteFile(path, []byte(broken), 0o644); err != nil {
		t.Fatal(err)
	}
	opts := CompileOptions{RepoRoot: tmp, ProjectName: "stray"}

	if _, err := DeployTargets(tmp, opts, []string{"agents"}); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(first), "My rules") {
		t.Fatalf("user content lost: %s", first)
	}

	for run := 2; run <= 3; run++ {
		if _, err := DeployTargets(tmp, opts, []string{"agents"}); err != nil {
			t.Fatal(err)
		}
		next, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if len(next) != len(first) {
			t.Fatalf("run %d grew the file: %d -> %d bytes (setup must converge)", run, len(first), len(next))
		}
		if string(next) != string(first) {
			t.Fatalf("run %d produced different content", run)
		}
	}
	if got := strings.Count(string(first), OwnedBlockBegin); got != 1 {
		t.Fatalf("expected exactly one owned block, got %d", got)
	}
}
