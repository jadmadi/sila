package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeOutputReplacesPathsIDsTimestampsSHAsAndANSI(t *testing.T) {
	raw := "\x1b[32m✔\x1b[0m Repo /tmp/sila-audit-sandbox-12345/repo and home /tmp/sila-audit-sandbox-12345/home\n" +
		"Root /tmp/sila-audit-sandbox-12345 at 2026-10-23T08:45:12.123Z on 2026-10-25 in 43.2ms for repo-41686647\n" +
		"Fact fact_9cda6455 handoff hoff_065015888fd4 commit a1b2c3d4e5f60718 uuid 1ebe8d9e-742e-4de3-923b-da7d37a3fbdf\n"

	got := NormalizeOutput(
		raw,
		"/tmp/sila-audit-sandbox-12345",
		"/tmp/sila-audit-sandbox-12345/home",
		"/tmp/sila-audit-sandbox-12345/repo",
	)

	if strings.Contains(got, "/tmp/sila-audit-sandbox-12345") {
		t.Fatalf("sandbox path leaked in normalized output:\n%s", got)
	}
	if strings.Contains(got, "\x1b[") {
		t.Fatalf("ANSI escape leaked in normalized output:\n%s", got)
	}
	for _, want := range []string{
		"<sandbox>/repo",
		"<sandbox>/home",
		"<sandbox>",
		"<timestamp>",
		"<date-iso>",
		"repo-<sha>",
		"<duration>",
		"<id>",
		"<sha>",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected token %q in normalized output, got:\n%s", want, got)
		}
	}
}

func TestSurfaceManifestCompletenessAndDenylistEnforcement(t *testing.T) {
	canonical := []string{"salam", "goals", "memo", "lesson", "case", "decision", "onboard", "agents", "search", "handoff", "gate", "sync", "ui"}
	specs := SurfaceManifest(canonical)
	cov, err := ValidateSurfaceManifest(specs, canonical)
	if err != nil {
		t.Fatalf("ValidateSurfaceManifest failed: %v", err)
	}
	if len(cov.MissingCommands) != 0 {
		t.Fatalf("unexpected missing commands: %v", cov.MissingCommands)
	}

	// Verify missing canonical command fails validation
	if _, err := ValidateSurfaceManifest(specs, append(canonical, "missing-canonical-cmd")); err == nil {
		t.Fatalf("expected error when a canonical command is missing from manifest")
	}

	// Verify denylisted command without --help or --dry-run fails validation
	unsafeSpecs := append([]SurfaceSpec(nil), specs...)
	unsafeSpecs = append(unsafeSpecs, SurfaceSpec{
		Group:   "unsafe",
		Label:   "unsafe__sync",
		Command: "sync",
		Argv:    []string{"sync", "--full"},
		Mode:    "human",
	})
	if _, err := ValidateSurfaceManifest(unsafeSpecs, canonical); err == nil {
		t.Fatalf("expected error when denylisted command is invoked without --help or --dry-run")
	}
}

func TestHarnessDeterminismAndHomeIsolation(t *testing.T) {
	repoRoot, err := resolveRepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	silaBin, cleanup, err := ensureSilaBinary(repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	if cleanup != nil {
		defer cleanup()
	}

	// Set a fake HOME with a sentinel file to prove the harness never writes to the caller's HOME/DB
	callerHome := t.TempDir()
	sentinel := filepath.Join(callerHome, "sentinel.txt")
	if err := os.WriteFile(sentinel, []byte("untouched\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", callerHome)
	t.Setenv("SILA_DB", filepath.Join(callerHome, "caller-sila.db"))

	out1 := t.TempDir()
	out2 := t.TempDir()

	m1, err := RunHarness(HarnessOptions{
		RepoRoot:    repoRoot,
		SilaBin:     silaBin,
		OutDir:      out1,
		GroupFilter: "goals",
	})
	if err != nil {
		t.Fatalf("RunHarness 1 failed: %v", err)
	}
	m2, err := RunHarness(HarnessOptions{
		RepoRoot:    repoRoot,
		SilaBin:     silaBin,
		OutDir:      out2,
		GroupFilter: "goals",
	})
	if err != nil {
		t.Fatalf("RunHarness 2 failed: %v", err)
	}

	b1, err := os.ReadFile(filepath.Join(out1, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	b2, err := os.ReadFile(filepath.Join(out2, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(b1) != string(b2) {
		t.Fatalf("two consecutive harness runs were not byte-identical:\nrun1:\n%s\nrun2:\n%s", string(b1), string(b2))
	}
	if len(m1.Surfaces) == 0 || len(m1.Surfaces) != len(m2.Surfaces) {
		t.Fatalf("unexpected surface counts: %d vs %d", len(m1.Surfaces), len(m2.Surfaces))
	}

	// Verify caller HOME was untouched and caller-sila.db was never created
	if _, err := os.Stat(filepath.Join(callerHome, "caller-sila.db")); !os.IsNotExist(err) {
		t.Fatalf("harness touched caller SILA_DB: %v", err)
	}
	sentinelBytes, err := os.ReadFile(sentinel)
	if err != nil || string(sentinelBytes) != "untouched\n" {
		t.Fatalf("caller HOME sentinel modified: %v", err)
	}
}
