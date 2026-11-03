package assess

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// CFSentryViolation represents an anti-pattern, security defect, or isolate mismatch
// in Cloudflare Workers codebase.
type CFSentryViolation struct {
	RuleID      string `json:"rule_id"`
	File        string `json:"file"`
	LineNumber  int    `json:"line_number,omitempty"`
	LineContent string `json:"line_content,omitempty"`
	Message     string `json:"message"`
	Severity    string `json:"severity"` // "error" | "warning"
	Suggestion  string `json:"suggestion,omitempty"`
}

// Format returns a human-readable description of the violation.
func (v CFSentryViolation) Format() string {
	loc := v.File
	if v.LineNumber > 0 {
		loc = fmt.Sprintf("%s:%d", v.File, v.LineNumber)
	}
	sugg := ""
	if v.Suggestion != "" {
		sugg = fmt.Sprintf(" (suggestion: %s)", v.Suggestion)
	}
	return fmt.Sprintf("[%s] %s: %s%s", v.RuleID, loc, v.Message, sugg)
}

// CFSentryResult holds the outcome of running the Cloudflare Worker sentry.
type CFSentryResult struct {
	Clean        bool                `json:"clean"`
	Skipped      bool                `json:"skipped"`
	SkipReason   string              `json:"skip_reason,omitempty"`
	Duration     time.Duration       `json:"duration"`
	Violations   []CFSentryViolation `json:"violations"`
	FilesScanned int                 `json:"files_scanned"`
}

// WranglerManifests lists valid Cloudflare Worker configuration filenames.
var WranglerManifests = []string{
	"wrangler.json",
	"wrangler.jsonc",
	"wrangler.toml",
}

// IsCFWorkerProject evaluates whether dir or its ancestor/immediate workspace trees
// contain Cloudflare Worker configuration manifests.
func IsCFWorkerProject(dir string) bool {
	if dir == "" {
		dir = "."
	}
	// 1. Direct directory check
	for _, m := range WranglerManifests {
		if _, err := os.Stat(filepath.Join(dir, m)); err == nil {
			return true
		}
	}

	// 2. Direct subdirectories (e.g. worker/, apps/worker/, apps/api/)
	if entries, err := os.ReadDir(dir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") && entry.Name() != "node_modules" && entry.Name() != "vendor" {
				subDir := filepath.Join(dir, entry.Name())
				for _, m := range WranglerManifests {
					if _, err := os.Stat(filepath.Join(subDir, m)); err == nil {
						return true
					}
				}
			}
		}
	}

	// 3. Ancestor traversal up to git root or 4 levels
	curr, err := filepath.Abs(dir)
	if err != nil {
		curr = dir
	}
	for i := 0; i < 4; i++ {
		parent := filepath.Dir(curr)
		if parent == curr || parent == "" || parent == "/" {
			break
		}
		for _, m := range WranglerManifests {
			if _, err := os.Stat(filepath.Join(parent, m)); err == nil {
				return true
			}
		}
		if _, err := os.Stat(filepath.Join(parent, ".git")); err == nil {
			break
		}
		curr = parent
	}

	return false
}

// isWorkerSourceFile checks if the path represents a TypeScript/JavaScript source or config file.
func isWorkerSourceFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".ts" || ext == ".tsx" || ext == ".js" || ext == ".jsx" || ext == ".mjs" || ext == ".cjs"
}

// isWorkerTestFile checks if the path represents a test or spec file.
func isWorkerTestFile(path string) bool {
	norm := filepath.ToSlash(path)
	base := strings.ToLower(filepath.Base(norm))
	if strings.Contains(base, ".test.") || strings.Contains(base, ".spec.") || strings.HasPrefix(base, "test_") {
		return true
	}
	parts := strings.Split(norm, "/")
	for _, p := range parts {
		low := strings.ToLower(p)
		if low == "test" || low == "tests" || low == "__tests__" {
			return true
		}
	}
	return false
}

// Regex patterns for Rule 1: cf-secret-500-masking
// Catches responses or error throws returning 500 when validating secrets, tokens, or auth.
var (
	// Matches: return c.text("Unauthorized", 500), return c.json({ error: "missing secret" }, 500),
	// return new Response("...", { status: 500 }), etc., in connection with secret/auth/unauthorized
	reSecret500Return1 = regexp.MustCompile(`(?i)(?:return|status:\s*500).*?(?:status:\s*500|HTTPException\(500|,\s*500\b).*?(?:unauthoriz|forbidden|secret|token|api[_-]?key|bearer|auth|access\s+denied)`)
	reSecret500Return2 = regexp.MustCompile(`(?i)(?:unauthoriz|forbidden|secret|token|api[_-]?key|bearer|auth|access\s+denied).*?(?:status:\s*500|HTTPException\(500|,\s*500\b)`)

	// Matches: throw new Error("unauthorized" | "invalid secret" | "missing token" | "forbidden")
	reSecretThrowError = regexp.MustCompile(`(?i)throw\s+new\s+(?:Error|TypeError)\s*\(\s*['"](?:[^'"]*\b(?:unauthorized|forbidden|invalid\s+(?:secret|token|key|auth|api_key)|missing\s+(?:secret|token|key|auth|api_key)|access\s+denied)\b[^'"]*)['"]\s*\)`)

	// Matches: throw new HTTPException(500, ...) with auth/secret
	reSecretThrowHTTP500 = regexp.MustCompile(`(?i)throw\s+new\s+HTTPException\s*\(\s*500\b`)

	// Matches: process.env.NODE_ENV
	reTestNodeEnv = regexp.MustCompile(`\bprocess\.env\.NODE_ENV\b`)
)

// ScanCFWorkerDiff inspects unified git diff additions for Cloudflare Worker anti-patterns.
func ScanCFWorkerDiff(diffContent string) []CFSentryViolation {
	var violations []CFSentryViolation
	if strings.TrimSpace(diffContent) == "" {
		return violations
	}

	scanner := bufio.NewScanner(strings.NewReader(diffContent))
	var currentFile string
	var currentLineNumber int
	var isDeleted bool

	hunkRegex := regexp.MustCompile(`^@@\s+-[0-9]+(?:,[0-9]+)?\s+\+([0-9]+)(?:,[0-9]+)?\s+@@`)
	seen := make(map[string]bool)

	addViolation := func(v CFSentryViolation) {
		key := fmt.Sprintf("%s:%s:%d:%s", v.RuleID, v.File, v.LineNumber, v.LineContent)
		if !seen[key] {
			seen[key] = true
			violations = append(violations, v)
		}
	}

	for scanner.Scan() {
		line := scanner.Text()

		if strings.HasPrefix(line, "diff --git ") {
			parts := strings.Fields(line)
			if len(parts) >= 4 {
				currentFile = strings.TrimPrefix(parts[3], "b/")
				isDeleted = false
			}
			continue
		}

		if strings.HasPrefix(line, "deleted file mode") {
			isDeleted = true
			continue
		}

		if strings.HasPrefix(line, "+++ b/") {
			currentFile = strings.TrimSpace(strings.TrimPrefix(line, "+++ b/"))
			continue
		}

		if strings.HasPrefix(line, "--- ") {
			continue
		}

		if m := hunkRegex.FindStringSubmatch(line); len(m) == 2 {
			if start, err := strconv.Atoi(m[1]); err == nil {
				currentLineNumber = start
			}
			continue
		}

		if isDeleted || currentFile == "" {
			continue
		}

		if strings.HasPrefix(line, "+") {
			content := line[1:]
			trimmed := strings.TrimSpace(content)

			if isWorkerSourceFile(currentFile) {
				// Rule 1: Check for secret/auth 500 masking
				if reSecret500Return1.MatchString(trimmed) || reSecret500Return2.MatchString(trimmed) || reSecretThrowError.MatchString(trimmed) || reSecretThrowHTTP500.MatchString(trimmed) {
					addViolation(CFSentryViolation{
						RuleID:      "cf-secret-500-masking",
						File:        currentFile,
						LineNumber:  currentLineNumber,
						LineContent: trimmed,
						Message:     "Top-level or middleware secret check returns/throws HTTP 500 instead of 401 Unauthorized or 403 Forbidden, masking Zero Trust gates.",
						Severity:    "error",
						Suggestion:  "Return HTTP 401 Unauthorized or 403 Forbidden (e.g. c.text('Unauthorized', 401) or throw new HTTPException(401)) to preserve Zero Trust access flow.",
					})
				}

				// Rule 2: Check for process.env.NODE_ENV in test files
				if isWorkerTestFile(currentFile) && reTestNodeEnv.MatchString(trimmed) {
					addViolation(CFSentryViolation{
						RuleID:      "cf-test-node-env-check",
						File:        currentFile,
						LineNumber:  currentLineNumber,
						LineContent: trimmed,
						Message:     "Workerd isolate test file inspects 'process.env.NODE_ENV'. In Cloudflare Workers isolate environments, use Worker env bindings instead of Node.js process.env.",
						Severity:    "error",
						Suggestion:  "Replace 'process.env.NODE_ENV' with Worker env bindings (e.g. env.ENVIRONMENT) or Vitest pool bindings.",
					})
				}
			}

			currentLineNumber++
		} else if !strings.HasPrefix(line, "-") {
			currentLineNumber++
		}
	}

	return violations
}

// ScanCFWorkerFile inspects a full file's content line-by-line for Cloudflare Worker anti-patterns.
func ScanCFWorkerFile(filePath, content string) []CFSentryViolation {
	var violations []CFSentryViolation
	if !isWorkerSourceFile(filePath) {
		return violations
	}

	lines := strings.Split(content, "\n")
	isTest := isWorkerTestFile(filePath)

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		lineNum := i + 1

		// Rule 1: Check for secret/auth 500 masking
		if reSecret500Return1.MatchString(trimmed) || reSecret500Return2.MatchString(trimmed) || reSecretThrowError.MatchString(trimmed) || reSecretThrowHTTP500.MatchString(trimmed) {
			violations = append(violations, CFSentryViolation{
				RuleID:      "cf-secret-500-masking",
				File:        filePath,
				LineNumber:  lineNum,
				LineContent: trimmed,
				Message:     "Top-level or middleware secret check returns/throws HTTP 500 instead of 401 Unauthorized or 403 Forbidden, masking Zero Trust gates.",
				Severity:    "error",
				Suggestion:  "Return HTTP 401 Unauthorized or 403 Forbidden (e.g. c.text('Unauthorized', 401) or throw new HTTPException(401)) to preserve Zero Trust access flow.",
			})
		}

		// Rule 2: Check for process.env.NODE_ENV in test files
		if isTest && reTestNodeEnv.MatchString(trimmed) {
			violations = append(violations, CFSentryViolation{
				RuleID:      "cf-test-node-env-check",
				File:        filePath,
				LineNumber:  lineNum,
				LineContent: trimmed,
				Message:     "Workerd isolate test file inspects 'process.env.NODE_ENV'. In Cloudflare Workers isolate environments, use Worker env bindings instead of Node.js process.env.",
				Severity:    "error",
				Suggestion:  "Replace 'process.env.NODE_ENV' with Worker env bindings (e.g. env.ENVIRONMENT) or Vitest pool bindings.",
			})
		}
	}

	return violations
}

// RunCFWorkerSentry audits a project directory and its diff against Cloudflare Worker invariants.
func RunCFWorkerSentry(dir string, diffContent string) CFSentryResult {
	start := time.Now()
	res := CFSentryResult{
		Clean:      true,
		Violations: []CFSentryViolation{},
	}

	if !IsCFWorkerProject(dir) {
		res.Skipped = true
		res.SkipReason = "no Cloudflare Worker configuration (wrangler.json/jsonc/toml) detected"
		res.Duration = time.Since(start)
		return res
	}

	// 1. Audit git diff if present
	if strings.TrimSpace(diffContent) != "" {
		diffViolations := ScanCFWorkerDiff(diffContent)
		res.Violations = append(res.Violations, diffViolations...)
	}

	// 2. If no violations found from diff, or if diff was empty, optionally scan workspace source files
	if len(res.Violations) == 0 && strings.TrimSpace(diffContent) == "" {
		filesScanned := 0
		_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if info.IsDir() {
				name := info.Name()
				if strings.HasPrefix(name, ".") || name == "node_modules" || name == "dist" || name == "build" || name == "vendor" {
					return filepath.SkipDir
				}
				return nil
			}
			if isWorkerSourceFile(path) {
				filesScanned++
				data, readErr := os.ReadFile(path)
				if readErr == nil {
					rel, _ := filepath.Rel(dir, path)
					if rel == "" {
						rel = path
					}
					v := ScanCFWorkerFile(rel, string(data))
					res.Violations = append(res.Violations, v...)
				}
			}
			return nil
		})
		res.FilesScanned = filesScanned
	}

	if len(res.Violations) > 0 {
		res.Clean = false
	}
	res.Duration = time.Since(start)
	return res
}
