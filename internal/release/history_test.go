package release

import (
	"os"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/render"
)

func TestParseReleases(t *testing.T) {
	content := `# Changelog

## [0.11.0] - 2026-10-04

### Added
- Edge Worker Web Governance and Data Processing Policies
- Legal Doc Scaffold Skill

### Changed
- Breaking: sila changelog --json keys are now snake_case
- sila changelog markdown improvements

## [0.10.0] - 2026-10-03

- **Compliance Persona Rubrics**:
  - Expanded legal-compliance-counsel rubric
  - Another sub detail
- Fix test stdout pipe deadlock
`
	releases, err := ParseReleases(content)
	if err != nil {
		t.Fatalf("unexpected error parsing releases: %v", err)
	}

	if len(releases) != 2 {
		t.Fatalf("expected 2 releases, got %d", len(releases))
	}

	r1 := releases[0]
	if r1.Version != "0.11.0" || r1.Date != "2026-10-04" {
		t.Errorf("r1 unexpected version/date: %s / %s", r1.Version, r1.Date)
	}
	if len(r1.Features) != 2 {
		t.Errorf("r1 expected 2 features, got %d", len(r1.Features))
	}
	if len(r1.Breaking) != 1 {
		t.Errorf("r1 expected 1 breaking change, got %d", len(r1.Breaking))
	}

	r2 := releases[1]
	if r2.Version != "0.10.0" || r2.Date != "2026-10-03" {
		t.Errorf("r2 unexpected version/date: %s / %s", r2.Version, r2.Date)
	}
	// Indented sub-bullets must not be treated as independent top-level features
	if len(r2.Features) != 1 {
		t.Errorf("r2 expected 1 feature, got %d: %v", len(r2.Features), r2.Features)
	}
	if len(r2.Fixes) != 1 {
		t.Errorf("r2 expected 1 fix, got %d", len(r2.Fixes))
	}

	// Verify details are attached
	details := r2.Details["Compliance Persona Rubrics"]
	if len(details) != 2 {
		t.Errorf("expected 2 sub-details, got %d: %v", len(details), details)
	}
}

func TestGetReleaseHistoryFromEmbedded(t *testing.T) {
	rep := GetReleaseHistory(2, "")
	if rep.TotalReleases == 0 {
		t.Errorf("expected total releases > 0 from embedded changelog")
	}
	if len(rep.Releases) == 0 {
		t.Errorf("expected selected releases > 0")
	}

	ui := render.New(os.Stdout, true)
	out := RenderReleaseHistory(rep, ui)
	if !strings.Contains(out, "Sila") || !strings.Contains(out, "changelog") {
		t.Errorf("expected header in output: %s", out)
	}
	if !strings.Contains(out, "╭─") || !strings.Contains(out, "─╮") {
		t.Errorf("expected card formatting in output: %s", out)
	}

	// Line count check: default 2 releases must fit comfortably (< 90 lines vs old 876 lines)
	lineCount := len(strings.Split(strings.TrimSpace(out), "\n"))
	if lineCount > 90 {
		t.Errorf("expected default changelog under 90 lines, got %d lines", lineCount)
	}
}

func TestRenderReleaseHistory_Budgeting(t *testing.T) {
	rep := ReleaseHistoryReport{
		CurrentVersion: "v0.11.0",
		TotalReleases:  1,
		Releases: []ReleaseEntry{
			{
				Version: "0.11.0",
				Date:    "2026-10-04",
				Features: []string{
					"Feature 1", "Feature 2", "Feature 3", "Feature 4",
					"Feature 5", "Feature 6", "Feature 7",
				},
				Fixes: []string{"Fix 1", "Fix 2", "Fix 3", "Fix 4"},
			},
		},
	}

	ui := render.New(os.Stdout, true)
	// Render with default budget (max 4 features, max 3 fixes)
	out := RenderReleaseHistory(rep, ui, DefaultHistoryRenderOptions())
	if !strings.Contains(out, "… and 3 more features") {
		t.Errorf("expected feature budget cutoff message, got:\n%s", out)
	}
	if !strings.Contains(out, "… and 1 more bug fixes") {
		t.Errorf("expected fix budget cutoff message, got:\n%s", out)
	}

	// In Full mode, all items must appear
	outFull := RenderReleaseHistory(rep, ui, HistoryRenderOptions{Full: true})
	if strings.Contains(outFull, "more features") {
		t.Errorf("full mode should not cut off features:\n%s", outFull)
	}
	if !strings.Contains(outFull, "Feature 7") || !strings.Contains(outFull, "Fix 4") {
		t.Errorf("full mode missing features or fixes:\n%s", outFull)
	}
}

func TestRenderTimeline(t *testing.T) {
	rep := ReleaseHistoryReport{
		CurrentVersion: "v0.11.0",
		TotalReleases:  2,
		Releases: []ReleaseEntry{
			{
				Version:  "0.11.0",
				Date:     "2026-10-04",
				Breaking: []string{"Breaking 1"},
				Features: []string{"Feature A", "Feature B"},
			},
			{
				Version:  "0.10.0",
				Date:     "2026-10-03",
				Features: []string{"Feature C"},
				Fixes:    []string{"Fix A"},
			},
		},
	}

	ui := render.New(os.Stdout, true)
	out := RenderTimeline(rep, ui)
	if !strings.Contains(out, "release timeline") {
		t.Errorf("expected release timeline header, got:\n%s", out)
	}
	if !strings.Contains(out, "RELEASE") || !strings.Contains(out, "DATE") {
		t.Errorf("expected table columns, got:\n%s", out)
	}
	if !strings.Contains(out, "v0.11.0") || !strings.Contains(out, "v0.10.0") {
		t.Errorf("expected version rows, got:\n%s", out)
	}
}

func TestGetReleaseHistory_TargetVersion(t *testing.T) {
	rep := GetReleaseHistory(0, "0.17.5")
	if len(rep.Releases) != 1 {
		t.Fatalf("expected 1 release for targeted version, got %d", len(rep.Releases))
	}
	if rep.Releases[0].Version != "0.17.5" {
		t.Errorf("expected version 0.11.0, got %s", rep.Releases[0].Version)
	}

	// Targeted with 'v' prefix
	repV := GetReleaseHistory(0, "v0.17.5")
	if len(repV.Releases) != 1 || repV.Releases[0].Version != "0.17.5" {
		t.Errorf("expected v-prefixed lookup to match")
	}

	// Non-existent version
	repMissing := GetReleaseHistory(0, "v999.999.999")
	if len(repMissing.Releases) != 0 {
		t.Errorf("expected 0 releases for non-existent version, got %d", len(repMissing.Releases))
	}
}
