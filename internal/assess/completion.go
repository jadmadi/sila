package assess

import (
	"bufio"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/jadmadi/sila/internal/goals"
)

// RuleOfFiveReport captures the results of the 5-point completion verification protocol:
// 1. Whole-Tree Execution
// 2. Stdout/Stderr Stream Inspection
// 3. Verbatim Requirement Reconciliation
// 4. Collateral Blast-Radius Auditing
// 5. Honest Incompleteness Disclosure
type RuleOfFiveReport struct {
	Clean          bool                           `json:"clean"`
	GoalSlug       string                         `json:"goal_slug,omitempty"`
	WholeTree      WholeTreeResult                `json:"whole_tree"`
	StreamInspect  StreamInspectionResult         `json:"stream_inspect"`
	Reconciliation ReconciliationResult           `json:"reconciliation"`
	BlastRadius    BlastRadiusResult              `json:"blast_radius"`
	Disclosure     IncompletenessDisclosureResult `json:"disclosure"`
	Violations     []string                       `json:"violations,omitempty"`
}

// WholeTreeResult audits whether full workspace/package-tree execution occurred.
type WholeTreeResult struct {
	Passed          bool     `json:"passed"`
	ScopeChecked    string   `json:"scope_checked"`
	PackagesTotal   int      `json:"packages_total"`
	PackagesTested  int      `json:"packages_tested"`
	SkippedPackages []string `json:"skipped_packages,omitempty"`
	Details         string   `json:"details"`
}

// StreamInspectionResult audits stdout/stderr for skipped tests, linter ignores, and silent warnings.
type StreamInspectionResult struct {
	Passed         bool     `json:"passed"`
	SkippedTests   int      `json:"skipped_tests"`
	LinterIgnores  int      `json:"linter_ignores"`
	SilentWarnings int      `json:"silent_warnings"`
	Offenses       []string `json:"offenses,omitempty"`
	Details        string   `json:"details"`
}

// ReconciliationResult verifies verbatim requirements against implemented files and tests.
type ReconciliationResult struct {
	Passed              bool     `json:"passed"`
	RequirementsTotal   int      `json:"requirements_total"`
	RequirementsCovered int      `json:"requirements_covered"`
	HollowStubsDetected []string `json:"hollow_stubs_detected,omitempty"`
	MissingTouchedFiles []string `json:"missing_touched_files,omitempty"`
	Details             []string `json:"details,omitempty"`
}

// BlastRadiusResult verifies that modifications did not leak beyond intended charter boundaries.
type BlastRadiusResult struct {
	Passed          bool     `json:"passed"`
	AllowedPaths    []string `json:"allowed_paths"`
	ModifiedFiles   []string `json:"modified_files"`
	CollateralLeaks []string `json:"collateral_leaks,omitempty"`
	Details         string   `json:"details"`
}

// IncompletenessDisclosureResult verifies that partial or unverified areas are explicitly documented.
type IncompletenessDisclosureResult struct {
	Passed             bool     `json:"passed"`
	HasDisclosedIssues bool     `json:"has_disclosed_issues"`
	UndisclosedSkips   []string `json:"undisclosed_skips,omitempty"`
	Disclosures        []string `json:"disclosures,omitempty"`
	Details            string   `json:"details"`
}

// RuleOfFiveOptions provides configuration for completion verification.
type RuleOfFiveOptions struct {
	AllowSkippedTests  bool `json:"allow_skipped_tests"`
	AllowWarnings      bool `json:"allow_warnings"`
	StrictBlastRadius  bool `json:"strict_blast_radius"`
	RequireFullTreeLog bool `json:"require_full_tree_log"`
}

var (
	testSkipRegex   = regexp.MustCompile(`(?i)(?:---\s*SKIP:|===\s*SKIP:|\b(?:test\s+skipped|t\.Skip\b))`)
	noTestFileRegex = regexp.MustCompile(`(?m)^\?\s+([^\s]+)\s+\[no test files\]`)
	okPkgRegex      = regexp.MustCompile(`(?m)^(?:ok|FAIL)\s+([^\s]+)`)
	linterIgnoreRe  = regexp.MustCompile(`(?i)(?://nolint|//lint:ignore|eslint-disable|oxlint-disable)`)
	warningRe       = regexp.MustCompile(`(?i)(?:\bwarning:|\b\[warn\]|\bdeprecated:|\bwarning\b)`)
	hollowStubRe    = regexp.MustCompile(`(?i)(?:panic\(["'][^"']*(?:TODO|implement|unimplemented)[^"']*["']\)|//\s*(?:TODO|FIXME):\s*implement)`)
)

// AuditWholeTree checks whether test execution covered the whole package tree.
func AuditWholeTree(workspaceRoot string, testOutput string) WholeTreeResult {
	res := WholeTreeResult{
		Passed:       true,
		ScopeChecked: "./...",
	}

	packagesOnDisk := discoverGoPackages(workspaceRoot)
	res.PackagesTotal = len(packagesOnDisk)

	if testOutput == "" {
		if res.PackagesTotal > 0 {
			res.Passed = false
			res.Details = "empty test execution output: whole-tree execution unverified"
		} else {
			res.Details = "no Go packages found on disk"
		}
		return res
	}

	testedMap := make(map[string]bool)
	for _, m := range okPkgRegex.FindAllStringSubmatch(testOutput, -1) {
		if len(m) > 1 {
			testedMap[m[1]] = true
		}
	}
	for _, m := range noTestFileRegex.FindAllStringSubmatch(testOutput, -1) {
		if len(m) > 1 {
			testedMap[m[1]] = true
		}
	}

	res.PackagesTested = len(testedMap)

	if len(packagesOnDisk) > 0 {
		var skipped []string
		for _, pkg := range packagesOnDisk {
			relPkg := filepath.Clean(pkg)
			matched := false
			for tested := range testedMap {
				if strings.HasSuffix(tested, relPkg) || relPkg == "." {
					matched = true
					break
				}
			}
			if !matched {
				skipped = append(skipped, relPkg)
			}
		}
		res.SkippedPackages = skipped
		if len(skipped) > 0 && len(testedMap) > 0 {
			res.Passed = false
			res.Details = fmt.Sprintf("%d of %d package(s) missing from execution: %s", len(skipped), len(packagesOnDisk), strings.Join(skipped, ", "))
			return res
		}
	}

	if res.PackagesTested == 0 && res.PackagesTotal > 0 {
		res.Passed = false
		res.Details = "zero packages verified in test stream"
		return res
	}

	res.Details = fmt.Sprintf("whole-tree verification confirmed across %d package(s)", res.PackagesTested)
	return res
}

// InspectStreams audits stdout and stderr logs for skipped tests, linter ignores, and silent warnings.
func InspectStreams(stdout, stderr string) StreamInspectionResult {
	res := StreamInspectionResult{Passed: true}
	combined := stdout + "\n" + stderr
	scanner := bufio.NewScanner(strings.NewReader(combined))

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		if testSkipRegex.MatchString(trimmed) || strings.Contains(trimmed, "[no test files]") {
			res.SkippedTests++
			res.Offenses = append(res.Offenses, fmt.Sprintf("skipped test: %s", trimmed))
		}
		if linterIgnoreRe.MatchString(trimmed) {
			res.LinterIgnores++
			res.Offenses = append(res.Offenses, fmt.Sprintf("linter suppression: %s", trimmed))
		}
		if warningRe.MatchString(trimmed) && !strings.Contains(trimmed, "no active") {
			res.SilentWarnings++
			res.Offenses = append(res.Offenses, fmt.Sprintf("silent warning: %s", trimmed))
		}
	}

	if res.SkippedTests > 0 || res.LinterIgnores > 0 || res.SilentWarnings > 0 {
		res.Passed = false
		res.Details = fmt.Sprintf("stream defects found: %d skipped tests, %d linter ignores, %d warnings",
			res.SkippedTests, res.LinterIgnores, res.SilentWarnings)
	} else {
		res.Details = "stdout and stderr streams clean: zero skips, suppressions, or warnings"
	}

	return res
}

// ReconcileRequirements reconciles goal acceptance criteria and touched files against source code.
func ReconcileRequirements(rootDir, goalSlug string, changedFiles []string) ReconciliationResult {
	res := ReconciliationResult{Passed: true}
	goalDir := filepath.Join(rootDir, "goals", goalSlug)
	goalMdPath := filepath.Join(goalDir, "goal.md")

	data, err := os.ReadFile(goalMdPath)
	if err != nil {
		res.Passed = false
		res.Details = append(res.Details, fmt.Sprintf("goal.md not readable: %v", err))
		return res
	}

	content := string(data)
	criteria := extractAcceptanceCriteria(content)
	res.RequirementsTotal = len(criteria)
	res.RequirementsCovered = len(criteria)

	declaredTouch := extractFilesToTouch(content)
	for _, dt := range declaredTouch {
		clean := filepath.Clean(dt)
		fullPath := filepath.Join(rootDir, clean)
		if _, statErr := os.Stat(fullPath); os.IsNotExist(statErr) {
			if isStagedDeletion(rootDir, clean) {
				continue
			}
			res.MissingTouchedFiles = append(res.MissingTouchedFiles, clean)
		}
	}

	if len(res.MissingTouchedFiles) > 0 {
		res.Passed = false
		res.Details = append(res.Details, fmt.Sprintf("declared files to touch missing on disk: %s", strings.Join(res.MissingTouchedFiles, ", ")))
	}

	filesToCheck := changedFiles
	if len(filesToCheck) == 0 {
		filesToCheck = declaredTouch
	}

	for _, f := range filesToCheck {
		fullPath := f
		if !filepath.IsAbs(fullPath) {
			fullPath = filepath.Join(rootDir, f)
		}
		if !strings.HasSuffix(f, ".go") || strings.HasSuffix(f, "_test.go") {
			continue
		}
		stubs := findHollowStubsInFile(fullPath)
		for _, s := range stubs {
			res.HollowStubsDetected = append(res.HollowStubsDetected, fmt.Sprintf("%s: %s", filepath.Base(f), s))
		}
	}

	if len(res.HollowStubsDetected) > 0 {
		res.Passed = false
		res.Details = append(res.Details, fmt.Sprintf("hollow stubs detected (%d): %s", len(res.HollowStubsDetected), strings.Join(res.HollowStubsDetected, "; ")))
	}

	return res
}

// AuditBlastRadius checks modified files against allowed charter and touched files boundaries.
func AuditBlastRadius(rootDir, goalSlug string, modifiedFiles []string) BlastRadiusResult {
	res := BlastRadiusResult{
		Passed:        true,
		ModifiedFiles: modifiedFiles,
	}

	allowed := make(map[string]bool)
	if ch, chErr := goals.LoadCharter(rootDir, goalSlug); chErr == nil && ch != nil {
		for _, p := range ch.AllowPaths {
			allowed[filepath.Clean(p)] = true
			res.AllowedPaths = append(res.AllowedPaths, p)
		}
	}

	if len(allowed) == 0 {
		goalMdPath := filepath.Join(rootDir, "goals", goalSlug, "goal.md")
		if data, err := os.ReadFile(goalMdPath); err == nil {
			for _, dt := range extractFilesToTouch(string(data)) {
				allowed[filepath.Clean(dt)] = true
				res.AllowedPaths = append(res.AllowedPaths, dt)
			}
		}
	}

	for _, m := range modifiedFiles {
		cleanM := filepath.Clean(m)
		if isAllowedAuxiliaryFile(cleanM, goalSlug) {
			continue
		}

		matched := false
		for p := range allowed {
			if cleanM == p || strings.HasPrefix(cleanM, p+string(filepath.Separator)) || goals.MatchCharterPath(cleanM, p) {
				matched = true
				break
			}
		}

		if !matched {
			res.CollateralLeaks = append(res.CollateralLeaks, cleanM)
		}
	}

	if len(res.CollateralLeaks) > 0 {
		res.Passed = false
		res.Details = fmt.Sprintf("collateral blast-radius violation: %d unauthorized file(s) modified: %s",
			len(res.CollateralLeaks), strings.Join(res.CollateralLeaks, ", "))
	} else {
		res.Details = "all modifications strictly bounded within goal charter"
	}

	return res
}

// AuditIncompletenessDisclosure verifies that any partial completion or skipped items are disclosed.
func AuditIncompletenessDisclosure(factsPath, goalMdPath string, detectedIssues []string) IncompletenessDisclosureResult {
	return auditDisclosures(nil, factsPath, goalMdPath, detectedIssues)
}

func auditDisclosures(extraDisclosures []string, factsPath, goalMdPath string, detectedIssues []string) IncompletenessDisclosureResult {
	res := IncompletenessDisclosureResult{Passed: true}

	var docText string
	if data, err := os.ReadFile(factsPath); err == nil {
		docText += "\n" + string(data)
	}
	if data, err := os.ReadFile(goalMdPath); err == nil {
		docText += "\n" + string(data)
	}

	disclosures := extractDisclosedLimitations(docText)
	disclosures = append(disclosures, extraDisclosures...)
	res.Disclosures = disclosures
	res.HasDisclosedIssues = len(disclosures) > 0

	if len(detectedIssues) == 0 {
		res.Details = "no defects or partial completions detected"
		return res
	}

	var undisclosed []string
	for _, issue := range detectedIssues {
		issueLower := strings.ToLower(issue)
		found := false
		for _, disc := range disclosures {
			discLower := strings.ToLower(disc)
			words := strings.Fields(issueLower)
			matchCount := 0
			for _, w := range words {
				if len(w) > 3 && strings.Contains(discLower, w) {
					matchCount++
				}
			}
			if matchCount >= 2 || strings.Contains(discLower, issueLower) {
				found = true
				break
			}
		}
		if !found {
			undisclosed = append(undisclosed, issue)
		}
	}

	res.UndisclosedSkips = undisclosed
	if len(undisclosed) > 0 {
		res.Passed = false
		res.Details = fmt.Sprintf("silent incompleteness omission: %d undisclosed issue(s): %s",
			len(undisclosed), strings.Join(undisclosed, "; "))
	} else {
		res.Details = "all detected limitations explicitly and honestly disclosed in documentation"
	}

	return res
}

// VerifyRuleOfFive executes all five points of the Rule-of-Five Completion Verification Protocol.
func VerifyRuleOfFive(rootDir, goalSlug, stdout, stderr string, modifiedFiles []string, opts RuleOfFiveOptions) *RuleOfFiveReport {
	rep := &RuleOfFiveReport{
		Clean:    true,
		GoalSlug: goalSlug,
	}

	rep.WholeTree = AuditWholeTree(rootDir, stdout)
	if !rep.WholeTree.Passed && rep.WholeTree.PackagesTotal > 0 {
		rep.Violations = append(rep.Violations, "Point 1 (Whole-Tree): "+rep.WholeTree.Details)
	}

	rep.StreamInspect = InspectStreams(stdout, stderr)
	if !rep.StreamInspect.Passed {
		if !opts.AllowSkippedTests && rep.StreamInspect.SkippedTests > 0 {
			rep.Violations = append(rep.Violations, fmt.Sprintf("Point 2 (Stream Inspect): %d skipped test(s) detected", rep.StreamInspect.SkippedTests))
		}
		if rep.StreamInspect.LinterIgnores > 0 {
			rep.Violations = append(rep.Violations, fmt.Sprintf("Point 2 (Stream Inspect): %d linter suppression(s) detected", rep.StreamInspect.LinterIgnores))
		}
		if !opts.AllowWarnings && rep.StreamInspect.SilentWarnings > 0 {
			rep.Violations = append(rep.Violations, fmt.Sprintf("Point 2 (Stream Inspect): %d silent warning(s) detected", rep.StreamInspect.SilentWarnings))
		}
	}

	if goalSlug != "" {
		rep.Reconciliation = ReconcileRequirements(rootDir, goalSlug, modifiedFiles)
		if !rep.Reconciliation.Passed {
			for _, d := range rep.Reconciliation.Details {
				rep.Violations = append(rep.Violations, "Point 3 (Reconciliation): "+d)
			}
		}

		rep.BlastRadius = AuditBlastRadius(rootDir, goalSlug, modifiedFiles)
		if !rep.BlastRadius.Passed {
			rep.Violations = append(rep.Violations, "Point 4 (Blast Radius): "+rep.BlastRadius.Details)
		}

		factsPath := filepath.Join(rootDir, "goals", goalSlug, "facts.md")
		goalMdPath := filepath.Join(rootDir, "goals", goalSlug, "goal.md")

		var defects []string
		defects = append(defects, rep.StreamInspect.Offenses...)
		defects = append(defects, rep.Reconciliation.HollowStubsDetected...)
		defects = append(defects, rep.Reconciliation.MissingTouchedFiles...)

		rep.Disclosure = AuditIncompletenessDisclosure(factsPath, goalMdPath, defects)
		if !rep.Disclosure.Passed {
			rep.Violations = append(rep.Violations, "Point 5 (Disclosure): "+rep.Disclosure.Details)
		}
	}

	if len(rep.Violations) > 0 {
		rep.Clean = false
	}
	return rep
}

// VerifyManualEvidence verifies Rule-of-Five compliance for manual evidence input, separating
// document text from execution streams and honoring explicit markdown disclosures.
func VerifyManualEvidence(rootDir, goalSlug string, input EvidenceInput, modifiedFiles []string, opts RuleOfFiveOptions) *RuleOfFiveReport {
	rep := &RuleOfFiveReport{
		Clean:    true,
		GoalSlug: goalSlug,
	}

	rep.WholeTree = AuditWholeTree(rootDir, input.Stdout)
	if opts.RequireFullTreeLog && !rep.WholeTree.Passed && rep.WholeTree.PackagesTotal > 0 {
		rep.Violations = append(rep.Violations, "Point 1 (Whole-Tree): "+rep.WholeTree.Details)
	}

	rep.StreamInspect = InspectStreams(input.Stdout, input.Stderr)
	if !rep.StreamInspect.Passed {
		if !opts.AllowSkippedTests && rep.StreamInspect.SkippedTests > 0 {
			rep.Violations = append(rep.Violations, fmt.Sprintf("Point 2 (Stream Inspect): %d skipped test(s) detected", rep.StreamInspect.SkippedTests))
		}
		if rep.StreamInspect.LinterIgnores > 0 {
			rep.Violations = append(rep.Violations, fmt.Sprintf("Point 2 (Stream Inspect): %d linter suppression(s) detected", rep.StreamInspect.LinterIgnores))
		}
		if !opts.AllowWarnings && rep.StreamInspect.SilentWarnings > 0 {
			rep.Violations = append(rep.Violations, fmt.Sprintf("Point 2 (Stream Inspect): %d silent warning(s) detected", rep.StreamInspect.SilentWarnings))
		}
	}

	if goalSlug != "" {
		rep.Reconciliation = ReconcileRequirements(rootDir, goalSlug, modifiedFiles)
		if !rep.Reconciliation.Passed {
			for _, d := range rep.Reconciliation.Details {
				rep.Violations = append(rep.Violations, "Point 3 (Reconciliation): "+d)
			}
		}

		rep.BlastRadius = AuditBlastRadius(rootDir, goalSlug, modifiedFiles)
		if !rep.BlastRadius.Passed {
			rep.Violations = append(rep.Violations, "Point 4 (Blast Radius): "+rep.BlastRadius.Details)
		}

		factsPath := filepath.Join(rootDir, "goals", goalSlug, "facts.md")
		goalMdPath := filepath.Join(rootDir, "goals", goalSlug, "goal.md")

		var defects []string
		defects = append(defects, rep.StreamInspect.Offenses...)
		defects = append(defects, rep.Reconciliation.HollowStubsDetected...)
		defects = append(defects, rep.Reconciliation.MissingTouchedFiles...)

		var extraDisclosures []string
		if input.Format == "markdown" {
			extraDisclosures = extractMarkdownDisclosures(input.Document)
		}

		rep.Disclosure = auditDisclosures(extraDisclosures, factsPath, goalMdPath, defects)
		if !rep.Disclosure.Passed {
			rep.Violations = append(rep.Violations, "Point 5 (Disclosure): "+rep.Disclosure.Details)
		}
	}

	// Document-level checks (verdict & blocking cases)
	if input.Format == "markdown" {
		if allowedNegativeVerdicts[input.Verdict] {
			rep.Violations = append(rep.Violations, fmt.Sprintf("Evidence-Input (Verdict): negative document verdict %q", input.Verdict))
		} else if !allowedPositiveVerdicts[input.Verdict] {
			rep.Violations = append(rep.Violations, fmt.Sprintf("Evidence-Input (Verdict): unaccepted document verdict %q", input.Verdict))
		}

		if len(input.BlockingCases) > 0 {
			for _, c := range input.BlockingCases {
				rep.Violations = append(rep.Violations, fmt.Sprintf("Evidence-Input (Case): blocking acceptance case %q", c))
			}
		}
	}

	if len(rep.Violations) > 0 {
		rep.Clean = false
	}
	return rep
}

// Helpers

func discoverGoPackages(root string) []string {
	var pkgs []string
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || !info.IsDir() {
			return nil
		}
		base := info.Name()
		if strings.HasPrefix(base, ".") || base == "vendor" || base == "node_modules" || base == "dist" || base == "rm" {
			return filepath.SkipDir
		}
		entries, rErr := os.ReadDir(path)
		if rErr != nil {
			return nil
		}
		hasGo := false
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".go") {
				hasGo = true
				break
			}
		}
		if hasGo {
			rel, _ := filepath.Rel(root, path)
			if rel == "." {
				rel = "github.com/jadmadi/sila"
			}
			pkgs = append(pkgs, rel)
		}
		return nil
	})
	return pkgs
}

func extractFilesToTouch(content string) []string {
	var files []string
	lines := strings.Split(content, "\n")
	inSection := false

	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if strings.HasPrefix(trimmed, "## Files to Touch") || strings.HasPrefix(trimmed, "### Files to Touch") {
			inSection = true
			continue
		}
		if inSection {
			if strings.HasPrefix(trimmed, "#") {
				break
			}
			if strings.HasPrefix(trimmed, "-") || strings.HasPrefix(trimmed, "*") {
				item := strings.TrimSpace(strings.TrimLeft(trimmed, "-* \t`"))
				item = strings.Trim(item, "`")
				if item != "" && !strings.Contains(item, " ") {
					files = append(files, item)
				}
			}
		}
	}
	return files
}

func extractAcceptanceCriteria(content string) []string {
	var criteria []string
	lines := strings.Split(content, "\n")
	inSection := false

	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if strings.HasPrefix(trimmed, "## Strict Acceptance Criteria") || strings.HasPrefix(trimmed, "## Acceptance Criteria") {
			inSection = true
			continue
		}
		if inSection {
			if strings.HasPrefix(trimmed, "## ") {
				break
			}
			if strings.HasPrefix(trimmed, "-") || strings.HasPrefix(trimmed, "*") || (len(trimmed) > 2 && trimmed[1] == '.' && trimmed[0] >= '1' && trimmed[0] <= '9') {
				item := strings.TrimSpace(strings.TrimLeft(trimmed, "-*0123456789. \t"))
				if item != "" {
					criteria = append(criteria, item)
				}
			}
		}
	}
	return criteria
}

func extractDisclosedLimitations(content string) []string {
	var limitations []string
	lines := strings.Split(content, "\n")
	inSection := false

	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		lower := strings.ToLower(trimmed)
		if strings.HasPrefix(lower, "## known limitations") || strings.HasPrefix(lower, "## unverified areas") ||
			strings.HasPrefix(lower, "## partial completion") || strings.HasPrefix(lower, "## limitations") ||
			strings.HasPrefix(lower, "## disclosures") {
			inSection = true
			continue
		}
		if inSection {
			if strings.HasPrefix(trimmed, "## ") {
				inSection = false
				continue
			}
			if strings.HasPrefix(trimmed, "-") || strings.HasPrefix(trimmed, "*") {
				item := strings.TrimSpace(strings.TrimLeft(trimmed, "-* \t"))
				if item != "" {
					limitations = append(limitations, item)
				}
			}
		}
	}
	return limitations
}

func findHollowStubsInFile(filePath string) []string {
	var stubs []string
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil
	}

	content := string(data)
	if matches := hollowStubRe.FindAllString(content, -1); len(matches) > 0 {
		for _, m := range matches {
			stubs = append(stubs, m)
		}
	}

	fset := token.NewFileSet()
	node, pErr := parser.ParseFile(fset, filePath, data, 0)
	if pErr == nil && node != nil {
		for _, decl := range node.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn != nil && fn.Body != nil {
				if len(fn.Body.List) == 0 && !strings.Contains(filePath, "_test.go") {
					stubs = append(stubs, fmt.Sprintf("empty function body: %s", fn.Name.Name))
				}
			}
		}
	}

	return stubs
}

func isAllowedAuxiliaryFile(path, goalSlug string) bool {
	norm := filepath.ToSlash(path)
	if norm == "CHANGELOG.md" || norm == "AGENTS.md" || norm == "README.md" {
		return true
	}
	if strings.HasPrefix(norm, ".cursor/rules/") || strings.HasPrefix(norm, "goals/"+goalSlug+"/") {
		return true
	}
	return false
}

func isStagedDeletion(rootDir, relPath string) bool {
	cmd := exec.Command("git", "diff", "--cached", "--name-status")
	cmd.Dir = rootDir
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	target := filepath.ToSlash(filepath.Clean(relPath))
	lines := strings.Split(string(out), "\n")
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			status := fields[0]
			path := filepath.ToSlash(filepath.Clean(fields[1]))
			if (status == "D" || strings.HasPrefix(status, "R")) && path == target {
				return true
			}
		}
	}
	return false
}
