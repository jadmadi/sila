package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/heal"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/salam"
	"github.com/jadmadi/sila/internal/store"
)

// runHeal brings one repository current with sila in a single command. It
// applies the automatic steps and reports the judgment steps that remain.
// Each agent heals the repository it is working in, so the fleet converges
// through use instead of a central sweep.
func runHeal(ui render.UI, dbPath, project string, dryRun, jsonOut bool, args []string) {
	force := false
	initHere := false
	global := false
	for _, a := range args {
		switch a {
		case "--force", "-f":
			force = true
		case "--init-here":
			initHere = true
		case "--global", "-g":
			global = true
		}
	}
	var st *store.Store
	if dryRun {
		var err error
		st, _, err = store.OpenInspection(dbPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila heal: %v\n", err)
			os.Exit(1)
		}
	} else {
		var err error
		st, err = store.Open(dbPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila: %v\n", err)
			os.Exit(1)
		}
	}
	if st != nil {
		defer st.Close()
	}

	if global {
		runHealGlobal(ui, st, dryRun, jsonOut)
		return
	}

	repoRoot := heal.RepoRootPath("")
	if project != "" {
		if st != nil {
			p, err := st.ResolveProject(project)
			if err != nil {
				fmt.Fprintf(os.Stderr, "sila heal: %v\n", err)
				os.Exit(1)
			}
			repoRoot = p.Path
		}
	}
	if initHere {
		if project != "" {
			fmt.Fprintln(os.Stderr, "sila heal: --init-here initializes the current directory; do not combine it with --project")
			os.Exit(2)
		}
		if dryRun {
			fmt.Fprintln(os.Stderr, "sila heal: --init-here creates .git and cannot run with --dry-run; run it without --dry-run")
			os.Exit(2)
		}
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila heal: cannot resolve the current directory: %v\n", err)
			os.Exit(2)
		}
		if repoAlreadyInitialized(cwd) {
			fmt.Fprintln(os.Stderr, "sila heal: already a repository; running the normal heal plan")
		} else {
			parent := parentRepositoryRoot(cwd)
			if parent == "" {
				fmt.Fprintf(os.Stderr, "sila heal: %s is not inside a parent git repository. Run `git init` here, then `sila heal`\n", cwd)
				os.Exit(2)
			}
			if !gitIgnoredBy(parent, cwd) {
				fmt.Fprintf(os.Stderr, "sila heal: %s is not gitignored by %s; run `sila heal` there instead\n", cwd, parent)
				os.Exit(2)
			}
			if err := initGitRepoHere(cwd); err != nil {
				fmt.Fprintf(os.Stderr, "sila heal: %v\n", err)
				if strings.Contains(strings.ToLower(err.Error()), "permission denied") {
					os.Exit(1)
				}
				os.Exit(2)
			}
		}
		repoRoot = cwd
	} else if !dryRun && !force {
		if cwd, err := os.Getwd(); err == nil {
			if warn := salam.DetectIgnoredSubdirWarning(repoRoot, cwd); warn != "" {
				fmt.Fprintf(os.Stderr, "sila heal: %s (pass --force to heal the parent repo anyway)\n", warn)
				os.Exit(1)
			}
		}
	}

	plan, err := heal.BuildPlan(st, repoRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila heal: %v\n", err)
		os.Exit(1)
	}

	if dryRun {
		if jsonOut {
			emitJSON(plan)
			return
		}
		printHealPlan(ui, plan)
		return
	}

	report := applyHealPlan(ui, st, plan)
	if jsonOut {
		emitJSON(report)
		return
	}
	printHealReport(ui, report)
}

type healedStep struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Applied  int      `json:"applied"`
	Files    []string `json:"files,omitempty"`
	Messages []string `json:"messages,omitempty"`
	Errors   []string `json:"errors,omitempty"`
}

type healReport struct {
	RepoRoot  string       `json:"repo_root"`
	Project   string       `json:"project"`
	Automatic int          `json:"automatic_applied"`
	Judgment  int          `json:"remaining_judgment"`
	Applied   []healedStep `json:"applied"`
	Remaining []heal.Step  `json:"remaining,omitempty"`
	Errors    []string     `json:"errors,omitempty"`
}

func applyHealPlan(ui render.UI, st *store.Store, plan *heal.Plan) *healReport {
	report := &healReport{RepoRoot: plan.RepoRoot, Project: plan.Project}
	for _, step := range plan.Steps {
		if !step.Automatic {
			continue
		}
		hs := healedStep{ID: step.ID, Title: step.Title}
		switch step.ID {
		case "aliases":
			merged, err := heal.ApplyAliases(st, plan)
			if err != nil {
				hs.Errors = append(hs.Errors, err.Error())
			}
			hs.Applied = len(merged)
			hs.Messages = append(hs.Messages, merged...)

		case "provision":
			gitignoreBefore, _ := os.ReadFile(filepath.Join(plan.RepoRoot, ".gitignore"))
			res := provisionOnboardProject(plan.RepoRoot, plan.Project)
			if len(res.RulesUpdated) > 0 || len(res.Hooks) > 0 || res.OpenCodePlugin != "" || res.GoalPackage != "" {
				hs.Applied++
				hs.Messages = append(hs.Messages, fmt.Sprintf("rules=%d hooks=%d mcp=%d plugin=%t goal=%t",
					len(res.RulesUpdated), len(res.Hooks), len(res.MCPConfigs), res.OpenCodePlugin != "", res.GoalPackage != ""))
				for _, p := range res.RulesUpdated {
					if rel, err := filepath.Rel(plan.RepoRoot, p); err == nil && !strings.HasPrefix(rel, "..") {
						hs.Files = append(hs.Files, filepath.ToSlash(rel))
					}
				}
				for _, p := range res.SkillsUpdated {
					if rel, err := filepath.Rel(plan.RepoRoot, p); err == nil && !strings.HasPrefix(rel, "..") {
						hs.Files = append(hs.Files, filepath.ToSlash(rel))
					}
				}
				for _, p := range res.Hooks {
					if rel, err := filepath.Rel(plan.RepoRoot, p); err == nil && !strings.HasPrefix(rel, "..") {
						hs.Files = append(hs.Files, filepath.ToSlash(rel))
					} else if !filepath.IsAbs(p) && !strings.HasPrefix(p, "~") {
						hs.Files = append(hs.Files, filepath.ToSlash(p))
					}
				}
				for _, p := range res.MCPConfigs {
					if !filepath.IsAbs(p) && !strings.HasPrefix(p, "~") {
						hs.Files = append(hs.Files, filepath.ToSlash(p))
					}
				}
				if res.GoalPackage != "" {
					hs.Files = append(hs.Files,
						"goals/sila-onboarding/goal.md",
						"goals/sila-onboarding/plan.md",
						"goals/sila-onboarding/facts.md",
					)
				}
				if gitignoreAfter, err := os.ReadFile(filepath.Join(plan.RepoRoot, ".gitignore")); err == nil && string(gitignoreAfter) != string(gitignoreBefore) {
					hs.Files = append(hs.Files, ".gitignore")
				}
			}
			hs.Errors = append(hs.Errors, res.Errors...)

		case "prune":
			paths, err := goals.PruneClosedClaims(plan.RepoRoot, false)
			if err != nil {
				hs.Errors = append(hs.Errors, err.Error())
				break
			}
			hs.Applied = len(paths)
			if len(paths) > 0 {
				hs.Messages = append(hs.Messages, fmt.Sprintf("removed %d claim %s", len(paths), plural(int64(len(paths)), "record")))
			}

		case "stale":
			released, err := goals.ReleaseStaleClaims(plan.RepoRoot, 72*time.Hour, false)
			if err != nil {
				hs.Errors = append(hs.Errors, err.Error())
				break
			}
			hs.Applied = len(released)
			if len(released) > 0 {
				hs.Messages = append(hs.Messages, fmt.Sprintf("released %d stale %s", len(released), plural(int64(len(released)), "claim")))
			}

		case "gitignore-claims":
			added, err := goals.EnsureGitIgnoreClaim(plan.RepoRoot)
			if err != nil {
				hs.Errors = append(hs.Errors, err.Error())
				break
			}
			if added {
				hs.Applied = 1
				hs.Messages = append(hs.Messages, "added goals/*/claim.json and .sila/state/ to .gitignore")
			}

		case "cfclean":
			applied, err := heal.EnsureCFGitFilter(plan.RepoRoot)
			if err != nil {
				hs.Errors = append(hs.Errors, err.Error())
				break
			}
			if applied {
				hs.Applied = 1
				hs.Messages = append(hs.Messages, "configured cfclean filter in .gitattributes and git config")
			}
		}
		if hs.Applied > 0 || len(hs.Errors) > 0 {
			report.Applied = append(report.Applied, hs)
		}
		if len(hs.Errors) > 0 {
			report.Errors = append(report.Errors, hs.Errors...)
		}
	}

	// Re-plan so the report shows only what still needs judgment; skip when no
	// automatic step mutated state.
	if len(report.Applied) == 0 {
		for _, step := range plan.Steps {
			if !step.Automatic {
				report.Remaining = append(report.Remaining, step)
			}
		}
		report.Judgment = len(report.Remaining)
	} else if after, err := heal.BuildPlan(st, plan.RepoRoot); err == nil {
		for _, step := range after.Steps {
			if !step.Automatic {
				report.Remaining = append(report.Remaining, step)
			}
		}
		report.Judgment = len(report.Remaining)
	}
	report.Automatic = len(report.Applied)
	return report
}

func printHealPlan(ui render.UI, plan *heal.Plan) {
	fmt.Fprint(os.Stdout, render.RenderHeader(ui, render.Header{Command: "heal", Scope: plan.Project, Title: render.DisplayPath(plan.RepoRoot, plan.RepoRoot)}))
	fmt.Fprintln(os.Stdout)

	if plan.Automatic == 0 && plan.Judgment == 0 {
		fmt.Fprintf(os.Stdout, "%s Everything is current. No findings.\n", ui.Green("✔"))
		return
	}

	if plan.Automatic > 0 {
		fmt.Fprintf(os.Stdout, "%s (%d) — sila heal runs these; preview only\n", ui.Bold("Automatic"), plan.Automatic)
		n := 0
		for _, s := range plan.Steps {
			if !s.Automatic {
				continue
			}
			n++
			fmt.Fprintf(os.Stdout, "  %d. %s %s\n", n, ui.Bold(s.Title), ui.Cyan("→ "+s.Command))
			if s.Reason != "" {
				fmt.Fprintf(os.Stdout, "     %s\n", ui.Dim(s.Reason))
			}
			if len(s.Files) > 0 {
				fmt.Fprintf(os.Stdout, "     %s %s\n", ui.Dim("Affected files:"), strings.Join(s.Files, ", "))
			}
		}
		fmt.Fprintln(os.Stdout)
	}

	if plan.Judgment > 0 {
		fmt.Fprintf(os.Stdout, "%s (%d) — sila cannot invent content; an agent or you must supply it\n", ui.Bold("Judgment"), plan.Judgment)
		n := 0
		for _, s := range plan.Steps {
			if s.Automatic {
				continue
			}
			n++
			fmt.Fprintf(os.Stdout, "  %d. %s %s\n", n, ui.Bold(s.Title), ui.Cyan("→ "+s.Command))
			if s.Reason != "" {
				fmt.Fprintf(os.Stdout, "     %s\n", ui.Dim(s.Reason))
			}
		}
		fmt.Fprintln(os.Stdout)
	}
	fmt.Fprintf(os.Stdout, "%s Run %s to apply the automatic steps.\n", ui.Dim("Next:"), ui.Cyan("sila heal"))
}

func printHealReport(ui render.UI, report *healReport) {
	fmt.Fprint(os.Stdout, render.RenderHeader(ui, render.Header{Command: "heal", Scope: report.Project, Title: render.DisplayPath(report.RepoRoot, report.RepoRoot)}))
	fmt.Fprintln(os.Stdout)
	if len(report.Applied) == 0 {
		fmt.Fprintf(os.Stdout, "%s Nothing automatic left to do.\n\n", ui.Green("✔"))
	} else {
		for _, s := range report.Applied {
			status := ui.Green("✔")
			if len(s.Errors) > 0 {
				status = ui.Yellow("▲")
			}
			fmt.Fprintf(os.Stdout, "  %s %s (%d)\n", status, ui.Bold(s.Title), s.Applied)
			if len(s.Files) > 0 {
				fmt.Fprintf(os.Stdout, "     %s\n", ui.Dim("Affected files: "+strings.Join(s.Files, ", ")))
			}
			for _, m := range s.Messages {
				fmt.Fprintf(os.Stdout, "     %s\n", ui.Dim(m))
			}
			for _, e := range s.Errors {
				fmt.Fprintf(os.Stdout, "     %s %s\n", ui.Red("✖"), e)
			}
		}
		fmt.Fprintln(os.Stdout)
	}

	if report.Judgment == 0 {
		fmt.Fprintf(os.Stdout, "%s Sila is current for this repository.\n", ui.Green("✔"))
		return
	}
	fmt.Fprintf(os.Stdout, "%s (%d) still need judgment:\n", ui.Bold("Judgment"), report.Judgment)
	for _, s := range report.Remaining {
		fmt.Fprintf(os.Stdout, "  • %s %s\n", ui.Bold(s.Title), ui.Cyan("→ "+s.Command))
	}
	fmt.Fprintln(os.Stdout)
	fmt.Fprintf(os.Stdout, "%s Re-run %s after finishing them.\n", ui.Dim("Next:"), ui.Cyan("sila heal"))
}

func repoAlreadyInitialized(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

func parentRepositoryRoot(cwd string) string {
	out, err := exec.Command("git", "-C", cwd, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return ""
	}
	root := strings.TrimSpace(string(out))
	if root == "" || filepath.Clean(root) == filepath.Clean(cwd) {
		return ""
	}
	return root
}

func gitIgnoredBy(repoRoot, path string) bool {
	return exec.Command("git", "-C", repoRoot, "check-ignore", "-q", path).Run() == nil
}

// initGitRepoHere creates a repository in place, preferring the main branch.
// Older git versions that lack `init -b` fall back to plain init plus an
// explicit HEAD update. The directory is never staged or added to a parent.
func initGitRepoHere(cwd string) error {
	if repoAlreadyInitialized(cwd) {
		return nil
	}
	cmd := exec.Command("git", "init", "-b", "main")
	cmd.Dir = cwd
	if _, err := cmd.CombinedOutput(); err != nil {
		fallback := exec.Command("git", "init")
		fallback.Dir = cwd
		if out2, err2 := fallback.CombinedOutput(); err2 != nil {
			return fmt.Errorf("git init failed in %s: %s", cwd, strings.TrimSpace(string(out2)))
		}
		_ = exec.Command("git", "-C", cwd, "symbolic-ref", "HEAD", "refs/heads/main").Run()
	}
	verify := exec.Command("git", "-C", cwd, "rev-parse", "--is-inside-work-tree")
	out, err := verify.Output()
	if err != nil || strings.TrimSpace(string(out)) != "true" {
		return fmt.Errorf("git init did not produce a work tree in %s", cwd)
	}
	return nil
}

type globalHealReport struct {
	Global         bool          `json:"global"`
	DryRun         bool          `json:"dry_run"`
	Scanned        int           `json:"scanned"`
	Healed         int           `json:"healed"`
	AlreadyCurrent int           `json:"already_current"`
	WithJudgment   int           `json:"with_judgment"`
	Plans          []*heal.Plan  `json:"plans,omitempty"`
	Reports        []*healReport `json:"reports,omitempty"`
}

func runHealGlobal(ui render.UI, st *store.Store, dryRun, jsonOut bool) {
	if st == nil {
		fmt.Fprintln(os.Stderr, "sila heal: knowledge store unavailable")
		os.Exit(1)
	}
	projects, err := st.Projects()
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila heal: %v\n", err)
		os.Exit(1)
	}
	out := &globalHealReport{
		Global: true,
		DryRun: dryRun,
	}
	seenPaths := make(map[string]bool)
	for _, p := range projects {
		if p.Path == "" || p.ID == "no-project" || store.IsTempDirPath(p.Path) || !store.IsRepoOrWorkspaceDir(p.Path) {
			continue
		}
		canon := store.CanonicalProjectPath(p.Path)
		if store.IsTempDirPath(canon) || !store.IsRepoOrWorkspaceDir(canon) || seenPaths[canon] {
			continue
		}
		seenPaths[canon] = true
		plan, err := heal.BuildPlan(st, canon)
		if err != nil || plan == nil {
			continue
		}
		out.Scanned++
		if dryRun {
			if plan.Automatic > 0 {
				out.Healed++
			} else {
				out.AlreadyCurrent++
			}
			if plan.Judgment > 0 {
				out.WithJudgment++
			}
			out.Plans = append(out.Plans, plan)
			continue
		}
		rep := applyHealPlan(ui, st, plan)
		if rep.Automatic > 0 {
			out.Healed++
		} else {
			out.AlreadyCurrent++
		}
		if rep.Judgment > 0 {
			out.WithJudgment++
		}
		out.Reports = append(out.Reports, rep)
	}
	sort.SliceStable(out.Plans, func(i, j int) bool { return out.Plans[i].Project < out.Plans[j].Project })
	sort.SliceStable(out.Reports, func(i, j int) bool { return out.Reports[i].Project < out.Reports[j].Project })
	if jsonOut {
		emitJSON(out)
		return
	}
	mode := "apply"
	if dryRun {
		mode = "dry-run"
	}
	fmt.Fprint(os.Stdout, render.RenderHeader(ui, render.Header{
		Command: "heal",
		Scope:   fmt.Sprintf("global (%d repositories)", out.Scanned),
		Mode:    mode,
	}))
	fmt.Fprintln(os.Stdout)
	fmt.Fprintf(os.Stdout, "  %s %d scanned · %s %d automatic · %s %d current · %d judgment\n\n",
		ui.Bold("Fleet:"), out.Scanned,
		ui.Green("✔"), out.Healed,
		ui.Dim("•"), out.AlreadyCurrent,
		out.WithJudgment,
	)
	var rows [][]string
	if dryRun {
		for _, pl := range out.Plans {
			if pl.Automatic == 0 && pl.Judgment == 0 {
				continue
			}
			rows = append(rows, []string{
				pl.Project,
				fmt.Sprintf("%d", pl.Automatic),
				fmt.Sprintf("%d", pl.Judgment),
				render.DisplayPath(pl.RepoRoot, pl.RepoRoot),
			})
		}
	} else {
		for _, r := range out.Reports {
			if r.Automatic == 0 && len(r.Errors) == 0 {
				continue
			}
			rows = append(rows, []string{
				r.Project,
				fmt.Sprintf("%d", r.Automatic),
				fmt.Sprintf("%d", r.Judgment),
				render.DisplayPath(r.RepoRoot, r.RepoRoot),
			})
		}
	}
	if len(rows) > 0 {
		fmt.Fprintln(os.Stdout, ui.Table([]string{"project", "auto", "judgment", "path"}, rows, []string{"l", "r", "r", "l"}))
	}
}
