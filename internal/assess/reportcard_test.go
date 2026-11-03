package assess

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuditReportCard_Fixtures(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "reportcard-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	pkgDir := filepath.Join(tmpDir, "cmd", "sample")
	if err := os.MkdirAll(pkgDir, 0755); err != nil {
		t.Fatalf("failed to create sample dir: %v", err)
	}

	src := `package main

// SampleFunc has low complexity.
func SampleFunc(x int) int {
	if x > 0 {
		return x
	}
	return -x
}

// IneffFunc has ineffectual assignment.
func IneffFunc() int {
	a := 10
	a = 20
	return a
}

// TypoFunc has teh wrong spelling in comment.
func TypoFunc() {}
`
	if err := os.WriteFile(filepath.Join(pkgDir, "main.go"), []byte(src), 0644); err != nil {
		t.Fatalf("failed to write main.go: %v", err)
	}

	rc, err := AuditReportCard(tmpDir)
	if err != nil {
		t.Fatalf("AuditReportCard failed: %v", err)
	}

	if rc.Grade == "" {
		t.Errorf("expected non-empty grade")
	}

	// Verify ineffassign caught 'a'
	var ineffComp *ComponentGrade
	var misspellComp *ComponentGrade
	for i := range rc.Components {
		if rc.Components[i].Name == "ineffassign" {
			ineffComp = &rc.Components[i]
		}
		if rc.Components[i].Name == "misspell" {
			misspellComp = &rc.Components[i]
		}
	}

	if ineffComp == nil || ineffComp.Passed {
		t.Errorf("expected ineffassign to detect overwritten variable")
	}
	if misspellComp == nil || len(misspellComp.Issues) == 0 {
		t.Errorf("expected misspell to detect 'teh'")
	}

	toon := RenderReportCardTOON(rc)
	if !strings.Contains(toon, "report_card_summary:") {
		t.Errorf("expected TOON report_card_summary, got:\n%s", toon)
	}
}

func TestAuditReportCard_Codebase(t *testing.T) {
	rc, err := AuditReportCard("../..")
	if err != nil {
		t.Fatalf("AuditReportCard failed on codebase: %v", err)
	}

	t.Logf("Report Card Grade: %s (Score: %.1f, Clean: %t, Files: %d)",
		rc.Grade, rc.Score, rc.Clean, rc.FilesScanned)

	for _, c := range rc.Components {
		t.Logf("  Component %s: Score=%.1f Passed=%t (%s)", c.Name, c.Score, c.Passed, c.Detail)
	}

	if rc.Grade != "A+" && rc.Grade != "A" {
		t.Errorf("expected codebase report card grade A or A+, got %s (Score: %.1f)", rc.Grade, rc.Score)
	}
}
