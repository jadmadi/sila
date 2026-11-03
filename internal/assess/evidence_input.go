package assess

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// MaxEvidenceInputSize is the maximum allowed size (1 MiB) for manual evidence.
const MaxEvidenceInputSize = 1024 * 1024

// EvidenceInput represents parsed manual evidence separated into document and runtime streams.
type EvidenceInput struct {
	Format        string   `json:"format"` // "markdown" or "log"
	Document      string   `json:"document,omitempty"`
	Stdout        string   `json:"stdout,omitempty"`
	Stderr        string   `json:"stderr,omitempty"`
	Verdict       string   `json:"verdict,omitempty"`
	BlockingCases []string `json:"blocking_cases,omitempty"`
}

var (
	headingRegex        = regexp.MustCompile(`^(#{1,6})\s+(.*)$`)
	acceptanceHeadRegex = regexp.MustCompile(`(?i)^(current\s+acceptance|acceptance\s+criteria|acceptance)$`)
	verdictHeadRegex    = regexp.MustCompile(`(?i)^verdict$`)
	checkboxRegex       = regexp.MustCompile(`^\s*-\s*\[([ xX])\]\s*(.*)$`)
	linkRegex           = regexp.MustCompile(`\[([^\]]+)\]\([^\)]*\)`)
	tableIDHeaderRegex  = regexp.MustCompile(`(?i)^(case_?id|case\s+id|id|case|test_?id|test\s+id)$`)
	tableStatHeadRegex  = regexp.MustCompile(`(?i)^(status|verdict|result|state)$`)
)

var allowedPositiveVerdicts = map[string]bool{
	"PASS":                   true,
	"DONE":                   true,
	"ACCEPTED":               true,
	"ACCEPTED WITH FINDINGS": true,
}

var allowedNegativeVerdicts = map[string]bool{
	"FAIL":                                true,
	"BLOCKED":                             true,
	"REOPENED":                            true,
	"REJECTED":                            true,
	"NOT CERTIFIED":                       true,
	"IMPLEMENTED - ACCEPTANCE INCOMPLETE": true,
}

var allowedPassingStatuses = map[string]bool{
	"PASS":     true,
	"PASSED":   true,
	"DONE":     true,
	"OK":       true,
	"ACCEPTED": true,
}

var allowedBlockingStatuses = map[string]bool{
	"FAIL":     true,
	"FAILED":   true,
	"BLOCKED":  true,
	"NOT RUN":  true,
	"NOT_RUN":  true,
	"STALE":    true,
	"REJECTED": true,
}

// ParseEvidenceInput parses manual evidence from data and path.
func ParseEvidenceInput(path string, data []byte) (EvidenceInput, error) {
	if len(data) > MaxEvidenceInputSize {
		return EvidenceInput{}, fmt.Errorf("EVIDENCE_TOO_LARGE: evidence input exceeds 1MiB limit (%d bytes)", len(data))
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return EvidenceInput{}, errors.New("EVIDENCE_EMPTY: evidence input is empty or whitespace")
	}

	ext := strings.ToLower(filepath.Ext(path))
	if ext != ".md" && ext != ".markdown" {
		return EvidenceInput{
			Format: "log",
			Stdout: string(data),
		}, nil
	}

	docText := string(data)
	lines := strings.Split(docText, "\n")

	var stdoutLines []string
	var stderrLines []string
	var foundVerdicts []string
	var blockingCases []string

	inFence := false
	fenceChar := byte(0)
	fenceLen := 0
	fenceIsLog := false
	fenceStream := ""
	var currentFenceLines []string

	inAcceptance := false
	inVerdictSection := false
	tableIDCol := -1
	tableStatusCol := -1
	tableExpectSep := false

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimLeft(line, " \t")

		if inFence {
			// Check if line closes the fence
			if len(trimmed) >= fenceLen && trimmed[0] == fenceChar {
				c := 0
				for c < len(trimmed) && trimmed[c] == fenceChar {
					c++
				}
				rest := strings.TrimSpace(trimmed[c:])
				if c >= fenceLen && rest == "" {
					// Closing fence matched
					if fenceIsLog {
						if fenceStream == "stdout" {
							stdoutLines = append(stdoutLines, currentFenceLines...)
						} else if fenceStream == "stderr" {
							stderrLines = append(stderrLines, currentFenceLines...)
						}
					}
					inFence = false
					fenceIsLog = false
					fenceStream = ""
					currentFenceLines = nil
					continue
				}
			}
			if fenceIsLog {
				currentFenceLines = append(currentFenceLines, line)
			}
			continue
		}

		// Check for opening fence
		if len(trimmed) >= 3 && (trimmed[0] == '`' || trimmed[0] == '~') {
			ch := trimmed[0]
			c := 0
			for c < len(trimmed) && trimmed[c] == ch {
				c++
			}
			if c >= 3 {
				info := strings.TrimSpace(trimmed[c:])
				if !(ch == '`' && strings.Contains(info, "`")) {
					inFence = true
					fenceChar = ch
					fenceLen = c
					currentFenceLines = nil
					lowerInfo := strings.ToLower(info)
					if lowerInfo == "stdout" || lowerInfo == "stderr" {
						fenceIsLog = true
						fenceStream = lowerInfo
					} else {
						fenceIsLog = false
						fenceStream = ""
					}
					continue
				}
			}
		}

		// Handle headings
		if hMatch := headingRegex.FindStringSubmatch(strings.TrimSpace(line)); hMatch != nil {
			tableIDCol = -1
			tableStatusCol = -1
			tableExpectSep = false

			headingText := cleanMarkup(hMatch[2])
			if acceptanceHeadRegex.MatchString(headingText) {
				inAcceptance = true
				inVerdictSection = false
			} else if verdictHeadRegex.MatchString(headingText) {
				inVerdictSection = true
				inAcceptance = false
			} else {
				inAcceptance = false
				inVerdictSection = false
			}
			continue
		}

		// If in ## Verdict section, first non-empty line provides verdict
		if inVerdictSection {
			cleaned := cleanMarkup(line)
			if cleaned != "" {
				if after, ok := cutPrefixCaseInsensitive(cleaned, "verdict:"); ok {
					cleaned = after
				}
				v := normalizeVerdict(cleaned)
				if v != "" {
					foundVerdicts = append(foundVerdicts, v)
				}
				inVerdictSection = false
			}
			continue
		}

		// Check standalone "Verdict: ..." line anywhere outside fences
		cleanedLine := cleanMarkup(line)
		if after, ok := cutPrefixCaseInsensitive(cleanedLine, "verdict:"); ok {
			v := normalizeVerdict(after)
			if v != "" {
				foundVerdicts = append(foundVerdicts, v)
			}
		}

		// Handle acceptance section content (tables and checkboxes)
		if inAcceptance {
			// Checkbox item
			if cb := checkboxRegex.FindStringSubmatch(line); cb != nil {
				checked := strings.ToLower(cb[1]) == "x"
				desc := strings.TrimSpace(cb[2])
				if !checked {
					caseID := extractCaseID(desc)
					if caseID != "" {
						blockingCases = append(blockingCases, caseID)
					}
				}
				continue
			}

			// Pipe table
			if strings.Contains(line, "|") {
				cells := parseTableRow(line)
				if tableIDCol == -1 || tableStatusCol == -1 {
					// Look for header row
					idIdx, statIdx := findTableColumns(cells)
					if idIdx != -1 && statIdx != -1 {
						tableIDCol = idIdx
						tableStatusCol = statIdx
						tableExpectSep = true
					}
					continue
				}

				if tableExpectSep {
					if isSeparatorRow(cells) {
						tableExpectSep = false
						continue
					}
					tableExpectSep = false
				}

				if tableIDCol < len(cells) && tableStatusCol < len(cells) {
					rawID := strings.TrimSpace(cells[tableIDCol])
					rawStatus := strings.TrimSpace(cells[tableStatusCol])

					caseID := extractCaseID(rawID)
					if caseID == "" {
						continue
					}

					cleanStatus := strings.ToUpper(cleanMarkup(rawStatus))
					cleanStatus = strings.Join(strings.Fields(cleanStatus), " ")

					if cleanStatus == "" {
						return EvidenceInput{}, fmt.Errorf("EVIDENCE_CASE_STATUS_UNKNOWN: empty status for case %q", caseID)
					}

					if allowedPassingStatuses[cleanStatus] {
						// Pass row passes parsing only
						continue
					}
					if allowedBlockingStatuses[cleanStatus] {
						blockingCases = append(blockingCases, caseID)
						continue
					}

					return EvidenceInput{}, fmt.Errorf("EVIDENCE_CASE_STATUS_UNKNOWN: unknown status %q for case %q", rawStatus, caseID)
				}
				continue
			} else {
				// Non-table line resets table parser within acceptance section
				tableIDCol = -1
				tableStatusCol = -1
				tableExpectSep = false
			}
		}
	}

	if inFence && fenceIsLog {
		return EvidenceInput{}, fmt.Errorf("EVIDENCE_MALFORMED: unterminated %s code fence", fenceStream)
	}

	// Validate verdict
	uniqueVerdicts := make(map[string]bool)
	for _, v := range foundVerdicts {
		uniqueVerdicts[v] = true
	}
	if len(uniqueVerdicts) == 0 {
		return EvidenceInput{}, errors.New("EVIDENCE_VERDICT_REQUIRED: missing verdict declaration in markdown evidence")
	}
	if len(uniqueVerdicts) > 1 {
		var list []string
		for v := range uniqueVerdicts {
			list = append(list, v)
		}
		return EvidenceInput{}, fmt.Errorf("EVIDENCE_CONFLICTING_VERDICT: conflicting verdict declarations found: %s", strings.Join(list, ", "))
	}

	var finalVerdict string
	for v := range uniqueVerdicts {
		finalVerdict = v
	}

	if !allowedPositiveVerdicts[finalVerdict] && !allowedNegativeVerdicts[finalVerdict] {
		return EvidenceInput{}, fmt.Errorf("EVIDENCE_VERDICT_REQUIRED: unrecognized verdict %q", finalVerdict)
	}

	var uniqueBlocking []string
	seenBlocking := make(map[string]bool)
	for _, c := range blockingCases {
		if !seenBlocking[c] {
			seenBlocking[c] = true
			uniqueBlocking = append(uniqueBlocking, c)
		}
	}

	stdout := ""
	if len(stdoutLines) > 0 {
		stdout = strings.Join(stdoutLines, "\n") + "\n"
	}
	stderr := ""
	if len(stderrLines) > 0 {
		stderr = strings.Join(stderrLines, "\n") + "\n"
	}

	return EvidenceInput{
		Format:        "markdown",
		Document:      docText,
		Stdout:        stdout,
		Stderr:        stderr,
		Verdict:       finalVerdict,
		BlockingCases: uniqueBlocking,
	}, nil
}

func cleanMarkup(s string) string {
	s = linkRegex.ReplaceAllString(s, "$1")
	s = strings.ReplaceAll(s, "*", "")
	s = strings.ReplaceAll(s, "_", "")
	s = strings.ReplaceAll(s, "`", "")
	return strings.TrimSpace(s)
}

func cutPrefixCaseInsensitive(s, prefix string) (string, bool) {
	if len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix) {
		return strings.TrimSpace(s[len(prefix):]), true
	}
	return s, false
}

func normalizeVerdict(s string) string {
	s = cleanMarkup(s)
	s = strings.ReplaceAll(s, "—", "-")
	s = strings.ReplaceAll(s, "–", "-")
	s = strings.Join(strings.Fields(s), " ")
	s = strings.ToUpper(s)
	return strings.TrimSpace(s)
}

func extractCaseID(s string) string {
	s = cleanMarkup(s)
	if idx := strings.Index(s, ":"); idx != -1 {
		s = strings.TrimSpace(s[:idx])
	}
	fields := strings.Fields(s)
	if len(fields) > 0 {
		return fields[0]
	}
	return ""
}

func parseTableRow(line string) []string {
	parts := strings.Split(line, "|")
	if len(parts) > 0 && strings.TrimSpace(parts[0]) == "" {
		parts = parts[1:]
	}
	if len(parts) > 0 && strings.TrimSpace(parts[len(parts)-1]) == "" {
		parts = parts[:len(parts)-1]
	}
	var res []string
	for _, p := range parts {
		res = append(res, strings.TrimSpace(p))
	}
	return res
}

func findTableColumns(cells []string) (idCol, statusCol int) {
	idCol = -1
	statusCol = -1
	for idx, cell := range cells {
		clean := cleanMarkup(cell)
		if tableIDHeaderRegex.MatchString(clean) && idCol == -1 {
			idCol = idx
		}
		if tableStatHeadRegex.MatchString(clean) && statusCol == -1 {
			statusCol = idx
		}
	}
	return idCol, statusCol
}

func isSeparatorRow(cells []string) bool {
	if len(cells) == 0 {
		return false
	}
	for _, cell := range cells {
		trimmed := strings.TrimSpace(cell)
		if trimmed == "" {
			continue
		}
		for i := 0; i < len(trimmed); i++ {
			ch := trimmed[i]
			if ch != '-' && ch != ':' && ch != ' ' {
				return false
			}
		}
	}
	return true
}

func extractMarkdownDisclosures(doc string) []string {
	var disclosures []string
	lines := strings.Split(doc, "\n")
	inSection := false

	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if hMatch := headingRegex.FindStringSubmatch(trimmed); hMatch != nil {
			headingText := strings.ToLower(cleanMarkup(hMatch[2]))
			if headingText == "limitations" || headingText == "disclosures" {
				inSection = true
			} else {
				inSection = false
			}
			continue
		}
		if inSection {
			if strings.HasPrefix(trimmed, "-") || strings.HasPrefix(trimmed, "*") {
				item := strings.TrimSpace(strings.TrimLeft(trimmed, "-* \t"))
				if item != "" {
					disclosures = append(disclosures, item)
				}
			}
		}
	}
	return disclosures
}
