package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/jadmadi/sila/internal/goals"
)

func (gc *goalsContext) handleCreate(tDir, slug string) {
	ui := gc.ui
	tierOpt := gc.tierOpt
	status := gc.status
	commitMsg := gc.commitMsg
	args := gc.args
	title := gc.title
	desc := gc.desc
	dependsOn := gc.dependsOn
	touch := gc.touch
	seq := gc.seq
	independent := gc.independent
	shape := gc.shape
	invariantsOpt := gc.invariantsOpt
	tasksOpt := gc.tasksOpt
	workPackageOpt := gc.workPackageOpt
	templateDirOpt := gc.templateDirOpt
	specReviewOpt := gc.specReviewOpt
	agent := gc.agent
	force := gc.force
	jsonOut := gc.jsonOut
	if slug == "" {
		fmt.Fprintln(os.Stderr, "Usage: sila goals create <slug> [--title=\"...\"] [--desc=\"...\"] [--status=pending|implemented] [--commit=<sha>] [--depends-on=\"...\"] [--touch=\"...\"] [--independent] [--seq=N] [--tier=immediate|roadmap|nice-to-have|wont-fix] [--invariants=\"...\"] [--tasks=\"...\"] [--template-dir=path] [--strict]")
		os.Exit(2)
	}
	createTier := tierOpt
	createStatus := status
	createCommit := commitMsg
	strictMode := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--strict" || a == "-strict" {
			strictMode = true
		} else if a == "--independent" || a == "-independent" {
			independent = true
		} else if (a == "--tier" || a == "-tier") && i+1 < len(args) {
			createTier = args[i+1]
			i++
		} else if after, ok := strings.CutPrefix(a, "--tier="); ok {
			createTier = after
		} else if after, ok := strings.CutPrefix(a, "-tier="); ok {
			createTier = after
		} else if after, ok := strings.CutPrefix(a, "--title="); ok {
			title = after
		} else if after, ok := strings.CutPrefix(a, "-title="); ok {
			title = after
		} else if after, ok := strings.CutPrefix(a, "--desc="); ok {
			desc = after
		} else if after, ok := strings.CutPrefix(a, "-desc="); ok {
			desc = after
		} else if after, ok := strings.CutPrefix(a, "--depends-on="); ok {
			dependsOn = after
		} else if after, ok := strings.CutPrefix(a, "-depends-on="); ok {
			dependsOn = after
		} else if after, ok := strings.CutPrefix(a, "--touch="); ok {
			touch = after
		} else if after, ok := strings.CutPrefix(a, "-touch="); ok {
			touch = after
		} else if after, ok := strings.CutPrefix(a, "--shape="); ok {
			shape = after
		} else if after, ok := strings.CutPrefix(a, "-shape="); ok {
			shape = after
		} else if after, ok := strings.CutPrefix(a, "--status="); ok {
			createStatus = after
		} else if after, ok := strings.CutPrefix(a, "-status="); ok {
			createStatus = after
		} else if after, ok := strings.CutPrefix(a, "--commit="); ok {
			createCommit = after
		} else if after, ok := strings.CutPrefix(a, "-commit="); ok {
			createCommit = after
		} else if after, ok := strings.CutPrefix(a, "--sha="); ok {
			createCommit = after
		} else if after, ok := strings.CutPrefix(a, "-sha="); ok {
			createCommit = after
		} else if after, ok := strings.CutPrefix(a, "--invariants="); ok {
			invariantsOpt = after
		} else if after, ok := strings.CutPrefix(a, "-invariants="); ok {
			invariantsOpt = after
		} else if after, ok := strings.CutPrefix(a, "--invariant="); ok {
			invariantsOpt = after
		} else if after, ok := strings.CutPrefix(a, "-invariant="); ok {
			invariantsOpt = after
		} else if after, ok := strings.CutPrefix(a, "--tasks="); ok {
			tasksOpt = after
		} else if after, ok := strings.CutPrefix(a, "-tasks="); ok {
			tasksOpt = after
		} else if after, ok := strings.CutPrefix(a, "--task="); ok {
			tasksOpt = after
		} else if after, ok := strings.CutPrefix(a, "-task="); ok {
			tasksOpt = after
		} else if after, ok := strings.CutPrefix(a, "--personas="); ok {
			gc.personasOpt = after
		} else if after, ok := strings.CutPrefix(a, "-personas="); ok {
			gc.personasOpt = after
		} else if after, ok := strings.CutPrefix(a, "--persona="); ok {
			gc.personasOpt = after
		} else if after, ok := strings.CutPrefix(a, "-persona="); ok {
			gc.personasOpt = after
		} else if after, ok := strings.CutPrefix(a, "--squad="); ok {
			gc.personasOpt = after
		} else if after, ok := strings.CutPrefix(a, "-squad="); ok {
			gc.personasOpt = after
		} else if after, ok := strings.CutPrefix(a, "--work-package="); ok {
			workPackageOpt = after
		} else if after, ok := strings.CutPrefix(a, "-work-package="); ok {
			workPackageOpt = after
		} else if after, ok := strings.CutPrefix(a, "--template-dir="); ok {
			templateDirOpt = after
		} else if after, ok := strings.CutPrefix(a, "-template-dir="); ok {
			templateDirOpt = after
		} else if after, ok := strings.CutPrefix(a, "--template="); ok {
			templateDirOpt = after
		} else if after, ok := strings.CutPrefix(a, "-template="); ok {
			templateDirOpt = after
		} else if after, ok := strings.CutPrefix(a, "--spec-review="); ok {
			specReviewOpt = after
		} else if after, ok := strings.CutPrefix(a, "-spec-review="); ok {
			specReviewOpt = after
		}
	}
	var loadedWorkPackage *goals.WorkPackage
	if workPackageOpt != "" {
		if tasksOpt != "" {
			if jsonOut {
				emitJSON(map[string]any{"error": "cannot specify both --work-package and --tasks", "slug": slug, "created": false})
				return
			}
			fmt.Fprintf(os.Stderr, "%s sila goals create error: cannot specify both --work-package and --tasks\n", ui.Red("✖"))
			os.Exit(2)
		}
		if invariantsOpt != "" {
			if jsonOut {
				emitJSON(map[string]any{"error": "cannot specify both --work-package and --invariants", "slug": slug, "created": false})
				return
			}
			fmt.Fprintf(os.Stderr, "%s sila goals create error: cannot specify both --work-package and --invariants\n", ui.Red("✖"))
			os.Exit(2)
		}
		wpPath := workPackageOpt
		if !filepath.IsAbs(wpPath) {
			wpPath = filepath.Join(tDir, wpPath)
		}
		wpData, err := os.ReadFile(wpPath)
		if err != nil {
			if jsonOut {
				emitJSON(map[string]any{"error": fmt.Sprintf("read work package: %v", err), "slug": slug, "created": false})
				return
			}
			fmt.Fprintf(os.Stderr, "%s sila goals create error reading work package: %v\n", ui.Red("✖"), err)
			os.Exit(1)
		}
		wp, err := goals.DecodeWorkPackage(wpData)
		if err != nil {
			if jsonOut {
				emitJSON(map[string]any{"error": fmt.Sprintf("invalid work package: %v", err), "slug": slug, "created": false})
				return
			}
			fmt.Fprintf(os.Stderr, "%s sila goals create error: invalid work package: %v\n", ui.Red("✖"), err)
			os.Exit(1)
		}
		if wp.GoalSlug != slug {
			if jsonOut {
				emitJSON(map[string]any{"error": fmt.Sprintf("work package goal_slug %q does not match requested slug %q", wp.GoalSlug, slug), "slug": slug, "created": false})
				return
			}
			fmt.Fprintf(os.Stderr, "%s sila goals create error: work package goal_slug %q does not match requested slug %q\n", ui.Red("✖"), wp.GoalSlug, slug)
			os.Exit(1)
		}
		loadedWorkPackage = wp
	}
	var loadedSpecReview *goals.SpecReview
	if specReviewOpt != "" {
		if loadedWorkPackage == nil {
			if jsonOut {
				emitJSON(map[string]any{"error": "spec_review requires a work_package", "slug": slug, "created": false})
				return
			}
			fmt.Fprintf(os.Stderr, "%s sila goals create error: --spec-review requires --work-package\n", ui.Red("✖"))
			os.Exit(2)
		}
		reviewPath := specReviewOpt
		if !filepath.IsAbs(reviewPath) {
			reviewPath = filepath.Join(tDir, reviewPath)
		}
		reviewData, err := os.ReadFile(reviewPath)
		if err != nil {
			if jsonOut {
				emitJSON(map[string]any{"error": fmt.Sprintf("read spec review: %v", err), "slug": slug, "created": false})
				return
			}
			fmt.Fprintf(os.Stderr, "%s sila goals create error reading spec review: %v\n", ui.Red("✖"), err)
			os.Exit(1)
		}
		var rev goals.SpecReview
		dec := json.NewDecoder(strings.NewReader(string(reviewData)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&rev); err != nil {
			if jsonOut {
				emitJSON(map[string]any{"error": fmt.Sprintf("invalid spec_review: %v", err), "slug": slug, "created": false})
				return
			}
			fmt.Fprintf(os.Stderr, "%s sila goals create error: invalid spec_review: %v\n", ui.Red("✖"), err)
			os.Exit(1)
		}
		loadedSpecReview = &rev
	}
	var parsedInvariants []string
	if invariantsOpt != "" {
		splitChar := ";"
		if !strings.Contains(invariantsOpt, ";") && strings.Contains(invariantsOpt, "\n") {
			splitChar = "\n"
		}
		for p := range strings.SplitSeq(invariantsOpt, splitChar) {
			if s := strings.TrimSpace(p); s != "" {
				parsedInvariants = append(parsedInvariants, s)
			}
		}
	}
	var parsedTasks []string
	if tasksOpt != "" {
		splitChar := ";"
		if !strings.Contains(tasksOpt, ";") && strings.Contains(tasksOpt, "\n") {
			splitChar = "\n"
		}
		for p := range strings.SplitSeq(tasksOpt, splitChar) {
			if s := strings.TrimSpace(p); s != "" {
				parsedTasks = append(parsedTasks, s)
			}
		}
	}
	var deps []string
	if dependsOn != "" {
		for p := range strings.SplitSeq(dependsOn, ",") {
			if s := strings.TrimSpace(p); s != "" {
				deps = append(deps, s)
			}
		}
	}
	var filesToTouch []string
	if touch != "" {
		for p := range strings.SplitSeq(touch, ",") {
			if s := goals.NormalizeGoalPath(p); s != "" {
				filesToTouch = append(filesToTouch, s)
			}
		}
	}
	var indepPtr *bool
	if independent {
		t := true
		indepPtr = &t
	}
	var parsedPersonas []string
	if gc.personasOpt != "" {
		for p := range strings.SplitSeq(gc.personasOpt, ",") {
			if s := strings.TrimSpace(p); s != "" {
				parsedPersonas = append(parsedPersonas, s)
			}
		}
	}
	g, err := goals.CreateGoal(goals.CreateGoalParams{
		RootDir:      tDir,
		Slug:         slug,
		Title:        title,
		Description:  desc,
		DependsOn:    deps,
		Independent:  indepPtr,
		Sequence:     seq,
		FilesToTouch: filesToTouch,
		Overwrite:    force,
		Shape:        shape,
		Tier:         createTier,
		Invariants:   parsedInvariants,
		Tasks:        parsedTasks,
		Personas:     parsedPersonas,
		Status:       createStatus,
		Commit:       createCommit,
		Strict:       strictMode,
		WorkPackage:  loadedWorkPackage,
		SpecReview:   loadedSpecReview,
		TemplateDir:  templateDirOpt,
	})
	if err != nil {
		if jsonOut {
			emitJSON(map[string]any{"error": err.Error(), "slug": slug, "created": false})
			return
		}
		fmt.Fprintf(os.Stderr, "%s sila goals create error: %v\n", ui.Red("✖"), err)
		os.Exit(1)
	}
	if jsonOut {
		res := map[string]any{"created": true, "goal": g}
		if g.PreFlight != nil {
			res["preflight"] = g.PreFlight
			if len(g.PreFlight.Warnings) > 0 {
				res["conflicts_detected"] = g.PreFlight.Warnings
			}
		}
		emitJSON(res)
		return
	}
	if g.PreFlight != nil && len(g.PreFlight.Warnings) > 0 {
		for _, w := range g.PreFlight.Warnings {
			fmt.Fprintf(os.Stderr, "%s %s\n", ui.Yellow("▲"), w)
		}
		if len(g.PreFlight.SuggestedDependsOn) > 0 {
			fmt.Fprintf(os.Stderr, "  %s Suggested prerequisites: --depends-on=%s\n", ui.Cyan("ℹ"), strings.Join(g.PreFlight.SuggestedDependsOn, ","))
		}
		if g.PreFlight.SuggestedSequence > 0 && seq == 0 {
			fmt.Fprintf(os.Stderr, "  %s Suggested sequence: --seq=%d\n", ui.Cyan("ℹ"), g.PreFlight.SuggestedSequence)
		}
	}
	modeStr := "Independent ⚡"
	if !g.Independent {
		modeStr = fmt.Sprintf("Dependent 🔗 (Prereqs: %s)", strings.Join(g.DependsOn, ", "))
	}
	absDir, _ := filepath.Abs(tDir)
	projName := filepath.Base(absDir)
	fmt.Fprintf(os.Stdout, "%s Goal package %s created under goals/%s in %s\n", ui.Green("✔"), ui.Bold(g.Slug), g.Slug, ui.Magenta(ui.Bold(projName)))
	fmt.Fprintf(os.Stdout, "  Title:     %s\n", g.Title)
	if g.Tier != "" {
		fmt.Fprintf(os.Stdout, "  Tier:      %s\n", g.Tier)
	}
	fmt.Fprintf(os.Stdout, "  Mode:      %s\n", modeStr)
	if len(g.FilesToTouch) > 0 {
		fmt.Fprintf(os.Stdout, "  Touch:     %s\n", strings.Join(g.FilesToTouch, ", "))
	}
	if len(g.Personas) > 0 {
		fmt.Fprintf(os.Stdout, "  Squad:     %s\n", ui.Cyan(strings.Join(g.Personas, ", ")))
	}
	fmt.Fprintf(os.Stdout, "  Launch:    %s\n", ui.Dim(g.LaunchCmd))
	commitCmd := fmt.Sprintf("git commit -m %q", fmt.Sprintf("docs(goals): register %s (goals/%s/goal.md)", g.Slug, g.Slug))
	fmt.Fprintf(os.Stdout, "  Register:  %s\n", ui.Green(commitCmd))
	closeCmd := fmt.Sprintf("git commit -m %q", fmt.Sprintf("feat: implement %s (goals/%s/goal.md)", g.Slug, g.Slug))
	fmt.Fprintf(os.Stdout, "  Commit:    %s (closure requires feat:/fix:/refactor:/perf:/chore:; docs:/test: are ignored)\n", ui.Green(closeCmd))
	claimAgent := agent
	if claimAgent == "" {
		claimAgent = "<name>"
	}
	fmt.Fprintf(os.Stdout, "  Execute:   Claim only when starting implementation: %s\n", ui.Cyan("sila goals claim "+g.Slug+" --agent="+claimAgent))
}

func (gc *goalsContext) handleReopen(tDir string, rArgs []string) {
	ui := gc.ui
	st := gc.st
	agent := gc.agent
	reason := gc.reason
	noSync := gc.noSync
	stage := gc.stage
	commitMsg := gc.commitMsg
	jsonOut := gc.jsonOut
	if len(rArgs) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: sila goals reopen <slug> [--reason=\"...\"] [--agent=<name>] [--claim]")
		os.Exit(2)
	}
	slug := rArgs[1]
	if tDir == "" {
		tDir, _ = goals.ResolveProjectDir(st, "")
	}
	note := gc.note
	claimImmediate := gc.claimImmediate
	reopenAgent := agent
	if reopenAgent == "" {
		reopenAgent = os.Getenv("SILA_AGENT")
		if reopenAgent == "" {
			reopenAgent = "agent"
		}
	}
	reopenReason := reason
	if reopenReason == "" {
		reopenReason = note
	}
	claimImmed := claimImmediate
	var cleanRemaining []string
	for _, a := range rArgs[2:] {
		if a == "--claim" || a == "-claim" {
			claimImmed = true
		} else if after, ok := strings.CutPrefix(a, "--reason="); ok {
			reopenReason = after
		} else if after, ok := strings.CutPrefix(a, "--agent="); ok {
			reopenAgent = after
		} else if !strings.HasPrefix(a, "-") {
			cleanRemaining = append(cleanRemaining, a)
		}
	}
	if len(cleanRemaining) > 0 && (reopenReason == "" || reopenReason == "reopened for remediation") {
		reopenReason = strings.Join(cleanRemaining, " ")
	}
	if reopenReason == "" {
		reopenReason = "reopened for remediation"
	}
	rec, cl, err := goals.ReopenGoal(tDir, slug, reopenAgent, reopenReason, claimImmed)
	if err != nil {
		if jsonOut {
			emitJSON(map[string]any{"error": err.Error(), "slug": slug, "reopened": false})
			return
		}
		fmt.Fprintf(os.Stderr, "sila goals reopen error: %v\n", err)
		os.Exit(1)
	}
	if !noSync {
		if res, err := goals.ScanGoals(tDir); err == nil {
			_ = goals.SyncReadmeWithOpts(tDir, res, goals.SyncReadmeOptions{Stage: stage, CommitMsg: commitMsg})
		}
	}
	if jsonOut {
		res := map[string]any{"slug": slug, "reopened": true, "record": rec}
		if cl != nil {
			res["claim"] = cl
		}
		emitJSON(res)
		return
	}
	absDir, _ := filepath.Abs(tDir)
	projName := filepath.Base(absDir)
	fmt.Fprintf(os.Stdout, "%s Goal %s in %s reopened for remediation by %s\n",
		ui.Yellow("🔁"), ui.Bold(slug), ui.Magenta(ui.Bold(projName)), ui.Cyan("@"+rec.ReopenedBy))
	fmt.Fprintf(os.Stdout, "  Reason: %s\n", ui.Yellow(rec.Reason))
	if rec.OriginalCommit != "" {
		fmt.Fprintf(os.Stdout, "  Prior:  commit %s\n", ui.Dim(rec.OriginalCommit))
	}
	if cl != nil {
		fmt.Fprintf(os.Stdout, "  Claim:  locked for %s\n", ui.Cyan("@"+cl.Agent))
	} else {
		fmt.Fprintf(os.Stdout, "  Next:   %s\n", ui.Cyan("sila goals claim "+slug+" --agent=<name>"))
	}
}

func (gc *goalsContext) handleUpdate(tDir string, uArgs []string) {
	ui := gc.ui
	st := gc.st
	title := gc.title
	desc := gc.desc
	dependsOn := gc.dependsOn
	touch := gc.touch
	seq := gc.seq
	independent := gc.independent
	tierOpt := gc.tierOpt
	shape := gc.shape
	addDep := gc.addDep
	removeDep := gc.removeDep
	noSync := gc.noSync
	stage := gc.stage
	commitMsg := gc.commitMsg
	jsonOut := gc.jsonOut
	if len(uArgs) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: sila goals update <slug> [--title=\"...\"] [--desc=\"...\"] [--depends-on=\"...\"] [--add-dep=\"...\"] [--remove-dep=\"...\"] [--independent] [--dependent] [--seq=N] [--shape=ship|scout] [--tier=...] [--touch=\"...\"] [--no-sync]")
		os.Exit(2)
	}
	slug := uArgs[1]
	if tDir == "" {
		tDir, _ = goals.ResolveProjectDir(st, "")
	}

	uTitle := title
	uDesc := desc
	uDependsOn := dependsOn
	uTouch := touch
	uSeq := seq
	dependent := gc.dependent
	uIndependent := independent
	uDependent := dependent
	uNoSync := noSync
	uShape := shape
	uTier := tierOpt
	uSpecReview := gc.specReviewOpt
	uStrict := false
	var addDepsList []string
	var removeDepsList []string
	var explicitIndep *bool
	hasExplicitDeps := dependsOn != "" || slices.Contains(os.Args, "--depends-on=") || slices.Contains(os.Args, "-depends-on=")

	if addDep != "" {
		for p := range strings.SplitSeq(addDep, ",") {
			if s := strings.TrimSpace(p); s != "" {
				addDepsList = append(addDepsList, s)
			}
		}
	}
	if removeDep != "" {
		for p := range strings.SplitSeq(removeDep, ",") {
			if s := strings.TrimSpace(p); s != "" {
				removeDepsList = append(removeDepsList, s)
			}
		}
	}
	if uIndependent {
		t := true
		explicitIndep = &t
	} else if uDependent {
		f := false
		explicitIndep = &f
	}

	var updatePersonas []string
	var addPersonasList []string
	hasExplicitPersonas := false
	clearPersonas := gc.clearPersonasOpt
	if gc.personasOpt != "" {
		hasExplicitPersonas = true
		for p := range strings.SplitSeq(gc.personasOpt, ",") {
			if s := strings.TrimSpace(p); s != "" {
				updatePersonas = append(updatePersonas, s)
			}
		}
	}
	if gc.addPersonasOpt != "" {
		for p := range strings.SplitSeq(gc.addPersonasOpt, ",") {
			if s := strings.TrimSpace(p); s != "" {
				addPersonasList = append(addPersonasList, s)
			}
		}
	}

	// Parse remaining args
	for _, a := range uArgs[2:] {
		if after, ok := strings.CutPrefix(a, "--title="); ok {
			uTitle = after
		} else if after, ok := strings.CutPrefix(a, "--desc="); ok {
			uDesc = after
		} else if after, ok := strings.CutPrefix(a, "--description="); ok {
			uDesc = after
		} else if after, ok := strings.CutPrefix(a, "--depends-on="); ok {
			uDependsOn = after
			hasExplicitDeps = true
		} else if after, ok := strings.CutPrefix(a, "--deps="); ok {
			uDependsOn = after
			hasExplicitDeps = true
		} else if after, ok := strings.CutPrefix(a, "--set="); ok {
			uDependsOn = after
			hasExplicitDeps = true
		} else if after, ok := strings.CutPrefix(a, "--add-dep="); ok {
			for p := range strings.SplitSeq(after, ",") {
				if s := strings.TrimSpace(p); s != "" {
					addDepsList = append(addDepsList, s)
				}
			}
		} else if after, ok := strings.CutPrefix(a, "--add="); ok {
			for p := range strings.SplitSeq(after, ",") {
				if s := strings.TrimSpace(p); s != "" {
					addDepsList = append(addDepsList, s)
				}
			}
		} else if after, ok := strings.CutPrefix(a, "--remove-dep="); ok {
			for p := range strings.SplitSeq(after, ",") {
				if s := strings.TrimSpace(p); s != "" {
					removeDepsList = append(removeDepsList, s)
				}
			}
		} else if after, ok := strings.CutPrefix(a, "--remove="); ok {
			for p := range strings.SplitSeq(after, ",") {
				if s := strings.TrimSpace(p); s != "" {
					removeDepsList = append(removeDepsList, s)
				}
			}
		} else if after, ok := strings.CutPrefix(a, "--seq="); ok {
			fmt.Sscanf(after, "%d", &uSeq)
		} else if after, ok := strings.CutPrefix(a, "--sequence="); ok {
			fmt.Sscanf(after, "%d", &uSeq)
		} else if after, ok := strings.CutPrefix(a, "--shape="); ok {
			uShape = after
		} else if after, ok := strings.CutPrefix(a, "--tier="); ok {
			uTier = after
		} else if after, ok := strings.CutPrefix(a, "-tier="); ok {
			uTier = after
		} else if after, ok := strings.CutPrefix(a, "--touch="); ok {
			uTouch = after
		} else if a == "--independent" {
			t := true
			explicitIndep = &t
		} else if a == "--dependent" {
			f := false
			explicitIndep = &f
		} else if a == "--no-sync" {
			uNoSync = true
		} else if after, ok := strings.CutPrefix(a, "--personas="); ok {
			hasExplicitPersonas = true
			updatePersonas = nil
			if after != "" && !strings.EqualFold(after, "none") && !strings.EqualFold(after, "nil") && !strings.EqualFold(after, "-") {
				for p := range strings.SplitSeq(after, ",") {
					if s := strings.TrimSpace(p); s != "" {
						updatePersonas = append(updatePersonas, s)
					}
				}
			}
		} else if after, ok := strings.CutPrefix(a, "-personas="); ok {
			hasExplicitPersonas = true
			updatePersonas = nil
			if after != "" && !strings.EqualFold(after, "none") && !strings.EqualFold(after, "nil") && !strings.EqualFold(after, "-") {
				for p := range strings.SplitSeq(after, ",") {
					if s := strings.TrimSpace(p); s != "" {
						updatePersonas = append(updatePersonas, s)
					}
				}
			}
		} else if after, ok := strings.CutPrefix(a, "--persona="); ok {
			hasExplicitPersonas = true
			updatePersonas = nil
			if after != "" && !strings.EqualFold(after, "none") && !strings.EqualFold(after, "nil") && !strings.EqualFold(after, "-") {
				for p := range strings.SplitSeq(after, ",") {
					if s := strings.TrimSpace(p); s != "" {
						updatePersonas = append(updatePersonas, s)
					}
				}
			}
		} else if after, ok := strings.CutPrefix(a, "-persona="); ok {
			hasExplicitPersonas = true
			updatePersonas = nil
			if after != "" && !strings.EqualFold(after, "none") && !strings.EqualFold(after, "nil") && !strings.EqualFold(after, "-") {
				for p := range strings.SplitSeq(after, ",") {
					if s := strings.TrimSpace(p); s != "" {
						updatePersonas = append(updatePersonas, s)
					}
				}
			}
		} else if after, ok := strings.CutPrefix(a, "--squad="); ok {
			hasExplicitPersonas = true
			updatePersonas = nil
			if after != "" && !strings.EqualFold(after, "none") && !strings.EqualFold(after, "nil") && !strings.EqualFold(after, "-") {
				for p := range strings.SplitSeq(after, ",") {
					if s := strings.TrimSpace(p); s != "" {
						updatePersonas = append(updatePersonas, s)
					}
				}
			}
		} else if after, ok := strings.CutPrefix(a, "--add-personas="); ok {
			for p := range strings.SplitSeq(after, ",") {
				if s := strings.TrimSpace(p); s != "" {
					addPersonasList = append(addPersonasList, s)
				}
			}
		} else if after, ok := strings.CutPrefix(a, "--add-persona="); ok {
			for p := range strings.SplitSeq(after, ",") {
				if s := strings.TrimSpace(p); s != "" {
					addPersonasList = append(addPersonasList, s)
				}
			}
		} else if a == "--clear-personas" || a == "--clear-squad" {
			clearPersonas = true
		} else if after, ok := strings.CutPrefix(a, "--spec-review="); ok {
			uSpecReview = after
		} else if after, ok := strings.CutPrefix(a, "-spec-review="); ok {
			uSpecReview = after
		} else if a == "--strict" || a == "-strict" {
			uStrict = true
		}
	}

	params := goals.UpdateGoalParams{
		RootDir: tDir,
		Slug:    slug,
	}
	if uTitle != "" {
		params.Title = &uTitle
	}
	if uDesc != "" {
		params.Description = &uDesc
	}
	if uTier != "" {
		params.Tier = &uTier
	}
	if hasExplicitDeps {
		deps := []string{}
		if uDependsOn != "" && !strings.EqualFold(uDependsOn, "none") && !strings.EqualFold(uDependsOn, "nil") && !strings.EqualFold(uDependsOn, "-") {
			for p := range strings.SplitSeq(uDependsOn, ",") {
				if s := strings.TrimSpace(p); s != "" {
					deps = append(deps, s)
				}
			}
		}
		params.DependsOn = deps
	}
	if len(addDepsList) > 0 {
		params.AddDeps = addDepsList
	}
	if len(removeDepsList) > 0 {
		params.RemoveDeps = removeDepsList
	}
	if explicitIndep != nil {
		params.Independent = explicitIndep
	}
	if uSeq > 0 {
		params.Sequence = &uSeq
	}
	if uShape != "" {
		params.Shape = &uShape
	}
	if uTouch != "" {
		var filesToTouch []string
		for p := range strings.SplitSeq(uTouch, ",") {
			if s := goals.NormalizeGoalPath(p); s != "" {
				filesToTouch = append(filesToTouch, s)
			}
		}
		params.FilesToTouch = filesToTouch
	}
	if hasExplicitPersonas {
		params.Personas = updatePersonas
	}
	if len(addPersonasList) > 0 {
		params.AddPersonas = addPersonasList
	}
	if clearPersonas {
		params.ClearPersonas = true
	}

	if uSpecReview != "" {
		reviewPath := uSpecReview
		if !filepath.IsAbs(reviewPath) {
			reviewPath = filepath.Join(tDir, reviewPath)
		}
		reviewData, err := os.ReadFile(reviewPath)
		if err != nil {
			if jsonOut {
				emitJSON(map[string]any{"error": fmt.Sprintf("read spec review: %v", err), "slug": slug, "updated": false})
				return
			}
			fmt.Fprintf(os.Stderr, "%s sila goals update error reading spec review: %v\n", ui.Red("✖"), err)
			os.Exit(1)
		}
		var rev goals.SpecReview
		dec := json.NewDecoder(strings.NewReader(string(reviewData)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&rev); err != nil {
			if jsonOut {
				emitJSON(map[string]any{"error": fmt.Sprintf("invalid spec_review: %v", err), "slug": slug, "updated": false})
				return
			}
			fmt.Fprintf(os.Stderr, "%s sila goals update error: invalid spec_review: %v\n", ui.Red("✖"), err)
			os.Exit(1)
		}
		params.SpecReview = &rev
	}
	params.Strict = uStrict

	g, err := goals.UpdateGoal(params)
	if err != nil {
		if jsonOut {
			emitJSON(map[string]any{"error": err.Error(), "slug": slug, "updated": false})
			return
		}
		fmt.Fprintf(os.Stderr, "sila goals update error: %v\n", err)
		os.Exit(1)
	}
	if !uNoSync {
		if res, err := goals.ScanGoals(tDir); err == nil {
			_ = goals.SyncReadmeWithOpts(tDir, res, goals.SyncReadmeOptions{Stage: stage, CommitMsg: commitMsg})
		}
	}
	if jsonOut {
		emitJSON(map[string]any{"updated": true, "goal": g})
		return
	}

	absDir, _ := filepath.Abs(tDir)
	projName := filepath.Base(absDir)
	modeStr := "Independent ⚡"
	if !g.Independent {
		modeStr = fmt.Sprintf("Dependent 🔗 (Prereqs: %s)", strings.Join(g.DependsOn, ", "))
	}
	fmt.Fprintf(os.Stdout, "%s Goal %s updated in %s\n", ui.Green("✔"), ui.Bold(g.Slug), ui.Magenta(ui.Bold(projName)))
	fmt.Fprintf(os.Stdout, "  Title:        %s\n", g.Title)
	if g.Tier != "" {
		fmt.Fprintf(os.Stdout, "  Tier:         %s\n", g.Tier)
	}
	fmt.Fprintf(os.Stdout, "  Mode:         %s\n", modeStr)
	if len(g.DependsOn) > 0 {
		fmt.Fprintf(os.Stdout, "  Dependencies: %s\n", ui.Cyan(strings.Join(g.DependsOn, ", ")))
	} else {
		fmt.Fprintf(os.Stdout, "  Dependencies: %s\n", ui.Dim("none"))
	}
	if g.Sequence > 0 {
		fmt.Fprintf(os.Stdout, "  Sequence:     %d\n", g.Sequence)
	}
	if g.Shape != "" {
		fmt.Fprintf(os.Stdout, "  Shape:        %s\n", g.Shape)
	}
	if len(g.Personas) > 0 {
		fmt.Fprintf(os.Stdout, "  Review Squad: %s\n", ui.Cyan(strings.Join(g.Personas, ", ")))
	}
}

func (gc *goalsContext) handleDepend(tDir string, dArgs []string) {
	ui := gc.ui
	st := gc.st
	stage := gc.stage
	commitMsg := gc.commitMsg
	noSync := gc.noSync
	jsonOut := gc.jsonOut
	if len(dArgs) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: sila goals depend <slug> [add|remove|set|clear] [<dep...>]")
		fmt.Fprintln(os.Stderr, "       sila goals depend <slug> [--add=<dep>] [--remove=<dep>] [--set=<deps>]")
		os.Exit(2)
	}
	slug := dArgs[1]
	if tDir == "" {
		tDir, _ = goals.ResolveProjectDir(st, "")
	}

	if len(dArgs) == 2 {
		// Show dependencies of slug
		audit, err := goals.ScanGoals(tDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila goals depend error: %v\n", err)
			os.Exit(1)
		}
		var target *goals.Goal
		for _, g := range audit.Goals {
			if g.Slug == slug {
				target = &g
				break
			}
		}
		if target == nil {
			fmt.Fprintf(os.Stderr, "goal %q not found\n", slug)
			os.Exit(1)
		}
		if jsonOut {
			emitJSON(target)
			return
		}
		modeStr := "Independent ⚡"
		if !target.Independent {
			modeStr = "Dependent 🔗"
		}
		fmt.Fprintf(os.Stdout, "Goal: %s (%s)\n", ui.Bold(target.Slug), target.Title)
		fmt.Fprintf(os.Stdout, "Mode: %s\n", modeStr)
		if len(target.DependsOn) > 0 {
			fmt.Fprintf(os.Stdout, "Dependencies (%d):\n", len(target.DependsOn))
			for _, d := range target.DependsOn {
				fmt.Fprintf(os.Stdout, "  - %s\n", ui.Cyan(d))
			}
		} else {
			fmt.Fprintln(os.Stdout, "Dependencies: "+ui.Dim("none"))
		}
		return
	}

	// Positional action or flags
	action := strings.ToLower(dArgs[2])
	var addList []string
	var removeList []string
	var setList []string
	hasSet := false
	uNoSync := noSync

	switch action {
	case "add":
		for _, a := range dArgs[3:] {
			if strings.HasPrefix(a, "-") {
				if a == "--no-sync" {
					uNoSync = true
				}
				continue
			}
			for p := range strings.SplitSeq(a, ",") {
				if s := strings.TrimSpace(p); s != "" {
					addList = append(addList, s)
				}
			}
		}
	case "remove", "rm", "del", "delete":
		for _, a := range dArgs[3:] {
			if strings.HasPrefix(a, "-") {
				if a == "--no-sync" {
					uNoSync = true
				}
				continue
			}
			for p := range strings.SplitSeq(a, ",") {
				if s := strings.TrimSpace(p); s != "" {
					removeList = append(removeList, s)
				}
			}
		}
	case "set":
		hasSet = true
		for _, a := range dArgs[3:] {
			if strings.HasPrefix(a, "-") {
				if a == "--no-sync" {
					uNoSync = true
				}
				continue
			}
			for p := range strings.SplitSeq(a, ",") {
				if s := strings.TrimSpace(p); s != "" && !strings.EqualFold(s, "none") && !strings.EqualFold(s, "nil") && !strings.EqualFold(s, "-") {
					setList = append(setList, s)
				}
			}
		}
	case "clear", "none", "reset":
		hasSet = true
		setList = []string{}
	default:
		// Parse flags like --add=..., --remove=..., --set=...
		for _, a := range dArgs[2:] {
			if after, ok := strings.CutPrefix(a, "--add="); ok {
				for p := range strings.SplitSeq(after, ",") {
					if s := strings.TrimSpace(p); s != "" {
						addList = append(addList, s)
					}
				}
			} else if after, ok := strings.CutPrefix(a, "--remove="); ok {
				for p := range strings.SplitSeq(after, ",") {
					if s := strings.TrimSpace(p); s != "" {
						removeList = append(removeList, s)
					}
				}
			} else if strings.HasPrefix(a, "--set=") {
				hasSet = true
				val := strings.TrimPrefix(a, "--set=")
				if val != "" && !strings.EqualFold(val, "none") && !strings.EqualFold(val, "nil") && !strings.EqualFold(val, "-") {
					for p := range strings.SplitSeq(val, ",") {
						if s := strings.TrimSpace(p); s != "" {
							setList = append(setList, s)
						}
					}
				}
			} else if a == "--no-sync" {
				uNoSync = true
			}
		}
	}

	params := goals.UpdateGoalParams{
		RootDir:    tDir,
		Slug:       slug,
		AddDeps:    addList,
		RemoveDeps: removeList,
	}
	if hasSet {
		params.DependsOn = setList
	}

	g, err := goals.UpdateGoal(params)
	if err != nil {
		if jsonOut {
			emitJSON(map[string]any{"error": err.Error(), "slug": slug, "updated": false})
			return
		}
		fmt.Fprintf(os.Stderr, "sila goals depend error: %v\n", err)
		os.Exit(1)
	}
	if !uNoSync {
		if res, err := goals.ScanGoals(tDir); err == nil {
			_ = goals.SyncReadmeWithOpts(tDir, res, goals.SyncReadmeOptions{Stage: stage, CommitMsg: commitMsg})
		}
	}
	if jsonOut {
		emitJSON(map[string]any{"updated": true, "goal": g})
		return
	}

	absDir, _ := filepath.Abs(tDir)
	projName := filepath.Base(absDir)
	modeStr := "Independent ⚡"
	if !g.Independent {
		modeStr = fmt.Sprintf("Dependent 🔗 (Prereqs: %s)", strings.Join(g.DependsOn, ", "))
	}
	fmt.Fprintf(os.Stdout, "%s Goal %s dependencies updated in %s\n", ui.Green("✔"), ui.Bold(g.Slug), ui.Magenta(ui.Bold(projName)))
	fmt.Fprintf(os.Stdout, "  Mode:         %s\n", modeStr)
	if len(g.DependsOn) > 0 {
		fmt.Fprintf(os.Stdout, "  Dependencies: %s\n", ui.Cyan(strings.Join(g.DependsOn, ", ")))
	} else {
		fmt.Fprintf(os.Stdout, "  Dependencies: %s\n", ui.Dim("none"))
	}
}
