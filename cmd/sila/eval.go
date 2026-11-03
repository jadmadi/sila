package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/eval"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

// runEval routes the `sila eval` subcommand family with centralized flags.
func runEval(ui render.UI, dbPath, project, harness, target, evalDir, caseFilter, tagFilter, fromLesson, fromFeedback string, ablation, report, bare, allLessons, jsonOut, toonOut bool, args []string) {
	if fromLesson != "" || fromFeedback != "" || allLessons {
		runEvalSynthesize(ui, dbPath, project, fromLesson, fromFeedback, allLessons, evalDir)
		return
	}

	if len(args) == 0 {
		// Bare `sila eval` or `sila eval --ablation` defaults to running suite
		runEvalExecute(ui, dbPath, project, harness, target, evalDir, caseFilter, tagFilter, ablation, report, jsonOut, toonOut, nil)
		return
	}

	sub := args[0]
	rest := args[1:]

	switch sub {
	case "run":
		runEvalExecute(ui, dbPath, project, harness, target, evalDir, caseFilter, tagFilter, ablation, report, jsonOut, toonOut, rest)
	case "ablate":
		runEvalExecute(ui, dbPath, project, harness, target, evalDir, caseFilter, tagFilter, true, report, jsonOut, toonOut, rest)
	case "recurse":
		runEvalRecurse(ui, dbPath, project, harness, evalDir, caseFilter, tagFilter, jsonOut, toonOut, rest)
	case "init":
		runEvalInit(ui, bare, evalDir, rest)
	case "synthesize", "synth":
		runEvalSynthesize(ui, dbPath, project, fromLesson, fromFeedback, allLessons, evalDir)
	case "adoption":
		runEvalAdoption(ui, dbPath, project, harness, caseFilter, jsonOut, toonOut, rest)
	case "--help", "-h", "help":
		runEvalHelp(ui)
	default:
		// If the first argument is a directory or path, treat as `sila eval run <path>`
		if isDir(sub) || strings.HasPrefix(sub, ".") {
			runEvalExecute(ui, dbPath, project, harness, target, evalDir, caseFilter, tagFilter, ablation, report, jsonOut, toonOut, args)
			return
		}
		fmt.Fprintf(os.Stderr, "sila: unknown eval subcommand: %q\n", sub)
		runEvalHelp(ui)
		os.Exit(2)
	}
}

func isDir(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

func runEvalHelp(ui render.UI) {
	fmt.Print(`sila eval — universal epistemic evaluation, multi-harness runner & context ablation

Usage
  sila eval [run] [dir] [options]        Run evaluation suite across harnesses
  sila eval ablate --target=<rule> [dir] Run context & rule ablation study (Token ROI)
  sila eval init <name> [--bare]         Scaffold a fresh test case directory
  sila eval synthesize [options]         Auto-synthesize regression tests from lessons/feedback

Options
  --harness=<name>       Target runner: auto, claude, agy, mock (default: auto)
  --ablation             Run With-Target vs. Without-Target ablation study
  --target=<path>        Target rule, skill, or context file for ablation comparison
  --report               Generate interactive HTML dashboard at evals/report.html
  --case=<name>          Filter suite to a single case ID or substring
  --tag=<tag>            Filter suite to cases matching this tag
  --eval-dir=<dir>       Root directory containing test cases (default: ./evals or .)
  --from-lesson=<id>     Synthesize regression test from a recorded lesson fact ID
  --from-feedback=<id>   Synthesize test case from a user feedback record ID
  --all-lessons          Synthesize regression tests for all recorded project lessons
  --json                 Emit machine-readable JSON on stdout
  --toon                 Emit compact Token-Oriented Object Notation on stdout
`)
}

func runEvalInit(ui render.UI, bare bool, evalDir string, args []string) {
	name := "sample-case"
	if len(args) > 0 {
		name = args[0]
	}

	targetDir, err := eval.InitCaseTemplate(evalDir, name, bare)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila: failed to initialize eval case: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("%s Initialized eval case %s at %s\n", ui.Green("✔"), ui.Bold(name), ui.Cyan(targetDir))
	fmt.Printf("  Edit prompt:  %s\n", filepath.Join(targetDir, "prompt.md"))
	if !bare {
		fmt.Printf("  Edit graders: %s\n", filepath.Join(targetDir, "graders/"))
	}
	fmt.Printf("  Run eval:     sila eval run %s\n", targetDir)
}

func runEvalExecute(ui render.UI, dbPath, project, harness, target, evalDir, caseFilter, tagFilter string, ablation, report, jsonOut, toonOut bool, args []string) {
	evalPath := evalDir
	timeout := 5 * time.Minute
	var cleanArgs []string
	for _, a := range args {
		if strings.HasPrefix(a, "--timeout=") {
			tStr := strings.TrimPrefix(a, "--timeout=")
			d, err := time.ParseDuration(tStr)
			if err != nil {
				fmt.Fprintf(os.Stderr, "sila: invalid --timeout %q: %v\n", tStr, err)
				os.Exit(2)
			}
			timeout = d
		} else {
			cleanArgs = append(cleanArgs, a)
		}
	}
	if len(cleanArgs) > 0 && cleanArgs[0] != "" {
		evalPath = cleanArgs[0]
	}

	// Registry & Runner selection
	reg := eval.NewRegistry()
	var runner eval.HarnessRunner
	if harness == "auto" || harness == "" {
		var err error
		runner, err = reg.AutoDetect()
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila: %v\n", err)
			os.Exit(1)
		}
	} else {
		var ok bool
		runner, ok = reg.Get(harness)
		if !ok {
			fmt.Fprintf(os.Stderr, "sila: unknown harness runner: %q (available: claude, agy, mock)\n", harness)
			os.Exit(1)
		}
		if !runner.IsAvailable() {
			fmt.Fprintf(os.Stderr, "sila: harness runner %q is not available on PATH\n", harness)
			os.Exit(1)
		}
	}

	// Load suite
	cases, err := eval.LoadSuite(evalPath)
	if err != nil || len(cases) == 0 {
		// Try loading single case directly
		singleCase, singleErr := eval.LoadCase(evalPath)
		if singleErr == nil && singleCase != nil {
			cases = []*eval.Case{singleCase}
		} else {
			fmt.Fprintf(os.Stderr, "sila: no evaluation cases found in %s\n", evalPath)
			fmt.Fprintln(os.Stderr, ui.Dim("  Create your first case with: sila eval init <case-name>"))
			os.Exit(1)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var suiteRes *eval.SuiteResult

	if ablation || target != "" {
		suiteRes, err = eval.ExecuteAblationSuite(ctx, runner, cases, target, "", caseFilter, tagFilter)
	} else {
		suiteRes, err = eval.ExecuteSuite(ctx, runner, cases, "", caseFilter, tagFilter)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "sila: evaluation failed: %v\n", err)
		os.Exit(1)
	}
	suiteRes.SuiteDir = evalPath

	if report {
		reportPath := filepath.Join(evalPath, "evals", "report.html")
		if evalPath == "." || evalPath == "evals" {
			reportPath = "evals/report.html"
		}
		if err := eval.GenerateHTMLReport(suiteRes, reportPath); err == nil && !jsonOut && !toonOut {
			fmt.Printf("%s Generated interactive visual report at %s\n", ui.Green("✔"), ui.Cyan(reportPath))
		}
		jsonReportPath := filepath.Join(filepath.Dir(reportPath), "aggregate-result.json")
		_ = eval.WriteJSONReport(suiteRes, jsonReportPath)
	}

	if jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(suiteRes)
		if !suiteRes.Passed {
			os.Exit(1)
		}
		return
	}

	if toonOut {
		renderEvalTerminalOutput(ui, suiteRes, ablation || target != "")
		if !suiteRes.Passed {
			os.Exit(1)
		}
		return
	}

	renderEvalTerminalOutput(ui, suiteRes, ablation || target != "")
	if !suiteRes.Passed {
		os.Exit(1)
	}
}

func runEvalSynthesize(ui render.UI, dbPath, project, fromLesson, fromFeedback string, allLessons bool, evalDir string) {
	st, err := store.OpenRO(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila: open knowledge store: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()

	if allLessons {
		lessons, err := st.LessonFacts(project, 50)
		if err != nil || len(lessons) == 0 {
			fmt.Println(ui.Yellow("! No recorded lessons found to synthesize."))
			return
		}
		count := 0
		for _, l := range lessons {
			target, err := eval.SynthesizeFromLesson(l, evalDir)
			if err == nil {
				count++
				fmt.Printf("%s Synthesized test case from lesson at %s\n", ui.Green("✔"), ui.Cyan(target))
			}
		}
		fmt.Printf("Synthesized %d automated regression test cases in %s\n", count, evalDir)
		return
	}

	if fromLesson != "" {
		lessons, err := st.LessonFacts(project, 200)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila: fetch lessons: %v\n", err)
			os.Exit(1)
		}
		var found *store.Fact
		for _, l := range lessons {
			if l.ID == fromLesson || strings.HasPrefix(l.ID, fromLesson) {
				found = &l
				break
			}
		}
		if found == nil {
			fmt.Fprintf(os.Stderr, "sila: lesson with ID %q not found\n", fromLesson)
			os.Exit(1)
		}
		target, err := eval.SynthesizeFromLesson(*found, evalDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila: synthesize failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("%s Synthesized regression test at %s\n", ui.Green("✔"), ui.Cyan(target))
		return
	}

	if fromFeedback != "" {
		fbItems, err := st.ListFeedback(project, "", 100)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila: fetch feedback: %v\n", err)
			os.Exit(1)
		}
		var found *store.Feedback
		for _, fb := range fbItems {
			if fb.ID == fromFeedback || strings.HasPrefix(fb.ID, fromFeedback) {
				found = &fb
				break
			}
		}
		if found == nil {
			fmt.Fprintf(os.Stderr, "sila: feedback with ID %q not found\n", fromFeedback)
			os.Exit(1)
		}
		target, err := eval.SynthesizeFromFeedback(*found, evalDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila: synthesize failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("%s Synthesized regression test from feedback at %s\n", ui.Green("✔"), ui.Cyan(target))
		return
	}

	fmt.Fprintln(os.Stderr, "sila: specify --from-lesson=<id>, --from-feedback=<id>, or --all-lessons")
	os.Exit(1)
}

func renderEvalTerminalOutput(ui render.UI, res *eval.SuiteResult, isAblation bool) {
	passRate := 0.0
	if res.TotalCases > 0 {
		passRate = float64(res.PassedCases) / float64(res.TotalCases) * 100.0
	}

	harnessLabel := res.Harness
	if res.Synthetic {
		harnessLabel += " (Synthetic / Mock)"
	}

	fmt.Printf("\n%s\n", ui.Bold(fmt.Sprintf("sila · eval · %s — Epistemic Evaluation Summary", harnessLabel)))
	fmt.Printf("  %-16s %s\n", ui.Dim("Harness:"), harnessLabel)
	fmt.Printf("  %-16s %d\n", ui.Dim("Total Cases:"), res.TotalCases)
	fmt.Printf("  %-16s %.1f%% (%d passed, %d failed)\n", ui.Dim("Pass Rate:"), passRate, res.PassedCases, res.TotalCases-res.PassedCases)
	fmt.Printf("  %-16s %.1f / %.1f\n", ui.Dim("Total Score:"), res.TotalScore, res.MaxScore)
	fmt.Printf("  %-16s %s\n", ui.Dim("Duration:"), res.Duration.Round(time.Millisecond).String())
	if res.Error != "" {
		fmt.Printf("  %-16s %s\n", ui.Red("Suite Error:"), res.Error)
	}

	if isAblation && len(res.Ablations) > 0 {
		fmt.Printf("\n%s\n", ui.Bold("Context & Rule Ablation Analysis"))
		for _, ab := range res.Ablations {
			deltaColor := ui.Green
			if ab.Delta < 0 {
				deltaColor = ui.Red
			}
			recBadge := ui.Cyan("[" + strings.ToUpper(ab.Recommendation) + "]")
			if ab.Recommendation == "prune" {
				recBadge = ui.Red("[PRUNE - WASTES TOKENS]")
			} else if ab.Recommendation == "keep" {
				recBadge = ui.Green("[KEEP - POSITIVE ROI]")
			}

			fmt.Printf("  • %s (target: %s)\n", ui.Bold(ab.CaseID), ui.Yellow(ab.Target))
			fmt.Printf("    Score: %.1f (with) vs %.1f (without) | Delta: %s | Token Delta: %+d\n",
				ab.ScoreWith, ab.ScoreWithout, deltaColor(fmt.Sprintf("%+.1f%%", ab.Delta*100.0)), ab.TokenDelta)
			fmt.Printf("    Efficacy per 1k tokens: %.2f | Verdict: %s\n", ab.EfficacyPer1k, recBadge)
		}
	}

	fmt.Printf("\n%s\n", ui.Bold("Case Breakdown"))
	for _, c := range res.Cases {
		badge := ui.Green("✔ PASS")
		if !c.Passed {
			badge = ui.Red("✖ FAIL")
		}
		fmt.Printf("  %s %s (Score: %.1f/%.1f, %s)\n", badge, ui.Bold(c.CaseID), c.TotalScore, c.MaxScore, c.Duration.Round(time.Millisecond))
		if c.Error != "" {
			fmt.Printf("      %s %s\n", ui.Red("Error:"), c.Error)
		}
		for _, g := range c.Grades {
			gBadge := ui.Green("✔")
			if g.Unavailable {
				gBadge = ui.Yellow("⊘")
			} else if !g.Passed {
				gBadge = ui.Red("✖")
			}
			typeStr := g.GraderType
			if g.JudgeModel != "" {
				typeStr += fmt.Sprintf(" [%s]", g.JudgeModel)
			}
			fmt.Printf("      %s %s (%s): %.1f/%.1f — %s\n", gBadge, g.GraderName, typeStr, g.Score, g.MaxScore, g.Detail)
		}
	}
	fmt.Println()
}

func parseEvalRecurseFlags(args []string) (k int, mode eval.RecursionMode, spec eval.SurfaceSpec, evalPath string, err error) {
	k = 2
	mode = eval.ModeParallel
	spec = eval.CodexSurface

	for _, a := range args {
		switch {
		case strings.HasPrefix(a, "--k="):
			val, pErr := strconv.Atoi(strings.TrimPrefix(a, "--k="))
			if pErr != nil || val < 1 {
				return 0, "", eval.SurfaceSpec{}, "", fmt.Errorf("invalid --k %q: must be an integer >= 1", strings.TrimPrefix(a, "--k="))
			}
			k = val
		case strings.HasPrefix(a, "--mode="):
			m := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(a, "--mode=")))
			switch m {
			case "parallel", "agg":
				mode = eval.ModeParallel
			case "sequential", "seq":
				mode = eval.ModeSequential
			default:
				return 0, "", eval.SurfaceSpec{}, "", fmt.Errorf("invalid recursion mode %q (valid: parallel, sequential)", m)
			}
		case strings.HasPrefix(a, "--surface="):
			sName := strings.TrimPrefix(a, "--surface=")
			resolved, sErr := eval.ResolveSurfaceSpec(sName)
			if sErr != nil {
				return 0, "", eval.SurfaceSpec{}, "", sErr
			}
			spec = resolved
		case !strings.HasPrefix(a, "-"):
			if evalPath == "" {
				evalPath = a
			}
		}
	}
	return k, mode, spec, evalPath, nil
}

func runEvalRecurse(ui render.UI, dbPath, project, harness, evalDir, caseFilter, tagFilter string, jsonOut, toonOut bool, args []string) {
	k, mode, spec, pathArg, err := parseEvalRecurseFlags(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila: %v\n", err)
		os.Exit(2)
	}
	targetDir := evalDir
	if pathArg != "" {
		targetDir = pathArg
	}
	if targetDir == "" {
		targetDir = "evals"
		if !isDir(targetDir) {
			targetDir = "."
		}
	}

	reg := eval.NewRegistry()
	var runner eval.HarnessRunner
	if harness == "" || harness == "auto" {
		r, rErr := reg.AutoDetect()
		if rErr != nil {
			runner, _ = reg.Get("mock")
		} else {
			runner = r
		}
	} else {
		var ok bool
		runner, ok = reg.Get(harness)
		if !ok {
			fmt.Fprintf(os.Stderr, "sila: unknown harness runner %q\n", harness)
			os.Exit(2)
		}
	}

	cases, err := eval.LoadSuite(targetDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila: failed to load eval suite from %s: %v\n", targetDir, err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	type caseRecurseSummary struct {
		CaseID    string                `json:"case_id"`
		Mode      eval.RecursionMode    `json:"mode"`
		K         int                   `json:"k"`
		Surface   string                `json:"surface"`
		Selection *eval.SelectionResult `json:"selection"`
		Stages    []eval.StageRecord    `json:"stages"`
	}
	summaries := make([]caseRecurseSummary, 0, len(cases))

	for i := range cases {
		c := cases[i]
		if caseFilter != "" && !strings.Contains(strings.ToLower(c.ID), strings.ToLower(caseFilter)) {
			continue
		}
		workDir, wErr := os.MkdirTemp("", "sila-eval-recurse-*")
		if wErr != nil {
			fmt.Fprintf(os.Stderr, "sila: %v\n", wErr)
			os.Exit(1)
		}
		cfg := eval.RecursionConfig{
			Case:     c,
			WorkDir:  workDir,
			K:        k,
			Surface:  spec,
			BaseTree: eval.ComponentTree{spec.AlwaysOn[0]: "# Base Instructions\n"},
			Runner:   runner,
		}
		var recRes *eval.RecursionResult
		if mode == eval.ModeSequential {
			recRes, err = eval.RunSequential(ctx, cfg)
		} else {
			recRes, err = eval.RunParallel(ctx, cfg)
		}
		_ = os.RemoveAll(workDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila: recursion failed for %s: %v\n", c.ID, err)
			os.Exit(1)
		}
		sel, sErr := eval.RunBlindSelection(ctx, eval.BlindSelectionRequest{
			TaskID:     c.ID,
			TaskPrompt: c.Prompt.Body,
			K:          k,
			Mode:       mode,
			Rollouts:   recRes.Rollouts,
		}, nil)
		if sErr != nil {
			fmt.Fprintf(os.Stderr, "sila: selection failed for %s: %v\n", c.ID, sErr)
			os.Exit(1)
		}
		summaries = append(summaries, caseRecurseSummary{
			CaseID:    c.ID,
			Mode:      mode,
			K:         k,
			Surface:   spec.Name,
			Selection: sel,
			Stages:    recRes.Stages,
		})
	}

	if jsonOut || toonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(summaries)
		return
	}

	fmt.Printf("%s Recursive Contrastive Harness Evaluation (mode=%s, k=%d, surface=%s, cases=%d)\n",
		ui.Green("✔"), string(mode), k, spec.Name, len(summaries))
	for _, s := range summaries {
		fmt.Printf("  • %s: chosen=%s (pool=%v, stages=%d)\n",
			ui.Bold(s.CaseID), s.Selection.Chosen, s.Selection.Pool, len(s.Stages))
	}
}
