package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func runGoals(ui render.UI, dbPath, project, agent, note, status, reason string, claimImmediate, stage bool, commitMsg, title, desc, dependsOn, touch string, seq int, independent, isGlobal, pendingOnly, claimedOnly, readyOnly, independentOnly, blockedOnly, force, jsonOut, toonOut, showAll, implementedOnly, noSync bool, shape, addDep, removeDep string, dependent bool, args []string, opts ...goalsRunOptions) {
	st, _ := store.OpenRO(dbPath)
	if st != nil {
		defer st.Close()
	}

	noBuild := false
	checkMode := false
	strictMemoryMode := false
	noMemoryMode := false
	if len(opts) > 0 {
		noBuild = opts[0].noBuild
		strictMemoryMode = opts[0].strictMemory
		noMemoryMode = opts[0].noMemory
		if opts[0].check {
			checkMode = true
		}
	}
	for _, a := range args {
		if a == "--check" || a == "-check" {
			checkMode = true
		}
	}

	targetDir, err := goals.ResolveProjectDir(st, project)
	if err != nil && project != "" {
		fmt.Fprintf(os.Stderr, "sila goals: %v\n", err)
		os.Exit(1)
	}

	filterMode := ""
	if readyOnly {
		filterMode = "ready"
	} else if independentOnly {
		filterMode = "independent"
	} else if blockedOnly {
		filterMode = "blocked"
	} else if pendingOnly {
		filterMode = "pending"
	} else if claimedOnly {
		filterMode = "claimed"
	} else if implementedOnly {
		filterMode = "implemented"
	} else if status != "" {
		switch strings.ToLower(strings.TrimSpace(status)) {
		case "all":
			showAll = true
		case "done", "implemented":
			filterMode = "implemented"
		case "pending", "ready", "independent", "blocked", "claimed", "contested", "sequence":
			filterMode = strings.ToLower(strings.TrimSpace(status))
		}
	}

	sessionID := ""
	staleRelease := false
	olderThan := "72h"
	pruneClaims := false
	dryRun := false
	byOpt := ""
	evidenceOpt := ""
	tierOpt := ""
	invariantsOpt := ""
	tasksOpt := ""
	workPackageOpt := ""
	specReviewOpt := ""
	templateDirOpt := ""
	reviewOpt := ""
	allowOpt := ""
	denyOpt := ""
	durationOpt := ""
	statsMode := false
	strictMode := false
	manifestOpt := ""
	lineMode, fullMode, liveMode := false, false, false
	intervalOpt := ""
	personasOpt := ""
	addPersonasOpt := ""
	clearPersonasOpt := false
	selectionFile := ""
	autoRun := false
	checkTasksOpt := false
	limitOpt := 0
	limitSet := false
	if len(opts) > 0 {
		sessionID = strings.TrimSpace(opts[0].session)
		staleRelease = opts[0].stale
		olderThan = opts[0].olderThan
		pruneClaims = opts[0].claims
		dryRun = opts[0].dryRun
		byOpt = opts[0].by
		evidenceOpt = opts[0].evidence
		tierOpt = strings.TrimSpace(opts[0].tier)
		invariantsOpt = strings.TrimSpace(opts[0].invariants)
		tasksOpt = strings.TrimSpace(opts[0].tasks)
		workPackageOpt = strings.TrimSpace(opts[0].workPackage)
		specReviewOpt = strings.TrimSpace(opts[0].specReview)
		checkTasksOpt = opts[0].checkTasks
		reviewOpt = strings.TrimSpace(opts[0].review)
		allowOpt = strings.TrimSpace(opts[0].allow)
		denyOpt = strings.TrimSpace(opts[0].deny)
		durationOpt = strings.TrimSpace(opts[0].duration)
		statsMode = opts[0].stats
		strictMode = opts[0].strict
		manifestOpt = opts[0].manifest
		lineMode = opts[0].line
		fullMode = opts[0].full
		liveMode = opts[0].live
		intervalOpt = opts[0].interval
		personasOpt = strings.TrimSpace(opts[0].personas)
		addPersonasOpt = strings.TrimSpace(opts[0].addPersonas)
		clearPersonasOpt = opts[0].clearPersonas
		selectionFile = strings.TrimSpace(opts[0].selectionFile)
		autoRun = opts[0].autoRun
		limitOpt = opts[0].limit
		limitSet = opts[0].limitSet
	}

	for _, a := range args {
		if a == "--stats" || a == "-stats" {
			statsMode = true
		} else if a == "--strict" || a == "-strict" {
			strictMode = true
		} else if after, ok := strings.CutPrefix(a, "--manifest="); ok {
			manifestOpt = after
		} else if after, ok := strings.CutPrefix(a, "-manifest="); ok {
			manifestOpt = after
		} else if after, ok := strings.CutPrefix(a, "--tier="); ok {
			tierOpt = after
		} else if after, ok := strings.CutPrefix(a, "-tier="); ok {
			tierOpt = after
		} else if after, ok := strings.CutPrefix(a, "--personas="); ok {
			personasOpt = after
		} else if after, ok := strings.CutPrefix(a, "-personas="); ok {
			personasOpt = after
		} else if after, ok := strings.CutPrefix(a, "--persona="); ok {
			personasOpt = after
		} else if after, ok := strings.CutPrefix(a, "-persona="); ok {
			personasOpt = after
		} else if after, ok := strings.CutPrefix(a, "--add-personas="); ok {
			addPersonasOpt = after
		} else if after, ok := strings.CutPrefix(a, "--add-persona="); ok {
			addPersonasOpt = after
		} else if after, ok := strings.CutPrefix(a, "--work-package="); ok {
			workPackageOpt = after
		} else if after, ok := strings.CutPrefix(a, "-work-package="); ok {
			workPackageOpt = after
		} else if after, ok := strings.CutPrefix(a, "--spec-review="); ok {
			specReviewOpt = after
		} else if after, ok := strings.CutPrefix(a, "-spec-review="); ok {
			specReviewOpt = after
		} else if after, ok := strings.CutPrefix(a, "--template-dir="); ok {
			templateDirOpt = after
		} else if after, ok := strings.CutPrefix(a, "-template-dir="); ok {
			templateDirOpt = after
		} else if after, ok := strings.CutPrefix(a, "--template="); ok {
			templateDirOpt = after
		} else if after, ok := strings.CutPrefix(a, "-template="); ok {
			templateDirOpt = after
		} else if after, ok := strings.CutPrefix(a, "--review="); ok {
			reviewOpt = after
		} else if after, ok := strings.CutPrefix(a, "-review="); ok {
			reviewOpt = after
		} else if a == "--auto-run" || a == "-auto-run" {
			autoRun = true
		} else if after, ok := strings.CutPrefix(a, "--selection="); ok {
			selectionFile = after
		} else if after, ok := strings.CutPrefix(a, "-selection="); ok {
			selectionFile = after
		} else if a == "--clear-personas" || a == "--clear-squad" {
			clearPersonasOpt = true
		}
	}

	gc := &goalsContext{
		ui:               ui,
		st:               st,
		dbPath:           dbPath,
		project:          project,
		agent:            agent,
		session:          sessionID,
		note:             note,
		status:           status,
		reason:           reason,
		claimImmediate:   claimImmediate,
		stage:            stage,
		commitMsg:        commitMsg,
		title:            title,
		desc:             desc,
		dependsOn:        dependsOn,
		touch:            touch,
		seq:              seq,
		independent:      independent,
		isGlobal:         isGlobal,
		pendingOnly:      pendingOnly,
		claimedOnly:      claimedOnly,
		readyOnly:        readyOnly,
		independentOnly:  independentOnly,
		blockedOnly:      blockedOnly,
		force:            force,
		jsonOut:          jsonOut,
		toonOut:          toonOut,
		showAll:          showAll,
		ascii:            len(opts) > 0 && opts[0].ascii,
		implementedOnly:  implementedOnly,
		noSync:           noSync,
		shape:            shape,
		addDep:           addDep,
		removeDep:        removeDep,
		dependent:        dependent,
		args:             args,
		targetDir:        targetDir,
		filterMode:       filterMode,
		sessionID:        sessionID,
		staleRelease:     staleRelease,
		olderThan:        olderThan,
		pruneClaims:      pruneClaims,
		dryRun:           dryRun,
		checkMode:        checkMode,
		noBuild:          noBuild,
		byOpt:            byOpt,
		evidenceOpt:      evidenceOpt,
		tierOpt:          tierOpt,
		invariantsOpt:    invariantsOpt,
		tasksOpt:         tasksOpt,
		workPackageOpt:   workPackageOpt,
		specReviewOpt:    specReviewOpt,
		templateDirOpt:   templateDirOpt,
		reviewOpt:        reviewOpt,
		allowOpt:         allowOpt,
		denyOpt:          denyOpt,
		durationOpt:      durationOpt,
		statsMode:        statsMode,
		personasOpt:      personasOpt,
		addPersonasOpt:   addPersonasOpt,
		clearPersonasOpt: clearPersonasOpt,
		selectionFile:    selectionFile,
		autoRun:          autoRun,
		noMemory:         noMemoryMode,
		limitOpt:         limitOpt,
		limitSet:         limitSet,
	}

	// Strip any leading flag-like args from args, rejecting unknown flags
	for len(args) > 0 && strings.HasPrefix(args[0], "-") {
		a := args[0]
		if after, ok := strings.CutPrefix(a, "--tier="); ok {
			tierOpt = after
		} else if after, ok := strings.CutPrefix(a, "-tier="); ok {
			tierOpt = after
		} else if !isKnownGoalsInlineFlag(a) {
			fmt.Fprintf(os.Stderr, "sila goals: unknown flag %q\n", a)
			os.Exit(2)
		}
		args = args[1:]
	}

	if len(args) > 0 {
		sub := args[0]
		switch sub {
		case "check":
			checkMode = true
			args = args[1:]
		case "sync":
			args = args[1:]
			for len(args) > 0 && strings.HasPrefix(args[0], "-") {
				if args[0] == "--check" || args[0] == "-check" {
					checkMode = true
				}
				args = args[1:]
			}
		case "global":
			isGlobal = true
			args = args[1:]
			if len(args) > 0 {
				switch args[0] {
				case "stats", "statistics":
					statsMode = true
					args = args[1:]
					if len(args) > 0 && args[0] == "live" {
						liveMode = true
						args = args[1:]
					}
				case "all":
					showAll = true
					args = args[1:]
				case "list", "ls":
					args = args[1:]
				case "implemented", "done":
					filterMode = "implemented"
					args = args[1:]
				case "pending":
					filterMode = "pending"
					args = args[1:]
				case "ready":
					filterMode = "ready"
					args = args[1:]
				case "independent", "indep":
					filterMode = "independent"
					args = args[1:]
				case "blocked":
					filterMode = "blocked"
					args = args[1:]
				case "sequence", "seq":
					filterMode = "sequence"
					args = args[1:]
				case "claimed", "in-progress", "active":
					filterMode = "claimed"
					args = args[1:]
				case "contested":
					filterMode = "contested"
					args = args[1:]
				}
			} else if filterMode == "" {
				filterMode = "pending"
			}
		case "stats", "statistics":
			statsMode = true
			args = args[1:]
			for len(args) > 0 {
				if args[0] == "live" {
					liveMode = true
					args = args[1:]
					continue
				}
				if args[0] == "global" || args[0] == "-g" || args[0] == "--global" {
					isGlobal = true
					args = args[1:]
					continue
				}
				if targetDir == "" {
					if resolved, err := goals.ResolveProjectDir(st, args[0]); err == nil {
						targetDir = resolved
						args = args[1:]
						continue
					}
				}
				break
			}
		case "all":
			showAll = true
			args = args[1:]
		case "list", "ls":
			args = args[1:]
		case "implemented", "done":
			filterMode = "implemented"
			args = args[1:]
		case "pending":
			filterMode = "pending"
			args = args[1:]
		case "ready":
			filterMode = "ready"
			args = args[1:]
		case "independent", "indep":
			filterMode = "independent"
			args = args[1:]
		case "blocked":
			filterMode = "blocked"
			args = args[1:]
		case "sequence", "seq":
			filterMode = "sequence"
			args = args[1:]
		case "claimed", "in-progress", "active":
			filterMode = "claimed"
			args = args[1:]
		case "contested":
			filterMode = "contested"
			args = args[1:]
		case "conflicts", "conflict":
			gc.handleConflicts(targetDir, args)
			return
		case "readiness", "ready-check":
			gc.handleReadiness(targetDir, args[1:])
			return
		case "packet", "task-packet":
			gc.handlePacket(targetDir, args[1:])
			return
		case "evidence", "task-evidence":
			gc.handleEvidence(targetDir, args[1:])
			return
		case "checkpoint", "task-checkpoint":
			gc.handleCheckpoint(targetDir, args[1:])
			return
		case "resume", "task-resume":
			gc.handleResume(targetDir, args[1:])
			return
		case "amend", "task-amend":
			gc.handleAmend(targetDir, args[1:])
			return
		case "update", "edit":
			gc.handleUpdate(targetDir, args)
			return
		case "depend", "deps", "dependency", "dependencies":
			gc.handleDepend(targetDir, args)
			return
		case "create", "new", "add":
			if len(args) < 2 {
				fmt.Fprintln(os.Stderr, "Usage: sila goals create <slug> --files=\"path,...\" [--title=\"...\"] [--desc=\"...\"] [--depends-on=\"...\"] [--touch=\"...\"] [--independent] [--seq=N] [--tier=immediate|roadmap|nice-to-have|wont-fix] [--invariants=\"...\"] [--tasks=\"...\"] [--template-dir=path]")
				os.Exit(2)
			}
			if targetDir == "" {
				targetDir, _ = goals.ResolveProjectDir(st, "")
			}
			gc.handleCreate(targetDir, args[1])
			return
		case "reopen":
			gc.handleReopen(targetDir, args)
			return
		case "view", "show", "inspect":
			gc.handleView(targetDir, args)
			return
		case "claim", "lock":
			gc.handleClaim(targetDir, args, true)
			return
		case "contract":
			gc.handleContract(targetDir, args)
			return
		case "charter":
			gc.handleCharter(targetDir, args, true)
			return
		case "commit":
			runGoalsCommit(ui, targetDir, args, commitMsg, noBuild, checkTasksOpt, jsonOut, toonOut)
			return
		case "attribute":
			runGoalsAttribute(ui, targetDir, args, commitMsg, jsonOut, toonOut)
			return
		case "reconcile":
			gc.handleReconcileTree(targetDir, args[1:])
			return
		case "focus":
			gc.handleFocus(targetDir, args)
			return
		case "prune":
			if !pruneClaims {
				fmt.Fprintln(os.Stderr, "Usage: sila goals prune --claims [--dry-run]")
				os.Exit(2)
			}
			runGoalsPruneClosed(ui, dbPath, targetDir, isGlobal, dryRun, jsonOut)
			return
		case "release", "unlock", "unclaim":
			gc.handleRelease(targetDir, args, true)
			return
		case "report", "status":
			gc.handleReport(targetDir, args)
			return
		case "touch", "heartbeat":
			gc.handleTouch(targetDir, args)
			return
		case "review":
			gc.handleReview(targetDir, args)
			return
		case "tasks", "task":
			gc.handleTasks(targetDir, args)
			return
		case "supersede":
			gc.handleSupersede(targetDir, args)
			return
		case "test":
			gc.handleTest(targetDir, args)
			return
		case "certify":
			gc.handleCertify(targetDir, args)
			return
		case "accept", "task-accept":
			gc.handleAccept(targetDir, args[1:])
			return
		case "audit":
			gc.handleAudit(targetDir, args)
			return
		case "verify":
			gc.handleVerify(targetDir, args)
			return
		case "drift":
			gc.handleDrift(targetDir, args)
			return
		case "reverify":
			gc.handleReverify(targetDir, args)
			return
		case "verification":
			gc.handleReconcile(targetDir, args)
			return
		case "validate":
			runGoalsValidate(ui, targetDir, args, jsonOut, toonOut, strictMode, manifestOpt, strictMemoryMode)
			return
		default:
			// If args is a single positional, check if it's an integer or goal identifier before failing as project scope
			if len(args) == 1 && sub != "" && !strings.HasPrefix(sub, "-") {
				checkDir := targetDir
				if checkDir == "" {
					checkDir, _ = goals.ResolveProjectDir(st, "")
				}
				if checkDir != "" {
					if audit, err := goals.ScanGoals(checkDir); err == nil && len(audit.Goals) > 0 {
						if _, err := goals.ResolveGoalByIdentifier(audit, sub); err == nil {
							gc.handleView(checkDir, []string{"view", sub})
							return
						}
					}
				}
			}

			// Treat args[0] as project name / ID / directory
			resolved, err := goals.ResolveProjectDir(st, sub)
			if err != nil {
				notice, hard := scopeFailureAction(sub, len(args) > 1, isGlobal)
				if hard {
					if jsonOut {
						emitJSON(map[string]any{"error": notice})
						return
					}
					fmt.Fprintln(os.Stderr, notice)
					os.Exit(1)
				}
				if notice != "" {
					fmt.Fprintln(os.Stderr, notice)
				}
			}
			if err == nil {
				targetDir = resolved
				args = args[1:]
				if len(args) > 0 {
					switch args[0] {
					case "stats", "statistics":
						statsMode = true
						args = args[1:]
						if len(args) > 0 && args[0] == "live" {
							liveMode = true
							args = args[1:]
						}
					case "all":
						showAll = true
					case "list", "ls":
						// use filterMode from --status or default audit
					case "implemented", "done":
						filterMode = "implemented"
					case "pending":
						filterMode = "pending"
					case "ready":
						filterMode = "ready"
					case "independent", "indep":
						filterMode = "independent"
					case "blocked":
						filterMode = "blocked"
					case "sequence", "seq":
						filterMode = "sequence"
					case "claimed", "in-progress", "active":
						filterMode = "claimed"
					case "contested":
						filterMode = "contested"

					case "conflicts", "conflict":
						gc.handleConflicts(targetDir, args)
						return
					case "create", "new", "add":
						if len(args) < 2 {
							fmt.Fprintln(os.Stderr, "Usage: sila goals <project> create <slug> [--title=\"...\"] [--desc=\"...\"] [--depends-on=\"...\"] [--independent] [--seq=N]")
							os.Exit(2)
						}
						gc.handleCreate(targetDir, args[1])
						return
					case "reopen":
						gc.handleReopen(targetDir, args)
						return
					case "claim", "lock":
						gc.handleClaim(targetDir, args, false)
						return
					case "charter":
						gc.handleCharter(targetDir, args, false)
						return
					case "view", "show", "inspect":
						gc.handleView(targetDir, args)
						return
					case "commit":
						runGoalsCommit(ui, targetDir, args[1:], commitMsg, noBuild, checkTasksOpt, jsonOut, toonOut)
						return
					case "attribute":
						runGoalsAttribute(ui, targetDir, args[1:], commitMsg, jsonOut, toonOut)
						return
					case "focus":
						gc.handleFocus(targetDir, args)
						return
					case "prune":
						if !pruneClaims {
							fmt.Fprintln(os.Stderr, "Usage: sila goals <project> prune --claims [--dry-run]")
							os.Exit(2)
						}
						runGoalsPruneClosed(ui, dbPath, targetDir, false, dryRun, jsonOut)
						return
					case "release", "unlock", "unclaim":
						gc.handleRelease(targetDir, args, false)
						return
					case "report", "status":
						gc.handleReport(targetDir, args)
						return
					case "review":
						gc.handleReview(targetDir, args)
						return
					case "supersede":
						gc.handleSupersede(targetDir, args)
						return
					case "certify":
						gc.handleCertify(targetDir, args)
						return
					case "accept", "task-accept":
						gc.handleAccept(targetDir, args[1:])
						return
					case "checkpoint", "task-checkpoint":
						gc.handleCheckpoint(targetDir, args[1:])
						return
					case "resume", "task-resume":
						gc.handleResume(targetDir, args[1:])
						return
					case "amend", "task-amend":
						gc.handleAmend(targetDir, args[1:])
						return
					case "import":
						gc.handleImport(targetDir, args)
						return
					}
				}
			}
		}
	}

	gc.statsMode = statsMode || lineMode
	gc.lineMode = lineMode
	gc.fullMode = fullMode
	gc.liveMode = liveMode
	gc.intervalOpt = intervalOpt
	gc.filterMode = filterMode
	gc.isGlobal = isGlobal
	gc.showAll = showAll
	gc.checkMode = checkMode
	gc.tierOpt = tierOpt
	gc.targetDir = targetDir
	gc.args = args

	if isGlobal {
		gc.renderGlobalAudit()
		return
	}

	gc.renderAudit(targetDir)
}

func isKnownGoalsInlineFlag(a string) bool {
	switch a {
	case "--check", "-check", "--stats", "-stats", "--strict", "-strict",
		"--auto-run", "-auto-run", "--clear-personas", "--clear-squad",
		"--claims", "--dry-run", "--force", "--independent", "--dependent",
		"--stage", "--no-sync", "--no-build", "--check-tasks", "--skip-canary",
		"--strict-memory", "--no-memory", "--line", "--full", "--live",
		"--stale", "--all", "-g", "--global", "--json", "--toon", "--ascii":
		return true
	}
	for _, p := range []string{
		"--manifest=", "-manifest=", "--tier=", "-tier=",
		"--personas=", "-personas=", "--persona=", "-persona=",
		"--add-personas=", "--add-persona=", "--work-package=", "-work-package=",
		"--spec-review=", "-spec-review=", "--template-dir=", "-template-dir=",
		"--template=", "-template=", "--review=", "-review=",
		"--selection=", "-selection=", "--charter=", "--allow=", "--deny=",
		"--duration=", "--agent=", "--session=", "--goal=", "--commit=",
		"--evidence=", "--by=", "--note=", "--reason=", "--status=",
		"--title=", "--desc=", "--depends-on=", "--touch=", "--invariants=",
		"--tasks=", "--shape=", "--add=", "--remove=", "--interval=", "--older-than=",
	} {
		if strings.HasPrefix(a, p) {
			return true
		}
	}
	return false
}
