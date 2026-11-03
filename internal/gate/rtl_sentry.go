package gate

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/store"
)

// RTLViolation records an instance of a physical directional CSS property or class
// in a project that enforces RTL logical properties.
type RTLViolation struct {
	File        string `json:"file"`
	Line        int    `json:"line"`
	Physical    string `json:"physical"`
	Suggested   string `json:"suggested"`
	CodeSnippet string `json:"code_snippet"`
}

// Format returns a concise diagnostic string with file:line and remedy.
func (v RTLViolation) Format() string {
	lineStr := strconv.Itoa(v.Line)
	if v.Line <= 0 {
		lineStr = "-"
	}
	return fmt.Sprintf(
		"✖ [rtl-logical-properties] %s:%s\n  Physical:  %s\n  Suggested: %s\n  Snippet:   %s",
		v.File, lineStr, v.Physical, v.Suggested, strings.TrimSpace(v.CodeSnippet),
	)
}

// RTLSentryResult encapsulates the findings of the RTL logical properties linter.
type RTLSentryResult struct {
	Clean      bool           `json:"clean"`
	Skipped    bool           `json:"skipped"`
	SkipReason string         `json:"skip_reason,omitempty"`
	Violations []RTLViolation `json:"violations"`
	Duration   time.Duration  `json:"duration"`
}

// Physical property and class matchers.
var (
	// Tailwind class tokens in class="..." or className="..."
	tailwindPhysicalPatterns = []struct {
		pattern   *regexp.Regexp
		name      string
		suggested string
	}{
		{pattern: regexp.MustCompile(`\b(-?)pl-(\d+|\[[^\]]+\])\b`), name: "pl-*", suggested: "${1}ps-${2}"},
		{pattern: regexp.MustCompile(`\b(-?)pr-(\d+|\[[^\]]+\])\b`), name: "pr-*", suggested: "${1}pe-${2}"},
		{pattern: regexp.MustCompile(`\b(-?)ml-(\d+|auto|\[[^\]]+\])\b`), name: "ml-*", suggested: "${1}ms-${2}"},
		{pattern: regexp.MustCompile(`\b(-?)mr-(\d+|auto|\[[^\]]+\])\b`), name: "mr-*", suggested: "${1}me-${2}"},
		{pattern: regexp.MustCompile(`\b(-?)left-(\d+|auto|full|px|\[[^\]]+\])\b`), name: "left-*", suggested: "${1}start-${2}"},
		{pattern: regexp.MustCompile(`\b(-?)right-(\d+|auto|full|px|\[[^\]]+\])\b`), name: "right-*", suggested: "${1}end-${2}"},
		{pattern: regexp.MustCompile(`\btext-left\b`), name: "text-left", suggested: "text-start"},
		{pattern: regexp.MustCompile(`\btext-right\b`), name: "text-right", suggested: "text-end"},
		{pattern: regexp.MustCompile(`\bborder-l\b`), name: "border-l", suggested: "border-s"},
		{pattern: regexp.MustCompile(`\bborder-l-(\d+|\[[^\]]+\])\b`), name: "border-l-*", suggested: "border-s-${1}"},
		{pattern: regexp.MustCompile(`\bborder-r\b`), name: "border-r", suggested: "border-e"},
		{pattern: regexp.MustCompile(`\bborder-r-(\d+|\[[^\]]+\])\b`), name: "border-r-*", suggested: "border-e-${1}"},
		{pattern: regexp.MustCompile(`\brounded-l\b`), name: "rounded-l", suggested: "rounded-s"},
		{pattern: regexp.MustCompile(`\brounded-l-(\w+|\[[^\]]+\])\b`), name: "rounded-l-*", suggested: "rounded-s-${1}"},
		{pattern: regexp.MustCompile(`\brounded-r\b`), name: "rounded-r", suggested: "rounded-e"},
		{pattern: regexp.MustCompile(`\brounded-r-(\w+|\[[^\]]+\])\b`), name: "rounded-r-*", suggested: "rounded-e-${1}"},
		{pattern: regexp.MustCompile(`\bfloat-left\b`), name: "float-left", suggested: "float-start"},
		{pattern: regexp.MustCompile(`\bfloat-right\b`), name: "float-right", suggested: "float-end"},
		{pattern: regexp.MustCompile(`\bclear-left\b`), name: "clear-left", suggested: "clear-start"},
		{pattern: regexp.MustCompile(`\bclear-right\b`), name: "clear-right", suggested: "clear-end"},
	}

	// Raw CSS property declarations
	rawCSSPhysicalPatterns = []struct {
		pattern   *regexp.Regexp
		name      string
		suggested string
	}{
		{pattern: regexp.MustCompile(`(?i)\bpadding-left\s*:`), name: "padding-left", suggested: "padding-inline-start"},
		{pattern: regexp.MustCompile(`(?i)\bpadding-right\s*:`), name: "padding-right", suggested: "padding-inline-end"},
		{pattern: regexp.MustCompile(`(?i)\bmargin-left\s*:`), name: "margin-left", suggested: "margin-inline-start"},
		{pattern: regexp.MustCompile(`(?i)\bmargin-right\s*:`), name: "margin-right", suggested: "margin-inline-end"},
		{pattern: regexp.MustCompile(`(?i)\bborder-left\s*:`), name: "border-left", suggested: "border-inline-start"},
		{pattern: regexp.MustCompile(`(?i)\bborder-right\s*:`), name: "border-right", suggested: "border-inline-end"},
		{pattern: regexp.MustCompile(`(?i)\bborder-left-width\s*:`), name: "border-left-width", suggested: "border-inline-start-width"},
		{pattern: regexp.MustCompile(`(?i)\bborder-right-width\s*:`), name: "border-right-width", suggested: "border-inline-end-width"},
		{pattern: regexp.MustCompile(`(?i)\bborder-left-color\s*:`), name: "border-left-color", suggested: "border-inline-start-color"},
		{pattern: regexp.MustCompile(`(?i)\bborder-right-color\s*:`), name: "border-right-color", suggested: "border-inline-end-color"},
		{pattern: regexp.MustCompile(`(?i)\btext-align\s*:\s*left\b`), name: "text-align: left", suggested: "text-align: start"},
		{pattern: regexp.MustCompile(`(?i)\btext-align\s*:\s*right\b`), name: "text-align: right", suggested: "text-align: end"},
	}
)

// IsRTLProject inspects the repository root and knowledge store to determine whether
// RTL conventions apply (Arabic locale, explicit RTL invariant, or dir="rtl").
func IsRTLProject(repoRoot string, st *store.Store) bool {
	if os.Getenv("SILA_CHECK_RTL") == "1" {
		return true
	}
	if os.Getenv("SILA_SKIP_RTL") == "1" {
		return false
	}

	// 1. Check knowledge store facts for active RTL invariant
	if st != nil {
		facts, err := st.FactsByProject(store.ProjectID(repoRoot))
		if err == nil {
			for _, f := range facts {
				textLower := strings.ToLower(f.Claim)
				if (f.Kind == "invariant" || strings.Contains(textLower, "invariant") || strings.Contains(textLower, "standard")) &&
					(strings.Contains(textLower, "rtl") || strings.Contains(textLower, "arabic") || strings.Contains(textLower, "dir=\"rtl\"")) {
					return true
				}
			}
		}
	}

	// 2. Check AGENTS.md / RULES.md in the repo root for RTL statements
	for _, docName := range []string{"AGENTS.md", "RULES.md", "README.md"} {
		docPath := filepath.Join(repoRoot, docName)
		if content, err := os.ReadFile(docPath); err == nil {
			textLower := strings.ToLower(string(content))
			if strings.Contains(textLower, "rtl invariant") ||
				strings.Contains(textLower, "dir=\"rtl\"") ||
				strings.Contains(textLower, "arabic") && strings.Contains(textLower, "rtl") {
				return true
			}
		}
	}

	return false
}

// IsRTLScannableFile checks if a file extension represents a UI template, component, or stylesheet.
func IsRTLScannableFile(filePath string) bool {
	ext := strings.ToLower(filepath.Ext(filePath))
	switch ext {
	case ".html", ".htm", ".jsx", ".tsx", ".vue", ".svelte", ".astro",
		".css", ".scss", ".sass", ".less", ".php":
		return true
	default:
		return false
	}
}

// ScanRTLDiff scans a staged git diff for physical directional CSS classes or properties.
func ScanRTLDiff(diffContent string) []RTLViolation {
	var violations []RTLViolation
	scanner := bufio.NewScanner(strings.NewReader(diffContent))

	var currentFile string
	var scannable bool
	var currentLineNum int
	inHunk := false

	for scanner.Scan() {
		rawLine := scanner.Text()

		if strings.HasPrefix(rawLine, "diff --git ") {
			parts := strings.Split(rawLine, " ")
			if len(parts) >= 4 {
				currentFile = strings.TrimPrefix(parts[3], "b/")
				scannable = IsRTLScannableFile(currentFile)
			}
			inHunk = false
			continue
		}
		if strings.HasPrefix(rawLine, "+++ b/") {
			currentFile = strings.TrimPrefix(rawLine, "+++ b/")
			scannable = IsRTLScannableFile(currentFile)
			inHunk = false
			continue
		}

		if strings.HasPrefix(rawLine, "@@ ") {
			matches := diffHunkHeaderRegex.FindStringSubmatch(rawLine)
			if len(matches) >= 2 {
				currentLineNum, _ = strconv.Atoi(matches[1])
				inHunk = true
			}
			continue
		}

		if !inHunk || !scannable {
			continue
		}

		// Only scan newly added or modified lines in diff
		if strings.HasPrefix(rawLine, "+") && !strings.HasPrefix(rawLine, "+++") {
			content := strings.TrimPrefix(rawLine, "+")
			lineV := CheckLineForPhysicalCSS(currentFile, currentLineNum, content)
			violations = append(violations, lineV...)
			currentLineNum++
		} else if !strings.HasPrefix(rawLine, "-") {
			currentLineNum++
		}
	}

	return violations
}

// CheckLineForPhysicalCSS scans a single source code line for physical directional CSS patterns.
func CheckLineForPhysicalCSS(file string, lineNum int, line string) []RTLViolation {
	var violations []RTLViolation
	trimmed := strings.TrimSpace(line)

	// Skip pure comment lines
	if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "*") {
		return nil
	}

	// 1. Check Tailwind physical directional classes
	for _, rule := range tailwindPhysicalPatterns {
		if rule.pattern.MatchString(line) {
			matches := rule.pattern.FindAllString(line, -1)
			for _, m := range matches {
				suggested := rule.pattern.ReplaceAllString(m, rule.suggested)
				violations = append(violations, RTLViolation{
					File:        file,
					Line:        lineNum,
					Physical:    m,
					Suggested:   suggested,
					CodeSnippet: line,
				})
			}
		}
	}

	// 2. Check Raw CSS declarations (e.g. padding-left:, margin-right:)
	for _, rule := range rawCSSPhysicalPatterns {
		if rule.pattern.MatchString(line) {
			violations = append(violations, RTLViolation{
				File:        file,
				Line:        lineNum,
				Physical:    rule.name,
				Suggested:   rule.suggested,
				CodeSnippet: line,
			})
		}
	}

	return violations
}

// RunRTLSentry executes the RTL logical properties check over staged changes.
func RunRTLSentry(repoRoot string, st *store.Store, diffContent string) RTLSentryResult {
	start := time.Now()
	res := RTLSentryResult{
		Clean:      true,
		Violations: make([]RTLViolation, 0),
	}

	if !IsRTLProject(repoRoot, st) {
		res.Skipped = true
		res.SkipReason = "no active RTL invariant or Arabic locale declared"
		res.Duration = time.Since(start)
		return res
	}

	violations := ScanRTLDiff(diffContent)
	if len(violations) > 0 {
		res.Clean = false
		res.Violations = violations
	}

	res.Duration = time.Since(start)
	return res
}
