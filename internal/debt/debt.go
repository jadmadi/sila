package debt

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// DebtStatus represents the health classification of a tracked technical debt item.
type DebtStatus string

const (
	StatusHealthy        DebtStatus = "healthy"
	StatusMissingTrigger DebtStatus = "missing_trigger"
	StatusMissingCeiling DebtStatus = "missing_ceiling"
	StatusRotRisk        DebtStatus = "rot_risk"
)

// DebtItem records a single tracked debt marker extracted from repository code.
type DebtItem struct {
	File       string     `json:"file"`
	Line       int        `json:"line"`
	Marker     string     `json:"marker"` // "shortcut" or "ponytail"
	Raw        string     `json:"raw"`
	Ceiling    string     `json:"ceiling"`
	Trigger    string     `json:"trigger"`
	LinkedGoal string     `json:"linked_goal,omitempty"`
	Status     DebtStatus `json:"status"`
	Details    string     `json:"details,omitempty"`
}

// DebtReport summarizes technical debt comment health across a repository.
type DebtReport struct {
	TotalItems   int        `json:"total_items"`
	HealthyCount int        `json:"healthy_count"`
	RotRiskCount int        `json:"rot_risk_count"`
	Items        []DebtItem `json:"items"`
	Summary      string     `json:"summary"`
}

// DebtScanOptions configures the debt comment scanner.
type DebtScanOptions struct {
	MaxFiles     int
	IncludeTests bool
}

var (
	// Regex matching line-level comment prefixes followed by shortcut or ponytail markers
	debtMarkerRE = regexp.MustCompile(`(?i)^\s*(?://|/\*|\*+|#|--|<!--|;)\s*(shortcut|ponytail)\s*:\s*(.*)`)
	// Regex matching goal linkage tags like [goal: slug], (goal: slug), or Fixes-Goal: slug
	goalTagRE = regexp.MustCompile(`(?i)(?:\[|\()?(?:goal|fixes-goal|fixes):\s*([a-z0-9_\-]+)(?:\]|\))?`)
	// Trailing comment delimiters to strip
	trailingCommentRE = regexp.MustCompile(`(?:\s*\*/|\s*-->|\s*#|\s*//)\s*$`)
)

// ParseDebtComment parses a single line of text and returns a DebtItem if a marker is present.
func ParseDebtComment(file string, lineNum int, lineText string) (*DebtItem, bool) {
	m := debtMarkerRE.FindStringSubmatch(lineText)
	if len(m) < 3 {
		return nil, false
	}

	marker := strings.ToLower(m[1])
	content := m[2]
	content = trailingCommentRE.ReplaceAllString(content, "")
	content = strings.TrimSpace(content)

	item := &DebtItem{
		File:   filepath.ToSlash(file),
		Line:   lineNum,
		Marker: marker,
		Raw:    strings.TrimSpace(lineText),
	}

	// 1. Extract goal link if present
	if gm := goalTagRE.FindStringSubmatch(content); len(gm) > 1 {
		item.LinkedGoal = gm[1]
		content = goalTagRE.ReplaceAllString(content, "")
		content = strings.Trim(strings.TrimSpace(content), ",;|")
	}

	// 2. Parse ceiling and trigger
	ceiling, trigger := parseCeilingAndTrigger(content)
	item.Ceiling = ceiling
	item.Trigger = trigger

	// 3. Evaluate health status
	switch {
	case ceiling != "" && trigger != "":
		item.Status = StatusHealthy
	case ceiling == "" && trigger == "":
		item.Status = StatusRotRisk
		item.Details = "missing operating ceiling and upgrade trigger"
	case trigger == "":
		item.Status = StatusRotRisk
		item.Details = "missing upgrade trigger; unbudgeted rot risk"
	case ceiling == "":
		item.Status = StatusMissingCeiling
		item.Details = "missing operating ceiling constraint"
	}

	return item, true
}

// parseCeilingAndTrigger separates ceiling constraint and upgrade trigger from debt body.
func parseCeilingAndTrigger(content string) (ceiling, trigger string) {
	content = strings.TrimSpace(content)
	if content == "" {
		return "", ""
	}

	// Check explicit labels: ceiling=... / trigger=... or ceiling: ... / trigger: ...
	low := strings.ToLower(content)
	if strings.Contains(low, "ceiling") && strings.Contains(low, "trigger") {
		reExplicit := regexp.MustCompile(`(?i)(?:ceiling[:=]\s*)([^,;]+)(?:[,;]\s*trigger[:=]\s*)(.+)`)
		if em := reExplicit.FindStringSubmatch(content); len(em) >= 3 {
			return strings.TrimSpace(em[1]), strings.TrimSpace(em[2])
		}
	}

	// Check standard delimiter: comma, pipe, or semicolon
	var parts []string
	if strings.Contains(content, "|") {
		parts = strings.SplitN(content, "|", 2)
	} else if strings.Contains(content, ";") {
		parts = strings.SplitN(content, ";", 2)
	} else if strings.Contains(content, ",") {
		parts = strings.SplitN(content, ",", 2)
	}

	if len(parts) == 2 {
		c := strings.TrimSpace(parts[0])
		t := strings.TrimSpace(parts[1])
		return c, t
	}

	// Single fragment: determine if it describes a trigger or ceiling
	if strings.HasPrefix(low, "when ") || strings.HasPrefix(low, "upgrade when ") || strings.HasPrefix(low, "trigger:") {
		return "", strings.TrimSpace(content)
	}

	return strings.TrimSpace(content), ""
}

// ScanRepoDebt walks the given directory and identifies all tracked debt comments.
func ScanRepoDebt(rootDir string, opts DebtScanOptions) (*DebtReport, error) {
	report := &DebtReport{
		Items: make([]DebtItem, 0),
	}

	knownSkipDirs := map[string]bool{
		".git":         true,
		".sila":        true,
		"node_modules": true,
		"vendor":       true,
		"target":       true,
		"dist":         true,
		"bin":          true,
		"build":        true,
		".idea":        true,
		".vscode":      true,
		"rm":           true,
	}

	sourceExtensions := map[string]bool{
		".go":    true,
		".ts":    true,
		".js":    true,
		".tsx":   true,
		".jsx":   true,
		".py":    true,
		".rs":    true,
		".rb":    true,
		".sh":    true,
		".sql":   true,
		".c":     true,
		".h":     true,
		".cpp":   true,
		".md":    true,
		".html":  true,
		".yaml":  true,
		".yml":   true,
		".jsonc": true,
	}

	fileCount := 0
	err := filepath.WalkDir(rootDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}

		name := d.Name()
		if d.IsDir() {
			if strings.HasPrefix(name, ".") && name != "." && name != ".." {
				return filepath.SkipDir
			}
			if knownSkipDirs[name] {
				return filepath.SkipDir
			}
			return nil
		}

		ext := strings.ToLower(filepath.Ext(path))
		if !sourceExtensions[ext] {
			return nil
		}

		if !opts.IncludeTests {
			base := filepath.Base(path)
			if strings.HasSuffix(base, "_test.go") || strings.Contains(base, ".test.") || strings.Contains(base, ".spec.") {
				return nil
			}
		}

		if opts.MaxFiles > 0 && fileCount >= opts.MaxFiles {
			return filepath.SkipAll
		}
		fileCount++

		rel, relErr := filepath.Rel(rootDir, path)
		if relErr != nil {
			rel = path
		}

		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()

		scanner := bufio.NewScanner(f)
		lineNum := 0
		for scanner.Scan() {
			lineNum++
			line := scanner.Text()
			if item, ok := ParseDebtComment(rel, lineNum, line); ok {
				report.Items = append(report.Items, *item)
				report.TotalItems++
				if item.Status == StatusHealthy {
					report.HealthyCount++
				} else {
					report.RotRiskCount++
				}
			}
		}
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("walk directory for debt scanning: %w", err)
	}

	report.Summary = fmt.Sprintf("%d tracked debt comment(s) found: %d healthy, %d rot risk(s)",
		report.TotalItems, report.HealthyCount, report.RotRiskCount)

	return report, nil
}
