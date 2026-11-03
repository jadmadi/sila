package assess

import (
	"strings"
	"testing"
)

func TestParseCoverageProfile_Synthetic(t *testing.T) {
	data := `mode: set
github.com/example/pkg/a.go:10.2,12.5 3 1
github.com/example/pkg/a.go:15.2,16.5 2 0
github.com/example/store/b.go:5.2,8.10 4 2
github.com/example/store/b.go:10.2,12.10 4 0
`
	profile, err := ParseCoverageProfile(strings.NewReader(data))
	if err != nil {
		t.Fatalf("ParseCoverageProfile failed: %v", err)
	}

	// Total statements: 3 + 2 + 4 + 4 = 13
	// Covered statements: 3 + 0 + 4 + 0 = 7
	// Overall: 7 / 13 = 53.846%
	if profile.TotalStatements != 13 {
		t.Errorf("expected 13 total statements, got %d", profile.TotalStatements)
	}
	if profile.CoveredStatements != 7 {
		t.Errorf("expected 7 covered statements, got %d", profile.CoveredStatements)
	}
	expectedPct := 7.0 / 13.0 * 100.0
	if profile.OverallPct < expectedPct-0.1 || profile.OverallPct > expectedPct+0.1 {
		t.Errorf("expected ~%.2f%%, got %.2f%%", expectedPct, profile.OverallPct)
	}

	thresholds := CoverageThresholds{
		MinGlobalPct: 50.0,
		CoreMinPct:   40.0,
		CorePackages: []string{"store"},
		Strict:       false,
	}

	report := AuditCoverage(profile, thresholds)
	if !report.Clean {
		t.Errorf("expected clean report with 50%% threshold on 53.8%%, got violations: %v", report.Violations)
	}

	// With strict threshold
	strictThresholds := CoverageThresholds{
		MinGlobalPct: 80.0,
		CoreMinPct:   90.0,
		CorePackages: []string{"store"},
		Strict:       true,
	}
	strictReport := AuditCoverage(profile, strictThresholds)
	if strictReport.Clean {
		t.Errorf("expected strict report to fail on 53.8%%")
	}
	if len(strictReport.Violations) == 0 {
		t.Errorf("expected violations in strict report")
	}

	toon := RenderCoverageTOON(report)
	if !strings.Contains(toon, "coverage_summary:") {
		t.Errorf("expected TOON coverage_summary, got:\n%s", toon)
	}
}
