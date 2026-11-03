package assess

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/jadmadi/sila/internal/render"
)

// ScarTissueViolation represents a single occurrence of an outlawed anti-pattern or rule breach.
type ScarTissueViolation struct {
	RuleID      string `json:"rule_id"`
	RuleName    string `json:"rule_name"`
	File        string `json:"file"`
	LineNumber  int    `json:"line_number,omitempty"`
	LineContent string `json:"line_content,omitempty"`
	Source      string `json:"source"`
	Citation    string `json:"citation"`
	Severity    string `json:"severity"` // "error" | "warning"
}

// ScarTissueReport contains the outcome of scanning git changes against architectural invariants.
type ScarTissueReport struct {
	Clean        bool                  `json:"clean"`
	TotalRules   int                   `json:"total_rules"`
	FilesScanned int                   `json:"files_scanned"`
	LinesScanned int                   `json:"lines_scanned"`
	Violations   []ScarTissueViolation `json:"violations"`
}

// AuditDiffString analyzes a unified diff string and untracked file contents against compiled scar tissue rules.
// It rigorously evaluates only newly added lines and paths, ignoring removed lines.
func AuditDiffString(diffContent string, untrackedFiles map[string]string, rules []ScarTissueRule) *ScarTissueReport {
	report := &ScarTissueReport{
		Clean:      true,
		TotalRules: len(rules),
		Violations: []ScarTissueViolation{},
	}

	filesSeen := make(map[string]bool)
	violationKeys := make(map[string]bool)

	addViolation := func(v ScarTissueViolation) {
		key := fmt.Sprintf("%s:%s:%d:%s", v.RuleID, v.File, v.LineNumber, v.LineContent)
		if !violationKeys[key] {
			violationKeys[key] = true
			report.Violations = append(report.Violations, v)
			if v.Severity == "error" || v.Severity == "" {
				report.Clean = false
			}
		}
	}

	// 1. Parse unified diff line-by-line
	scanner := bufio.NewScanner(strings.NewReader(diffContent))
	var currentFile string
	var currentLineNumber int
	var isDeleted bool

	hunkRegex := regexp.MustCompile(`^@@\s+-[0-9]+(?:,[0-9]+)?\s+\+([0-9]+)(?:,[0-9]+)?\s+@@`)

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
			if currentFile != "" && !filesSeen[currentFile] {
				filesSeen[currentFile] = true
				// Check path rules
				for _, rule := range rules {
					if rule.MatchPath && rule.PathRegex != nil && rule.PathRegex.MatchString(currentFile) {
						addViolation(ScarTissueViolation{
							RuleID:      rule.ID,
							RuleName:    rule.Name,
							File:        currentFile,
							LineNumber:  0,
							LineContent: "(prohibited file path added)",
							Source:      rule.Source,
							Citation:    rule.Citation,
							Severity:    rule.Severity,
						})
					}
				}
			}
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
			// Newly added line!
			content := line[1:]
			report.LinesScanned++

			for _, rule := range rules {
				if rule.MatchPath {
					continue
				}
				if !matchFilePattern(rule.FilePattern, currentFile) {
					continue
				}
				if rule.Regex != nil && rule.Regex.MatchString(content) {
					addViolation(ScarTissueViolation{
						RuleID:      rule.ID,
						RuleName:    rule.Name,
						File:        currentFile,
						LineNumber:  currentLineNumber,
						LineContent: strings.TrimSpace(content),
						Source:      rule.Source,
						Citation:    rule.Citation,
						Severity:    rule.Severity,
					})
				}
			}
			currentLineNumber++
		} else if strings.HasPrefix(line, " ") {
			// Context line
			currentLineNumber++
		} else if strings.HasPrefix(line, "-") {
			// Removed line: intentionally ignored per high-precision scar tissue invariant
			continue
		}
	}

	// 2. Audit untracked files
	for path, content := range untrackedFiles {
		path = filepath.ToSlash(path)
		path = strings.TrimPrefix(path, "./")
		if !filesSeen[path] {
			filesSeen[path] = true
		}

		// Check path rules for untracked file
		for _, rule := range rules {
			if rule.MatchPath && rule.PathRegex != nil && rule.PathRegex.MatchString(path) {
				addViolation(ScarTissueViolation{
					RuleID:      rule.ID,
					RuleName:    rule.Name,
					File:        path,
					LineNumber:  0,
					LineContent: "(untracked file in prohibited path)",
					Source:      rule.Source,
					Citation:    rule.Citation,
					Severity:    rule.Severity,
				})
			}
		}

		// Check content line-by-line
		fileScanner := bufio.NewScanner(strings.NewReader(content))
		lineNum := 1
		for fileScanner.Scan() {
			report.LinesScanned++
			lText := fileScanner.Text()
			for _, rule := range rules {
				if rule.MatchPath {
					continue
				}
				if !matchFilePattern(rule.FilePattern, path) {
					continue
				}
				if rule.Regex != nil && rule.Regex.MatchString(lText) {
					addViolation(ScarTissueViolation{
						RuleID:      rule.ID,
						RuleName:    rule.Name,
						File:        path,
						LineNumber:  lineNum,
						LineContent: strings.TrimSpace(lText),
						Source:      rule.Source,
						Citation:    rule.Citation,
						Severity:    rule.Severity,
					})
				}
			}
			lineNum++
		}
	}

	report.FilesScanned = len(filesSeen)
	return report
}

// AuditRepoDiff executes git diff inspection against working tree changes in repoDir.
func AuditRepoDiff(ctx context.Context, repoDir string, rules []ScarTissueRule) (*ScarTissueReport, error) {
	if repoDir == "" {
		var err error
		repoDir, err = os.Getwd()
		if err != nil {
			return nil, err
		}
	}

	// 1. Run git diff against HEAD (falling back to bare diff if HEAD unborn)
	diffCmd := exec.CommandContext(ctx, "git", "-C", repoDir, "diff", "HEAD")
	diffOut, err := diffCmd.Output()
	if err != nil {
		// Fallback for unborn HEAD or detached initial state
		fallbackCmd := exec.CommandContext(ctx, "git", "-C", repoDir, "diff")
		diffOut, _ = fallbackCmd.Output()

		cachedCmd := exec.CommandContext(ctx, "git", "-C", repoDir, "diff", "--cached")
		cachedOut, _ := cachedCmd.Output()
		diffOut = append(diffOut, cachedOut...)
	}

	// 2. Discover untracked files
	untrackedMap := make(map[string]string)
	statusCmd := exec.CommandContext(ctx, "git", "-C", repoDir, "status", "--porcelain")
	statusOut, _ := statusCmd.Output()

	statusScanner := bufio.NewScanner(strings.NewReader(string(statusOut)))
	for statusScanner.Scan() {
		line := strings.TrimSpace(statusScanner.Text())
		if strings.HasPrefix(line, "?? ") {
			relPath := strings.TrimPrefix(line, "?? ")
			relPath = strings.Trim(relPath, "\"")
			fullPath := filepath.Join(repoDir, relPath)

			fi, statErr := os.Stat(fullPath)
			if statErr != nil || fi.IsDir() {
				// If directory, check path rules directly
				for _, rule := range rules {
					slashPath := filepath.ToSlash(relPath) + "/"
					if rule.MatchPath && rule.PathRegex != nil && rule.PathRegex.MatchString(slashPath) {
						untrackedMap[slashPath] = ""
					}
				}
				continue
			}

			// Skip giant files (> 1MB) or binary assets
			if fi.Size() > 1024*1024 {
				untrackedMap[relPath] = ""
				continue
			}

			data, readErr := os.ReadFile(fullPath)
			if readErr == nil {
				untrackedMap[relPath] = string(data)
			}
		}
	}

	report := AuditDiffString(string(diffOut), untrackedMap, rules)
	return report, nil
}

// RenderScarTissueTOON produces compact Token-Oriented Object Notation (TOON) for scar tissue reports.
func RenderScarTissueTOON(report *ScarTissueReport) string {
	var b strings.Builder

	summaryFields := [][2]string{
		{"clean", fmt.Sprintf("%t", report.Clean)},
		{"total_rules", fmt.Sprintf("%d", report.TotalRules)},
		{"files_scanned", fmt.Sprintf("%d", report.FilesScanned)},
		{"lines_scanned", fmt.Sprintf("%d", report.LinesScanned)},
		{"violations_count", fmt.Sprintf("%d", len(report.Violations))},
	}
	b.WriteString(render.FormatTOONObject("scar_tissue_audit", summaryFields))

	if len(report.Violations) > 0 {
		headers := []string{"rule_id", "file", "line", "content", "citation"}
		var rows [][]string
		for _, v := range report.Violations {
			lineStr := fmt.Sprintf("%d", v.LineNumber)
			if v.LineNumber == 0 {
				lineStr = "-"
			}
			rows = append(rows, []string{
				v.RuleID,
				v.File,
				lineStr,
				v.LineContent,
				v.Citation,
			})
		}
		b.WriteString(render.FormatTOONTable("violations", headers, rows))
	}

	return b.String()
}

func matchFilePattern(pattern, filePath string) bool {
	if pattern == "" {
		return true
	}
	base := filepath.Base(filePath)
	cleanPath := filepath.ToSlash(filePath)
	cleanPath = strings.TrimPrefix(cleanPath, "./")

	patterns := strings.FieldsFunc(pattern, func(r rune) bool {
		return r == '|' || r == ','
	})
	if len(patterns) == 0 {
		return true
	}

	hasPositive := false
	positiveMatch := false

	for _, p := range patterns {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if strings.HasPrefix(p, "!") {
			neg := strings.TrimPrefix(p, "!")
			if matchSinglePattern(neg, cleanPath, base) {
				return false // excluded
			}
		} else {
			hasPositive = true
			if matchSinglePattern(p, cleanPath, base) {
				positiveMatch = true
			}
		}
	}

	if !hasPositive {
		return true // only exclusions were specified and none matched
	}
	return positiveMatch
}

func matchSinglePattern(pat, cleanPath, base string) bool {
	if pat == "*" || pat == "" {
		return true
	}
	if strings.HasPrefix(pat, "*.") {
		ext := strings.TrimPrefix(pat, "*")
		return strings.HasSuffix(strings.ToLower(cleanPath), strings.ToLower(ext))
	}
	if matched, _ := filepath.Match(pat, base); matched {
		return true
	}
	if matched, _ := filepath.Match(pat, cleanPath); matched {
		return true
	}
	return strings.Contains(cleanPath, pat)
}
