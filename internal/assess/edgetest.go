package assess

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/render"
)

// EdgeIssue represents an architectural violation or warning detected in
// Cloudflare Workers test suites (@cloudflare/vitest-pool-workers).
type EdgeIssue struct {
	File       string `json:"file"`
	Line       int    `json:"line"`
	TestName   string `json:"test_name,omitempty"`
	RuleID     string `json:"rule_id"`
	Severity   string `json:"severity"` // "FAIL", "WARN", "INFO"
	Message    string `json:"message"`
	Detail     string `json:"detail"`
	Suggestion string `json:"suggestion"`
}

// EdgeTestFileSummary holds per-file statistics for edge test suites.
type EdgeTestFileSummary struct {
	File          string `json:"file"`
	TestCount     int    `json:"test_count"`
	MutatingCount int    `json:"mutating_count"`
	HasBeforeEach bool   `json:"has_before_each"`
	HasBeforeAll  bool   `json:"has_before_all"`
	Isolated      bool   `json:"isolated"`
}

// EdgeAuditSummary aggregates overall statistics for the test suite scan.
type EdgeAuditSummary struct {
	TotalFiles    int `json:"total_files"`
	TotalTests    int `json:"total_tests"`
	MutatingTests int `json:"mutating_tests"`
	IsolatedFiles int `json:"isolated_files"`
	Violations    int `json:"violations"`
	Warnings      int `json:"warnings"`
}

// EdgeAuditReport is the root report returned by AuditEdgeTests and rendered
// by `sila doctor --edge`.
type EdgeAuditReport struct {
	Applicable bool                  `json:"applicable"`
	Passed     bool                  `json:"passed"`
	Status     string                `json:"status"` // "PASS", "WARN", "FAIL", "SKIPPED"
	Reason     string                `json:"reason,omitempty"`
	AuditTime  time.Time             `json:"audit_time"`
	TargetDir  string                `json:"target_dir"`
	Framework  string                `json:"framework"`
	Summary    EdgeAuditSummary      `json:"summary"`
	Files      []EdgeTestFileSummary `json:"files"`
	Issues     []EdgeIssue           `json:"issues"`
}

// EdgeAuditOptions configures the edge test suite scanner.
type EdgeAuditOptions struct {
	ExcludeDirs []string // Subdirectories to skip (default: node_modules, .git, dist, .wrangler, vendor)
	Strict      bool     // If true, treat single-mutation warnings as failures
}

// DefaultEdgeAuditOptions returns standard production options.
func DefaultEdgeAuditOptions() EdgeAuditOptions {
	return EdgeAuditOptions{
		ExcludeDirs: []string{"node_modules", ".git", "dist", ".wrangler", ".cache", "vendor", ".turbo", ".next"},
		Strict:      false,
	}
}

// Regular expressions for static AST-like parsing of TypeScript/JavaScript test files.
var (
	edgeTestCallRE     = regexp.MustCompile(`(?m)^\s*(?:it|test)(?:\.(?:only|skip|todo|concurrent))?\s*\(\s*['"` + "`" + `](.*?)['"` + "`" + `]`)
	edgeBeforeEachRE   = regexp.MustCompile(`(?m)^\s*beforeEach\s*\(`)
	edgeAfterEachRE    = regexp.MustCompile(`(?m)^\s*afterEach\s*\(`)
	edgeBeforeAllRE    = regexp.MustCompile(`(?m)^\s*beforeAll\s*\(`)
	edgeAfterAllRE     = regexp.MustCompile(`(?m)^\s*afterAll\s*\(`)
	edgeTopLevelLetRE  = regexp.MustCompile(`^\s*(?:let|var)\s+([a-zA-Z_$][a-zA-Z0-9_$]*)`)
	edgeSqlMutationRE  = regexp.MustCompile(`(?i)\b(INSERT\s+INTO|UPDATE\s+[a-zA-Z0-9_.]+\s+SET|DELETE\s+FROM|TRUNCATE\s+(?:TABLE\s+)?|DROP\s+TABLE|ALTER\s+TABLE)\b`)
	edgeD1MutationRE   = regexp.MustCompile(`(?:\.prepare\s*\([^)]*\)\s*\.run\s*\(|\.batch\s*\(|\.exec\s*\()`)
	edgeDrizzleMutRE   = regexp.MustCompile(`(?:\b(?:db|drizzle)\s*\.\s*(?:insert|update|delete)\s*\()`)
	edgeKvMutationRE   = regexp.MustCompile(`(?:\b(?:env\.[a-zA-Z0-9_]*(?:kv|cache)[a-zA-Z0-9_]*|\.kv)\s*\.\s*(?:put|delete)\b|\b(?:env\.[a-zA-Z0-9_]+)\s*\.\s*(?:put|delete)\s*\()`)
	edgeHttpMutationRE = regexp.MustCompile(`method\s*:\s*['"](POST|PUT|DELETE|PATCH)['"]`)
	edgeWorkerFetchRE  = regexp.MustCompile(`(?:\bworker\.fetch\b|\bSELF\.fetch\b|\bfetch\s*\()`)
	edgeResetSqlRE     = regexp.MustCompile(`(?i)\b(DELETE\s+FROM|TRUNCATE|DROP\s+TABLE)\b`)
	edgeResetFuncRE    = regexp.MustCompile(`(?i)\b(reset|cleanup|clear|truncate|reseed|migrate|applyD1Migrations)\b`)
	edgeSchemaSetupRE  = regexp.MustCompile(`(?i)\b(CREATE\s+TABLE|applyD1Migrations|migrate|runMigrations)\b`)
	edgeHardcodedIdRE  = regexp.MustCompile(`(?i)(?:id\s*:\s*['"]([a-zA-Z0-9_-]{3,})['"]|values\s*\([^)]*['"]([a-zA-Z0-9_-]{3,})['"])`)
)

// IsCloudflareWorkerProject reports whether targetDir appears to be a Cloudflare Worker project.
func IsCloudflareWorkerProject(targetDir string) bool {
	if targetDir == "" {
		targetDir = "."
	}

	wranglerFiles := []string{"wrangler.json", "wrangler.jsonc", "wrangler.toml"}
	for _, f := range wranglerFiles {
		if _, err := os.Stat(filepath.Join(targetDir, f)); err == nil {
			return true
		}
	}

	// Check package.json for cloudflare worker dependencies
	pkgPath := filepath.Join(targetDir, "package.json")
	if data, err := os.ReadFile(pkgPath); err == nil {
		s := string(data)
		if strings.Contains(s, "@cloudflare/vitest-pool-workers") ||
			strings.Contains(s, "@cloudflare/workers-types") ||
			strings.Contains(s, "wrangler") {
			return true
		}
	}

	// Check vitest.config.ts / vitest.config.js
	for _, vConf := range []string{"vitest.config.ts", "vitest.config.js", "vitest.config.mjs"} {
		if data, err := os.ReadFile(filepath.Join(targetDir, vConf)); err == nil {
			if strings.Contains(string(data), "@cloudflare/vitest-pool-workers") {
				return true
			}
		}
	}

	return false
}

type testBlock struct {
	kind      string // "test", "beforeEach", "afterEach", "beforeAll", "afterAll"
	name      string
	startLine int
	endLine   int
	body      string
}

// AuditEdgeTests scans TypeScript and JavaScript test suites in targetDir for
// storage sandbox isolation, cross-test state leakage, and configuration drift.
func AuditEdgeTests(targetDir string, opts EdgeAuditOptions) (*EdgeAuditReport, error) {
	if targetDir == "" {
		var err error
		targetDir, err = os.Getwd()
		if err != nil {
			return nil, err
		}
	}

	if len(opts.ExcludeDirs) == 0 {
		opts.ExcludeDirs = DefaultEdgeAuditOptions().ExcludeDirs
	}

	report := &EdgeAuditReport{
		Applicable: false,
		Passed:     true,
		Status:     "PASS",
		AuditTime:  time.Now(),
		TargetDir:  targetDir,
		Framework:  "@cloudflare/vitest-pool-workers",
		Files:      []EdgeTestFileSummary{},
		Issues:     []EdgeIssue{},
	}

	if !IsCloudflareWorkerProject(targetDir) {
		report.Status = "SKIPPED"
		report.Reason = "No Cloudflare Worker configuration (wrangler.json/jsonc/toml or @cloudflare/vitest-pool-workers) detected in project directory."
		return report, nil
	}
	report.Applicable = true

	// Step 1: Audit wrangler.jsonc / vitest.config.ts binding parity
	auditConfigBindingParity(targetDir, report)

	// Step 2: Discover test files
	var testFiles []string
	excludeMap := make(map[string]bool)
	for _, d := range opts.ExcludeDirs {
		excludeMap[d] = true
	}

	err := filepath.Walk(targetDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if excludeMap[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}

		name := info.Name()
		if strings.HasSuffix(name, ".spec.ts") || strings.HasSuffix(name, ".test.ts") ||
			strings.HasSuffix(name, ".spec.js") || strings.HasSuffix(name, ".test.js") {
			testFiles = append(testFiles, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walking test files: %w", err)
	}

	sort.Strings(testFiles)

	// Step 3: Analyze each test file
	for _, tf := range testFiles {
		relPath, _ := filepath.Rel(targetDir, tf)
		if relPath == "" {
			relPath = tf
		}
		auditTestFile(targetDir, relPath, tf, opts, report)
	}

	// Step 4: Finalize summary counts and status
	for _, issue := range report.Issues {
		if issue.Severity == "FAIL" {
			report.Summary.Violations++
			report.Passed = false
			report.Status = "FAIL"
		} else if issue.Severity == "WARN" {
			report.Summary.Warnings++
		}
	}

	if report.Passed && report.Summary.Warnings > 0 {
		report.Status = "WARN"
	}

	return report, nil
}

func auditTestFile(targetDir, relFile, fullPath string, opts EdgeAuditOptions, report *EdgeAuditReport) {
	data, err := os.ReadFile(fullPath)
	if err != nil {
		return
	}
	content := string(data)

	// Check if this test file interacts with Cloudflare Workers harness
	isWorkerTest := strings.Contains(content, "cloudflare:test") ||
		strings.Contains(content, "@cloudflare/vitest-pool-workers") ||
		strings.Contains(content, "env.DB") ||
		strings.Contains(content, "env.KV")

	// If the file doesn't touch worker harness, still check if it's a test file in a worker project
	blocks, topLevelLets := parseTestBlocks(content)

	var (
		tests         []testBlock
		hasBeforeEach bool
		hasAfterEach  bool
		hasBeforeAll  bool
		resetInEach   bool
		schemaInAll   bool
	)

	for _, b := range blocks {
		switch b.kind {
		case "test":
			tests = append(tests, b)
		case "beforeEach":
			hasBeforeEach = true
			if edgeResetSqlRE.MatchString(b.body) || edgeResetFuncRE.MatchString(b.body) {
				resetInEach = true
			}
		case "afterEach":
			hasAfterEach = true
			if edgeResetSqlRE.MatchString(b.body) || edgeResetFuncRE.MatchString(b.body) {
				resetInEach = true
			}
		case "beforeAll":
			hasBeforeAll = true
			if edgeSchemaSetupRE.MatchString(b.body) {
				schemaInAll = true
			}
		}
	}

	fileSum := EdgeTestFileSummary{
		File:          relFile,
		TestCount:     len(tests),
		HasBeforeEach: hasBeforeEach || hasAfterEach,
		HasBeforeAll:  hasBeforeAll,
		Isolated:      true,
	}

	report.Summary.TotalFiles++
	report.Summary.TotalTests += len(tests)

	if len(tests) == 0 {
		fileSum.Isolated = true
		report.Summary.IsolatedFiles++
		report.Files = append(report.Files, fileSum)
		return
	}

	var mutatingTests []testBlock
	var kvMutatingTests []testBlock
	var usesD1 bool
	idCounts := make(map[string][]int) // id -> lines

	for _, t := range tests {
		body := t.body
		isMutating := false

		// 1. Direct D1 mutations
		if edgeSqlMutationRE.MatchString(body) || edgeD1MutationRE.MatchString(body) || edgeDrizzleMutRE.MatchString(body) {
			isMutating = true
			usesD1 = true
		}

		// 2. HTTP mutations on worker instance
		if edgeWorkerFetchRE.MatchString(body) && edgeHttpMutationRE.MatchString(body) {
			isMutating = true
			usesD1 = true
		}

		// 3. D1 read queries
		if strings.Contains(body, "env.DB") || strings.Contains(body, "SELECT ") {
			usesD1 = true
		}

		// 4. KV mutations
		if edgeKvMutationRE.MatchString(body) {
			kvMutatingTests = append(kvMutatingTests, t)
		}

		// 5. Track hardcoded primary keys
		matches := edgeHardcodedIdRE.FindAllStringSubmatch(body, -1)
		for _, m := range matches {
			id := m[1]
			if id == "" && len(m) > 2 {
				id = m[2]
			}
			if len(id) >= 3 && !isGenericName(id) {
				idCounts[id] = append(idCounts[id], t.startLine)
			}
		}

		// 6. Check top-level mutable variable reassignments
		for _, v := range topLevelLets {
			reassignRE := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(v) + `\s*=[^=]`)
			if reassignRE.MatchString(body) && !resetInEach {
				report.Issues = append(report.Issues, EdgeIssue{
					File:       relFile,
					Line:       t.startLine,
					TestName:   t.name,
					RuleID:     "EDGE-004",
					Severity:   "WARN",
					Message:    fmt.Sprintf("Shared mutable module variable '%s' reassigned inside test block.", v),
					Detail:     "Top-level let/var declarations retain mutations across tests, leading to order-dependent flakiness.",
					Suggestion: fmt.Sprintf("Move '%s' into the test block, or reset it inside a beforeEach() hook.", v),
				})
			}
		}

		if isMutating {
			mutatingTests = append(mutatingTests, t)
		}
	}

	fileSum.MutatingCount = len(mutatingTests)
	report.Summary.MutatingTests += len(mutatingTests)

	// Rule EDGE-001: D1 Cross-Test State Leakage
	if len(mutatingTests) > 0 && !resetInEach {
		fileSum.Isolated = false
		first := mutatingTests[0]
		severity := "WARN"
		if len(mutatingTests) >= 2 || opts.Strict {
			severity = "FAIL"
		}

		msg := fmt.Sprintf("Unisolated D1 database mutations detected across %d test(s) without a beforeEach/afterEach reset hook.", len(mutatingTests))
		if len(mutatingTests) == 1 {
			msg = "Unisolated D1 database mutation in test without a beforeEach/afterEach reset hook."
		}

		report.Issues = append(report.Issues, EdgeIssue{
			File:       relFile,
			Line:       first.startLine,
			TestName:   first.name,
			RuleID:     "EDGE-001",
			Severity:   severity,
			Message:    msg,
			Detail:     fmt.Sprintf("%d test(s) perform D1 mutations (INSERT, UPDATE, DELETE, or mutating fetch) on the shared in-memory database. Subsequent tests inherit dirty state, causing ordering dependencies.", len(mutatingTests)),
			Suggestion: "Add a beforeEach(async () => { ... }) hook that clears mutated tables (e.g. `await env.DB.prepare('DELETE FROM ...').run()`) or re-applies schema migrations.",
		})
	}

	// Rule EDGE-002: KV Cross-Test State Leakage
	if len(kvMutatingTests) > 0 && !resetInEach {
		first := kvMutatingTests[0]
		report.Issues = append(report.Issues, EdgeIssue{
			File:       relFile,
			Line:       first.startLine,
			TestName:   first.name,
			RuleID:     "EDGE-002",
			Severity:   "WARN",
			Message:    fmt.Sprintf("Unisolated KV storage mutation(s) detected across %d test(s) without key cleanup.", len(kvMutatingTests)),
			Detail:     "KV key writes persist in the miniflare worker isolate across tests in the same suite unless cleaned up.",
			Suggestion: "Add an afterEach() or beforeEach() hook that cleans up modified keys, or use crypto.randomUUID() key prefixes.",
		})
	}

	// Rule EDGE-003: Missing D1 Schema Initialization
	if usesD1 && isWorkerTest && !schemaInAll && !resetInEach {
		report.Issues = append(report.Issues, EdgeIssue{
			File:       relFile,
			Line:       1,
			RuleID:     "EDGE-003",
			Severity:   "WARN",
			Message:    "D1 queries or mutations detected without explicit schema initialization in beforeAll().",
			Detail:     "Tests interact with env.DB without a detected CREATE TABLE statement or applyD1Migrations() setup in beforeAll(). If tables are not pre-migrated, tests will fail with SQLITE_ERROR.",
			Suggestion: "Add a beforeAll(async () => { ... }) hook applying D1 schema migrations via `applyD1Migrations` or `env.DB.prepare('CREATE TABLE...').run()`.",
		})
	}

	// Rule EDGE-005: Hardcoded Primary Key Collisions
	for id, lines := range idCounts {
		if len(lines) >= 2 && !resetInEach {
			report.Issues = append(report.Issues, EdgeIssue{
				File:       relFile,
				Line:       lines[0],
				RuleID:     "EDGE-005",
				Severity:   "WARN",
				Message:    fmt.Sprintf("Duplicate hardcoded primary key '%s' inserted across %d tests without teardown.", id, len(lines)),
				Detail:     "Inserting the same hardcoded identifier across multiple tests risks UNIQUE constraint violations if database state leaks.",
				Suggestion: fmt.Sprintf("Generate dynamic IDs with `crypto.randomUUID()` or ensure tables are truncated between tests in beforeEach()."),
			})
		}
	}

	if fileSum.Isolated {
		report.Summary.IsolatedFiles++
	}
	report.Files = append(report.Files, fileSum)
}

func isGenericName(id string) bool {
	lower := strings.ToLower(id)
	return lower == "test" || lower == "true" || lower == "false" || lower == "null" || lower == "undefined"
}

// auditConfigBindingParity checks if D1/KV bindings declared in wrangler.jsonc/json
// are properly registered in vitest.config.ts miniflare pool options.
func auditConfigBindingParity(targetDir string, report *EdgeAuditReport) {
	type WranglerConfig struct {
		D1Databases []struct {
			Binding string `json:"binding"`
		} `json:"d1_databases"`
		KVNamespaces []struct {
			Binding string `json:"binding"`
		} `json:"kv_namespaces"`
	}

	var d1Bindings []string
	var kvBindings []string

	for _, wName := range []string{"wrangler.jsonc", "wrangler.json"} {
		wPath := filepath.Join(targetDir, wName)
		data, err := os.ReadFile(wPath)
		if err != nil {
			continue
		}
		// Strip comments if jsonc
		clean := stripJSONComments(string(data))
		var wConf WranglerConfig
		if err := json.Unmarshal([]byte(clean), &wConf); err == nil {
			for _, d := range wConf.D1Databases {
				if d.Binding != "" {
					d1Bindings = append(d1Bindings, d.Binding)
				}
			}
			for _, k := range wConf.KVNamespaces {
				if k.Binding != "" {
					kvBindings = append(kvBindings, k.Binding)
				}
			}
			break
		}
	}

	if len(d1Bindings) == 0 && len(kvBindings) == 0 {
		return
	}

	// Inspect vitest.config.ts
	for _, vName := range []string{"vitest.config.ts", "vitest.config.js"} {
		vPath := filepath.Join(targetDir, vName)
		data, err := os.ReadFile(vPath)
		if err != nil {
			continue
		}
		vStr := string(data)

		for _, b := range d1Bindings {
			// Check if binding is mentioned in d1Databases
			if !strings.Contains(vStr, fmt.Sprintf("'%s'", b)) && !strings.Contains(vStr, fmt.Sprintf(`"%s"`, b)) {
				report.Issues = append(report.Issues, EdgeIssue{
					File:       vName,
					Line:       1,
					RuleID:     "EDGE-006",
					Severity:   "WARN",
					Message:    fmt.Sprintf("D1 binding '%s' declared in wrangler config is missing from %s miniflare pool options.", b, vName),
					Detail:     fmt.Sprintf("Miniflare workers pool requires d1Databases: ['%s'] to bind the in-memory database to env.%s.", b, b),
					Suggestion: fmt.Sprintf("Add '%s' to miniflare.d1Databases in %s.", b, vName),
				})
			}
		}

		for _, b := range kvBindings {
			if !strings.Contains(vStr, fmt.Sprintf("'%s'", b)) && !strings.Contains(vStr, fmt.Sprintf(`"%s"`, b)) {
				report.Issues = append(report.Issues, EdgeIssue{
					File:       vName,
					Line:       1,
					RuleID:     "EDGE-006",
					Severity:   "WARN",
					Message:    fmt.Sprintf("KV namespace binding '%s' declared in wrangler config is missing from %s miniflare pool options.", b, vName),
					Detail:     fmt.Sprintf("Miniflare workers pool requires kvNamespaces: ['%s'] to bind the KV store to env.%s.", b, b),
					Suggestion: fmt.Sprintf("Add '%s' to miniflare.kvNamespaces in %s.", b, vName),
				})
			}
		}
	}
}

// stripJSONComments removes single-line // and block /* */ comments from JSONC.
func stripJSONComments(s string) string {
	var b strings.Builder
	inString := false
	inBlockComment := false
	inLineComment := false
	var quoteChar rune

	chars := []rune(s)
	n := len(chars)

	for i := 0; i < n; i++ {
		r := chars[i]
		next := rune(0)
		if i+1 < n {
			next = chars[i+1]
		}

		if inLineComment {
			if r == '\n' {
				inLineComment = false
				b.WriteRune(r)
			}
			continue
		}

		if inBlockComment {
			if r == '*' && next == '/' {
				inBlockComment = false
				i++
			}
			continue
		}

		if inString {
			b.WriteRune(r)
			if r == '\\' && i+1 < n {
				i++
				b.WriteRune(chars[i])
			} else if r == quoteChar {
				inString = false
			}
			continue
		}

		// Not in string or comment
		if r == '"' || r == '\'' {
			inString = true
			quoteChar = r
			b.WriteRune(r)
			continue
		}

		if r == '/' && next == '/' {
			inLineComment = true
			i++
			continue
		}

		if r == '/' && next == '*' {
			inBlockComment = true
			i++
			continue
		}

		b.WriteRune(r)
	}

	return b.String()
}

// parseTestBlocks extracts test blocks, lifecycle hooks, and top-level let declarations.
func parseTestBlocks(content string) ([]testBlock, []string) {
	lines := strings.Split(content, "\n")
	var blocks []testBlock
	var topLevelLets []string

	depth := 0
	inString := false
	var quoteChar rune
	inBlockComment := false

	var curBlock *testBlock
	curBlockDepth := 0
	var curBody strings.Builder

	for lineIdx, line := range lines {
		lineNum := lineIdx + 1
		trimmed := strings.TrimSpace(line)

		// Check for top-level let declarations at depth 0
		if depth == 0 && curBlock == nil {
			if m := edgeTopLevelLetRE.FindStringSubmatch(trimmed); len(m) > 1 {
				topLevelLets = append(topLevelLets, m[1])
			}
		}

		// Detect block start if not currently recording one
		if curBlock == nil {
			var kind, name string
			if m := edgeTestCallRE.FindStringSubmatch(line); len(m) > 1 {
				kind = "test"
				name = m[1]
			} else if edgeBeforeEachRE.MatchString(line) {
				kind = "beforeEach"
				name = "beforeEach"
			} else if edgeAfterEachRE.MatchString(line) {
				kind = "afterEach"
				name = "afterEach"
			} else if edgeBeforeAllRE.MatchString(line) {
				kind = "beforeAll"
				name = "beforeAll"
			} else if edgeAfterAllRE.MatchString(line) {
				kind = "afterAll"
				name = "afterAll"
			}

			if kind != "" {
				curBlock = &testBlock{
					kind:      kind,
					name:      name,
					startLine: lineNum,
				}
				curBlockDepth = depth + 1
				curBody.Reset()
			}
		}

		// Append line to active block body
		if curBlock != nil {
			curBody.WriteString(line)
			curBody.WriteString("\n")
		}

		// Count brace depth changes on this line
		chars := []rune(line)
		for i := 0; i < len(chars); i++ {
			c := chars[i]
			next := rune(0)
			if i+1 < len(chars) {
				next = chars[i+1]
			}

			if inBlockComment {
				if c == '*' && next == '/' {
					inBlockComment = false
					i++
				}
				continue
			}

			if inString {
				if c == '\\' && i+1 < len(chars) {
					i++
				} else if c == quoteChar {
					inString = false
				}
				continue
			}

			if c == '/' && next == '/' {
				break // rest of line is line comment
			}
			if c == '/' && next == '*' {
				inBlockComment = true
				i++
				continue
			}

			if c == '"' || c == '\'' || c == '`' {
				inString = true
				quoteChar = c
				continue
			}

			if c == '{' {
				depth++
			} else if c == '}' {
				depth--
				// Check if current block closed
				if curBlock != nil && depth < curBlockDepth {
					curBlock.endLine = lineNum
					curBlock.body = curBody.String()
					blocks = append(blocks, *curBlock)
					curBlock = nil
				}
			}
		}
	}

	// Flush unclosed block if file ended
	if curBlock != nil {
		curBlock.endLine = len(lines)
		curBlock.body = curBody.String()
		blocks = append(blocks, *curBlock)
	}

	return blocks, topLevelLets
}

// RenderEdgeAuditTOON serializes the EdgeAuditReport into Token-Oriented Object Notation format.
func RenderEdgeAuditTOON(rep *EdgeAuditReport) string {
	var b strings.Builder
	summaryFields := [][2]string{
		{"applicable", fmt.Sprintf("%t", rep.Applicable)},
		{"status", rep.Status},
		{"passed", fmt.Sprintf("%t", rep.Passed)},
		{"framework", rep.Framework},
		{"total_files", fmt.Sprintf("%d", rep.Summary.TotalFiles)},
		{"isolated_files", fmt.Sprintf("%d", rep.Summary.IsolatedFiles)},
		{"total_tests", fmt.Sprintf("%d", rep.Summary.TotalTests)},
		{"mutating_tests", fmt.Sprintf("%d", rep.Summary.MutatingTests)},
		{"violations", fmt.Sprintf("%d", rep.Summary.Violations)},
		{"warnings", fmt.Sprintf("%d", rep.Summary.Warnings)},
		{"audit_time", rep.AuditTime.Format(time.RFC3339)},
	}
	b.WriteString(render.FormatTOONObject("edge_audit_summary", summaryFields))

	if len(rep.Issues) > 0 {
		headers := []string{"rule_id", "severity", "file", "line", "test_name", "message", "suggestion"}
		var rows [][]string
		for _, is := range rep.Issues {
			rows = append(rows, []string{
				is.RuleID,
				is.Severity,
				is.File,
				fmt.Sprintf("%d", is.Line),
				is.TestName,
				is.Message,
				is.Suggestion,
			})
		}
		b.WriteString(render.FormatTOONTable("issues", headers, rows))
	}

	return b.String()
}
