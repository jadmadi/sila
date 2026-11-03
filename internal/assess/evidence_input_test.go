package assess

import (
	"strings"
	"testing"
)

func TestParseEvidenceInputMarkdownBoundaries(t *testing.T) {
	// 1. Classification
	logInput := []byte("plain log output with warning: something")
	parsedLog, err := ParseEvidenceInput("output.log", logInput)
	if err != nil {
		t.Fatalf("unexpected error parsing log: %v", err)
	}
	if parsedLog.Format != "log" {
		t.Fatalf("expected format log, got %s", parsedLog.Format)
	}
	if parsedLog.Stdout != string(logInput) {
		t.Fatalf("expected stdout to match raw log data")
	}
	if parsedLog.Document != "" {
		t.Fatalf("expected empty document for log format")
	}

	// 2. Oversize check (> 1 MiB)
	hugeData := make([]byte, MaxEvidenceInputSize+10)
	for i := range hugeData {
		hugeData[i] = 'a'
	}
	_, err = ParseEvidenceInput("review.md", hugeData)
	if err == nil || !strings.Contains(err.Error(), "EVIDENCE_TOO_LARGE") {
		t.Fatalf("expected EVIDENCE_TOO_LARGE error for > 1MiB input, got: %v", err)
	}

	// 3. Empty / whitespace check
	_, err = ParseEvidenceInput("review.md", []byte("   \n\t  \n"))
	if err == nil || !strings.Contains(err.Error(), "EVIDENCE_EMPTY") {
		t.Fatalf("expected EVIDENCE_EMPTY error for whitespace input, got: %v", err)
	}

	// 4. Verbatim document preservation and stream fence extraction
	mdText := `# Review Report: Handling warning cases

Verdict: ACCEPTED WITH FINDINGS

## Narrative
We observed several warnings during build, but they are expected.
See the following code:
` + "```go" + `
func WarningHandler() {
	println("not a real log")
}
` + "```" + `

Here is actual test execution:
` + "```stdout" + `
=== RUN   TestSample
--- PASS: TestSample (0.00s)
PASS
` + "```" + `

And stderr:
` + "~~~stderr" + `
[warn] deprecated config key
` + "~~~" + `

` + "```stdout" + `
coverage: 100% of statements
` + "```" + `
`
	input, err := ParseEvidenceInput("review.MD", []byte(mdText))
	if err != nil {
		t.Fatalf("unexpected error parsing markdown: %v", err)
	}
	if input.Format != "markdown" {
		t.Fatalf("expected format markdown, got %s", input.Format)
	}
	if input.Document != mdText {
		t.Fatalf("expected Document to preserve markdown verbatim")
	}
	if input.Verdict != "ACCEPTED WITH FINDINGS" {
		t.Fatalf("expected verdict 'ACCEPTED WITH FINDINGS', got %q", input.Verdict)
	}
	expectedStdout := "=== RUN   TestSample\n--- PASS: TestSample (0.00s)\nPASS\ncoverage: 100% of statements\n"
	if input.Stdout != expectedStdout {
		t.Fatalf("expected stdout %q, got %q", expectedStdout, input.Stdout)
	}
	expectedStderr := "[warn] deprecated config key\n"
	if input.Stderr != expectedStderr {
		t.Fatalf("expected stderr %q, got %q", expectedStderr, input.Stderr)
	}
	// Generic fence content and narrative containing 'warning' must not be in stdout/stderr
	if strings.Contains(input.Stdout, "WarningHandler") || strings.Contains(input.Stderr, "WarningHandler") {
		t.Fatalf("generic code fence was incorrectly routed to stream")
	}

	// 5. Unterminated recognized log fence
	badFence := `# Review
Verdict: PASS
` + "```stdout" + `
unterminated output
`
	_, err = ParseEvidenceInput("review.md", []byte(badFence))
	if err == nil || !strings.Contains(err.Error(), "EVIDENCE_MALFORMED") {
		t.Fatalf("expected EVIDENCE_MALFORMED for unterminated fence, got: %v", err)
	}

	// 6. Standalone verdict formats
	verdictVariants := []struct {
		text     string
		expected string
	}{
		{"# Report\n**Verdict:** PASS", "PASS"},
		{"# Report\n**Verdict**: ACCEPTED", "ACCEPTED"},
		{"# Report\nVerdict: **DONE**", "DONE"},
		{"# Report\n*Verdict:* ACCEPTED WITH FINDINGS", "ACCEPTED WITH FINDINGS"},
		{"# Report\n## Verdict\n\n**PASS**\n", "PASS"},
		{"# Report\n## Verdict\nVerdict: ACCEPTED\n", "ACCEPTED"},
	}
	for _, v := range verdictVariants {
		res, vErr := ParseEvidenceInput("test.md", []byte(v.text))
		if vErr != nil {
			t.Fatalf("unexpected error for %q: %v", v.text, vErr)
		}
		if res.Verdict != v.expected {
			t.Fatalf("for %q expected verdict %q, got %q", v.text, v.expected, res.Verdict)
		}
	}

	// 7. Missing verdict
	_, err = ParseEvidenceInput("test.md", []byte("# Report\nNo verdict here"))
	if err == nil || !strings.Contains(err.Error(), "EVIDENCE_VERDICT_REQUIRED") {
		t.Fatalf("expected EVIDENCE_VERDICT_REQUIRED for missing verdict, got: %v", err)
	}

	// 8. Unknown verdict
	_, err = ParseEvidenceInput("test.md", []byte("# Report\nVerdict: MAYBE"))
	if err == nil || !strings.Contains(err.Error(), "EVIDENCE_VERDICT_REQUIRED") {
		t.Fatalf("expected EVIDENCE_VERDICT_REQUIRED for unknown verdict, got: %v", err)
	}

	// 9. Conflicting verdicts
	conflicting := `# Report
Verdict: PASS
## Verdict
FAIL
`
	_, err = ParseEvidenceInput("test.md", []byte(conflicting))
	if err == nil || !strings.Contains(err.Error(), "EVIDENCE_CONFLICTING_VERDICT") {
		t.Fatalf("expected EVIDENCE_CONFLICTING_VERDICT, got: %v", err)
	}
}

func TestParseEvidenceInputIncompleteCases(t *testing.T) {
	// 1. Negative verdicts parsed into Verdict
	negativeVerdicts := []string{
		"FAIL",
		"BLOCKED",
		"REOPENED",
		"REJECTED",
		"NOT CERTIFIED",
		"IMPLEMENTED - ACCEPTANCE INCOMPLETE",
		"IMPLEMENTED — ACCEPTANCE INCOMPLETE",
	}
	for _, nv := range negativeVerdicts {
		text := "# Review\nVerdict: " + nv + "\n"
		res, err := ParseEvidenceInput("review.md", []byte(text))
		if err != nil {
			t.Fatalf("unexpected error parsing negative verdict %q: %v", nv, err)
		}
		expected := normalizeVerdict(nv)
		if res.Verdict != expected {
			t.Fatalf("expected verdict %q, got %q", expected, res.Verdict)
		}
	}

	// 2. Acceptance criteria table parsing
	tableMd := `# Review
Verdict: ACCEPTED

## Current acceptance
| case_id | status |
|---------|--------|
| C13-1   | PASS   |
| C13-2   | NOT RUN |
| C13-3   | BLOCKED |
| C13-4   | FAIL   |
| C13-5   | STALE  |

## Historical runs
| case_id | status |
|---------|--------|
| H-01    | FAIL   |
| H-02    | NOT RUN |
`
	res, err := ParseEvidenceInput("review.markdown", []byte(tableMd))
	if err != nil {
		t.Fatalf("unexpected error parsing acceptance table: %v", err)
	}
	if len(res.BlockingCases) != 4 {
		t.Fatalf("expected 4 blocking cases (C13-2, C13-3, C13-4, C13-5), got %d: %v", len(res.BlockingCases), res.BlockingCases)
	}
	expectedBlocking := map[string]bool{
		"C13-2": true,
		"C13-3": true,
		"C13-4": true,
		"C13-5": true,
	}
	for _, b := range res.BlockingCases {
		if !expectedBlocking[b] {
			t.Fatalf("unexpected blocking case: %s", b)
		}
	}

	// 3. Checkbox parsing under ## Acceptance criteria
	checkboxMd := `# Review
**Verdict:** PASS

## Acceptance criteria
- [x] [AC-001](tasks/T-001.md): First criteria passing
- [ ] [AC-002](tasks/T-002.md): Second criteria incomplete
- [ ] AC-003: Third criteria incomplete

## Background
- [ ] BG-001: should be ignored
`
	res, err = ParseEvidenceInput("review.md", []byte(checkboxMd))
	if err != nil {
		t.Fatalf("unexpected error parsing checkboxes: %v", err)
	}
	if len(res.BlockingCases) != 2 {
		t.Fatalf("expected 2 blocking cases (AC-002, AC-003), got %d: %v", len(res.BlockingCases), res.BlockingCases)
	}
	if res.BlockingCases[0] != "AC-002" || res.BlockingCases[1] != "AC-003" {
		t.Fatalf("expected [AC-002, AC-003], got %v", res.BlockingCases)
	}

	// 4. Unknown case status in table
	unknownStatusMd := `# Review
Verdict: PASS

## Acceptance
| ID | Status |
|----|--------|
| T-1 | MAYBE |
`
	_, err = ParseEvidenceInput("review.md", []byte(unknownStatusMd))
	if err == nil || !strings.Contains(err.Error(), "EVIDENCE_CASE_STATUS_UNKNOWN") {
		t.Fatalf("expected EVIDENCE_CASE_STATUS_UNKNOWN for unknown status, got: %v", err)
	}

	// 5. Empty case status in table
	emptyStatusMd := `# Review
Verdict: PASS

## Acceptance
| ID | Status |
|----|--------|
| T-1 |        |
`
	_, err = ParseEvidenceInput("review.md", []byte(emptyStatusMd))
	if err == nil || !strings.Contains(err.Error(), "EVIDENCE_CASE_STATUS_UNKNOWN") {
		t.Fatalf("expected EVIDENCE_CASE_STATUS_UNKNOWN for empty status, got: %v", err)
	}
}
