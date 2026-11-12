package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/render"
)

func gateGoldenFixtures() (pass []gateCheck, fail []gateCheck, failVerdict gateVerdict) {
	pass = []gateCheck{
		{Name: "tests", Status: "pass", Detail: "go test ./cmd/sila passed in 1.2s", DurationMS: 1200},
		{Name: "format", Status: "pass", Detail: "all files are gofmt clean", DurationMS: 40},
		{Name: "changelog", Status: "pass", Detail: "CHANGELOG.md has an [Unreleased] entry", DurationMS: 5},
	}
	fail = []gateCheck{
		{Name: "tests", Status: "pass", Detail: "go test ./cmd/sila passed in 1.2s", DurationMS: 1200},
		{Name: "gate", Status: "fail", Detail: "one blocker", DurationMS: 30, Output: "boom: assertion failed", Remediation: "fix the assertion"},
	}
	failVerdict = gateVerdict{Clean: false, Blockers: []gateCheck{fail[1]}}
	return pass, fail, failVerdict
}

func renderGateGolden(t *testing.T, compact bool, checks []gateCheck, verdict gateVerdict) string {
	t.Helper()
	ui := render.New(os.Stdout, false)
	return captureStdout(t, func() {
		if compact {
			renderGateCompact(ui, checks, verdict, "/tmp/gate.log", 1300*time.Millisecond)
			return
		}
		printGatePreCommit(ui, checks, verdict)
	})
}

func compareOrUpdateGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "gate", name)
	if os.Getenv("UPDATE_GOLDENS") == "1" {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: %v (run UPDATE_GOLDENS=1)", name, err)
	}
	if string(want) != got {
		t.Fatalf("%s mismatch:\n--- want ---\n%s\n--- got ---\n%s", name, want, got)
	}
}

func TestGateGoldens(t *testing.T) {
	pass, fail, failVerdict := gateGoldenFixtures()
	clean := gateVerdict{Clean: true}
	compareOrUpdateGolden(t, "pass-compact.golden", renderGateGolden(t, true, pass, clean))
	compareOrUpdateGolden(t, "pass-detail.golden", renderGateGolden(t, false, pass, clean))
	compareOrUpdateGolden(t, "fail-compact.golden", renderGateGolden(t, true, fail, failVerdict))
	compareOrUpdateGolden(t, "fail-detail.golden", renderGateGolden(t, false, fail, failVerdict))
}

func TestGateCompactMeasurement(t *testing.T) {
	checks := make([]gateCheck, 30)
	for i := range checks {
		checks[i] = gateCheck{Name: "check-" + string(rune('a'+i%26)), Status: "pass", Detail: "detail line for the check", DurationMS: 10}
	}
	verdict := gateVerdict{Clean: true}
	compact := renderGateGolden(t, true, checks, verdict)
	detail := renderGateGolden(t, false, checks, verdict)
	t.Logf("compact=%d bytes detail=%d bytes ratio=%.0f%%", len(compact), len(detail), 100*float64(len(compact))/float64(len(detail)))
	if len(compact) > len(detail)/5 {
		t.Fatalf("compact %d bytes exceeds 20%% of detail %d bytes", len(compact), len(detail))
	}
}
