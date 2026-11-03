package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jadmadi/sila/internal/agents"
	"github.com/jadmadi/sila/internal/assess"
	"github.com/jadmadi/sila/internal/heal"
	"github.com/jadmadi/sila/internal/onboard"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

// runOnboard provisions and verifies sila participation for a repository.
func runOnboard(ui render.UI, dbPath, project string, convertTasks bool, from string, review, yes, dryRun, strict, global, jsonOut bool, reasonFlag, agent string, args []string) {
	if len(args) > 0 && args[0] == "check" {
		runOnboardCheck(ui, dbPath, project, strict, global, jsonOut)
		return
	}
	if len(args) > 0 && args[0] == "defer" {
		runOnboardDefer(ui, dbPath, project, reasonFlag, agent, jsonOut, args[1:])
		return
	}
	if convertTasks {
		runOnboardConvert(ui, project, from, review, yes, jsonOut)
		return
	}

	cwd, _ := os.Getwd()
	if project == "" {
		project = store.ProjectID(cwd)
	}

	type provisionResult struct {
		RepoRoot       string                `json:"repo_root"`
		Project        string                `json:"project"`
		DryRun         bool                  `json:"dry_run,omitempty"`
		Inventory      *onboard.Report       `json:"inventory"`
		RulesUpdated   []string              `json:"rules_updated,omitempty"`
		Hooks          []string              `json:"hooks_installed,omitempty"`
		MCPConfigs     []string              `json:"mcp_configs,omitempty"`
		OpenCodePlugin string                `json:"opencode_plugin,omitempty"`
		GoalPackage    string                `json:"goal_package,omitempty"`
		Notes          []string              `json:"notes,omitempty"`
		Errors         []string              `json:"errors,omitempty"`
		Plan           []agents.PlannedWrite `json:"plan,omitempty"`
	}
	res := &provisionResult{RepoRoot: cwd, Project: project, Errors: []string{}}

	report, err := onboard.Probe(cwd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila onboard: inventory: %v\n", err)
		os.Exit(1)
	}
	res.Inventory = report

	opts := agents.CompileOptions{RepoRoot: cwd, ProjectName: project, Preview: dryRun}
	deployTools := []string{"all"}

	if dryRun {
		if plan, err := agents.DeployTargets(cwd, opts, deployTools); err == nil {
			res.Plan = append(res.Plan, plan.Plan...)
		} else {
			res.Errors = append(res.Errors, fmt.Sprintf("rules preview: %v", err))
		}
		res.Plan = append(res.Plan, agents.PlannedWrite{Action: "write", Path: "goals/sila-onboarding", Detail: "scaffold the completion goal when absent"})
		if _, err := os.Stat(filepath.Join(cwd, ".git")); err == nil {
			res.Plan = append(res.Plan, agents.PlannedWrite{Action: "write", Path: ".git/hooks/pre-commit", Detail: "install the sila pre-commit gate"})
		}
		if jsonOut {
			emitJSON(map[string]any{"dry_run": true, "repo_root": cwd, "project": project, "inventory": report, "plan": res.Plan, "errors": res.Errors})
			return
		}
		fmt.Fprint(os.Stdout, render.RenderHeader(ui, render.Header{Command: "onboard", Scope: render.DisplayPath(cwd, cwd), Mode: "dry-run"}))
		for _, p := range res.Plan {
			fmt.Fprintf(os.Stdout, "  • %s %s\n", p.Path, ui.Dim(p.Detail))
		}
		printOnboardInventory(ui, report)
		return
	}

	provisioned := provisionOnboardProject(cwd, project)
	res.RulesUpdated = provisioned.RulesUpdated
	res.Hooks = provisioned.Hooks
	res.MCPConfigs = provisioned.MCPConfigs
	res.OpenCodePlugin = provisioned.OpenCodePlugin
	res.GoalPackage = provisioned.GoalPackage
	res.Notes = provisioned.Notes
	res.Errors = append(res.Errors, provisioned.Errors...)

	if jsonOut {
		emitJSON(res)
		if len(res.Errors) > 0 {
			os.Exit(1)
		}
		return
	}

	fmt.Fprint(os.Stdout, render.RenderHeader(ui, render.Header{Command: "onboard", Scope: cardProjectLabel(nil, project), Title: render.DisplayPath(cwd, cwd)}))
	fmt.Fprintln(os.Stdout)
	printOnboardInventory(ui, report)
	fmt.Fprintln(os.Stdout)
	fmt.Fprintln(os.Stdout, ui.Bold("Wiring:"))
	fmt.Fprintf(os.Stdout, "  %s rules: %s\n", ui.Green("✔"), ui.Dim(firstFewOr(res.RulesUpdated, "current")))
	if len(res.Hooks) > 0 {
		fmt.Fprintf(os.Stdout, "  %s git hook: %s\n", ui.Green("✔"), res.Hooks[0])
	}
	if len(res.MCPConfigs) > 0 {
		fmt.Fprintf(os.Stdout, "  %s mcp: %s\n", ui.Green("✔"), firstFewOr(res.MCPConfigs, ""))
	}
	if res.OpenCodePlugin != "" {
		fmt.Fprintf(os.Stdout, "  %s opencode plugin: %s\n", ui.Green("✔"), res.OpenCodePlugin)
	}
	if res.GoalPackage != "" {
		fmt.Fprintf(os.Stdout, "  %s completion goal: %s\n", ui.Green("✔"), res.GoalPackage)
	} else {
		fmt.Fprintf(os.Stdout, "  %s completion goal: already present\n", ui.Dim("•"))
	}
	for _, n := range res.Notes {
		fmt.Fprintf(os.Stdout, "  %s %s\n", ui.Dim("•"), ui.Dim(n))
	}
	for _, e := range res.Errors {
		fmt.Fprintf(os.Stdout, "  %s %s\n", ui.Red("✖"), e)
	}
	fmt.Fprintln(os.Stdout)
	if !outModes.quiet {
		fmt.Fprintf(os.Stdout, "Next: %s\n", ui.Cyan("sila onboard check --strict"))
	}
}

func printOnboardInventory(ui render.UI, report *onboard.Report) {
	if report == nil {
		return
	}
	fmt.Fprintln(os.Stdout, ui.Bold("Inventory:")+ui.Dim(fmt.Sprintf(" %d surfaces (%d unmanaged)", len(report.Surfaces), report.Unmanaged)))
	if len(report.Surfaces) == 0 {
		fmt.Fprintln(os.Stdout, ui.Dim("  no legacy task surfaces found"))
		return
	}
	rows := make([][]string, 0, len(report.Surfaces))
	for _, s := range report.Surfaces {
		rows = append(rows, []string{s.Kind, s.Path, s.State, s.Detail})
	}
	fmt.Fprintln(os.Stdout, ui.Table([]string{"kind", "path", "state", "detail"}, rows, []string{"l", "l", "l", "l"}))
}

func firstFewOr(items []string, fallback string) string {
	if len(items) == 0 {
		return fallback
	}
	max := 3
	if len(items) < max {
		max = len(items)
	}
	out := items[:max]
	if len(items) > max {
		return strings.Join(out, ", ") + fmt.Sprintf(" +%d more", len(items)-max)
	}
	return strings.Join(out, ", ")
}

// runOnboardCheck verifies one repository, or every registered project with --global.
func runOnboardCheck(ui render.UI, dbPath, project string, strict, global, jsonOut bool) {
	st, err := store.OpenRO(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()

	cwd, _ := os.Getwd()
	if !global {
		rep := onboard.Check(st, cwd, project, strict)
		if jsonOut {
			emitOnboardJSON(onboardJSONData(rep, outModes.advisory), rep.Ready, outModes.advisory)
		} else if outModes.porcelain {
			for _, item := range rep.Items {
				critical := "0"
				if item.Critical {
					critical = "1"
				}
				fix := strings.NewReplacer("\t", " ", "\n", " ").Replace(item.Fix)
				fmt.Fprintf(os.Stdout, "%s\t%s\t%s\t%s\n", item.Name, item.Status, critical, fix)
			}
		} else {
			printOnboardCheck(ui, rep)
		}
		if code := onboardExitCode(rep.Ready, outModes.advisory); code != 0 {
			os.Exit(code)
		}
		return
	}

	projects, err := st.Projects()
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila: %v\n", err)
		os.Exit(1)
	}
	type projectResult struct {
		Project string               `json:"project"`
		Report  *onboard.CheckReport `json:"report"`
	}
	var results []projectResult
	notReady := false
	seenPaths := make(map[string]bool)
	for _, p := range projects {
		if p.Path == "" || p.ID == "no-project" || !store.IsRepoOrWorkspaceDir(p.Path) {
			continue
		}
		canon := store.CanonicalProjectPath(p.Path)
		if !store.IsRepoOrWorkspaceDir(canon) || seenPaths[canon] {
			continue
		}
		seenPaths[canon] = true
		rep := onboard.Check(st, canon, p.ID, strict)
		if !rep.Ready {
			notReady = true
		}
		results = append(results, projectResult{Project: p.ID, Report: rep})
	}
	sort.SliceStable(results, func(i, j int) bool { return results[i].Project < results[j].Project })

	if jsonOut {
		emitOnboardJSON(map[string]any{
			"global": true, "count": len(results), "projects": results, "advisory": outModes.advisory,
		}, !notReady, outModes.advisory)
		if code := onboardExitCode(!notReady, outModes.advisory); code != 0 {
			os.Exit(code)
		}
		return
	}

	if outModes.porcelain {
		for _, r := range results {
			ready := "0"
			if r.Report.Ready {
				ready = "1"
			}
			fmt.Fprintf(os.Stdout, "%s\t%s\t%d\t%d\n", r.Project, ready, r.Report.Warn, r.Report.Fail)
		}
		if code := onboardExitCode(!notReady, outModes.advisory); code != 0 {
			os.Exit(code)
		}
		return
	}

	rows := make([][]string, 0, len(results))
	for _, r := range results {
		status := ui.Green("ready")
		if !r.Report.Ready {
			status = ui.Red("blocked")
		} else if r.Report.Warn > 0 {
			status = ui.Yellow("warn")
		}
		topFix := ""
		for _, item := range r.Report.Items {
			if item.Status != "pass" && item.Fix != "" {
				topFix = item.Fix
				break
			}
		}
		rows = append(rows, []string{r.Project, status, fmt.Sprintf("%d warn", r.Report.Warn), topFix})
	}
	fmt.Fprint(os.Stdout, render.RenderHeader(ui, render.Header{Command: "onboard check", Scope: fmt.Sprintf("global (%d projects)", len(results))}))
	fmt.Fprintln(os.Stdout)
	fmt.Fprintln(os.Stdout, ui.Table([]string{"project", "status", "warnings", "next fix"}, rows, []string{"l", "l", "r", "l"}))
	if notReady && outModes.advisory {
		fmt.Fprintf(os.Stdout, "\n%s advisory: failures reported without a non-zero exit\n", ui.Yellow("▲"))
	}
	if code := onboardExitCode(!notReady, outModes.advisory); code != 0 {
		os.Exit(code)
	}
}

func printOnboardCheck(ui render.UI, rep *onboard.CheckReport) {
	fmt.Fprint(os.Stdout, render.RenderHeader(ui, render.Header{Command: "onboard check", Scope: onboardScope(rep)}))
	fmt.Fprintln(os.Stdout)
	rows := make([][]string, 0, len(rep.Items))
	hasCritical := false
	for _, item := range rep.Items {
		if item.Critical {
			hasCritical = true
		}
		status := ui.Green("pass")
		switch item.Status {
		case "warn":
			status = ui.Yellow("warn")
		case "fail":
			status = ui.Red("fail")
		}
		if item.Critical {
			status += ui.Dim(" *")
		}
		rows = append(rows, []string{item.Name, status, item.Detail, item.Fix})
	}
	fmt.Fprintln(os.Stdout, ui.Table([]string{"item", "status", "evidence", "fix"}, rows, []string{"l", "l", "l", "l"}))
	if hasCritical {
		fmt.Fprintln(os.Stdout, ui.Dim("* critical - must be resolved before onboarding is complete"))
	}
	fmt.Fprintln(os.Stdout)
	if rep.Ready {
		fmt.Fprintf(os.Stdout, "%s Onboarding complete (%d pass, %d warn).\n", ui.Green("✔"), rep.Pass, rep.Warn)
	} else {
		fmt.Fprintf(os.Stdout, "%s Onboarding incomplete: %d critical %s.\n", ui.Red("✖"), rep.Fail, plural(int64(rep.Fail), "failure"))
		if outModes.advisory {
			fmt.Fprintf(os.Stdout, "%s advisory: failures reported without a non-zero exit\n", ui.Yellow("▲"))
		}
	}
}

// onboardExitCode is the single exit contract for onboard check: a critical
// failure exits 1 unless advisory mode was explicitly requested.
func onboardExitCode(ready, advisory bool) int {
	if ready || advisory {
		return 0
	}
	return 1
}

// onboardScope never renders an empty header scope.
func onboardScope(rep *onboard.CheckReport) string {
	if rep == nil {
		return "unregistered project"
	}
	if scope := strings.TrimSpace(rep.Project); scope != "" {
		return scope
	}
	if root := strings.TrimSpace(rep.RepoRoot); root != "" {
		return filepath.Base(root)
	}
	if cwd, err := os.Getwd(); err == nil && strings.TrimSpace(cwd) != "" {
		return filepath.Base(cwd)
	}
	return "unregistered project"
}

func onboardJSONData(rep *onboard.CheckReport, advisory bool) map[string]any {
	return map[string]any{
		"project":   rep.Project,
		"repo_root": rep.RepoRoot,
		"ready":     rep.Ready,
		"pass":      rep.Pass,
		"warn":      rep.Warn,
		"fail":      rep.Fail,
		"strict":    rep.Strict,
		"advisory":  advisory,
		"items":     rep.Items,
	}
}

// emitOnboardJSON keeps the report under data and uses the shared envelope.
// A non-advisory failure is an error envelope with a stable code.
func emitOnboardJSON(data any, ready, advisory bool) {
	env := render.Envelope{
		Schema:    render.JSONSchemaV1,
		OK:        ready || advisory,
		Data:      render.NormalizeSlices(data),
		Warnings:  []string{},
		Mutations: []string{},
	}
	if !ready {
		env.OK = false
		env.Error = &render.JSONError{
			Code:    "ONBOARDING_INCOMPLETE",
			Message: "critical onboarding items are unresolved",
			Fix:     "sila heal",
		}
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(env); err != nil {
		fmt.Fprintf(os.Stderr, "sila: %v\n", err)
		os.Exit(1)
	}
}

// runOnboardConvert turns a markdown task list into a reviewed backlog goal.
func runOnboardConvert(ui render.UI, project, from string, review, yes, jsonOut bool) {
	cwd, _ := os.Getwd()
	if from == "" {
		fmt.Fprintln(os.Stderr, "sila onboard --convert-tasks: specify --from=<markdown file>")
		os.Exit(2)
	}
	if project == "" {
		project = store.ProjectID(cwd)
	}
	proposal, err := onboard.ProposeTaskConversion(cwd, from)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila onboard --convert-tasks: %v\n", err)
		os.Exit(1)
	}
	if review && !yes {
		if jsonOut {
			emitJSON(map[string]any{"review": true, "written": false, "proposal": proposal})
			return
		}
		fmt.Fprintf(os.Stdout, "%s %s would become goals/%s with %d tasks:\n", ui.Yellow("▲"), proposal.Source, proposal.Slug, len(proposal.Tasks))
		for _, t := range proposal.Tasks {
			fmt.Fprintf(os.Stdout, "   - [ ] %s\n", t)
		}
		fmt.Fprintln(os.Stdout, ui.Dim("Re-run with --yes to write the package. The source file is never modified."))
		return
	}
	dir, err := onboard.WriteConvertedGoal(cwd, proposal)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila onboard --convert-tasks: %v\n", err)
		os.Exit(1)
	}
	if jsonOut {
		emitJSON(map[string]any{"written": true, "path": dir, "proposal": proposal})
		return
	}
	fmt.Fprintf(os.Stdout, "%s Converted %d tasks from %s into %s\n", ui.Green("✔"), len(proposal.Tasks), proposal.Source, dir)
	fmt.Fprintf(os.Stdout, "  %s The source file %s was not modified.\n", ui.Dim("→"), proposal.Source)
}

// onboardProvisionResult aliases onboard.ProvisionResult for CLI output.
type onboardProvisionResult = onboard.ProvisionResult

// provisionOnboardProject is the shared execution path for `sila onboard` and
// `sila heal --apply`: managed rules, the git hook, MCP configs, the OpenCode
// plugin, the project-local completion goal, and Cloudflare filters.
func provisionOnboardProject(cwd, project string) onboardProvisionResult {
	res := onboard.Provision(cwd, project)
	if assess.IsCFWorkerProject(cwd) || assess.IsCloudflareWorkerProject(cwd) {
		if configured, err := heal.EnsureCFGitFilter(cwd); err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("cfclean: %v", err))
		} else if configured {
			res.Notes = append(res.Notes, "configured cfclean credential sanitization filter")
		}
	}
	return res
}

// runOnboardDefer records an explicit deferral of a legacy task surface.
func runOnboardDefer(ui render.UI, dbPath, project, reasonFlag, agent string, jsonOut bool, args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "sila onboard defer: specify surface (e.g. TODO.md, TASKS.md, .brain, tasks, legacy)")
		os.Exit(1)
	}
	surface := args[0]
	reason := strings.TrimSpace(reasonFlag)
	if reason == "" && len(args) > 1 {
		reason = strings.TrimSpace(strings.Join(args[1:], " "))
	}
	if reason == "" {
		reason = "deferred legacy task surface"
	}
	if agent == "" {
		agent = "agent"
	}

	cwd, _ := os.Getwd()
	if project == "" {
		project = store.ProjectID(cwd)
	}

	var st *store.Store
	if dbPath == "" {
		dbPath = store.DefaultDBPath()
	}
	if openSt, err := store.Open(dbPath); err == nil {
		st = openSt
		defer st.Close()
	}

	rec, err := onboard.DeferSurface(st, cwd, project, surface, reason, agent)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila onboard defer: %v\n", err)
		os.Exit(1)
	}

	if jsonOut {
		emitJSON(rec)
		return
	}

	fmt.Fprintf(os.Stdout, "%s %s (%s)\n", ui.Green("✔"), ui.Bold("Deferred legacy task surface "+rec.Surface), ui.Dim(rec.Reason))
	fmt.Fprintf(os.Stdout, "  Recorded architectural deferral memo in knowledge store\n")
	fmt.Fprintf(os.Stdout, "  Updated .sila/deferrals.json\n")
	fmt.Fprintf(os.Stdout, "Next: %s\n", ui.Cyan("sila onboard check"))
}
