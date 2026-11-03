package agents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectDriftManagedHashMatch(t *testing.T) {
	tmp := t.TempDir()
	opts := CompileOptions{RepoRoot: tmp, ProjectName: "drift-match", Stack: "go"}
	if _, err := DeployTargets(tmp, opts, []string{"agents"}); err != nil {
		t.Fatal(err)
	}

	report, err := DetectDrift(tmp, opts)
	if err != nil {
		t.Fatal(err)
	}
	if report.HasDrift {
		t.Fatalf("expected no drift, details: %v", report.Details)
	}
	if !report.HashMatch || report.LegacyBlock {
		t.Fatalf("hash match = %v, legacy = %v", report.HashMatch, report.LegacyBlock)
	}
	if report.ManagedRevision != 1 {
		t.Fatalf("managed revision = %d, want 1", report.ManagedRevision)
	}
	if len(report.ManagedHash) != 16 || report.ManagedHash != report.HubManagedHash {
		t.Fatalf("managed hash = %q, hub = %q", report.ManagedHash, report.HubManagedHash)
	}
	if report.CoreHubHash == "" || report.CurrentHash == "" {
		t.Fatalf("legacy hashes must remain populated: core=%q current=%q", report.CoreHubHash, report.CurrentHash)
	}
}

func TestDetectDriftManagedHashMismatch(t *testing.T) {
	tmp := t.TempDir()
	opts := CompileOptions{RepoRoot: tmp, ProjectName: "drift-mismatch", Stack: "go"}
	if _, err := DeployTargets(tmp, opts, []string{"agents"}); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(tmp, "AGENTS.md")
	content := readString(t, path)
	_, meta := mustExtractManaged(t, content)
	replacement := strings.Repeat("0", 16)
	if meta.Hash == replacement {
		replacement = strings.Repeat("1", 16)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(content, "hash="+meta.Hash, "hash="+replacement, 1)), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := DetectDrift(tmp, opts)
	if err != nil {
		t.Fatal(err)
	}
	if !report.HasDrift || report.HashMatch || report.LegacyBlock {
		t.Fatalf("mismatch report: has_drift=%v hash_match=%v legacy=%v", report.HasDrift, report.HashMatch, report.LegacyBlock)
	}
	if report.ManagedHash != replacement || report.HubManagedHash == replacement {
		t.Fatalf("managed hash = %q, hub = %q", report.ManagedHash, report.HubManagedHash)
	}
}

func TestDetectDriftLegacyBlock(t *testing.T) {
	tmp := t.TempDir()
	legacy := "# Custom\n\n" + OwnedBlockBegin + "\n## 0. Safety Rails (NEVER Do This)\n- rule\n" + OwnedBlockEnd + "\n"
	if err := os.WriteFile(filepath.Join(tmp, "AGENTS.md"), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := DetectDrift(tmp, CompileOptions{RepoRoot: tmp, ProjectName: "drift-legacy", Stack: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if !report.LegacyBlock {
		t.Fatal("expected legacy_block=true")
	}
	if report.HashMatch {
		t.Fatal("legacy block must not report a hash match")
	}
	if report.ManagedHash != "" || report.ManagedRevision != 0 {
		t.Fatalf("legacy block reported fingerprint: hash=%q rev=%d", report.ManagedHash, report.ManagedRevision)
	}
	if !report.HasDrift {
		t.Fatal("legacy block must be surfaced as drift so sync migrates it")
	}
}

func TestDetectDriftUserContentOnlyChange(t *testing.T) {
	tmp := t.TempDir()
	opts := CompileOptions{RepoRoot: tmp, ProjectName: "drift-user", Stack: "go"}
	if _, err := DeployTargets(tmp, opts, []string{"agents"}); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(tmp, "AGENTS.md")
	content := readString(t, path)
	edited := "# Team Notes\n\nKeep this local note.\n\n" + content
	if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := DetectDrift(tmp, opts)
	if err != nil {
		t.Fatal(err)
	}
	if !report.HashMatch {
		t.Fatalf("user content change drifted the managed hash: %+v", report)
	}
	if report.HasDrift {
		t.Fatalf("user content outside the block must not drift: %v", report.Details)
	}
}

func TestDetectDriftHubManagedHashIsProjectIndependent(t *testing.T) {
	first := t.TempDir()
	second := t.TempDir()

	firstReport, err := DetectDrift(first, CompileOptions{RepoRoot: first, ProjectName: "alpha", Stack: "go"})
	if err != nil {
		t.Fatal(err)
	}
	secondReport, err := DetectDrift(second, CompileOptions{RepoRoot: second, ProjectName: "beta", Stack: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if firstReport.HubManagedHash != secondReport.HubManagedHash {
		t.Fatalf("hub managed hash depends on project name: %q vs %q", firstReport.HubManagedHash, secondReport.HubManagedHash)
	}
}

func TestDetectDriftMissingManagedBlock(t *testing.T) {
	tmp := t.TempDir()
	foreign := "# Custom rules\n\n## 0. Safety Rails\n- rule\n"
	if err := os.WriteFile(filepath.Join(tmp, "AGENTS.md"), []byte(foreign), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := DetectDrift(tmp, CompileOptions{RepoRoot: tmp, ProjectName: "no-block", Stack: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if report.LegacyBlock {
		t.Fatal("a file without markers is not a legacy block")
	}
	if report.HashMatch {
		t.Fatal("a file without markers cannot hash-match")
	}
	if report.ManagedHash != "" {
		t.Fatalf("managed hash = %q, want empty", report.ManagedHash)
	}
	if report.HubManagedHash == "" {
		t.Fatal("hub managed hash must always be computed")
	}
}
