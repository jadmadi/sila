package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/adoption"
	"github.com/jadmadi/sila/internal/cockpit"
	"github.com/jadmadi/sila/internal/memory"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

// runStats executes the one-shot 9-box mission control cockpit snapshot (`sila stats`).
func runStats(ui render.UI, dbPath, project string, jsonOut, toonOut, plain bool, args []string) {
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") && project == "" {
		project = args[0]
	}

	st, err := store.OpenRO(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila stats: open database: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()

	cwd, _ := os.Getwd()
	projPath := cwd
	if project != "" {
		if p, err := st.ResolveProject(project); err == nil {
			project = p.ID
			projPath = p.Path
		}
	} else {
		if p, ok, err := st.ProjectForDir(cwd); err == nil && ok {
			project = p.ID
			projPath = p.Path
		}
	}

	snap, err := cockpit.CollectSnapshot(context.Background(), st, project, projPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila stats: collect telemetry: %v\n", err)
		os.Exit(1)
	}

	if jsonOut {
		jsonStr, err := snap.ToJSON()
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila stats: serialize json: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(jsonStr)
		return
	}

	if toonOut {
		fmt.Print(snap.ToTOON())
		return
	}

	w := termWidth()
	colorEnabled := !plain && ui.ColorEnabled()
	if plain {
		colorEnabled = false
	}
	output := cockpit.RenderSnapshot(snap, w, colorEnabled)
	fmt.Print(output)
	since := time.Now().UTC().Add(-7 * 24 * time.Hour)
	if events, err := st.ListMemoryEvents(store.MemoryEventFilter{ProjectID: project, Since: since, Limit: 100000}); err == nil {
		report := memory.BuildReport(events, memory.CollectGoalStats(cwd, since), project, since, time.Now().UTC())
		fmt.Fprintf(os.Stdout, "memory: prime %d/%d · writes %d/%d · handoff %d/%d\n",
			report.Sessions.Prime, report.Sessions.Total,
			report.Goals.WithValidMemoryBlock, report.Goals.Implemented,
			report.Sessions.Handoff, report.Sessions.Total)
	}
	if funnel, err := adoption.BuildFunnelReport(context.Background(), st, adoption.ReportOptions{ProjectID: project, Since: since}); err == nil {
		fmt.Fprintf(os.Stdout, "adoption: prime %d/%d · writes %d/%d · handoff %d/%d · tools %d/%d\n",
			funnel.Sessions.Prime, funnel.Sessions.Total,
			funnel.Sessions.Writes, funnel.Sessions.Total,
			funnel.Sessions.Handoff, funnel.Sessions.Total,
			funnel.Coverage.Used, funnel.Coverage.Exposed)
	}
}
