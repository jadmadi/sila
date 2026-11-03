package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/harness/contract"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func isStructuredStatus(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "in_progress", "in-progress", "blocked", "evaluating", "review", "done", "completed":
		return true
	default:
		return false
	}
}

func normalizeStatus(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "in-progress", "in_progress":
		return "in_progress"
	case "evaluating", "review":
		return "evaluating"
	case "done", "completed":
		return "done"
	case "blocked":
		return "blocked"
	default:
		return s
	}
}

func renderStatusBadge(ui render.UI, s string) string {
	switch strings.ToLower(s) {
	case "blocked":
		return ui.Red("⛔ [BLOCKED]")
	case "evaluating", "review":
		return ui.Yellow("🔍 [EVALUATING]")
	case "done", "completed":
		return ui.Green("✔ [DONE]")
	case "in_progress", "in-progress":
		return ui.Cyan("🔵 [IN-PROGRESS]")
	default:
		return ui.Cyan(fmt.Sprintf("[%s]", strings.ToUpper(s)))
	}
}

func formatIndicesList(tasks []*goals.GoalTask) string {
	var parts []string
	for _, t := range tasks {
		parts = append(parts, fmt.Sprintf("#%d", t.Index))
	}
	return strings.Join(parts, ", ")
}

// runGoals audits goal packages under goals/, manages claims/locks, matches git commits, and auto-syncs goals/README.md.
// goalsRunOptions carries the less common goals flags without widening the
// already long positional signature for every caller.
type goalsRunOptions struct {
	session       string
	stale         bool
	olderThan     string
	claims        bool
	dryRun        bool
	check         bool
	by            string
	evidence      string
	tier          string
	invariants    string
	tasks         string
	workPackage   string
	specReview    string
	review        string
	noBuild       bool
	checkTasks    bool
	allow         string
	deny          string
	duration      string
	stats         bool
	strict        bool
	strictMemory  bool
	noMemory      bool
	manifest      string
	line          bool
	full          bool
	live          bool
	interval      string
	personas      string
	addPersonas   string
	clearPersonas bool
	selectionFile string
	autoRun       bool
	ascii         bool
}

func extractAttributeArgs(args []string, commitFlag string) (string, string, error) {
	tokens := args
	if len(tokens) > 0 && strings.EqualFold(tokens[0], "attribute") {
		tokens = tokens[1:]
	}

	slug := ""
	commitOpt := strings.TrimSpace(commitFlag)

	for i := 0; i < len(tokens); i++ {
		t := tokens[i]
		if after, ok := strings.CutPrefix(t, "--commit="); ok {
			commitOpt = strings.TrimSpace(after)
		} else if after, ok := strings.CutPrefix(t, "-commit="); ok {
			commitOpt = strings.TrimSpace(after)
		} else if after, ok := strings.CutPrefix(t, "-c="); ok {
			commitOpt = strings.TrimSpace(after)
		} else if t == "--commit" || t == "-commit" || t == "-c" {
			if i+1 < len(tokens) {
				commitOpt = strings.TrimSpace(tokens[i+1])
				i++
			}
		} else if !strings.HasPrefix(t, "-") {
			if slug == "" {
				slug = t
			} else if commitOpt == "" {
				commitOpt = t
			}
		}
	}

	if slug == "" {
		return "", "", fmt.Errorf("goal slug is required")
	}
	if commitOpt == "" {
		return "", "", fmt.Errorf("--commit=<hash> is required for goal attribution")
	}
	return slug, commitOpt, nil
}

func runGoalsAttribute(ui render.UI, targetDir string, args []string, commitFlag string, jsonOut, toonOut bool) {
	slug, commitOpt, err := extractAttributeArgs(args, commitFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\nUsage: sila goals attribute <slug> --commit=<hash>\n", err)
		os.Exit(2)
	}

	g, err := goals.AttributeGoal(targetDir, slug, commitOpt)
	if err != nil {
		if jsonOut {
			emitJSON(map[string]any{"error": err.Error(), "slug": slug, "success": false})
			return
		}
		fmt.Fprintf(os.Stderr, "%s sila goals attribute error: %v\n", ui.Red("✖"), err)
		os.Exit(1)
	}

	if jsonOut {
		emitJSON(map[string]any{
			"slug":    slug,
			"commit":  g.Commit,
			"status":  g.Status,
			"success": true,
		})
		return
	}
	if toonOut {
		fields := [][2]string{
			{"slug", slug},
			{"commit", g.Commit},
			{"status", g.Status},
			{"success", "true"},
		}
		fmt.Fprint(os.Stdout, render.FormatTOONObject("goal_attribution", fields))
		return
	}

	fmt.Fprintf(os.Stdout, "%s Goal %s successfully attributed to commit %s\n",
		ui.Green("✔"), ui.Bold(slug), ui.Cyan(g.Commit))
}

type goalsContext struct {
	ui               render.UI
	st               *store.Store
	dbPath           string
	project          string
	agent            string
	session          string
	note             string
	status           string
	reason           string
	claimImmediate   bool
	stage            bool
	commitMsg        string
	title            string
	desc             string
	dependsOn        string
	touch            string
	seq              int
	independent      bool
	isGlobal         bool
	pendingOnly      bool
	claimedOnly      bool
	readyOnly        bool
	independentOnly  bool
	blockedOnly      bool
	force            bool
	jsonOut          bool
	toonOut          bool
	showAll          bool
	ascii            bool
	implementedOnly  bool
	noSync           bool
	shape            string
	addDep           string
	removeDep        string
	dependent        bool
	args             []string
	targetDir        string
	filterMode       string
	sessionID        string
	staleRelease     bool
	olderThan        string
	pruneClaims      bool
	dryRun           bool
	checkMode        bool
	noBuild          bool
	byOpt            string
	evidenceOpt      string
	tierOpt          string
	invariantsOpt    string
	tasksOpt         string
	workPackageOpt   string
	specReviewOpt    string
	templateDirOpt   string
	reviewOpt        string
	allowOpt         string
	denyOpt          string
	durationOpt      string
	statsMode        bool
	lineMode         bool
	fullMode         bool
	liveMode         bool
	intervalOpt      string
	personasOpt      string
	addPersonasOpt   string
	clearPersonasOpt bool
	selectionFile    string
	autoRun          bool
	noMemory         bool
}

func (gc *goalsContext) handleView(tDir string, vArgs []string) {
	ui := gc.ui
	st := gc.st
	jsonOut := gc.jsonOut
	toonOut := gc.toonOut
	if len(vArgs) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: sila goal view <slug|index> [--json] [--toon]")
		os.Exit(2)
	}
	ident := vArgs[1]
	for _, a := range vArgs[2:] {
		if a == "--json" || a == "-json" {
			jsonOut = true
		} else if a == "--toon" || a == "-toon" {
			toonOut = true
		} else if ident == "" && !strings.HasPrefix(a, "-") {
			ident = a
		}
	}
	if ident == "" {
		fmt.Fprintln(os.Stderr, "Usage: sila goal view <slug|index> [--json] [--toon]")
		os.Exit(2)
	}
	if tDir == "" {
		tDir, _ = goals.ResolveProjectDir(st, "")
	}

	audit, err := goals.ScanGoals(tDir)
	if err != nil {
		if jsonOut {
			emitJSON(map[string]any{"error": err.Error()})
			return
		}
		fmt.Fprintf(os.Stderr, "sila goal view error: %v\n", err)
		os.Exit(1)
	}

	targetGoal, err := goals.ResolveGoalByIdentifier(audit, ident)
	if err != nil {
		if jsonOut {
			emitJSON(map[string]any{"error": err.Error(), "identifier": ident})
			return
		}
		fmt.Fprintf(os.Stderr, "sila goal view: %v\n", err)
		os.Exit(1)
	}

	view, err := goals.BuildGoalView(tDir, targetGoal)
	if err != nil {
		if jsonOut {
			emitJSON(map[string]any{"error": err.Error(), "slug": targetGoal.Slug})
			return
		}
		fmt.Fprintf(os.Stderr, "sila goal view error: %v\n", err)
		os.Exit(1)
	}

	if jsonOut {
		emitJSON(view)
		return
	}
	if toonOut {
		fmt.Fprintln(os.Stdout, goals.RenderGoalViewTOON(view))
		return
	}

	fmt.Fprint(os.Stdout, goals.RenderGoalViewTerminal(ui, view))
}

func (gc *goalsContext) handleFocus(tDir string, rest []string) {
	ui := gc.ui
	st := gc.st
	sessionID := gc.sessionID
	agent := gc.agent
	stage := gc.stage
	commitMsg := gc.commitMsg
	note := gc.note
	force := gc.force
	noSync := gc.noSync
	jsonOut := gc.jsonOut
	if len(rest) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: sila goals focus <slug> --session=<id> [--agent=<name>] [--note=\"...\"] [--force]")
		os.Exit(2)
	}
	if sessionID == "" {
		fmt.Fprintln(os.Stderr, "sila goals focus: --session=<id> is required to bind a session to a goal")
		fmt.Fprintln(os.Stdout, "help: sila goals focus <slug> --session=<id>")
		os.Exit(2)
	}
	slug := rest[1]
	if tDir == "" {
		tDir, _ = goals.ResolveProjectDir(st, "")
	}
	focusAgent := agent
	if focusAgent == "" {
		focusAgent = os.Getenv("SILA_AGENT")
	}
	claim, err := goals.FocusGoal(tDir, slug, focusAgent, note, sessionID, force)
	if err != nil {
		if jsonOut {
			emitJSON(map[string]any{"error": err.Error(), "slug": slug, "focused": false})
			return
		}
		fmt.Fprintf(os.Stderr, "sila goals focus error: %v\n", err)
		os.Exit(1)
	}
	if !noSync {
		if res, err := goals.ScanGoals(tDir); err == nil {
			_ = goals.SyncReadmeWithOpts(tDir, res, goals.SyncReadmeOptions{Stage: stage, CommitMsg: commitMsg})
		}
	}
	if jsonOut {
		emitJSON(map[string]any{"slug": slug, "focused": true, "claim": claim})
		return
	}
	absDir, _ := filepath.Abs(tDir)
	projName := filepath.Base(absDir)
	fmt.Fprintf(os.Stdout, "%s Goal %s in %s focused for session %s %s\n",
		ui.Green("✔"), ui.Bold(slug), ui.Magenta(ui.Bold(projName)), ui.Cyan(sessionID), ui.Dim("(agent @"+claim.ClaimedBy+")"))
}

func runGoalsValidate(ui render.UI, targetDir string, args []string, jsonOut, toonOut, strict bool, manifestPath string, strictMemoryFlag bool) {
	slug := ""
	strictMemory := strictMemoryFlag
	for i := 1; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") {
			if a == "--json" || a == "-json" {
				jsonOut = true
			} else if a == "--toon" || a == "-toon" {
				toonOut = true
			} else if a == "--strict" || a == "-strict" {
				strict = true
			} else if a == "--strict-memory" || a == "-strict-memory" {
				strictMemory = true
			} else if after, ok := strings.CutPrefix(a, "--manifest="); ok {
				manifestPath = after
			} else if after, ok := strings.CutPrefix(a, "-manifest="); ok {
				manifestPath = after
			} else if (a == "--manifest" || a == "-manifest") && i+1 < len(args) {
				manifestPath = args[i+1]
				i++
			} else if after, ok := strings.CutPrefix(a, "--project="); ok {
				targetDir = after
			} else if after, ok := strings.CutPrefix(a, "-project="); ok {
				targetDir = after
			} else if (a == "--project" || a == "-p") && i+1 < len(args) {
				targetDir = args[i+1]
				i++
			}
		} else if slug == "" {
			slug = a
		}
	}

	rep, err := goals.ValidateGoals(targetDir, goals.ValidateOptions{
		TargetDir:    targetDir,
		Slug:         slug,
		Strict:       strict,
		StrictMemory: strictMemory,
		ManifestPath: manifestPath,
	})
	if err != nil {
		if jsonOut {
			emitJSON(map[string]any{"error": err.Error(), "valid": false})
		} else {
			fmt.Fprintf(os.Stderr, "sila goals validate error: %v\n", err)
		}
		os.Exit(1)
	}

	if jsonOut {
		emitJSON(rep)
	} else {
		renderValidationReport(ui, rep)
	}

	if !rep.Valid {
		os.Exit(1)
	}
}

func renderValidationReport(ui render.UI, rep *goals.ValidationReport) {
	fmt.Fprintf(os.Stdout, "%s %s — checked %d goal(s) (%s, %s)\n",
		ui.Cyan("sila · goals validate"),
		ui.Dim(rep.Summary),
		rep.CheckedGoals,
		ui.Red(fmt.Sprintf("%d error(s)", rep.ErrorsCount)),
		ui.Yellow(fmt.Sprintf("%d warning(s)", rep.WarningsCount)),
	)
	if len(rep.Diagnostics) > 0 {
		maxShow := 50
		shown := rep.Diagnostics
		if len(shown) > maxShow {
			shown = shown[:maxShow]
		}
		for _, d := range shown {
			icon := ui.Red("❌")
			if d.Severity == "warning" {
				icon = ui.Yellow("⚠️")
			}
			pathStr := d.Path
			if pathStr == "" && d.GoalSlug != "" {
				pathStr = filepath.Join("goals", d.GoalSlug)
			}
			fmt.Fprintf(os.Stdout, "  %s [%s] %s: %s\n", icon, ui.Bold(d.Code), ui.Cyan(pathStr), d.Message)
			if d.Remediation != "" {
				fmt.Fprintf(os.Stdout, "     %s %s\n", ui.Dim("↳ Hint:"), ui.Dim(d.Remediation))
			}
		}
		if len(rep.Diagnostics) > maxShow {
			fmt.Fprintf(os.Stdout, "  %s\n", ui.Dim(fmt.Sprintf("... and %d more diagnostic(s) omitted", len(rep.Diagnostics)-maxShow)))
		}
	}
	if rep.Valid {
		fmt.Fprintf(os.Stdout, "%s %s\n", ui.Green("✔"), ui.Bold("All goal package checks passed"))
	} else {
		fmt.Fprintf(os.Stdout, "%s %s\n", ui.Red("✘"), ui.Bold(fmt.Sprintf("Goal validation failed (%d error(s), %d warning(s))", rep.ErrorsCount, rep.WarningsCount)))
	}
}

// annotateContract adds the active claim's read-only contract status to the
// goals JSON payload. It never writes a receipt or mutates goal state.
func (gc *goalsContext) annotateContract(res *goals.AuditResult, targetDir string) {
	if gc.st == nil || res == nil {
		return
	}
	var slug, session, harness string
	for i := range res.Goals {
		g := &res.Goals[i]
		if g.Superseded || g.Claim == nil || g.Claim.Stale {
			continue
		}
		slug, session, harness = g.Slug, g.Claim.SessionID, g.Claim.Agent
		break
	}
	if slug == "" {
		return
	}
	if session == "" {
		session = "unbound"
	}
	policy := contract.ResolvePolicy(targetDir)
	if policy.Policy == contract.PolicyOff {
		return
	}
	sig := contract.NormalizeSignal(contract.Signal{
		Harness: contract.HarnessID(harness), SessionID: session,
		ProjectID: store.ProjectID(targetDir), Cwd: targetDir,
		Phase: contract.PhaseSessionEnd, SilaGoalSlug: slug,
	})
	d := contract.NewChecker(gc.st, targetDir).Evaluate(sig, policy)
	res.ContractStatus = d.Decision
	res.ContractReason = contract.Summary(d)
}
