package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jadmadi/sila/internal/cockpit"
	"github.com/jadmadi/sila/internal/config"
	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/ingest"
	"github.com/jadmadi/sila/internal/loaders"
	"github.com/jadmadi/sila/internal/release"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func runIngest(ui render.UI, dbPath, project string, jsonOut, verbose, full bool, since time.Time, tools []string) {
	st, err := store.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila: %s\n", friendlyStoreError(err))
		os.Exit(1)
	}
	defer st.Close()

	results, err := ingest.Run(st, ingest.Options{
		Tools:      tools,
		ProjectSel: project,
		Since:      since,
		Out:        os.Stderr,
		Verbose:    verbose,
		Full:       full,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila: %s\n", friendlyStoreError(err))
		os.Exit(1)
	}

	if jsonOut {
		out := make([]jsonResult, 0, len(results))
		for _, r := range results {
			jr := jsonResult{Tool: r.Tool, Messages: r.Messages, NewRecords: r.NewRecords, Merged: r.Merged, Overlaps: r.Overlaps}
			if r.Error != nil {
				jr.Error = r.Error.Error()
			}
			out = append(out, jr)
		}
		emitJSON(out)
		return
	}

	fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · ingest")))

	headers := []string{"tool", "status", "loaded", "new", "merged", "overlaps"}
	rows := make([][]string, 0, len(results))
	var totals struct{ new, merged, overlaps int64 }
	var wroteAny bool
	for _, r := range results {
		status, stCol := "ok", render.Green
		if r.Error != nil {
			status, stCol = "skip", render.Yellow
		} else {
			totals.new += int64(r.NewRecords)
			totals.merged += int64(r.Merged)
			totals.overlaps += int64(r.Overlaps)
			wroteAny = r.NewRecords > 0
		}
		rows = append(rows, []string{r.Tool, ui.Paint(stCol, status),
			ui.Dim(fmt.Sprint(r.Messages)),
			ui.Bold(fmt.Sprint(r.NewRecords)),
			ui.Dim(fmt.Sprint(r.Merged)),
			overlapCell(ui, r.Overlaps)})
	}
	fmt.Fprintln(os.Stdout, ui.Table(headers, rows, []string{"l", "l", "r", "r", "r", "r"}))

	summary := ui.Paint(render.Green, "Nothing new to write.")
	if totals.merged > 0 && totals.new == 0 {
		summary = ui.Dim(fmt.Sprintf("Nothing new; %d %s already known (deduped).", totals.merged, plural(totals.merged, "record")))
	} else if wroteAny {
		parts := fmt.Sprintf("Wrote %d new %s", totals.new, plural(totals.new, "record"))
		if totals.merged > 0 {
			parts += fmt.Sprintf(", merged %d duplicate %s", totals.merged, plural(totals.merged, "record"))
		}
		if totals.overlaps > 0 {
			parts += fmt.Sprintf("; %d cross-tool %s", totals.overlaps, plural(totals.overlaps, "overlap"))
		}
		summary = ui.Bold(ui.Yellow(parts + "."))
	}
	fmt.Fprintln(os.Stdout)
	fmt.Fprintln(os.Stdout, summary)
	if wroteAny {
		fmt.Fprintln(os.Stdout, actionLine(ui, "sila dream", "distill these messages into verified facts"))
	} else {
		fmt.Fprintln(os.Stdout, actionLine(ui, "sila status", "confirm health; nothing to write"))
	}
	fmt.Fprintln(os.Stdout, ui.Dim("Re-runs are incremental (per-tool watermark) and idempotent (content-hash dedup)."))
}

// runAssess reports what is discoverable and what a future ingest would pull in,
// without writing anything. The store is opened read-only and only when present.

func runSync(ui render.UI, dbPath, project string, watchMode, forceSync, dryRun, full, jsonOut, verbose bool, intervalStr string, since time.Time, isGlobal, compactMode bool, args []string) {
	cockpitMode := false
	for _, a := range args {
		if a == "--compact" || a == "-compact" {
			compactMode = true
		}
		if a == "cockpit" || a == "--cockpit" || a == "-cockpit" {
			cockpitMode = true
		}
	}
	if len(args) > 0 {
		first := args[0]
		if first == "watch" || first == "live" || first == "daemon" {
			watchMode = true
			if len(args) > 1 && !strings.HasPrefix(args[1], "-") {
				if args[1] == "global" || args[1] == "--global" {
					isGlobal = true
					project = ""
					if len(args) > 2 && (args[2] == "cockpit" || args[2] == "--cockpit") {
						cockpitMode = true
					}
				} else if args[1] == "cockpit" || args[1] == "--cockpit" {
					cockpitMode = true
				} else if project == "" {
					project = args[1]
					if len(args) > 2 && (args[2] == "cockpit" || args[2] == "--cockpit") {
						cockpitMode = true
					}
				}
			}
		} else if first == "global" {
			isGlobal = true
			project = ""
			if len(args) > 1 && (args[1] == "cockpit" || args[1] == "--cockpit") {
				cockpitMode = true
			}
		} else if first == "cockpit" || first == "--cockpit" {
			cockpitMode = true
		} else if !strings.HasPrefix(first, "-") && project == "" {
			project = first
			if len(args) > 1 && (args[1] == "cockpit" || args[1] == "--cockpit") {
				cockpitMode = true
			}
		}
	}

	if !watchMode {
		if dryRun {
			st, err := store.OpenRO(dbPath)
			if err != nil {
				fmt.Fprintf(os.Stderr, "sila: %s\n", friendlyStoreError(err))
				os.Exit(1)
			}
			defer st.Close()

			if !isGlobal && project == "" {
				if p, ok, err := st.ProjectForDir(""); err == nil && ok {
					project = p.ID
				}
			} else if project != "" {
				if p, err := st.ResolveProject(project); err == nil {
					project = p.ID
				}
			}

			if err := runSingleSyncCore(ui, st, project, true, full, jsonOut, verbose, since); err != nil {
				fmt.Fprintf(os.Stderr, "sila: %s\n", friendlyStoreError(err))
				os.Exit(1)
			}
			return
		}

		err := withSyncLease(dbPath, func() error {
			st, err := store.Open(dbPath)
			if err != nil {
				return err
			}
			defer st.Close()

			if !isGlobal && project == "" {
				if p, ok, err := st.ProjectForDir(""); err == nil && ok {
					project = p.ID
				}
			} else if project != "" {
				if p, err := st.ResolveProject(project); err == nil {
					project = p.ID
				}
			}

			if !forceSync {
				if liveInfo, active, _ := store.CheckLiveLockScoped(dbPath, project); active && liveInfo != nil {
					if jsonOut {
						emitJSON(map[string]any{
							"live_active": true,
							"live_pid":    liveInfo.PID,
							"started_at":  liveInfo.StartedAt,
							"project":     liveInfo.Project,
							"status":      "managed_by_live_sync",
							"message":     fmt.Sprintf("Live digestion is already running in process #%d", liveInfo.PID),
						})
						return nil
					}
					fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · sync"))+" — live digestion active")
					startedStr := "recently"
					if !liveInfo.StartedAt.IsZero() {
						startedStr = time.Since(liveInfo.StartedAt).Round(time.Second).String() + " ago"
					}
					scopeDesc := "globally across all projects"
					if liveInfo.Project != "" {
						scopeDesc = fmt.Sprintf("for project %s", ui.Bold(liveInfo.Project))
					}
					fmt.Fprintf(os.Stdout, "  %s Live knowledge sync is already active in background %s (PID #%d, started %s).\n",
						ui.Green("✔"), scopeDesc, liveInfo.PID, startedStr)
					fmt.Fprintln(os.Stdout, ui.Dim("  New AI messages and git commits are being ingested continuously. Skipping duplicate sync pass."))
					fmt.Fprintln(os.Stdout, ui.Dim("  (Pass --force if you need an immediate manual pass)."))
					return nil
				}
			}

			return runSingleSyncCore(ui, st, project, false, full, jsonOut, verbose, since)
		})
		if err != nil {
			handleSyncLeaseError(err, jsonOut)
		}
		return
	}

	var st *store.Store
	err := withSyncLease(dbPath, func() error {
		var openErr error
		st, openErr = store.Open(dbPath)
		return openErr
	})
	if err != nil {
		handleSyncLeaseError(err, jsonOut)
	}
	defer st.Close()

	var projPath string

	// Project auto-detection from CWD if not in global mode and no project specified
	if !isGlobal && project == "" {
		if p, ok, err := st.ProjectForDir(""); err == nil && ok {
			project = p.ID
			projPath = p.Path
		}
	} else if project != "" {
		if p, err := st.ResolveProject(project); err == nil {
			project = p.ID
			projPath = p.Path
		} else if info, err := os.Stat(project); err == nil && info.IsDir() {
			projPath, _ = filepath.Abs(project)
		}
	}

	// Watch Mode: Try to acquire single-instance live lock for the target scope
	releaseLock, err := store.AcquireLiveLockScoped(dbPath, project)
	if err != nil {
		if jsonOut {
			emitJSON(map[string]any{
				"error":   err.Error(),
				"blocked": true,
			})
			return
		}
		fmt.Fprintln(os.Stdout, ui.Yellow("•")+" "+ui.Bold(err.Error()))
		fmt.Fprintln(os.Stdout, ui.Dim("  Skipping duplicate live instance to prevent database lock contention."))
		return
	}
	defer releaseLock()

	interval, err := time.ParseDuration(intervalStr)
	if err != nil || interval < time.Second {
		interval = 15 * time.Second
	}

	if jsonOut {
		// Machine mode: one JSON object per line from the first byte, so
		// parsers never see human banner text on stdout.
		emitJSONLine(newWatchStartedEvent(os.Getpid(), dbPath, interval.String(), watchScopeName(project)))
	} else {
		fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · live sync"))+" — Continuous Background Knowledge Synchronization")
		fmt.Fprintf(os.Stdout, "  PID:      %s\n", ui.Bold(strconv.Itoa(os.Getpid())))
		fmt.Fprintf(os.Stdout, "  Store:    %s\n", dbPath)
		fmt.Fprintf(os.Stdout, "  Interval: %s (auto-polling tools + git + claim distillation)\n", ui.Bold(interval.String()))
		if project != "" {
			pathPart := ""
			if projPath != "" {
				pathPart = " " + ui.Dim("("+projPath+")")
			}
			fmt.Fprintf(os.Stdout, "  Scope:    %s%s\n", ui.Magenta(ui.Bold("📁 Project: "+project)), pathPart)
		} else {
			fmt.Fprintf(os.Stdout, "  Scope:    %s\n", ui.Cyan(ui.Bold("🌐 Global Workspace (All Registered Projects)")))
		}

		// Display initial goals roadmap snapshot if goals exist
		var gSum *goals.GoalTelemetrySummary
		if project != "" {
			targetGoalsDir := projPath
			if targetGoalsDir == "" {
				targetGoalsDir, _ = os.Getwd()
			}
			gSum, _ = goals.FormatProjectGoalsTelemetry(ui, targetGoalsDir)
		} else {
			gSum, _ = goals.FormatGlobalGoalsTelemetry(ui, st)
		}

		if gSum != nil && gSum.HasGoals {
			fmt.Fprintf(os.Stdout, "  Roadmap:  %s\n", gSum.RoadmapLine)
			if len(gSum.ActiveWorkers) > 0 {
				for _, w := range gSum.ActiveWorkers {
					fmt.Fprintf(os.Stdout, "  Worker:   👷 %s\n", ui.Cyan(w))
				}
			} else {
				fmt.Fprintf(os.Stdout, "  Workers:  %s\n", ui.Dim("none active"))
			}
			if gSum.DecisionHint != "" {
				fmt.Fprintf(os.Stdout, "  Next Up:  %s\n", ui.Bold(gSum.DecisionHint))
			}
		}
		fmt.Fprintln(os.Stdout, ui.Dim("  Press q or Ctrl+C to stop.\n"))
	}

	fetchGoals := func() *goals.GoalTelemetrySummary {
		if project != "" {
			targetGoalsDir := projPath
			if targetGoalsDir == "" {
				targetGoalsDir, _ = os.Getwd()
			}
			s, _ := goals.FormatProjectGoalsTelemetry(ui, targetGoalsDir)
			return s
		}
		s, _ := goals.FormatGlobalGoalsTelemetry(ui, st)
		return s
	}

	iteration := 1
	lastBeatHash := ""
	syncPass := func() {
		ts := time.Now().Format("15:04:05")
		passErr := withSyncLease(dbPath, func() error {
			results, err := ingest.Run(st, ingest.Options{
				Tools:      nil, // all
				ProjectSel: project,
				Since:      since,
				Out:        io.Discard,
				Verbose:    false,
			})
			if err != nil {
				fmt.Fprintf(os.Stderr, "[%s] %s %v\n", ui.Dim(ts), ui.Red("sync error:"), err)
				return nil
			}
			var newRec, merged, overlaps int64
			var activeTools []string
			var failedTools []string
			for _, r := range results {
				if r.Error == nil {
					newRec += int64(r.NewRecords)
					merged += int64(r.Merged)
					overlaps += int64(r.Overlaps)
					if r.NewRecords > 0 {
						activeTools = append(activeTools, r.Tool)
					}
				} else {
					failedTools = append(failedTools, r.Tool)
				}
			}

			produced, verifiedN, projCount := 0, 0, 0
			var activeProjects []string
			if newRec > 0 || merged > 0 {
				var err error
				produced, verifiedN, projCount, activeProjects, err = distill(st, project)
				if err != nil {
					fmt.Fprintf(os.Stderr, "[%s] %s %v\n", ui.Dim(ts), ui.Red("distill error:"), err)
					return nil
				}
			} else if iteration%240 == 0 {
				// A periodic reconciliation catches disk drift without rewriting every fact.
				if _, err := st.ReconcileDrift(project); err != nil {
					fmt.Fprintf(os.Stderr, "[%s] %s %v\n", ui.Dim(ts), ui.Red("reconcile error:"), err)
				}
				_, _, verifiedN, projCount, _ = st.Counts()
			} else {
				_, _, verifiedN, projCount, _ = st.Counts()
			}

			if newRec > 0 || produced > 0 {
				var parts []string
				if newRec > 0 {
					toolStr := ""
					if len(activeTools) > 0 {
						toolStr = " (" + strings.Join(activeTools, ", ") + ")"
					}
					parts = append(parts, fmt.Sprintf("%d new records ingested%s", newRec, toolStr))
				}
				if produced > 0 {
					projStr := ""
					if len(activeProjects) > 0 {
						projStr = " across " + strings.Join(activeProjects, ", ")
					}
					parts = append(parts, fmt.Sprintf("%d facts distilled (%d verified)%s", produced, verifiedN, projStr))
				}
				if len(failedTools) > 0 {
					parts = append(parts, fmt.Sprintf("%d failed sources (%s)", len(failedTools), strings.Join(failedTools, ", ")))
				}
				summary := strings.Join(parts, ", ")

				gSum := fetchGoals()
				var pulse string
				if gSum != nil && gSum.HasGoals {
					pulse = " | " + gSum.RoadmapPulse()
				}

				if jsonOut {
					roadmap, action := "", ""
					if gSum != nil && gSum.HasGoals {
						roadmap = gSum.RoadmapPulse()
						if len(gSum.ActiveWorkers) > 0 {
							action = gSum.ActionPulse(ui)
						}
					}
					emitJSONLine(newWatchStatusEvent(watchScopeName(project), newRec, produced, verifiedN, projCount, strings.Join(parts, " · "), roadmap, action))
				} else {
					fmt.Fprintf(os.Stdout, "[%s] %s %s%s\n", ui.Dim(ts), ui.Green("✔"), summary, pulse)
					if gSum != nil && gSum.HasGoals && len(gSum.ActiveWorkers) > 0 {
						fmt.Fprintln(os.Stdout, gSum.ActionPulse(ui))
					}
				}
				// Force the next idle heartbeat to print the post-event state.
				lastBeatHash = ""
			} else if iteration == 1 || verbose || iteration%4 == 0 {
				// Periodic heartbeat every ~1 minute. Piped output skips
				// byte-identical repeats so logs stay quiet while idle.
				var gSum *goals.GoalTelemetrySummary
				if project != "" {
					targetGoalsDir := projPath
					if targetGoalsDir == "" {
						targetGoalsDir, _ = os.Getwd()
					}
					gSum, _ = goals.FormatProjectGoalsTelemetry(ui, targetGoalsDir)
				} else {
					gSum, _ = goals.FormatGlobalGoalsTelemetry(ui, st)
				}

				syncPulse := ""
				if len(failedTools) > 0 {
					syncPulse = ui.Paint(render.Yellow, fmt.Sprintf("Partial sync (%d source error(s): %s)", len(failedTools), strings.Join(failedTools, ", ")))
				} else if project != "" {
					syncPulse = fmt.Sprintf("In sync for %s (0 pending · %d verified facts)", ui.Magenta(ui.Bold(project)), verifiedN)
				} else {
					syncPulse = fmt.Sprintf("In sync globally (0 pending across %d %s · %d verified facts)",
						projCount, plural(int64(projCount), "project"), verifiedN)
				}

				var actionLine string
				if gSum != nil && gSum.HasGoals {
					if pulse := gSum.RoadmapPulse(); pulse != "" {
						syncPulse += " | " + pulse
					}
					actionLine = gSum.ActionPulse(ui)
				}
				if !verbose && iteration != 1 {
					if h := watchStateHash(syncPulse, actionLine, verifiedN, projCount); h == lastBeatHash {
						iteration++
						return nil
					} else {
						lastBeatHash = h
					}
				} else if !verbose {
					lastBeatHash = watchStateHash(syncPulse, actionLine, verifiedN, projCount)
				}
				if jsonOut {
					roadmap := ""
					if gSum != nil && gSum.HasGoals {
						roadmap = gSum.RoadmapPulse()
					}
					emitJSONLine(newWatchStatusEvent(watchScopeName(project), 0, 0, verifiedN, projCount, syncPulse, roadmap, actionLine))
				} else if gSum != nil && gSum.HasGoals {
					fmt.Fprintf(os.Stdout, "[%s] %s %s\n", ui.Dim(ts), ui.Dim("•"), syncPulse)
					if actionLine != "" {
						fmt.Fprintln(os.Stdout, actionLine)
					}
				} else {
					fmt.Fprintf(os.Stdout, "[%s] %s %s\n", ui.Dim(ts), ui.Dim("•"), syncPulse)
				}
			}
			iteration++
			return nil
		})
		if passErr != nil {
			if errors.Is(passErr, store.ErrSyncBusy) {
				if jsonOut {
					emitJSONLine(defaultWatchSyncBusyEvent)
				} else {
					fmt.Fprintf(os.Stdout, "[%s] %s another sync pass is running; skipping iteration\n", ui.Dim(ts), ui.Yellow("•"))
				}
				return
			}
			fmt.Fprintf(os.Stderr, "[%s] %s %v\n", ui.Dim(ts), ui.Red("sync error:"), passErr)
			return
		}
	}

	// Full-screen interactive 9-box agent mission control cockpit on TTYs ONLY when explicitly requested
	if isStdoutTTY() && !jsonOut && !verbose && cockpitMode {
		_ = cockpit.RunLive(context.Background(), st, project, projPath, interval, syncPass)
		return
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	ignoreTerminalFlowSignals()

	// Enable single-keypress 'q' to quit (raw terminal mode on Linux).
	restoreStdin := setupRawStdin()
	defer restoreStdin()
	quitChan := make(chan struct{}, 1)
	go func() {
		buf := make([]byte, 1)
		for {
			n, err := os.Stdin.Read(buf)
			if err != nil || n == 0 {
				return
			}
			if buf[0] == 'q' || buf[0] == 'Q' {
				quitChan <- struct{}{}
				return
			}
		}
	}()

	// Interactive terminals default to the lightweight in-place progress view so
	// idle polls never scroll and background synchronization stays visible.
	// Pipes keep append mode, and --verbose/--json explicitly request scrolling per-tick logs.
	if isStdoutTTY() && !jsonOut && !verbose {
		runWatchInPlaceLoop(ui, st, project, projPath, interval, since, sigChan, quitChan)
		return
	}

	syncPass()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-sigChan:
			if jsonOut {
				emitJSONLine(newWatchStoppedEvent())
			} else {
				fmt.Fprintln(os.Stdout, "\n"+ui.Green("✔")+" Continuous live sync stopped cleanly.")
			}
			return
		case <-quitChan:
			if jsonOut {
				emitJSONLine(newWatchStoppedEvent())
			} else {
				fmt.Fprintln(os.Stdout, "\n"+ui.Green("✔")+" Continuous live sync stopped cleanly.")
			}
			return
		case <-ticker.C:
			syncPass()
		}
	}
}

func runSingleSync(ui render.UI, st *store.Store, project string, dryRun, full, jsonOut, verbose bool, since time.Time) {
	if err := runSingleSyncCore(ui, st, project, dryRun, full, jsonOut, verbose, since); err != nil {
		fmt.Fprintf(os.Stderr, "sila: %s\n", friendlyStoreError(err))
		os.Exit(1)
	}
}

func runSingleSyncCore(ui render.UI, st *store.Store, project string, dryRun, full, jsonOut, verbose bool, since time.Time) error {
	isGlobal := project == ""
	prog := newSyncProgress(ui, isStdoutTTY(), verbose, jsonOut, isGlobal, project)

	// Discover and drain unmerged records from workspace-local stores (.sila/sila.db)
	// when operating against the primary global store.
	if !dryRun && !store.IsWorkspaceStore(st.Path) {
		cwd, _ := os.Getwd()
		wsDB := config.WorkspaceDBPath(cwd)
		if wsDB != "" && wsDB != st.Path {
			if _, err := os.Stat(wsDB); err == nil {
				if stats, err := st.DrainWorkspaceStore(wsDB); err == nil && stats.Total() > 0 {
					if !jsonOut {
						fmt.Fprintf(os.Stdout, "  %s Drained %d local session records from .sila/sila.db into primary store.\n",
							ui.Green("✔"), stats.Total())
					}
				}
			}
		}
		if isGlobal {
			if projs, err := st.Projects(); err == nil {
				for _, p := range projs {
					pWS := config.WorkspaceDBPath(p.Path)
					if pWS != "" && pWS != st.Path && pWS != wsDB {
						if _, err := os.Stat(pWS); err == nil {
							if stats, err := st.DrainWorkspaceStore(pWS); err == nil && stats.Total() > 0 {
								if !jsonOut {
									fmt.Fprintf(os.Stdout, "  %s Drained %d local records from %s into primary store.\n",
										ui.Green("✔"), stats.Total(), pWS)
								}
							}
						}
					}
				}
			}
		}
	}

	results, err := ingest.Run(st, ingest.Options{
		Tools:      nil, // all
		ProjectSel: project,
		Since:      since,
		Out:        os.Stderr,
		Verbose:    verbose,
		DryRun:     dryRun,
		Full:       full,
		OnProgress: prog.OnToolProgress,
	})
	if err != nil {
		prog.Finish()
		return err
	}
	var wroteAny bool
	var newRec, merged, overlaps int64
	for _, r := range results {
		if r.Error == nil {
			newRec += int64(r.NewRecords)
			merged += int64(r.Merged)
			overlaps += int64(r.Overlaps)
			if r.NewRecords > 0 {
				wroteAny = true
			}
		}
	}
	missingSources := 0
	for _, r := range results {
		if r.Error != nil && loaders.IsMissingSource(r.Error) {
			missingSources++
		}
	}

	allProjects, _ := st.Projects()
	var targetProjCount int
	for _, p := range allProjects {
		if project == "" || projSelMatch(p, project) {
			targetProjCount++
		}
	}

	var produced, verifiedN, projCount int
	var vacReclaimed int64
	var vacPct float64
	var vacDur time.Duration

	if dryRun {
		// In dry-run mode, query current stats without mutating facts or running vacuum
		allFacts, _ := st.FactsAll()
		if project != "" {
			allFacts = filterProject(allFacts, project)
		}
		for _, f := range allFacts {
			if f.FileRefsExist || f.Verified {
				verifiedN++
			}
		}
		produced = len(allFacts)
		projCount = targetProjCount
		if stats, err := st.Stats(); err == nil && stats != nil {
			vacReclaimed = stats.FreeBytes
			vacPct = stats.FragmentationPct
		}
		prog.Finish()

		if jsonOut {
			emitJSON(jsonSync{
				DryRun:          true,
				MessagesWritten: newRec,
				Merged:          merged,
				Overlaps:        overlaps,
				FactsDistilled:  produced,
				Verified:        verifiedN,
				Projects:        projCount,
				SourcesMissing:  missingSources,
				AutoVacuumBytes: vacReclaimed,
			})
			return nil
		}

		indent := "  "
		title := ui.Cyan(ui.Bold("sila · sync")) + " " + ui.Yellow("[PREVIEW / DRY-RUN]")
		if project != "" {
			title += fmt.Sprintf(" (%s %s)", ui.Dim("scoped to"), ui.Bold(project))
		}
		fmt.Fprintln(os.Stdout, title)
		msg := "no new records detected to ingest"
		if newRec > 0 {
			msg = fmt.Sprintf("%d new records would be ingested", newRec)
			if overlaps > 0 {
				msg += fmt.Sprintf(", %d cross-tool %s", overlaps, plural(overlaps, "overlap"))
			}
		}
		fmt.Fprintf(os.Stdout, "%s%s.\n", indent, ui.Yellow(msg))
		fmt.Fprintf(os.Stdout, "%s%d projects targeted for claim distillation (%d verified facts currently).\n", indent, targetProjCount, verifiedN)
		if vacReclaimed > 0 {
			fmt.Fprintf(os.Stdout, "%s%s (%0.1f%% fragmentation) reclaimable via vacuum.\n", indent, render.FormatBytes(vacReclaimed), vacPct)
		} else {
			fmt.Fprintf(os.Stdout, "%sstorage is compact (0 bytes reclaimable).\n", indent)
		}
		fmt.Fprintln(os.Stdout, ui.Dim(indent+"(Dry-run: no database changes were written)."))
		return nil
	}

	prog.StartDistill(targetProjCount)

	produced, verifiedN, projCount, _, err = distillWithProgress(st, project, full, prog.OnDistillProgress)
	if err != nil {
		prog.Finish()
		return err
	}

	prog.StartVacuum()
	// Auto-vacuum if fragmentation or freelist exceeds threshold
	if vacuumed, vRes, _ := st.AutoVacuumIfNeeded(100, 24*time.Hour); vacuumed && vRes != nil {
		vacReclaimed = vRes.ReclaimedBytes
		vacPct = vRes.ReclaimedPct
		vacDur = vRes.Duration
	}
	release.SyncSoakDailyIfDue("", st.Path)
	ensureDaemonRunning(st.Path)

	prog.Finish()

	if jsonOut {
		emitJSON(jsonSync{
			MessagesWritten: newRec, Merged: merged, Overlaps: overlaps,
			FactsDistilled: produced, Verified: verifiedN, Projects: projCount,
			SourcesMissing: missingSources, AutoVacuumBytes: vacReclaimed,
		})
		return nil
	}

	indent := "  "
	if verbose {
		indent = "    "
	} else {
		title := ui.Cyan(ui.Bold("sila · sync"))
		if project != "" {
			title += fmt.Sprintf(" (%s %s)", ui.Dim("scoped to"), ui.Bold(project))
		}
		fmt.Fprintln(os.Stdout, title)
	}
	state := ui.Paint(render.Green, "nothing new to ingest")
	if wroteAny {
		msg := fmt.Sprintf("ingested %d new %s", newRec, plural(newRec, "record"))
		if merged > 0 {
			msg += fmt.Sprintf(", %d merged", merged)
		}
		if overlaps > 0 {
			msg += fmt.Sprintf(", %d cross-tool %s", overlaps, plural(overlaps, "overlap"))
		}
		state = ui.Bold(ui.Yellow(msg))
	}
	factLine := ui.Bold(fmt.Sprintf("distilled %d %s (%d verified) across %d %s",
		produced, plural(int64(produced), "fact"), verifiedN, projCount, plural(int64(projCount), "project")))
	fmt.Fprintln(os.Stdout, indent+state+".")
	if project != "" && !wroteAny {
		fmt.Fprintln(os.Stdout, ui.Dim(indent+"Tip: Pass --global to sync all projects across the workspace."))
	}
	fmt.Fprintln(os.Stdout, indent+factLine+".")
	if vacReclaimed > 0 {
		fmt.Fprintln(os.Stdout, ui.Dim(fmt.Sprintf(indent+"reclaimed %s (%.1f%%) via background auto-vacuum in %s",
			render.FormatBytes(vacReclaimed), vacPct, vacDur.Round(time.Millisecond))))
	}
	fmt.Fprintln(os.Stdout, ui.Dim("Run `sila dream query <term>` to search the distilled facts."))
	return nil
}

// jsonSync is the machine-readable sync summary.
type jsonSync struct {
	DryRun          bool  `json:"dry_run,omitempty"`
	MessagesWritten int64 `json:"messages_written"`
	Merged          int64 `json:"merged"`
	Overlaps        int64 `json:"overlaps"`
	FactsDistilled  int   `json:"facts_distilled"`
	Verified        int   `json:"verified"`
	Projects        int   `json:"projects"`
	SourcesMissing  int   `json:"sources_missing"`
	AutoVacuumBytes int64 `json:"auto_vacuum_bytes,omitempty"`
}
