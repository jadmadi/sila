package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/assess"
	"github.com/jadmadi/sila/internal/audit"
	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/harness/contract"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func init() {
	goals.SetMarkdownEvidenceVerifier(verifyMarkdownEvidence)
}

// verifyMarkdownEvidence is the CLI seam for internal/assess so
// goals.CertifyGoal can validate markdown evidence without an import cycle.
func verifyMarkdownEvidence(rootDir, slug, evidencePath string, data []byte) error {
	input, err := assess.ParseEvidenceInput(evidencePath, data)
	if err != nil {
		return err
	}
	rep := assess.VerifyManualEvidence(rootDir, slug, input, nil, assess.RuleOfFiveOptions{
		AllowSkippedTests: false,
		AllowWarnings:     true,
	})
	if rep == nil || rep.Clean {
		return nil
	}
	var offenses []string
	for _, v := range rep.Violations {
		if strings.HasPrefix(v, "Point 2") || strings.HasPrefix(v, "Point 3") || strings.HasPrefix(v, "Point 5") || strings.HasPrefix(v, "Evidence-Input") {
			offenses = append(offenses, v)
		}
	}
	if len(offenses) > 0 {
		return fmt.Errorf("EVIDENCE_AUDIT_BLOCKED: Rule-of-Five completion verification failed: %s", strings.Join(offenses, "; "))
	}
	return nil
}

func (gc *goalsContext) handleClaim(targetDir string, args []string, isDirect bool) {
	ui := gc.ui
	sessionID := gc.sessionID
	st := gc.st
	agent := gc.agent
	allowOpt := gc.allowOpt
	denyOpt := gc.denyOpt
	durationOpt := gc.durationOpt
	force := gc.force
	note := gc.note
	stage := gc.stage
	commitMsg := gc.commitMsg
	noSync := gc.noSync
	jsonOut := gc.jsonOut
	if len(args) < 2 {
		if isDirect {
			fmt.Fprintln(os.Stderr, "Usage: sila goals claim <slug> [--agent=<name>] [--note=\"...\"] [--allow=\"path...\"] [--deny=\"path...\"] [--duration=2h] [--force]")
		} else {
			fmt.Fprintln(os.Stderr, "Usage: sila goals <project> claim <slug> [--agent=<name>] [--note=\"...\"] [--allow=\"path...\"] [--deny=\"path...\"] [--duration=2h] [--force]")
		}
		os.Exit(2)
	}
	slug := args[1]
	if targetDir == "" {
		targetDir, _ = goals.ResolveProjectDir(st, "")
	}
	if charter, ok, cerr := charterForWrite(os.Args[1:]); cerr != nil {
		charterFail(cerr)
	} else if ok {
		claimPath := filepath.Join(targetDir, "goals", slug, "claim.json")
		if _, err := os.Stat(claimPath); err == nil {
			fmt.Fprintf(os.Stderr, "sila goals claim: a parent claim already exists for %s; subagent %s must use `sila goals touch %s` or `sila goals report %s`\n", slug, charter.SubagentID, slug, slug)
			os.Exit(2)
		}
		fmt.Fprintf(os.Stderr, "sila goals claim: subagent %s requires an existing parent claim for %s\n", charter.SubagentID, slug)
		os.Exit(2)
	}
	if agent == "" {
		agent = os.Getenv("SILA_AGENT")
		if agent == "" {
			agent = "agent"
		}
	}
	var allowPaths []string
	var denyPaths []string
	var claimDuration time.Duration
	for _, a := range args[2:] {
		if after, ok := strings.CutPrefix(a, "--allow="); ok {
			for p := range strings.SplitSeq(after, ",") {
				if s := strings.TrimSpace(p); s != "" {
					allowPaths = append(allowPaths, s)
				}
			}
		} else if after, ok := strings.CutPrefix(a, "--deny="); ok {
			for p := range strings.SplitSeq(after, ",") {
				if s := strings.TrimSpace(p); s != "" {
					denyPaths = append(denyPaths, s)
				}
			}
		} else if after, ok := strings.CutPrefix(a, "--duration="); ok {
			if d, err := time.ParseDuration(after); err == nil {
				claimDuration = d
			}
		}
	}
	if len(allowPaths) == 0 && allowOpt != "" {
		for p := range strings.SplitSeq(allowOpt, ",") {
			if s := strings.TrimSpace(p); s != "" {
				allowPaths = append(allowPaths, s)
			}
		}
	}
	if len(denyPaths) == 0 && denyOpt != "" {
		for p := range strings.SplitSeq(denyOpt, ",") {
			if s := strings.TrimSpace(p); s != "" {
				denyPaths = append(denyPaths, s)
			}
		}
	}
	if claimDuration == 0 && durationOpt != "" {
		if d, err := time.ParseDuration(durationOpt); err == nil {
			claimDuration = d
		}
	}
	claimOpts := goals.ClaimOptions{
		AllowPaths:  allowPaths,
		DenyPaths:   denyPaths,
		MaxDuration: claimDuration,
	}
	claim, charter, err := goals.ClaimGoalWithCharter(targetDir, slug, agent, note, sessionID, force, claimOpts)
	if err != nil {
		if jsonOut {
			emitJSON(map[string]any{"error": err.Error(), "slug": slug, "claimed": false})
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "sila goals claim error: %v\n", err)
		os.Exit(1)
	}
	if !noSync {
		if res, err := goals.ScanGoals(targetDir); err == nil {
			_ = goals.SyncReadmeWithOpts(targetDir, res, goals.SyncReadmeOptions{Stage: stage, CommitMsg: commitMsg})
		}
	}
	missingTouch := false
	if paths, err := goals.DeclaredTouchPaths(targetDir, slug); err == nil && len(paths) == 0 {
		missingTouch = true
	}
	if jsonOut {
		res := map[string]any{"slug": slug, "claimed": true, "claim": claim, "files_to_touch_missing": missingTouch}
		if charter != nil {
			res["charter"] = charter
		}
		emitJSON(res)
		return
	}
	absDir, _ := filepath.Abs(targetDir)
	projName := filepath.Base(absDir)
	fmt.Fprintf(os.Stdout, "%s Goal %s in %s claimed and locked for %s\n",
		ui.Green("✔"), ui.Bold(slug), ui.Magenta(ui.Bold(projName)), ui.Cyan("@"+claim.Agent))
	if claim.Note != "" {
		fmt.Fprintf(os.Stdout, "  Status: %s\n", ui.Yellow(claim.Note))
	}
	if charter != nil && len(charter.AllowPaths) > 0 {
		fmt.Fprintf(os.Stdout, "  Charter: %d allowed paths, %d denied boundaries (lock token: %s)\n",
			len(charter.AllowPaths), len(charter.DenyPaths), ui.Dim(charter.LockToken))
	}
	if missingTouch {
		fmt.Fprintf(os.Stdout, "  %s goal declares no Files To Touch; verification cannot bind content. Add one before certifying.\n", ui.Yellow("⚠"))
	}
	fmt.Fprintln(os.Stdout, ui.Dim("  Run `/goal goals/"+slug+"/goal.md` to begin execution."))
	return

}

func (gc *goalsContext) handleCharter(targetDir string, args []string, isDirect bool) {
	ui := gc.ui
	st := gc.st
	jsonOut := gc.jsonOut
	toonOut := gc.toonOut
	if len(args) < 2 {
		if isDirect {
			fmt.Fprintln(os.Stderr, "Usage: sila goals charter <slug> [--json] [--toon]")
		} else {
			fmt.Fprintln(os.Stderr, "Usage: sila goals <project> charter <slug> [--json] [--toon]")
		}
		os.Exit(2)
	}
	slug := args[1]
	if targetDir == "" {
		targetDir, _ = goals.ResolveProjectDir(st, "")
	}
	charter, err := goals.LoadCharter(targetDir, slug)
	if err != nil || charter == nil {
		if jsonOut {
			emitJSON(map[string]any{"error": fmt.Sprintf("no active charter found for goal %q", slug), "slug": slug, "found": false})
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "No active charter found for goal %q in %s\n", slug, targetDir)
		os.Exit(1)
	}
	if jsonOut {
		emitJSON(charter)
		return
	}
	if toonOut {
		fmt.Fprint(os.Stdout, goals.RenderCharterTOON(charter))
		return
	}
	goals.PrintCharter(ui, charter)
	return
}

func (gc *goalsContext) handleRelease(targetDir string, args []string, isDirect bool) {
	ui := gc.ui
	st := gc.st
	dbPath := gc.dbPath
	isGlobal := gc.isGlobal
	staleRelease := gc.staleRelease
	olderThan := gc.olderThan
	dryRun := gc.dryRun
	stage := gc.stage
	commitMsg := gc.commitMsg
	noSync := gc.noSync
	jsonOut := gc.jsonOut

	if staleRelease {
		runGoalsReleaseStale(ui, dbPath, targetDir, isGlobal, olderThan, dryRun, jsonOut)
		return
	}
	if len(args) < 2 {
		if isDirect {
			fmt.Fprintln(os.Stderr, "Usage: sila goals release <slug>")
		} else {
			fmt.Fprintln(os.Stderr, "Usage: sila goals <project> release <slug>")
		}
		os.Exit(2)
	}
	slug := args[1]
	if targetDir == "" {
		targetDir, _ = goals.ResolveProjectDir(st, "")
	}
	if err := goals.ReleaseGoal(targetDir, slug); err != nil {
		if jsonOut {
			emitJSON(map[string]any{"error": err.Error(), "slug": slug, "released": false})
			return
		}
		fmt.Fprintf(os.Stderr, "sila goals release error: %v\n", err)
		os.Exit(1)
	}
	if !noSync {
		if res, err := goals.ScanGoals(targetDir); err == nil {
			_ = goals.SyncReadmeWithOpts(targetDir, res, goals.SyncReadmeOptions{Stage: stage, CommitMsg: commitMsg})
		}
	}
	if jsonOut {
		emitJSON(map[string]any{"slug": slug, "released": true})
		return
	}
	absDir, _ := filepath.Abs(targetDir)
	projName := filepath.Base(absDir)
	fmt.Fprintf(os.Stdout, "%s Goal %s in %s released back to the pending queue.\n", ui.Green("✔"), ui.Bold(slug), ui.Magenta(ui.Bold(projName)))
	return

}

func (gc *goalsContext) handleReport(targetDir string, args []string) {
	ui := gc.ui
	st := gc.st
	note := gc.note
	status := gc.status
	stage := gc.stage
	commitMsg := gc.commitMsg
	noSync := gc.noSync
	jsonOut := gc.jsonOut
	if len(args) >= 2 && !strings.HasPrefix(args[1], "-") {
		slug := args[1]
		reportNote := note
		reportStatus := status
		remaining := args[2:]
		if len(remaining) > 0 {
			if reportStatus == "" && isStructuredStatus(remaining[0]) {
				reportStatus = normalizeStatus(remaining[0])
				if len(remaining) > 1 {
					reportNote = strings.Join(remaining[1:], " ")
				}
			} else {
				reportNote = strings.Join(remaining, " ")
			}
		}
		if reportStatus == "" {
			reportStatus = "in_progress"
		} else {
			reportStatus = normalizeStatus(reportStatus)
		}
		if targetDir == "" {
			targetDir, _ = goals.ResolveProjectDir(st, "")
		}
		claim, err := goals.ReportGoal(targetDir, slug, reportNote, reportStatus)
		if err != nil {
			if jsonOut {
				emitJSON(map[string]any{"error": err.Error(), "slug": slug})
				return
			}
			fmt.Fprintf(os.Stderr, "sila goals report error: %v\n", err)
			os.Exit(1)
		}
		if !noSync {
			if res, err := goals.ScanGoals(targetDir); err == nil {
				_ = goals.SyncReadmeWithOpts(targetDir, res, goals.SyncReadmeOptions{Stage: stage, CommitMsg: commitMsg})
			}
		}
		if jsonOut {
			emitJSON(map[string]any{"slug": slug, "claim": claim, "updated": true})
			return
		}
		absDir, _ := filepath.Abs(targetDir)
		projName := filepath.Base(absDir)
		fmt.Fprintf(os.Stdout, "%s Progress report updated for goal %s in %s\n", ui.Green("✔"), ui.Bold(slug), ui.Magenta(ui.Bold(projName)))
		if claim.Status != "" {
			fmt.Fprintf(os.Stdout, "  Status: %s\n", renderStatusBadge(ui, claim.Status))
		}
		if claim.Note != "" {
			fmt.Fprintf(os.Stdout, "  Note:   %s\n", ui.Yellow(claim.Note))
		}
		return
	}
}

func (gc *goalsContext) handleTouch(targetDir string, args []string) {
	ui := gc.ui
	st := gc.st
	agent := gc.agent
	stage := gc.stage
	commitMsg := gc.commitMsg
	noSync := gc.noSync
	jsonOut := gc.jsonOut
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: sila goals touch <slug> [--agent=<name>]")
		os.Exit(2)
	}
	slug := args[1]
	if targetDir == "" {
		targetDir, _ = goals.ResolveProjectDir(st, "")
	}
	touchAgent := agent
	if touchAgent == "" {
		touchAgent = os.Getenv("SILA_AGENT")
		if touchAgent == "" {
			touchAgent = "agent"
		}
	}
	claim, err := goals.TouchGoal(targetDir, slug, touchAgent)
	if err != nil {
		if jsonOut {
			emitJSON(map[string]any{"error": err.Error(), "slug": slug, "touched": false})
			return
		}
		fmt.Fprintf(os.Stderr, "sila goals touch error: %v\n", err)
		os.Exit(1)
	}
	if !noSync {
		if res, err := goals.ScanGoals(targetDir); err == nil {
			_ = goals.SyncReadmeWithOpts(targetDir, res, goals.SyncReadmeOptions{Stage: stage, CommitMsg: commitMsg})
		}
	}
	if jsonOut {
		emitJSON(map[string]any{"slug": slug, "touched": true, "claim": claim})
		return
	}
	absDir, _ := filepath.Abs(targetDir)
	projName := filepath.Base(absDir)
	fmt.Fprintf(os.Stdout, "%s Heartbeat renewed for goal %s in %s (lease extended)\n",
		ui.Green("✔"), ui.Bold(slug), ui.Magenta(ui.Bold(projName)))
	fmt.Fprintf(os.Stdout, "  Claimed By: @%s\n", claim.Agent)
	fmt.Fprintf(os.Stdout, "  Updated:    %s\n", claim.UpdatedAt.Format(time.RFC3339))
	return
}

func (gc *goalsContext) handleReview(targetDir string, args []string) {
	ui := gc.ui
	st := gc.st
	slug := ""
	if len(args) > 1 {
		slug = args[1]
	}
	if targetDir == "" {
		targetDir, _ = goals.ResolveProjectDir(st, "")
	}
	if slug == "" {
		res, err := goals.ScanGoals(targetDir)
		if err == nil {
			for _, g := range res.Goals {
				if g.Status == "pending" || g.Status == "claimed" {
					slug = g.Slug
					break
				}
			}
		}
	}
	if slug == "" {
		fmt.Fprintln(os.Stderr, "sila goals review: no pending or in-progress goal found to review")
		os.Exit(1)
	}
	if err := goals.ReviewWithPlannotator(ui, targetDir, slug); err != nil {
		fmt.Fprintf(os.Stderr, "sila goals review error: %v\n", err)
		os.Exit(1)
	}
	return
}

func (gc *goalsContext) handleTasks(targetDir string, args []string) {
	ui := gc.ui
	st := gc.st
	stage := gc.stage
	commitMsg := gc.commitMsg
	noSync := gc.noSync
	jsonOut := gc.jsonOut
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: sila goals tasks <slug> [check|uncheck <index|range...>]")
		os.Exit(2)
	}
	slug := args[1]
	if targetDir == "" {
		targetDir, _ = goals.ResolveProjectDir(st, "")
	}
	if len(args) >= 4 && (args[2] == "check" || args[2] == "uncheck" || args[2] == "complete") {
		indices, err := goals.ParseTaskIndices(args[3:])
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila goals task error: %v\n", err)
			os.Exit(2)
		}
		completed := args[2] != "uncheck"
		tasks, err := goals.ToggleGoalTasks(targetDir, slug, indices, completed)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila goals task error: %v\n", err)
			os.Exit(1)
		}
		if !noSync {
			if res, err := goals.ScanGoals(targetDir); err == nil {
				_ = goals.SyncReadmeWithOpts(targetDir, res, goals.SyncReadmeOptions{Stage: stage, CommitMsg: commitMsg})
			}
		}
		if jsonOut {
			emitJSON(tasks)
			return
		}
		state := ui.Green("completed")
		if !completed {
			state = ui.Yellow("pending")
		}
		if len(tasks) == 1 {
			fmt.Fprintf(os.Stdout, "%s Task %d for goal %s marked %s: %s\n", ui.Green("✔"), tasks[0].Index, ui.Bold(slug), state, tasks[0].Text)
		} else {
			fmt.Fprintf(os.Stdout, "%s %d tasks for goal %s marked %s (tasks %s)\n",
				ui.Green("✔"), len(tasks), ui.Bold(slug), state, formatIndicesList(tasks))
		}
		return
	}

	// List tasks
	tasks, err := goals.GetGoalTasks(targetDir, slug)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila goals tasks error: %v\n", err)
		os.Exit(1)
	}
	if jsonOut {
		emitJSON(tasks)
		return
	}
	met := 0
	for _, t := range tasks {
		if t.Completed {
			met++
		}
	}
	absDir, _ := filepath.Abs(targetDir)
	projName := filepath.Base(absDir)
	fmt.Fprintf(os.Stdout, "%s — %s (%d/%d completed) in %s:\n",
		ui.Cyan(ui.Bold("sila · goals · tasks")), ui.Bold(slug), met, len(tasks), ui.Magenta(ui.Bold(projName)))
	for _, t := range tasks {
		box := ui.Dim("[ ]")
		text := t.Text
		if t.Completed {
			box = ui.Green("[✔]")
			text = ui.Green(text)
		}
		fmt.Fprintf(os.Stdout, "  %s %2d. %s\n", box, t.Index, text)
	}
	return

}

func (gc *goalsContext) handleSupersede(targetDir string, args []string) {
	ui := gc.ui
	byOpt := gc.byOpt
	reason := gc.reason
	agent := gc.agent

	jsonOut := gc.jsonOut
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: sila goals supersede <slug> [--by=<other>] [--reason=\"...\"] [--agent=<name>]")
		os.Exit(1)
	}
	slug := args[1]
	supersededBy := byOpt
	supReason := reason
	supAgent := agent
	for _, a := range args[2:] {
		if after, ok := strings.CutPrefix(a, "--by="); ok {
			supersededBy = after
		} else if after, ok := strings.CutPrefix(a, "--reason="); ok {
			supReason = after
		} else if after, ok := strings.CutPrefix(a, "--agent="); ok {
			supAgent = after
		}
	}
	rec, err := goals.SupersedeGoal(targetDir, slug, supersededBy, supAgent, supReason)
	if err != nil {
		if jsonOut {
			emitJSON(map[string]any{"error": err.Error(), "slug": slug})
			return
		}
		fmt.Fprintf(os.Stderr, "sila goals supersede error: %v\n", err)
		os.Exit(1)
	}
	if jsonOut {
		emitJSON(rec)
		return
	}
	fmt.Fprintf(os.Stdout, "%s Goal %s marked as superseded\n", ui.Green("✔"), ui.Bold(slug))
	if rec.SupersededBy != "" {
		fmt.Fprintf(os.Stdout, "  Superseded By: %s\n", ui.Cyan(rec.SupersededBy))
	}
	if rec.Reason != "" {
		fmt.Fprintf(os.Stdout, "  Reason:        %s\n", ui.Yellow(rec.Reason))
	}
	return

}

func (gc *goalsContext) handleTest(targetDir string, args []string) {
	ui := gc.ui
	jsonOut := gc.jsonOut
	toonOut := gc.toonOut
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: sila goals test <slug> [--json] [--toon]")
		os.Exit(1)
	}
	slug := args[1]
	res, err := goals.RunVerificationContract(context.Background(), targetDir, slug)
	if err != nil && res == nil {
		if jsonOut {
			emitJSON(map[string]any{"error": err.Error(), "slug": slug})
			return
		}
		fmt.Fprintf(os.Stderr, "sila goals test error: %v\n", err)
		os.Exit(1)
	}
	// Record iteration against active claim if present
	_, tripped, _ := goals.RecordClaimIteration(targetDir, slug, res.Passed, res.FailureReason)
	if jsonOut {
		emitJSON(map[string]any{"result": res, "circuit_breaker_tripped": tripped})
		return
	}
	if toonOut {
		fields := [][2]string{
			{"slug", slug},
			{"passed", fmt.Sprintf("%t", res.Passed)},
			{"exit_code", fmt.Sprintf("%d", res.ExitCode)},
			{"duration_ms", fmt.Sprintf("%d", res.DurationMs)},
			{"circuit_breaker", fmt.Sprintf("%t", tripped)},
		}
		fmt.Fprint(os.Stdout, render.FormatTOONObject("goal_test_result", fields))
		return
	}
	if res.Passed {
		fmt.Fprintf(os.Stdout, "%s Verification contract for %s PASSED in %dms\n", ui.Green("✔"), ui.Bold(slug), res.DurationMs)
	} else {
		fmt.Fprintf(os.Stdout, "%s Verification contract for %s FAILED (exit code %d in %dms)\n", ui.Red("✖"), ui.Bold(slug), res.ExitCode, res.DurationMs)
		if res.FailureReason != "" {
			fmt.Fprintf(os.Stdout, "  Reason: %s\n", ui.Red(res.FailureReason))
		}
	}
	if tripped {
		fmt.Fprintf(os.Stdout, "  %s Circuit breaker tripped! Claim lock released to prevent endless agent looping.\n", ui.Yellow("⚠"))
	}
	if res.OutputSnippet != "" {
		fmt.Fprintln(os.Stdout, "\nOutput snippet:")
		fmt.Fprintln(os.Stdout, ui.Dim(res.OutputSnippet))
	}
	if !res.Passed {
		os.Exit(1)
	}
	return

}

func extractEvidenceErrorCode(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if os.IsNotExist(err) || strings.Contains(msg, "evidence file not found") || strings.Contains(msg, "no such file") {
		return "EVIDENCE_NOT_FOUND"
	}
	if strings.Contains(msg, "EVIDENCE_") {
		if idx := strings.Index(msg, ":"); idx != -1 {
			prefix := strings.TrimSpace(msg[:idx])
			if strings.HasPrefix(prefix, "EVIDENCE_") {
				return prefix
			}
		}
	}
	if strings.Contains(msg, "active non-overrulable reviewer veto") {
		return "REVIEWER_VETO_ACTIVE"
	}
	if strings.Contains(msg, "self-audit prohibited") {
		return "SELF_AUDIT_PROHIBITED"
	}
	return "EVIDENCE_ERROR"
}

func (gc *goalsContext) handleCertify(targetDir string, args []string) {
	ui := gc.ui
	evidenceOpt := gc.evidenceOpt
	agent := gc.agent
	note := gc.note
	jsonOut := gc.jsonOut
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: sila goals certify <slug> [--auto-run | --evidence=<file>] [--agent=<name>] [--note=\"...\"]")
		os.Exit(1)
	}
	slug := args[1]
	evidenceFile := evidenceOpt
	certAgent := agent
	certNote := note
	autoRun := gc.autoRun
	noMemory := gc.noMemory
	memoryReason := gc.reason
	sessionID := gc.sessionID
	certWaiver := ""
	for _, a := range args[2:] {
		if a == "--auto-run" || a == "-auto-run" {
			autoRun = true
		} else if a == "--no-memory" || a == "-no-memory" {
			noMemory = true
		} else if after, ok := strings.CutPrefix(a, "--evidence="); ok {
			evidenceFile = after
		} else if after, ok := strings.CutPrefix(a, "--agent="); ok {
			certAgent = after
		} else if after, ok := strings.CutPrefix(a, "--note="); ok {
			certNote = after
		} else if after, ok := strings.CutPrefix(a, "--reason="); ok {
			memoryReason = after
		} else if after, ok := strings.CutPrefix(a, "--waiver="); ok {
			certWaiver = after
		} else if after, ok := strings.CutPrefix(a, "--session="); ok {
			sessionID = after
		} else if after, ok := strings.CutPrefix(a, "-session="); ok {
			sessionID = after
		}
	}
	if sessionID == "" {
		sessionID = strings.TrimSpace(os.Getenv("SILA_SESSION_ID"))
	}
	for _, a := range append(args, os.Args...) {
		if a == "--json" || a == "-json" {
			jsonOut = true
		}
	}

	if autoRun {
		cert, res, err := goals.AutoCertifyGoal(context.Background(), targetDir, slug, certAgent, certNote)
		if err != nil {
			if jsonOut {
				emitJSON(map[string]any{"error": err.Error(), "slug": slug, "result": res, "accepted": false})
				os.Exit(1)
			}
			fmt.Fprintf(os.Stderr, "sila goals certify --auto-run error: %v\n", err)
			os.Exit(1)
		}
		if jsonOut {
			emitJSON(map[string]any{"certification": cert, "contract_result": res, "accepted": true})
			return
		}
		fmt.Fprintf(os.Stdout, "%s Goal %s verified and certified automatically\n", ui.Green("✔"), ui.Bold(slug))
		fmt.Fprintf(os.Stdout, "  Duration:      %dms\n", res.DurationMs)
		if cert.EvidenceFile != "" {
			fmt.Fprintf(os.Stdout, "  Evidence File: %s\n", ui.Cyan(cert.EvidenceFile))
		}
		if cert.Note != "" {
			fmt.Fprintf(os.Stdout, "  Note:          %s\n", ui.Yellow(cert.Note))
		}
		return
	}

	if !autoRun && contractCertifyBlocked(targetDir, slug, evidenceFile, certWaiver, jsonOut) {
		os.Exit(1)
	}
	cert, err := goals.CertifyGoalWithOptions(targetDir, slug, evidenceFile, certAgent, certNote, goals.CertifyOptions{NoMemory: noMemory, Reason: memoryReason, SessionID: sessionID})
	if err != nil {
		exitCode := 1
		if strings.Contains(err.Error(), "MEMORY_WAIVER_REASON_REQUIRED") {
			exitCode = 2
		}
		if jsonOut {
			code := extractEvidenceErrorCode(err)
			if code == "EVIDENCE_ERROR" {
				code = "CERTIFICATION_ERROR"
			}
			emitJSON(map[string]any{"error": err.Error(), "slug": slug, "code": code, "accepted": false})
			os.Exit(exitCode)
		}
		fmt.Fprintf(os.Stderr, "sila goals certify error: %v\n", err)
		os.Exit(exitCode)
	}
	if jsonOut {
		emitJSON(map[string]any{
			"certification": cert,
			"accepted":      true,
			"verified":      cert.Verified,
			"goal_slug":     cert.GoalSlug,
			"certified_by":  cert.CertifiedBy,
			"certified_at":  cert.CertifiedAt,
			"evidence_file": cert.EvidenceFile,
			"memory":        cert.Memory,
		})
		return
	}
	fmt.Fprintf(os.Stdout, "%s Goal %s certified from evidence\n", ui.Green("✔"), ui.Bold(slug))
	if cert.EvidenceFile != "" {
		fmt.Fprintf(os.Stdout, "  Evidence File: %s\n", ui.Cyan(cert.EvidenceFile))
	}
	if cert.Note != "" {
		fmt.Fprintf(os.Stdout, "  Note:          %s\n", ui.Yellow(cert.Note))
	}
	if cert.Memory != nil {
		if cert.Memory.Status == "legacy_skipped" {
			fmt.Fprintf(os.Stdout, "  Memory:        %s\n", ui.Yellow(fmt.Sprintf("memory protocol not required for legacy goals (missing '- **Memory Protocol**: required' in goals/%s/goal.md; status: legacy_skipped)", slug)))
		} else if cert.Memory.Status != "" {
			fmt.Fprintf(os.Stdout, "  Memory:        %s\n", ui.Green(cert.Memory.Status))
		}
	}
	return
}

// handleStaleAudit audits the next bounded batch of goals that have changed
// content or only hold a legacy audit receipt. It runs one goal at a time,
// records each outcome in .sila/verification-audit-queue.json, and never
// retries a conclusive or inconclusive attempt automatically.
func (gc *goalsContext) handleStaleAudit(targetDir string, args []string) {
	limit := 10
	if gc.limitSet && gc.limitOpt > 0 {
		limit = gc.limitOpt
	}
	dryRun := gc.dryRun
	for i := 0; i < len(args); i++ {
		a := args[i]
		if after, ok := strings.CutPrefix(a, "--limit="); ok {
			if n, err := strconv.Atoi(after); err == nil && n > 0 {
				limit = n
			}
		} else if (a == "--limit" || a == "-limit") && i+1 < len(args) {
			if n, err := strconv.Atoi(args[i+1]); err == nil && n > 0 {
				limit = n
			}
			i++
		} else if a == "--dry-run" {
			dryRun = true
		}
	}

	root := gc.verificationRoot(targetDir)
	candidates, err := goals.SelectStaleAuditCandidates(root, limit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila goals audit --stale: %v\n", err)
		os.Exit(1)
	}
	if dryRun {
		if gc.jsonOut {
			emitJSON(map[string]any{"batch": "stale", "limit": limit, "dry_run": true, "candidates": candidates})
			return
		}
		fmt.Fprintf(os.Stdout, "Stale audit batch: %d candidate(s), limit %d\n", len(candidates), limit)
		for _, c := range candidates {
			fmt.Fprintf(os.Stdout, "  %-14s %s\n", c.State, c.Slug)
		}
		return
	}

	personaID := "mystery-auditor"
	timeout := 10 * time.Minute
	for i := 0; i < len(args); i++ {
		if after, ok := strings.CutPrefix(args[i], "--hire="); ok {
			personaID = after
		} else if after, ok := strings.CutPrefix(args[i], "--persona="); ok {
			personaID = after
		} else if after, ok := strings.CutPrefix(args[i], "--timeout="); ok {
			if d, err := time.ParseDuration(after); err == nil {
				timeout = d
			}
		}
	}

	passed, rejected, inconclusive := 0, 0, 0
	for _, c := range candidates {
		if !gc.jsonOut {
			fmt.Fprintf(os.Stdout, "%s auditing %s (%s)\n", gc.ui.Cyan("·"), gc.ui.Bold(c.Slug), c.State)
		}
		eval, err := audit.AuditGoal(context.Background(), gc.st, audit.GoalAuditOptions{
			GoalSlug:   c.Slug,
			ProjectDir: root,
			PersonaID:  personaID,
			SessionID:  gc.sessionID,
			CommitSHA:  strings.TrimSpace(gc.commitMsg),
			Timeout:    timeout,
			Blindfold:  true,
		})
		switch {
		case err != nil:
			inconclusive++
			if _, rerr := goals.RecordAuditOutcome(root, c.Slug, goals.AuditStateInconclusive, err.Error()); rerr != nil {
				fmt.Fprintf(os.Stderr, "sila goals audit --stale: record %s: %v\n", c.Slug, rerr)
			}
			if !gc.jsonOut {
				fmt.Fprintf(os.Stdout, "  %s %s: inconclusive - %v\n", gc.ui.Yellow("▲"), c.Slug, err)
			}
		case eval == nil:
			inconclusive++
			if _, rerr := goals.RecordAuditOutcome(root, c.Slug, goals.AuditStateInconclusive, "audit returned no evaluation"); rerr != nil {
				fmt.Fprintf(os.Stderr, "sila goals audit --stale: record %s: %v\n", c.Slug, rerr)
			}
			if !gc.jsonOut {
				fmt.Fprintf(os.Stdout, "  %s %s: inconclusive - no evaluation\n", gc.ui.Yellow("▲"), c.Slug)
			}
		case eval.Verdict == assess.VerdictPass:
			if _, verr := goals.RecordVerification(root, c.Slug, goals.LevelL3, "audit"); verr != nil {
				inconclusive++
				reason := "audit passed but content could not be bound: " + verr.Error()
				if _, rerr := goals.RecordAuditOutcome(root, c.Slug, goals.AuditStateInconclusive, reason); rerr != nil {
					fmt.Fprintf(os.Stderr, "sila goals audit --stale: record %s: %v\n", c.Slug, rerr)
				}
				if !gc.jsonOut {
					fmt.Fprintf(os.Stdout, "  %s %s: %s\n", gc.ui.Yellow("▲"), c.Slug, reason)
				}
				continue
			}
			passed++
			if _, rerr := goals.RecordAuditOutcome(root, c.Slug, goals.AuditStatePassed, ""); rerr != nil {
				fmt.Fprintf(os.Stderr, "sila goals audit --stale: record %s: %v\n", c.Slug, rerr)
			}
			if !gc.jsonOut {
				fmt.Fprintf(os.Stdout, "  %s %s: passed and bound at L3\n", gc.ui.Green("✔"), c.Slug)
			}
		case eval.Verdict == assess.VerdictInconclusive:
			inconclusive++
			reason := eval.InconclusiveReason
			if reason == "" {
				reason = "audit could not decide"
			}
			if _, rerr := goals.RecordAuditOutcome(root, c.Slug, goals.AuditStateInconclusive, reason); rerr != nil {
				fmt.Fprintf(os.Stderr, "sila goals audit --stale: record %s: %v\n", c.Slug, rerr)
			}
			if !gc.jsonOut {
				fmt.Fprintf(os.Stdout, "  %s %s: inconclusive - %s\n", gc.ui.Yellow("▲"), c.Slug, reason)
			}
		default:
			rejected++
			reason := "audit verdict did not pass"
			if eval.CCDSI.FailureReason != "" {
				reason = eval.CCDSI.FailureReason
			}
			if _, rerr := goals.RecordAuditOutcome(root, c.Slug, goals.AuditStateRejected, reason); rerr != nil {
				fmt.Fprintf(os.Stderr, "sila goals audit --stale: record %s: %v\n", c.Slug, rerr)
			}
			if !gc.jsonOut {
				fmt.Fprintf(os.Stdout, "  %s %s: rejected - %s\n", gc.ui.Red("✖"), c.Slug, reason)
			}
		}
	}

	if gc.jsonOut {
		emitJSON(map[string]any{
			"batch": "stale", "candidates": len(candidates),
			"passed": passed, "rejected": rejected, "inconclusive": inconclusive,
		})
		return
	}
	fmt.Fprintf(os.Stdout, "Stale audit batch: %d candidate(s), %s passed, %s rejected, %s inconclusive\n",
		len(candidates), gc.ui.Green(fmt.Sprint(passed)), gc.ui.Red(fmt.Sprint(rejected)), gc.ui.Yellow(fmt.Sprint(inconclusive)))
	if inconclusive > 0 {
		fmt.Fprintln(os.Stdout, gc.ui.Dim("  Inconclusive goals stay unbound and are not retried automatically; re-run `sila goals audit <slug>` to retry one."))
	}
}

func (gc *goalsContext) handleAudit(targetDir string, args []string) {
	if gc.staleRelease {
		gc.handleStaleAudit(targetDir, args)
		return
	}
	slug := ""
	personaID := "mystery-auditor"
	targetURL := ""
	targetBin := ""
	timeoutStr := ""
	commitSHA := strings.TrimSpace(gc.commitMsg)
	skipCanary := false

	if len(args) > 1 && !strings.HasPrefix(args[1], "-") {
		slug = args[1]
		args = args[2:]
	} else if len(args) > 0 && args[0] == "audit" {
		args = args[1:]
	}

	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "--slug=") {
			slug = strings.TrimPrefix(a, "--slug=")
		} else if strings.HasPrefix(a, "--hire=") || strings.HasPrefix(a, "--persona=") {
			personaID = strings.TrimPrefix(strings.TrimPrefix(a, "--hire="), "--persona=")
		} else if strings.HasPrefix(a, "--url=") {
			targetURL = strings.TrimPrefix(a, "--url=")
		} else if strings.HasPrefix(a, "--bin=") || strings.HasPrefix(a, "--binary=") {
			targetBin = strings.TrimPrefix(strings.TrimPrefix(a, "--bin="), "--binary=")
		} else if strings.HasPrefix(a, "--timeout=") {
			timeoutStr = strings.TrimPrefix(a, "--timeout=")
		} else if after, ok := strings.CutPrefix(a, "--commit="); ok {
			commitSHA = strings.TrimSpace(after)
		} else if after, ok := strings.CutPrefix(a, "-commit="); ok {
			commitSHA = strings.TrimSpace(after)
		} else if after, ok := strings.CutPrefix(a, "-c="); ok {
			commitSHA = strings.TrimSpace(after)
		} else if (a == "--commit" || a == "-commit" || a == "-c") && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
			commitSHA = strings.TrimSpace(args[i+1])
			i++
		} else if a == "--skip-canary" {
			skipCanary = true
		} else if !strings.HasPrefix(a, "-") && slug == "" {
			slug = a
		}
	}

	if slug == "" {
		fmt.Fprintln(os.Stderr, "Usage: sila goals audit <slug> [--commit=<sha>] [--hire=<persona>] [--url=<url>] [--bin=<binary>] [--timeout=10m]")
		os.Exit(2)
	}

	if targetDir == "" {
		targetDir, _ = goals.ResolveProjectDir(gc.st, "")
	}

	timeout := 10 * time.Minute
	if timeoutStr != "" {
		if d, err := time.ParseDuration(timeoutStr); err == nil {
			timeout = d
		}
	}

	opts := audit.GoalAuditOptions{
		GoalSlug:     slug,
		ProjectDir:   targetDir,
		PersonaID:    personaID,
		SessionID:    gc.sessionID,
		CommitSHA:    commitSHA,
		TargetURL:    targetURL,
		TargetBinary: targetBin,
		Timeout:      timeout,
		Blindfold:    true,
		SkipCanary:   skipCanary,
	}

	if !gc.jsonOut {
		fmt.Fprintf(os.Stdout, "%s sila · auditing goal %s with mystery auditor...\n", gc.ui.Cyan("·"), gc.ui.Bold(slug))
	}
	eval, err := audit.AuditGoal(context.Background(), gc.st, opts)
	if err != nil {
		if gc.jsonOut {
			emitJSON(map[string]any{"error": err.Error(), "goal": slug})
			return
		}
		fmt.Fprintf(os.Stderr, "sila goals audit error: %v\n", err)
		os.Exit(1)
	}

	if gc.jsonOut {
		emitJSON(eval)
		if eval.Verdict != "PASS" {
			os.Exit(auditExitCode(eval.Verdict))
		}
		return
	}

	switch eval.Verdict {
	case "PASS":
		fmt.Fprintf(os.Stdout, "%s Goal %s passed epistemic mystery audit!\n\n", gc.ui.Green("✔"), gc.ui.Bold(slug))
		fmt.Fprintf(os.Stdout, "  CCDSI Score:          %0.2f (floor: 0.40)\n", eval.CCDSI.CCDSIScore)
		fmt.Fprintf(os.Stdout, "  Probes Passed:        %d/%d\n", eval.CCDSI.PassedProbes, eval.CCDSI.TotalProbes)
		fmt.Fprintf(os.Stdout, "  Requirement Coverage: %0.2f (floor: 0.50)\n", eval.CCDSI.ReqCoverage)
		fmt.Fprintf(os.Stdout, "  Canary Detected:      %v (%s)\n", eval.CanaryPassed, eval.CanaryStatus)
		fmt.Fprintf(os.Stdout, "  Blindfold Isolation:  %v\n", eval.BlindfoldVerified)
		if eval.Receipt != nil {
			fmt.Fprintf(os.Stdout, "  Receipt:              goals/%s/audit-receipt.json\n", slug)
		}
	case "INCONCLUSIVE":
		fmt.Fprintf(os.Stdout, "%s Goal %s audit was inconclusive; the goal state is unchanged.\n\n", gc.ui.Yellow("▲"), gc.ui.Bold(slug))
		fmt.Fprintf(os.Stdout, "  Canary Status:        %s\n", eval.CanaryStatus)
		fmt.Fprintf(os.Stdout, "  Canary Detail:        %s\n", eval.CanaryDetail)
		fmt.Fprintf(os.Stdout, "  Inconclusive Reason:  %s\n", eval.InconclusiveReason)
		fmt.Fprintf(os.Stdout, "  Probes Passed:        %d/%d\n", eval.CCDSI.PassedProbes, eval.CCDSI.TotalProbes)
		os.Exit(auditExitCode(eval.Verdict))
	default:
		fmt.Fprintf(os.Stdout, "%s Goal %s was rejected by mystery audit and reopened for remediation.\n\n", gc.ui.Red("✖"), gc.ui.Bold(slug))
		fmt.Fprintf(os.Stdout, "  CCDSI Score:          %0.2f\n", eval.CCDSI.CCDSIScore)
		fmt.Fprintf(os.Stdout, "  Probes Passed:        %d/%d\n", eval.CCDSI.PassedProbes, eval.CCDSI.TotalProbes)
		fmt.Fprintf(os.Stdout, "  Failure Reason:       %s\n", eval.CCDSI.FailureReason)
		if eval.CanaryStatus != "" && eval.CanaryStatus != "passed" && eval.CanaryStatus != "skipped" {
			fmt.Fprintf(os.Stdout, "  Canary Status:        %s\n", eval.CanaryStatus)
			fmt.Fprintf(os.Stdout, "  Canary Detail:        %s\n", eval.CanaryDetail)
		}
		if eval.Dossier != nil {
			fmt.Fprintf(os.Stdout, "  Failed Probe:         %s\n", eval.Dossier.FailedProbeName)
			fmt.Fprintf(os.Stdout, "  Defect Dossier:       goals/%s/defect-dossier.json\n", slug)
			fmt.Fprintf(os.Stdout, "  Remediation Guide:    goals/%s/remediation.md\n", slug)
		}
		os.Exit(auditExitCode(eval.Verdict))
	}
}

// auditExitCode maps an audit verdict to a process exit code.
func auditExitCode(verdict string) int {
	switch verdict {
	case "PASS":
		return 0
	case "INCONCLUSIVE":
		return 3
	default:
		return 1
	}
}

func (gc *goalsContext) handleConflicts(targetDir string, args []string) {
	ui := gc.ui
	jsonOut := gc.jsonOut
	slug := ""
	if len(args) > 1 {
		slug = args[1]
	}
	res, err := goals.ScanGoals(targetDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila goals conflicts error: %v\n", err)
		os.Exit(1)
	}
	var conflicts []goals.FileConflict
	if slug != "" {
		for _, g := range res.Goals {
			if g.Slug == slug {
				conflicts = g.FileConflicts
				break
			}
		}
	} else {
		conflicts = res.FileConflicts
	}
	if jsonOut {
		emitJSON(map[string]any{"conflicts": conflicts, "count": len(conflicts)})
		return
	}
	if len(conflicts) == 0 {
		if slug != "" {
			fmt.Fprintf(os.Stdout, "%s No file concurrency conflicts detected for goal %s.\n", ui.Green("✔"), ui.Bold(slug))
		} else {
			fmt.Fprintf(os.Stdout, "%s Zero file concurrency conflicts across all goals in %s.\n", ui.Green("✔"), ui.Magenta(ui.Bold(res.Project)))
		}
		return
	}
	fmt.Fprintf(os.Stdout, "⚠️  %s in %s (%d detected):\n", ui.Red(ui.Bold("FILE CONCURRENCY CONFLICTS")), ui.Magenta(ui.Bold(res.Project)), len(conflicts))
	for _, cf := range conflicts {
		fmt.Fprintf(os.Stdout, "  - Goal %s overlaps with active goal %s (claimed by @%s) on %v\n",
			ui.Bold(cf.GoalSlug), ui.Bold(cf.ConflictingGoal), ui.Cyan(cf.HolderAgent), cf.OverlappingFiles)
	}
	return
}

func (gc *goalsContext) handleImport(targetDir string, args []string) {
	ui := gc.ui
	jsonOut := gc.jsonOut
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: sila goals import <slug> [--from=<dir>]")
		os.Exit(1)
	}
	slug := args[1]
	fromDir := ""
	for _, a := range args[2:] {
		if after, ok := strings.CutPrefix(a, "--from="); ok {
			fromDir = after
		} else if after, ok := strings.CutPrefix(a, "--dir="); ok {
			fromDir = after
		}
	}
	if err := goals.ImportPlannotatorPackage(targetDir, slug, fromDir); err != nil {
		if jsonOut {
			emitJSON(map[string]any{"error": err.Error(), "slug": slug})
			return
		}
		fmt.Fprintf(os.Stderr, "sila goals import error: %v\n", err)
		os.Exit(1)
	}
	if jsonOut {
		emitJSON(map[string]any{"slug": slug, "imported": true})
		return
	}
	fmt.Fprintf(os.Stdout, "%s Plannotator package imported for goal %s\n", ui.Green("✔"), ui.Bold(slug))
	fmt.Fprintf(os.Stdout, "  Goal package: goals/%s (goal.md, facts.md, plan.md)\n", slug)
}

// handleContract implements the harness-neutral goal contract surfaces.
func (gc *goalsContext) handleContract(targetDir string, args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: sila goals contract <link|unlink|status|check|waive|set> [flags]")
		os.Exit(2)
	}
	if targetDir == "" {
		targetDir, _ = goals.ResolveProjectDir(gc.st, "")
	}
	if targetDir == "" {
		fmt.Fprintln(os.Stderr, "sila: cannot resolve project directory")
		os.Exit(1)
	}
	jsonOut := gc.jsonOut || contractBool(args, "--json")
	sub := strings.ToLower(args[1])
	switch sub {
	case "link":
		slug := contractFlag(args, "--goal")
		if slug == "" {
			fmt.Fprintln(os.Stderr, "Usage: sila goals contract link --session=<id> --goal=<slug> [--harness=] [--expires=24h]")
			os.Exit(2)
		}
		session := contractFlag(args, "--session")
		if session == "" {
			session = gc.session
		}
		if session == "" {
			fmt.Fprintln(os.Stderr, "sila: --session is required")
			os.Exit(2)
		}
		audit, err := goals.ScanGoals(targetDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila: %v\n", err)
			os.Exit(1)
		}
		var found *goals.Goal
		for i := range audit.Goals {
			if audit.Goals[i].Slug == slug {
				found = &audit.Goals[i]
				break
			}
		}
		if found == nil {
			fmt.Fprintf(os.Stderr, "sila: goal %q not found\n", slug)
			os.Exit(1)
		}
		expires := 24 * time.Hour
		if raw := contractFlag(args, "--expires"); raw != "" {
			if d, err := time.ParseDuration(raw); err == nil && d > 0 {
				expires = d
			}
		}
		if expires > 7*24*time.Hour {
			expires = 7 * 24 * time.Hour
		}
		b := contract.Binding{
			Schema: contract.BindingSchema, Harness: contract.HarnessID(contractHarness(args)),
			SessionID: session, HarnessGoalID: contractFlag(args, "--harness-goal"),
			SilaGoalSlug: slug, Source: "explicit",
			CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(expires),
		}
		if found.Claim != nil {
			b.ClaimID = found.Claim.ClaimedBy
		}
		if err := contract.SaveBinding(targetDir, b); err != nil {
			fmt.Fprintf(os.Stderr, "sila: %v\n", err)
			os.Exit(1)
		}
		if jsonOut {
			emitJSON(map[string]any{"linked": true, "binding": b, "path": contract.BindingPath(targetDir, b.Harness, session)})
			return
		}
		fmt.Printf("linked %s -> %s (expires %s)\n", session, slug, b.ExpiresAt.Format(time.RFC3339))
	case "unlink":
		session := contractFlag(args, "--session")
		if session == "" {
			session = gc.session
		}
		if session == "" {
			fmt.Fprintln(os.Stderr, "sila: --session is required")
			os.Exit(2)
		}
		h := contract.HarnessID(contractHarness(args))
		if err := contract.RemoveBinding(targetDir, h, session); err != nil {
			fmt.Fprintf(os.Stderr, "sila: %v\n", err)
			os.Exit(1)
		}
		if jsonOut {
			emitJSON(map[string]any{"unlinked": true, "session_id": session})
			return
		}
		fmt.Printf("unlinked %s\n", session)
	case "status":
		h := contract.HarnessID(contractHarness(args))
		session := contractFlag(args, "--session")
		if session == "" {
			session = gc.session
		}
		b, _ := contract.LoadBinding(targetDir, h, session)
		pol := contract.ResolvePolicy(targetDir)
		claimSlug, claimSession := contractActiveClaim(targetDir)
		if session == "" {
			session = claimSession
		}
		if b == nil && claimSlug != "" {
			b = &contract.Binding{Schema: contract.BindingSchema, Harness: h, SessionID: session, SilaGoalSlug: claimSlug, Source: "claim"}
		}
		if jsonOut {
			emitJSON(map[string]any{"binding": b, "policy": pol, "active_claim_goal": claimSlug})
			return
		}
		if b == nil {
			fmt.Println("goal contract: unbound")
		} else {
			fmt.Printf("goal contract: %s (%s)\n", b.SilaGoalSlug, b.Source)
		}
		fmt.Printf("policy: %s (%s)\n", pol.Policy, pol.Source)
	case "check":
		h := contract.HarnessID(contractHarness(args))
		session := contractFlag(args, "--session")
		if session == "" {
			session = gc.session
		}
		slug := contractFlag(args, "--goal")
		if session == "" || slug == "" {
			claimSlug, claimSession := contractActiveClaim(targetDir)
			if slug == "" {
				slug = claimSlug
			}
			if session == "" {
				session = claimSession
			}
		}
		if session == "" {
			fmt.Fprintln(os.Stderr, "sila: --session is required (no active claim session found)")
			os.Exit(2)
		}
		phase := contract.PhaseSessionEnd
		switch strings.ToLower(contractFlag(args, "--phase")) {
		case "stop", "turn-end":
			phase = contract.PhaseStop
		case "certify":
			phase = contract.PhaseCertify
		case "handoff":
			phase = contract.PhaseHandoff
		case "session-start":
			phase = contract.PhaseSessionStart
		}
		sig := contract.NormalizeSignal(contract.Signal{
			Harness: h, SessionID: session, ProjectID: store.ProjectID(targetDir),
			Cwd: targetDir, Phase: phase, SilaGoalSlug: slug,
		})
		pol := contract.ResolvePolicy(targetDir)
		d := contract.NewChecker(gc.st, targetDir).Evaluate(sig, pol)
		d = contract.RecordDecision(targetDir, sig, pol, d, time.Duration(d.DurationMS)*time.Millisecond)
		if jsonOut {
			emitJSON(d)
			if d.Decision == contract.DecisionDeny {
				os.Exit(1)
			}
			return
		}
		fmt.Printf("goal contract: %s\n", contract.Summary(d))
		for _, name := range []string{"session", "binding", "claim", "tasks", "evidence", "facts", "handoff", "memory", "adoption"} {
			if cr, ok := d.Checks[name]; ok {
				fmt.Printf("  %-8s %-5s %s\n", name, cr.Status, cr.Detail)
			}
		}
		if d.ReceiptPath != "" {
			fmt.Printf("receipt: %s\n", d.ReceiptPath)
		}
		if d.Decision == contract.DecisionDeny {
			os.Exit(1)
		}
	case "waive":
		session := contractFlag(args, "--session")
		if session == "" {
			session = gc.session
		}
		reason := contractFlag(args, "--reason")
		if session == "" || reason == "" {
			fmt.Fprintln(os.Stderr, "Usage: sila goals contract waive --session=<id> --reason=\"...\" [--check=handoff,facts] [--expires=168h]")
			os.Exit(2)
		}
		expires := 7 * 24 * time.Hour
		if raw := contractFlag(args, "--expires"); raw != "" {
			if d, err := time.ParseDuration(raw); err == nil && d > 0 {
				expires = d
			}
		}
		if expires > 7*24*time.Hour {
			expires = 7 * 24 * time.Hour
		}
		w := contract.Waiver{
			Schema: contract.WaiverSchema, Harness: contract.HarnessID(contractHarness(args)),
			SessionID: session, Reason: reason, Author: gc.agent,
			CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(expires),
		}
		if raw := contractFlag(args, "--check"); raw != "" {
			for _, c := range strings.Split(raw, ",") {
				if c = strings.TrimSpace(c); c != "" {
					w.Checks = append(w.Checks, c)
				}
			}
		}
		if err := contract.SaveWaiver(targetDir, w); err != nil {
			fmt.Fprintf(os.Stderr, "sila: %v\n", err)
			os.Exit(1)
		}
		if jsonOut {
			emitJSON(map[string]any{"waived": true, "waiver": w})
			return
		}
		fmt.Printf("waived %s until %s\n", session, w.ExpiresAt.Format(time.RFC3339))
	case "set":
		gc.handleContractSet(targetDir, args)
	default:
		fmt.Fprintf(os.Stderr, "sila: unknown contract subcommand %q\n", sub)
		os.Exit(2)
	}
}

func contractHarness(args []string) string {
	if h := contractFlag(args, "--harness"); h != "" {
		return h
	}
	if h := os.Getenv("SILA_HARNESS"); h != "" {
		return h
	}
	return "unknown"
}

func contractActiveClaim(targetDir string) (string, string) {
	audit, err := goals.ScanGoals(targetDir)
	if err != nil {
		return "", ""
	}
	for i := range audit.Goals {
		g := &audit.Goals[i]
		if g.Superseded || g.Claim == nil || g.Claim.Stale {
			continue
		}
		return g.Slug, g.Claim.SessionID
	}
	return "", ""
}

func contractFlag(args []string, name string) string {
	for _, set := range [][]string{args, os.Args} {
		for i, a := range set {
			if a == name && i+1 < len(set) {
				return set[i+1]
			}
			if strings.HasPrefix(a, name+"=") {
				return strings.TrimPrefix(a, name+"=")
			}
		}
	}
	return ""
}

func contractBool(args []string, name string) bool {
	for _, set := range [][]string{args, os.Args} {
		for _, a := range set {
			if a == name {
				return true
			}
		}
	}
	return false
}

// contractCertifyBlocked refuses certification when the active goal contract
// fails. A non-empty waiver is recorded on the receipt and allows the call.
func contractCertifyBlocked(targetDir, slug, evidenceFile, waiverReason string, jsonOut bool) bool {
	audit, err := goals.ScanGoals(targetDir)
	if err != nil {
		return false
	}
	var goal *goals.Goal
	for i := range audit.Goals {
		if audit.Goals[i].Slug == slug {
			goal = &audit.Goals[i]
			break
		}
	}
	if goal == nil || goal.Claim == nil || goal.Claim.Stale {
		return false
	}
	session := goal.Claim.SessionID
	if session == "" {
		session = "unbound"
	}
	policy := contract.ResolvePolicy(targetDir)
	if policy.Policy == contract.PolicyOff {
		return false
	}
	sig := contract.NormalizeSignal(contract.Signal{
		Harness: contract.HarnessID(goal.Claim.Agent), SessionID: session,
		ProjectID: store.ProjectID(targetDir), Cwd: targetDir,
		Phase: contract.PhaseCertify, SilaGoalSlug: slug,
	})
	if evidenceFile != "" {
		sig.EvidencePaths = []string{evidenceFile}
	}
	st, err := store.Open(store.DefaultDBPath())
	if err != nil {
		return false
	}
	defer st.Close()
	d := contract.NewChecker(st, targetDir).Evaluate(sig, policy)
	d = contract.RecordDecision(targetDir, sig, policy, d, 0)
	if d.Decision != contract.DecisionDeny {
		return false
	}
	if waiverReason != "" {
		if !jsonOut {
			fmt.Fprintf(os.Stderr, "sila: contract waiver: %s (%s)\n", waiverReason, contract.Summary(d))
		}
		_ = contract.SaveWaiver(targetDir, contract.Waiver{
			Schema: contract.WaiverSchema, Harness: sig.Harness, SessionID: session,
			Reason: waiverReason, Author: goal.Claim.Agent,
			CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(24 * time.Hour),
		})
		return false
	}
	if jsonOut {
		emitJSON(map[string]any{"error": "goal contract failed", "slug": slug, "contract": d, "accepted": false})
	} else {
		fmt.Fprintf(os.Stderr, "sila goals certify blocked by contract: %s\n", contract.Summary(d))
	}
	return true
}

// handleReconcileTree reports (and optionally records) attributions for goals
// whose work is present in the current tree after a history rewrite.
func (gc *goalsContext) handleReconcileTree(targetDir string, args []string) {
	tree := false
	apply := false
	dryRun := false
	allowDirty := false
	jsonOut := gc.jsonOut
	for _, a := range args {
		switch a {
		case "--tree":
			tree = true
		case "--apply":
			apply = true
		case "--dry-run":
			dryRun = true
		case "--allow-dirty":
			allowDirty = true
		case "--json":
			jsonOut = true
		}
	}
	if !tree {
		fmt.Fprintln(os.Stderr, "Usage: sila goals reconcile --tree [--apply] [--dry-run] [--allow-dirty] [--json]")
		os.Exit(2)
	}
	if dryRun {
		apply = false
	}
	if targetDir == "" {
		targetDir, _ = goals.ResolveProjectDir(gc.st, "")
	}
	report, err := goals.ReconcileTree(targetDir, apply, allowDirty)
	if err != nil {
		if jsonOut {
			emitJSON(map[string]any{"error": err.Error()})
			return
		}
		fmt.Fprintf(os.Stderr, "sila goals reconcile: %v\n", err)
		os.Exit(1)
	}
	if jsonOut {
		emitJSON(report)
		return
	}
	if !apply {
		if len(report.Candidates) == 0 {
			fmt.Fprintln(os.Stdout, "No complete goals need tree reconciliation.")
			return
		}
		fmt.Fprintf(os.Stdout, "%s goals reconcile --tree (dry run)\n\n", gc.ui.Cyan(gc.ui.Bold("sila")))
		for _, c := range report.Candidates {
			fmt.Fprintf(os.Stdout, "  %-40s %d/%d tasks  %d files  %s\n", c.Slug, c.PlanChecked, c.PlanTotal, len(c.MatchedFiles), c.Head)
		}
		fmt.Fprintf(os.Stdout, "\n%d candidate(s); pass --apply to record tree attribution. Nothing was certified.\n", len(report.Candidates))
		return
	}
	if len(report.Applied) == 0 {
		fmt.Fprintln(os.Stdout, "No complete goals need tree reconciliation.")
		return
	}
	for _, c := range report.Applied {
		fmt.Fprintf(os.Stdout, "%s attributed %s to %s (implemented, not verified)\n", gc.ui.Green("✔"), gc.ui.Bold(c.Slug), c.Head)
	}
	fmt.Fprintf(os.Stdout, "\n%d goal(s) attributed by tree-reconcile; certification is unchanged.\n", len(report.Applied))
}
