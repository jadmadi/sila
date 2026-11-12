package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func (gc *goalsContext) renderGlobalAudit() {
	ui := gc.ui
	st := gc.st
	dbPath := gc.dbPath
	tierOpt := gc.tierOpt
	statsMode := gc.statsMode
	jsonOut := gc.jsonOut
	toonOut := gc.toonOut
	filterMode := gc.filterMode
	if st == nil {
		s, err := store.OpenRO(dbPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila goals: %v\n", err)
			os.Exit(1)
		}
		defer s.Close()
		st = s
	}

	if statsMode {
		gc.checkStatsFlags()
		if gc.lineMode || gc.liveMode {
			gc.runStatsFrames(gc.globalStatsFrame(st))
			return
		}
	}

	globalRes, err := gc.scanGlobal(st)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila goals: %v\n", err)
		os.Exit(1)
	}

	if statsMode {
		if jsonOut {
			emitJSON(globalRes.ToStatsPayload())
			return
		}
		if toonOut {
			fmt.Print(globalRes.ToStatsTOON())
			return
		}
		goals.PrintGlobalStatsTable(ui, globalRes)
		return
	}

	if jsonOut {
		emitJSON(globalRes)
		return
	}

	if globalRes.TotalProjects == 0 {
		if tierOpt != "" {
			fmt.Fprintf(os.Stdout, "%s Zero %s tier goals found across all registered projects.\n",
				ui.Yellow("▲"), ui.Bold(tierOpt))
			return
		}
		fmt.Fprintln(os.Stdout, ui.Dim("No registered projects with goals/ directories found."))
		return
	}

	goals.PrintGlobalAuditTable(ui, globalRes, filterMode)
}

func (gc *goalsContext) renderAudit(targetDir string) {
	ui := gc.ui
	st := gc.st
	tierOpt := gc.tierOpt
	noSync := gc.noSync
	statsMode := gc.statsMode
	jsonOut := gc.jsonOut
	toonOut := gc.toonOut
	sessionID := gc.sessionID
	checkMode := gc.checkMode
	filterMode := gc.filterMode
	showAll := gc.showAll
	stage := gc.stage
	commitMsg := gc.commitMsg

	if targetDir == "" {
		targetDir, _ = goals.ResolveProjectDir(st, "")
	}

	if statsMode {
		gc.checkStatsFlags()
		if gc.lineMode || gc.liveMode {
			gc.runStatsFrames(gc.projectStatsFrame(targetDir))
			return
		}
	}

	res, err := goals.ScanGoals(targetDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila goals error: %v\n", err)
		os.Exit(1)
	}

	if tierOpt != "" {
		res = res.FilterByTier(tierOpt)
		noSync = true
	}

	if statsMode {
		if jsonOut {
			emitJSON(res.ToStatsPayload())
			return
		}
		if toonOut {
			fmt.Print(res.ToStatsTOON())
			return
		}
		goals.PrintProjectStatsTable(ui, res)
		return
	}

	// Session-scoped reads are deterministic: exactly the goals whose claim
	// carries the session id, never a transcript heuristic.
	if sessionID != "" {
		matched := goals.GoalsBySession(res, sessionID)
		sessionRow := func(g goals.Goal) []string {
			agentName := "agent"
			statusName := "in_progress"
			if g.Claim != nil {
				if g.Claim.ClaimedBy != "" {
					agentName = g.Claim.ClaimedBy
				}
				if g.Claim.Status != "" {
					statusName = g.Claim.Status
				}
			}
			tasks := ""
			if g.TasksTotal > 0 {
				tasks = fmt.Sprintf("%d/%d", g.TasksMet, g.TasksTotal)
			}
			return []string{g.Slug, agentName, statusName, tasks, g.Title}
		}
		if jsonOut {
			emitJSON(map[string]any{"project": res.Project, "filter": "claimed", "session": sessionID, "count": len(matched), "goals": matched})
			return
		}
		if toonOut {
			rows := make([][]string, 0, len(matched))
			for _, g := range matched {
				rows = append(rows, sessionRow(g))
			}
			fmt.Fprint(os.Stdout, render.FormatTOONTable("session_goals", []string{"slug", "agent", "status", "tasks", "title"}, rows))
			return
		}
		if len(matched) == 0 {
			fmt.Fprintf(os.Stdout, "%s No goals bound to session %s in %s.\n", ui.Yellow("▲"), ui.Bold(sessionID), ui.Magenta(ui.Bold(res.Project)))
			return
		}
		fmt.Fprintf(os.Stdout, "%s (%d):\n\n", ui.Cyan(ui.Bold("sila · goals · session "+sessionID)), len(matched))
		rows := make([][]string, 0, len(matched))
		for _, g := range matched {
			rows = append(rows, sessionRow(g))
		}
		fmt.Fprintln(os.Stdout, ui.Table([]string{"slug", "agent", "status", "tasks", "title"}, rows, []string{"l", "l", "l", "r", "l"}))
		return
	}

	// Machine-readable output is read-only by default. `--sync` explicitly
	// opts machine modes into claim cleanup and README reconciliation; human
	// output keeps the historical auto-sync behavior unless --no-sync.
	machineRead := jsonOut || toonOut || outModes.porcelain
	var clearedClaims []string
	var mutations []string
	syncPerformed := false
	if !noSync && !checkMode && filterMode == "" && res.Total > 0 && (!machineRead || outModes.sync) {
		rootDir := goals.ResolveGoalsRootDir(targetDir)
		readmePath := filepath.Join(rootDir, "goals", "README.md")
		before, _ := os.ReadFile(readmePath)

		var err error
		clearedClaims, err = goals.ClearClosedClaims(targetDir, res)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila goals: clear closed claims: %v\n", err)
			os.Exit(1)
		}
		if err := goals.SyncReadmeWithOpts(targetDir, res, goals.SyncReadmeOptions{Stage: stage, CommitMsg: commitMsg, ExtraPaths: clearedClaims}); err != nil {
			fmt.Fprintf(os.Stderr, "sila goals: sync README: %v\n", err)
			os.Exit(1)
		}
		after, _ := os.ReadFile(readmePath)
		if !bytes.Equal(before, after) {
			mutations = append(mutations, "goals/README.md")
		}
		mutations = append(mutations, clearedClaims...)
		syncPerformed = true
	}

	matchesFilter := func(g goals.Goal) bool {
		switch filterMode {
		case "implemented", "done":
			return g.Status == "implemented"
		case "claimed":
			return g.Status == "claimed"
		case "ready":
			return !g.Blocked && len(g.FileConflicts) == 0 && g.Status == "pending"
		case "blocked":
			return g.Blocked && g.Status == "pending"
		case "pending":
			return g.Status == "pending"
		default:
			return true
		}
	}

	if jsonOut && !checkMode {
		gc.annotateContract(res, targetDir)
		if filterMode != "" {
			type blockedGoalPayload struct {
				goals.Goal
				Blockers []goals.DependencyBlocker `json:"blockers,omitempty"`
			}
			var filtered []any
			for _, g := range res.Goals {
				if !matchesFilter(g) {
					continue
				}
				if g.Blocked {
					blockers := goals.ResolveGoalBlockers(targetDir, g.Slug)
					filtered = append(filtered, blockedGoalPayload{
						Goal:     g,
						Blockers: blockers,
					})
				} else {
					filtered = append(filtered, g)
				}
			}
			payload := map[string]any{"project": res.Project, "filter": filterMode, "count": len(filtered), "goals": filtered}
			if syncPerformed {
				emitEnvelope(payload, nil, mutations)
			} else {
				emitJSON(payload)
			}
			return
		}
		if syncPerformed {
			emitEnvelope(res, nil, mutations)
		} else {
			emitJSON(res)
		}
		return
	}

	if toonOut {
		fmt.Fprint(os.Stdout, goals.RenderGoalsTOON(res, filterMode))
		return
	}

	if outModes.porcelain {
		for _, g := range res.Goals {
			if !matchesFilter(g) {
				continue
			}
			blocked := "0"
			if g.Blocked {
				blocked = "1"
			}
			title := strings.NewReplacer("\t", " ", "\n", " ").Replace(g.Title)
			fmt.Fprintf(os.Stdout, "%s\t%s\t%s\t%s\t%d/%d\t%s\n", g.Slug, g.Status, g.Tier, blocked, g.TasksMet, g.TasksTotal, title)
		}
		return
	}

	if res.Total == 0 {
		if jsonOut && checkMode {
			emitJSON(map[string]any{
				"in_sync":     true,
				"total":       0,
				"implemented": 0,
				"verified":    0,
				"unverified":  0,
				"stale":       0,
				"claimed":     0,
				"pending":     0,
			})
			return
		}
		if tierOpt != "" {
			fmt.Fprintf(os.Stdout, "%s Zero %s tier goals found in project %s.\n",
				ui.Yellow("▲"), ui.Bold(tierOpt), ui.Magenta(ui.Bold(res.Project)))
			return
		}
		projName := res.Project
		if projName == "" {
			projName = filepath.Base(targetDir)
		}
		fmt.Fprintf(os.Stdout, "%s No goals/ directory found in project %s %s\n",
			ui.Cyan(ui.Bold("sila · goals")),
			ui.Magenta(ui.Bold(projName)),
			ui.Dim("("+targetDir+")"),
		)
		fmt.Fprintln(os.Stdout, ui.Dim("help: adopt the roadmap with `sila goals create <slug> --title=\"...\" --files=\"path,...\"`"))
		return
	}

	if checkMode {
		if err := goals.CheckReadmeSync(targetDir, res); err != nil {
			if jsonOut {
				emitJSON(map[string]any{
					"in_sync":     false,
					"error":       err.Error(),
					"total":       res.Total,
					"implemented": res.ImplementedCount,
					"verified":    res.VerifiedCount,
					"unverified":  res.UnverifiedCount,
					"stale":       res.StaleCount,
					"unbound":     res.UnboundCount,
					"claimed":     res.ClaimedCount,
					"pending":     res.PendingCount,
				})
			} else {
				fmt.Fprintf(os.Stderr, "%s sila goals --check: %v\n", ui.Red("✖"), err)
			}
			os.Exit(1)
		}
		if jsonOut {
			emitJSON(map[string]any{
				"in_sync":     true,
				"total":       res.Total,
				"implemented": res.ImplementedCount,
				"verified":    res.VerifiedCount,
				"unverified":  res.UnverifiedCount,
				"stale":       res.StaleCount,
				"unbound":     res.UnboundCount,
				"claimed":     res.ClaimedCount,
				"pending":     res.PendingCount,
			})
			return
		}
		goals.PrintAuditTable(ui, res, filterMode, showAll)
		fmt.Fprintln(os.Stdout, ui.Green("\n✔")+" goals/README.md is in sync with roadmap state (check passed)")
		return
	}

	if goalsSyncNotice(noSync, false, outModes.quiet, filterMode, res.Total) {
		marker := "ℹ"
		if gc.ascii {
			marker = "[i]"
		}
		fmt.Fprintf(os.Stdout, "%s goals/README.md will be reconciled after this report (use --no-sync to preview only)\n\n", ui.Dim(marker))
	}
	goals.PrintAuditTable(ui, res, filterMode, showAll, gc.ascii)
	if syncPerformed && !outModes.quiet {
		if len(clearedClaims) > 0 {
			fmt.Fprintf(os.Stdout, "\n🔓 Cleared %d closed claim %s\n", len(clearedClaims), plural(int64(len(clearedClaims)), "record"))
		}
		syncMsg := fmt.Sprintf("\n✨ Auto-synced goals/README.md (%d implemented [%d verified, %d unbound, %d unverified], %d claimed, %d pending)",
			res.ImplementedCount, res.VerifiedCount, res.UnboundCount, res.UnverifiedCount, res.ClaimedCount, res.PendingCount)
		if res.StaleCount > 0 {
			syncMsg = fmt.Sprintf("\n✨ Auto-synced goals/README.md (%d implemented [%d verified, %d unbound, %d stale, %d unverified], %d claimed, %d pending)",
				res.ImplementedCount, res.VerifiedCount, res.UnboundCount, res.StaleCount, res.UnverifiedCount, res.ClaimedCount, res.PendingCount)
		}
		if commitMsg != "" {
			syncMsg += " (committed)"
		} else if stage {
			syncMsg += " (staged)"
		}
		fmt.Fprintln(os.Stdout, ui.Dim(syncMsg))
	}
	if !syncPerformed && !checkMode && res.Total > 0 && !outModes.quiet && (noSync || filterMode != "") {
		if err := goals.CheckReadmeSync(targetDir, res); err != nil {
			marker := "•"
			if gc.ascii {
				marker = "-"
			}
			fmt.Fprintf(os.Stdout, "\n%s hint: run 'sila goals' or 'sila goals --sync' to reconcile goals/README.md\n", ui.Dim(marker))
		}
	}
}

// goalsSyncNotice reports whether the human matrix must announce its
// reconciliation intent before printing.
func goalsSyncNotice(noSync, machineRead, quiet bool, filterMode string, total int) bool {
	return !noSync && !machineRead && !quiet && filterMode == "" && total > 0
}
