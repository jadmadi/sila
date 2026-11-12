package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
	"github.com/jadmadi/sila/internal/version"
)

func (rf *rootFlags) dispatch(sub string, ui render.UI, fs *flag.FlagSet, globalNoColor bool) {
	asOfRequest = strings.TrimSpace(*rf.asOfFlag)
	topSpecified, targetSpecified, kindExplicit, harnessSpecified, dbExplicit, olderThanSpecified, limitSpecified, goalsInterval := rf.visitedFlagState(fs)
	if dbExplicit || os.Getenv("SILA_DB") != "" {
		store.SetFallbackAllowed(false)
	}
	if err := validateOutputModes(*rf.jsonOut, *rf.jsonEnvelopeFlag, *rf.toonOut, *rf.porcelainFlag); err != nil {
		fmt.Fprintf(os.Stderr, "sila: %v\n", err)
		os.Exit(2)
	}
	if *rf.jsonEnvelopeFlag {
		*rf.jsonOut = true
	}
	outModes = outputModes{
		envelope:  *rf.jsonEnvelopeFlag,
		quiet:     *rf.quietFlag || *rf.porcelainFlag,
		porcelain: *rf.porcelainFlag,
		sync:      *rf.syncFlag,
		advisory:  *rf.advisoryFlag,
	}
	relativePathsMode = *rf.relativePathsFlag
	if *rf.noColor {
		ui = render.New(os.Stdout, true)
	}
	sinceTime := parseSince(*rf.since)
	effVerbose := *rf.verbose || *rf.verboseAlias
	writeSessionOverride = *rf.sessionFlag
	writeVisibility = *rf.visibilityFlag
	writeSessionVerbose = effVerbose
	factsDryRun = *rf.dryRunFlag
	factsYes = *rf.yesFlag
	effProject := firstNonEmpty(*rf.project, *rf.projectAlias)
	if *rf.globalFlag {
		effProject = ""
	}
	effTool := firstNonEmpty(*rf.toolFlag, *rf.toolAlias)

	switch sub {
	case "ingest":
		tools := resolveTools(fs.Args())
		runIngest(ui, *rf.dbPath, effProject, *rf.jsonOut, effVerbose, *rf.fullFlag, sinceTime, tools)
	case "assess":
		tools := resolveTools(fs.Args())
		runAssess(ui, *rf.dbPath, effProject, *rf.jsonOut, effVerbose, tools)
	case "dream":
		runDream(ui, *rf.dbPath, effProject, effTool, *rf.refsExistOnly, *rf.staleOnly, *rf.factsOnly, *rf.dryRunFlag, *rf.semanticFlag, *rf.llmProvider, *rf.retryFailed, *rf.jsonOut, *rf.toonOut, effVerbose, *rf.top, fs.Args())
	case "compact":
		var st *store.Store
		var err error
		if *rf.dryRunFlag {
			st, err = store.OpenRO(*rf.dbPath)
		} else {
			st, err = store.Open(*rf.dbPath)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila: %v\n", err)
			os.Exit(1)
		}
		defer st.Close()
		runDreamCompact(ui, st, effProject, *rf.dryRunFlag, *rf.jsonOut, *rf.toonOut, effVerbose, fs.Args())
	case "verify", "reconcile":
		var st *store.Store
		var err error
		if *rf.dryRunFlag {
			st, err = store.OpenRO(*rf.dbPath)
		} else {
			st, err = store.Open(*rf.dbPath)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila: %v\n", err)
			os.Exit(1)
		}
		defer st.Close()
		proj := effProject
		if proj == "" && len(fs.Args()) > 0 {
			proj = fs.Args()[0]
		}
		runReconcile(ui, st, proj, *rf.dryRunFlag, *rf.semanticFlag, *rf.jsonOut, effVerbose)
	case "projects":
		runProjects(ui, *rf.dbPath, *rf.forceFlag, *rf.dryRunFlag, *rf.jsonOut, effVerbose, fs.Args())
	case "overlaps":
		runOverlaps(ui, *rf.dbPath, effProject, *rf.jsonOut, effVerbose)
	case "sync":
		if *rf.cloudSyncFlag && !*rf.dryRunFlag {
			runCloudPull(ui, *rf.dbPath, effProject, 50, *rf.jsonOut, *rf.toonOut)
		}
		runSync(ui, *rf.dbPath, effProject, *rf.watchFlag, *rf.forceFlag, *rf.dryRunFlag, *rf.fullFlag, *rf.jsonOut, effVerbose, *rf.intervalFlag, sinceTime, *rf.globalFlag, *rf.compactFlag, fs.Args())
		if *rf.cloudSyncFlag && !*rf.dryRunFlag {
			runCloudPush(ui, *rf.dbPath, effProject, *rf.jsonOut, *rf.toonOut)
		}
	case "live", "watch":
		runSync(ui, *rf.dbPath, effProject, true, *rf.forceFlag, false, *rf.fullFlag, *rf.jsonOut, effVerbose, *rf.intervalFlag, sinceTime, *rf.globalFlag, *rf.compactFlag, fs.Args())
	case "stats":
		runStats(ui, *rf.dbPath, effProject, *rf.jsonOut, *rf.toonOut, *rf.plainFlag, fs.Args())
	case "adoption":
		runAdoption(ui, *rf.dbPath, effProject, *rf.jsonOut, *rf.toonOut, fs.Args())
	case "subagent":
		runSubagent(ui, *rf.dbPath, *rf.jsonOut, *rf.toonOut, fs.Args())
	case "memory":
		memoryOlderThan := ""
		if olderThanSpecified {
			memoryOlderThan = *rf.olderThanFlag
		}
		memoryKind := ""
		if kindExplicit {
			memoryKind = *rf.kindFlag
		}
		memoryReason := ""
		if rf.reasonFlag != nil {
			memoryReason = *rf.reasonFlag
		}
		memoryNote := ""
		if rf.noteFlag != nil {
			memoryNote = *rf.noteFlag
		}
		runMemory(ui, *rf.dbPath, effProject, *rf.since, memoryOlderThan, *rf.memoryFileFlag, memoryKind, *rf.agent, *rf.limitFlag, *rf.yesFlag, *rf.jsonOut, *rf.toonOut, memoryLifecycleOptions{
			reason:          memoryReason,
			authority:       *rf.archiveAuthorityFlag,
			note:            memoryNote,
			duplicateOf:     *rf.duplicateOfFlag,
			hardDelete:      *rf.hardDeleteFlag,
			revealSensitive: *rf.revealSensitiveFlag,
			includeArchived: *rf.includeArchivedFlag,
			verify:          *rf.verifyFlag,
		}, fs.Args())
	case "import":
		if code := runImport(ui, *rf.dbPath, importCLIOptions{
			from:                *rf.fromFlag,
			project:             effProject,
			dryRun:              *rf.dryRunFlag,
			yes:                 *rf.yesFlag,
			again:               *rf.againFlag,
			forceSimilar:        *rf.forceSimilarFlag,
			includeInstructions: *rf.includeInstructions,
			maxFiles:            *rf.maxFilesFlag,
			jsonOut:             *rf.jsonOut,
			toonOut:             *rf.toonOut,
		}); code != 0 {
			os.Exit(code)
		}
	case "export":
		if len(fs.Args()) > 0 && fs.Args()[0] == "opinions" {
			runOpinions(ui, *rf.dbPath, effProject, *rf.categoryFlag, *rf.outFile, *rf.jsonOut, *rf.toonOut, fs.Args())
		} else if len(fs.Args()) > 0 && fs.Args()[0] == "vision" {
			runVision(ui, *rf.dbPath, effProject, *rf.outFile, *rf.agent, *rf.jsonOut, *rf.toonOut, fs.Args())
		} else {
			runExport(ui, *rf.dbPath, effProject, *rf.target, *rf.outFile, effVerbose, *rf.reviewFlag, *rf.scopeFlag, *rf.forceFlag, *rf.jsonOut, fs.Args())
		}
	case "completions":
		runCompletions(fs.Arg(0))
	case "status":
		runStatus(ui, *rf.dbPath, *rf.jsonOut, effVerbose, *rf.scan)
	case "sources":
		runSources(ui, *rf.dbPath, *rf.jsonOut, effVerbose, fs.Args())
	case "connect":
		runConnect(ui, *rf.dbPath, effProject, *rf.jsonOut, *rf.yesFlag, fs.Args())
	case "cards":
		runCards(ui, *rf.dbPath, effProject, *rf.jsonOut, fs.Args())
	case "workspace":
		if code := runWorkspace(ui, effProject, *rf.jsonOut, fs.Args()); code != 0 {
			os.Exit(code)
		}
	case "commits":
		runCommits(ui, *rf.dbPath, effProject, *rf.since, *rf.jsonOut, fs.Args())
	case "session":
		sessionOlderThan := ""
		if olderThanSpecified {
			sessionOlderThan = *rf.olderThanFlag
		}
		sessionHarness := ""
		if harnessSpecified {
			sessionHarness = *rf.harnessFlag
		}
		runSession(ui, *rf.dbPath, effProject, *rf.agent, *rf.sessionFlag, sessionHarness, *rf.goalFlag, sessionOlderThan, *rf.since, *rf.forceFlag, *rf.yesFlag, *rf.jsonOut, fs.Args())
	case "intro":
		runIntro(ui, *rf.agent, *rf.outFile, *rf.dbPath, fs.Args())
	case "soak":
		runSoak(ui, *rf.dbPath, *rf.jsonOut, *rf.toonOut)
	case "man":
		runMan(ui, *rf.jsonOut, fs.Args())
	case "daemon":
		runDaemonCmd(ui, *rf.dbPath, *rf.jsonOut, fs.Args())
	case "mcp":
		if *rf.mcpSchemaFlag {
			if code := runMCPSchema(*rf.toolFlag, *rf.profileFlag, *rf.jsonOut); code != 0 {
				os.Exit(code)
			}
			return
		}
		runMCP(*rf.dbPath, *rf.upstreamsFlag, *rf.noUpstreamsFlag, *rf.profileFlag, *rf.mcpTraceFlag)
	case "search":
		runSearchFiltered(ui, *rf.dbPath, effProject, effTool, *rf.role, sinceTime, *rf.omniFlag, *rf.semanticFlag, *rf.twoPassFlag && !*rf.noTwoPassFlag, *rf.windowFlag, *rf.occurrenceFlag, *rf.refsExistOnly, *rf.staleOnly, *rf.factsOnly, *rf.jsonOut, *rf.toonOut, effVerbose, *rf.top, *rf.includeArchivedFlag, *rf.aclScopedFlag, fs.Args())
	case "prime":
		primeArgs := fs.Args()
		if len(primeArgs) > 0 && strings.EqualFold(primeArgs[0], "canary") {
			canaryArgs := append([]string{}, primeArgs[1:]...)
			if *rf.strictFlag {
				canaryArgs = append(canaryArgs, "--strict")
			}
			if harnessSpecified {
				canaryArgs = append(canaryArgs, "--harness="+*rf.harnessFlag)
			}
			os.Exit(runPrimeCanary(ui, canaryArgs, *rf.jsonOut))
		}
		for _, a := range primeArgs {
			if a == "--verify" {
				os.Exit(runPrimeVerify(*rf.dbPath, effProject, *rf.sessionFlag, *rf.jsonOut))
			}
		}
		if len(primeArgs) > 0 && strings.EqualFold(primeArgs[0], "status") {
			primeHarness := ""
			if harnessSpecified {
				primeHarness = *rf.harnessFlag
			}
			runPrimeStatus(ui, *rf.dbPath, effProject, primeHarness, *rf.jsonOut, *rf.toonOut, *rf.strictFlag)
			return
		}
		for _, a := range primeArgs {
			if strings.HasPrefix(a, "-") {
				fmt.Fprintf(os.Stderr, "sila prime: unknown flag %q\n", a)
				os.Exit(2)
			}
		}
		runPrimeWithOptions(ui, *rf.dbPath, effProject, *rf.jsonOut, *rf.toonOut, *rf.top, *rf.budgetFlag, primeRunOptions{
			Strict:             *rf.strictFlag,
			Full:               *rf.fullFlag,
			MaxTokensStr:       *rf.maxTokensFlag,
			PressureStr:        *rf.pressureFlag,
			IncludeMarkdown:    *rf.includeMarkdownFlag || rf.markdownFlag.Enabled,
			CachePinned:        *rf.cachePinnedFlag,
			AllGoals:           *rf.primeAllGoalsFlag,
			DisableCodebaseMap: *rf.noMapFlag,
			CodebaseMapChars:   *rf.mapCharsFlag,
			IncludeArchived:    *rf.includeArchivedFlag,
		})
	case "preflight":
		runPreflight(ui, *rf.dbPath, effProject, fs.Args(), *rf.jsonOut)
		return
	case "hook":
		hookHarness := ""
		if harnessSpecified {
			hookHarness = *rf.harnessFlag
		}
		hookPolicy := *rf.stopPolicyFlag
		if *rf.stopEnforceFlag {
			hookPolicy = "enforce"
		}
		runHookWithOptions(ui, *rf.dbPath, effProject, *rf.eventFlag, *rf.agent, *rf.sessionFlag, *rf.cwdFlag, *rf.payloadFlag, *rf.drainFlag, *rf.autoSync, *rf.jsonOut, *rf.top, fs.Args(), hookOptions{
			Harness:     hookHarness,
			Replace:     *rf.replaceFlag,
			AutoHandoff: !*rf.hookNoAutoHandoff,
			Budget:      parseHookBudget(*rf.budgetFlag),
			DryRun:      *rf.dryRunFlag,
			Policy:      hookPolicy,
			Strict:      *rf.strictFlag,
			Surface:     *rf.surfaceFlag,
		})
	case "handoff":
		effOpenItems := ""
		if rf.openItems != nil {
			effOpenItems = *rf.openItems
		}
		runHandoff(ui, *rf.dbPath, effProject, *rf.branch, *rf.agent, effOpenItems, *rf.decisions, *rf.files, *rf.jsonOut, effVerbose, fs.Args())
	case "memo":
		runMemo(ui, *rf.dbPath, effProject, *rf.agent, *rf.files, *rf.decisionFlag, memoOptions{
			text:       *rf.memoTextFlag,
			since:      *rf.since,
			claim:      *rf.memoClaimFlag,
			limit:      *rf.limitFlag,
			visibility: *rf.visibilityFlag,
			jsonOut:    *rf.jsonOut,
			toonOut:    *rf.toonOut,
		}, fs.Args())
	case "note", "notes":
		noteArgs := fs.Args()
		if kindExplicit {
			noteArgs = append(noteArgs, "--kind="+*rf.kindFlag)
		}
		if *rf.symbolFlag != "" {
			noteArgs = append(noteArgs, "--symbol="+*rf.symbolFlag)
		}
		if *rf.exportFlag {
			noteArgs = append(noteArgs, "--export")
		}
		if *rf.importFlag {
			noteArgs = append(noteArgs, "--import")
		}
		runNote(ui, *rf.dbPath, effProject, *rf.jsonOut, noteArgs)
	case "decision", "decisions":
		runDecision(ui, *rf.dbPath, effProject, *rf.agent, *rf.goalFlag, *rf.jsonOut, fs.Args())
	case "lesson":
		effFix := stringFlagValue(rf.fixFlag)
		runLesson(ui, *rf.dbPath, effProject, *rf.agent, *rf.files, *rf.causeFlag, effFix, *rf.tagsFlag, *rf.since, *rf.forceFlag, *rf.saveFlag, *rf.jsonOut, *rf.toonOut, fs.Args())
	case "case", "cases":
		runCase(ui, *rf.dbPath, effProject, *rf.agent, *rf.files, *rf.approachFlag, *rf.invariantsFlag, *rf.tagsFlag, *rf.since, *rf.forceFlag, *rf.jsonOut, *rf.toonOut, fs.Args())
	case "recall":
		recallArgs := fs.Args()
		if *rf.symbolFlag != "" {
			recallArgs = append(recallArgs, "--symbol="+*rf.symbolFlag)
		}
		runRecallWithContext(ui, *rf.dbPath, effProject, *rf.top, *rf.semanticFlag, *rf.jsonOut, *rf.toonOut, recallArgs, recallContextInput{
			Task:            *rf.recallTaskFlag,
			Paths:           *rf.recallPathsFlag,
			Branch:          *rf.branch,
			Head:            *rf.recallHeadFlag,
			Symbols:         *rf.recallSymbolsFlag,
			Budget:          *rf.budgetFlag,
			ContextFile:     *rf.recallContextFile,
			IncludeArchived: *rf.includeArchivedFlag,
			ACLScoped:       *rf.aclScopedFlag,
		})
	case "doc":
		runDoc(ui, *rf.dbPath, effProject, *rf.title, *rf.lang, *rf.repo, *rf.ext, *rf.eco, *rf.tag, *rf.crawl, *rf.depth, *rf.maxPages, *rf.top, *rf.autoIngest, *rf.portFlag, *rf.hostFlag, *rf.noOpenFlag, *rf.jsonOut, effVerbose, *rf.formatFlag, fs.Args())
	case "graph":
		runGraph(ui, *rf.dbPath, effProject, *rf.jsonOut, *rf.dryRunFlag, *rf.forceFlag, *rf.ifStaleFlag, *rf.top, *rf.includeAmbiguousFlag, *rf.codeOnlyFlag, *rf.maxHopsFlag, *rf.maxDepthFlag, *rf.files, *rf.budgetFlag, fs.Args())
	case "config":
		runConfig(ui, *rf.jsonOut, *rf.revealFlag, fs.Args())
	case "vacuum":
		runVacuum(ui, *rf.dbPath, *rf.statsFlag, *rf.forceFlag, *rf.jsonOut)
	case "backup":
		runBackup(ui, *rf.dbPath, *rf.outFile, *rf.jsonOut)
	case "storage":
		runStorage(ui, *rf.dbPath, *rf.jsonOut)
	case "clean":
		cwd, _ := os.Getwd()
		effRepoClean := false
		if rf.repoCleanFlag != nil {
			effRepoClean = *rf.repoCleanFlag
		}
		effAllClean := false
		if rf.allCleanFlag != nil {
			effAllClean = *rf.allCleanFlag
		}
		effHeavyClean := false
		if rf.heavyCleanFlag != nil {
			effHeavyClean = *rf.heavyCleanFlag
		}
		runClean(ui, cwd, fs.Args(), cleanRunOptions{
			repoClean:   effRepoClean,
			globalClean: *rf.globalFlag,
			allClean:    effAllClean,
			heavy:       effHeavyClean,
			dryRun:      *rf.dryRunFlag,
			yes:         *rf.yesFlag,
			force:       *rf.forceFlag,
			jsonOut:     *rf.jsonOut,
			toonOut:     *rf.toonOut,
		})
	case "store":
		runStore(ui, *rf.dbPath, *rf.jsonOut, *rf.toonOut, fs.Args())
	case "facts":
		factsArgs := fs.Args()
		if *rf.dryRunFlag {
			factsArgs = append(factsArgs, "--dry-run")
		}
		if *rf.yesFlag {
			factsArgs = append(factsArgs, "--yes")
		}
		runFacts(ui, *rf.dbPath, effProject, *rf.jsonOut, *rf.toonOut, effVerbose, *rf.reasonFlag, factsArgs)
	case "guide":
		guideArgs := fs.Args()
		if *rf.formatFlag != "" && *rf.formatFlag != "auto" {
			guideArgs = append([]string{"--format=" + *rf.formatFlag}, guideArgs...)
		}
		if targetSpecified && *rf.target != "" {
			guideArgs = append(guideArgs, "--target="+*rf.target)
		}
		if *rf.domainFlag != "" {
			guideArgs = append(guideArgs, "--domain="+*rf.domainFlag)
		}
		if rf.allGoalsFlag != nil && *rf.allGoalsFlag {
			guideArgs = append(guideArgs, "--all")
		}
		runGuide(ui, *rf.dbPath, effProject, *rf.jsonOut, *rf.toonOut, guideArgs)
	case "learn":
		effFix := ""
		if rf.fixFlag != nil {
			effFix = *rf.fixFlag
		}
		runLearn(ui, *rf.dbPath, effProject, *rf.domainFlag, *rf.title, *rf.symptomFlag, *rf.causeFlag, effFix, *rf.ruleFlag, *rf.stackFlag, *rf.tagsFlag, *rf.jsonOut, *rf.toonOut, fs.Args())
	case "doctor":
		doctorSkills := false
		if rf.skillsFlag != nil && *rf.skillsFlag {
			doctorSkills = true
		}
		doctorDebt := false
		if rf.debtFlag != nil && *rf.debtFlag {
			doctorDebt = true
		}
		doctorMemory = rf.memoryFlag != nil && *rf.memoryFlag
		doctorAdoption = rf.adoptionFlag != nil && *rf.adoptionFlag
		runDoctor(ui, *rf.dbPath, effProject, *rf.repairFlag, *rf.dryRunFlag, *rf.jsonOut, *rf.toonOut, doctorSkills, *rf.covenantFlag, *rf.edgeFlag, doctorDebt, *rf.mcpFlag)
	case "edit-check", "editcheck":
		ecArgs := fs.Args()
		if *rf.symbolFlag != "" {
			ecArgs = append(ecArgs, "--symbol="+*rf.symbolFlag)
		}
		runEditCheck(ui, *rf.jsonOut, *rf.toonOut, *rf.dryRunFlag, *rf.payloadFlag, ecArgs)
	case "safe-delete", "safedelete":
		sdArgs := fs.Args()
		if *rf.symbolFlag != "" {
			sdArgs = append(sdArgs, "--symbol="+*rf.symbolFlag)
		}
		runSafeDelete(ui, *rf.jsonOut, *rf.toonOut, *rf.dryRunFlag, sdArgs)
	case "test":
		runTest(ui, *rf.jsonOut, *rf.toonOut, *rf.runFlag, fs.Args())
	case "gate":
		runGate(ui, *rf.jsonOut, *rf.toonOut, *rf.hookFlag, *rf.remoteFlag, *rf.scarTissueFlag, *rf.docCoverageFlag, *rf.reportCardFlag, *rf.coverageFlag, *rf.strictFlag, *rf.requireTestsFlag, *rf.preCommitFlag, *rf.fullFlag, *rf.skipArchFlag, *rf.compactFlag, *rf.verbose, *rf.postCommitFlag, *rf.trustTargetFlag, *rf.tagFlag, *rf.showConfigFlag, *rf.profileFlag, *rf.forwardFlag, *rf.branch, fs.Args())
	case "reportcard", "report-card":
		runGateReportCardStandalone(ui, *rf.jsonOut, *rf.toonOut)
	case "coverage":
		runGateCoverageStandalone(ui, *rf.jsonOut, *rf.toonOut, *rf.strictFlag, *rf.profileFlag, effVerbose)
	case "goals", "goal":
		effTouch := *rf.touchFlag
		if effTouch == "" {
			effTouch = *rf.files
		}
		effAllGoals := false
		if rf.allGoalsFlag != nil {
			effAllGoals = *rf.allGoalsFlag
		}
		effSelection := ""
		if rf.selectionFlag != nil {
			effSelection = *rf.selectionFlag
		}
		effAutoRun := false
		if rf.autoRunFlag != nil {
			effAutoRun = *rf.autoRunFlag
		}
		if runMergeSlotIfRequested(ui, *rf.dbPath, effProject, *rf.jsonOut, fs.Args(), *rf.tokenFlag, *rf.forceFlag) {
			return
		}
		runGoals(ui, *rf.dbPath, effProject, *rf.agent, *rf.noteFlag, *rf.statusFlag, *rf.reasonFlag, *rf.claimImmediateFlag, *rf.stageFlag, *rf.commitFlag, *rf.title, *rf.descFlag, *rf.dependsOnFlag, effTouch, *rf.seqFlag, *rf.independentFlag, *rf.globalFlag, *rf.pendingFlag, *rf.claimedFlag, *rf.readyFlag, *rf.independentFlag, *rf.blockedFlag, *rf.forceFlag, *rf.jsonOut, *rf.toonOut, effAllGoals, *rf.implementedFlag, *rf.noSyncFlag, *rf.shapeFlag, *rf.addDepFlag, *rf.removeDepFlag, *rf.dependentFlag, fs.Args(), goalsRunOptions{session: *rf.sessionFlag, stale: *rf.staleOnly, olderThan: *rf.olderThanFlag, claims: *rf.claimsFlag, dryRun: *rf.dryRunFlag, check: *rf.checkOnlyFlag, by: *rf.byFlag, evidence: *rf.evidenceFlag, tier: *rf.tierFlag, invariants: *rf.invariantsFlag, tasks: *rf.tasksFlag, workPackage: *rf.workPackageFlag, specReview: *rf.specReviewFlag, review: *rf.goalReviewFlag, noBuild: *rf.noBuildFlag, checkTasks: *rf.checkTasksFlag, allow: *rf.allowFlag, deny: *rf.denyFlag, duration: *rf.durationFlag, stats: *rf.statsFlag, strict: *rf.strictFlag, strictMemory: *rf.strictMemoryFlag, noMemory: *rf.noMemoryFlag, manifest: *rf.manifestFlag, line: *rf.lineFlag, full: *rf.fullFlag, live: *rf.watchFlag, interval: goalsInterval, personas: *rf.personasFlag, selectionFile: effSelection, autoRun: effAutoRun, ascii: *rf.asciiFlag || *rf.plainFlag, limit: *rf.limitFlag, limitSet: limitSpecified})
	case "feedback":
		effOpen := false
		if rf.openFeedback != nil {
			effOpen = *rf.openFeedback
		}
		fbArgs := fs.Args()
		if rf.tokenFlag != nil && *rf.tokenFlag != "" {
			fbArgs = append([]string{"--token=" + *rf.tokenFlag}, fbArgs...)
		}
		if rf.remoteURLFlag != nil && *rf.remoteURLFlag != "" {
			fbArgs = append([]string{"--remote=" + *rf.remoteURLFlag}, fbArgs...)
		}
		runFeedback(ui, *rf.dbPath, effProject, *rf.agent, *rf.categoryFlag, *rf.statusFlag, *rf.goalFlag, *rf.noteFlag, *rf.idFlag, *rf.jsonOut, *rf.top, effOpen, *rf.closedFeedback, *rf.issueFlag, *rf.shareFlag, *rf.yesFlag, *rf.noOpenFlag, fbArgs)
	case "update", "upgrade":
		opts := version.UpdateOptions{
			Force:     *rf.forceFlag,
			CheckOnly: *rf.checkOnlyFlag,
			Source:    *rf.sourceFlag,
			JSON:      *rf.jsonOut,
			NoColor:   *rf.noColor || globalNoColor,
		}
		if err := version.RunUpgrade(ui, opts); err != nil {
			fmt.Fprintf(os.Stderr, "sila %s error: %v\n", sub, err)
			os.Exit(1)
		}
	case "release":
		runRelease(ui, *rf.jsonOut, *rf.toonOut, *rf.outFile, *rf.allowDirtyFlag, *rf.offlineFlag, *rf.strictFlag, fs.Args())
	case "credits":
		runCredits(ui, *rf.jsonOut, *rf.toonOut, fs.Args()...)
	case "opinions":
		runOpinions(ui, *rf.dbPath, effProject, *rf.categoryFlag, *rf.outFile, *rf.jsonOut, *rf.toonOut, fs.Args())
	case "vision":
		runVision(ui, *rf.dbPath, effProject, *rf.outFile, *rf.agent, *rf.jsonOut, *rf.toonOut, fs.Args())
	case "adr":
		runAdr(ui, *rf.dbPath, effProject, *rf.decidersFlag, *rf.files, *rf.outFile, *rf.jsonOut, fs.Args())
	case "forget":
		runForget(ui, *rf.dbPath, effProject, *rf.includeTranscriptsFlag, *rf.yesFlag, *rf.jsonOut)
	case "pause":
		runPause(ui, *rf.dbPath, effProject, *rf.reasonFlag, *rf.agent, *rf.globalFlag, *rf.jsonOut)
	case "resume":
		runResume(ui, *rf.dbPath, effProject, *rf.globalFlag, *rf.jsonOut)
	case "report":
		// Bare `sila report` is the continuity report; a positional report id
		// or --goal keeps the existing audit-report browser behavior.
		if len(fs.Args()) > 0 || *rf.goalFlag != "" {
			repArgs := fs.Args()
			if *rf.goalFlag != "" {
				repArgs = append([]string{"--goal=" + *rf.goalFlag}, repArgs...)
			}
			runReports(ui, *rf.dbPath, *rf.jsonOut, *rf.toonOut, repArgs)
		} else {
			runReport(ui, *rf.dbPath, effProject, *rf.since, *rf.jsonOut, rf.markdownFlag, fs.Args())
		}
	case "digest":
		runDigest(ui, *rf.dbPath, effProject, *rf.since, effTool, *rf.top, topSpecified, *rf.jsonOut, *rf.toonOut, *rf.humanOut, *rf.activityFlag, *rf.globalFlag, fs.Args())
	case "standup":
		runStandup(ui, *rf.dbPath, effProject, *rf.since, effTool, *rf.top, topSpecified, *rf.jsonOut, *rf.toonOut, *rf.activityFlag, *rf.globalFlag, fs.Args())
	case "recap":
		runRecap(ui, *rf.dbPath, effProject, *rf.since, *rf.jsonOut)
	case "benchmark":
		runBenchmark(ui, *rf.dbPath, effProject, *rf.minSessions, *rf.jsonOut, *rf.toonOut)
	case "changelog":
		allReleases := rf.allChangelogFlag != nil && *rf.allChangelogFlag
		timeline := (rf.timelineFlag != nil && *rf.timelineFlag) || (rf.compactFlag != nil && *rf.compactFlag)
		targetVer := ""
		args := fs.Args()
		if len(args) > 0 {
			targetVer = args[0]
		}
		opts := changelogOptions{
			projectFlag:    effProject,
			tagFlag:        *rf.tagFlag,
			writeFlag:      *rf.writeFlag,
			unreleasedFlag: *rf.unreleasedFlag,
			scanFlag:       *rf.scan,
			top:            *rf.top,
			topSpecified:   topSpecified,
			allReleases:    allReleases,
			timeline:       timeline,
			full:           *rf.fullFlag,
			targetVersion:  targetVer,
			jsonOut:        *rf.jsonOut,
			toonOut:        *rf.toonOut,
		}
		runChangelog(ui, opts)
	case "worktree":
		runWorktree(ui, *rf.dbPath, effProject, *rf.jsonOut, *rf.toonOut, fs.Args())
	case "crew":
		crewArgs := fs.Args()
		if *rf.briefFlag != "" {
			crewArgs = append(crewArgs, "--brief="+*rf.briefFlag)
		}
		if rf.taskFlag != nil && *rf.taskFlag != "" {
			crewArgs = append(crewArgs, "--task="+*rf.taskFlag)
		}
		if rf.goalsFlag != nil && *rf.goalsFlag != "" {
			crewArgs = append(crewArgs, "--goals="+*rf.goalsFlag)
		}
		if *rf.surfaceFlag != "" {
			crewArgs = append(crewArgs, "--surface="+*rf.surfaceFlag)
		}
		if *rf.role != "" {
			crewArgs = append(crewArgs, "--role="+*rf.role)
		}
		if *rf.forceFlag {
			crewArgs = append(crewArgs, "--force")
		}
		runCrew(ui, *rf.jsonOut, *rf.toonOut, crewArgs)
	case "install":
		runInstall(ui, effProject, *rf.toolsFlag, *rf.policyPackFlag, *rf.noHooksFlag, *rf.noMCPFlag, *rf.shimFlag, *rf.dryRunFlag, *rf.replaceFlag, *rf.jsonOut)
	case "agents":
		agentArgs := fs.Args()
		isAll := (rf.allAgentsFlag != nil && *rf.allAgentsFlag) || (rf.globalFlag != nil && *rf.globalFlag)
		if isAll {
			foundAll := false
			for _, a := range agentArgs {
				if a == "--all" || a == "-all" || a == "-a" || a == "--global" || a == "-global" || a == "-g" {
					foundAll = true
					break
				}
			}
			if !foundAll {
				agentArgs = append(agentArgs, "--all")
			}
		}
		if *rf.forceFlag {
			agentArgs = append(agentArgs, "--force")
		}
		if *rf.changedOnlyFlag {
			agentArgs = append(agentArgs, "--changed-only")
		}
		runAgents(ui, *rf.dbPath, effProject, *rf.target, *rf.outFile, *rf.policyPackFlag, *rf.scopeFlag, *rf.dryRunFlag, *rf.replaceFlag, *rf.principlesFlag, *rf.stateBlockFlag, *rf.stackFlag, *rf.jsonOut, agentArgs)
	case "unslop":
		isFix := false
		if rf.unslopFixFlag != nil {
			isFix = *rf.unslopFixFlag
		}
		isDiff := false
		if rf.unslopDiffFlag != nil {
			isDiff = *rf.unslopDiffFlag
		}
		isCheck := false
		if rf.checkOnlyFlag != nil {
			isCheck = *rf.checkOnlyFlag
		}
		runUnslop(ui, *rf.jsonOut, isCheck, isFix, isDiff, fs.Args())
	case "onboard":
		runOnboard(ui, *rf.dbPath, effProject, *rf.convertTasksFlag, *rf.fromFlag, *rf.reviewFlag, *rf.yesFlag, *rf.dryRunFlag, *rf.strictFlag, *rf.globalFlag, *rf.jsonOut, *rf.reasonFlag, *rf.agent, fs.Args())
	case "heal":
		healArgs := fs.Args()
		if *rf.globalFlag {
			healArgs = append(healArgs, "--global")
		}
		if *rf.forceFlag {
			healArgs = append(healArgs, "--force")
		}
		runHeal(ui, *rf.dbPath, effProject, *rf.dryRunFlag, *rf.jsonOut, healArgs)
	case "salam", "salaam", "hi", "hello", "merhaba":
		salamArgs := fs.Args()
		if *rf.fullFlag {
			salamArgs = append(salamArgs, "--full")
		}
		if *rf.globalFlag {
			salamArgs = append(salamArgs, "--global")
		}
		runSalam(ui, *rf.dbPath, effProject, *rf.yesFlag, *rf.jsonOut, salamArgs)
	case "ui", "web":
		if *rf.demoFlag {
			runDemo(ui, *rf.portFlag, *rf.hostFlag, *rf.noOpenFlag, *rf.exposeFlag, *rf.jsonOut, *rf.toonOut, true, false, "all")
			return
		}
		runUI(ui, *rf.dbPath, effProject, *rf.portFlag, *rf.hostFlag, *rf.noOpenFlag, *rf.exposeFlag, *rf.jsonOut, fs.Args())
	case "cloud":
		runCloud(ui, *rf.dbPath, effProject, *rf.limitFlag, *rf.jsonOut, *rf.toonOut, fs.Args())
	case "gateway":
		runGateway(ui, *rf.dbPath, effProject, *rf.jsonOut, *rf.toonOut, fs.Args())
	case "approve", "approval":
		runApprove(ui, *rf.dbPath, effProject, *rf.rejectFlag, *rf.byFlag, *rf.reasonFlag, *rf.pendingFlag, *rf.jsonOut, *rf.toonOut, fs.Args())
	case "demo":
		runDemo(ui, *rf.portFlag, *rf.hostFlag, *rf.noOpenFlag, *rf.exposeFlag, *rf.jsonOut, *rf.toonOut, *rf.webFlag, *rf.keepFlag, *rf.stepFlag)
	case "eval":
		evalTarget := ""
		if targetSpecified {
			evalTarget = *rf.target
		}
		runEval(ui, *rf.dbPath, effProject, *rf.harnessFlag, evalTarget, *rf.evalDirFlag, *rf.caseFlag, *rf.tag, *rf.fromLessonFlag, *rf.fromFeedbackFlag, *rf.ablationFlag, *rf.reportFlag, *rf.bareFlag, *rf.allLessonsFlag, *rf.jsonOut, *rf.toonOut, fs.Args())
	case "persona", "personas":
		personaArgs := fs.Args()
		if *rf.groupFlag != "" {
			personaArgs = append(personaArgs, "--group="+*rf.groupFlag)
		}
		runPersona(ui, *rf.dbPath, *rf.jsonOut, *rf.toonOut, personaArgs)
	case "audit":
		runAudit(ui, *rf.dbPath, *rf.jsonOut, *rf.toonOut, auditOptions{
			promptOnly:    *rf.promptOnlyFlag,
			registerGoals: *rf.registerGoalsFlag,
			dryRun:        *rf.dryRunFlag,
			squad:         *rf.squadFlag,
			personas:      *rf.personasFlag,
			pages:         *rf.pagesFlag,
			app:           *rf.appFlag,
			recommend:     *rf.recommendFlag,
			preset:        *rf.presetFlag,
			register:      *rf.registerFlag,
			context:       *rf.contextFlag,
		}, fs.Args())
	case "hire":
		runHire(ui, *rf.dbPath, *rf.jsonOut, *rf.toonOut, auditOptions{
			promptOnly:    *rf.promptOnlyFlag,
			registerGoals: *rf.registerGoalsFlag,
			dryRun:        *rf.dryRunFlag,
			squad:         *rf.squadFlag,
			personas:      *rf.personasFlag,
			pages:         *rf.pagesFlag,
			app:           *rf.appFlag,
			recommend:     *rf.recommendFlag,
			preset:        *rf.presetFlag,
			register:      *rf.registerFlag,
			context:       *rf.contextFlag,
			goals:         rf.goalsAuditFlag != nil && *rf.goalsAuditFlag,
			goal:          *rf.goalFlag,
		}, fs.Args())
	case "reports":
		repArgs := fs.Args()
		if *rf.goalFlag != "" {
			repArgs = append([]string{"--goal=" + *rf.goalFlag}, repArgs...)
		}
		runReports(ui, *rf.dbPath, *rf.jsonOut, *rf.toonOut, repArgs)
	case "skill-create", "skills-create":
		runSkills(ui, *rf.dbPath, *rf.jsonOut, *rf.toonOut, skillCreateOptions{
			promptOnly:  *rf.promptOnlyFlag,
			interactive: *rf.interactiveFlag,
			force:       *rf.forceFlag,
			outDir:      *rf.outFile,
			dryRun:      *rf.dryRunFlag,
			yes:         *rf.yesFlag,
		}, fs.Args(), "skill-create")
	case "skills", "skill":
		skillsVal := ""
		if rf.skillsListFlag != nil {
			skillsVal = *rf.skillsListFlag
		}
		excludeVal := ""
		if rf.excludeFlag != nil {
			excludeVal = *rf.excludeFlag
		}
		harnessVal := "antigravity"
		if rf.harnessFlag != nil && *rf.harnessFlag != "" {
			harnessVal = *rf.harnessFlag
		}
		compactVal := false
		if rf.compactFlag != nil && *rf.compactFlag {
			compactVal = true
		}
		runSkills(ui, *rf.dbPath, *rf.jsonOut, *rf.toonOut, skillCreateOptions{
			promptOnly:  *rf.promptOnlyFlag,
			interactive: *rf.interactiveFlag,
			force:       *rf.forceFlag,
			outDir:      *rf.outFile,
			dryRun:      *rf.dryRunFlag,
			harness:     harnessVal,
			skills:      skillsVal,
			exclude:     excludeVal,
			yes:         *rf.yesFlag,
			compact:     compactVal,
		}, fs.Args(), sub)
	case "plugins", "plugin":
		runPlugins(ui, *rf.jsonOut, *rf.toonOut, fs.Args())
	case "silence":
		runSilence(ui, *rf.dbPath, effProject, *rf.jsonOut, *rf.toonOut, fs.Args())
	case "do":
		printDoShortUsage(os.Stderr)
		os.Exit(2)
	case "ccli":
		code := executeCCLICommand(fs.Args(), *rf.noCCLIFlag, *rf.noColor || globalNoColor, os.Stdout, os.Stderr)
		os.Exit(code)
	}
}
