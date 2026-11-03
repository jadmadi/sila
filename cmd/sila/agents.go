package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/agents"
	"github.com/jadmadi/sila/internal/assist"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func runAgents(ui render.UI, dbPath, project, target, outFile, policyPack, scope string, preview, replace, principles bool, stack string, jsonOut bool, args []string) {
	cwd, _ := os.Getwd()
	if project == "" {
		project = store.ProjectID(cwd)
	}

	opts := agents.CompileOptions{
		RepoRoot:          cwd,
		ProjectName:       project,
		Preview:           preview,
		Replace:           replace,
		PolicyPack:        policyPack,
		IncludePrinciples: principles,
		Stack:             stack,
	}

	force, changedOnly := false, false
	for _, arg := range args {
		switch arg {
		case "--force", "-f":
			force = true
		case "--changed-only":
			changedOnly = true
		}
	}
	if force && changedOnly {
		fmt.Fprintln(os.Stderr, "sila agents sync: --force and --changed-only are mutually exclusive; use --force to repair or omit both for changed-only")
		os.Exit(2)
	}
	opts.Force = force
	opts.ChangedOnly = !force

	allProjects := false

	// Parse sub-flags from args
	for _, arg := range args {
		switch {
		case arg == "--all" || arg == "-all" || arg == "-a" || arg == "--global" || arg == "-global" || arg == "-g":
			allProjects = true
		case arg == "--principles" || arg == "-principles":
			opts.IncludePrinciples = true
		case strings.HasPrefix(arg, "--stack="):
			opts.Stack = strings.TrimPrefix(arg, "--stack=")
		case strings.HasPrefix(arg, "--tech-stack="):
			opts.Stack = strings.TrimPrefix(arg, "--tech-stack=")
		case arg == "--dry-run" || arg == "-n":
			opts.Preview = true
		case arg == "--replace":
			opts.Replace = true
		case arg == "--json":
			jsonOut = true
		}
	}

	sub := "check"
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			sub = strings.ToLower(arg)
			break
		}
	}

	switch sub {
	case "check":
		if allProjects {
			runAgentsCheckAll(ui, dbPath, opts, jsonOut)
		} else {
			runAgentsCheck(ui, cwd, opts, jsonOut)
		}
	case "sync":
		if allProjects {
			runAgentsSyncAll(ui, dbPath, opts, jsonOut)
		} else {
			runAgentsSync(ui, cwd, opts, jsonOut, dbPath)
		}
	case "status", "last":
		runAgentsStatus(ui, dbPath, project, allProjects, jsonOut)
	case "compile":
		runAgentsCompile(cwd, opts, outFile, jsonOut)
	case "list":
		runAgentsList(ui, cwd, jsonOut)
	case "review":
		if opts.IncludePrinciples || (len(args) > 1 && (args[1] == "principles" || args[1] == "--principles")) {
			opts.IncludePrinciples = true
			opts.Preview = true
			runAgentsPrinciplesReview(ui, cwd, opts, jsonOut)
			return
		}
		reviewTarget := target
		if len(args) > 1 {
			reviewTarget = args[1]
		}
		runAgentsReview(ui, dbPath, cwd, project, reviewTarget, scope, jsonOut)
	default:
		fmt.Fprintf(os.Stderr, "sila agents: unknown subcommand %q (expected check, sync, status, compile, list, review)\n", sub)
		os.Exit(2)
	}
}

func runAgentsCheck(ui render.UI, repoRoot string, opts agents.CompileOptions, jsonOut bool) {
	report, err := agents.DetectDrift(repoRoot, opts)
	if err != nil {
		if jsonOut {
			emitJSON(map[string]any{"error": err.Error()})
		} else {
			fmt.Fprintf(os.Stderr, "sila agents check: %v\n", err)
		}
		os.Exit(1)
	}

	if jsonOut {
		emitJSON(report)
		if report.HasDrift {
			os.Exit(1)
		}
		return
	}

	fmt.Fprint(os.Stdout, render.RenderHeader(ui, render.Header{Command: "agents check", Scope: render.DisplayPath("", repoRoot)}))
	hashState := "mismatch"
	if report.HashMatch {
		hashState = "match"
	} else if report.LegacyBlock {
		hashState = "legacy"
	}
	principlesState := "absent"
	if report.PrinciplesPresent {
		principlesState = "match"
		if !report.PrinciplesHashMatch {
			principlesState = "mismatch"
		}
	}
	fmt.Fprintf(os.Stdout, "  managed rev=%d hash=%s hub=%s\n", report.ManagedRevision, report.ManagedHash, report.HubManagedHash)
	fmt.Fprintf(os.Stdout, "  hash: %s\n", hashState)
	fmt.Fprintf(os.Stdout, "  principles: %s\n", principlesState)
	for _, target := range report.TargetStatuses {
		state := "absent"
		if target.Present {
			state = "present"
			if target.Managed {
				state = "managed"
			}
			if target.HashMatch {
				state += " (hash match)"
			}
		}
		fmt.Fprintf(os.Stdout, "  target %s %s: %s\n", target.Kind, target.Path, state)
	}
	if !report.HasDrift {
		fmt.Fprintf(os.Stdout, "  %s %s (file hash: %s)\n", ui.Green("✔"), ui.Bold("PASS — rules in sync with Core Hub"), report.CurrentHash)
		return
	}

	fmt.Fprintf(os.Stdout, "  %s %s (%s)\n", ui.Red("✖"), ui.Bold("DRIFT DETECTED"), report.DriftKind)
	for _, d := range report.Details {
		fmt.Fprintf(os.Stdout, "    %s %s\n", ui.Yellow("▲"), ui.Dim(d))
	}
	for _, m := range report.MissingFiles {
		fmt.Fprintf(os.Stdout, "    %s missing file: %s\n", ui.Red("✖"), ui.Bold(m))
	}
	fmt.Fprintf(os.Stdout, "\n  %s run %s to reconcile.\n", ui.Dim("tip:"), ui.Cyan("sila agents sync"))
	os.Exit(1)
}

func runAgentsSync(ui render.UI, repoRoot string, opts agents.CompileOptions, jsonOut bool, dbPathArg ...string) {
	res, err := agents.DeployTargets(repoRoot, opts, []string{"all"})
	if err != nil {
		if jsonOut {
			emitJSON(map[string]any{"error": err.Error()})
		} else {
			fmt.Fprintf(os.Stderr, "sila agents sync: %v\n", err)
		}
		os.Exit(1)
	}

	if !res.Preview {
		effDB := ""
		if len(dbPathArg) > 0 {
			effDB = dbPathArg[0]
		}
		if effDB == "" {
			effDB = store.DefaultDBPath()
		}
		if st, err := store.Open(effDB); err == nil {
			defer st.Close()
			agentsDir := agents.FindAgentsDir(repoRoot)
			hubCommit := agents.ResolveHubCommit(agentsDir)
			rec := agents.NewSyncReceiptFromSingle(res, opts.ProjectName, hubCommit, agents.ResolveInitiator())
			_ = agents.SaveSyncReceipt(st, rec)
		}
	}

	if jsonOut {
		emitJSON(res)
		if res.Failed > 0 {
			os.Exit(1)
		}
		return
	}

	if res.Preview {
		fmt.Fprint(os.Stdout, render.RenderHeader(ui, render.Header{Command: "agents sync", Scope: render.DisplayPath("", repoRoot), Mode: "dry-run"}))
		for _, p := range res.Plan {
			detail := p.Detail
			if detail != "" {
				detail = " — " + detail
			}
			fmt.Fprintf(os.Stdout, "  • %s %s%s (%d → %d bytes)\n", p.Action, p.Path, detail, p.BeforeBytes, p.AfterBytes)
		}
		fmt.Fprintf(os.Stdout, "\n  Summary: %d written, %d skipped, %d failed.\n", res.Written, res.Skipped, res.Failed)
		fmt.Fprintln(os.Stdout, "  "+ui.Dim("nothing was written; re-run without --dry-run to apply"))
		return
	}

	fmt.Fprint(os.Stdout, render.RenderHeader(ui, render.Header{Command: "agents sync", Scope: render.DisplayPath("", repoRoot)}))
	for _, p := range res.Plan {
		if p.Action == "skip" {
			reason := p.SkipReason
			if reason == "" {
				reason = p.Detail
			}
			fmt.Fprintf(os.Stdout, "  %s skip %s — %s\n", ui.Dim("•"), ui.Bold(p.Path), ui.Dim(reason))
		}
	}
	for _, f := range res.FilesWritten {
		fmt.Fprintf(os.Stdout, "  %s wrote %s\n", ui.Green("✔"), ui.Bold(f))
	}
	for _, s := range res.Symlinks {
		fmt.Fprintf(os.Stdout, "  %s created link %s\n", ui.Green("✔"), ui.Bold(s))
	}
	for _, e := range res.Errors {
		fmt.Fprintf(os.Stderr, "  %s %s\n", ui.Red("✖"), e)
	}
	fmt.Fprintf(os.Stdout, "\n  Summary: %d written, %d skipped, %d failed.\n", res.Written, res.Skipped, res.Failed)
	if res.Failed > 0 {
		os.Exit(1)
	}
	if res.Written == 0 {
		fmt.Fprintf(os.Stdout, "  %s all target rule projections are already up to date.\n", ui.Green("✔"))
		return
	}
	fmt.Fprintf(os.Stdout, "  %s all target rule projections updated successfully.\n", ui.Green("✔"))
}

func runAgentsCompile(repoRoot string, opts agents.CompileOptions, outFile string, jsonOut bool) {
	compiler := agents.NewCompiler(repoRoot)
	content, err := compiler.Compile(opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila agents compile: %v\n", err)
		os.Exit(1)
	}

	if opts.IncludePrinciples {
		pack := agents.DefaultPrinciplesPack(opts.Stack)
		content = content + "\n\n" + pack.RenderDelimited() + "\n"
	}

	if outFile != "" {
		if err := os.WriteFile(outFile, []byte(content), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "sila agents compile: write %s: %v\n", outFile, err)
			os.Exit(1)
		}
		fmt.Printf("✔ compiled rules written to %s\n", outFile)
		return
	}

	if jsonOut {
		emitJSON(map[string]string{"content": content})
	} else {
		fmt.Print(content)
	}
}

func runAgentsPrinciplesReview(ui render.UI, repoRoot string, opts agents.CompileOptions, jsonOut bool) {
	opts.Preview = true
	res, err := agents.DeployTargets(repoRoot, opts, []string{"all"})
	if err != nil {
		if jsonOut {
			emitJSON(map[string]any{"error": err.Error()})
		} else {
			fmt.Fprintf(os.Stderr, "sila agents review: %v\n", err)
		}
		os.Exit(1)
	}

	if jsonOut {
		emitJSON(res)
		return
	}

	fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · agents review (principles)"))+fmt.Sprintf(" (%s)", repoRoot))
	hasChanges := false
	for _, p := range res.Plan {
		if p.Action == "skip" {
			continue
		}
		hasChanges = true
		detail := p.Detail
		if detail != "" {
			detail = " — " + detail
		}
		fmt.Fprintf(os.Stdout, "  • %s %s%s (%d → %d bytes)\n", p.Action, ui.Bold(p.Path), detail, p.BeforeBytes, p.AfterBytes)
		if p.Diff != "" {
			for line := range strings.SplitSeq(p.Diff, "\n") {
				if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
					fmt.Fprintf(os.Stdout, "    %s\n", ui.Green(line))
				} else if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
					fmt.Fprintf(os.Stdout, "    %s\n", ui.Red(line))
				} else if strings.HasPrefix(line, "@@") {
					fmt.Fprintf(os.Stdout, "    %s\n", ui.Cyan(line))
				} else if line != "" {
					fmt.Fprintf(os.Stdout, "    %s\n", ui.Dim(line))
				}
			}
			fmt.Fprintln(os.Stdout, "")
		}
	}
	if !hasChanges {
		fmt.Fprintf(os.Stdout, "  %s %s\n", ui.Green("✔"), ui.Bold("All target rule projections already have the latest principles pack."))
	}
	fmt.Fprintln(os.Stdout, "  "+ui.Dim("Preview only: zero files modified. Run 'sila agents sync --principles' to apply."))
}

func runAgentsList(ui render.UI, repoRoot string, jsonOut bool) {
	compiler := agents.NewCompiler(repoRoot)
	domains := compiler.ListDomains()

	if jsonOut {
		emitJSON(map[string]any{"domains": domains})
		return
	}

	fmt.Fprint(os.Stdout, render.RenderHeader(ui, render.Header{Command: "agents list", Title: "available domain standards"}))
	if len(domains) == 0 {
		fmt.Fprintln(os.Stdout, "  (no domain standards found in agents/ directory)")
		return
	}
	for _, d := range domains {
		fmt.Fprintf(os.Stdout, "  • %s (%s)\n", ui.Bold(d), ui.Dim("AGENTS."+d+".md"))
	}
}

func runAgentsReview(ui render.UI, dbPath, repoRoot, project, targetPath, scope string, jsonOut bool) {
	effScope := strings.ToLower(strings.TrimSpace(scope))
	if effScope != "global" {
		effScope = "project"
	}

	switch strings.ToLower(strings.TrimSpace(targetPath)) {
	case "claude":
		targetPath = "CLAUDE.md"
	case "cursor":
		targetPath = ".cursorrules"
	case "windsurf":
		targetPath = ".windsurfrules"
	case "agents", "":
		targetPath = "AGENTS.md"
	}
	fullTarget := targetPath
	if !filepath.IsAbs(fullTarget) {
		fullTarget = filepath.Join(repoRoot, targetPath)
	}

	var existingRules string
	if data, err := os.ReadFile(fullTarget); err == nil {
		existingRules = string(data)
	}

	st, err := store.OpenRO(dbPath)
	if err != nil {
		if jsonOut {
			emitJSON(map[string]any{"proposals": []any{}, "error": err.Error()})
		} else {
			fmt.Fprintf(os.Stderr, "sila agents review: %v\n", err)
		}
		os.Exit(1)
	}
	defer st.Close()

	if project == "" {
		project = store.ProjectID(repoRoot)
	}

	fetchProject := project
	if effScope == "global" {
		fetchProject = ""
	}

	provider := assist.ActiveProviderFor("rules", fetchProject)
	if provider == nil {
		provider = assist.ActiveProviderFor("review", fetchProject)
	}

	if provider == nil {
		if jsonOut {
			emitJSON(map[string]any{
				"proposals": []any{},
				"notice":    "Assistance provider not configured or scope not consented; no proposals generated.",
				"target":    targetPath,
			})
			return
		}
		fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · agents review"))+fmt.Sprintf(" (%s)", targetPath))
		fmt.Fprintln(os.Stdout, "  "+ui.Dim("No assistance provider configured or scope not consented."))
		fmt.Fprintln(os.Stdout, "  "+ui.Dim("Run 'sila config set assist_enabled true' and configure an approved provider to preview proposals."))
		fmt.Fprintln(os.Stdout, "  "+ui.Dim("Preview only: zero files modified."))
		return
	}

	// Fetch recent messages for context
	msgs, err := st.RecentMessages(fetchProject, 30)
	if err != nil {
		msgs = nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	proposals := assist.ProposeInstructionUpdatesWithOptions(ctx, provider, existingRules, targetPath, msgs, assist.InstructionReviewOptions{Scope: effScope})

	if jsonOut {
		emitJSON(map[string]any{
			"proposals": proposals,
			"target":    targetPath,
			"count":     len(proposals),
		})
		return
	}

	fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · agents review"))+fmt.Sprintf(" (%s)", targetPath))
	if len(proposals) == 0 {
		fmt.Fprintf(os.Stdout, "  %s %s\n", ui.Green("✔"), ui.Bold("No instruction updates proposed; existing rules are sufficient."))
		fmt.Fprintln(os.Stdout, "  "+ui.Dim("Preview only: zero files modified."))
		return
	}

	for _, p := range proposals {
		actionColor := ui.Yellow(string(p.Action))
		if p.Action == assist.ActionAdd {
			actionColor = ui.Green(string(p.Action))
		}
		hashShort := p.TargetContentHash
		if len(hashShort) > 12 {
			hashShort = hashShort[:12]
		}
		fmt.Fprintf(os.Stdout, "  • %s [%s] target: %s (base: %s)\n", actionColor, p.Scope, ui.Bold(p.TargetFile), ui.Dim(hashShort))
		fmt.Fprintf(os.Stdout, "    %s %s\n", ui.Dim("summary:"), p.FailureSummary)
		fmt.Fprintf(os.Stdout, "    %s %s\n", ui.Dim("prevents:"), p.PreventedRecurrence)
		if len(p.EvidenceSources) > 0 {
			fmt.Fprintf(os.Stdout, "    %s %s\n", ui.Dim("evidence:"), strings.Join(p.EvidenceSources, ", "))
		}
		if p.ExceptionNotes != "" {
			fmt.Fprintf(os.Stdout, "    %s %s\n", ui.Dim("scope:"), p.ExceptionNotes)
		}
		fmt.Fprintln(os.Stdout, "")
		for line := range strings.SplitSeq(p.ProposedDiff, "\n") {
			if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
				fmt.Fprintf(os.Stdout, "    %s\n", ui.Green(line))
			} else if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
				fmt.Fprintf(os.Stdout, "    %s\n", ui.Red(line))
			} else if strings.HasPrefix(line, "@@") {
				fmt.Fprintf(os.Stdout, "    %s\n", ui.Cyan(line))
			} else if line != "" {
				fmt.Fprintf(os.Stdout, "    %s\n", ui.Dim(line))
			}
		}
		fmt.Fprintln(os.Stdout, "")
	}
	fmt.Fprintln(os.Stdout, "  "+ui.Dim("Preview only: zero files modified. Apply changes manually if approved."))
}

func discoverProjects(dbPath string) ([]agents.CandidateProject, error) {
	st, err := store.Open(dbPath)
	if err != nil {
		return nil, err
	}
	defer st.Close()

	projects, err := st.Projects()
	if err != nil {
		return nil, err
	}

	candidates := make([]agents.CandidateProject, 0, len(projects))
	for _, p := range projects {
		candidates = append(candidates, agents.CandidateProject{
			ID:   p.ID,
			Path: p.Path,
		})
	}
	return agents.FilterValidProjects(candidates), nil
}

func runAgentsSyncAll(ui render.UI, dbPath string, opts agents.CompileOptions, jsonOut bool) {
	projects, err := discoverProjects(dbPath)
	if err != nil {
		if jsonOut {
			emitJSON(map[string]any{"error": err.Error()})
		} else {
			fmt.Fprintf(os.Stderr, "sila agents sync --all: %v\n", err)
		}
		os.Exit(1)
	}

	res := agents.SyncAllProjects(projects, opts)

	if !res.Preview {
		effDB := dbPath
		if effDB == "" {
			effDB = store.DefaultDBPath()
		}
		if st, err := store.Open(effDB); err == nil {
			defer st.Close()
			agentsDir := agents.FindAgentsDir(opts.RepoRoot)
			hubCommit := agents.ResolveHubCommit(agentsDir)
			rec := agents.NewSyncReceiptFromMulti(res, hubCommit, agents.ResolveInitiator())
			_ = agents.SaveSyncReceipt(st, rec)
		}
	}

	if jsonOut {
		emitJSON(res)
		if res.Failed > 0 {
			os.Exit(1)
		}
		return
	}

	modeStr := ""
	if res.Preview {
		modeStr = " (dry-run)"
	}
	fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · agents sync --all"+modeStr))+fmt.Sprintf(" (%d projects)", res.Total))

	for _, item := range res.Projects {
		if item.Error != "" {
			fmt.Fprintf(os.Stderr, "  %s %s: %s\n", ui.Red("✖"), ui.Bold(item.ProjectID), item.Error)
			continue
		}
		if res.Preview {
			fmt.Fprintf(os.Stdout, "\n  %s %s (%s)\n", ui.Bold("Project:"), ui.Bold(item.ProjectID), item.Path)
			for _, p := range item.Result.Plan {
				detail := p.Detail
				if detail != "" {
					detail = " — " + detail
				}
				fmt.Fprintf(os.Stdout, "    • %s %s%s (%d → %d bytes)\n", p.Action, p.Path, detail, p.BeforeBytes, p.AfterBytes)
			}
		} else {
			var summary []string
			status := ui.Green("✔")
			if len(item.Result.Errors) > 0 {
				status = ui.Red("✖")
				switch item.Result.FailureStage {
				case "rollback":
					summary = append(summary, "failed (rolled back)")
				case "commit":
					summary = append(summary, "failed (commit)")
				default:
					summary = append(summary, "failed (preflight)")
				}
			} else {
				if len(item.Result.FilesWritten) > 0 {
					summary = append(summary, fmt.Sprintf("wrote %d file(s)", len(item.Result.FilesWritten)))
				}
				if len(item.Result.Symlinks) > 0 {
					summary = append(summary, fmt.Sprintf("linked %d tool(s)", len(item.Result.Symlinks)))
				}
				if len(summary) == 0 {
					summary = append(summary, "skipped (up to date)")
				}
			}
			fmt.Fprintf(os.Stdout, "  %s %s: %s\n", status, ui.Bold(item.ProjectID), strings.Join(summary, ", "))
			for _, e := range item.Result.Errors {
				fmt.Fprintf(os.Stderr, "    %s %s\n", ui.Red("✖"), e)
			}
		}
	}

	if res.Preview {
		fmt.Fprintln(os.Stdout, "\n  "+ui.Dim("nothing was written; re-run without --dry-run to apply"))
		return
	}

	fmt.Fprintf(os.Stdout, "\n  Summary: %d written, %d skipped, %d failed across %d projects (atomic failures=%d).\n",
		res.Written, res.Skipped, res.Failed, res.Total, res.AtomicFailures)
	if res.Failed > 0 {
		fmt.Fprintf(os.Stderr, "  %s %d/%d projects failed to sync.\n", ui.Red("✖"), res.Failed, res.Total)
		os.Exit(1)
	}
	if res.Written == 0 {
		fmt.Fprintf(os.Stdout, "  %s all %d projects are already up to date.\n", ui.Green("✔"), res.Total)
		return
	}
	fmt.Fprintf(os.Stdout, "  %s all %d projects synced successfully.\n", ui.Green("✔"), res.Total)
}

func runAgentsCheckAll(ui render.UI, dbPath string, opts agents.CompileOptions, jsonOut bool) {
	projects, err := discoverProjects(dbPath)
	if err != nil {
		if jsonOut {
			emitJSON(map[string]any{"error": err.Error()})
		} else {
			fmt.Fprintf(os.Stderr, "sila agents check --all: %v\n", err)
		}
		os.Exit(1)
	}

	res := agents.CheckAllProjects(projects, opts)

	if jsonOut {
		emitJSON(res)
		if res.Drifted > 0 || res.Failed > 0 {
			os.Exit(1)
		}
		return
	}

	fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · agents check --all"))+fmt.Sprintf(" (%d projects)", res.Total))

	for _, item := range res.Projects {
		if item.Error != "" {
			fmt.Fprintf(os.Stderr, "  %s %s: %s\n", ui.Red("✖"), ui.Bold(item.ProjectID), item.Error)
			continue
		}
		if !item.Report.HasDrift {
			continue
		}
		fmt.Fprintf(os.Stdout, "  %s %s: DRIFT DETECTED (%s, managed rev=%d hash=%s)\n", ui.Red("✖"), ui.Bold(item.ProjectID), item.Report.DriftKind, item.Report.ManagedRevision, item.Report.ManagedHash)
		for _, d := range item.Report.Details {
			fmt.Fprintf(os.Stdout, "    %s %s\n", ui.Yellow("▲"), ui.Dim(d))
		}
		for _, m := range item.Report.MissingFiles {
			fmt.Fprintf(os.Stdout, "    %s missing file: %s\n", ui.Red("✖"), ui.Bold(m))
		}
	}

	fmt.Fprintf(os.Stdout, "\n  in_sync=%d hash_drift=%d legacy=%d missing_target=%d principles_drift=%d failed=%d\n",
		res.InSync, res.HashDrift, res.Legacy, res.MissingTarget, res.PrinciplesDrift, res.Failed)
	if res.Drifted > 0 || res.Failed > 0 {
		fmt.Fprintf(os.Stderr, "  %s %d drifted, %d failed out of %d projects.\n", ui.Red("✖"), res.Drifted, res.Failed, res.Total)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stdout, "  %s all %d projects in sync with Core Hub standards.\n", ui.Green("✔"), res.Total)
}

func runAgentsStatus(ui render.UI, dbPath, project string, allProjects, jsonOut bool) {
	effDB := dbPath
	if effDB == "" {
		effDB = store.DefaultDBPath()
	}
	st, err := store.OpenRO(effDB)
	if err != nil {
		if jsonOut {
			emitJSON(map[string]any{"synced": false, "error": err.Error()})
		} else {
			fmt.Fprintf(os.Stderr, "sila agents status: %v\n", err)
		}
		os.Exit(1)
	}
	defer st.Close()

	rec, err := agents.LoadLastSyncReceipt(st)
	if err != nil {
		if jsonOut {
			emitJSON(map[string]any{"synced": false, "error": err.Error()})
		} else {
			fmt.Fprintf(os.Stderr, "sila agents status: %v\n", err)
		}
		os.Exit(1)
	}

	if rec == nil {
		if jsonOut {
			emitJSON(map[string]any{
				"synced":  false,
				"message": "No agent rule sync recorded yet. Run 'sila agents sync' to sync rules.",
			})
		} else {
			fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · agents status")))
			fmt.Fprintln(os.Stdout, "  "+ui.Dim("No agent rule sync recorded yet. Run 'sila agents sync' or 'sila agents sync --all' to sync rules."))
		}
		return
	}

	relAge := agents.FormatRelativeAge(rec.Timestamp, time.Now().UTC())

	if jsonOut {
		type jsonReceipt struct {
			*agents.SyncReceipt
			RelativeAge string `json:"relative_age"`
		}
		emitJSON(jsonReceipt{
			SyncReceipt: rec,
			RelativeAge: relAge,
		})
		return
	}

	fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · agents status")))
	fmt.Fprintf(os.Stdout, "  • Last synchronized: %s (%s)\n", ui.Bold(relAge), ui.Dim(rec.Timestamp))
	fmt.Fprintf(os.Stdout, "  • Core Hub Commit:   %s\n", ui.Bold(rec.HubCommit))
	if rec.ManagedHash != "" {
		fmt.Fprintf(os.Stdout, "  • Managed Block:     rev=%d hash=%s\n", rec.ManagedRevision, rec.ManagedHash)
	} else {
		fmt.Fprintf(os.Stdout, "  • Managed Block:     %s\n", ui.Dim("not recorded (pre-fingerprint receipt)"))
	}
	fmt.Fprintf(os.Stdout, "  • Scope:             %s (%d project(s) targeted)\n", ui.Bold(rec.Scope), rec.TotalProjects)
	fmt.Fprintf(os.Stdout, "  • Initiator:         %s\n", ui.Bold(rec.Initiator))
	fmt.Fprintf(os.Stdout, "  • Result:            %s succeeded, %s failed · %d file(s) written, %d link(s)\n",
		ui.Green(fmt.Sprintf("%d", rec.Succeeded)),
		ui.Red(fmt.Sprintf("%d", rec.Failed)),
		rec.FilesWritten,
		rec.Symlinks,
	)

	if len(rec.Projects) > 0 {
		fmt.Fprintln(os.Stdout, "\n  Projects:")
		for _, p := range rec.Projects {
			statusIcon := ui.Green("✔")
			statusText := "up to date"
			if !p.Success || len(p.Errors) > 0 {
				statusIcon = ui.Red("✖")
				statusText = strings.Join(p.Errors, ", ")
			} else if p.Skipped {
				statusText = "skipped (up to date)"
			} else if len(p.FilesWritten) > 0 || len(p.Symlinks) > 0 {
				statusText = fmt.Sprintf("wrote %d file(s), %d link(s)", len(p.FilesWritten), len(p.Symlinks))
			}
			if p.ManagedHash != "" {
				statusText += fmt.Sprintf(" · rev=%d hash=%s", p.ManagedRevision, p.ManagedHash)
			}
			if p.DriftKind != "" {
				statusText += " · " + p.DriftKind
			}
			fmt.Fprintf(os.Stdout, "    %s %s (%s): %s\n", statusIcon, ui.Bold(p.ProjectID), ui.Dim(p.Path), statusText)
		}
	}
}
