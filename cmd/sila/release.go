package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/jadmadi/sila/internal/release"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
	"github.com/jadmadi/sila/internal/version"
)

// runRelease exposes deterministic release metadata, quality scorecard, and preflight checks.
func runRelease(ui render.UI, jsonOut, toonOut bool, outFile string, allowDirty, offline, strict bool, args []string) {
	cmd := "preflight"
	if len(args) > 0 {
		cmd = args[0]
	}
	path := outFile
	switch cmd {
	case "snapshot":
		if path == "" {
			path = filepath.Join("contracts", "mcp-v1.json")
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			failRelease(err)
		}
		if err := release.WriteContract(path); err != nil {
			failRelease(err)
		}
		fmt.Fprintln(os.Stdout, ui.Green("✔")+" Wrote MCP contract snapshot to "+path)
	case "metadata":
		if path == "" {
			path = "release-metadata.json"
		}
		if err := release.WriteMetadata(path); err != nil {
			failRelease(err)
		}
		fmt.Fprintln(os.Stdout, ui.Green("✔")+" Wrote release metadata to "+path)
	case "check":
		result, err := releaseCheck()
		if err != nil {
			result.Error = err.Error()
		}
		if jsonOut {
			emitJSON(result)
		} else {
			if err != nil {
				fmt.Fprintln(os.Stderr, ui.Red("✖ ")+err.Error())
			} else {
				fmt.Fprintln(os.Stdout, ui.Green("✔ release preflight passed: ")+version.String())
			}
		}
		if err != nil {
			os.Exit(1)
		}
	case "status":
		subArgs := []string{}
		if len(args) > 1 {
			subArgs = args[1:]
		}
		runReleaseStatus(ui, jsonOut, toonOut, allowDirty, offline, subArgs)
	case "preflight", "scorecard":
		runReleasePreflight(ui, jsonOut, toonOut, allowDirty, offline, strict)
	case "export":
		subArgs := []string{}
		if len(args) > 1 {
			subArgs = args[1:]
		}
		runReleaseExport(ui, jsonOut, subArgs)
	default:
		failRelease(fmt.Errorf("unknown release command %q (use status, preflight, export, check, snapshot, or metadata)", cmd))
	}
}

func runReleaseStatus(ui render.UI, jsonOut, toonOut bool, allowDirty, offline bool, args []string) {
	showDiff := false
	for _, arg := range args {
		if arg == "--diff" || arg == "-d" {
			showDiff = true
		}
	}
	opts := release.ReleaseStatusOptions{
		Offline:    offline,
		ShowDiff:   showDiff,
		AllowDirty: allowDirty,
	}
	report, err := release.GetReleaseStatus(opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila release status error: %v\n", err)
		os.Exit(1)
	}

	if jsonOut {
		emitJSON(report)
		return
	}
	if toonOut {
		fmt.Fprint(os.Stdout, release.RenderStatusTOON(report))
		return
	}
	fmt.Fprint(os.Stdout, release.RenderStatusTerminal(ui, report, showDiff))
}

func runReleasePreflight(ui render.UI, jsonOut, toonOut bool, allowDirty, offline, strict bool) {
	opts := release.PreflightOptions{
		AllowDirty: allowDirty,
		Offline:    offline,
		Strict:     strict,
	}

	report, err := release.RunPreflight(opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila release preflight error: %v\n", err)
		os.Exit(1)
	}

	if jsonOut {
		emitJSON(report)
		if !report.Passed {
			os.Exit(1)
		}
		return
	}

	if toonOut {
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("release_scorecard:\n  version: %s\n  score: %.1f\n  grade: %s\n  passed: %t\n  offline_mode: %t\n",
			report.Version, report.Score, report.Grade, report.Passed, report.OfflineMode))
		sb.WriteString("  checks:\n")
		for _, c := range report.Checks {
			sb.WriteString(fmt.Sprintf("    - name: %s\n      status: %s\n      detail: %s\n", c.Name, c.Status, c.Detail))
		}
		fmt.Fprint(os.Stdout, sb.String())
		if !report.Passed {
			os.Exit(1)
		}
		return
	}

	fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · release preflight"))+" — Software Quality Scorecard & Release Matrix")
	fmt.Fprintln(os.Stdout)

	headers := []string{"Category", "Check", "Status", "Evaluation Details"}
	var rows [][]string
	for _, c := range report.Checks {
		var statusStr string
		switch c.Status {
		case release.StatusPass:
			statusStr = ui.Green("✔ PASS")
		case release.StatusWarn:
			statusStr = ui.Yellow("▲ WARN")
		case release.StatusFail:
			statusStr = ui.Red("✖ FAIL")
		case release.StatusSkipped:
			statusStr = ui.Dim("○ SKIPPED")
		}
		rows = append(rows, []string{c.Category, c.Name, statusStr, c.Detail})
	}
	fmt.Fprint(os.Stdout, ui.Table(headers, rows, []string{"l", "l", "l", "l"}))
	fmt.Fprintln(os.Stdout)

	gradeBadge := fmt.Sprintf("[%s]", report.Grade)
	if report.Grade == "A+" || report.Grade == "A" {
		gradeBadge = ui.Green(ui.Bold(gradeBadge))
	} else if report.Grade == "B" {
		gradeBadge = ui.Yellow(ui.Bold(gradeBadge))
	} else {
		gradeBadge = ui.Red(ui.Bold(gradeBadge))
	}

	if report.Passed {
		fmt.Fprintf(os.Stdout, "✔ Release Quality Scorecard passed: %s Score: %.1f%% %s\n",
			gradeBadge, report.Score, ui.Dim("("+report.Summary+")"))
	} else {
		var failing []string
		for _, c := range report.Checks {
			if c.Status == release.StatusFail {
				failing = append(failing, c.Name+": "+strings.TrimSpace(c.Detail))
			}
		}
		fmt.Fprintf(os.Stderr, "✖ Release Quality Scorecard failed: %s Score: %.1f%% %s failing: %s\n",
			gradeBadge, report.Score, ui.Dim("("+report.Summary+")"), strings.Join(failing, "; "))
		os.Exit(1)
	}
}

type releaseCheckResult struct {
	Version         string   `json:"version"`
	ContractChanges []string `json:"contract_changes"`
	SchemaVersion   int      `json:"schema_version"`
	Ready           bool     `json:"ready"`
	Error           string   `json:"error,omitempty"`
}

func releaseCheck() (releaseCheckResult, error) {
	result := releaseCheckResult{Version: version.Version, SchemaVersion: store.CurrentSchemaVersion()}
	if !version.IsRelease(version.Version) {
		return result, fmt.Errorf("release preflight requires an exact SemVer tag; current build is %q", version.Version)
	}
	if out, err := exec.Command("git", "describe", "--tags", "--exact-match", "HEAD").Output(); err != nil || strings.TrimSpace(string(out)) != version.Version {
		return result, fmt.Errorf("HEAD must be tagged %s", version.Version)
	}
	if out, err := exec.Command("git", "cat-file", "-t", version.Version).Output(); err != nil || strings.TrimSpace(string(out)) != "tag" {
		return result, fmt.Errorf("release preflight requires %s to be an annotated tag", version.Version)
	}
	if out, err := exec.Command("git", "status", "--porcelain", "--untracked-files=no").Output(); err != nil || strings.TrimSpace(string(out)) != "" {
		return result, fmt.Errorf("release preflight requires a clean worktree")
	}
	changelog, err := os.ReadFile("CHANGELOG.md")
	if err != nil {
		return result, err
	}
	if !strings.Contains(string(changelog), "## ["+strings.TrimPrefix(version.Version, "v")+"]") {
		return result, fmt.Errorf("CHANGELOG.md has no section for %s", version.Version)
	}
	previous, err := release.ReadContract(filepath.Join("contracts", "mcp-v1.json"))
	if err != nil {
		return result, fmt.Errorf("read MCP contract baseline: %w", err)
	}
	result.ContractChanges = release.BreakingChanges(previous, release.Contract())
	if len(result.ContractChanges) > 0 {
		return result, fmt.Errorf("breaking MCP contract changes: %s", strings.Join(result.ContractChanges, "; "))
	}
	if os.Getenv("SILA_SKIP_SOAK") != "1" {
		soak := release.EvaluateSoak(release.SoakOptions{})
		if soak.State == release.SoakNeedsDebug || soak.State == release.SoakInProgress {
			return result, fmt.Errorf("3-day local soak incomplete (%s): %s", soak.State, soak.Summary)
		}
	}
	result.Ready = true
	return result, nil
}

func failRelease(err error) { fmt.Fprintln(os.Stderr, "sila release:", err); os.Exit(2) }

func runReleaseExport(ui render.UI, jsonOut bool, args []string) {
	dryRun := false
	destDir := ""
	refOverride := ""
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--dry-run":
			dryRun = true
		case strings.HasPrefix(arg, "--dest="):
			destDir = strings.TrimSpace(strings.TrimPrefix(arg, "--dest="))
		case arg == "--dest" && i+1 < len(args):
			i++
			destDir = strings.TrimSpace(args[i])
		case strings.HasPrefix(arg, "--ref="):
			refOverride = strings.TrimSpace(strings.TrimPrefix(arg, "--ref="))
		case arg == "--ref" && i+1 < len(args):
			i++
			refOverride = strings.TrimSpace(args[i])
		}
	}

	repoDir := release.ResolveGitRepoDir("")
	if destDir == "" {
		destDir = filepath.Join(filepath.Dir(repoDir), "public-export")
	}

	soak := release.EvaluateSoak(release.SoakOptions{
		RepoDir: repoDir,
		DBPath:  store.DefaultDBPath(),
	})
	targetRef := refOverride
	if targetRef == "" {
		if soak.State != release.SoakReady || soak.ReadyThroughCommit == "" {
			failRelease(fmt.Errorf("no soaked feature train ready for public export (state=%s: %s)", soak.State, soak.Summary))
		}
		targetRef = soak.ReadyThroughCommit
	}

	exportArgs := []string{"run", "./tools/exportpublic", "--source", repoDir, "--dest", destDir, "--ref", targetRef, "--force"}
	if dryRun {
		exportArgs = append(exportArgs, "--dry-run")
	}
	cmd := exec.Command("go", exportArgs...)
	cmd.Dir = repoDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		failRelease(fmt.Errorf("exportpublic --ref %s failed: %v\n%s", targetRef, err, strings.TrimSpace(string(out))))
	}

	if jsonOut {
		emitJSON(map[string]any{
			"status":               "exported",
			"dry_run":              dryRun,
			"ready_through_commit": targetRef,
			"ready_features":       soak.ReadyFeatures,
			"soaking_features":     soak.SoakingFeatures,
			"dest":                 destDir,
			"output":               strings.TrimSpace(string(out)),
		})
		return
	}
	modeStr := "Exported"
	if dryRun {
		modeStr = "Dry-run verified"
	}
	fmt.Fprintf(os.Stdout, "%s %s ready feature train (%s, %d ready, %d soaking on own 72h timer) -> %s\n  %s\n",
		ui.Green("✔"), modeStr, targetRef, soak.ReadyFeatures, soak.SoakingFeatures, destDir, strings.TrimSpace(string(out)))
}
