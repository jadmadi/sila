package main

import (
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/release"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func TestPrintSoakReport(t *testing.T) {
	report := release.SoakReport{
		State:              release.SoakInProgress,
		SoakStartAt:        "2026-11-07T07:45:00Z",
		ElapsedHours:       6,
		RequiredHours:      72,
		RemainingHours:     66,
		CurrentDay:         1,
		ActiveDays:         1,
		RequiredActiveDays: 3,
		ActiveDates:        []string{"2026-11-07"},
		HarnessCount:       2,
		RequiredHarnesses:  2,
		Harnesses:          []string{"cli", "codex"},
		ToolEventsTotal:    215,
		ReadyFeatures:      1,
		SoakingFeatures:    1,
		DebugFeatures:      1,
		ReadyThroughCommit: "ee138913",
		FeatureLedger: []store.FeatureSoakRecord{
			{FeatureID: "feat-mcp-4e568b90", Title: "feat(mcp): add as-of reads", Status: "needs_debug", ErrorCount: 1, LastVerifiedAt: "2026-11-07T10:00:00Z"},
			{FeatureID: "feat-cli-78940a4e", Title: "feat(cli): add soak status command", Status: "soaking", ElapsedHours: 36.9, ActiveDays: 2, LastVerifiedAt: "2026-11-07T10:00:00Z"},
		},
	}
	out := captureStdout(t, func() {
		printSoakReport(render.New(nil, true), report)
	})
	for _, want := range []string{"day 1/3", "66.0h left", "1 / 3", "2 / 2", "cli, codex", "per-feature timers", "ready train", "ee138913", "soaking now", "feat-cli-78940a4e", "needs debug", "next actions"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "feat-mcp-4e568b90") {
		t.Fatalf("missing feature in:\n%s", out)
	}
}

func TestSoakActions(t *testing.T) {
	report := release.SoakReport{State: release.SoakInProgress, HarnessCount: 1, RequiredHarnesses: 2, ActiveDays: 1, RequiredActiveDays: 3, RemainingHours: 60}
	actions := strings.Join(soakActions(report), "\n")
	if !strings.Contains(actions, "second harness") || !strings.Contains(actions, "at least once today") {
		t.Fatalf("actions = %s", actions)
	}
	ready := soakActions(release.SoakReport{State: release.SoakReady, HarnessCount: 2, RequiredHarnesses: 2, ActiveDays: 3, RequiredActiveDays: 3})
	if !strings.Contains(strings.Join(ready, "\n"), "Soak complete") {
		t.Fatalf("ready actions = %v", ready)
	}
	if truncateTitle("short", 10) != "short" || !strings.HasSuffix(truncateTitle("a very long title here", 10), "...") {
		t.Fatal("truncateTitle failed")
	}
	soaking := soakingFeatureSoaks([]store.FeatureSoakRecord{
		{FeatureID: "a", Status: "ready"},
		{FeatureID: "b", Status: "soaking"},
	})
	if len(soaking) != 1 || soaking[0].FeatureID != "b" {
		t.Fatalf("soakingFeatureSoaks = %+v", soaking)
	}
}
