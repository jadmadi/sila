package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/render"
)

func TestComputeGateVerdict(t *testing.T) {
	cases := []struct {
		name         string
		checks       []gateCheck
		requireTests bool
		strict       bool
		wantClean    bool
		wantBlockers int
		wantWarnings int
		wantAdvisory int
		wantSkipped  int
	}{
		{
			name:      "all pass",
			checks:    []gateCheck{{Name: "tests", Status: "pass"}, {Name: "format", Status: "pass"}},
			wantClean: true,
		},
		{
			name:         "fail blocks",
			checks:       []gateCheck{{Name: "format", Status: "fail", Detail: "needs gofmt"}},
			wantBlockers: 1,
		},
		{
			name:        "tests skip is advisory by default",
			checks:      []gateCheck{{Name: "tests", Status: "skip"}},
			wantClean:   true,
			wantSkipped: 1,
		},
		{
			name:         "require-tests promotes a skipped run",
			checks:       []gateCheck{{Name: "tests", Status: "skip"}},
			requireTests: true,
			wantBlockers: 1,
		},
		{
			name:        "other skips stay advisory",
			checks:      []gateCheck{{Name: "format", Status: "skip"}},
			wantClean:   true,
			wantSkipped: 1,
		},
		{
			name:         "unavailable is advisory by default",
			checks:       []gateCheck{{Name: "changelog", Status: "unavailable"}},
			wantClean:    true,
			wantAdvisory: 1,
		},
		{
			name:         "strict promotes unavailable",
			checks:       []gateCheck{{Name: "changelog", Status: "unavailable"}},
			strict:       true,
			wantBlockers: 1,
		},
		{
			name:         "partial blocks",
			checks:       []gateCheck{{Name: "sentry", Status: "partial"}},
			wantClean:    true,
			wantBlockers: 0,
			wantAdvisory: 0,
		},
		{
			name:         "sentry fail blocks",
			checks:       []gateCheck{{Name: "sentry", Status: "fail"}},
			wantBlockers: 1,
		},
		{
			name:         "warning is not a blocker",
			checks:       []gateCheck{{Name: "rules-budget", Status: "pass", Warning: "2 warnings"}},
			wantClean:    true,
			wantWarnings: 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := computeGateVerdict(tc.checks, tc.requireTests, tc.strict)
			if v.Clean != tc.wantClean {
				t.Fatalf("clean = %v, want %v", v.Clean, tc.wantClean)
			}
			if len(v.Blockers) != tc.wantBlockers {
				t.Fatalf("blockers = %d, want %d", len(v.Blockers), tc.wantBlockers)
			}
			if len(v.Warnings) != tc.wantWarnings {
				t.Fatalf("warnings = %d, want %d", len(v.Warnings), tc.wantWarnings)
			}
			if len(v.Advisory) != tc.wantAdvisory {
				t.Fatalf("advisory = %d, want %d", len(v.Advisory), tc.wantAdvisory)
			}
			if len(v.Skipped) != tc.wantSkipped {
				t.Fatalf("skipped = %d, want %d", len(v.Skipped), tc.wantSkipped)
			}
			if v.Clean != (len(v.Blockers) == 0) {
				t.Fatalf("invariant violated: clean=%v blockers=%d", v.Clean, len(v.Blockers))
			}
			if v.Clean && v.ExitCode != 0 {
				t.Fatalf("clean verdict exit = %d, want 0", v.ExitCode)
			}
			if !v.Clean && v.ExitCode != 1 {
				t.Fatalf("dirty verdict exit = %d, want 1", v.ExitCode)
			}
		})
	}
}

func TestPrintGateNamesBlockersAndWarnings(t *testing.T) {
	ui := render.New(os.Stdout, false)
	checks := []gateCheck{
		{Name: "tests", Status: "skip", Detail: "no test command detected"},
		{Name: "rules-budget", Status: "pass", Detail: "clean", Warning: "2 rules near the ceiling"},
	}
	verdict := computeGateVerdict(checks, true, false)
	out := captureStdout(t, func() {
		printGate(ui, checks, verdict)
	})
	if !strings.Contains(out, "1 blocking check") {
		t.Fatalf("missing blocker count: %s", out)
	}
	if !strings.Contains(out, "tests") || !strings.Contains(out, "fix:") {
		t.Fatalf("missing named blocker or remediation: %s", out)
	}
	if !strings.Contains(out, "WARN") || !strings.Contains(out, "2 rules near the ceiling") {
		t.Fatalf("missing warning row: %s", out)
	}
}

func TestGateReportJSONParity(t *testing.T) {
	verdict := computeGateVerdict([]gateCheck{
		{Name: "tests", Status: "skip", Detail: "no test command"},
		{Name: "format", Status: "pass", Detail: "clean"},
	}, true, false)
	rep := gateReport{
		Clean: verdict.Clean, Tier: "pre-commit", Checks: []gateCheck{},
		Blockers: verdict.Blockers, Warnings: verdict.Warnings,
		Advisory: verdict.Advisory, Skipped: verdict.Skipped,
		ExitCode: verdict.ExitCode,
	}
	b, err := json.Marshal(rep)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(b, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed["clean"] != false {
		t.Fatalf("clean = %#v, want false", parsed["clean"])
	}
	blockers, ok := parsed["blockers"].([]any)
	if !ok || len(blockers) != 1 {
		t.Fatalf("blockers = %#v, want one entry", parsed["blockers"])
	}
	if parsed["exit_code"].(float64) != 1 {
		t.Fatalf("exit_code = %#v, want 1", parsed["exit_code"])
	}
}
