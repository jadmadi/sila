package assist

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// DiffAuditResult contains findings from evaluating staged git diffs.
type DiffAuditResult struct {
	Status             string  `json:"status"` // "pass", "finding", "partial", "unavailable", "skip"
	LeaksSecret        float64 `json:"leaks_secret"`
	SkipsTests         float64 `json:"skips_tests"`
	DestructiveSchema  float64 `json:"destructive_schema"`
	TotalBytes         int     `json:"total_bytes"`
	CoveredBytes       int     `json:"covered_bytes"`
	Truncated          bool    `json:"truncated"`
	FilesCount         int     `json:"files_count"`
	TestFixturesExempt int     `json:"test_fixtures_exempt,omitempty"`
	TailRiskDetected   bool    `json:"tail_risk_detected,omitempty"`
	Warning            string  `json:"warning,omitempty"`
	DurationMs         int64   `json:"duration_ms"`
}

var (
	secretPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?is)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`),
		regexp.MustCompile(`(?i)-----BEGIN [A-Z ]*PRIVATE KEY-----`),
		regexp.MustCompile(`(?i)(ghp_[0-9a-zA-Z]{36})`),
		regexp.MustCompile(`(?i)(gho_[0-9a-zA-Z]{36})`),
		regexp.MustCompile(`(?i)(glpat-[0-9a-zA-Z\-_]{20})`),
		regexp.MustCompile(`(?i)(xox[baprs]-[0-9a-zA-Z]{10,48})`),
		regexp.MustCompile(`(?i)(sk-[0-9a-zA-Z]{20,64})`),
		regexp.MustCompile(`(?i)(api[_-]?key\s*[:=]\s*["'][0-9a-zA-Z\-_]{16,}["'])`),
		regexp.MustCompile(`(?i)(secret[_-]?key\s*[:=]\s*["'][0-9a-zA-Z\-_]{16,}["'])`),
	}

	destructiveSchemaPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\bDROP\s+TABLE\b`),
		regexp.MustCompile(`(?i)\bDROP\s+DATABASE\b`),
		regexp.MustCompile(`(?i)\bTRUNCATE\s+TABLE\b`),
		regexp.MustCompile(`(?i)\bTRUNCATE\b\s+[a-zA-Z0-9_]+`),
		regexp.MustCompile(`(?i)\bDELETE\s+FROM\s+[a-zA-Z0-9_]+(?:\s*;|\s*$|\s+WHERE\s+(?:1=1|true)\b)`),
	}

	skipTestPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\bt\.Skip(f)?\(`),
		regexp.MustCompile(`(?i)\btesting\.Skip\(`),
		regexp.MustCompile(`(?i)\b(it|test|describe)\.skip\(`),
	}
)

func redactSecrets(text string) (string, bool) {
	redacted := text
	found := false
	for _, pat := range secretPatterns {
		if pat.MatchString(redacted) {
			found = true
			redacted = pat.ReplaceAllString(redacted, "[REDACTED_SECRET]")
		}
	}
	return redacted, found
}

func addedLines(diffText string) string {
	lines := strings.Split(diffText, "\n")
	hasDiffMarkers := false
	for _, l := range lines {
		if (strings.HasPrefix(l, "+") && !strings.HasPrefix(l, "+++")) ||
			(strings.HasPrefix(l, "-") && !strings.HasPrefix(l, "---")) {
			hasDiffMarkers = true
			break
		}
	}
	if !hasDiffMarkers {
		return diffText
	}
	var b strings.Builder
	for _, l := range lines {
		if strings.HasPrefix(l, "+") && !strings.HasPrefix(l, "+++") {
			b.WriteString(strings.TrimPrefix(l, "+"))
			b.WriteString("\n")
		}
	}
	return b.String()
}

// IsTestPath returns true if the specified file path belongs to automated tests,
// test fixtures, or mock utilities.
func IsTestPath(path string) bool {
	if path == "" {
		return false
	}
	p := strings.ToLower(filepath.ToSlash(path))
	clean := filepath.Clean(p)
	base := filepath.Base(clean)

	if strings.HasSuffix(base, "_test.go") {
		return true
	}
	if strings.HasSuffix(base, ".test.ts") || strings.HasSuffix(base, ".spec.ts") ||
		strings.HasSuffix(base, ".test.js") || strings.HasSuffix(base, ".spec.js") ||
		strings.HasSuffix(base, ".test.tsx") || strings.HasSuffix(base, ".spec.tsx") ||
		strings.HasSuffix(base, ".test.jsx") || strings.HasSuffix(base, ".spec.jsx") {
		return true
	}
	if (strings.HasPrefix(base, "test_") && strings.HasSuffix(base, ".py")) ||
		strings.HasSuffix(base, "_test.py") {
		return true
	}
	if strings.HasSuffix(base, "_test.rs") || base == "tests.rs" {
		return true
	}
	if strings.HasSuffix(base, "_test.rb") || strings.HasSuffix(base, "_spec.rb") ||
		strings.HasSuffix(base, "test.php") {
		return true
	}

	parts := strings.Split(clean, "/")
	for _, part := range parts {
		switch part {
		case "testdata", "testfixtures", "fixtures", "mocks", "mock", "testutil",
			"tests", "test", "__tests__", "spec", "specs":
			return true
		}
	}
	return false
}

func isSyntheticFixtureContent(text string) bool {
	lower := strings.ToLower(text)
	markers := []string{
		"mocktest",
		"mock_sec_",
		"mock_secret_fixture_",
		"mockkeytestingfixture",
		"testutil.mock",
		"sila:test-fixture",
		"sila:mock",
	}
	for _, m := range markers {
		if strings.Contains(lower, m) {
			return true
		}
	}
	return false
}

func isNonDestructiveSchemaPath(path string) bool {
	if path == "" {
		return false
	}
	p := strings.ToLower(filepath.ToSlash(path))
	clean := filepath.Clean(p)
	base := filepath.Base(clean)

	if strings.HasSuffix(base, ".md") || strings.HasSuffix(base, ".txt") ||
		strings.HasSuffix(base, ".rst") || strings.HasSuffix(base, ".adoc") ||
		strings.HasSuffix(base, ".html") || strings.HasSuffix(base, ".json") ||
		strings.HasSuffix(base, ".yaml") || strings.HasSuffix(base, ".yml") ||
		strings.HasSuffix(base, ".toml") {
		return true
	}

	parts := strings.Split(clean, "/")
	for _, part := range parts {
		switch part {
		case "lint", "linter", "assess", "assist", "cloudflare":
			return true
		}
	}
	return false
}

type diffFileSection struct {
	Path      string
	IsTest    bool
	AddedText string
}

func extractDiffPath(line string) string {
	idx := strings.Index(line, " b/")
	if idx >= 0 {
		p := line[idx+3:]
		return strings.Trim(p, "\"")
	}
	fields := strings.Fields(line)
	if len(fields) >= 4 {
		p := strings.TrimPrefix(fields[3], "b/")
		return strings.Trim(p, "\"")
	}
	return ""
}

func parseDiffSections(diffText string) []diffFileSection {
	lines := strings.Split(diffText, "\n")
	var sections []diffFileSection
	var curPath string
	var curAdded strings.Builder

	flush := func() {
		if curAdded.Len() > 0 || curPath != "" {
			sections = append(sections, diffFileSection{
				Path:      curPath,
				IsTest:    IsTestPath(curPath),
				AddedText: curAdded.String(),
			})
			curPath = ""
			curAdded.Reset()
		}
	}

	for _, line := range lines {
		if strings.HasPrefix(line, "diff --git ") {
			flush()
			curPath = extractDiffPath(line)
			continue
		}
		if curPath == "" && strings.HasPrefix(line, "+++ b/") {
			curPath = strings.Trim(strings.TrimPrefix(line, "+++ b/"), "\"")
			continue
		}
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			curAdded.WriteString(strings.TrimPrefix(line, "+"))
			curAdded.WriteString("\n")
		}
	}
	flush()
	return sections
}

func scanLocalRisksDetailed(text string) (hasSecret, hasSchema, hasSkip bool, testExemptions int) {
	sections := parseDiffSections(text)
	if len(sections) == 0 {
		added := addedLines(text)
		for _, pat := range secretPatterns {
			if pat.MatchString(added) {
				if isSyntheticFixtureContent(added) {
					testExemptions++
				} else {
					hasSecret = true
				}
				break
			}
		}
		for _, pat := range destructiveSchemaPatterns {
			if pat.MatchString(added) {
				hasSchema = true
				break
			}
		}
		for _, pat := range skipTestPatterns {
			if pat.MatchString(added) {
				// Match the sectioned scan: skips inside test files are
				// expected capability guards, not silently removed assertions.
				if strings.Contains(text, "_test.go") {
					testExemptions++
				} else {
					hasSkip = true
				}
				break
			}
		}
		return
	}

	for _, sec := range sections {
		added := sec.AddedText
		if added == "" {
			continue
		}

		secHasSecret := false
		for _, pat := range secretPatterns {
			if pat.MatchString(added) {
				secHasSecret = true
				break
			}
		}

		if secHasSecret {
			if sec.IsTest || isSyntheticFixtureContent(added) {
				testExemptions++
			} else {
				hasSecret = true
			}
		}

		for _, pat := range destructiveSchemaPatterns {
			if pat.MatchString(added) {
				if sec.IsTest || isNonDestructiveSchemaPath(sec.Path) {
					testExemptions++
				} else {
					hasSchema = true
				}
				break
			}
		}

		for _, pat := range skipTestPatterns {
			if pat.MatchString(added) {
				if sec.IsTest {
					testExemptions++
				} else {
					hasSkip = true
				}
				break
			}
		}
	}
	return
}

func scanLocalRisks(text string) (hasSecret, hasSchema, hasSkip bool) {
	hasSecret, hasSchema, hasSkip, _ = scanLocalRisksDetailed(text)
	return
}

func countDiffFiles(diffText string) int {
	count := 0
	lines := strings.SplitSeq(diffText, "\n")
	for l := range lines {
		if strings.HasPrefix(l, "diff --git ") {
			count++
		}
	}
	if count == 0 {
		return 1
	}
	return count
}

// AuditDiff evaluates a staged git diff against semantic security and regression questions.
// It executes with bounded payloads, local secret pre-redaction, honest coverage reporting,
// and tail-risk detection.
func AuditDiff(ctx context.Context, p Provider, diffText string) (*DiffAuditResult, error) {
	if p == nil || strings.TrimSpace(diffText) == "" {
		return nil, nil
	}

	totalBytes := len(diffText)
	filesCount := countDiffFiles(diffText)
	coveredBytes := totalBytes
	truncated := false

	// Small local risk scan over added lines in diff, aware of test files & fixtures
	localSecret, localSchema, localSkip, testExemptions := scanLocalRisksDetailed(diffText)

	// Redact secrets on full diffText BEFORE sampling so secrets spanning boundary are fully redacted
	redactedText, _ := redactSecrets(diffText)

	// Bounded sample of redacted diff (8000 bytes)
	sample := redactedText
	var tailRisk bool
	if len(sample) > 8000 {
		sample = sample[:8000]
		coveredBytes = 8000
		truncated = true
		tail := diffText[8000:]
		tSecret, tSchema, tSkip, tExempt := scanLocalRisksDetailed(tail)
		testExemptions += tExempt
		tailRisk = tSecret || tSchema || tSkip
	}

	state := map[string]any{
		"staged_diff": sample,
	}

	questions := map[string]Question{
		"leaks_secret": {
			Type:         TypeNoul,
			Instructions: "Does this git diff introduce or expose hardcoded API keys, private tokens, or secret credentials in production code? (Ignore synthetic mock tokens in test files or test fixtures).",
		},
		"skips_tests": {
			Type:         TypeNoul,
			Instructions: "Does this git diff silently disable, comment out, or skip existing test assertions or test files?",
		},
		"destructive_schema": {
			Type:         TypeNoul,
			Instructions: "Does this git diff contain destructive database schema changes (DROP TABLE, TRUNCATE, or unbounded column removal)?",
		},
	}

	subCtx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()

	resp, err := p.Evaluate(subCtx, "", state, questions)
	if err != nil {
		if localSecret || localSchema || localSkip {
			res := &DiffAuditResult{
				Status:             "finding",
				TotalBytes:         totalBytes,
				CoveredBytes:       coveredBytes,
				Truncated:          truncated,
				FilesCount:         filesCount,
				TestFixturesExempt: testExemptions,
			}
			var findings []string
			if localSecret {
				res.LeaksSecret = 1.0
				findings = append(findings, "potential credential/secret leak detected")
			}
			if localSchema {
				res.DestructiveSchema = 1.0
				findings = append(findings, "destructive database schema change detected")
			}
			if localSkip {
				res.SkipsTests = 1.0
				findings = append(findings, "silently disabled or skipped test assertions detected")
			}
			res.Warning = strings.Join(findings, "; ") + fmt.Sprintf(" (semantic sentry unavailable: %v; local risk confirmed)", err)
			return res, nil
		}
		return &DiffAuditResult{
			Status:             "unavailable",
			TotalBytes:         totalBytes,
			CoveredBytes:       coveredBytes,
			Truncated:          truncated,
			FilesCount:         filesCount,
			TestFixturesExempt: testExemptions,
			Warning:            fmt.Sprintf("semantic sentry unavailable (%v)", err),
		}, nil
	}

	res := &DiffAuditResult{
		TotalBytes:         totalBytes,
		CoveredBytes:       coveredBytes,
		Truncated:          truncated,
		FilesCount:         filesCount,
		TestFixturesExempt: testExemptions,
		TailRiskDetected:   tailRisk,
		DurationMs:         resp.DurationMs,
	}

	leaksAns, okLeaks := resp.Nouls["leaks_secret"]
	skipsAns, okSkips := resp.Nouls["skips_tests"]
	schemaAns, okSchema := resp.Nouls["destructive_schema"]

	if !okLeaks || !okSkips || !okSchema {
		if localSecret || localSchema || localSkip {
			res.Status = "finding"
			var findings []string
			if localSecret {
				res.LeaksSecret = 1.0
				findings = append(findings, "potential credential/secret leak detected")
			}
			if localSchema {
				res.DestructiveSchema = 1.0
				findings = append(findings, "destructive database schema change detected")
			}
			if localSkip {
				res.SkipsTests = 1.0
				findings = append(findings, "silently disabled or skipped test assertions detected")
			}
			res.Warning = strings.Join(findings, "; ") + " (semantic provider missing answers; local risk confirmed)"
			return res, nil
		}
		res.Status = "unavailable"
		res.Warning = "semantic sentry unavailable: provider response missing required answers"
		return res, nil
	}

	res.LeaksSecret = leaksAns.Noul
	res.SkipsTests = skipsAns.Noul
	res.DestructiveSchema = schemaAns.Noul

	// Incorporate local pre-checks
	if localSecret {
		res.LeaksSecret = 1.0
	}
	if localSchema {
		res.DestructiveSchema = 1.0
	}
	if localSkip {
		res.SkipsTests = 1.0
	}

	var findings []string
	if res.LeaksSecret >= 0.80 {
		findings = append(findings, fmt.Sprintf("potential credential/secret leak detected (confidence: %.0f%%)", res.LeaksSecret*100))
	}
	if res.DestructiveSchema >= 0.80 {
		findings = append(findings, fmt.Sprintf("destructive database schema change detected (confidence: %.0f%%)", res.DestructiveSchema*100))
	}
	if res.SkipsTests >= 0.80 {
		findings = append(findings, fmt.Sprintf("silently disabled or skipped test assertions detected (confidence: %.0f%%)", res.SkipsTests*100))
	}

	if len(findings) > 0 {
		res.Status = "finding"
		res.Warning = strings.Join(findings, "; ")
		if tailRisk {
			res.Warning += " [risk confirmed in diff tail]"
		}
		return res, nil
	}

	if truncated {
		res.Status = "partial"
		res.Warning = fmt.Sprintf("partial coverage: audited %d/%d bytes (%d files); tail (%d bytes) inspected by local checks only", coveredBytes, totalBytes, filesCount, totalBytes-coveredBytes)
		return res, nil
	}

	res.Status = "pass"
	return res, nil
}

// HighBlastRadiusTools contains tools with state-mutating, process-spawning, or destructive capabilities.
var HighBlastRadiusTools = map[string]bool{
	"vacuum_database":     true,
	"compile_rules":       true,
	"sync_rules":          true,
	"reopen_goal":         true,
	"release_goal":        true,
	"create_goal":         true,
	"diagnose_system":     true,
	"save_memo":           true,
	"save_lesson":         true,
	"save_doc":            true,
	"save_handoff":        true,
	"spawn_crewmate":      true,
	"supervise_crew":      true,
	"run_gate_pipeline":   true,
	"reconcile_drift":     true,
	"cloudflare_d1_query": true,
}

// IsHighBlastRadiusTool reports whether an MCP tool is classified as high blast radius.
func IsHighBlastRadiusTool(toolName string) bool {
	return HighBlastRadiusTools[toolName]
}

// MCPOperationAudit represents the result of evaluating an MCP tool call for safety risks.
type MCPOperationAudit struct {
	ToolName          string  `json:"tool_name"`
	IsHighBlastRadius bool    `json:"is_high_blast_radius"`
	Allowed           bool    `json:"allowed"`
	AdvisoryWarning   string  `json:"advisory_warning,omitempty"`
	LeaksSecret       float64 `json:"leaks_secret"`
	DestructiveSQL    float64 `json:"destructive_sql"`
	Provider          string  `json:"provider"` // "jev", "local", or "none"
	DurationMs        int64   `json:"duration_ms"`
}

// AuditMCPCall evaluates an MCP tool invocation for safety risks before execution.
// It fails open on provider timeouts or unavailability in under 250ms with guaranteed non-blocking advisory semantics.
func AuditMCPCall(ctx context.Context, toolName string, rawArgs []byte, project string, p Provider) MCPOperationAudit {
	start := time.Now()

	// 1. Skip low-risk read-only tools
	if !IsHighBlastRadiusTool(toolName) {
		return MCPOperationAudit{
			ToolName:          toolName,
			IsHighBlastRadius: false,
			Allowed:           true,
			Provider:          "none",
			DurationMs:        0,
		}
	}

	payloadStr := string(rawArgs)

	// 2. Local deterministic pre-checks
	localSecret := false
	for _, pat := range secretPatterns {
		if pat.MatchString(payloadStr) {
			localSecret = true
			break
		}
	}

	localDestructive := false
	for _, pat := range destructiveSchemaPatterns {
		if pat.MatchString(payloadStr) {
			localDestructive = true
			break
		}
	}

	// 3. Privacy & Redaction boundary: scrub secrets before evaluating against Jev
	redactedPayload, _ := redactSecrets(payloadStr)

	// 4. Resolve provider if not supplied
	if p == nil {
		p = ActiveProviderFor("sentry", project)
		if p == nil {
			p = ActiveProviderFor("gate", project)
		}
	}

	audit := MCPOperationAudit{
		ToolName:          toolName,
		IsHighBlastRadius: true,
		Allowed:           true, // Fail-open: always allow execution
		Provider:          "local",
	}

	if localSecret {
		audit.LeaksSecret = 1.0
	}
	if localDestructive {
		audit.DestructiveSQL = 1.0
	}

	// 5. Evaluate with Jev if provider available and context active
	if p != nil && ctx.Err() == nil {
		timeoutCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
		defer cancel()

		state := map[string]any{
			"tool_name": toolName,
			"arguments": redactedPayload,
		}

		questions := map[string]Question{
			"leaks_secret": {
				Type:         TypeNoul,
				Instructions: "Does this MCP tool argument payload expose raw secrets, private keys, or sensitive credentials?",
			},
			"destructive_sql": {
				Type:         TypeNoul,
				Instructions: "Does this MCP tool argument payload execute destructive SQL schema changes (such as DROP or TRUNCATE)?",
			},
		}

		resp, err := p.Evaluate(timeoutCtx, "", state, questions)
		if err == nil && resp != nil {
			audit.Provider = "jev"
			if ans, ok := resp.Nouls["leaks_secret"]; ok {
				if ans.Noul > audit.LeaksSecret {
					audit.LeaksSecret = ans.Noul
				}
			}
			if ans, ok := resp.Nouls["destructive_sql"]; ok {
				if ans.Noul > audit.DestructiveSQL {
					audit.DestructiveSQL = ans.Noul
				}
			}
		}
	}

	// 6. Formulate advisory warnings
	var warnings []string
	if audit.LeaksSecret >= 0.80 {
		warnings = append(warnings, fmt.Sprintf("potential credential exposure detected (confidence: %.0f%%)", audit.LeaksSecret*100))
	}
	if audit.DestructiveSQL >= 0.80 {
		warnings = append(warnings, fmt.Sprintf("destructive database schema change or table wipe detected (confidence: %.0f%%)", audit.DestructiveSQL*100))
	}

	if len(warnings) > 0 {
		audit.AdvisoryWarning = strings.Join(warnings, "; ")
	}

	audit.DurationMs = time.Since(start).Milliseconds()
	return audit
}
