package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/store"
)

// checkStatsFlags rejects flag mixes that have no meaning. Exit code 2 is the
// usage-error code used across sila.
func (gc *goalsContext) checkStatsFlags() {
	machine := gc.jsonOut || gc.toonOut
	var msg string
	switch {
	case gc.lineMode && gc.fullMode:
		msg = "--line and --full cannot be combined"
	case (gc.lineMode || gc.liveMode) && machine:
		msg = "--line and live cannot be combined with --json or --toon"
	case gc.fullMode && !gc.liveMode:
		msg = "--full is only valid with live (sila goals stats live --full)"
	}
	if msg != "" {
		fmt.Fprintf(os.Stderr, "sila goals stats: %s\n", msg)
		os.Exit(2)
	}
}

// scanGlobal scans every registered project and applies the tier filter.
func (gc *goalsContext) scanGlobal(st *store.Store) (*goals.GlobalAuditResult, error) {
	globalRes, err := goals.ScanAllProjectsGoals(st)
	if err != nil {
		return nil, err
	}
	if gc.tierOpt == "" {
		return globalRes, nil
	}

	filteredProjects := make(map[string]*goals.AuditResult)
	globalRes.TotalGoals = 0
	globalRes.ImplementedCount = 0
	globalRes.PendingCount = 0
	globalRes.ClaimedCount = 0
	globalRes.BlockedCount = 0
	globalRes.ContestedCount = 0
	globalRes.IndependentCount = 0
	globalRes.DraftCount = 0
	globalRes.ImmediateCount = 0
	globalRes.RoadmapCount = 0
	globalRes.NiceToHaveCount = 0
	globalRes.WontFixCount = 0
	globalRes.TotalTasks = 0
	globalRes.CompletedTasks = 0
	totalProjects := 0

	for pID, pAudit := range globalRes.Projects {
		pFiltered := pAudit.FilterByTier(gc.tierOpt)
		if pFiltered.Total > 0 {
			filteredProjects[pID] = pFiltered
			totalProjects++
			globalRes.TotalGoals += pFiltered.Total
			globalRes.ImplementedCount += pFiltered.ImplementedCount
			globalRes.PendingCount += pFiltered.PendingCount
			globalRes.ClaimedCount += pFiltered.ClaimedCount
			globalRes.BlockedCount += pFiltered.BlockedCount
			globalRes.ContestedCount += pFiltered.ContestedCount
			globalRes.IndependentCount += pFiltered.IndependentCount
			globalRes.DraftCount += pFiltered.DraftCount
			globalRes.ImmediateCount += pFiltered.ImmediateCount
			globalRes.RoadmapCount += pFiltered.RoadmapCount
			globalRes.NiceToHaveCount += pFiltered.NiceToHaveCount
			globalRes.WontFixCount += pFiltered.WontFixCount
			globalRes.TotalTasks += pFiltered.TotalTasks
			globalRes.CompletedTasks += pFiltered.CompletedTasks
		}
	}
	globalRes.Projects = filteredProjects
	globalRes.TotalProjects = totalProjects
	return globalRes, nil
}

// projectStatsFrame returns a frame writer for one project. Line frames carry
// no trailing newline; full frames carry their own.
func (gc *goalsContext) projectStatsFrame(dir string) func(io.Writer) error {
	return func(w io.Writer) error {
		res, err := goals.ScanGoals(dir)
		if err != nil {
			return err
		}
		if gc.tierOpt != "" {
			res = res.FilterByTier(gc.tierOpt)
		}
		if gc.fullMode {
			goals.FprintProjectStatsTable(w, gc.ui, res)
			return nil
		}
		_, err = io.WriteString(w, goals.RenderStatsLine(gc.ui, res.StatsCounts()))
		return err
	}
}

// globalStatsFrame returns a frame writer across all projects.
func (gc *goalsContext) globalStatsFrame(st *store.Store) func(io.Writer) error {
	return func(w io.Writer) error {
		res, err := gc.scanGlobal(st)
		if err != nil {
			return err
		}
		if gc.fullMode {
			goals.FprintGlobalStatsTable(w, gc.ui, res)
			return nil
		}
		_, err = io.WriteString(w, goals.RenderStatsLine(gc.ui, res.StatsCounts()))
		return err
	}
}

// runStatsFrames runs the one-line (--line) or live view. Without live it
// prints one line. With live it refreshes until Ctrl-C.
func (gc *goalsContext) runStatsFrames(frame func(io.Writer) error) {
	interval := goals.DefaultStatsLiveInterval
	if gc.intervalOpt != "" {
		d, err := time.ParseDuration(gc.intervalOpt)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila goals stats: invalid --interval %q: %v\n", gc.intervalOpt, err)
			os.Exit(2)
		}
		var raised bool
		if interval, raised = goals.ClampStatsLiveInterval(d); raised {
			fmt.Fprintf(os.Stderr, "sila goals stats: --interval raised to %s (minimum)\n", interval)
		}
	}

	if !gc.liveMode {
		var buf bytes.Buffer
		if err := frame(&buf); err != nil {
			fmt.Fprintln(os.Stdout, "Goals: ?")
			fmt.Fprintf(os.Stderr, "sila goals stats: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintln(os.Stdout, buf.String())
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	_ = goals.RunStatsLive(ctx, os.Stdout, goals.StatsLiveOptions{
		Interval: interval,
		Full:     gc.fullMode,
		TTY:      gc.ui.IsTerminal(),
		Render:   frame,
		Errw:     os.Stderr,
	})
}
