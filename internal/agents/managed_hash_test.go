package agents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func mustExtractManaged(t *testing.T, content string) (string, ManagedBlockMeta) {
	t.Helper()
	body, meta, ok, legacy := ExtractManagedBlock(content)
	if !ok {
		t.Fatalf("expected a well-ordered managed block:\n%s", content)
	}
	if legacy {
		t.Fatalf("expected valid metadata, got legacy block:\n%s", content)
	}
	return body, meta
}

func findPlan(t *testing.T, res *SyncResult, path string) PlannedWrite {
	t.Helper()
	for _, p := range res.Plan {
		if p.Path == path {
			return p
		}
	}
	t.Fatalf("plan entry for %s not found: %+v", path, res.Plan)
	return PlannedWrite{}
}

func TestHashManagedBodyCanonicalization(t *testing.T) {
	body := "## 0. Safety Rails\n- one\n- two"
	want := HashManagedBody(body)
	if len(want) != 16 {
		t.Fatalf("hash length = %d, want 16: %q", len(want), want)
	}
	for _, r := range want {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			t.Fatalf("hash %q is not lowercase hex", want)
		}
	}

	for name, variant := range map[string]string{
		"trailing newline":      body + "\n",
		"CRLF line endings":     strings.ReplaceAll(body, "\n", "\r\n"),
		"bare CR line endings":  strings.ReplaceAll(body, "\n", "\r"),
		"project H1 alpha":      "# AGENTS.md — alpha\n\n" + body,
		"project H1 beta":       "# AGENTS.md — beta\n\n" + body,
		"project H1 no padding": "# AGENTS.md — gamma\n" + body,
	} {
		if got := HashManagedBody(variant); got != want {
			t.Errorf("%s: hash = %q, want %q", name, got, want)
		}
	}

	for name, variant := range map[string]string{
		"rule change":           "## 0. Safety Rails\n- one\n- three",
		"two trailing newlines": body + "\n\n",
		"trailing space":        "## 0. Safety Rails\n- one\n- two ",
		"leading space":         " ## 0. Safety Rails\n- one\n- two",
		"internal blank line":   "## 0. Safety Rails\n- one\n\n- two",
	} {
		if got := HashManagedBody(variant); got == want {
			t.Errorf("%s must change the hash, got %q", name, got)
		}
	}
}

func TestFormatManagedBlockMetaRoundTrip(t *testing.T) {
	meta := ManagedBlockMeta{Revision: ManagedRevision, Scope: ManagedScopeCore, Hash: strings.Repeat("f", 16)}
	line := FormatManagedBlockMeta(meta)
	if !strings.HasPrefix(line, OwnedBlockMetaPrefix) || !strings.HasSuffix(line, OwnedBlockMetaSuffix) {
		t.Fatalf("formatted metadata is not delimited: %q", line)
	}
	if !strings.HasPrefix(line, OwnedBlockMetaPrefix+"rev=1 scope=core hash=") {
		t.Fatalf("formatted metadata fields are wrong: %q", line)
	}
	parsed, ok := ParseManagedBlockMeta(line)
	if !ok || parsed != meta {
		t.Fatalf("round-trip = %+v ok=%v, want %+v", parsed, ok, meta)
	}
}

func TestParseManagedBlockMeta(t *testing.T) {
	validHash := strings.Repeat("a", 16)
	valid := "<!-- sila:meta rev=1 scope=core hash=" + validHash + " -->"

	meta, ok := ParseManagedBlockMeta(valid)
	if !ok {
		t.Fatalf("valid metadata rejected: %q", valid)
	}
	if meta.Revision != 1 || meta.Scope != ManagedScopeCore || meta.Hash != validHash {
		t.Fatalf("parsed metadata = %+v", meta)
	}

	if meta, ok := ParseManagedBlockMeta("  " + valid + "  "); !ok || meta.Hash != validHash {
		t.Fatalf("surrounding whitespace should be tolerated: %+v ok=%v", meta, ok)
	}

	invalid := []string{
		"<!-- sila:meta rev=1 scope=core -->",
		"<!-- sila:meta rev=1 scope=core hash=short -->",
		"<!-- sila:meta rev=1 scope=core hash=" + strings.Repeat("A", 16) + " -->",
		"<!-- sila:meta rev=1 scope=core hash=" + strings.Repeat("z", 16) + " -->",
		"<!-- sila:meta rev=0 scope=core hash=" + validHash + " -->",
		"<!-- sila:meta rev=x scope=core hash=" + validHash + " -->",
		"<!-- sila:meta rev=1 scope=other hash=" + validHash + " -->",
		"<!-- sila:meta rev=1 scope=core hash=" + validHash + " extra -->",
		"<!-- sila:meta rev=1 scope=core hash=" + validHash,
		"<!-- other rev=1 scope=core hash=" + validHash + " -->",
		"plain text",
	}
	for _, line := range invalid {
		if meta, ok := ParseManagedBlockMeta(line); ok {
			t.Errorf("malformed metadata accepted: %q -> %+v", line, meta)
		}
	}
}

func TestExtractManagedBlock(t *testing.T) {
	managed := "# AGENTS.md — project\n\n## 0. Safety Rails\n- rule one\n"
	content := ComposeAgentsFile("# My Rules\n\nKeep this.\n", managed)

	body, meta, ok, legacy := ExtractManagedBlock(content)
	if !ok || legacy {
		t.Fatalf("ok=%v legacy=%v for composed content:\n%s", ok, legacy, content)
	}
	if meta.Revision != ManagedRevision || meta.Scope != ManagedScopeCore {
		t.Fatalf("metadata = %+v", meta)
	}
	if meta.Hash != HashManagedBody(body) {
		t.Fatalf("metadata hash %q does not match body hash %q", meta.Hash, HashManagedBody(body))
	}
	for _, marker := range []string{OwnedBlockBegin, OwnedBlockEnd, OwnedBlockMetaPrefix} {
		if strings.Contains(body, marker) {
			t.Fatalf("extracted body must exclude %q:\n%s", marker, body)
		}
	}
	if !strings.Contains(body, "## 0. Safety Rails") {
		t.Fatalf("extracted body lost compiled rules:\n%s", body)
	}

	legacyContent := "user\n\n" + OwnedBlockBegin + "\nold compiled body\n" + OwnedBlockEnd + "\n"
	legacyBody, legacyMeta, ok, legacy := ExtractManagedBlock(legacyContent)
	if !ok || !legacy {
		t.Fatalf("legacy block: ok=%v legacy=%v", ok, legacy)
	}
	if legacyMeta != (ManagedBlockMeta{}) {
		t.Fatalf("legacy block returned metadata: %+v", legacyMeta)
	}
	if legacyBody != "old compiled body" {
		t.Fatalf("legacy body = %q", legacyBody)
	}

	malformed := "user\n\n" + OwnedBlockBegin + "\n<!-- sila:meta rev=nope scope=core hash=zzzz -->\nstale body\n" + OwnedBlockEnd + "\n"
	_, _, ok, legacy = ExtractManagedBlock(malformed)
	if !ok || !legacy {
		t.Fatalf("malformed metadata must be treated as legacy: ok=%v legacy=%v", ok, legacy)
	}

	if _, _, ok, legacy := ExtractManagedBlock("no markers here\n"); ok || legacy {
		t.Fatalf("content without markers must not report a block: ok=%v legacy=%v", ok, legacy)
	}

	firstPair := "top\n\n" + OwnedBlockBegin + "\nfirst body\n" + OwnedBlockEnd + "\n\n" + OwnedBlockBegin + "\nsecond body\n" + OwnedBlockEnd + "\n"
	body, _, ok, _ = ExtractManagedBlock(firstPair)
	if !ok || body != "first body" {
		t.Fatalf("multiple pairs must extract only the first: body=%q ok=%v", body, ok)
	}

	crlf := strings.ReplaceAll(ComposeAgentsFile("", managed), "\n", "\r\n")
	body, meta, ok, legacy = ExtractManagedBlock(crlf)
	if !ok || legacy {
		t.Fatalf("CRLF block: ok=%v legacy=%v", ok, legacy)
	}
	if meta.Hash != HashManagedBody(body) {
		t.Fatalf("CRLF metadata hash %q does not match body hash %q", meta.Hash, HashManagedBody(body))
	}
}

func TestComposeAgentsFileEmitsSingleMetadataLine(t *testing.T) {
	managed := "# AGENTS.md — project\n\n## 0. Safety Rails\n- rule one\n"
	content := ""
	for run := 0; run < 3; run++ {
		content = ComposeAgentsFile(content, managed)
		if got := strings.Count(content, OwnedBlockBegin); got != 1 {
			t.Fatalf("run %d: begin markers = %d, want 1:\n%s", run+1, got, content)
		}
		if got := strings.Count(content, OwnedBlockEnd); got != 1 {
			t.Fatalf("run %d: end markers = %d, want 1:\n%s", run+1, got, content)
		}
		if got := strings.Count(content, OwnedBlockMetaPrefix); got != 1 {
			t.Fatalf("run %d: metadata lines = %d, want 1:\n%s", run+1, got, content)
		}
	}
	_, meta := mustExtractManaged(t, content)
	if meta.Revision != 1 || meta.Scope != "core" {
		t.Fatalf("metadata = %+v", meta)
	}
}

func TestComposeAgentsFileMigratesLegacyBlockOnce(t *testing.T) {
	legacy := "# Mine\n\nCustom rule.\n\n" + OwnedBlockBegin + "\nold compiled body\n" + OwnedBlockEnd + "\n"
	managed := "# AGENTS.md — project\n\n## 0. Safety Rails\n- fresh rule\n"

	first := ComposeAgentsFile(legacy, managed)
	if !strings.Contains(first, "Custom rule.") {
		t.Fatalf("user content lost during migration:\n%s", first)
	}
	if strings.Contains(first, "old compiled body") {
		t.Fatalf("legacy body survived migration:\n%s", first)
	}
	if strings.Count(first, OwnedBlockMetaPrefix) != 1 {
		t.Fatalf("expected exactly one metadata line:\n%s", first)
	}
	second := ComposeAgentsFile(first, managed)
	if first != second {
		t.Fatalf("migration not idempotent:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

func TestComposeAgentsFilePreservesMultipleMarkerPairs(t *testing.T) {
	existing := "top\n\n" + OwnedBlockBegin + "\nold body\n" + OwnedBlockEnd +
		"\n\nmiddle\n\n" + OwnedBlockBegin + "\nsecond block body\n" + OwnedBlockEnd + "\n"
	managed := "# AGENTS.md — project\n\n## 0. Safety Rails\n- fresh rule\n"

	got := ComposeAgentsFile(existing, managed)
	if strings.Contains(got, "old body") {
		t.Fatalf("owned first pair was not replaced:\n%s", got)
	}
	for _, want := range []string{"top", "middle", "second block body", "- fresh rule"} {
		if strings.Count(got, want) != 1 {
			t.Fatalf("content %q count = %d, want 1:\n%s", want, strings.Count(got, want), got)
		}
	}
	if n := strings.Count(got, OwnedBlockBegin); n != 2 {
		t.Fatalf("begin markers = %d, want 2 (preserved pair + new block):\n%s", n, got)
	}
	if n := strings.Count(got, OwnedBlockMetaPrefix); n != 1 {
		t.Fatalf("metadata lines = %d, want 1:\n%s", n, got)
	}
}

func TestDeployTargetsHashSkip(t *testing.T) {
	tmp := t.TempDir()
	opts := CompileOptions{RepoRoot: tmp, ProjectName: "hash-skip", Stack: "go"}

	first, err := DeployTargets(tmp, opts, []string{"agents"})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.FilesWritten) != 1 || first.FilesWritten[0] != "AGENTS.md" {
		t.Fatalf("first sync writes = %v", first.FilesWritten)
	}
	if len(first.ManagedHash) != 16 || first.ManagedRevision != 1 {
		t.Fatalf("first sync managed fields = %q rev=%d", first.ManagedHash, first.ManagedRevision)
	}
	content := readString(t, filepath.Join(tmp, "AGENTS.md"))

	second, err := DeployTargets(tmp, opts, []string{"agents"})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.FilesWritten) != 0 {
		t.Fatalf("second sync wrote files: %v", second.FilesWritten)
	}
	if second.HashSkips != 1 {
		t.Fatalf("hash skips = %d, want 1", second.HashSkips)
	}
	if second.ManagedHash != first.ManagedHash {
		t.Fatalf("managed hash changed: %q -> %q", first.ManagedHash, second.ManagedHash)
	}
	plan := findPlan(t, second, "AGENTS.md")
	if plan.Action != "skip" {
		t.Fatalf("action = %q, want skip: %+v", plan.Action, plan)
	}
	if plan.Detail != "managed hash match (rev=1)" {
		t.Fatalf("detail = %q", plan.Detail)
	}
	if !plan.HashMatch || plan.ManagedHash != first.ManagedHash || plan.ExistingManagedHash != first.ManagedHash {
		t.Fatalf("plan managed fields = %+v", plan)
	}
	if got := readString(t, filepath.Join(tmp, "AGENTS.md")); got != content {
		t.Fatal("hash skip must not modify AGENTS.md")
	}
}

func TestDeployTargetsUserOnlyEditKeepsManagedHash(t *testing.T) {
	tmp := t.TempDir()
	opts := CompileOptions{RepoRoot: tmp, ProjectName: "user-edit", Stack: "go"}
	userContent := "# Team Rules\n\nKeep this.\n"
	if err := os.WriteFile(filepath.Join(tmp, "AGENTS.md"), []byte(userContent), 0o644); err != nil {
		t.Fatal(err)
	}

	first, err := DeployTargets(tmp, opts, []string{"agents"})
	if err != nil {
		t.Fatal(err)
	}
	before := readString(t, filepath.Join(tmp, "AGENTS.md"))
	_, metaBefore := mustExtractManaged(t, before)

	edited := strings.Replace(before, "Keep this.", "Keep this and that.", 1)
	if err := os.WriteFile(filepath.Join(tmp, "AGENTS.md"), []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}

	second, err := DeployTargets(tmp, opts, []string{"agents"})
	if err != nil {
		t.Fatal(err)
	}
	if second.ManagedHash != first.ManagedHash {
		t.Fatalf("user-only edit changed the managed hash: %q -> %q", first.ManagedHash, second.ManagedHash)
	}
	after := readString(t, filepath.Join(tmp, "AGENTS.md"))
	_, metaAfter := mustExtractManaged(t, after)
	if metaAfter.Hash != metaBefore.Hash {
		t.Fatalf("managed metadata hash changed: %q -> %q", metaBefore.Hash, metaAfter.Hash)
	}
	if !strings.Contains(after, "Keep this and that.") {
		t.Fatalf("user edit lost:\n%s", after)
	}
	if second.HashSkips != 1 {
		t.Fatalf("hash skips = %d, want 1 (block unchanged, file already normalized)", second.HashSkips)
	}
	if len(second.FilesWritten) != 0 {
		t.Fatalf("user-only edit should not write: %v", second.FilesWritten)
	}
}

func TestDeployTargetsRuleChangeChangesHashAndRewrites(t *testing.T) {
	tmp := t.TempDir()
	goOpts := CompileOptions{RepoRoot: tmp, ProjectName: "rule-change", Stack: "go"}
	first, err := DeployTargets(tmp, goOpts, []string{"agents"})
	if err != nil {
		t.Fatal(err)
	}

	pythonOpts := goOpts
	pythonOpts.Stack = "python"
	second, err := DeployTargets(tmp, pythonOpts, []string{"agents"})
	if err != nil {
		t.Fatal(err)
	}
	if second.ManagedHash == first.ManagedHash {
		t.Fatalf("stack rule change did not change the hash: %q", first.ManagedHash)
	}
	if len(second.FilesWritten) != 1 || second.FilesWritten[0] != "AGENTS.md" {
		t.Fatalf("rule change writes = %v", second.FilesWritten)
	}
	plan := findPlan(t, second, "AGENTS.md")
	if plan.Action != "merge" || plan.HashMatch {
		t.Fatalf("rule change plan = %+v", plan)
	}
	if _, meta := mustExtractManaged(t, readString(t, filepath.Join(tmp, "AGENTS.md"))); meta.Hash != second.ManagedHash {
		t.Fatalf("written metadata hash = %q, want %q", meta.Hash, second.ManagedHash)
	}
}

func TestDeployTargetsLegacyBlockMigratesOnce(t *testing.T) {
	tmp := t.TempDir()
	legacy := "# Custom\n\nPreserve me.\n\n" + OwnedBlockBegin + "\nstale compiled rules\n" + OwnedBlockEnd + "\n"
	if err := os.WriteFile(filepath.Join(tmp, "AGENTS.md"), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	opts := CompileOptions{RepoRoot: tmp, ProjectName: "legacy", Stack: "go"}

	first, err := DeployTargets(tmp, opts, []string{"agents"})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.FilesWritten) != 1 {
		t.Fatalf("legacy migration writes = %v", first.FilesWritten)
	}
	migrated := readString(t, filepath.Join(tmp, "AGENTS.md"))
	if strings.Contains(migrated, "stale compiled rules") {
		t.Fatalf("legacy body survived:\n%s", migrated)
	}
	if !strings.Contains(migrated, "Preserve me.") {
		t.Fatalf("user content lost:\n%s", migrated)
	}
	if strings.Count(migrated, OwnedBlockMetaPrefix) != 1 {
		t.Fatalf("metadata lines = %d, want 1:\n%s", strings.Count(migrated, OwnedBlockMetaPrefix), migrated)
	}

	second, err := DeployTargets(tmp, opts, []string{"agents"})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.FilesWritten) != 0 || second.HashSkips != 1 {
		t.Fatalf("second run must be a no-op: writes=%v skips=%d", second.FilesWritten, second.HashSkips)
	}
	if got := readString(t, filepath.Join(tmp, "AGENTS.md")); got != migrated {
		t.Fatal("legacy migration is not idempotent")
	}
}

func TestDeployTargetsMalformedMetadataMigrates(t *testing.T) {
	tmp := t.TempDir()
	broken := "# Custom\n\nPreserve me.\n\n" + OwnedBlockBegin +
		"\n<!-- sila:meta rev=1 scope=unknown hash=0123456789abcdef -->\nstale body\n" + OwnedBlockEnd + "\n"
	if err := os.WriteFile(filepath.Join(tmp, "AGENTS.md"), []byte(broken), 0o644); err != nil {
		t.Fatal(err)
	}
	opts := CompileOptions{RepoRoot: tmp, ProjectName: "malformed", Stack: "go"}

	res, err := DeployTargets(tmp, opts, []string{"agents"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.FilesWritten) != 1 {
		t.Fatalf("malformed metadata was not migrated: %v", res.FilesWritten)
	}
	got := readString(t, filepath.Join(tmp, "AGENTS.md"))
	if strings.Contains(got, "stale body") || strings.Contains(got, "scope=unknown") {
		t.Fatalf("malformed block survived:\n%s", got)
	}
	if !strings.Contains(got, "Preserve me.") {
		t.Fatalf("user content lost:\n%s", got)
	}
	if _, meta := mustExtractManaged(t, got); meta.Revision != 1 || meta.Scope != "core" {
		t.Fatalf("migrated metadata = %+v", meta)
	}
}

func TestDeployTargetsHashSkipKeepsOtherRepairs(t *testing.T) {
	tmp := t.TempDir()
	if err := os.Mkdir(filepath.Join(tmp, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	opts := CompileOptions{RepoRoot: tmp, ProjectName: "repairs", Stack: "go"}

	if _, err := DeployTargets(tmp, opts, []string{"agents"}); err != nil {
		t.Fatal(err)
	}

	res, err := DeployTargets(tmp, opts, []string{"all"})
	if err != nil {
		t.Fatal(err)
	}
	if res.HashSkips != 1 {
		t.Fatalf("hash skips = %d, want 1", res.HashSkips)
	}
	if plan := findPlan(t, res, "AGENTS.md"); plan.Action != "skip" {
		t.Fatalf("AGENTS.md action = %q, want skip", plan.Action)
	}
	if plan := findPlan(t, res, "CLAUDE.md"); plan.Action != "symlink" {
		t.Fatalf("CLAUDE.md action = %q, want symlink: %+v", plan.Action, plan)
	}
	if len(res.Symlinks) != 1 || res.Symlinks[0] != "CLAUDE.md -> AGENTS.md" {
		t.Fatalf("symlink repairs suppressed by hash skip: %v", res.Symlinks)
	}
	fi, err := os.Lstat(filepath.Join(tmp, "CLAUDE.md"))
	if err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("CLAUDE.md symlink not created (err=%v)", err)
	}
}

func TestDeployTargetsDryRunHashParity(t *testing.T) {
	tmp := t.TempDir()
	legacy := "# Custom\n\nPreserve me.\n\n" + OwnedBlockBegin + "\nstale\n" + OwnedBlockEnd + "\n"
	if err := os.WriteFile(filepath.Join(tmp, "AGENTS.md"), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	opts := CompileOptions{RepoRoot: tmp, ProjectName: "dry-run", Stack: "go", Preview: true}

	preview, err := DeployTargets(tmp, opts, []string{"agents"})
	if err != nil {
		t.Fatal(err)
	}
	if got := readString(t, filepath.Join(tmp, "AGENTS.md")); got != legacy {
		t.Fatal("dry-run modified AGENTS.md")
	}
	previewPlan := findPlan(t, preview, "AGENTS.md")
	if previewPlan.Action != "merge" || len(previewPlan.ManagedHash) != 16 {
		t.Fatalf("preview plan = %+v", previewPlan)
	}

	apply := opts
	apply.Preview = false
	real, err := DeployTargets(tmp, apply, []string{"agents"})
	if err != nil {
		t.Fatal(err)
	}
	realPlan := findPlan(t, real, "AGENTS.md")
	if realPlan.Action != previewPlan.Action || realPlan.ManagedHash != previewPlan.ManagedHash {
		t.Fatalf("dry-run decision diverged: preview=%+v real=%+v", previewPlan, realPlan)
	}

	steady, err := DeployTargets(tmp, opts, []string{"agents"})
	if err != nil {
		t.Fatal(err)
	}
	steadyPlan := findPlan(t, steady, "AGENTS.md")
	if steadyPlan.Action != "skip" || !steadyPlan.HashMatch || steadyPlan.Detail != "managed hash match (rev=1)" {
		t.Fatalf("steady-state dry-run plan = %+v", steadyPlan)
	}
	if steadyPlan.ManagedHash != previewPlan.ManagedHash {
		t.Fatalf("steady-state hash = %q, want %q", steadyPlan.ManagedHash, previewPlan.ManagedHash)
	}
}

func TestHashManagedBodyIgnoresProjectTitle(t *testing.T) {
	alpha := "# AGENTS.md — alpha\n\n## 0. Safety Rails\n- same rule\n"
	beta := "# AGENTS.md — beta\n\n## 0. Safety Rails\n- same rule\n"
	if HashManagedBody(alpha) != HashManagedBody(beta) {
		t.Fatal("project-specific H1 must not affect the managed fingerprint")
	}
}
