package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/memory"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func runMemory(ui render.UI, dbPath, project, since, olderThan string, yes, jsonOut, toonOut bool, args []string) {
	sub := "report"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub = args[0]
	}
	st, err := store.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila memory: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()

	switch sub {
	case "report":
		runMemoryReport(ui, st, project, since, jsonOut, toonOut)
	case "prune":
		if !yes {
			fmt.Fprintln(os.Stderr, "sila memory prune: pass --yes to delete events")
			os.Exit(2)
		}
		age, err := parseSessionAge(olderThan)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila memory prune: %v\n", err)
			os.Exit(2)
		}
		deleted, err := st.PruneMemoryEvents(time.Now().UTC().Add(-age))
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila memory prune: %v\n", err)
			os.Exit(1)
		}
		if jsonOut {
			emitJSON(map[string]any{"deleted": deleted, "older_than": age.String()})
			return
		}
		fmt.Fprintf(os.Stdout, "%s Pruned %d memory event(s) older than %s\n", ui.Green("✔"), deleted, age)
	default:
		fmt.Fprintf(os.Stderr, "sila memory: unknown subcommand %q (valid: report, prune)\n", sub)
		os.Exit(2)
	}
}

func runMemoryReport(ui render.UI, st *store.Store, project, sinceRaw string, jsonOut, toonOut bool) {
	since := time.Now().UTC().Add(-7 * 24 * time.Hour)
	if strings.TrimSpace(sinceRaw) != "" {
		since = parseSince(sinceRaw)
	}
	until := time.Now().UTC()
	events, err := st.ListMemoryEvents(store.MemoryEventFilter{ProjectID: project, Since: since, Until: until, Limit: 100000})
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila memory report: %v\n", err)
		os.Exit(1)
	}
	cwd, _ := os.Getwd()
	goals := memory.CollectGoalStats(cwd, since)
	report := memory.BuildReport(events, goals, project, since, until)
	report.Verdict = memory.Verdict(report)
	if jsonOut || toonOut {
		emitJSON(report)
		return
	}
	fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · memory adoption"))+" — "+fmt.Sprintf("%s → %s", report.Window.Since, report.Window.Until))
	fmt.Fprintln(os.Stdout, "\n  Events")
	for _, verb := range []string{"prime", "recall", "memo", "lesson", "case", "facts", "gate", "handoff", "sync"} {
		counts := report.Events[verb]
		fmt.Fprintf(os.Stdout, "    %-10s ok=%d empty=%d error=%d skipped=%d\n", verb, counts["ok"], counts["empty"], counts["error"], counts["skipped"])
	}
	fmt.Fprintf(os.Stdout, "\n  Sessions: total=%d prime=%d (%.0f%%) recall=%d (%.0f%%) handoff=%d (%.0f%%)\n",
		report.Sessions.Total, report.Sessions.Prime, report.Sessions.PrimeRate*100,
		report.Sessions.Recall, report.Sessions.RecallRate*100, report.Sessions.Handoff, report.Sessions.HandoffRate*100)
	fmt.Fprintf(os.Stdout, "  Goals:    implemented=%d required=%d legacy=%d valid_memory=%d (%.0f%%)\n",
		report.Goals.Implemented, report.Goals.Required, report.Goals.Legacy,
		report.Goals.WithValidMemoryBlock, report.Goals.WriteComplianceRate*100)
	fmt.Fprintf(os.Stdout, "  Gate:     pass=%d fail=%d waiver=%d skip=%d\n",
		report.Gate.Pass, report.Gate.Fail, report.Gate.Waiver, report.Gate.Skip)
	fmt.Fprintf(os.Stdout, "  Unattributed events: %d\n", report.UnattributedEvents)
	fmt.Fprintf(os.Stdout, "  Verdict:  %s\n", strings.ToUpper(report.Verdict))
}
