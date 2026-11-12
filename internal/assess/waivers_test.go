package assess

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeWaiver(t *testing.T, body string) (string, string) {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "goals", "demo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "waiver.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return root, "demo"
}

const validWaiver = `{
  "schema": "sila/goal-waiver/v1",
  "goal": "demo",
  "waivers": [{
    "probe": "Deliverable Contract Verification (go test -race ./...)",
    "reason": "third-party driver spins under the race detector during schema migration",
    "upstream": ["https://example.com/issue"],
    "approved_by": "owner",
    "approved_at": "2026-11-07",
    "review_by": "2027-02-07",
    "fallback": "go test ./... and the pre-commit gate remain required"
  }]
}`

func TestLoadGoalWaivers(t *testing.T) {
	now := time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC)
	root, slug := writeWaiver(t, validWaiver)
	waivers, err := LoadGoalWaivers(root, slug, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(waivers) != 1 {
		t.Fatalf("waivers = %d", len(waivers))
	}
	probe := Probe{ID: "CLI-DELIV-001", Name: "Deliverable Contract Verification (go test -race ./...) "}
	result := WaivedProbeResult(probe, waivers[probe.Name])
	if !result.Passed || !result.Waived || result.WaiverReason == "" {
		t.Fatalf("result = %+v", result)
	}
	if _, err := LoadGoalWaivers(t.TempDir(), "missing", now); err != nil {
		t.Fatalf("missing file should be empty: %v", err)
	}
}

func TestLoadGoalWaiversRejectsInvalid(t *testing.T) {
	now := time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC)
	cases := map[string]string{
		"expired":      `{"schema":"sila/goal-waiver/v1","goal":"demo","waivers":[{"probe":"p","reason":"a sufficiently long reason for the waiver","upstream":["https://example.com"],"approved_by":"owner","approved_at":"2026-11-07","review_by":"2026-11-06","fallback":"x"}]}`,
		"short reason": `{"schema":"sila/goal-waiver/v1","goal":"demo","waivers":[{"probe":"p","reason":"short","upstream":["https://example.com"],"approved_by":"owner","review_by":"2027-01-01","fallback":"x"}]}`,
		"no approver":  `{"schema":"sila/goal-waiver/v1","goal":"demo","waivers":[{"probe":"p","reason":"a sufficiently long reason for the waiver","upstream":["https://example.com"],"review_by":"2027-01-01","fallback":"x"}]}`,
		"no upstream":  `{"schema":"sila/goal-waiver/v1","goal":"demo","waivers":[{"probe":"p","reason":"a sufficiently long reason for the waiver","approved_by":"owner","review_by":"2027-01-01","fallback":"x"}]}`,
		"wrong goal":   `{"schema":"sila/goal-waiver/v1","goal":"other","waivers":[]}`,
	}
	for name, body := range cases {
		root, slug := writeWaiver(t, body)
		if _, err := LoadGoalWaivers(root, slug, now); err == nil {
			t.Fatalf("%s: expected error", name)
		}
	}
}
