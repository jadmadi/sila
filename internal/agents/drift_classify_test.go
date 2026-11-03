package agents

import (
	"os"
	"testing"
)

func TestClassifyDriftPriority(t *testing.T) {
	cases := []struct {
		name   string
		report DriftReport
		want   string
	}{
		{"missing", DriftReport{MissingFiles: []string{"AGENTS.md"}, PrinciplesHashMatch: false}, "missing_target"},
		{"principles", DriftReport{PrinciplesHashMatch: false, ManagedHash: "x", HashMatch: false}, "principles_drift"},
		{"managed", DriftReport{ManagedHash: "x", HashMatch: false, LegacyBlock: true, PrinciplesHashMatch: true}, "managed_hash_drift"},
		{"legacy", DriftReport{LegacyBlock: true, PrinciplesHashMatch: true, TargetStatuses: []TargetStatus{{Kind: "cursor", Present: true}}}, "legacy_block"},
		{"foreign", DriftReport{PrinciplesHashMatch: true, TargetStatuses: []TargetStatus{{Kind: "cursor", Present: true, Managed: false}}}, "foreign_target"},
		{"in_sync", DriftReport{HashMatch: true, PrinciplesHashMatch: true}, "in_sync"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			report := tc.report
			classifyDrift(&report)
			if report.DriftKind != tc.want {
				t.Fatalf("DriftKind = %q, want %q (kinds=%v)", report.DriftKind, tc.want, report.DriftKinds)
			}
			if report.HasDrift != (tc.want != "in_sync") {
				t.Fatalf("HasDrift = %v for %s", report.HasDrift, tc.want)
			}
		})
	}
}

func TestDetectDriftUserOnlyEditKeepsManagedMatch(t *testing.T) {
	repo := t.TempDir()
	opts := CompileOptions{RepoRoot: repo, ProjectName: "user-edit", Stack: "go"}
	if _, err := DeployTargets(repo, opts, []string{"agents"}); err != nil {
		t.Fatal(err)
	}
	path := repo + "/AGENTS.md"
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append([]byte("user preface\n\n"), before...), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := DetectDrift(repo, opts)
	if err != nil {
		t.Fatal(err)
	}
	if !report.HashMatch {
		t.Fatalf("user-only edit changed ManagedHashMatch: %+v", report)
	}
	if report.DriftKind != "in_sync" {
		t.Fatalf("user-only edit classified as %s: %v", report.DriftKind, report.Details)
	}
}
