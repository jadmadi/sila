package gate

import (
	"bufio"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var (
	// d1QueryRegex matches D1 async query operations
	d1QueryRegex = regexp.MustCompile(`await\s+(?:[a-zA-Z0-9_$.]+\.)?(?:db|env\.DB|d1)\.(?:insert|update|delete|prepare)`)
	// loopStartRegex matches start of a for/while/forEach loop
	loopStartRegex = regexp.MustCompile(`\b(?:for\s*\(|for\s+await\s*\(|while\s*\(|\.forEach\s*\()`)

	// ansiEscapeRegex matches raw ANSI sequences in code
	ansiEscapeRegex = regexp.MustCompile(`(?:\x1b\[|\033\[|\\033\[|\\x1b\[|\\x1B\[)`)

	// mockKeyRegex matches contiguous monolithic mock keys or certificates
	mockKeyRegex = regexp.MustCompile(`-----BEGIN (?:[A-Z0-9_-]+ )?(?:PRIVATE KEY|CERTIFICATE)-----`)

	// diffHunkHeaderRegex matches @@ -old,count +new,count @@
	diffHunkHeaderRegex = regexp.MustCompile(`^@@\s+-[0-9]+(?:,[0-9]+)?\s+\+([0-9]+)(?:,[0-9]+)?\s+@@`)
)

// ArchViolation represents a detected architectural anti-pattern in a staged git diff.
type ArchViolation struct {
	File        string `json:"file"`
	Line        int    `json:"line"`
	Rule        string `json:"rule"`
	Principle   string `json:"principle"`
	Remedy      string `json:"remedy"`
	CodeSnippet string `json:"code_snippet"`
}

// Format returns a human-readable diagnostic message explaining the violation with exact file:line and remedy.
func (v ArchViolation) Format() string {
	lineStr := strconv.Itoa(v.Line)
	if v.Line <= 0 {
		lineStr = "-"
	}
	return fmt.Sprintf(
		"✖ [%s] %s:%s\n  Principle: %s\n  Violation: %s\n  Snippet:   %s\n  Remedy:    %s",
		v.Rule, v.File, lineStr, v.Principle, v.Rule, strings.TrimSpace(v.CodeSnippet), v.Remedy,
	)
}

// ArchScanResult encapsulates the outcome of scanning a git diff.
type ArchScanResult struct {
	Clean      bool            `json:"clean"`
	Violations []ArchViolation `json:"violations"`
	LinesRead  int             `json:"lines_read"`
}

// ScanArchDiff inspects a unified git diff in-memory for architectural anti-patterns.
func ScanArchDiff(diffContent string) ArchScanResult {
	res := ArchScanResult{
		Clean:      true,
		Violations: make([]ArchViolation, 0),
	}

	scanner := bufio.NewScanner(strings.NewReader(diffContent))
	var currentFile string
	var isWorker, isCLI, isTest bool
	var currentLineNum int
	inHunk := false
	loopDepth := 0

	for scanner.Scan() {
		res.LinesRead++
		rawLine := scanner.Text()

		// Track current file from diff header
		if strings.HasPrefix(rawLine, "diff --git ") {
			parts := strings.Split(rawLine, " ")
			if len(parts) >= 4 {
				currentFile = strings.TrimPrefix(parts[3], "b/")
				isWorker = isWorkerSourceFile(currentFile)
				isCLI = isCLISourceFile(currentFile) && !isRenderOrTestFile(currentFile)
				isTest = isTestFile(currentFile)
			}
			inHunk = false
			loopDepth = 0
			continue
		}
		if strings.HasPrefix(rawLine, "+++ b/") {
			currentFile = strings.TrimPrefix(rawLine, "+++ b/")
			isWorker = isWorkerSourceFile(currentFile)
			isCLI = isCLISourceFile(currentFile) && !isRenderOrTestFile(currentFile)
			isTest = isTestFile(currentFile)
			inHunk = false
			loopDepth = 0
			continue
		}

		// Parse hunk header
		if strings.HasPrefix(rawLine, "@@ ") {
			matches := diffHunkHeaderRegex.FindStringSubmatch(rawLine)
			if len(matches) >= 2 {
				currentLineNum, _ = strconv.Atoi(matches[1])
				inHunk = true
				loopDepth = 0
			}
			continue
		}

		if !inHunk {
			continue
		}

		// Context line
		if strings.HasPrefix(rawLine, " ") {
			currentLineNum++
			if isWorker {
				lineText := rawLine[1:]
				loopDepth += countLoopBraceDelta(lineText)
				if loopDepth < 0 {
					loopDepth = 0
				}
			}
			continue
		}

		// Deleted line (does not advance new file line counter)
		if strings.HasPrefix(rawLine, "-") {
			continue
		}

		// Added line
		if strings.HasPrefix(rawLine, "+") {
			addedContent := rawLine[1:]
			lineNum := currentLineNum
			currentLineNum++

			// Check Rule 1: Unbatched Cloudflare D1 Loops in Workers
			if isWorker {
				hasLoopCandidate := (loopDepth > 0 || hasLoopStart(addedContent))
				if hasLoopCandidate && hasD1Query(addedContent) {
					res.Clean = false
					res.Violations = append(res.Violations, ArchViolation{
						File:        currentFile,
						Line:        lineNum,
						Rule:        "unbatched-d1-loop",
						Principle:   "Cloudflare Edge Discipline (D1 Batching Principle)",
						Remedy:      "Violates Cloudflare D1 batching principle; use env.DB.batch([...]) or batch statements to prevent N+1 query billing inflation and V8 CPU timeouts.",
						CodeSnippet: addedContent,
					})
				}
				loopDepth += countLoopBraceDelta(addedContent)
				if loopDepth < 0 {
					loopDepth = 0
				}
			}

			// Check Rule 2: Raw ANSI Escape Codes in CLI Output
			if isCLI && hasRawANSI(addedContent) {
				res.Clean = false
				res.Violations = append(res.Violations, ArchViolation{
					File:        currentFile,
					Line:        lineNum,
					Rule:        "raw-ansi-escape",
					Principle:   "CUPID (Unix-philosophy piping)",
					Remedy:      "Raw ANSI sequences break piped Unix output; use render.UI abstractions to respect --plain, --no-color, and pipe redirection.",
					CodeSnippet: addedContent,
				})
			}

			// Check Rule 3: Contiguous Synthetic Test Credentials
			if isTest && hasMockCredential(addedContent) {
				res.Clean = false
				res.Violations = append(res.Violations, ArchViolation{
					File:        currentFile,
					Line:        lineNum,
					Rule:        "contiguous-synthetic-credential",
					Principle:   "Security / Credential Sentry",
					Remedy:      "Author mock credentials as split concatenations to avoid triggering diff regex scanners.",
					CodeSnippet: addedContent,
				})
			}
		}
	}

	return res
}

func hasLoopStart(line string) bool {
	if strings.Contains(line, "for") || strings.Contains(line, "while") || strings.Contains(line, "forEach") {
		return loopStartRegex.MatchString(line)
	}
	return false
}

func hasD1Query(line string) bool {
	if strings.Contains(line, "await") && (strings.Contains(line, "db.") || strings.Contains(line, "DB.") || strings.Contains(line, "d1.")) {
		return d1QueryRegex.MatchString(line)
	}
	return false
}

func hasRawANSI(line string) bool {
	if strings.Contains(line, "\033[") || strings.Contains(line, "\x1b[") ||
		strings.Contains(line, `\033[`) || strings.Contains(line, `\x1b[`) || strings.Contains(line, `\x1B[`) {
		return ansiEscapeRegex.MatchString(line)
	}
	return false
}

func hasMockCredential(line string) bool {
	if strings.Contains(line, "-----BEGIN") {
		return mockKeyRegex.MatchString(line)
	}
	return false
}

func countLoopBraceDelta(line string) int {
	if !strings.Contains(line, "{") && !strings.Contains(line, "}") {
		if hasLoopStart(line) {
			// Single line loop without braces (e.g. for (...) doSomething())
			return 1
		}
		return 0
	}
	openBraces := strings.Count(line, "{")
	closeBraces := strings.Count(line, "}")
	return openBraces - closeBraces
}

func isWorkerSourceFile(path string) bool {
	norm := strings.ToLower(path)
	if strings.HasPrefix(norm, "worker/") || strings.Contains(norm, "/worker/") {
		return strings.HasSuffix(norm, ".ts") || strings.HasSuffix(norm, ".js")
	}
	return false
}

func isCLISourceFile(path string) bool {
	norm := strings.ToLower(path)
	if strings.HasSuffix(norm, ".go") {
		return true
	}
	return false
}

func isRenderOrTestFile(path string) bool {
	norm := strings.ToLower(path)
	if strings.Contains(norm, "internal/render/") || strings.HasPrefix(norm, "render/") ||
		strings.Contains(norm, "internal/gate/") || strings.HasPrefix(norm, "gate/") {
		return true
	}
	if strings.HasSuffix(norm, "_test.go") {
		return true
	}
	return false
}

func isTestFile(path string) bool {
	norm := strings.ToLower(path)
	return strings.HasSuffix(norm, "_test.go") ||
		strings.HasSuffix(norm, ".test.ts") ||
		strings.HasSuffix(norm, ".spec.ts") ||
		strings.HasSuffix(norm, ".test.js") ||
		strings.HasSuffix(norm, ".spec.js") ||
		strings.Contains(norm, "/testdata/") ||
		strings.Contains(norm, "/fixtures/")
}
