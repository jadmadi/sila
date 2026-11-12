package main

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/jadmadi/sila/internal/release"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

// runSoak answers "what do I do now for the release soak?".
func runSoak(ui render.UI, dbPath string, jsonOut, toonOut bool) {
	report := release.EvaluateSoak(release.SoakOptions{DBPath: dbPath})
	if jsonOut || toonOut {
		emitJSON(report)
		return
	}
	printSoakReport(ui, report)
}

func printSoakReport(ui render.UI, report release.SoakReport) {
	state := string(report.State)
	if state == "" {
		state = "not started"
	}
	header := fmt.Sprintf("sila · soak — %s", strings.ToUpper(state))
	switch report.State {
	case release.SoakReady, release.SoakUpToDate:
		header = ui.Green(ui.Bold(header))
	case release.SoakInProgress:
		header = ui.Yellow(ui.Bold(fmt.Sprintf("%s — day %d/%d", header, report.CurrentDay, report.RequiredActiveDays)))
	case release.SoakNeedsDebug:
		header = ui.Red(ui.Bold(header))
	}
	fmt.Fprintln(os.Stdout, header)
	if report.SoakStartAt != "" {
		fmt.Fprintf(os.Stdout, "  clock:        %.1fh / %.0fh (%.1fh left)\n", report.ElapsedHours, report.RequiredHours, report.RemainingHours)
	}
	fmt.Fprintf(os.Stdout, "  active days:  %d / %d", report.ActiveDays, report.RequiredActiveDays)
	if len(report.ActiveDates) > 0 {
		fmt.Fprintf(os.Stdout, " (%s)", strings.Join(report.ActiveDates, ", "))
	}
	fmt.Fprintln(os.Stdout)
	fmt.Fprintf(os.Stdout, "  harnesses:    %d / %d", report.HarnessCount, report.RequiredHarnesses)
	if len(report.Harnesses) > 0 {
		fmt.Fprintf(os.Stdout, " (%s)", strings.Join(report.Harnesses, ", "))
	}
	fmt.Fprintln(os.Stdout)
	fmt.Fprintf(os.Stdout, "  events:       %d (floor %d), errors %d\n", report.ToolEventsTotal+report.PrimeEventsTotal, release.SoakMinUsage, report.ToolErrorCount+report.PrimeErrorCount)
	if len(report.FeatureLedger) > 0 {
		fmt.Fprintf(os.Stdout, "  features:     %d ready, %d soaking, %d needs debug (per-feature timers)\n",
			report.ReadyFeatures, report.SoakingFeatures, report.DebugFeatures)
	}
	if report.ReadyThroughCommit != "" {
		fmt.Fprintf(os.Stdout, "  ready train:  through %s (%d soaked feature(s) ready to release)\n",
			report.ReadyThroughCommit, report.ReadyFeatures)
	}
	soaking := soakingFeatureSoaks(report.FeatureLedger)
	if len(soaking) > 0 {
		fmt.Fprintf(os.Stdout, "  soaking now:  %d feature(s) on independent timers\n", len(soaking))
		shown := soaking
		if len(shown) > 3 {
			shown = shown[:3]
		}
		reqHours := report.RequiredHours
		if reqHours <= 0 {
			reqHours = release.SoakRequiredHours
		}
		reqDays := report.RequiredActiveDays
		if reqDays <= 0 {
			reqDays = release.SoakRequiredActiveDays
		}
		for _, feature := range shown {
			rem := reqHours - feature.ElapsedHours
			if rem < 0 {
				rem = 0
			}
			fmt.Fprintf(os.Stdout, "    %s %s (%.1fh / %.0fh, %.1fh left, %d/%dd)\n",
				feature.FeatureID, truncateTitle(feature.Title, 44),
				feature.ElapsedHours, reqHours, rem, feature.ActiveDays, reqDays)
		}
		if more := len(soaking) - len(shown); more > 0 {
			fmt.Fprintf(os.Stdout, "    ... %d more soaking feature(s) (see 'sila soak --json')\n", more)
		}
	}

	debug := debugFeatureSoaks(report.FeatureLedger)
	if len(debug) > 0 {
		fmt.Fprintf(os.Stdout, "  needs debug:  %d flagged, newest first\n", len(debug))
		shown := debug
		if len(shown) > 3 {
			shown = shown[:3]
		}
		for _, feature := range shown {
			fmt.Fprintf(os.Stdout, "    %s %s (%d error(s))\n", feature.FeatureID, truncateTitle(feature.Title, 58), feature.ErrorCount)
		}
		if more := len(debug) - len(shown); more > 0 {
			fmt.Fprintf(os.Stdout, "    ... %d more (see 'sila soak --json')\n", more)
		}
	}
	for i, failure := range report.FailedTools {
		if i == 3 {
			fmt.Fprintf(os.Stdout, "    ... %d more failing tool(s)\n", len(report.FailedTools)-3)
			break
		}
		fmt.Fprintf(os.Stdout, "  failing tool: %s (%d failure(s), last %s)\n", failure.ToolName, failure.FailureCount, failure.LastOutcome)
	}
	fmt.Fprintln(os.Stdout, "  next actions:")
	for i, step := range soakActions(report) {
		fmt.Fprintf(os.Stdout, "    %d. %s\n", i+1, step)
	}
}

func soakingFeatureSoaks(ledger []store.FeatureSoakRecord) []store.FeatureSoakRecord {
	var soaking []store.FeatureSoakRecord
	for _, feature := range ledger {
		if feature.Status == "soaking" {
			soaking = append(soaking, feature)
		}
	}
	return soaking
}

func debugFeatureSoaks(ledger []store.FeatureSoakRecord) []store.FeatureSoakRecord {
	var debug []store.FeatureSoakRecord
	for _, feature := range ledger {
		if feature.Status == "needs_debug" || feature.ErrorCount > 0 {
			debug = append(debug, feature)
		}
	}
	sort.SliceStable(debug, func(i, j int) bool {
		return debug[i].LastVerifiedAt > debug[j].LastVerifiedAt
	})
	return debug
}

func truncateTitle(title string, limit int) string {
	if len(title) <= limit {
		return title
	}
	return title[:limit-3] + "..."
}

func soakActions(report release.SoakReport) []string {
	var actions []string
	if report.HarnessCount == 0 {
		actions = append(actions, "Call Sila through an MCP harness, or set SILA_AGENT, so events carry a harness name.")
	} else if report.HarnessCount < report.RequiredHarnesses {
		actions = append(actions, "Run any Sila tool from a second harness today.")
	}
	if report.ActiveDays < report.RequiredActiveDays {
		actions = append(actions, "Use Sila at least once today so this day counts as active.")
	}
	actions = append(actions, "Run 'sila release status' and 'sila standup last 24h' as the daily check.")
	if len(debugFeatureSoaks(report.FeatureLedger)) > 0 || len(report.FailedTools) > 0 || report.DiagnosticClusters > 0 {
		actions = append(actions, "Fix the flagged items above, then record a lesson with 'sila lesson'.")
	}
	if report.State == release.SoakReady || report.State == release.SoakUpToDate {
		actions = append(actions, "Soak complete: run 'sila release preflight' and cut the release.")
	} else {
		actions = append(actions, fmt.Sprintf("Re-check after the next active day; %.1fh remain in the window.", report.RemainingHours))
	}
	return actions
}
