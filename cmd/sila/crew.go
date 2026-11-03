package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/jadmadi/sila/internal/crew"
	"github.com/jadmadi/sila/internal/render"
)

// runCrew implements `sila crew [spawn|list|status|kill|supervise]`.
// It manages the First Mate crew orchestrator for multi-agent sessions.
func runCrew(ui render.UI, jsonOut, toonOut bool, args []string) {
	if len(args) == 0 {
		printCrewHelp(ui)
		os.Exit(2)
	}

	cwd, _ := os.Getwd()
	mgr, err := crew.NewCrewManager(cwd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila crew: %v\n", err)
		os.Exit(1)
	}

	sub := args[0]
	rest := args[1:]

	switch sub {
	case "spawn":
		runCrewSpawn(ui, mgr, jsonOut, rest)
	case "list":
		runCrewList(ui, mgr, jsonOut, toonOut)
	case "status":
		runCrewStatus(ui, mgr, jsonOut, rest)
	case "kill":
		runCrewKill(ui, mgr, rest)
	case "supervise":
		runCrewSupervise(ui, mgr, jsonOut, toonOut)
	case "sentinel":
		runCrewSentinel(ui, mgr, jsonOut, toonOut, rest)
	case "plan-lanes", "planlanes", "lanes", "lane":
		runCrewPlanLanes(ui, cwd, jsonOut, toonOut, rest)
	case "prune":
		runCrewPrune(ui, mgr, jsonOut, toonOut, rest)
	default:
		fmt.Fprintf(os.Stderr, "sila crew: unknown subcommand %q (expected spawn, list, status, kill, supervise, sentinel, plan-lanes, or prune)\n", sub)
		os.Exit(2)
	}
}

func runCrewSpawn(ui render.UI, mgr *crew.CrewManager, jsonOut bool, args []string) {
	fs := flag.NewFlagSet("sila crew spawn", flag.ExitOnError)
	surfaceFlag := fs.String("surface", "", "comma-separated exclusive write surface patterns (e.g. 'internal/crew/**,!*.md')")
	roleFlag := fs.String("role", "producer", "agent role class: producer, reviewer, or orchestrator")
	forceFlag := fs.Bool("force", false, "bypass write-surface collision checks")
	_ = fs.Parse(args)

	rest := fs.Args()
	if len(rest) < 2 {
		fmt.Fprintln(os.Stderr, "sila crew spawn: usage: sila crew spawn [--surface=...] [--role=...] <harness> <goal-slug> [base-ref]")
		fmt.Fprintln(os.Stderr, "  harnesses: devin, claude, agy, opencode, codex, mimocode")
		os.Exit(2)
	}
	harness := rest[0]
	goalSlug := rest[1]
	baseRef := ""
	if len(rest) > 2 {
		baseRef = rest[2]
	}

	roleClass := crew.AgentRoleClass(*roleFlag)
	var patterns []string
	if *surfaceFlag != "" {
		for _, p := range strings.Split(*surfaceFlag, ",") {
			if s := strings.TrimSpace(p); s != "" {
				patterns = append(patterns, s)
			}
		}
	}

	cm, err := mgr.SpawnWithSurface(harness, goalSlug, baseRef, roleClass, patterns)
	if err != nil {
		if *forceFlag && strings.Contains(err.Error(), "write-surface collision") {
			cm, err = mgr.SpawnWithRole(harness, goalSlug, baseRef, roleClass)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila crew spawn: %v\n", err)
			os.Exit(1)
		}
	}

	if jsonOut {
		emitJSON(cm)
		return
	}

	fmt.Fprintf(os.Stdout, "%s Spawned crewmate %s\n", ui.Green("✔"), ui.Bold(cm.ID))
	fmt.Fprintf(os.Stdout, "  %s harness:  %s\n", ui.Dim("→"), cm.Harness)
	fmt.Fprintf(os.Stdout, "  %s goal:     %s\n", ui.Dim("→"), cm.GoalSlug)
	if len(cm.AssignedPersonas) > 0 {
		fmt.Fprintf(os.Stdout, "  %s reviewers:%s\n", ui.Dim("→"), ui.Cyan(strings.Join(cm.AssignedPersonas, ", ")))
	}
	if cm.WriteSurface != nil && len(cm.WriteSurface.Patterns) > 0 {
		fmt.Fprintf(os.Stdout, "  %s surface:  %s\n", ui.Dim("→"), strings.Join(cm.WriteSurface.Patterns, ", "))
	}
	fmt.Fprintf(os.Stdout, "  %s worktree: %s\n", ui.Dim("→"), cm.Worktree)
	fmt.Fprintf(os.Stdout, "  %s branch:   %s\n", ui.Dim("→"), cm.Branch)
	if cm.PID > 0 {
		fmt.Fprintf(os.Stdout, "  %s pid:      %d\n", ui.Dim("→"), cm.PID)
		fmt.Fprintf(os.Stdout, "  %s tmux:     %s:%s\n", ui.Dim("→"), cm.TmuxSession, cm.TmuxWindow)
	} else {
		fmt.Fprintf(os.Stdout, "  %s tmux:     %s\n", ui.Dim("→"), ui.Dim("(not available — headless mode)"))
	}
	fmt.Fprintf(os.Stdout, "\n  %s sila crew status %s\n", ui.Dim("Monitor:"), cm.ID)
}

func runCrewList(ui render.UI, mgr *crew.CrewManager, jsonOut, toonOut bool) {
	crewmates, err := mgr.List()
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila crew list: %v\n", err)
		os.Exit(1)
	}

	if jsonOut {
		emitJSON(crewmates)
		return
	}

	if toonOut {
		count := len(crewmates)
		fmt.Fprintf(os.Stdout, "crew[%d]{id,harness,goal,status,pid}:\n", count)
		for _, cm := range crewmates {
			fmt.Fprintf(os.Stdout, "  %s,%s,%s,%s,%d\n", cm.ID, cm.Harness, cm.GoalSlug, cm.Status, cm.PID)
		}
		return
	}

	if len(crewmates) == 0 {
		fmt.Fprintln(os.Stdout, ui.Dim("_No active crewmates. Run `sila crew spawn <harness> <goal-slug>` to spawn one._"))
		return
	}

	fmt.Fprintf(os.Stdout, "%s sila · crew list (%d active)\n\n", ui.Cyan("·"), len(crewmates))
	for _, cm := range crewmates {
		statusIcon := statusIconForCrewmate(cm.Status)
		fmt.Fprintf(os.Stdout, "  %s %s  %s  %s  pid:%d\n",
			statusIcon, ui.Bold(cm.ID), cm.Harness, cm.GoalSlug, cm.PID)
		fmt.Fprintf(os.Stdout, "    %s status:   %s\n", ui.Dim("→"), cm.Status)
		if len(cm.AssignedPersonas) > 0 {
			fmt.Fprintf(os.Stdout, "    %s reviewers:%s\n", ui.Dim("→"), ui.Cyan(strings.Join(cm.AssignedPersonas, ", ")))
		}
		fmt.Fprintf(os.Stdout, "    %s worktree: %s\n", ui.Dim("→"), cm.Worktree)
		if cm.GateResult != "" {
			fmt.Fprintf(os.Stdout, "    %s gate:     %s\n", ui.Dim("→"), cm.GateResult)
		}
	}
}

func runCrewStatus(ui render.UI, mgr *crew.CrewManager, jsonOut bool, args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "sila crew status: crewmate ID is required")
		os.Exit(2)
	}
	id := args[0]

	cm, err := mgr.Get(id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila crew status: %v\n", err)
		os.Exit(1)
	}

	if jsonOut {
		emitJSON(cm)
		return
	}

	statusIcon := statusIconForCrewmate(cm.Status)
	fmt.Fprintf(os.Stdout, "%s Crewmate %s\n\n", statusIcon, ui.Bold(cm.ID))
	fmt.Fprintf(os.Stdout, "  %s harness:    %s\n", ui.Dim("→"), cm.Harness)
	fmt.Fprintf(os.Stdout, "  %s goal:       %s\n", ui.Dim("→"), cm.GoalSlug)
	if len(cm.AssignedPersonas) > 0 {
		fmt.Fprintf(os.Stdout, "  %s reviewers:  %s\n", ui.Dim("→"), ui.Cyan(strings.Join(cm.AssignedPersonas, ", ")))
	}
	fmt.Fprintf(os.Stdout, "  %s status:     %s\n", ui.Dim("→"), cm.Status)
	fmt.Fprintf(os.Stdout, "  %s worktree:   %s\n", ui.Dim("→"), cm.Worktree)
	fmt.Fprintf(os.Stdout, "  %s branch:     %s\n", ui.Dim("→"), cm.Branch)
	if cm.PID > 0 {
		fmt.Fprintf(os.Stdout, "  %s pid:        %d\n", ui.Dim("→"), cm.PID)
		fmt.Fprintf(os.Stdout, "  %s tmux:       %s:%s\n", ui.Dim("→"), cm.TmuxSession, cm.TmuxWindow)
	}
	fmt.Fprintf(os.Stdout, "  %s spawned:    %s\n", ui.Dim("→"), cm.SpawnedAt.Format("2006-01-02 15:04:05"))
	fmt.Fprintf(os.Stdout, "  %s last check: %s\n", ui.Dim("→"), cm.LastChecked.Format("2006-01-02 15:04:05"))
	if !cm.CompletedAt.IsZero() {
		fmt.Fprintf(os.Stdout, "  %s completed:  %s\n", ui.Dim("→"), cm.CompletedAt.Format("2006-01-02 15:04:05"))
	}
	if cm.GateResult != "" {
		fmt.Fprintf(os.Stdout, "  %s gate:       %s\n", ui.Dim("→"), cm.GateResult)
	}
	if cm.ErrorMsg != "" {
		fmt.Fprintf(os.Stdout, "  %s error:      %s\n", ui.Red("→"), cm.ErrorMsg)
	}
}

func runCrewKill(ui render.UI, mgr *crew.CrewManager, args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "sila crew kill: crewmate ID is required")
		os.Exit(2)
	}
	id := args[0]

	if err := mgr.Kill(id); err != nil {
		fmt.Fprintf(os.Stderr, "sila crew kill: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stdout, "%s Killed crewmate %s (worktree cleaned up)\n", ui.Red("✖"), ui.Bold(id))
}

func runCrewSupervise(ui render.UI, mgr *crew.CrewManager, jsonOut, toonOut bool) {
	crewmates, err := mgr.Supervise()
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila crew supervise: %v\n", err)
		os.Exit(1)
	}

	if jsonOut {
		emitJSON(crewmates)
		return
	}

	if len(crewmates) == 0 {
		fmt.Fprintln(os.Stdout, ui.Dim("_No crewmates to supervise._"))
		return
	}

	// Summary line.
	running, idle, completed, failed := 0, 0, 0, 0
	for _, cm := range crewmates {
		switch cm.Status {
		case crew.StatusRunning:
			running++
		case crew.StatusIdle:
			idle++
		case crew.StatusCompleted:
			completed++
		case crew.StatusFailed, crew.StatusGateFailed, crew.StatusKilled:
			failed++
		}
	}
	fmt.Fprintf(os.Stdout, "%s sila · crew supervise — %d crewmates (🟢 %d running, 🟡 %d idle, ✔ %d completed, ✖ %d failed)\n\n",
		ui.Cyan("·"), len(crewmates), running, idle, completed, failed)

	if toonOut {
		fmt.Fprintf(os.Stdout, "crew_supervise[%d]{id,status,harness,goal}:\n", len(crewmates))
		for _, cm := range crewmates {
			fmt.Fprintf(os.Stdout, "  %s,%s,%s,%s\n", cm.ID, cm.Status, cm.Harness, cm.GoalSlug)
		}
		return
	}

	for _, cm := range crewmates {
		statusIcon := statusIconForCrewmate(cm.Status)
		fmt.Fprintf(os.Stdout, "  %s %s  %s  %s\n", statusIcon, cm.ID, cm.Harness, cm.Status)
	}
}

func statusIconForCrewmate(status crew.CrewmateStatus) string {
	switch status {
	case crew.StatusRunning:
		return "🟢"
	case crew.StatusIdle:
		return "🟡"
	case crew.StatusCompleted:
		return "✔"
	case crew.StatusFailed, crew.StatusGateFailed:
		return "✖"
	case crew.StatusKilled:
		return "☠"
	case crew.StatusSpawning:
		return "⏳"
	default:
		return "•"
	}
}

func runCrewSentinel(ui render.UI, mgr *crew.CrewManager, jsonOut, toonOut bool, args []string) {
	fs := flag.NewFlagSet("sila crew sentinel", flag.ExitOnError)
	watch := fs.Bool("watch", false, "run continuous dual-cron supervision watch loop")
	maxRetries := fs.Int("max-retries", 3, "maximum victory audit retries before halting runaway recursion")
	agent := fs.String("auditor", "victory-auditor", "isolated victory auditor identifier")
	fs.Parse(args)

	cfg := crew.DefaultSentinelConfig()
	if *maxRetries > 0 {
		cfg.MaxRetries = *maxRetries
	}
	if *agent != "" {
		cfg.AuditorAgent = *agent
	}

	orchestrator := crew.NewSentinelOrchestrator(mgr, cfg, nil)

	if *watch {
		fmt.Fprintf(os.Stdout, "%s Starting Sentinel dual-cron supervision (liveness: %v, auditor: %v, max-retries: %d)...\n",
			ui.Cyan("·"), cfg.LivenessInterval, cfg.AuditorInterval, cfg.MaxRetries)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		_ = orchestrator.Watch(ctx, func(rep *crew.SentinelReport) {
			if jsonOut {
				emitJSON(rep)
			} else {
				printSentinelReport(ui, rep)
			}
		})
		return
	}

	rep, err := orchestrator.RunPass()
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila crew sentinel: %v\n", err)
		os.Exit(1)
	}

	if jsonOut {
		emitJSON(rep)
		return
	}

	if toonOut {
		fmt.Fprintf(os.Stdout, "sentinel_report{total,active,audited,approved,rejected,halted}:\n")
		fmt.Fprintf(os.Stdout, "  %d,%d,%d,%d,%d,%d\n",
			rep.TotalCrewmates, rep.Active, rep.Audited, rep.Approved, rep.Rejected, rep.HaltedRunaway)
		return
	}

	printSentinelReport(ui, rep)
}

func printSentinelReport(ui render.UI, rep *crew.SentinelReport) {
	fmt.Fprintf(os.Stdout, "%s sila · crew sentinel — %d crewmates (🟢 %d active, 🔍 %d audited: ✔ %d approved, ✖ %d rejected, 🛑 %d halted runaway)\n",
		ui.Cyan("·"), rep.TotalCrewmates, rep.Active, rep.Audited, rep.Approved, rep.Rejected, rep.HaltedRunaway)

	if len(rep.AuditResults) > 0 {
		for id, res := range rep.AuditResults {
			icon := ui.Green("✔")
			if !res.Passed {
				icon = ui.Red("✖")
			}
			fmt.Fprintf(os.Stdout, "  %s %s: %s (auditor: @%s)\n",
				icon, ui.Bold(id), res.Verdict, res.AuditorAgent)
			if len(res.Errors) > 0 {
				for _, errStr := range res.Errors {
					fmt.Fprintf(os.Stdout, "    %s %s\n", ui.Red("→"), errStr)
				}
			}
			if res.Notes != "" {
				fmt.Fprintf(os.Stdout, "    %s %s\n", ui.Dim("→"), res.Notes)
			}
		}
	}
}

func runCrewPlanLanes(ui render.UI, repoRoot string, jsonOut, toonOut bool, args []string) {
	fs := flag.NewFlagSet("sila crew plan-lanes", flag.ExitOnError)
	briefFlag := fs.String("brief", "", "path to task brief file")
	taskFlag := fs.String("task", "", "candidate goal slug or comma-separated goal slugs")
	goalsFlag := fs.String("goals", "", "candidate goal slug or 'pending'")
	fs.BoolVar(&jsonOut, "json", jsonOut, "output JSON format")
	fs.BoolVar(&toonOut, "toon", toonOut, "output TOON format")
	_ = fs.Parse(args)

	requestedLanes := 2
	rest := fs.Args()
	if len(rest) > 0 {
		if n, err := strconv.Atoi(rest[0]); err == nil && n > 0 {
			requestedLanes = n
		}
	}

	var taskGoals []string
	if *taskFlag != "" {
		for _, t := range strings.Split(*taskFlag, ",") {
			if s := strings.TrimSpace(t); s != "" {
				taskGoals = append(taskGoals, s)
			}
		}
	}
	if *goalsFlag != "" {
		for _, g := range strings.Split(*goalsFlag, ",") {
			if s := strings.TrimSpace(g); s != "" {
				taskGoals = append(taskGoals, s)
			}
		}
	}

	res, err := crew.PlanLanes(repoRoot, requestedLanes, *briefFlag, taskGoals)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila crew plan-lanes: %v\n", err)
		os.Exit(1)
	}

	if jsonOut {
		emitJSON(res)
		if res.ExitCode != 0 {
			os.Exit(res.ExitCode)
		}
		return
	}

	if toonOut {
		fmt.Fprintf(os.Stdout, "lane_plan{status,requested,planned,conflicts,same_file_risks,contract_touches}:\n")
		fmt.Fprintf(os.Stdout, "  %s,%d,%d,%d,%d,%d\n",
			res.Status, res.RequestedLanes, res.PlannedLanes, len(res.Conflicts), len(res.SameFileRisks), len(res.ContractTouches))
		for _, l := range res.Lanes {
			fmt.Fprintf(os.Stdout, "lane{id,role,order,pkgs}:\n  %d,%s,%d,%s\n",
				l.Lane, l.Role, l.LandingOrder, strings.Join(l.Packages, ";"))
		}
		if res.ExitCode != 0 {
			os.Exit(res.ExitCode)
		}
		return
	}

	fmt.Fprintf(os.Stdout, "%s sila · crew plan-lanes — %d lanes planned (status: %s)\n",
		ui.Cyan("·"), res.PlannedLanes, res.Status)

	if len(res.Conflicts) > 0 {
		fmt.Fprintf(os.Stdout, "%s Detected %d hard conflict(s):\n", ui.Red("✖"), len(res.Conflicts))
		for _, c := range res.Conflicts {
			fmt.Fprintf(os.Stdout, "  %s Lane %d vs Lane %d: package %s (%s)\n",
				ui.Red("→"), c.LaneA, c.LaneB, c.Package, c.Reason)
		}
	}

	if len(res.SameFileRisks) > 0 {
		fmt.Fprintf(os.Stdout, "%s Detected %d same-file risk(s):\n", ui.Yellow("⚠"), len(res.SameFileRisks))
		for _, sfr := range res.SameFileRisks {
			fmt.Fprintf(os.Stdout, "  %s %s touched by lanes: %v\n", ui.Yellow("→"), sfr.File, sfr.Lanes)
		}
	}

	if len(res.ContractTouches) > 0 {
		fmt.Fprintf(os.Stdout, "%s Contract touches (dependency ordering):\n", ui.Cyan("·"))
		for _, ct := range res.ContractTouches {
			fmt.Fprintf(os.Stdout, "  %s Lane %d -> Lane %d: shared package %s\n",
				ui.Dim("→"), ct.UpstreamLane, ct.DownstreamLane, ct.SharedPackage)
		}
	}

	for _, l := range res.Lanes {
		fmt.Fprintf(os.Stdout, "  %s Lane %d [%s] (landing order: %d)\n",
			ui.Green("✔"), l.Lane, ui.Bold(l.Role), l.LandingOrder)
		if len(l.Tasks) > 0 {
			fmt.Fprintf(os.Stdout, "    %s       %s\n", ui.Dim("tasks:"), strings.Join(l.Tasks, ", "))
		}
		fmt.Fprintf(os.Stdout, "    %s    %s\n", ui.Dim("packages:"), strings.Join(l.Packages, ", "))
		fmt.Fprintf(os.Stdout, "    %s %s\n", ui.Dim("allow_paths:"), strings.Join(l.AllowPaths, ", "))
		if len(l.DenyPaths) > 0 {
			fmt.Fprintf(os.Stdout, "    %s  %s\n", ui.Dim("deny_paths:"), strings.Join(l.DenyPaths, ", "))
		}
	}

	if res.ExitCode != 0 {
		os.Exit(res.ExitCode)
	}
}

func runCrewPrune(ui render.UI, mgr *crew.CrewManager, jsonOut, toonOut bool, args []string) {
	fs := flag.NewFlagSet("sila crew prune", flag.ExitOnError)
	dryRun := fs.Bool("dry-run", false, "preview actions without deleting resources or terminating sessions")
	force := fs.Bool("force", false, "force eviction of dead locks with reused PIDs without signaling processes")
	fs.BoolVar(&jsonOut, "json", jsonOut, "output JSON format")
	fs.BoolVar(&toonOut, "toon", toonOut, "output TOON format")
	_ = fs.Parse(args)

	isDryRun := *dryRun
	if !isDryRun {
		for _, a := range os.Args {
			if a == "--dry-run" || a == "-n" || a == "--preview" || strings.HasPrefix(a, "--dry-run=") {
				isDryRun = true
				break
			}
		}
	}

	isForce := *force
	if !isForce {
		for _, a := range os.Args {
			if a == "--force" || a == "-f" || strings.HasPrefix(a, "--force=") {
				isForce = true
				break
			}
		}
	}

	rep, err := mgr.Prune(crew.PruneOptions{
		DryRun: isDryRun,
		Force:  isForce,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila crew prune: %v\n", err)
		os.Exit(1)
	}

	if jsonOut {
		emitJSON(rep)
		return
	}

	if toonOut {
		fmt.Fprintf(os.Stdout, "crew_prune{dry_run,total,pruned,retained,manual_followup}:\n")
		fmt.Fprintf(os.Stdout, "  %t,%d,%d,%d,%d\n", rep.DryRun, rep.TotalEvaluated, rep.PrunedCount, rep.RetainedCount, rep.ManualFollowup)
		for _, ev := range rep.Evaluations {
			fmt.Fprintf(os.Stdout, "  resource{%s,%s,%s,%s}\n", ev.Type, ev.Identifier, ev.Action, ev.Reason)
		}
		return
	}

	title := "sila · crew prune"
	if rep.DryRun {
		title += " (dry-run preview)"
	}
	fmt.Fprintf(os.Stdout, "%s %s\n\n", ui.Cyan("·"), title)

	if len(rep.Evaluations) == 0 {
		fmt.Fprintln(os.Stdout, ui.Dim("_No ephemeral crew resources or scoped locks to prune._"))
		return
	}

	for _, ev := range rep.Evaluations {
		var icon string
		switch ev.Action {
		case crew.ActionPruned:
			icon = ui.Green("✔")
		case crew.ActionSkipped:
			icon = ui.Yellow("⚡")
		case crew.ActionManualFollowup:
			icon = ui.Yellow("⚠")
		default:
			icon = ui.Dim("·")
		}

		fmt.Fprintf(os.Stdout, "  %s [%s] %s\n", icon, ui.Bold(string(ev.Type)), ev.Identifier)
		fmt.Fprintf(os.Stdout, "    %s action: %s\n", ui.Dim("→"), ev.Action)
		fmt.Fprintf(os.Stdout, "    %s reason: %s\n", ui.Dim("→"), ev.Reason)
		if ev.Details != "" {
			fmt.Fprintf(os.Stdout, "    %s detail: %s\n", ui.Dim("→"), ev.Details)
		}
	}

	fmt.Fprintf(os.Stdout, "\n%s Total evaluated: %d · Pruned: %d · Retained: %d · Manual follow-up: %d\n",
		ui.Cyan("·"), rep.TotalEvaluated, rep.PrunedCount, rep.RetainedCount, rep.ManualFollowup)
}

func printCrewHelp(ui render.UI) {
	fmt.Fprintln(os.Stderr, ui.Cyan(ui.Bold("sila crew"))+" — First Mate crew orchestrator & zero-token event supervision")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Usage:")
	fmt.Fprintln(os.Stderr, "  sila crew spawn <harness> <goal-slug> [base-ref]  Spawn a crewmate in a disposable worktree + tmux window")
	fmt.Fprintln(os.Stderr, "  sila crew list [--toon|--json]                     List active crewmates with liveness status")
	fmt.Fprintln(os.Stderr, "  sila crew status <id>                              Show detailed status of one crewmate")
	fmt.Fprintln(os.Stderr, "  sila crew kill <id>                                Terminate crewmate, kill tmux window, clean up worktree")
	fmt.Fprintln(os.Stderr, "  sila crew supervise [--toon|--json]                Run one zero-token supervision pass over all crewmates")
	fmt.Fprintln(os.Stderr, "  sila crew sentinel [--watch] [--max-retries=3]     Run Sentinel dual-cron supervision and isolated victory auditing")
	fmt.Fprintln(os.Stderr, "  sila crew plan-lanes <N> [--brief=file] [--json]   Partition tasks into conflict-free lanes")
	fmt.Fprintln(os.Stderr, "  sila crew prune [--dry-run] [--force] [--json]     Safely prune dead crew resources and provably dead locks")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Harnesses: devin, claude, agy, opencode, codex, mimocode")
	fmt.Fprintln(os.Stderr, "Each crewmate runs in .git/sila-worktrees/crew-<id> with auto-seeded SILA_CONTEXT.md.")
	fmt.Fprintln(os.Stderr, "Supervision uses zero-token OS process probes (kill -0, tmux pane activity) — no LLM API calls.")
}
