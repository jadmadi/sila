package main

import (
	"fmt"
	"os"

	"github.com/jadmadi/sila/internal/render"
)

// runStandup implements `sila standup`: the human morning standup & executive briefing.
// It accepts natural lookback windows like `last 5h`, `last 2d`, `last 1w`, `last 30m`,
// or `5h`, `2d`, as well as `--global` / `-g` to summarize across all workspace projects.
// It displays multi-agent activity trendlines via internal/render sparklines.
func runStandup(ui render.UI, dbPath, project, sinceFlag, toolFlag string, top int, topSpecified, jsonOut, toonOut, activityOut, globalFlag bool, args []string) {
	if activityOut && globalFlag {
		fmt.Fprintln(os.Stderr, "sila standup: --activity requires a single project; --global is not supported")
		os.Exit(2)
	}

	sinceWindow, err := parseHumanStandupWindow(args, sinceFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila standup: %v\n", err)
		os.Exit(2)
	}

	if activityOut {
		runActivity(ui, dbPath, project, sinceWindow, toolFlag, top, topSpecified, jsonOut, toonOut)
		return
	}

	runHumanBriefing(ui, dbPath, project, sinceWindow, jsonOut, toonOut, globalFlag)
}

// renderStandupSessionSparkline formats a 7-day multi-agent session sparkline
// (e.g. `Sessions (7d):  ▂▃▅█▄  [42 total]`).
func renderStandupSessionSparkline(dailyCounts []int) string {
	if len(dailyCounts) == 0 {
		return ""
	}
	tot := 0
	for _, c := range dailyCounts {
		tot += c
	}
	spark := render.SparklineInts(dailyCounts)
	return fmt.Sprintf("Sessions (7d): %s [%d total]", spark, tot)
}
