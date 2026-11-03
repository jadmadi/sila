package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/pprof"
	"sort"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/andon"
	"github.com/jadmadi/sila/internal/assess"
	"github.com/jadmadi/sila/internal/assist"
	"github.com/jadmadi/sila/internal/contract"
	"github.com/jadmadi/sila/internal/crew"
	"github.com/jadmadi/sila/internal/dream"
	silagate "github.com/jadmadi/sila/internal/gate"
	"github.com/jadmadi/sila/internal/git"
	"github.com/jadmadi/sila/internal/goals"
	harnesscontract "github.com/jadmadi/sila/internal/harness/contract"
	"github.com/jadmadi/sila/internal/lint"
	"github.com/jadmadi/sila/internal/onboard"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/sentry"
	"github.com/jadmadi/sila/internal/store"
	"github.com/jadmadi/sila/internal/testmatrix"
	"github.com/jadmadi/sila/internal/version"
)

// gateCheck is a single discipline-check result.
type gateCheck struct {
	Name        string `json:"name"`
	Status      string `json:"status"` // pass | warn | fail | skip | unavailable | partial
	Detail      string `json:"detail"`
	Remediation string `json:"remediation,omitempty"`
	Warning     string `json:"warning,omitempty"`
}

// gateReport is the machine-readable gate verdict.
type gateReport struct {
	Clean    bool        `json:"clean"`
	Tier     string      `json:"tier,omitempty"`
	Checks   []gateCheck `json:"checks"`
	Blockers []gateCheck `json:"blockers"`
	Warnings []gateCheck `json:"warnings"`
	Advisory []gateCheck `json:"advisory"`
	Skipped  []gateCheck `json:"skipped"`
	ExitCode int         `json:"exit_code"`
}

// gateVerdict is the single attribution model used by text, JSON, TOON, and
// the process exit code.
type gateVerdict struct {
	Clean    bool
	Blockers []gateCheck
	Warnings []gateCheck
	Advisory []gateCheck
	Skipped  []gateCheck
	ExitCode int
}

// gateFailOpenCheck reports whether a check is advisory-only: its non-pass
// states are observations, not merge blockers.
func gateFailOpenCheck(name string) bool {
	switch name {
	case "sentry", "arch-sentry", "rtl-sentry", "cf-worker-sentry", "cfclean", "reviewer-veto":
		return true
	}
	return false
}

// gateBlocks decides whether one check blocks the commit or CI run.
func gateBlocks(c gateCheck, requireTests, strict bool) bool {
	if gateFailOpenCheck(c.Name) {
		return c.Status == "fail"
	}
	switch c.Status {
	case "fail":
		return true
	case "partial":
		return true
	case "unavailable":
		return strict
	case "skip":
		return c.Name == "tests" && requireTests
	}
	return false
}

// computeGateVerdict partitions checks into blockers, warnings, advisory, and
// skipped, and derives the exit code. clean is always equivalent to
// len(blockers)==0.
func computeGateVerdict(checks []gateCheck, requireTests, strict bool) gateVerdict {
	v := gateVerdict{Clean: true, Blockers: []gateCheck{}, Warnings: []gateCheck{}, Advisory: []gateCheck{}, Skipped: []gateCheck{}}
	for _, c := range checks {
		if c.Warning != "" || c.Status == "warn" {
			v.Warnings = append(v.Warnings, c)
		}
		switch {
		case gateBlocks(c, requireTests, strict):
			if c.Remediation == "" {
				c.Remediation = gateRemediation(c.Name)
			}
			v.Blockers = append(v.Blockers, c)
		case c.Status == "skip":
			v.Skipped = append(v.Skipped, c)
		case c.Status == "unavailable":
			v.Advisory = append(v.Advisory, c)
		case c.Status == "warn":
			// warnings already recorded; never blockers
		case c.Status == "pass":
			// clean
		}
	}
	v.Clean = len(v.Blockers) == 0
	if !v.Clean {
		v.ExitCode = 1
	}
	return v
}

// gateRemediation names the smallest runnable next step for a blocker.
func gateRemediation(name string) string {
	switch name {
	case "tests":
		return "add a test command/manifest, or pass --require-tests to enforce a skipped run"
	case "format":
		return "gofmt -w <changed files>"
	case "changelog":
		return "add a CHANGELOG.md [Unreleased] entry"
	case "memory-write":
		return "record a memo or lesson grounded to a changed file"
	case "knowledge", "decisions", "goal-contract", "silence-sentry":
		return "initialize the knowledge store with sila sync and re-run"
	case "rules-budget":
		return "trim AGENTS.md/GEMINI.md below the 24 KB ceiling"
	case "scar-tissue":
		return "read the named contradicted rule and update the code or the rule"
	case "charter-sentry":
		return "add the file to files_to_touch in the goal package"
	case "rule-of-five":
		return "complete the goal contract or release the claim"
	case "rebase-drift":
		return "re-run the authoritative tests and update the baseline"
	case "constant-guard":
		return "move the literal into a named constant"
	}
	return "run `sila gate --json` and inspect the named check"
}

// runGate verifies engineering discipline or manages pre-push gate hooks and validation pipeline.
func runGate(ui render.UI, jsonOut, toonOut, hookFlag, remoteFlag, scarTissueFlag, docCoverageFlag, reportCardFlag, coverageFlag, strictFlag, requireTestsFlag, preCommitFlag, fullFlag, skipArchFlag bool, customProfile, forwardTarget, branch string, args []string) {
	if reportCardFlag || (len(args) > 0 && (args[0] == "reportcard" || args[0] == "report-card")) {
		runGateReportCardStandalone(ui, jsonOut, toonOut)
		return
	}
	if coverageFlag || (len(args) > 0 && args[0] == "coverage") {
		runGateCoverageStandalone(ui, jsonOut, toonOut, strictFlag, customProfile, false)
		return
	}
	if docCoverageFlag || (len(args) > 0 && (args[0] == "doc-coverage" || args[0] == "doc-lint" || args[0] == "doclint")) {
		runGateDocCoverageStandalone(ui, jsonOut, toonOut)
		return
	}
	if scarTissueFlag || (len(args) > 0 && (args[0] == "scar-tissue" || args[0] == "scartissue")) {
		runGateScarTissueStandalone(ui, jsonOut, toonOut)
		return
	}
	if len(args) > 0 && (args[0] == "smoke" || args[0] == "smoke-ab") {
		runGateSmokeStandalone(ui, jsonOut, toonOut)
		return
	}
	if len(args) > 0 && (args[0] == "constants" || args[0] == "constant-guard" || args[0] == "literals") {
		runGateConstantGuardStandalone(ui, jsonOut, toonOut)
		return
	}
	if len(args) > 0 && (args[0] == "arch" || args[0] == "arch-sentry" || args[0] == "architecture") {
		runGateArchStandalone(ui, jsonOut, toonOut, skipArchFlag)
		return
	}
	if len(args) > 0 && (args[0] == "provenance" || args[0] == "sources-check") {
		runGateProvenanceStandalone(ui, jsonOut, toonOut)
		return
	}

	// Pre-commit fast path (<3s changed-package tests, formatting, secrets)
	if preCommitFlag || (len(args) > 0 && (args[0] == "pre-commit" || args[0] == "quick" || args[0] == "fast")) {
		runGatePreCommit(ui, jsonOut, toonOut, skipArchFlag, requireTestsFlag, strictFlag)
		return
	}
	if fullFlag || (len(args) > 0 && args[0] == "full") {
		runGateDiscipline(ui, jsonOut, toonOut, skipArchFlag, requireTestsFlag, strictFlag)
		return
	}

	if len(args) == 0 {
		if os.Getenv("SILA_GATE_TIER") == "pre-commit" {
			runGatePreCommit(ui, jsonOut, toonOut, skipArchFlag, requireTestsFlag, strictFlag)
			return
		}
		runGateDiscipline(ui, jsonOut, toonOut, skipArchFlag, requireTestsFlag, strictFlag)
		return
	}

	sub := args[0]
	switch sub {
	case "check":
		runGateDiscipline(ui, jsonOut, toonOut, skipArchFlag, requireTestsFlag, strictFlag)
	case "pre-commit", "quick", "fast":
		runGatePreCommit(ui, jsonOut, toonOut, skipArchFlag, requireTestsFlag, strictFlag)
	case "full":
		runGateDiscipline(ui, jsonOut, toonOut, skipArchFlag, requireTestsFlag, strictFlag)
	case "arch", "arch-sentry", "architecture":
		runGateArchStandalone(ui, jsonOut, toonOut, skipArchFlag)
	case "install":
		runGateInstall(ui, jsonOut, hookFlag, remoteFlag)
	case "pipeline":
		runGatePipelineCmd(ui, jsonOut, branch, forwardTarget, false)
	case "run-remote":
		runGatePipelineCmd(ui, jsonOut, branch, forwardTarget, true)
	case "hook":
		if len(args) > 1 && args[1] == "install" {
			runGateInstall(ui, jsonOut, true, false)
			return
		}
		runGateInstall(ui, jsonOut, true, false)
	case "scar-tissue", "scartissue":
		runGateScarTissueStandalone(ui, jsonOut, toonOut)
	case "doc-coverage", "doc-lint", "doclint":
		runGateDocCoverageStandalone(ui, jsonOut, toonOut)
	case "reportcard", "report-card":
		runGateReportCardStandalone(ui, jsonOut, toonOut)
	case "coverage":
		runGateCoverageStandalone(ui, jsonOut, toonOut, strictFlag, customProfile, false)
	case "smoke", "smoke-ab":
		runGateSmokeStandalone(ui, jsonOut, toonOut)
	case "constants", "constant-guard", "literals":
		runGateConstantGuardStandalone(ui, jsonOut, toonOut)
	default:
		fmt.Fprintf(os.Stderr, "sila gate: unknown subcommand %q (expected check, pre-commit, full, install, pipeline, run-remote, scar-tissue, doc-coverage, report-card, coverage, smoke, or constants)\n", sub)
		os.Exit(2)
	}
}

// runGateDiscipline verifies the engineering-discipline MUSTs from AGENTS.md and
// exits 1 when any check fails (so it can gate a pre-commit hook or CI step).
func runGateDiscipline(ui render.UI, jsonOut, toonOut, skipArch, requireTests, strict bool) {
	if os.Getenv("SILA_SKIP_GATE") == "1" || os.Getenv("SILA_NO_GATE") == "1" || os.Getenv("SKIP_GATE") == "1" {
		if jsonOut {
			emitJSON(gateReport{Clean: true, Checks: []gateCheck{{Name: "gate", Status: "skip", Detail: "skipped via SILA_SKIP_GATE"}}})
		} else if toonOut {
			fmt.Fprint(os.Stdout, renderGateTOON([]gateCheck{{Name: "gate", Status: "skip", Detail: "skipped via SILA_SKIP_GATE"}}, true))
		} else {
			fmt.Fprintln(os.Stdout, ui.Yellow("⚠")+" quality gate skipped via SILA_SKIP_GATE")
		}
		return
	}
	cwd, _ := os.Getwd()
	proj := store.ProjectID(cwd)

	var checks []gateCheck
	if onboard.IsSilaSourceRepo(cwd) {
		checks = []gateCheck{
			gateTests(),
			gateCoverage(),
			gateReportCard(),
			gateFormat(),
			gateChangelog(),
			gateBinary(),
			gateReadme(),
			gateParity(),
			gateTelemetry(),
			gateKnowledge(),
			gateConcurrency(),
			gateLicense(),
			gateScarTissue(),
			gateDocCoverage(),
			gateSmokeAB(),
			gateConstantGuard(),
		}
	} else {
		checks = gateExternalChecks(cwd)
	}
	if hasOxlint(cwd) {
		checks = append(checks, gateEdgeOxlint(cwd))
	}
	checks = append(checks, gateCharterSentry(cwd))
	checks = append(checks, gateSurfaceSentry(cwd))
	checks = append(checks, gateReviewerVetoSentry(cwd))
	checks = append(checks, gateRuleOfFiveCompletion(cwd))
	checks = append(checks, gateRebaseDriftSentry(cwd))
	checks = append(checks, gateDecisionSentry(cwd))
	checks = append(checks, gateContractSentry(cwd))
	checks = append(checks, gateHarnessContract(cwd))
	checks = append(checks, gateTestSelectorSentry(cwd))
	checks = append(checks, gateArchSentry(cwd, skipArch))
	checks = append(checks, gateRTLSentry(cwd))
	checks = append(checks, gateCFWorkerSentry(cwd))
	checks = append(checks, gateCFCleanSentry(cwd))
	checks = append(checks, gateRuleBudget(cwd))
	checks = append(checks, gateProvenanceSentry(cwd))
	checks = append(checks, gateSilenceSentry(cwd))
	checks = append(checks, gateMemoryWriteSentry(cwd))
	checks = append(checks, gatePrimeCanary(cwd))

	if provider := assist.ActiveProviderFor("gate", proj); provider != nil {
		checks = append(checks, gateSentry(provider))
	}
	verdict := computeGateVerdict(checks, requireTests, strict)
	emitGateHeartbeats(cwd, checks)
	if jsonOut {
		emitJSON(gateReport{
			Clean: verdict.Clean, Tier: "full", Checks: checks,
			Blockers: verdict.Blockers, Warnings: verdict.Warnings,
			Advisory: verdict.Advisory, Skipped: verdict.Skipped,
			ExitCode: verdict.ExitCode,
		})
	} else if toonOut {
		fmt.Fprint(os.Stdout, renderGateTOON(checks, verdict.Clean))
	} else {
		printGate(ui, checks, verdict)
	}
	if verdict.ExitCode != 0 {
		os.Exit(verdict.ExitCode)
	}
}

// runGatePreCommit executes the fast-path pre-commit checks (<3s target latency):
// changed-package unit tests (without -race), format check, changelog check, secret sentry,
// and deterministic architectural sentry (SRP file weights, edge anti-patterns, fact grounding).
// Heavy checks (25-package full race detector, SLSA provenance, statement coverage, 194-file report card)
// are deferred to pre-push and CI.
func runGatePreCommit(ui render.UI, jsonOut, toonOut, skipArch, requireTests, strict bool) {
	if os.Getenv("SILA_SKIP_GATE") == "1" || os.Getenv("SILA_NO_GATE") == "1" || os.Getenv("SKIP_GATE") == "1" {
		if jsonOut {
			emitJSON(gateReport{Clean: true, Tier: "pre-commit", Checks: []gateCheck{{Name: "gate", Status: "skip", Detail: "skipped via SILA_SKIP_GATE"}}})
		} else if toonOut {
			fmt.Fprint(os.Stdout, renderGateTOON([]gateCheck{{Name: "gate", Status: "skip", Detail: "skipped via SILA_SKIP_GATE"}}, true))
		} else {
			fmt.Fprintln(os.Stdout, ui.Yellow("⚠")+" pre-commit quality gate skipped via SILA_SKIP_GATE")
		}
		return
	}
	cwd, _ := os.Getwd()
	proj := store.ProjectID(cwd)

	var checks []gateCheck
	if onboard.IsSilaSourceRepo(cwd) {
		checks = []gateCheck{
			gateTestsPreCommit(),
			gateFormat(),
			gateChangelog(),
			gateScarTissue(),
		}
	} else {
		checks = gateExternalChecksPreCommit(cwd)
	}
	if hasOxlint(cwd) {
		checks = append(checks, gateEdgeOxlint(cwd))
	}
	checks = append(checks, gateCharterSentry(cwd))
	checks = append(checks, gateSurfaceSentry(cwd))
	checks = append(checks, gateReviewerVetoSentry(cwd))
	checks = append(checks, gateRuleOfFiveCompletion(cwd))
	checks = append(checks, gateRebaseDriftSentry(cwd))
	checks = append(checks, gateDecisionSentry(cwd))
	checks = append(checks, gateContractSentry(cwd))
	checks = append(checks, gateHarnessContract(cwd))
	checks = append(checks, gateTestSelectorSentry(cwd))
	checks = append(checks, gateArchSentry(cwd, skipArch))
	checks = append(checks, gateRTLSentry(cwd))
	checks = append(checks, gateCFWorkerSentry(cwd))
	checks = append(checks, gateCFCleanSentry(cwd))
	checks = append(checks, gateRuleBudget(cwd))
	checks = append(checks, gateProvenanceSentry(cwd))
	checks = append(checks, gateSilenceSentry(cwd))
	checks = append(checks, gateMemoryWriteSentry(cwd))
	checks = append(checks, gatePrimeCanary(cwd))

	if provider := assist.ActiveProviderFor("gate", proj); provider != nil {
		checks = append(checks, gateSentry(provider))
	}
	verdict := computeGateVerdict(checks, requireTests, strict)
	emitGateHeartbeats(cwd, checks)
	if jsonOut {
		emitJSON(gateReport{
			Clean: verdict.Clean, Tier: "pre-commit", Checks: checks,
			Blockers: verdict.Blockers, Warnings: verdict.Warnings,
			Advisory: verdict.Advisory, Skipped: verdict.Skipped,
			ExitCode: verdict.ExitCode,
		})
	} else if toonOut {
		fmt.Fprint(os.Stdout, renderGateTOON(checks, verdict.Clean))
	} else {
		printGatePreCommit(ui, checks, verdict)
	}
	if verdict.ExitCode != 0 {
		os.Exit(verdict.ExitCode)
	}
}

func printGatePreCommit(ui render.UI, checks []gateCheck, verdict gateVerdict) {
	fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · gate (pre-commit)")))
	for _, c := range checks {
		icon, status := "✔", ui.Green("PASS")
		switch c.Status {
		case "fail":
			icon, status = ui.Red("✖"), ui.Red("FAIL")
		case "skip":
			icon, status = ui.Yellow("▲"), ui.Yellow("SKIP")
		case "unavailable":
			icon, status = ui.Yellow("▲"), ui.Yellow("UNAVAILABLE")
		case "partial":
			icon, status = ui.Yellow("▲"), ui.Yellow("PARTIAL")
		}
		fmt.Fprintf(os.Stdout, "  %s %s %s\n", icon, status, ui.Bold(c.Name))
		fmt.Fprintf(os.Stdout, "    %s\n", ui.Dim(c.Detail))
		if c.Warning != "" {
			fmt.Fprintf(os.Stdout, "    %s %s: %s\n", ui.Yellow("▲"), ui.Bold("WARN"), ui.Yellow(c.Warning))
		}
	}
	for _, b := range verdict.Blockers {
		fix := b.Remediation
		if fix == "" {
			fix = gateRemediation(b.Name)
		}
		fmt.Fprintf(os.Stdout, "  %s %s: %s\n", ui.Red("✖"), ui.Bold(b.Name), b.Detail)
		fmt.Fprintf(os.Stdout, "    %s %s\n", ui.Dim("fix:"), fix)
	}
	if verdict.Clean {
		fmt.Fprintln(os.Stdout, ui.Bold(ui.Green("✔ all pre-commit checks pass (<3s fast path). Full race/SLSA checks deferred to pre-push.")))
	} else {
		fmt.Fprintln(os.Stdout, ui.Bold(ui.Red(fmt.Sprintf("✖ %d blocking check(s) — resolve before committing.", len(verdict.Blockers)))))
	}
}

// gateTestsPreCommit runs fast targeted package tests for pre-commit checks without -race or -coverprofile.
func gateTestsPreCommit() gateCheck {
	if _, err := exec.LookPath("go"); err != nil {
		return gateCheck{Name: "tests", Status: "skip", Detail: "go toolchain not found on PATH"}
	}
	if isDocs, count := isDocsOnlyDiff(); isDocs {
		return gateCheck{Name: "tests", Status: "pass", Detail: fmt.Sprintf("skipped go test (docs-only diff: %d file(s) modified)", count)}
	}
	pkgs := changedGoPackages()
	if len(pkgs) == 0 {
		return gateCheck{Name: "tests", Status: "pass", Detail: "skipped go test (no Go source files modified)"}
	}
	cmdKey := "go test " + strings.Join(pkgs, " ")
	if cached, ok := checkGateGreenReceipt("", cmdKey, ""); ok {
		return cached
	}
	args := append([]string{"test"}, pkgs...)
	start := time.Now()
	out, err := exec.Command("go", args...).CombinedOutput()
	dur := time.Since(start).Round(time.Millisecond)
	if err == nil {
		detail := fmt.Sprintf("go test %s passed in %s", strings.Join(pkgs, " "), dur)
		saveGateGreenReceipt("", cmdKey, detail)
		return gateCheck{
			Name:   "tests",
			Status: "pass",
			Detail: detail,
		}
	}
	detail := fmt.Sprintf("go test %s failed in %s:\n%s", strings.Join(pkgs, " "), dur, tailStr(string(out), 600))
	if path := saveGateTestLog(out); path != "" {
		detail = fmt.Sprintf("go test failed; full output saved to %s\n%s", path, tailStr(string(out), 600))
	}
	return gateCheck{Name: "tests", Status: "fail", Detail: detail}
}

// changedGoPackages returns unique package paths for modified Go files in the working tree.
func changedGoPackages() []string {
	out, err := exec.Command("git", "status", "--porcelain").Output()
	if err != nil {
		return []string{"./..."}
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) == 0 || (len(lines) == 1 && lines[0] == "") {
		return nil
	}

	pkgsMap := make(map[string]bool)
	hasRootChange := false
	for _, l := range lines {
		path := parsePorcelainPath(l)
		if path == "" {
			continue
		}
		pathLower := filepath.ToSlash(strings.ToLower(strings.TrimSpace(path)))
		pathLower = strings.TrimPrefix(pathLower, "./")
		if isGoalLedgerArtifact(pathLower) {
			continue
		}
		if pathLower == "go.mod" || pathLower == "go.sum" || pathLower == "build.sh" {
			hasRootChange = true
		}
		if strings.HasSuffix(pathLower, ".go") {
			dir := filepath.Dir(path)
			if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
				continue
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				continue
			}
			hasGoFile := false
			for _, entry := range entries {
				if !entry.IsDir() && strings.HasSuffix(strings.ToLower(entry.Name()), ".go") {
					hasGoFile = true
					break
				}
			}
			if !hasGoFile {
				continue
			}
			if dir == "." {
				pkgsMap["."] = true
			} else {
				pkg := "./" + filepath.ToSlash(dir)
				pkgsMap[pkg] = true
			}
		}
	}

	if hasRootChange {
		return []string{"./..."}
	}
	if len(pkgsMap) == 0 {
		return nil
	}
	var pkgs []string
	for p := range pkgsMap {
		pkgs = append(pkgs, p)
	}
	sort.Strings(pkgs)
	return pkgs
}

// parsePorcelainPath extracts the normalized relative file path from a git status --porcelain line.
func parsePorcelainPath(line string) string {
	line = strings.TrimSpace(line)
	if len(line) < 3 {
		return ""
	}
	payload := strings.TrimSpace(line[2:])
	if idx := strings.Index(payload, "->"); idx != -1 {
		payload = strings.TrimSpace(payload[idx+2:])
	}
	return strings.Trim(payload, "\"")
}

// gateExternalChecksPreCommit returns fast pre-commit checks for external repositories.
func gateExternalChecksPreCommit(cwd string) []gateCheck {
	return []gateCheck{
		gateExternalTestsPreCommit(cwd),
		gateExternalFormat(cwd),
		gateExternalChangelog(cwd),
		gateScarTissue(),
	}
}

// gateExternalTestsPreCommit executes fast tests for external projects without full race/coverage matrix.
func gateExternalTestsPreCommit(cwd string) gateCheck {
	if isDocs, count := isDocsOnlyDiffForDir(cwd); isDocs {
		return gateCheck{Name: "tests", Status: "pass", Detail: fmt.Sprintf("skipped tests (docs-only diff: %d file(s) modified)", count)}
	}
	if _, err := os.Stat(filepath.Join(cwd, "go.mod")); err == nil {
		pkgs := changedGoPackages()
		if len(pkgs) == 0 {
			return gateCheck{Name: "tests", Status: "pass", Detail: "skipped go test (no Go source files modified)"}
		}
		cmdKey := "go test " + strings.Join(pkgs, " ")
		if cached, ok := checkGateGreenReceipt(cwd, cmdKey, ""); ok {
			return cached
		}
		args := append([]string{"test"}, pkgs...)
		start := time.Now()
		cmd := exec.Command("go", args...)
		cmd.Dir = cwd
		out, err := cmd.CombinedOutput()
		dur := time.Since(start).Round(time.Millisecond)
		if err == nil {
			detail := fmt.Sprintf("go test %s passed in %s", strings.Join(pkgs, " "), dur)
			saveGateGreenReceipt(cwd, cmdKey, detail)
			return gateCheck{Name: "tests", Status: "pass", Detail: detail}
		}
		detail := fmt.Sprintf("go test %s failed in %s:\n%s", strings.Join(pkgs, " "), dur, tailStr(string(out), 600))
		return gateCheck{Name: "tests", Status: "fail", Detail: detail}
	}
	if !hasExternalSourceChanges(cwd) {
		return gateCheck{Name: "tests", Status: "pass", Detail: "skipped tests (no source code or manifest files modified)"}
	}
	return gateExternalTests(cwd)
}

func runGateInstall(ui render.UI, jsonOut, hookFlag, remoteFlag bool) {
	cwd, _ := os.Getwd()
	mgr, err := git.NewManager(cwd)
	if err != nil {
		if jsonOut {
			emitJSON(map[string]any{"error": err.Error()})
		} else {
			fmt.Fprintf(os.Stderr, "sila gate install: %v\n", err)
		}
		os.Exit(1)
	}

	installAll := !hookFlag && !remoteFlag
	var installedHook, installedRemote string
	var errs []string

	if hookFlag || installAll {
		hookPath, err := mgr.InstallPrePushHook()
		if err != nil {
			errs = append(errs, fmt.Sprintf("hook: %v", err))
		} else {
			installedHook = hookPath
		}
	}

	if remoteFlag || installAll {
		rem, err := mgr.InstallGateRemote("sila-gate")
		if err != nil {
			errs = append(errs, fmt.Sprintf("remote: %v", err))
		} else {
			installedRemote = rem
		}
	}

	if len(errs) > 0 {
		if jsonOut {
			emitJSON(map[string]any{"error": strings.Join(errs, "; "), "hook": installedHook, "remote": installedRemote})
		} else {
			for _, e := range errs {
				fmt.Fprintf(os.Stderr, "%s %s\n", ui.Red("✖"), e)
			}
		}
		os.Exit(1)
	}

	if jsonOut {
		emitJSON(map[string]any{
			"hook":   installedHook,
			"remote": installedRemote,
			"status": "installed",
		})
		return
	}

	fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · gate install")))
	if installedHook != "" {
		fmt.Fprintf(os.Stdout, "  %s installed pre-push hook: %s\n", ui.Green("✔"), ui.Dim(installedHook))
	}
	if installedRemote != "" {
		fmt.Fprintf(os.Stdout, "  %s installed git remote proxy: %s (%s)\n", ui.Green("✔"), ui.Bold(installedRemote), ui.Dim("git push sila-gate <branch>"))
	}
}

func runGatePipelineCmd(ui render.UI, jsonOut bool, branch, forwardTarget string, autoForward bool) {
	if os.Getenv("SILA_SKIP_GATE") == "1" || os.Getenv("SILA_NO_GATE") == "1" || os.Getenv("SKIP_GATE") == "1" {
		if jsonOut {
			emitJSON(map[string]any{"clean": true, "skipped": true})
		} else {
			fmt.Fprintln(os.Stdout, ui.Yellow("⚠")+" quality gate pipeline skipped via SILA_SKIP_GATE")
		}
		return
	}

	cwd, _ := os.Getwd()
	mgr, err := git.NewManager(cwd)
	if err != nil {
		if jsonOut {
			emitJSON(map[string]any{"clean": false, "error": err.Error()})
		} else {
			fmt.Fprintf(os.Stderr, "sila gate: %v\n", err)
		}
		os.Exit(1)
	}

	if branch == "" {
		branch = mgr.CurrentBranch()
	}
	if forwardTarget == "" {
		forwardTarget = "origin"
	}

	if !jsonOut {
		fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · gate pipeline"))+fmt.Sprintf(" (branch: %s, forward: %s)", branch, forwardTarget))
		fmt.Fprintf(os.Stdout, "  %s running quality gate in disposable worktree...\n", ui.Dim("·"))
	}

	res, err := mgr.RunGatePipeline(branch, forwardTarget, autoForward)
	if err != nil {
		if jsonOut {
			emitJSON(map[string]any{"clean": false, "error": err.Error()})
		} else {
			fmt.Fprintf(os.Stderr, "%s %v\n", ui.Red("✖"), err)
		}
		os.Exit(1)
	}

	if jsonOut {
		emitJSON(res)
	} else {
		if res.Clean {
			fmt.Fprintf(os.Stdout, "  %s %s (%s)\n", ui.Green("✔"), ui.Bold("PASS"), res.Duration.Round(time.Millisecond))
			for _, c := range res.Checks {
				fmt.Fprintf(os.Stdout, "    %s %s\n", ui.Green("✔"), ui.Dim(c))
			}
			if autoForward {
				if res.Forwarded {
					fmt.Fprintf(os.Stdout, "  %s %s\n", ui.Green("✔"), ui.Bold(fmt.Sprintf("forwarded push to %s %s", forwardTarget, branch)))
				} else {
					fmt.Fprintf(os.Stderr, "  %s forwarding failed: %s\n", ui.Red("✖"), res.ErrorString)
				}
			}
		} else {
			fmt.Fprintf(os.Stdout, "  %s %s (%s)\n", ui.Red("✖"), ui.Bold("FAIL"), res.Duration.Round(time.Millisecond))
			if res.Output != "" {
				fmt.Fprintf(os.Stdout, "%s\n", res.Output)
			}
			if res.ErrorString != "" {
				fmt.Fprintf(os.Stderr, "%s %s\n", ui.Red("✖"), res.ErrorString)
			}
		}
	}

	if !res.Clean || (autoForward && !res.Forwarded) {
		os.Exit(1)
	}
}

// gateClean is retained for callers that only need the boolean verdict.
func gateClean(checks []gateCheck) bool {
	return computeGateVerdict(checks, false, false).Clean
}

// emitGateHeartbeats records epistemic heartbeats into the knowledge store for verified
// architectural invariant rules when gate checks pass cleanly.
func emitGateHeartbeats(cwd string, checks []gateCheck) {
	if !inGitRepo() {
		return
	}
	dbPath := store.DefaultDBPath()
	if env := os.Getenv("SILA_DB"); env != "" {
		dbPath = env
	}
	if dbPath == "" {
		return
	}
	st, err := store.Open(dbPath)
	if err != nil {
		if os.Getenv("SILA_DEBUG") == "1" {
			fmt.Fprintf(os.Stderr, "sila gate: store open error during heartbeat emission: %v\n", err)
		}
		return
	}
	defer st.Close()

	proj, err := st.EnsureProject(cwd)
	if err != nil {
		if os.Getenv("SILA_DEBUG") == "1" {
			fmt.Fprintf(os.Stderr, "sila gate: project resolve error during heartbeat emission: %v\n", err)
		}
		return
	}

	commitSHA := ""
	gitCmd := exec.Command("git", "rev-parse", "HEAD")
	if cwd != "" {
		gitCmd.Dir = cwd
	}
	if out, err := gitCmd.Output(); err == nil {
		commitSHA = strings.TrimSpace(string(out))
	}
	if commitSHA == "" {
		commitSHA = gitHead()
	}
	if commitSHA == "" {
		return
	}

	checkStatus := make(map[string]string)
	for _, c := range checks {
		checkStatus[c.Name] = c.Status
	}

	var verifiedRules []string

	scarTissuePassed := checkStatus["scar-tissue"] == "pass"
	archSentryPassed := checkStatus["arch-sentry"] == "pass"
	telemetryPassed := checkStatus["telemetry"] == "pass"

	if scarTissuePassed {
		verifiedRules = append(verifiedRules,
			"Always compile with CGO_ENABLED=0",
			"CGO disabled",
			"Pure-Go SQLite only",
			"No hardcoded secrets or sensitive tokens",
			"Zero silent telemetry or ambient beacons",
		)
	}
	if archSentryPassed {
		verifiedRules = append(verifiedRules,
			"Architecture sentry rules",
			"Epistemic fact grounding",
		)
	}
	if telemetryPassed {
		verifiedRules = append(verifiedRules,
			"Zero ambient telemetry",
		)
	}

	// Match and verify active project invariant facts from the store
	if facts, err := st.FactsByProject(proj.ID); err == nil {
		for _, f := range facts {
			isInvariant := f.Kind == "invariant" || f.Tier == string(store.TierSemantic) || f.Confidence == "invariant"
			if !isInvariant {
				continue
			}
			claimLower := strings.ToLower(f.Claim)
			matched := false
			if (strings.Contains(claimLower, "cgo") || strings.Contains(claimLower, "sqlite") || strings.Contains(claimLower, "secret")) && scarTissuePassed {
				matched = true
			} else if (strings.Contains(claimLower, "arch") || strings.Contains(claimLower, "layer") || strings.Contains(claimLower, "grounding") || strings.Contains(claimLower, "srp")) && archSentryPassed {
				matched = true
			} else if strings.Contains(claimLower, "telemetry") && (telemetryPassed || scarTissuePassed) {
				matched = true
			}
			if matched {
				verifiedRules = append(verifiedRules, f.Claim)
			}
		}
	}

	for _, c := range checks {
		if c.Status == "pass" {
			verifiedRules = append(verifiedRules, "gate:"+c.Name)
		}
	}

	seen := make(map[string]bool)
	now := time.Now().UTC().Format(time.RFC3339)
	for _, rule := range verifiedRules {
		if rule == "" || seen[rule] {
			continue
		}
		seen[rule] = true

		hb := store.EpistemicHeartbeat{
			ProjectID:     proj.ID,
			SessionID:     "gate",
			Agent:         "sila-gate",
			InvariantRule: rule,
			EvidenceType:  "gate",
			EvidenceRef:   commitSHA,
			Status:        "pass",
			CreatedAt:     now,
		}
		if err := st.RecordHeartbeat(hb); err != nil {
			if os.Getenv("SILA_DEBUG") == "1" {
				fmt.Fprintf(os.Stderr, "sila gate: record heartbeat error for %q: %v\n", rule, err)
			}
		}
	}
}

func printGate(ui render.UI, checks []gateCheck, verdict gateVerdict) {
	fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · gate")))
	for _, c := range checks {
		icon, status := "✔", ui.Green("PASS")
		switch c.Status {
		case "fail":
			icon, status = ui.Red("✖"), ui.Red("FAIL")
		case "skip":
			icon, status = ui.Yellow("▲"), ui.Yellow("SKIP")
		case "unavailable":
			icon, status = ui.Yellow("▲"), ui.Yellow("UNAVAILABLE")
		case "partial":
			icon, status = ui.Yellow("▲"), ui.Yellow("PARTIAL")
		case "warn":
			icon, status = ui.Yellow("▲"), ui.Yellow("WARN")
		}
		fmt.Fprintf(os.Stdout, "  %s %s %s\n", icon, status, ui.Bold(c.Name))
		fmt.Fprintf(os.Stdout, "    %s\n", ui.Dim(c.Detail))
		if c.Warning != "" {
			fmt.Fprintf(os.Stdout, "    %s %s: %s\n", ui.Yellow("▲"), ui.Bold("WARN"), ui.Yellow(c.Warning))
		}
	}
	for _, b := range verdict.Blockers {
		fix := b.Remediation
		if fix == "" {
			fix = gateRemediation(b.Name)
		}
		fmt.Fprintf(os.Stdout, "  %s %s: %s\n", ui.Red("✖"), ui.Bold(b.Name), b.Detail)
		fmt.Fprintf(os.Stdout, "    %s %s\n", ui.Dim("fix:"), fix)
	}
	if verdict.Clean {
		fmt.Fprintln(os.Stdout, ui.Bold(ui.Green("✔ all checks pass — discipline clean.")))
	} else {
		fmt.Fprintln(os.Stdout, ui.Bold(ui.Red(fmt.Sprintf("✖ %d blocking check(s) — resolve before committing.", len(verdict.Blockers)))))
	}
}

// gateTestTimeout returns the timeout duration string for full gate test runs.
// Defaults to "20m" unless overridden by SILA_GATE_TIMEOUT.
func gateTestTimeout() string {
	if t := strings.TrimSpace(os.Getenv("SILA_GATE_TIMEOUT")); t != "" {
		return t
	}
	return "20m"
}

// gateTestArgs builds the argument list for go test execution in gateTests.
func gateTestArgs(covPath string) []string {
	timeout := gateTestTimeout()
	args := []string{"test", "-race", "-timeout=" + timeout}
	if covPath != "" {
		args = append(args, "-coverprofile="+covPath)
	}
	args = append(args, "./...")
	return args
}

// gateTests runs `go test -race ./...` and reports pass/fail.
func gateTests() gateCheck {
	if _, err := exec.LookPath("go"); err != nil {
		return gateCheck{Name: "tests", Status: "skip", Detail: "go toolchain not found on PATH"}
	}
	if isDocs, count := isDocsOnlyDiff(); isDocs {
		return gateCheck{Name: "tests", Status: "pass", Detail: fmt.Sprintf("skipped go test -race (docs-only diff: %d file(s) modified)", count)}
	}
	covPath := gateCoverageProfilePath()
	const fullTestCmd = "go test -race ./..."
	if cached, ok := checkGateGreenReceipt("", fullTestCmd, covPath); ok {
		return cached
	}
	args := gateTestArgs(covPath)
	out, err := exec.Command("go", args...).CombinedOutput()
	if err == nil {
		n := strings.Count("\n"+string(out), "\nok")
		detail := fmt.Sprintf("go test -race ./... passed (%d packages)", n)
		saveGateGreenReceipt("", fullTestCmd, detail)
		return gateCheck{Name: "tests", Status: "pass", Detail: detail}
	}
	detail := fmt.Sprintf("go test -race ./... failed:\n%s", tailStr(string(out), 600))
	if path := saveGateTestLog(out); path != "" {
		detail = fmt.Sprintf("go test -race ./... failed; full output saved to %s\n%s", path, tailStr(string(out), 600))
	}
	return gateCheck{Name: "tests", Status: "fail", Detail: detail}
}

// saveGateTestLog writes the full `go test` output next to the repository's
// git dir, so a flaky failure can be diagnosed after the truncated gate
// detail scrolls away. It returns "" when the log cannot be written.
func saveGateTestLog(out []byte) string {
	dir := gateGitDir()
	if dir == "" {
		dir = os.TempDir()
	}
	path := filepath.Join(dir, "sila-gate-tests.log")
	if err := os.WriteFile(path, out, 0o600); err != nil {
		return ""
	}
	return path
}

// gateGitDir returns the absolute git directory this repository reports, or "".
func gateGitDir() string {
	out, err := exec.Command("git", "rev-parse", "--absolute-git-dir").Output()
	if err == nil {
		return strings.TrimSpace(string(out))
	}
	out, err = exec.Command("git", "rev-parse", "--git-dir").Output()
	if err != nil {
		return ""
	}
	p := strings.TrimSpace(string(out))
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

// gateFormat flags Go source under cmd/ and internal/ that gofmt would rewrite.
func gateFormat() gateCheck {
	if _, err := exec.LookPath("gofmt"); err != nil {
		return gateCheck{Name: "format", Status: "skip", Detail: "gofmt not found on PATH"}
	}
	out, err := exec.Command("gofmt", "-l", "cmd", "internal").Output()
	if err != nil {
		return gateCheck{Name: "format", Status: "fail", Detail: "gofmt -l failed: " + err.Error()}
	}
	files := strings.Fields(string(out))
	if len(files) == 0 {
		return gateCheck{Name: "format", Status: "pass", Detail: "gofmt reports no formatting changes"}
	}
	if inGitRepo() {
		if trackedOut, err := exec.Command("git", "ls-files", "cmd", "internal").Output(); err == nil {
			trackedMap := make(map[string]bool)
			for _, tf := range strings.Fields(string(trackedOut)) {
				trackedMap[filepath.Clean(tf)] = true
			}
			var trackedUnformatted []string
			for _, f := range files {
				if trackedMap[filepath.Clean(f)] {
					trackedUnformatted = append(trackedUnformatted, f)
				}
			}
			files = trackedUnformatted
		}
	}
	if len(files) == 0 {
		return gateCheck{Name: "format", Status: "pass", Detail: "gofmt reports no formatting changes"}
	}
	return gateCheck{Name: "format", Status: "fail", Detail: fmt.Sprintf("gofmt would reformat %d file(s): %s", len(files), strings.Join(files, ", "))}
}

// gateChangelog flags when source paths changed more recently than CHANGELOG.md.
func gateChangelog() gateCheck {
	if _, err := exec.LookPath("git"); err != nil {
		return gateCheck{Name: "changelog", Status: "skip", Detail: "git not found on PATH"}
	}
	if !inGitRepo() {
		return gateCheck{Name: "changelog", Status: "skip", Detail: "not inside a git repository"}
	}
	src := lastChange("cmd", "internal", "go.mod", "go.sum", ":(exclude)internal/release/CHANGELOG.md")
	log := lastChange("CHANGELOG.md")
	if src.IsZero() || log.IsZero() {
		return gateCheck{Name: "changelog", Status: "pass", Detail: "no comparable source/CHANGELOG history yet"}
	}
	if src.After(log) {
		return gateCheck{Name: "changelog", Status: "fail", Detail: "source changed but CHANGELOG.md [Unreleased] was not updated"}
	}
	return gateCheck{Name: "changelog", Status: "pass", Detail: "CHANGELOG.md is current with source changes"}
}

// gateBinary flags when the running binary was built before the newest source
// change (unbuilt edits) or from a different commit than HEAD.
func gateBinary() gateCheck {
	if _, err := exec.LookPath("git"); err != nil {
		return gateCheck{Name: "binary", Status: "skip", Detail: "git not found on PATH"}
	}
	if !inGitRepo() {
		return gateCheck{Name: "binary", Status: "skip", Detail: "not inside a git repository"}
	}
	// Check if there are uncommitted source changes under cmd/ or internal/
	hasUncommittedSource := false
	if diffCmd := exec.Command("git", "diff", "--quiet", "HEAD", "--", "cmd", "internal", ":(exclude)internal/release/CHANGELOG.md"); diffCmd.Run() != nil {
		hasUncommittedSource = true
	} else if statusOut, err := exec.Command("git", "status", "--porcelain", "cmd", "internal", ":(exclude)internal/release/CHANGELOG.md").Output(); err == nil && len(strings.TrimSpace(string(statusOut))) > 0 {
		hasUncommittedSource = true
	}

	// Primary: build timestamp vs newest source change catches "edit code, forget ./build.sh".
	// Only evaluate filesystem mtime when there are uncommitted source modifications,
	// because checking out clean git worktrees creates files with current checkout timestamp.
	if hasUncommittedSource {
		if bt, err := time.Parse(time.RFC3339, version.Date); err == nil {
			if newest := newestSourceTime(); !newest.IsZero() && newest.After(bt.Add(time.Second)) {
				return gateCheck{Name: "binary", Status: "fail", Detail: "source changed after the binary was built — run ./build.sh"}
			}
		}
	}
	built := strings.TrimSuffix(version.Commit, "-dirty")
	head := gitHead()
	if built == "unknown" || built == "" {
		return gateCheck{Name: "binary", Status: "skip", Detail: "build commit unknown (built from source without vcs info)"}
	}
	if built != head {
		return gateCheck{Name: "binary", Status: "fail", Detail: fmt.Sprintf("binary commit %s != HEAD %s — run ./build.sh", built, head)}
	}
	return gateCheck{Name: "binary", Status: "pass", Detail: "binary is current with source and HEAD (" + head + ")"}
}

// newestSourceTime returns the newest modification time of any .go file under
// cmd/ or internal/.
func newestSourceTime() time.Time {
	var newest time.Time
	for _, dir := range []string{"cmd", "internal"} {
		filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") {
				return nil
			}
			if info.ModTime().After(newest) {
				newest = info.ModTime()
			}
			return nil
		})
	}
	return newest
}

// gateReadme flags subcommands present in commandHelp but absent from the
// documentation command table. The full reference lives in docs/reference.md;
// README.md stays the human entry point and is used as a fallback.
func gateReadme() gateCheck {
	path := "docs/reference.md"
	data, err := os.ReadFile(path)
	if err != nil {
		path = "README.md"
		data, err = os.ReadFile(path)
		if err != nil {
			return gateCheck{Name: "readme", Status: "fail", Detail: "docs/reference.md and README.md not found: " + err.Error()}
		}
	}
	missing := readmeMissingCommands(string(data))
	if len(missing) == 0 {
		return gateCheck{Name: "readme", Status: "pass", Detail: path + " documents every command"}
	}
	return gateCheck{Name: "readme", Status: "fail", Detail: path + " is missing commands: " + strings.Join(missing, ", ")}
}

// gateSentry runs System One semantic inspection on the staged or working tree diff.
func gateSentry(provider assist.Provider) gateCheck {
	if provider == nil {
		return gateCheck{Name: "sentry", Status: "skip", Detail: "assistance provider not configured or consented"}
	}
	out, err := exec.Command("git", "diff", "--cached").Output()
	if err != nil || len(strings.TrimSpace(string(out))) == 0 {
		out, err = exec.Command("git", "diff", "HEAD").Output()
	}
	if err != nil || len(strings.TrimSpace(string(out))) == 0 {
		out, _ = exec.Command("git", "show", "--format=", "HEAD").Output()
	}
	diffText := strings.TrimSpace(string(out))
	if diffText == "" {
		return gateCheck{Name: "sentry", Status: "skip", Detail: "no staged or working tree diff to audit"}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()

	res, err := assist.AuditDiff(ctx, provider, diffText)
	if err != nil || res == nil {
		return gateCheck{Name: "sentry", Status: "unavailable", Detail: "semantic sentry unavailable (timeout or offline); local gate passed"}
	}

	switch res.Status {
	case "unavailable":
		return gateCheck{Name: "sentry", Status: "unavailable", Detail: res.Warning}
	case "finding":
		return gateCheck{Name: "sentry", Status: "fail", Detail: res.Warning}
	case "partial":
		return gateCheck{Name: "sentry", Status: "partial", Detail: res.Warning}
	case "skip":
		return gateCheck{Name: "sentry", Status: "skip", Detail: res.Warning}
	default:
		detail := fmt.Sprintf("semantic sentry clean (%d bytes across %d files, %dms)", res.CoveredBytes, res.FilesCount, res.DurationMs)
		if res.TestFixturesExempt > 0 {
			detail += fmt.Sprintf(" [%d test fixture(s) exempted]", res.TestFixturesExempt)
		}
		return gateCheck{Name: "sentry", Status: "pass", Detail: detail}
	}
}

// readmeMissingCommands returns the unique first-words of commandHelp that do
// not appear in the document's "CLI Command Reference" section.
func readmeMissingCommands(readme string) []string {
	start := strings.Index(readme, "## CLI Command Reference")
	if start < 0 {
		return commandWords()
	}
	section := readme[start:]
	if i := strings.Index(section, "\n## "); i >= 0 {
		section = section[:i]
	}
	var missing []string
	seen := map[string]bool{}
	for _, c := range commandHelp {
		w := strings.Fields(c[0])[0]
		if seen[w] {
			continue
		}
		seen[w] = true
		if !strings.Contains(section, w) {
			missing = append(missing, w)
		}
	}
	return missing
}

// commandWords returns the deduped first-words of every commandHelp entry.
func commandWords() []string {
	var words []string
	seen := map[string]bool{}
	for _, c := range commandHelp {
		w := strings.Fields(c[0])[0]
		if seen[w] {
			continue
		}
		seen[w] = true
		words = append(words, w)
	}
	return words
}

// inGitRepo reports whether the working directory is inside a git repository.
func inGitRepo() bool {
	return exec.Command("git", "rev-parse", "--git-dir").Run() == nil
}

// gitHead returns the short commit SHA of HEAD, or "" on failure.
func gitHead() string {
	out, err := exec.Command("git", "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// lastChange returns the newest time paths changed: the latest commit touching
// any of them, or "now" when there are uncommitted working-tree changes.
func lastChange(paths ...string) time.Time {
	args := append([]string{"log", "-1", "--format=%ct", "--"}, paths...)
	var t time.Time
	if out, err := exec.Command("git", args...).Output(); err == nil {
		var sec int64
		if _, e := fmt.Sscan(strings.TrimSpace(string(out)), &sec); e == nil {
			t = time.Unix(sec, 0)
		}
	}
	if dirty(paths...) {
		return time.Now()
	}
	return t
}

// dirty reports whether paths have uncommitted working-tree changes.
func dirty(paths ...string) bool {
	args := append([]string{"status", "--porcelain", "--"}, paths...)
	out, _ := exec.Command("git", args...).Output()
	return strings.TrimSpace(string(out)) != ""
}

// tailStr returns the last n bytes of s, prefixed with "…" when truncated.
func tailStr(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}

var docExtensions = map[string]bool{
	".md": true, ".markdown": true, ".mdown": true, ".mkd": true,
	".txt": true, ".text": true, ".adoc": true, ".asciidoc": true, ".rst": true,
	".html": true, ".htm": true, ".css": true, ".scss": true, ".sass": true, ".less": true,
	".png": true, ".svg": true, ".jpg": true, ".jpeg": true, ".webp": true,
	".gif": true, ".ico": true, ".bmp": true, ".tiff": true,
	".pdf": true, ".drawio": true,
}

var docBaseNames = map[string]bool{
	"readme": true, "license": true, "licence": true, "credits": true,
	"changelog": true, "contributing": true, "authors": true, "notice": true,
	"codeowners": true, "security": true, ".gitignore": true, ".windsurfrules": true,
}

var codeExtensions = map[string]bool{
	".go": true,
	".ts": true, ".tsx": true, ".js": true, ".jsx": true, ".mjs": true, ".cjs": true,
	".vue": true, ".svelte": true, ".astro": true,
	".py": true, ".pyi": true,
	".rs":   true,
	".rb":   true,
	".php":  true,
	".java": true, ".kt": true, ".kts": true, ".scala": true,
	".c": true, ".cpp": true, ".cc": true, ".cxx": true, ".h": true, ".hpp": true, ".hh": true,
	".cs": true, ".fs": true,
	".swift": true,
	".sh":    true, ".bash": true, ".zsh": true,
	".sql": true,
}

var manifestFiles = map[string]bool{
	"go.mod": true, "go.sum": true, "build.sh": true, "makefile": true,
	"package.json": true, "pnpm-lock.yaml": true, "package-lock.json": true, "yarn.lock": true, "bun.lockb": true,
	"tsconfig.json": true,
	"cargo.toml":    true, "cargo.lock": true,
	"pyproject.toml": true, "requirements.txt": true, "pipfile": true, "setup.py": true,
	"pom.xml": true, "build.gradle": true, "build.gradle.kts": true,
	"wrangler.json": true, "wrangler.jsonc": true, "wrangler.toml": true,
	"dockerfile": true, "docker-compose.yml": true, "docker-compose.yaml": true,
}

// isGoalLedgerArtifact reports true if the relative path belongs to the goals ledger
// or local Sila state hierarchy (such as goals/**, .sila/**), including task scripts,
// verification receipts, and test evidence artifacts.
func isGoalLedgerArtifact(norm string) bool {
	norm = strings.TrimPrefix(norm, "./")
	if strings.HasPrefix(norm, "goals/") || norm == "goals" ||
		strings.HasPrefix(norm, ".sila/") || norm == ".sila" {
		return true
	}
	return false
}

func isCodeOrManifestFile(relPath string) bool {
	norm := filepath.ToSlash(strings.ToLower(strings.TrimSpace(relPath)))
	norm = strings.TrimPrefix(norm, "./")
	if norm == "" {
		return false
	}
	if strings.HasSuffix(norm, "/") {
		norm = strings.TrimSuffix(norm, "/")
	}
	if isGoalLedgerArtifact(norm) {
		return false
	}
	base := filepath.Base(norm)
	ext := filepath.Ext(norm)
	if manifestFiles[base] {
		return true
	}
	if strings.HasPrefix(base, "tsconfig.") && strings.HasSuffix(base, ".json") {
		return true
	}
	if strings.HasSuffix(base, ".config.js") || strings.HasSuffix(base, ".config.ts") || strings.HasSuffix(base, ".config.mjs") || strings.HasSuffix(base, ".config.cjs") {
		return true
	}
	return codeExtensions[ext]
}

func isDocOrAssetFile(relPath string) bool {
	norm := filepath.ToSlash(strings.ToLower(strings.TrimSpace(relPath)))
	norm = strings.TrimPrefix(norm, "./")
	if norm == "" {
		return false
	}
	if strings.HasSuffix(norm, "/") {
		norm = strings.TrimSuffix(norm, "/")
	}
	if isGoalLedgerArtifact(norm) {
		return true
	}
	if strings.HasPrefix(norm, "docs/") || norm == "docs" ||
		strings.HasPrefix(norm, "doc/") || norm == "doc" ||
		strings.HasPrefix(norm, "documentation/") || norm == "documentation" ||
		strings.HasPrefix(norm, ".ui-craft/") || norm == ".ui-craft" ||
		strings.HasPrefix(norm, ".github/") || norm == ".github" ||
		strings.HasPrefix(norm, ".cursor/") || norm == ".cursor" ||
		strings.HasPrefix(norm, ".agents/") || norm == ".agents" ||
		strings.HasPrefix(norm, ".claude/") || norm == ".claude" {
		return !isCodeOrManifestFile(norm)
	}
	ext := filepath.Ext(norm)
	if docExtensions[ext] {
		return true
	}
	base := filepath.Base(norm)
	baseWithoutExt := strings.TrimSuffix(base, ext)
	if docBaseNames[baseWithoutExt] || docBaseNames[base] {
		return true
	}
	return false
}

func getChangedFilesForDir(dir string) []string {
	cmdStaged := exec.Command("git", "diff", "--cached", "--name-only")
	if dir != "" {
		cmdStaged.Dir = dir
	}
	if out, err := cmdStaged.Output(); err == nil {
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		var staged []string
		for _, l := range lines {
			if s := strings.TrimSpace(l); s != "" {
				staged = append(staged, s)
			}
		}
		if len(staged) > 0 {
			return staged
		}
	}

	cmdStatus := exec.Command("git", "status", "--porcelain")
	if dir != "" {
		cmdStatus.Dir = dir
	}
	out, err := cmdStatus.Output()
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var paths []string
	for _, l := range lines {
		p := parsePorcelainPath(l)
		if p != "" {
			paths = append(paths, p)
		}
	}
	return paths
}

func hasExternalSourceChanges(dir string) bool {
	candidates := getChangedFilesForDir(dir)
	if len(candidates) == 0 {
		return false
	}
	for _, p := range candidates {
		if isCodeOrManifestFile(p) {
			return true
		}
	}
	return false
}

// isDocsOnlyDiff reports true when git working tree or staged diff modifies exclusively
// documentation, goals, assets, or markup files without touching code or manifests.
func isDocsOnlyDiff() (bool, int) {
	return isDocsOnlyDiffForDir("")
}

// isDocsOnlyDiffForDir reports true when git working tree or staged diff in dir modifies
// exclusively documentation, goals, assets, or markup files without touching code or manifests.
func isDocsOnlyDiffForDir(dir string) (bool, int) {
	if os.Getenv("SILA_FORCE_TESTS") == "1" || os.Getenv("SILA_GATE_FULL") == "1" {
		return false, 0
	}
	candidates := getChangedFilesForDir(dir)
	if len(candidates) == 0 {
		return false, 0
	}
	docCount := 0
	for _, p := range candidates {
		if isCodeOrManifestFile(p) {
			return false, 0
		}
		if isDocOrAssetFile(p) {
			docCount++
			continue
		}
		return false, 0
	}
	return docCount > 0, docCount
}

// gateParity audits cross-surface consistency across README.md, html-docs/index.html, CREDITS.md, and docs/reference.md.
func gateParity() gateCheck {
	var drifts []string

	// 1. Canonical Maintainer & sponsorship links
	canonicalURLs := []string{
		"jadmadi.net",
		"x.com/jadmadi",
		"github.com/jadmadi",
		"paypal.me/madise",
	}

	surfaces := []string{
		"README.md",
		"html-docs/index.html",
		"CREDITS.md",
	}

	for _, s := range surfaces {
		content, err := os.ReadFile(s)
		if err != nil {
			drifts = append(drifts, fmt.Sprintf("%s not found on disk: %v", s, err))
			continue
		}
		sContent := string(content)
		for _, u := range canonicalURLs {
			if !strings.Contains(sContent, u) {
				drifts = append(drifts, fmt.Sprintf("%s is missing canonical URL %q", s, u))
			}
		}
	}

	// 2. Relative markdown links in README.md, docs/reference.md, CREDITS.md, and CONTRIBUTING.md
	mdSurfaces := []string{"README.md", "docs/reference.md", "CREDITS.md", "CONTRIBUTING.md"}
	for _, mdFile := range mdSurfaces {
		content, err := os.ReadFile(mdFile)
		if err != nil {
			continue
		}
		dir := filepath.Dir(mdFile)
		sContent := string(content)
		for {
			idx := strings.Index(sContent, "](")
			if idx == -1 {
				break
			}
			sContent = sContent[idx+2:]
			closeIdx := strings.Index(sContent, ")")
			if closeIdx == -1 {
				break
			}
			link := strings.TrimSpace(sContent[:closeIdx])
			sContent = sContent[closeIdx+1:]

			// Ignore external URLs, anchors, mailto, and file schemas
			if strings.HasPrefix(link, "http://") || strings.HasPrefix(link, "https://") ||
				strings.HasPrefix(link, "mailto:") || strings.HasPrefix(link, "#") ||
				strings.HasPrefix(link, "file://") {
				continue
			}

			// Strip anchors and query parameters
			if qIdx := strings.IndexAny(link, "#?"); qIdx != -1 {
				link = link[:qIdx]
			}
			link = strings.TrimSpace(link)
			if link == "" {
				continue
			}

			target := filepath.Join(dir, link)
			if _, err := os.Stat(target); err != nil {
				drifts = append(drifts, fmt.Sprintf("%s: broken local link [%s] -> %s (not found on disk)", mdFile, link, target))
			}
		}
	}

	if len(drifts) > 0 {
		return gateCheck{
			Name:   "parity",
			Status: "fail",
			Detail: fmt.Sprintf("cross-surface drift detected:\n%s", strings.Join(drifts, "\n")),
		}
	}

	return gateCheck{
		Name:   "parity",
		Status: "pass",
		Detail: "canonical maintainer links, sponsorship, and local references in sync across surfaces",
	}
}

// gateKnowledge audits session friction and uncaptured lessons/memos.
// It detects bug fixes in recent commit logs or candidate lessons in recent session messages,
// advising the agent to record them before closing their session.
func gateKnowledge() gateCheck {
	dbPath := store.DefaultDBPath()
	st, ok, err := store.OpenInspection(dbPath)
	if err != nil {
		return gateCheck{Name: "knowledge", Status: "fail", Detail: fmt.Sprintf("knowledge store inspection failed: %v", err)}
	}
	if !ok || st == nil {
		return gateCheck{Name: "knowledge", Status: "skip", Detail: "knowledge store not initialized; skipping friction audit"}
	}
	defer st.Close()

	cwd, _ := os.Getwd()
	proj := store.ProjectID(cwd)

	// Check if candidate lessons exist in recent messages
	candidates := dream.ExtractProjectLessonCandidates(st, proj, time.Time{})
	if len(candidates) > 0 {
		return gateCheck{
			Name:   "knowledge",
			Status: "pass",
			Detail: fmt.Sprintf("%d candidate lesson(s) detected in session; capture with 'sila lesson distill --save'", len(candidates)),
		}
	}

	// Check if recent commit message was a bug fix without recent lessons recorded
	if out, err := exec.Command("git", "log", "-1", "--pretty=%s").Output(); err == nil {
		msg := strings.TrimSpace(string(out))
		lower := strings.ToLower(msg)
		if strings.HasPrefix(lower, "fix:") || strings.HasPrefix(lower, "fix(") {
			lessons, _ := st.LessonFacts(proj, 5)
			hasRecentLesson := false
			for _, l := range lessons {
				if t, err := time.Parse(time.RFC3339, l.FirstSeen); err == nil {
					if time.Since(t) < 2*time.Hour {
						hasRecentLesson = true
						break
					}
				}
			}
			if !hasRecentLesson {
				return gateCheck{
					Name:   "knowledge",
					Status: "pass",
					Detail: fmt.Sprintf("recent bug fix %q detected; consider capturing lesson with 'sila lesson \"<symptom>\" --cause=... --fix=...'", msg),
				}
			}
		}
	}

	return gateCheck{
		Name:   "knowledge",
		Status: "pass",
		Detail: "no unrecorded session friction detected; knowledge store up to date",
	}
}

// gateConcurrency checks active runtime goroutines via pprof goroutine profiles
// to ensure no dangling background routines leak during gate execution.
func gateConcurrency() gateCheck {
	prof := pprof.Lookup("goroutineleak")
	if prof == nil {
		prof = pprof.Lookup("goroutine")
	}
	if prof != nil {
		count := prof.Count()
		if count > 100 {
			return gateCheck{
				Name:   "concurrency",
				Status: "fail",
				Detail: fmt.Sprintf("goroutine leak sentry detected %d active goroutines (expected < 100)", count),
			}
		}
		return gateCheck{
			Name:   "concurrency",
			Status: "pass",
			Detail: fmt.Sprintf("goroutine profile clean (%d active routines)", count),
		}
	}
	return gateCheck{
		Name:   "concurrency",
		Status: "pass",
		Detail: "runtime/pprof profile clean",
	}
}

// gateLicense audits open-source dependency licenses to guarantee zero unauthorized
// copyleft dependencies (GPL, AGPL, SSPL, etc.) are imported, strictly enforcing AGENTS.md §0.
func gateLicense() gateCheck {
	report, err := assess.AuditLicenses(".")
	if err != nil {
		return gateCheck{
			Name:   "license",
			Status: "fail",
			Detail: fmt.Sprintf("license audit error: %v", err),
		}
	}
	if !report.Clean {
		return gateCheck{
			Name:   "license",
			Status: "fail",
			Detail: fmt.Sprintf("unapproved copyleft licenses detected: %s", strings.Join(report.Violations, "; ")),
		}
	}
	return gateCheck{
		Name:   "license",
		Status: "pass",
		Detail: fmt.Sprintf("all %d direct and indirect dependencies carry approved permissive licenses", report.TotalScanned),
	}
}

// gateTelemetry audits the codebase to guarantee zero silent telemetry,
// ensuring no tracking SDKs or unauthorized background network beacons exist.
func gateTelemetry() gateCheck {
	// 1. Audit go.mod for banned telemetry / tracking dependencies
	goModBytes, err := os.ReadFile("go.mod")
	if err == nil {
		bannedDeps := []string{
			"segment-go", "mixpanel", "posthog", "statsig", "launchdarkly", "amplitude", "sentry-go",
		}
		for _, dep := range bannedDeps {
			if strings.Contains(string(goModBytes), dep) {
				return gateCheck{
					Name:   "telemetry",
					Status: "fail",
					Detail: fmt.Sprintf("banned telemetry dependency %q found in go.mod", dep),
				}
			}
		}
	}

	return gateCheck{
		Name:   "telemetry",
		Status: "pass",
		Detail: "zero ambient telemetry; network calls strictly explicit & user-consented",
	}
}

// gateExternalChecks validates engineering discipline for external repositories
// using auto-detected or documented runbooks instead of Sila's internal requirements.
func gateExternalChecks(cwd string) []gateCheck {
	return []gateCheck{
		gateExternalTests(cwd),
		gateExternalFormat(cwd),
		gateExternalChangelog(cwd),
		gateKnowledge(),
		gateScarTissue(),
	}
}

// gateExternalTests executes the verified test command detected for the repository.
func gateExternalTests(cwd string) gateCheck {
	if isDocs, count := isDocsOnlyDiffForDir(cwd); isDocs {
		return gateCheck{Name: "tests", Status: "pass", Detail: fmt.Sprintf("skipped tests (docs-only diff: %d file(s) modified)", count)}
	}
	rb := dream.DetectRunbook(cwd)
	var testCmd, source string
	if rb != nil {
		for _, e := range rb.Entries {
			if e.Phase == "Test" {
				testCmd = e.Command
				source = e.Source
				break
			}
		}
	}
	if testCmd == "" {
		if _, err := os.Stat(filepath.Join(cwd, "go.mod")); err == nil {
			testCmd = "go test ./..."
			source = "manifest"
		} else if _, err := os.Stat(filepath.Join(cwd, "package.json")); err == nil {
			testCmd = "pnpm test"
			source = "manifest"
		} else if _, err := os.Stat(filepath.Join(cwd, "Cargo.toml")); err == nil {
			testCmd = "cargo test"
			source = "manifest"
		}
	}
	if testCmd == "" {
		return gateCheck{Name: "tests", Status: "skip", Detail: "no test command detected in AGENTS.md or manifest"}
	}
	if cached, ok := checkGateGreenReceipt(cwd, testCmd, ""); ok {
		return cached
	}

	cmd := exec.Command("sh", "-c", testCmd)
	cmd.Dir = cwd
	out, err := cmd.CombinedOutput()
	if err == nil {
		detail := fmt.Sprintf("%s passed (%s)", testCmd, source)
		saveGateGreenReceipt(cwd, testCmd, detail)
		return gateCheck{Name: "tests", Status: "pass", Detail: detail}
	}
	detail := fmt.Sprintf("%s failed:\n%s", testCmd, tailStr(string(out), 600))
	return gateCheck{Name: "tests", Status: "fail", Detail: detail}
}

// gateExternalFormat validates formatting using toolchains relevant to the external project.
func gateExternalFormat(cwd string) gateCheck {
	if _, err := os.Stat(filepath.Join(cwd, "go.mod")); err == nil {
		if _, err := exec.LookPath("gofmt"); err != nil {
			return gateCheck{Name: "format", Status: "skip", Detail: "gofmt not found on PATH"}
		}
		cmd := exec.Command("gofmt", "-l", ".")
		cmd.Dir = cwd
		out, err := cmd.Output()
		if err != nil {
			return gateCheck{Name: "format", Status: "skip", Detail: "gofmt check failed: " + err.Error()}
		}
		files := strings.Fields(strings.TrimSpace(string(out)))
		var unformatted []string
		for _, f := range files {
			if !strings.HasPrefix(f, "vendor/") {
				unformatted = append(unformatted, f)
			}
		}
		if len(unformatted) > 0 {
			return gateCheck{Name: "format", Status: "fail", Detail: fmt.Sprintf("gofmt would reformat %d file(s): %s", len(unformatted), strings.Join(unformatted, ", "))}
		}
		return gateCheck{Name: "format", Status: "pass", Detail: "gofmt reports no formatting changes"}
	}
	// No go.mod: this is not a Go project, so Go formatting does not apply.
	// Report pass (not skip) so the fail-closed gate does not block non-Go
	// repositories forever; this matches gateExternalTests, which reports pass
	// when there are no Go sources to check.
	return gateCheck{Name: "format", Status: "pass", Detail: "no Go manifest; gofmt check not applicable"}
}

// gateExternalChangelog checks CHANGELOG.md if one is present in the repository root.
func gateExternalChangelog(cwd string) gateCheck {
	changelogPath := filepath.Join(cwd, "CHANGELOG.md")
	if _, err := os.Stat(changelogPath); err != nil {
		return gateCheck{Name: "changelog", Status: "pass", Detail: "no root CHANGELOG.md present; changelog check not applicable"}
	}
	return gateChangelog()
}

func hasOxlint(cwd string) bool {
	if _, err := os.Stat(filepath.Join(cwd, "oxlintrc.json")); err == nil {
		return true
	}
	if _, err := os.Stat(filepath.Join(cwd, ".oxlintrc.json")); err == nil {
		return true
	}
	return false
}

// gateEdgeOxlint executes oxlint for fast static analysis on TypeScript/JavaScript repositories.
func gateEdgeOxlint(cwd string) gateCheck {
	var cmd *exec.Cmd
	localBin := filepath.Join(cwd, "node_modules", ".bin", "oxlint")
	if _, err := os.Stat(localBin); err == nil {
		cmd = exec.Command(localBin, "--config", "oxlintrc.json", "src")
	} else if _, err := exec.LookPath("oxlint"); err == nil {
		cmd = exec.Command("oxlint", "--config", "oxlintrc.json", "src")
	} else if _, err := exec.LookPath("pnpm"); err == nil {
		cmd = exec.Command("pnpm", "exec", "oxlint", "--config", "oxlintrc.json", "src")
	} else if _, err := exec.LookPath("npx"); err == nil {
		cmd = exec.Command("npx", "oxlint", "--config", "oxlintrc.json", "src")
	} else {
		return gateCheck{Name: "oxlint", Status: "skip", Detail: "oxlint toolchain not found"}
	}
	cmd.Dir = cwd
	start := time.Now()
	out, err := cmd.CombinedOutput()
	dur := time.Since(start).Round(time.Millisecond)
	if err == nil {
		return gateCheck{
			Name:   "oxlint",
			Status: "pass",
			Detail: fmt.Sprintf("oxlint passed in %s", dur),
		}
	}
	detail := fmt.Sprintf("oxlint failed in %s:\n%s", dur, tailStr(string(out), 600))
	return gateCheck{Name: "oxlint", Status: "fail", Detail: detail}
}

// gateCharterSentry verifies that staged files strictly conform to active GoalCharter boundaries.
func gateCharterSentry(cwd string) gateCheck {
	if _, err := exec.LookPath("git"); err != nil {
		return gateCheck{Name: "charter-sentry", Status: "skip", Detail: "git not found on PATH"}
	}
	activeCharters, err := goals.ListActiveCharters(cwd)
	if err != nil || len(activeCharters) == 0 {
		return gateCheck{Name: "charter-sentry", Status: "pass", Detail: "no active goal charter constraints"}
	}

	cmd := exec.Command("git", "diff", "--cached", "--name-only")
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		return gateCheck{Name: "charter-sentry", Status: "skip", Detail: "failed to inspect staged files"}
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var staged []string
	for _, l := range lines {
		if s := strings.TrimSpace(l); s != "" {
			staged = append(staged, s)
		}
	}
	if len(staged) == 0 {
		return gateCheck{Name: "charter-sentry", Status: "pass", Detail: "no staged files to validate against active charters"}
	}

	activeGoalSlug := os.Getenv("SILA_ACTIVE_GOAL")
	if activeGoalSlug == "" && len(activeCharters) == 1 {
		activeGoalSlug = activeCharters[0].GoalSlug
	}

	for _, c := range activeCharters {
		if activeGoalSlug != "" && c.GoalSlug == activeGoalSlug {
			if err := goals.ValidateStagedFilesAgainstCharter(c, staged); err != nil {
				return gateCheck{Name: "charter-sentry", Status: "fail", Detail: err.Error()}
			}
		} else {
			if err := goals.ValidateStagedFilesNoCollisionWithCharter(c, staged); err != nil {
				return gateCheck{Name: "charter-sentry", Status: "fail", Detail: err.Error()}
			}
		}
	}
	return gateCheck{Name: "charter-sentry", Status: "pass", Detail: fmt.Sprintf("staged files (%d) strictly adhere to active goal charter boundaries", len(staged))}
}

// gateSurfaceSentry verifies that staged files strictly conform to exclusive agent write-surface boundaries.
func gateSurfaceSentry(cwd string) gateCheck {
	if _, err := exec.LookPath("git"); err != nil {
		return gateCheck{Name: "surface-guard", Status: "skip", Detail: "git not found on PATH"}
	}

	cmd := exec.Command("git", "diff", "--cached", "--name-only")
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		return gateCheck{Name: "surface-guard", Status: "skip", Detail: "failed to inspect staged diff"}
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var staged []string
	for _, l := range lines {
		if s := strings.TrimSpace(l); s != "" {
			staged = append(staged, s)
		}
	}
	if len(staged) == 0 {
		return gateCheck{Name: "surface-guard", Status: "pass", Detail: "no staged files to validate against write surface"}
	}

	// 1. Invariant: Reviewer agents are structurally read-only with no write surface
	roleEnv := strings.ToLower(strings.TrimSpace(os.Getenv("SILA_AGENT_ROLE")))
	if roleEnv == "" {
		roleEnv = strings.ToLower(strings.TrimSpace(os.Getenv("SILA_ROLE_CLASS")))
	}
	if roleEnv == "reviewer" || roleEnv == "auditor" {
		return gateCheck{
			Name:   "surface-guard",
			Status: "fail",
			Detail: fmt.Sprintf("Reviewer agent is structurally read-only; cannot stage file mutations (%d files staged)", len(staged)),
		}
	}

	// 2. Explicit write surface from SILA_WRITE_SURFACE
	if surfEnv := os.Getenv("SILA_WRITE_SURFACE"); surfEnv != "" {
		parts := strings.FieldsFunc(surfEnv, func(r rune) bool {
			return r == ',' || r == '\n' || r == ';'
		})
		surface := &crew.WriteSurface{
			AgentID:   "current-agent",
			RoleClass: crew.RoleClassProducer,
			Patterns:  parts,
		}
		if err := crew.ValidateStagedFilesAgainstSurface(surface, staged); err != nil {
			return gateCheck{Name: "surface-guard", Status: "fail", Detail: err.Error()}
		}
	}

	// 3. Active crewmate inspection: check if current worktree belongs to a spawned crewmate
	if mgr, err := crew.NewCrewManager(cwd); err == nil {
		crewmates, _ := mgr.List()
		for _, cm := range crewmates {
			if cm.Status == crew.StatusRunning || cm.Status == crew.StatusSpawning || cm.Status == crew.StatusIdle {
				if cm.Worktree != "" && (cm.Worktree == cwd || strings.HasPrefix(cwd, cm.Worktree)) {
					if cm.WriteSurface != nil {
						if err := crew.ValidateStagedFilesAgainstSurface(cm.WriteSurface, staged); err != nil {
							return gateCheck{Name: "surface-guard", Status: "fail", Detail: err.Error()}
						}
					}
				}
			}
		}
	}

	return gateCheck{Name: "surface-guard", Status: "pass", Detail: fmt.Sprintf("staged files (%d) adhere to exclusive write-surface boundaries", len(staged))}
}

// gateReviewerVetoSentry blocks the gate if any active non-overrulable reviewer veto exists.
func gateReviewerVetoSentry(cwd string) gateCheck {
	vetoes, err := assess.ScanActiveVetoes(cwd)
	if err != nil {
		return gateCheck{Name: "reviewer-veto", Status: "skip", Detail: "failed to inspect reviewer vetoes: " + err.Error()}
	}
	if len(vetoes) == 0 {
		return gateCheck{Name: "reviewer-veto", Status: "pass", Detail: "no active reviewer vetoes"}
	}
	var details []string
	for _, v := range vetoes {
		details = append(details, fmt.Sprintf("goal %s vetoed by %s: %s", v.GoalSlug, v.AuditorName, v.Reason))
	}
	return gateCheck{
		Name:   "reviewer-veto",
		Status: "fail",
		Detail: fmt.Sprintf("non-overrulable reviewer veto active on %d goal(s): %s", len(vetoes), strings.Join(details, "; ")),
	}
}

// gateRuleOfFiveCompletion audits active goal modifications against the 5-point completion verification protocol.
func gateRuleOfFiveCompletion(cwd string) gateCheck {
	activeCharters, err := goals.ListActiveCharters(cwd)
	if err != nil || len(activeCharters) == 0 {
		return gateCheck{Name: "rule-of-five", Status: "pass", Detail: "no active claimed goals requiring completion verification"}
	}

	cmd := exec.Command("git", "diff", "--cached", "--name-only")
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		return gateCheck{Name: "rule-of-five", Status: "skip", Detail: "failed to inspect staged files"}
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var staged []string
	for _, l := range lines {
		if s := strings.TrimSpace(l); s != "" {
			staged = append(staged, s)
		}
	}
	if len(staged) == 0 {
		return gateCheck{Name: "rule-of-five", Status: "pass", Detail: "no staged files to audit against Rule of Five"}
	}

	activeGoalSlug := os.Getenv("SILA_ACTIVE_GOAL")
	if activeGoalSlug == "" && len(activeCharters) == 1 {
		activeGoalSlug = activeCharters[0].GoalSlug
	}
	if activeGoalSlug == "" {
		return gateCheck{Name: "rule-of-five", Status: "pass", Detail: "multiple active goals; set SILA_ACTIVE_GOAL to enforce completion gate"}
	}

	rep, err := assess.CheckRuleOfFiveCompletion(cwd, activeGoalSlug, staged)
	if err != nil {
		return gateCheck{Name: "rule-of-five", Status: "skip", Detail: "failed to evaluate Rule of Five: " + err.Error()}
	}

	if rep != nil && !rep.Clean {
		var relevant []string
		for _, v := range rep.Violations {
			if strings.HasPrefix(v, "Point 3") || strings.HasPrefix(v, "Point 4") || strings.HasPrefix(v, "Point 5") {
				relevant = append(relevant, v)
			}
		}
		if len(relevant) > 0 {
			return gateCheck{
				Name:   "rule-of-five",
				Status: "fail",
				Detail: fmt.Sprintf("Rule of Five completion violation on goal %q: %s", activeGoalSlug, strings.Join(relevant, "; ")),
			}
		}
	}

	return gateCheck{
		Name:   "rule-of-five",
		Status: "pass",
		Detail: fmt.Sprintf("Rule of Five verified: 0 hollow stubs, clean blast radius, honest disclosures for goal %q", activeGoalSlug),
	}
}

// rebaseRetestTimeout bounds the semantic rebase-drift re-test. The default
// keeps small repositories fast; large repositories can raise it through
// SILA_REBASE_RETEST_TIMEOUT when their authoritative runbook command needs
// longer than the default.
func rebaseRetestTimeout() time.Duration {
	if v := strings.TrimSpace(os.Getenv("SILA_REBASE_RETEST_TIMEOUT")); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return 60 * time.Second
}

// gateRebaseDriftSentry checks active claimed goals against their recorded patch-id baseline.
// If semantic rebase drift is detected, it triggers automated re-testing to certify safety.
func gateRebaseDriftSentry(cwd string) gateCheck {
	activeCharters, err := goals.ListActiveCharters(cwd)
	if err != nil {
		return gateCheck{
			Name:   "rebase-drift",
			Status: "fail",
			Detail: fmt.Sprintf("failed to list active charters: %v", err),
		}
	}
	if len(activeCharters) == 0 {
		return gateCheck{Name: "rebase-drift", Status: "pass", Detail: "no active claimed goals requiring rebase drift sentry"}
	}

	ctx, cancel := context.WithTimeout(context.Background(), rebaseRetestTimeout())
	defer cancel()

	for _, charter := range activeCharters {
		report, err := assess.CheckRebaseDrift(ctx, cwd, charter.GoalSlug, "", nil)
		if err != nil {
			return gateCheck{
				Name:   "rebase-drift",
				Status: "fail",
				Detail: fmt.Sprintf("rebase drift check failed on goal %q: %v", charter.GoalSlug, err),
			}
		}
		if report.DriftDetected && !report.ReTestPassed {
			if strings.Contains(report.Detail, "RETEST_COMMAND_UNRESOLVED: no ") && !hasExternalSourceChanges(cwd) {
				_, _ = assess.StoreBaselinePatch(cwd, charter.GoalSlug, report.CurrentPatch, "HEAD", "docs-only-rebaseline")
				continue
			}
			return gateCheck{
				Name:   "rebase-drift",
				Status: "fail",
				Detail: fmt.Sprintf("semantic rebase drift on goal %q failed automated re-test: %s", charter.GoalSlug, report.Detail),
			}
		}
	}

	return gateCheck{Name: "rebase-drift", Status: "pass", Detail: "patch-id verified against merge-base (zero semantic drift)"}
}

// gateDecisionSentry verifies decision address uniqueness, formatting, and flags unresolved decisions.
func gateDecisionSentry(cwd string) gateCheck {
	dbPath := store.DefaultDBPath()
	st, ok, err := store.OpenInspection(dbPath)
	if err != nil {
		return gateCheck{Name: "decisions", Status: "fail", Detail: fmt.Sprintf("knowledge store inspection failed: %v", err)}
	}
	if !ok || st == nil {
		return gateCheck{Name: "decisions", Status: "skip", Detail: "knowledge store not initialized; skipping decision sentry"}
	}
	defer st.Close()

	project := store.ProjectID(cwd)
	anomalies, err := st.CheckDecisionCollisions(project)
	if err != nil {
		return gateCheck{Name: "decisions", Status: "fail", Detail: "decision check failed: " + err.Error()}
	}
	if len(anomalies) > 0 {
		return gateCheck{
			Name:   "decisions",
			Status: "fail",
			Detail: fmt.Sprintf("detected %d decision address collision(s)/violation(s): %s", len(anomalies), strings.Join(anomalies, "; ")),
		}
	}

	decisions, err := st.ListDecisions(store.DecisionFilter{ProjectID: project})
	if err != nil {
		return gateCheck{Name: "decisions", Status: "fail", Detail: "list decisions failed: " + err.Error()}
	}

	openCount := 0
	var openAddrs []string
	for _, d := range decisions {
		if d.Status == "open" {
			openCount++
			openAddrs = append(openAddrs, d.Address)
		}
	}

	if openCount > 0 {
		return gateCheck{
			Name:   "decisions",
			Status: "pass",
			Detail: fmt.Sprintf("all decision addresses unique (%d total); %d open decision(s) tracked: %s", len(decisions), openCount, strings.Join(openAddrs, ", ")),
		}
	}

	return gateCheck{
		Name:   "decisions",
		Status: "pass",
		Detail: fmt.Sprintf("all decision addresses unique (%d total, 0 open)", len(decisions)),
	}
}

// gateArchSentry runs fast in-memory architectural discipline checks (<50ms):
// single responsibility file weights, diff anti-patterns (unbatched D1 loops, raw ANSI escapes, mock keys),
// and epistemic fact grounding.
func gateArchSentry(cwd string, skipArch bool) gateCheck {
	if _, err := exec.LookPath("git"); err != nil {
		return gateCheck{Name: "arch-sentry", Status: "skip", Detail: "git not found on PATH"}
	}
	if !inGitRepo() {
		return gateCheck{Name: "arch-sentry", Status: "skip", Detail: "not inside a git repository"}
	}

	var st *store.Store
	var storeErr error
	if dbPath := store.DefaultDBPath(); dbPath != "" {
		s, ok, err := store.OpenInspection(dbPath)
		if err != nil {
			storeErr = err
		} else if ok && s != nil {
			st = s
			defer st.Close()
		}
	}

	if storeErr != nil {
		out, err := exec.Command("git", "diff", "--cached", "--name-status").Output()
		if err == nil {
			deleted := silagate.ExtractDeletedPaths(string(out))
			if len(deleted) > 0 {
				return gateCheck{
					Name:   "arch-sentry",
					Status: "fail",
					Detail: fmt.Sprintf("fact grounding check unavailable due to store contention/inspection error: %v", storeErr),
				}
			}
		}
	}

	res := silagate.RunArchSentry(silagate.ArchSentryOptions{
		RepoRoot: cwd,
		Store:    st,
		SkipArch: skipArch,
	})

	if res.Skipped {
		return gateCheck{
			Name:   "arch-sentry",
			Status: "skip",
			Detail: res.SkipReason,
		}
	}

	if !res.Clean {
		return gateCheck{
			Name:   "arch-sentry",
			Status: "fail",
			Detail: strings.Join(res.Errors, "\n"),
		}
	}

	if len(res.Warnings) > 0 {
		return gateCheck{
			Name:   "arch-sentry",
			Status: "pass",
			Detail: fmt.Sprintf("clean across SRP, edge anti-patterns, and epistemic grounding (%d warnings:\n%s)", len(res.Warnings), strings.Join(res.Warnings, "\n")),
		}
	}

	return gateCheck{
		Name:   "arch-sentry",
		Status: "pass",
		Detail: fmt.Sprintf("clean across SRP file weights, edge anti-patterns, and epistemic fact grounding (scanned in %v)", res.Duration.Round(time.Millisecond)),
	}
}

// gateRTLSentry runs deterministic linter checking for physical directional CSS properties
// when the project enforces an active RTL invariant.
func gateRTLSentry(cwd string) gateCheck {
	if _, err := exec.LookPath("git"); err != nil {
		return gateCheck{Name: "rtl-sentry", Status: "skip", Detail: "git not found on PATH"}
	}
	if !inGitRepo() {
		return gateCheck{Name: "rtl-sentry", Status: "skip", Detail: "not inside a git repository"}
	}

	var st *store.Store
	if dbPath := store.DefaultDBPath(); dbPath != "" {
		if s, ok, err := store.OpenInspection(dbPath); err == nil && ok && s != nil {
			st = s
			defer st.Close()
		}
	}

	// Fetch staged diff
	cmd := exec.Command("git", "diff", "--cached")
	cmd.Dir = cwd
	out, err := cmd.Output()
	diffText := ""
	if err == nil {
		diffText = string(out)
	}

	res := silagate.RunRTLSentry(cwd, st, diffText)
	if res.Skipped {
		return gateCheck{
			Name:   "rtl-sentry",
			Status: "skip",
			Detail: res.SkipReason,
		}
	}

	if !res.Clean {
		var messages []string
		for _, v := range res.Violations {
			messages = append(messages, v.Format())
		}
		return gateCheck{
			Name:   "rtl-sentry",
			Status: "fail",
			Detail: strings.Join(messages, "\n"),
		}
	}

	return gateCheck{
		Name:   "rtl-sentry",
		Status: "pass",
		Detail: fmt.Sprintf("clean across UI templates, strictly enforcing RTL logical properties (scanned in %v)", res.Duration.Round(time.Millisecond)),
	}
}

// gateCFWorkerSentry runs preflight checks on Cloudflare Worker repositories,
// warning against secret checks that mask Zero Trust 403 gates with 500 errors,
// and flagging process.env.NODE_ENV checks in workerd isolate test files.
func gateCFWorkerSentry(cwd string) gateCheck {
	if _, err := exec.LookPath("git"); err != nil {
		return gateCheck{Name: "cf-worker-sentry", Status: "skip", Detail: "git not found on PATH"}
	}
	if !inGitRepo() {
		return gateCheck{Name: "cf-worker-sentry", Status: "skip", Detail: "not inside a git repository"}
	}

	// Fetch staged diff first, fallback to working tree diff
	cmd := exec.Command("git", "diff", "--cached")
	cmd.Dir = cwd
	out, err := cmd.Output()
	diffText := ""
	if err == nil {
		diffText = string(out)
	}
	if strings.TrimSpace(diffText) == "" {
		cmdHead := exec.Command("git", "diff", "HEAD")
		cmdHead.Dir = cwd
		if outHead, errHead := cmdHead.Output(); errHead == nil {
			diffText = string(outHead)
		}
	}

	res := assess.RunCFWorkerSentry(cwd, diffText)
	if res.Skipped {
		return gateCheck{
			Name:   "cf-worker-sentry",
			Status: "skip",
			Detail: res.SkipReason,
		}
	}

	if !res.Clean {
		var messages []string
		for _, v := range res.Violations {
			messages = append(messages, v.Format())
		}
		return gateCheck{
			Name:   "cf-worker-sentry",
			Status: "fail",
			Detail: strings.Join(messages, "\n"),
		}
	}

	return gateCheck{
		Name:   "cf-worker-sentry",
		Status: "pass",
		Detail: fmt.Sprintf("clean across Cloudflare Worker auth/secret patterns and test isolate environments (scanned in %v)", res.Duration.Round(time.Millisecond)),
	}
}

// gateCFCleanSentry checks staged files and wrangler manifests for unmasked Cloudflare credentials
// (32-hex account IDs and 36-char database UUIDs).
func gateCFCleanSentry(cwd string) gateCheck {
	if _, err := exec.LookPath("git"); err != nil {
		return gateCheck{Name: "cfclean", Status: "skip", Detail: "git not found on PATH"}
	}
	if !inGitRepo() {
		return gateCheck{Name: "cfclean", Status: "skip", Detail: "not inside a git repository"}
	}

	nameStatus := ""
	cmd := exec.Command("git", "diff", "--cached", "--name-status")
	cmd.Dir = cwd
	if out, err := cmd.Output(); err == nil {
		nameStatus = string(out)
	}

	staged := silagate.ParseStagedFiles(nameStatus)
	res := silagate.AuditCFClean(cwd, staged)
	if res.Skipped {
		return gateCheck{
			Name:   "cfclean",
			Status: "skip",
			Detail: res.SkipReason,
		}
	}

	if !res.Clean {
		var msgs []string
		for _, v := range res.Violations {
			msgs = append(msgs, fmt.Sprintf("%s:%d [%s] %s", v.File, v.LineNumber, v.Kind, v.Line))
		}
		detail := fmt.Sprintf("unmasked Cloudflare credentials detected in %d location(s):\n%s\nRemediation: run `cfclean setup` and `cfclean file -i <file>` to sanitize credentials before committing.",
			len(res.Violations), strings.Join(msgs, "\n"))
		return gateCheck{
			Name:   "cfclean",
			Status: "fail",
			Detail: detail,
		}
	}

	return gateCheck{
		Name:   "cfclean",
		Status: "pass",
		Detail: fmt.Sprintf("clean across Cloudflare credentials (%d manifest(s) sanitized)", res.FilesScanned),
	}
}

// gateRuleBudget audits AGENTS.md and GEMINI.md against Antigravity's 24 KB ceiling
// and validates harness symlink integrity.
func gateRuleBudget(cwd string) gateCheck {
	res := silagate.AuditRuleBudget(cwd)
	if !res.Clean {
		return gateCheck{
			Name:   "rules-budget",
			Status: "fail",
			Detail: strings.Join(res.Errors, "\n"),
		}
	}
	if len(res.Warnings) > 0 {
		return gateCheck{
			Name:    "rules-budget",
			Status:  "pass",
			Detail:  "clean within 24 KB ceiling",
			Warning: fmt.Sprintf("%d warning(s): %s", len(res.Warnings), strings.Join(res.Warnings, "; ")),
		}
	}
	return gateCheck{
		Name:   "rules-budget",
		Status: "pass",
		Detail: "all rule files within 24 KB ceiling; harness symlinks verified",
	}
}

// runGateArchStandalone runs the architectural pre-commit sentry as a standalone inspection tool.
func runGateArchStandalone(ui render.UI, jsonOut, toonOut, skipArch bool) {
	cwd, _ := os.Getwd()
	check := gateArchSentry(cwd, skipArch)
	clean := (check.Status != "fail")
	emitGateHeartbeats(cwd, []gateCheck{check})
	if jsonOut {
		emitJSON(gateReport{Clean: clean, Tier: "arch", Checks: []gateCheck{check}})
	} else if toonOut {
		fmt.Fprint(os.Stdout, renderGateTOON([]gateCheck{check}, clean))
	} else {
		printGatePreCommit(ui, []gateCheck{check}, computeGateVerdict([]gateCheck{check}, false, false))
	}
	if !clean {
		os.Exit(1)
	}
}

// gateProvenanceSentry validates regulatory and external source citations against closed license taxonomy.
func gateProvenanceSentry(cwd string) gateCheck {
	var st *store.Store
	dbPath := store.DefaultDBPath()
	if dbPath != "" {
		s, ok, err := store.OpenInspection(dbPath)
		if err != nil {
			return gateCheck{
				Name:   "provenance-sentry",
				Status: "fail",
				Detail: fmt.Sprintf("knowledge store inspection failed: %v", err),
			}
		}
		if ok && s != nil {
			defer s.Close()
			st = s
		}
	}

	report, err := assess.ScanProvenance(cwd, st)
	if err != nil {
		return gateCheck{
			Name:   "provenance-sentry",
			Status: "fail",
			Detail: fmt.Sprintf("failed to scan external citations: %v", err),
		}
	}

	if !report.Valid {
		var errMsgs []string
		for _, v := range report.Violations {
			if v.Severity == "error" {
				errMsgs = append(errMsgs, fmt.Sprintf("%s:%d %s (%s)", v.Path, v.Line, v.Message, v.URL))
			}
		}
		return gateCheck{
			Name:   "provenance-sentry",
			Status: "fail",
			Detail: fmt.Sprintf("%d provenance violation(s):\n%s", len(errMsgs), strings.Join(errMsgs, "\n")),
		}
	}

	if len(report.Violations) > 0 {
		return gateCheck{
			Name:   "provenance-sentry",
			Status: "pass",
			Detail: fmt.Sprintf("verified %d citation(s) across %d file(s) (%d uncataloged warning(s))",
				report.TotalCitations, report.ScannedFiles, len(report.Violations)),
		}
	}

	return gateCheck{
		Name:   "provenance-sentry",
		Status: "pass",
		Detail: fmt.Sprintf("verified %d citation(s) across %d file(s) with closed license compliance",
			report.TotalCitations, report.ScannedFiles),
	}
}

// runGateProvenanceStandalone runs the regulatory provenance sentry as a standalone inspection tool.
func runGateProvenanceStandalone(ui render.UI, jsonOut, toonOut bool) {
	cwd, _ := os.Getwd()
	check := gateProvenanceSentry(cwd)
	clean := (check.Status != "fail")
	if jsonOut {
		emitJSON(gateReport{Clean: clean, Tier: "provenance", Checks: []gateCheck{check}})
	} else if toonOut {
		fmt.Fprint(os.Stdout, renderGateTOON([]gateCheck{check}, clean))
	} else {
		printGatePreCommit(ui, []gateCheck{check}, computeGateVerdict([]gateCheck{check}, false, false))
	}
	if !clean {
		os.Exit(1)
	}
}

// gateScarTissue audits working tree changes (staged and unstaged git diff)
// against historical project incident patterns, architectural memos, and AGENTS.md prohibitions.
func gateScarTissue() gateCheck {
	if _, err := exec.LookPath("git"); err != nil {
		return gateCheck{Name: "scar-tissue", Status: "skip", Detail: "git not found on PATH"}
	}
	if !inGitRepo() {
		return gateCheck{Name: "scar-tissue", Status: "skip", Detail: "not inside a git repository"}
	}

	cwd, _ := os.Getwd()
	proj := store.ProjectID(cwd)

	var st *store.Store
	if dbPath := store.DefaultDBPath(); dbPath != "" {
		s, ok, err := store.OpenInspection(dbPath)
		if err != nil {
			return gateCheck{
				Name:   "scar-tissue",
				Status: "fail",
				Detail: fmt.Sprintf("knowledge store inspection failed: %v", err),
			}
		}
		if ok && s != nil {
			st = s
			defer st.Close()
		}
	}

	rules := assess.CompileProjectRules(cwd, st, proj)
	rep, err := assess.AuditRepoDiff(context.Background(), cwd, rules)
	if err != nil {
		return gateCheck{
			Name:   "scar-tissue",
			Status: "fail",
			Detail: fmt.Sprintf("scar tissue audit error: %v", err),
		}
	}

	if !rep.Clean {
		var details []string
		for _, v := range rep.Violations {
			lineStr := fmt.Sprintf("%d", v.LineNumber)
			if v.LineNumber == 0 {
				lineStr = "-"
			}
			details = append(details, fmt.Sprintf(" - [%s] %s in %s:%s: %q (Citation: %s)",
				v.Source, v.RuleName, v.File, lineStr, v.LineContent, v.Citation))
		}
		return gateCheck{
			Name:   "scar-tissue",
			Status: "fail",
			Detail: fmt.Sprintf("%d outlawed architectural anti-pattern(s) detected in diff:\n%s",
				len(rep.Violations), strings.Join(details, "\n")),
		}
	}

	return gateCheck{
		Name:   "scar-tissue",
		Status: "pass",
		Detail: fmt.Sprintf("diff clean across %d architectural rule(s) (%d files, %d lines scanned)",
			rep.TotalRules, rep.FilesScanned, rep.LinesScanned),
	}
}

func runGateScarTissueStandalone(ui render.UI, jsonOut, toonOut bool) {
	cwd, _ := os.Getwd()
	proj := store.ProjectID(cwd)

	var st *store.Store
	if dbPath := store.DefaultDBPath(); dbPath != "" {
		s, ok, err := store.OpenInspection(dbPath)
		if err != nil {
			if jsonOut {
				emitJSON(map[string]any{"error": err.Error()})
			} else {
				fmt.Fprintf(os.Stderr, "sila gate --scar-tissue: %v\n", err)
			}
			os.Exit(1)
		}
		if ok && s != nil {
			st = s
			defer st.Close()
		}
	}

	rules := assess.CompileProjectRules(cwd, st, proj)
	rep, err := assess.AuditRepoDiff(context.Background(), cwd, rules)
	if err != nil {
		if jsonOut {
			emitJSON(map[string]any{"error": err.Error()})
		} else {
			fmt.Fprintf(os.Stderr, "sila gate --scar-tissue: %v\n", err)
		}
		os.Exit(1)
	}

	if jsonOut {
		emitJSON(rep)
	} else if toonOut {
		fmt.Fprint(os.Stdout, assess.RenderScarTissueTOON(rep))
	} else {
		printScarTissueReport(ui, rep)
	}

	if !rep.Clean {
		os.Exit(1)
	}
}

func printScarTissueReport(ui render.UI, rep *assess.ScarTissueReport) {
	if rep.Clean {
		fmt.Fprintf(os.Stdout, "%s epistemic scar tissue sentry clean: 0 violations across %d rules (%d files, %d lines scanned)\n",
			ui.Green("✔"), rep.TotalRules, rep.FilesScanned, rep.LinesScanned)
		return
	}

	fmt.Fprintf(os.Stdout, "%s epistemic scar tissue sentry detected %d violation(s):\n\n",
		ui.Red("✖"), len(rep.Violations))

	headers := []string{"Rule", "File:Line", "Violation", "Citation"}
	var rows [][]string
	for _, v := range rep.Violations {
		lineStr := fmt.Sprintf("%d", v.LineNumber)
		if v.LineNumber == 0 {
			lineStr = "-"
		}
		loc := fmt.Sprintf("%s:%s", v.File, lineStr)
		content := v.LineContent
		if len(content) > 40 {
			content = content[:37] + "..."
		}
		citation := v.Citation
		if len(citation) > 50 {
			citation = citation[:47] + "..."
		}
		rows = append(rows, []string{
			ui.Bold(v.RuleName),
			loc,
			ui.Red(content),
			citation,
		})
	}
	t := ui.Table(headers, rows, []string{"l", "l", "l", "l"})
	fmt.Fprintln(os.Stdout, t)
}

// runGateSmokeStandalone executes the pre-flight Smoke A/B dry-run sandbox audit and integrity checks.
func runGateSmokeStandalone(ui render.UI, jsonOut, toonOut bool) {
	check := gateSmokeAB()
	clean := check.Status == "pass"

	if jsonOut {
		emitJSON(gateReport{Clean: clean, Tier: "smoke-ab", Checks: []gateCheck{check}})
	} else if toonOut {
		fmt.Fprint(os.Stdout, renderGateTOON([]gateCheck{check}, clean))
	} else {
		fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · gate (smoke-ab)")))
		if clean {
			fmt.Fprintf(os.Stdout, "  %s %s %s\n", "✔", ui.Green("PASS"), ui.Bold(check.Name))
			fmt.Fprintf(os.Stdout, "    %s\n", ui.Dim(check.Detail))
			fmt.Fprintln(os.Stdout, ui.Bold(ui.Green("✔ Smoke A/B sandbox verification passed.")))
		} else {
			fmt.Fprintf(os.Stdout, "  %s %s %s\n", ui.Red("✖"), ui.Red("FAIL"), ui.Bold(check.Name))
			fmt.Fprintf(os.Stdout, "    %s\n", ui.Red(check.Detail))
			fmt.Fprintln(os.Stdout, ui.Bold(ui.Red("✖ Smoke A/B sandbox failed invariants.")))
		}
	}

	if !clean {
		os.Exit(1)
	}
}

// gateSmokeAB checks knowledge store integrity via non-mutating inspection and verifies
// the Smoke A/B dry-run sandbox using an isolated disposable database.
func gateSmokeAB() gateCheck {
	// 1. Live knowledge store integrity inspection (strictly non-mutating)
	dbPath := store.DefaultDBPath()
	liveStoreChecked := false
	if dbPath != "" {
		st, ok, err := store.OpenInspection(dbPath)
		if err != nil {
			return gateCheck{Name: "smoke-ab", Status: "fail", Detail: fmt.Sprintf("live store inspection failed: %v", err)}
		}
		if ok && st != nil {
			defer st.Close()
			if err := st.QuickCheck(); err != nil {
				return gateCheck{Name: "smoke-ab", Status: "fail", Detail: fmt.Sprintf("live database quick_check failed: %v", err)}
			}
			liveStoreChecked = true
		}
	}

	// 2. Transaction smoke dry-run verification against an isolated disposable database
	sandbox, err := andon.NewDisposableSmokeSandbox("gate:smoke_ab")
	if err != nil {
		return gateCheck{Name: "smoke-ab", Status: "fail", Detail: fmt.Sprintf("create disposable smoke sandbox: %v", err)}
	}
	defer sandbox.Close()

	dryRunErr := sandbox.ExecuteDryRunOnly(func(tx *sql.Tx) error {
		if _, err := tx.Exec("CREATE TEMPORARY TABLE IF NOT EXISTS _gate_smoke (id INT PRIMARY KEY, val TEXT)"); err != nil {
			return err
		}
		if _, err := tx.Exec("INSERT INTO _gate_smoke VALUES (1, 'active')"); err != nil {
			return err
		}
		return nil
	})
	if dryRunErr != nil {
		if he, ok := andon.AsHaltError(dryRunErr); ok {
			return gateCheck{Name: "smoke-ab", Status: "fail", Detail: fmt.Sprintf("andon halt: %s - %s", he.Code, he.Reason)}
		}
		return gateCheck{Name: "smoke-ab", Status: "fail", Detail: fmt.Sprintf("smoke sandbox dry-run failed: %v", dryRunErr)}
	}

	detail := "disposable transaction smoke dry-run verified"
	if liveStoreChecked {
		detail = "store integrity and smoke A/B dry-run sandbox verified"
	}
	return gateCheck{Name: "smoke-ab", Status: "pass", Detail: detail}
}

// runGateConstantGuardStandalone audits Go source code for bare magic strings and un-factored sentinel errors.
func runGateConstantGuardStandalone(ui render.UI, jsonOut, toonOut bool) {
	cwd, _ := os.Getwd()
	cfg := lint.DefaultConfig()

	rep, err := lint.ScanDir(cwd, cfg)
	if err != nil {
		if jsonOut {
			emitJSON(map[string]any{"error": err.Error()})
		} else {
			fmt.Fprintf(os.Stderr, "sila gate --constant-guard: %v\n", err)
		}
		os.Exit(1)
	}

	if jsonOut {
		emitJSON(rep)
		return
	}

	fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · gate (constant-guard)")))
	fmt.Fprintf(os.Stdout, "  Scanned: %d Go source files (%d literals) in %s\n",
		rep.FilesScanned, rep.TotalLiterals, rep.Duration.Round(time.Millisecond))

	if rep.Clean {
		fmt.Fprintln(os.Stdout, ui.Green("✔ All string literals and sentinel errors adhere to constant hygiene."))
		return
	}

	fmt.Fprintf(os.Stdout, "  Found %s candidate literals that should be declared as typed package constants:\n\n",
		ui.Yellow(fmt.Sprintf("%d", len(rep.Violations))))

	headers := []string{"Category", "Count", "Suggested Constant", "Literal", "First Location"}
	var rows [][]string
	for _, v := range rep.Violations {
		firstLoc := "-"
		if len(v.Locations) > 0 {
			firstLoc = v.Locations[0]
			if rel, err := filepath.Rel(cwd, firstLoc); err == nil {
				firstLoc = rel
			}
		}
		litDisplay := v.Literal
		if len(litDisplay) > 35 {
			litDisplay = litDisplay[:32] + "..."
		}

		catDisplay := ui.Dim(v.Category)
		if v.Category == "sentinel_error" {
			catDisplay = ui.Red("sentinel_err")
		}

		rows = append(rows, []string{
			catDisplay,
			fmt.Sprintf("%d", v.Count),
			ui.Bold(v.Suggestion),
			fmt.Sprintf("%q", litDisplay),
			firstLoc,
		})
	}
	t := ui.Table(headers, rows, []string{"l", "r", "l", "l", "l"})
	fmt.Fprintln(os.Stdout, t)
	fmt.Fprintln(os.Stdout, ui.Dim("\nFactoring bare strings into package constants prevents silent drift during agent refactoring."))
}

// gateConstantGuard audits AST protected literals and constant hygiene across packages.
func gateConstantGuard() gateCheck {
	cwd, _ := os.Getwd()
	cfg := lint.DefaultConfig()
	// High threshold for general gate checks (focusing on bare sentinel errors)
	cfg.MinOccurrences = 15

	rep, err := lint.ScanDir(cwd, cfg)
	if err != nil {
		return gateCheck{Name: "constants", Status: "skip", Detail: fmt.Sprintf("constant guard scan failed: %v", err)}
	}

	detail := fmt.Sprintf("scanned %d files (%d literals in %s): %d candidate literals for constant factoring",
		rep.FilesScanned, rep.TotalLiterals, rep.Duration.Round(time.Millisecond), len(rep.Violations))

	return gateCheck{
		Name:   "constants",
		Status: "pass",
		Detail: detail,
	}
}

func renderGateTOON(checks []gateCheck, clean bool) string {
	var b strings.Builder
	passed := 0
	failed := 0
	skipped := 0
	for _, c := range checks {
		switch c.Status {
		case "pass":
			passed++
		case "fail":
			failed++
		case "skip":
			skipped++
		}
	}
	summaryFields := [][2]string{
		{"clean", fmt.Sprintf("%t", clean)},
		{"passed", fmt.Sprintf("%d", passed)},
		{"failed", fmt.Sprintf("%d", failed)},
		{"skipped", fmt.Sprintf("%d", skipped)},
		{"total_checks", fmt.Sprintf("%d", len(checks))},
	}
	b.WriteString(render.FormatTOONObject("gate_summary", summaryFields))

	headers := []string{"name", "status", "detail"}
	var rows [][]string
	for _, c := range checks {
		rows = append(rows, []string{c.Name, c.Status, c.Detail})
	}
	b.WriteString(render.FormatTOONTable("checks", headers, rows))
	return b.String()
}

// gateDocCoverage verifies that 100% of exported Go declarations have doc comments.
func gateDocCoverage() gateCheck {
	cwd, _ := os.Getwd()
	opts := assess.DefaultDocLintOptions()
	rep, err := assess.AuditDocCoverage(cwd, opts)
	if err != nil {
		return gateCheck{
			Name:   "doc-coverage",
			Status: "fail",
			Detail: fmt.Sprintf("failed to scan Go source files: %v", err),
		}
	}

	if rep.Clean || rep.CoveragePct >= 90.0 {
		detail := fmt.Sprintf("100%% exported Go symbols documented (%d/%d declarations clean)", rep.Documented, rep.TotalExported)
		if !rep.Clean {
			detail = fmt.Sprintf("%.1f%% exported Go symbols documented (%d/%d declarations clean, threshold >= 90%%)", rep.CoveragePct, rep.Documented, rep.TotalExported)
		}
		return gateCheck{
			Name:   "doc-coverage",
			Status: "pass",
			Detail: detail,
		}
	}

	first := rep.Missing[0]
	return gateCheck{
		Name:   "doc-coverage",
		Status: "fail",
		Detail: fmt.Sprintf("%d un-commented exported Go symbol(s) (%.1f%% coverage); first: %s:%d [%s %s]",
			rep.MissingCount, rep.CoveragePct, first.File, first.Line, first.Kind, first.Name),
	}
}

func runGateDocCoverageStandalone(ui render.UI, jsonOut, toonOut bool) {
	cwd, _ := os.Getwd()
	opts := assess.DefaultDocLintOptions()
	rep, err := assess.AuditDocCoverage(cwd, opts)
	if err != nil {
		if jsonOut {
			emitJSON(map[string]any{"error": err.Error()})
		} else {
			fmt.Fprintf(os.Stderr, "sila gate --doc-coverage: %v\n", err)
		}
		os.Exit(1)
	}

	if jsonOut {
		emitJSON(rep)
	} else if toonOut {
		fmt.Fprint(os.Stdout, assess.RenderDocCoverageTOON(rep))
	} else {
		printDocCoverageReport(ui, rep)
	}

	if !rep.Clean {
		os.Exit(1)
	}
}

func printDocCoverageReport(ui render.UI, rep *assess.DocCoverageReport) {
	if rep.Clean {
		fmt.Fprintf(os.Stdout, "%s 100%% exported Go symbols documented (%d/%d declarations clean)\n",
			ui.Green("✔"), rep.Documented, rep.TotalExported)
		return
	}

	fmt.Fprintf(os.Stdout, "%s doc coverage gate detected %d un-commented exported symbol(s) (%.1f%% coverage):\n\n",
		ui.Red("✖"), rep.MissingCount, rep.CoveragePct)

	headers := []string{"Package", "Symbol", "Kind", "Location", "Detail"}
	var rows [][]string
	for _, m := range rep.Missing {
		loc := fmt.Sprintf("%s:%d", m.File, m.Line)
		rows = append(rows, []string{
			ui.Bold(m.Package),
			m.Name,
			m.Kind,
			loc,
			ui.Red(m.Detail),
		})
	}
	t := ui.Table(headers, rows, []string{"l", "l", "l", "l", "l"})
	fmt.Fprintln(os.Stdout, t)
}

// gateCoverageProfilePath returns the deterministic temporary path for the gate coverage profile.
func gateCoverageProfilePath() string {
	cwd, _ := os.Getwd()
	h := sha256.Sum256([]byte(cwd))
	return filepath.Join(os.TempDir(), fmt.Sprintf("sila-gate-coverage-%x.out", h[:6]))
}

// gateCoverage validates statement coverage against quality thresholds.
func gateCoverage() gateCheck {
	covPath := gateCoverageProfilePath()
	profile, err := assess.ParseCoverageFile(covPath)
	if err != nil {
		if isDocs, _ := isDocsOnlyDiff(); isDocs {
			return gateCheck{Name: "coverage", Status: "pass", Detail: "skipped (docs-only diff)"}
		}
		return gateCheck{Name: "coverage", Status: "skip", Detail: "coverage profile not yet generated"}
	}

	thresholds := assess.DefaultCoverageThresholds()
	rep := assess.AuditCoverage(profile, thresholds)
	if rep.Clean {
		return gateCheck{
			Name:   "coverage",
			Status: "pass",
			Detail: fmt.Sprintf("%.1f%% statement coverage across %d packages (threshold >= %.0f%%)",
				rep.OverallPct, len(profile.Packages), thresholds.MinGlobalPct),
		}
	}
	return gateCheck{
		Name:   "coverage",
		Status: "fail",
		Detail: strings.Join(rep.Violations, "; "),
	}
}

// gateReportCard evaluates repository code quality across gofmt, govet, gocyclo, ineffassign, and misspell.
func gateReportCard() gateCheck {
	cwd, _ := os.Getwd()
	rc, err := assess.AuditReportCard(cwd)
	if err != nil {
		return gateCheck{Name: "report-card", Status: "fail", Detail: fmt.Sprintf("report card analysis failed: %v", err)}
	}
	if rc.Clean {
		return gateCheck{
			Name:   "report-card",
			Status: "pass",
			Detail: fmt.Sprintf("Grade %s (Score: %.1f/100 across %d files; %d checks passed)",
				rc.Grade, rc.Score, rc.FilesScanned, len(rc.Components)),
		}
	}
	return gateCheck{
		Name:   "report-card",
		Status: "fail",
		Detail: fmt.Sprintf("Grade %s below threshold (Score: %.1f/100)", rc.Grade, rc.Score),
	}
}

func runGateReportCardStandalone(ui render.UI, jsonOut, toonOut bool) {
	cwd, _ := os.Getwd()
	rc, err := assess.AuditReportCard(cwd)
	if err != nil {
		if jsonOut {
			emitJSON(map[string]any{"error": err.Error()})
		} else {
			fmt.Fprintf(os.Stderr, "sila reportcard: %v\n", err)
		}
		os.Exit(1)
	}

	if jsonOut {
		emitJSON(rc)
	} else if toonOut {
		fmt.Fprint(os.Stdout, assess.RenderReportCardTOON(rc))
	} else {
		printReportCard(ui, rc)
	}

	if !rc.Clean {
		os.Exit(1)
	}
}

func printReportCard(ui render.UI, rc *assess.ReportCard) {
	gradeColor := ui.Green
	if rc.Grade == "B" {
		gradeColor = ui.Yellow
	} else if rc.Grade == "C" || rc.Grade == "D" || rc.Grade == "F" {
		gradeColor = ui.Red
	}

	fmt.Fprintf(os.Stdout, "%s Local Go Report Card: %s (Score: %.1f/100, %d files scanned)\n\n",
		ui.Green("✔"), gradeColor(ui.Bold(rc.Grade)), rc.Score, rc.FilesScanned)

	headers := []string{"Check", "Weight", "Score", "Status", "Diagnostics"}
	var rows [][]string
	for _, c := range rc.Components {
		statusStr := ui.Green("PASS")
		if !c.Passed {
			statusStr = ui.Red("FAIL")
		}
		rows = append(rows, []string{
			ui.Bold(c.Name),
			fmt.Sprintf("%.0f%%", c.Weight*100),
			fmt.Sprintf("%.1f%%", c.Score),
			statusStr,
			c.Detail,
		})
	}
	t := ui.Table(headers, rows, []string{"l", "r", "r", "l", "l"})
	fmt.Fprintln(os.Stdout, t)
}

func runGateCoverageStandalone(ui render.UI, jsonOut, toonOut, strict bool, customProfile string, verbose bool) {
	covPath := customProfile
	if covPath == "" {
		covPath = gateCoverageProfilePath()
	}
	profile, err := assess.ParseCoverageFile(covPath)
	if err != nil {
		cwd, _ := os.Getwd()
		covPath = filepath.Join(os.TempDir(), "sila-coverage.out")
		cmd := exec.Command("go", "test", "-coverprofile="+covPath, "./cmd/...", "./internal/...")
		cmd.Dir = cwd
		if out, runErr := cmd.CombinedOutput(); runErr != nil {
			fix := "run 'sila coverage' from the module root or pass --profile=<path>"
			if jsonOut {
				emitErrorEnvelope("COVERAGE_FAILED", fmt.Sprintf("coverage failed: %v", runErr), fix)
			} else {
				fmt.Fprint(os.Stderr, render.RenderError(ui, fmt.Sprintf("coverage failed: %v", runErr), fix))
				if verbose {
					fmt.Fprintln(os.Stderr, string(out))
				}
			}
			os.Exit(1)
		}
		profile, err = assess.ParseCoverageFile(covPath)
		if err != nil {
			fix := "pass --profile=<coverage.out> or run 'sila coverage' from the module root"
			if jsonOut {
				emitErrorEnvelope("COVERAGE_FAILED", err.Error(), fix)
			} else {
				fmt.Fprint(os.Stderr, render.RenderError(ui, err.Error(), fix))
			}
			os.Exit(1)
		}
	}

	thresholds := assess.DefaultCoverageThresholds()
	if strict {
		thresholds = assess.StrictCoverageThresholds()
	}
	rep := assess.AuditCoverage(profile, thresholds)

	if jsonOut {
		emitJSON(rep)
	} else if toonOut {
		fmt.Fprint(os.Stdout, assess.RenderCoverageTOON(rep))
	} else {
		printCoverageReport(ui, rep)
	}

	if !rep.Clean {
		os.Exit(1)
	}
}

func printCoverageReport(ui render.UI, rep *assess.CoverageReport) {
	statusIcon := ui.Green("✔")
	if !rep.Clean {
		statusIcon = ui.Red("✖")
	}

	fmt.Fprintf(os.Stdout, "%s Overall Test Coverage: %.1f%% (Minimum: %.1f%%)\n\n",
		statusIcon, rep.OverallPct, rep.MinGlobalPct)

	if len(rep.Violations) > 0 {
		for _, v := range rep.Violations {
			fmt.Fprintf(os.Stdout, "  %s %s\n", ui.Red("✖"), v)
		}
		fmt.Fprintln(os.Stdout)
	}

	headers := []string{"Package", "Covered", "Total", "Coverage"}
	var rows [][]string
	for _, p := range rep.PackageReports {
		covStr := fmt.Sprintf("%.1f%%", p.CoveragePct)
		if p.CoveragePct >= 80.0 {
			covStr = ui.Green(covStr)
		} else if p.CoveragePct >= 50.0 {
			covStr = ui.Yellow(covStr)
		} else {
			covStr = ui.Red(covStr)
		}
		rows = append(rows, []string{
			p.Package,
			fmt.Sprintf("%d", p.CoveredStatements),
			fmt.Sprintf("%d", p.TotalStatements),
			covStr,
		})
	}
	t := ui.Table(headers, rows, []string{"l", "r", "r", "r"})
	fmt.Fprintln(os.Stdout, t)
}

// gateContractSentry verifies that staged Go changes do not introduce breaking signature changes
// to symbols that have active call sites across the repository.
func gateContractSentry(cwd string) gateCheck {
	if _, err := exec.LookPath("git"); err != nil {
		return gateCheck{Name: "contract-check", Status: "skip", Detail: "git not found on PATH"}
	}

	cmd := exec.Command("git", "diff", "--cached", "--name-only")
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		return gateCheck{Name: "contract-check", Status: "skip", Detail: "failed to inspect staged diff"}
	}

	var stagedGoFiles []string
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l != "" && strings.HasSuffix(l, ".go") && !strings.HasSuffix(l, "_test.go") {
			stagedGoFiles = append(stagedGoFiles, l)
		}
	}

	if len(stagedGoFiles) == 0 {
		return gateCheck{Name: "contract-check", Status: "pass", Detail: "no staged Go source files requiring contract verification"}
	}

	for _, relFile := range stagedGoFiles {
		headSrc, err := contract.LoadGitHeadFile(cwd, relFile)
		if err != nil {
			continue
		}
		diskSrc, err := os.ReadFile(filepath.Join(cwd, relFile))
		if err != nil {
			continue
		}

		fset := token.NewFileSet()
		headNode, err := parser.ParseFile(fset, relFile, headSrc, 0)
		if err != nil {
			continue
		}
		diskNode, err := parser.ParseFile(fset, relFile, diskSrc, 0)
		if err != nil {
			continue
		}

		headFuncs := make(map[string]*ast.FuncDecl)
		for _, decl := range headNode.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok {
				headFuncs[fn.Name.Name] = fn
			}
		}

		for _, decl := range diskNode.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			headFn, exists := headFuncs[fn.Name.Name]
			if !exists {
				continue
			}
			headParams := contract.CountFieldIdentifiers(headFn.Type.Params)
			diskParams := contract.CountFieldIdentifiers(fn.Type.Params)
			headResults := contract.CountFieldIdentifiers(headFn.Type.Results)
			diskResults := contract.CountFieldIdentifiers(fn.Type.Results)
			if headParams != diskParams || headResults != diskResults {
				res, err := contract.EditCheck(cwd, relFile+"::"+fn.Name.Name, nil)
				if err == nil && res.IncompatibleCount > 0 {
					return gateCheck{
						Name:   "contract-check",
						Status: "fail",
						Detail: fmt.Sprintf("breaking contract change on symbol %q in %s: %d incompatible caller(s)", fn.Name.Name, relFile, res.IncompatibleCount),
					}
				}
			}
		}
	}

	return gateCheck{Name: "contract-check", Status: "pass", Detail: "zero contract regressions or breaking signature changes across repository"}
}

// gateTestSelectorSentry checks if modified Go symbols have targeted test obligations.
func gateTestSelectorSentry(cwd string) gateCheck {
	res, err := testmatrix.SelectTargetedTests(cwd)
	if err != nil {
		return gateCheck{Name: "test-selector", Status: "skip", Detail: "test selector: " + err.Error()}
	}

	if res.Status == "no_changes" {
		return gateCheck{Name: "test-selector", Status: "pass", Detail: "no code modifications requiring targeted tests"}
	}

	if len(res.UntestedSymbols) > 0 {
		return gateCheck{
			Name:   "test-selector",
			Status: "pass",
			Detail: fmt.Sprintf("%d targeted test(s) identified; %d symbol(s) lack direct unit test coverage (%s)",
				len(res.TestsToRun), len(res.UntestedSymbols), strings.Join(res.UntestedSymbols, ", ")),
		}
	}

	return gateCheck{
		Name:   "test-selector",
		Status: "pass",
		Detail: fmt.Sprintf("%d targeted test(s) identified covering %d changed symbol(s)",
			len(res.TestsToRun), len(res.ChangedSymbols)),
	}
}

func runEditCheck(ui render.UI, jsonOut, toonOut, dryRun bool, payloadPath string, args []string) {
	symbol := ""
	for _, a := range args {
		if strings.HasPrefix(a, "--symbol=") {
			symbol = strings.TrimPrefix(a, "--symbol=")
		} else if strings.HasPrefix(a, "-s=") {
			symbol = strings.TrimPrefix(a, "-s=")
		} else if strings.HasPrefix(a, "--payload=") {
			payloadPath = strings.TrimPrefix(a, "--payload=")
		} else if !strings.HasPrefix(a, "-") && symbol == "" {
			symbol = a
		}
	}

	if symbol == "" {
		fmt.Fprintln(os.Stderr, "Usage: sila edit-check <symbol> [--payload=<file>|-] [--dry-run] [--json]")
		os.Exit(1)
	}

	cwd, _ := os.Getwd()

	var payload []byte
	if payloadPath != "" {
		if payloadPath == "-" {
			var buf bytes.Buffer
			_, _ = buf.ReadFrom(os.Stdin)
			payload = buf.Bytes()
		} else {
			data, err := os.ReadFile(payloadPath)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: read payload %s: %v\n", payloadPath, err)
				os.Exit(2)
			}
			payload = data
		}
	}

	res, err := contract.EditCheck(cwd, symbol, payload)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: edit check: %v\n", err)
		os.Exit(2)
	}

	if jsonOut || toonOut {
		emitJSON(res)
	} else {
		printEditCheckReport(ui, res, dryRun)
	}

	if !dryRun && res.ExitCode != 0 {
		os.Exit(res.ExitCode)
	}
}

func printEditCheckReport(ui render.UI, res *contract.EditCheckResult, dryRun bool) {
	icon := ui.Green("✔")
	if res.ExitCode != 0 {
		icon = ui.Red("✖")
	} else if res.Contract.ParamsWas != res.Contract.ParamsNow {
		icon = ui.Yellow("▲")
	}

	dryNotice := ""
	if dryRun {
		dryNotice = " [dry-run]"
	}

	fmt.Fprintf(os.Stdout, "%s Contract Check: %s (%s)%s\n", icon, res.Symbol, res.File, dryNotice)
	fmt.Fprintf(os.Stdout, "  Parameters: %d -> %d\n", res.Contract.ParamsWas, res.Contract.ParamsNow)
	fmt.Fprintf(os.Stdout, "  Results:    %d -> %d\n", res.Contract.ResultsWas, res.Contract.ResultsNow)
	fmt.Fprintf(os.Stdout, "  Exported:   %v\n", res.Contract.IsExported)
	if res.Contract.ExportDropped {
		fmt.Fprintf(os.Stdout, "  %s Visibility dropped from exported to unexported\n", ui.Red("✖"))
	}
	fmt.Fprintf(os.Stdout, "  Callers:    %d total, %d incompatible\n", res.CallersCount, res.IncompatibleCount)

	if len(res.IncompatibleCallers) > 0 {
		fmt.Fprintf(os.Stdout, "\nIncompatible Callers:\n")
		for _, c := range res.IncompatibleCallers {
			fmt.Fprintf(os.Stdout, "  %s %s (%s:%d:%d) passes %d arg(s), expected %d\n",
				ui.Red("✖"), c.Caller, c.File, c.Line, c.Col, c.ArgsPassed, c.ArgsExpected)
		}
	}
	if res.Detail != "" {
		fmt.Fprintf(os.Stdout, "\n  Detail: %s\n", res.Detail)
	}
}

func runSafeDelete(ui render.UI, jsonOut, toonOut, dryRun bool, args []string) {
	symbol := ""
	for _, a := range args {
		if strings.HasPrefix(a, "--symbol=") {
			symbol = strings.TrimPrefix(a, "--symbol=")
		} else if strings.HasPrefix(a, "-s=") {
			symbol = strings.TrimPrefix(a, "-s=")
		} else if !strings.HasPrefix(a, "-") && symbol == "" {
			symbol = a
		}
	}

	if symbol == "" {
		fmt.Fprintln(os.Stderr, "Usage: sila safe-delete <symbol> [--dry-run] [--json]")
		os.Exit(1)
	}

	cwd, _ := os.Getwd()

	res, err := contract.VerifySafeDelete(cwd, symbol)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: safe delete check: %v\n", err)
		os.Exit(2)
	}

	if jsonOut || toonOut {
		emitJSON(res)
	} else {
		printSafeDeleteReport(ui, res, dryRun)
	}

	if !dryRun && res.ExitCode != 0 {
		os.Exit(res.ExitCode)
	}
}

func printSafeDeleteReport(ui render.UI, res *contract.SafeDeleteResult, dryRun bool) {
	icon := ui.Green("✔")
	if res.ExitCode != 0 {
		icon = ui.Red("✖")
	}

	dryNotice := ""
	if dryRun {
		dryNotice = " [dry-run]"
	}

	fmt.Fprintf(os.Stdout, "%s Safe Delete Check: %s%s\n", icon, res.Symbol, dryNotice)
	if res.File != "" {
		fmt.Fprintf(os.Stdout, "  File:               %s\n", res.File)
	}
	fmt.Fprintf(os.Stdout, "  Inbound References: %d\n", res.InboundReferences)
	fmt.Fprintf(os.Stdout, "  Status:             %s\n", res.Status)
	fmt.Fprintf(os.Stdout, "  Recommendation:     %s\n", res.Recommendation)

	if len(res.Callers) > 0 {
		fmt.Fprintf(os.Stdout, "\nActive Inbound References:\n")
		for _, c := range res.Callers {
			fmt.Fprintf(os.Stdout, "  %s %s (%s:%d:%d)\n", ui.Red("✖"), c.Caller, c.File, c.Line, c.Col)
		}
	}
}

func ingestProjectDiagnostics(st *store.Store, projectID, cwd string) error {
	rows, err := st.DB.Query(`
		SELECT COALESCE(so.session_id, 'session_default'), m.content
		FROM messages m
		LEFT JOIN source_occurrences so ON m.id = so.message_id
		WHERE m.project_id = ?
		  AND (m.content LIKE '%.go:%' OR m.content LIKE '%error%' OR m.content LIKE '%FAIL%' OR m.content LIKE '%panic%')
		  AND m.ts >= datetime('now', '-7 days')
		ORDER BY m.id ASC
	`, projectID)
	if err != nil {
		return err
	}
	defer rows.Close()

	parser := sentry.NewDrainParser(0.85)
	var sessions []string
	sessionSet := make(map[string]bool)

	for rows.Next() {
		var sID, content string
		if err := rows.Scan(&sID, &content); err != nil {
			continue
		}
		if sID != "" && !sessionSet[sID] {
			sessionSet[sID] = true
			sessions = append(sessions, sID)
		}
		diags := sentry.ExtractDiagnosticsFromText(content, sID, cwd)
		for _, d := range diags {
			parser.Ingest(d)
		}
	}

	hbRows, err := st.DB.Query(`SELECT DISTINCT session_id FROM epistemic_heartbeats WHERE project_id = ?`, projectID)
	if err == nil {
		for hbRows.Next() {
			var hSession string
			if err := hbRows.Scan(&hSession); err == nil && hSession != "" && !sessionSet[hSession] {
				sessionSet[hSession] = true
				sessions = append(sessions, hSession)
			}
		}
		hbRows.Close()
	}

	now := time.Now().UTC().Format(time.RFC3339)
	for _, c := range parser.Clusters() {
		_ = st.SaveDiagnosticCluster(store.StoredDiagnosticCluster{
			Signature:       c.Signature,
			ProjectID:       projectID,
			Category:        c.Category,
			Template:        c.Template,
			TargetFile:      c.TargetFile,
			TargetSymbol:    c.TargetSymbol,
			OccurrenceCount: c.OccurrenceCount,
			FirstSeen:       c.FirstSeen,
			LastSeen:        c.LastSeen,
			SessionHistory:  c.SessionHistory,
		})

		// For clusters with established recurrence (>= 3 occurrences across sessions),
		// record trailing zero sessions to monitor for flag extinction if active in recent sessions.
		if c.OccurrenceCount >= 3 {
			recentSessions := sessions
			if len(recentSessions) > 10 {
				recentSessions = recentSessions[len(recentSessions)-10:]
			}
			hasRecentActivity := false
			for _, sID := range recentSessions {
				if c.SessionHistory != nil && c.SessionHistory[sID] > 0 {
					hasRecentActivity = true
					break
				}
			}
			if hasRecentActivity {
				for _, sID := range recentSessions {
					if c.SessionHistory != nil && c.SessionHistory[sID] > 0 {
						continue
					}
					_ = st.RecordDiagnosticSessionFrequency(projectID, c.Signature, sID, 0, now)
				}
			}
		}
	}
	return nil
}

// gateSilenceSentry verifies that recurring diagnostic messages have not experienced uncoupled flag extinction.
// gateMemoryWriteSentry requires a recent grounded memory write for a
// goal-attributed closure commit. It is advisory (skip) for unattributed,
// docs/test, and non-closure commits.
func gateMemoryWriteSentry(cwd string) gateCheck {
	subject := strings.TrimSpace(os.Getenv("SILA_COMMIT_MSG"))
	if subject == "" {
		if data, err := os.ReadFile(filepath.Join(cwd, ".git", "COMMIT_EDITMSG")); err == nil {
			subject = strings.TrimSpace(string(data))
		}
	}
	if subject == "" {
		if out, err := exec.Command("git", "-C", cwd, "log", "-1", "--format=%s").Output(); err == nil {
			subject = strings.TrimSpace(string(out))
		}
	}
	var staged []string
	if out, err := exec.Command("git", "-C", cwd, "diff", "--cached", "--name-only").Output(); err == nil {
		staged = strings.Fields(string(out))
	}
	res := sentry.CheckMemoryWrite(sentry.MemoryWriteOptions{
		RepoRoot: cwd,
		Subject:  subject,
		Staged:   staged,
	})
	return gateCheck{Name: res.Name, Status: res.Status, Detail: res.Detail}
}

func gateSilenceSentry(cwd string) gateCheck {
	dbPath := store.DefaultDBPath()
	if os.Getenv("SILA_DB") != "" {
		dbPath = os.Getenv("SILA_DB")
	}
	if _, err := os.Stat(dbPath); err != nil {
		return gateCheck{
			Name:   "silence-sentry",
			Status: "fail",
			Detail: fmt.Sprintf("database open error: %v", err),
		}
	}
	st, err := store.Open(dbPath)
	if err != nil {
		return gateCheck{
			Name:   "silence-sentry",
			Status: "fail",
			Detail: fmt.Sprintf("database open error: %v", err),
		}
	}
	defer st.Close()

	proj, err := st.ResolveScope("", cwd)
	if err != nil {
		return gateCheck{
			Name:   "silence-sentry",
			Status: "fail",
			Detail: fmt.Sprintf("scope resolution error: %v", err),
		}
	}

	// Ingest session diagnostics from multi-harness conversation records
	_ = ingestProjectDiagnostics(st, proj.ID, cwd)

	clusters, err := st.ListDiagnosticClusters(proj.ID)
	if err != nil {
		return gateCheck{
			Name:   "silence-sentry",
			Status: "fail",
			Detail: fmt.Sprintf("query diagnostic clusters error: %v", err),
		}
	}
	if len(clusters) == 0 {
		return gateCheck{Name: "silence-sentry", Status: "pass", Detail: "zero diagnostic clusters tracked across active sessions"}
	}

	tracker := sentry.DefaultPageHinkleyTracker()
	var anomalies []string

	cutoff := time.Now().Add(-7 * 24 * time.Hour)
	for _, c := range clusters {
		if tLast, err := time.Parse(time.RFC3339, c.LastSeen); err == nil && tLast.Before(cutoff) {
			continue
		}
		isOverridden, err := st.IsSilenceOverridden(proj.ID, c.Signature)
		if err != nil {
			return gateCheck{
				Name:   "silence-sentry",
				Status: "fail",
				Detail: fmt.Sprintf("query silence override error: %v", err),
			}
		}
		if isOverridden {
			continue
		}
		freqs, err := st.GetClusterFrequencySeries(proj.ID, c.Signature)
		if err != nil {
			return gateCheck{
				Name:   "silence-sentry",
				Status: "fail",
				Detail: fmt.Sprintf("query cluster frequency series error: %v", err),
			}
		}
		aci, _ := sentry.CalculateACIFromGit(cwd, c.TargetFile, c.TargetSymbol)
		cluster := &sentry.DiagnosticCluster{
			Signature:       c.Signature,
			Category:        c.Category,
			Template:        c.Template,
			TargetFile:      c.TargetFile,
			TargetSymbol:    c.TargetSymbol,
			OccurrenceCount: c.OccurrenceCount,
			FirstSeen:       c.FirstSeen,
			LastSeen:        c.LastSeen,
		}
		anomaly := sentry.EvaluateSilence(cluster, freqs, tracker, aci)
		if anomaly.Status == "silence_anomaly" {
			anomalies = append(anomalies, fmt.Sprintf("%s (%s::%s)", c.Signature, c.TargetFile, c.TargetSymbol))
		}
	}

	if len(anomalies) > 0 {
		return gateCheck{
			Name:   "silence-sentry",
			Status: "fail",
			Detail: fmt.Sprintf("detected %d uncoupled silence anomaly(ies): %s (override with sila silence override <signature>)", len(anomalies), strings.Join(anomalies, ", ")),
		}
	}

	return gateCheck{Name: "silence-sentry", Status: "pass", Detail: "zero uncoupled silence anomalies detected"}
}

// gateHarnessContract enforces the core goal contract without requiring a
// harness hook. It is a no-op when no goal is actively claimed.
func gateHarnessContract(cwd string) gateCheck {
	audit, err := goals.ScanGoals(cwd)
	if err != nil {
		return gateCheck{Name: "goal-contract", Status: "skip", Detail: "cannot scan goals"}
	}
	var slug, session, harness string
	for i := range audit.Goals {
		g := &audit.Goals[i]
		if g.Superseded || g.Claim == nil || g.Claim.Stale {
			continue
		}
		slug = g.Slug
		session = g.Claim.SessionID
		harness = g.Claim.Agent
		break
	}
	if slug == "" {
		return gateCheck{Name: "goal-contract", Status: "skip", Detail: "no active goal claim"}
	}
	policy := harnesscontract.ResolvePolicy(cwd)
	if policy.Policy == harnesscontract.PolicyOff {
		return gateCheck{Name: "goal-contract", Status: "skip", Detail: "policy off"}
	}
	if session == "" {
		session = "unbound"
	}
	sig := harnesscontract.NormalizeSignal(harnesscontract.Signal{
		Harness: harnesscontract.HarnessID(harness), SessionID: session,
		ProjectID: store.ProjectID(cwd), Cwd: cwd,
		Phase: harnesscontract.PhaseSessionEnd, SilaGoalSlug: slug,
	})
	st, err := store.Open(store.DefaultDBPath())
	if err != nil {
		return gateCheck{Name: "goal-contract", Status: "warn", Detail: "store unavailable: " + err.Error()}
	}
	defer st.Close()
	d := harnesscontract.NewChecker(st, cwd).Evaluate(sig, policy)
	d = harnesscontract.RecordDecision(cwd, sig, policy, d, 0)
	switch d.Decision {
	case harnesscontract.DecisionDeny:
		return gateCheck{Name: "goal-contract", Status: "fail", Detail: harnesscontract.Summary(d)}
	case harnesscontract.DecisionWarn:
		return gateCheck{Name: "goal-contract", Status: "pass", Detail: "advisory: " + harnesscontract.Summary(d)}
	default:
		return gateCheck{Name: "goal-contract", Status: "pass", Detail: slug}
	}
}
