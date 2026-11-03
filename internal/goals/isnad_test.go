package goals

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseIsnadHeader(t *testing.T) {
	content := `# Goal: Test Goal

## Chain of Custody (Isnad)
- **Registered By**: @antigravity | Session: 858af83c-c8c0-4f36-af3a-ad93375a58b4 | Date: 2026-10-03
- **Spec Audited By**: @auditor | Session: abc-123 | Date: 2026-10-03
- **Implemented By**: @builder | Session: def-456 | Commit: c0c08f8
- **Mystery Audited By**: @mystery-auditor | Session: ghi-789 | Report: docs/specs/report.md

## Goal Description
Some description here.
`

	isnad := ParseIsnadHeader(content)
	if isnad == nil {
		t.Fatalf("expected non-nil isnad header")
	}

	if isnad.RegisteredBy != "@antigravity" {
		t.Errorf("RegisteredBy = %q, want @antigravity", isnad.RegisteredBy)
	}
	if isnad.RegisteredSession != "858af83c-c8c0-4f36-af3a-ad93375a58b4" {
		t.Errorf("RegisteredSession = %q", isnad.RegisteredSession)
	}
	if isnad.RegisteredDate != "2026-10-03" {
		t.Errorf("RegisteredDate = %q", isnad.RegisteredDate)
	}
	if isnad.SpecAuditedBy != "@auditor" {
		t.Errorf("SpecAuditedBy = %q", isnad.SpecAuditedBy)
	}
	if isnad.ImplementedBy != "@builder" {
		t.Errorf("ImplementedBy = %q", isnad.ImplementedBy)
	}
	if isnad.ImplementedCommit != "c0c08f8" {
		t.Errorf("ImplementedCommit = %q", isnad.ImplementedCommit)
	}
	if isnad.MysteryAuditedBy != "@mystery-auditor" {
		t.Errorf("MysteryAuditedBy = %q", isnad.MysteryAuditedBy)
	}
	if isnad.MysteryAuditedReport != "docs/specs/report.md" {
		t.Errorf("MysteryAuditedReport = %q", isnad.MysteryAuditedReport)
	}
}

func TestFormatAndRoundTripIsnad(t *testing.T) {
	orig := &IsnadHeader{
		RegisteredBy:          "@antigravity",
		RegisteredSession:     "sess-1",
		RegisteredDate:        "2026-10-03",
		SpecAuditedBy:         "@spec-checker",
		SpecAuditedSession:    "sess-2",
		SpecAuditedDate:       "2026-10-03",
		ImplementedBy:         "@coder",
		ImplementedSession:    "sess-3",
		ImplementedCommit:     "1234567",
		MysteryAuditedBy:      "@mystery",
		MysteryAuditedSession: "sess-4",
		MysteryAuditedReport:  "report.md",
	}

	formatted := FormatIsnadHeader(orig)
	if !strings.Contains(formatted, "## Chain of Custody (Isnad)") {
		t.Fatalf("formatted isnad missing header: %s", formatted)
	}

	parsed := ParseIsnadHeader(formatted)
	if parsed == nil {
		t.Fatalf("failed to parse formatted isnad")
	}

	if parsed.RegisteredBy != orig.RegisteredBy || parsed.RegisteredSession != orig.RegisteredSession {
		t.Errorf("roundtrip mismatch: got %+v, want %+v", parsed, orig)
	}
	if parsed.ImplementedCommit != orig.ImplementedCommit {
		t.Errorf("implemented commit mismatch: got %q, want %q", parsed.ImplementedCommit, orig.ImplementedCommit)
	}
}

func TestUpdateGoalMdIsnad(t *testing.T) {
	tmpDir := t.TempDir()
	goalMd := filepath.Join(tmpDir, "goal.md")

	initial := `# Goal: Sample Goal

## Goal Description
Testing update isnad.
`
	if err := os.WriteFile(goalMd, []byte(initial), 0644); err != nil {
		t.Fatal(err)
	}

	isnad := &IsnadHeader{
		RegisteredBy:      "@tester",
		RegisteredSession: "sess-init",
		RegisteredDate:    "2026-10-03",
	}

	if err := UpdateGoalMdIsnad(goalMd, isnad); err != nil {
		t.Fatalf("UpdateGoalMdIsnad failed: %v", err)
	}

	updated, err := os.ReadFile(goalMd)
	if err != nil {
		t.Fatal(err)
	}

	parsed := ParseIsnadHeader(string(updated))
	if parsed == nil {
		t.Fatalf("expected parsed isnad from updated goal.md")
	}
	if parsed.RegisteredBy != "@tester" {
		t.Errorf("RegisteredBy = %q, want @tester", parsed.RegisteredBy)
	}

	// Update again with implemented info
	isnad.ImplementedBy = "@coder"
	isnad.ImplementedCommit = "abcdef"
	if err := UpdateGoalMdIsnad(goalMd, isnad); err != nil {
		t.Fatalf("UpdateGoalMdIsnad second pass failed: %v", err)
	}

	updated2, err := os.ReadFile(goalMd)
	if err != nil {
		t.Fatal(err)
	}
	parsed2 := ParseIsnadHeader(string(updated2))
	if parsed2 == nil || parsed2.ImplementedBy != "@coder" {
		t.Errorf("expected ImplementedBy = @coder, got %+v", parsed2)
	}
}
