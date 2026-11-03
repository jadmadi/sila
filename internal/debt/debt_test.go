package debt

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseDebtComment_Formats(t *testing.T) {
	tests := []struct {
		name       string
		line       string
		expectedOK bool
		marker     string
		ceiling    string
		trigger    string
		linkedGoal string
		status     DebtStatus
	}{
		{
			name:       "Go single-line shortcut with comma",
			line:       "// shortcut: < 10k users, when adding multi-tenant",
			expectedOK: true,
			marker:     "shortcut",
			ceiling:    "< 10k users",
			trigger:    "when adding multi-tenant",
			status:     StatusHealthy,
		},
		{
			name:       "Python comment with ponytail and pipe",
			line:       "# ponytail: max 500 items | migrate to Postgres when dataset > 100MB",
			expectedOK: true,
			marker:     "ponytail",
			ceiling:    "max 500 items",
			trigger:    "migrate to Postgres when dataset > 100MB",
			status:     StatusHealthy,
		},
		{
			name:       "C-style block comment with goal tag",
			line:       "/* shortcut: in-memory cache, cluster redis when replicas > 1 [goal: redis-cache] */",
			expectedOK: true,
			marker:     "shortcut",
			ceiling:    "in-memory cache",
			trigger:    "cluster redis when replicas > 1",
			linkedGoal: "redis-cache",
			status:     StatusHealthy,
		},
		{
			name:       "Explicit ceiling and trigger labels",
			line:       "// shortcut: ceiling: 100 req/sec; trigger: autoscaling group configured",
			expectedOK: true,
			marker:     "shortcut",
			ceiling:    "100 req/sec",
			trigger:    "autoscaling group configured",
			status:     StatusHealthy,
		},
		{
			name:       "Missing upgrade trigger (rot risk)",
			line:       "// shortcut: only works on local filesystem",
			expectedOK: true,
			marker:     "shortcut",
			ceiling:    "only works on local filesystem",
			trigger:    "",
			status:     StatusRotRisk,
		},
		{
			name:       "Only upgrade trigger specified",
			line:       "// shortcut: when migrating to v2 API",
			expectedOK: true,
			marker:     "shortcut",
			ceiling:    "",
			trigger:    "when migrating to v2 API",
			status:     StatusMissingCeiling,
		},
		{
			name:       "Empty shortcut body",
			line:       "// shortcut: ",
			expectedOK: true,
			marker:     "shortcut",
			ceiling:    "",
			trigger:    "",
			status:     StatusRotRisk,
		},
		{
			name:       "Unrelated comment",
			line:       "// regular comment explaining an algorithm",
			expectedOK: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			item, ok := ParseDebtComment("service/auth.go", 42, tc.line)
			if ok != tc.expectedOK {
				t.Fatalf("expected ok=%v, got %v", tc.expectedOK, ok)
			}
			if !tc.expectedOK {
				return
			}
			if item.Marker != tc.marker {
				t.Errorf("expected marker %q, got %q", tc.marker, item.Marker)
			}
			if item.Ceiling != tc.ceiling {
				t.Errorf("expected ceiling %q, got %q", tc.ceiling, item.Ceiling)
			}
			if item.Trigger != tc.trigger {
				t.Errorf("expected trigger %q, got %q", tc.trigger, item.Trigger)
			}
			if tc.linkedGoal != "" && item.LinkedGoal != tc.linkedGoal {
				t.Errorf("expected linkedGoal %q, got %q", tc.linkedGoal, item.LinkedGoal)
			}
			if item.Status != tc.status {
				t.Errorf("expected status %v, got %v", tc.status, item.Status)
			}
		})
	}
}

func TestScanRepoDebt_DirectoryWalk(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "test-debt-scanner-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create dummy files with debt comments
	srcFile := filepath.Join(tempDir, "server.go")
	srcContent := `package main

// shortcut: single-threaded queue, switch to worker pool when batch > 1000
func processQueue() {}

// shortcut: hardcoded timeout [goal: dynamic-timeout]
func connect() {}
`
	if err := os.WriteFile(srcFile, []byte(srcContent), 0644); err != nil {
		t.Fatalf("write srcFile: %v", err)
	}

	pyFile := filepath.Join(tempDir, "script.py")
	pyContent := `# shortcut: ceiling: 50MB memory; trigger: stream to disk
def load_data():
    pass
`
	if err := os.WriteFile(pyFile, []byte(pyContent), 0644); err != nil {
		t.Fatalf("write pyFile: %v", err)
	}

	// Create test file (should be ignored when IncludeTests is false)
	testFile := filepath.Join(tempDir, "server_test.go")
	testContent := `package main
// shortcut: mock db, test only
`
	if err := os.WriteFile(testFile, []byte(testContent), 0644); err != nil {
		t.Fatalf("write testFile: %v", err)
	}

	// Run scan
	report, err := ScanRepoDebt(tempDir, DebtScanOptions{IncludeTests: false})
	if err != nil {
		t.Fatalf("ScanRepoDebt failed: %v", err)
	}

	if report.TotalItems != 3 {
		t.Fatalf("expected 3 total debt items, got %d", report.TotalItems)
	}
	if report.HealthyCount != 2 {
		t.Errorf("expected 2 healthy items, got %d", report.HealthyCount)
	}
	if report.RotRiskCount != 1 {
		t.Errorf("expected 1 rot risk item, got %d", report.RotRiskCount)
	}

	// Test IncludeTests=true
	reportWithTests, err := ScanRepoDebt(tempDir, DebtScanOptions{IncludeTests: true})
	if err != nil {
		t.Fatalf("ScanRepoDebt with tests failed: %v", err)
	}
	if reportWithTests.TotalItems != 4 {
		t.Fatalf("expected 4 debt items with tests included, got %d", reportWithTests.TotalItems)
	}
}
