package release

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCalculateGrade(t *testing.T) {
	tests := []struct {
		score float64
		grade string
	}{
		{100.0, "A+"},
		{95.0, "A+"},
		{94.9, "A"},
		{90.0, "A"},
		{89.9, "B"},
		{80.0, "B"},
		{79.9, "C"},
		{70.0, "C"},
		{69.9, "D"},
		{60.0, "D"},
		{59.9, "F"},
		{0.0, "F"},
	}

	for _, tc := range tests {
		if got := calculateGrade(tc.score); got != tc.grade {
			t.Errorf("calculateGrade(%v) = %v; want %v", tc.score, got, tc.grade)
		}
	}
}

func TestModeLabel(t *testing.T) {
	if got := modeLabel(true, false); got != "Forced Offline (--offline)" {
		t.Errorf("expected forced offline label, got %q", got)
	}
	if got := modeLabel(false, true); got != "Offline Resilient (Auto-Detected)" {
		t.Errorf("expected auto-detected offline label, got %q", got)
	}
	if got := modeLabel(false, false); got != "Connected Online" {
		t.Errorf("expected connected online label, got %q", got)
	}
}

func TestRunPreflight_OfflineAndAllowDirty(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	// If in internal/release, walk up to cli root
	repoDir := cwd
	if filepath.Base(repoDir) == "release" {
		repoDir = filepath.Dir(filepath.Dir(repoDir))
	}

	opts := PreflightOptions{
		AllowDirty: true,
		Offline:    true,
		Strict:     false,
		RepoDir:    repoDir,
	}

	report, err := RunPreflight(opts)
	if err != nil {
		t.Fatalf("RunPreflight failed: %v", err)
	}

	if !report.OfflineMode {
		t.Errorf("expected OfflineMode to be true")
	}

	if len(report.Checks) == 0 {
		t.Fatalf("expected checks to be evaluated, got 0")
	}

	// Verify that remote probes were skipped in offline mode
	skippedCount := 0
	for _, c := range report.Checks {
		if c.Status == StatusSkipped {
			skippedCount++
		}
	}

	if skippedCount < 2 {
		t.Errorf("expected at least 2 skipped remote checks in offline mode, got %d", skippedCount)
	}

	// Grade should be passing
	if report.Grade != "A+" && report.Grade != "A" && report.Grade != "B" {
		t.Errorf("expected passing grade, got %s (score: %.1f%%)", report.Grade, report.Score)
	}
}
