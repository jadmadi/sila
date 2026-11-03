package assess

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/jadmadi/sila/internal/store"
)

// ScarTissueRule represents an architectural invariant, policy rule, or anti-pattern constraint.
type ScarTissueRule struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Category    string         `json:"category"`               // e.g. "ci", "toolchain", "cgo", "css", "typography", "telemetry", "security", "memo"
	Source      string         `json:"source"`                 // e.g. "AGENTS.md §0", "memo:f_123"
	Citation    string         `json:"citation"`               // Exact rule directive or memo quotation
	Severity    string         `json:"severity"`               // "error" (fails gate) | "warning"
	FilePattern string         `json:"file_pattern,omitempty"` // glob pattern like "*.go", "*.css", "!*.md"
	Regex       *regexp.Regexp `json:"-"`
	PathRegex   *regexp.Regexp `json:"-"`
	MatchPath   bool           `json:"match_path,omitempty"`
}

// DefaultHardProhibitions returns the baseline architectural anti-pattern rules codified in AGENTS.md §0.
func DefaultHardProhibitions() []ScarTissueRule {
	return []ScarTissueRule{
		{
			ID:        "no-github-workflows",
			Name:      "Zero GitHub Workflows / CI Sprawl",
			Category:  "ci",
			Source:    "AGENTS.md §0",
			Citation:  "Prefer local verification before push; CI workflows are allowed for the public repository.",
			Severity:  "warning",
			MatchPath: true,
			PathRegex: regexp.MustCompile(`(?i)^\.github/workflows/`),
		},
		{
			ID:        "no-npm-yarn-lockfile",
			Name:      "No Unapproved Package Managers (Lockfiles)",
			Category:  "toolchain",
			Source:    "AGENTS.md §0",
			Citation:  "No unapproved package managers: NEVER use npm or yarn. Use aube toolchain when present, or pnpm.",
			Severity:  "error",
			MatchPath: true,
			PathRegex: regexp.MustCompile(`(?i)(^|/)(package-lock\.json|yarn\.lock)$`),
		},
		{
			ID:          "no-npm-yarn-commands",
			Name:        "No Unapproved Package Managers (Commands)",
			Category:    "toolchain",
			Source:      "AGENTS.md §0",
			Citation:    "No unapproved package managers: NEVER use npm or yarn. Use aube toolchain when present, or pnpm.",
			Severity:    "error",
			FilePattern: "!*.md|!*.txt|!*_test.go|!internal/assess/rules.go",
			Regex:       regexp.MustCompile(`(?i)\b(?:npm\s+(?:install|i|ci|run|exec)|yarn\s+(?:add|install|run))\b`),
		},
		{
			ID:          "no-cgo",
			Name:        "No CGO Dependencies",
			Category:    "cgo",
			Source:      "AGENTS.md §0",
			Citation:    "No CGO dependencies: Pure-Go SQLite (modernc.org/sqlite) with CGO_ENABLED=0 only.",
			Severity:    "error",
			FilePattern: "*.go|!*_test.go|!internal/assess/rules.go",
			Regex:       regexp.MustCompile(`(?m)^\s*(?:import\s+)?"C"(?:\s|/|$)`),
		},
		{
			ID:          "no-cgo-enabled",
			Name:        "CGO_ENABLED Prohibition",
			Category:    "cgo",
			Source:      "AGENTS.md §0",
			Citation:    "No CGO dependencies: Pure-Go SQLite (modernc.org/sqlite) with CGO_ENABLED=0 only.",
			Severity:    "error",
			FilePattern: "!*.md|!*.txt|!*_test.go|!internal/assess/rules.go",
			Regex:       regexp.MustCompile(`(?i)\bCGO_ENABLED=1\b`),
		},
		{
			ID:          "no-physical-css",
			Name:        "No Physical Directional CSS",
			Category:    "css",
			Source:      "AGENTS.md §0",
			Citation:    "No physical directional CSS: NEVER use ml-*, mr-*, pl-*, pr-*, left-*, right-* without LTR container isolation. Use logical properties (ms-*, ps-*).",
			Severity:    "error",
			FilePattern: "*.css|*.html|*.gohtml|*.tmpl|*.jsx|*.tsx|*.vue|*.svelte|!*_test.go|!internal/assess/rules.go",
			Regex:       regexp.MustCompile(`\b(?:ml-\d+|mr-\d+|pl-\d+|pr-\d+)\b|\b(?:margin-left|margin-right|padding-left|padding-right)\s*:`),
		},
		{
			ID:          "no-eastern-arabic-numerals",
			Name:        "No Eastern Arabic-Indic Numerals in Code/UI",
			Category:    "typography",
			Source:      "AGENTS.md §0",
			Citation:    "No Eastern Arabic-Indic numerals: NEVER use ٠-٩ in UI. Always use 0-9.",
			Severity:    "error",
			FilePattern: "*.go|*.js|*.ts|*.jsx|*.tsx|*.html|*.gohtml|*.vue|!*_test.go|!internal/assess/rules.go",
			Regex:       regexp.MustCompile(`[٠-٩]`),
		},
		{
			ID:          "no-silent-telemetry",
			Name:        "Zero Silent Telemetry or Ambient Beacons",
			Category:    "telemetry",
			Source:      "AGENTS.md §0",
			Citation:    "Zero silent telemetry or ambient beacons: NEVER introduce periodic phone-home beacons, unconsented analytics pings, or background network calls.",
			Severity:    "error",
			FilePattern: "*.go|go.mod|!*_test.go|!internal/assess/rules.go|!cmd/sila/gate.go",
			Regex:       regexp.MustCompile(`(?i)\b(?:segment-go|mixpanel|posthog|statsig|launchdarkly|amplitude|sentry-go)\b`),
		},
		{
			ID:          "no-hardcoded-secrets",
			Name:        "No Hardcoded Secrets or Sensitive Tokens",
			Category:    "security",
			Source:      "AGENTS.md §0",
			Citation:    "No hardcoded secrets or sensitive logging: NEVER commit credentials or log tokens, passwords, payment payloads, or customer PII.",
			Severity:    "error",
			FilePattern: "!*.md|!*_test.go|!internal/assess/rules.go|!testutil|!testdata",
			Regex:       regexp.MustCompile(`(?i)(?:ghp_[A-Za-z0-9]{36}|xox[baprs]-[A-Za-z0-9-]{10,}|bearer\s+ey[A-Za-z0-9-_=]+\.[A-Za-z0-9-_=]+\.[A-Za-z0-9-_=]+|(?:api[_-]?key|secret[_-]?key|auth[_-]?token)\s*[:=]\s*["'][A-Za-z0-9-_]{20,}["'])`),
		},
		{
			ID:        "no-automation-scripts",
			Name:      "No Python or Bash Automation Scripts in CLI Tools",
			Category:  "toolchain",
			Source:    "AGENTS.md §0",
			Citation:  "No Python or Bash automation scripts: All helper utilities, sync jobs, and CLI tools MUST be written in Go.",
			Severity:  "error",
			MatchPath: true,
			PathRegex: regexp.MustCompile(`^(?:cmd|internal)/.*\.(?:py|sh)$`),
		},
	}
}

// ExtractRulesFromAgentsMD parses prohibition directives from AGENTS.md content.
// It detects standard §0 bullet points as well as custom prohibition declarations.
func ExtractRulesFromAgentsMD(content string) []ScarTissueRule {
	rules := DefaultHardProhibitions()
	ruleMap := make(map[string]bool)
	for _, r := range rules {
		ruleMap[r.ID] = true
	}

	scanner := bufio.NewScanner(strings.NewReader(content))
	inProhibitions := false
	prohibitionHeader := regexp.MustCompile(`(?i)^#{1,3}\s*(?:0\.\s*)?(?:Hard\s+Prohibitions|Prohibitions|Banned|Anti-Patterns)`)
	nextHeader := regexp.MustCompile(`^#{1,3}\s+`)

	bulletRegex := regexp.MustCompile(`^-\s+\*\*([^*]+)\*\*:\s*(.+)`)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if prohibitionHeader.MatchString(line) {
			inProhibitions = true
			continue
		}
		if inProhibitions && nextHeader.MatchString(line) {
			inProhibitions = false
		}
		if !inProhibitions {
			continue
		}

		if m := bulletRegex.FindStringSubmatch(line); len(m) == 3 {
			name := strings.TrimSpace(m[1])
			directive := strings.TrimSpace(m[2])

			// Skip standard prohibitions already handled with high precision by DefaultHardProhibitions
			if isStandardProhibition(name) {
				continue
			}

			id := "agents:" + slugify(name)
			if ruleMap[id] {
				continue
			}

			// If directive contains explicit target tokens like backticks `token` preceded by prohibitive words
			backticks := extractNegativeBacktickTokens(directive)
			if len(backticks) > 0 {
				for _, token := range backticks {
					subID := id + ":" + slugify(token)
					if ruleMap[subID] {
						continue
					}
					esc := regexp.QuoteMeta(token)
					rx, err := regexp.Compile(`\b` + esc + `\b`)
					if err == nil {
						rules = append(rules, ScarTissueRule{
							ID:          subID,
							Name:        fmt.Sprintf("%s (%s)", name, token),
							Category:    "policy",
							Source:      "AGENTS.md",
							Citation:    fmt.Sprintf("AGENTS.md: %s: %s", name, directive),
							Severity:    "error",
							FilePattern: "!*.md|!*.txt|!*_test.go|!internal/assess/rules.go",
							Regex:       rx,
						})
						ruleMap[subID] = true
					}
				}
			}
		}
	}

	return rules
}

// ExtractRulesFromMemos extracts dynamic prohibition rules from architectural memos and verified lessons.
func ExtractRulesFromMemos(facts []store.Fact) []ScarTissueRule {
	var rules []ScarTissueRule
	seen := make(map[string]bool)

	importBannedRx := regexp.MustCompile(`(?i)(?:never\s+import|do\s+not\s+import|don't\s+import|banned\s+import:?)\s+["']?([a-zA-Z0-9_\-\./]+)["']?`)
	usageBannedRx := regexp.MustCompile(`(?i)(?:never\s+use|do\s+not\s+use|don't\s+use|banned:?|outlawed:?|prohibit(?:ed)?:\s*)\s+["']?([a-zA-Z0-9_\-\./]+)["']?`)

	for _, f := range facts {
		// Only consider manual memos, invariants, or verified bug lessons
		isAuth := f.IsManualMemo() || f.IsInvariant() || f.Kind == "lesson" || f.Kind == "invariant" || f.Kind == "nongoal" || f.SessionRef == "manual_memo"
		if !isAuth {
			continue
		}

		claim := strings.TrimSpace(f.Claim)
		if claim == "" {
			continue
		}

		claimLower := strings.ToLower(claim)
		hasProhibitiveIntent := strings.Contains(claimLower, "never") ||
			strings.Contains(claimLower, "do not") ||
			strings.Contains(claimLower, "don't") ||
			strings.Contains(claimLower, "banned") ||
			strings.Contains(claimLower, "prohibit") ||
			strings.Contains(claimLower, "outlawed") ||
			strings.Contains(claimLower, "must not") ||
			strings.Contains(claimLower, "forbidden")

		if !hasProhibitiveIntent {
			continue
		}

		// 1. Check for banned imports
		if m := importBannedRx.FindStringSubmatch(claim); len(m) == 2 {
			target := strings.TrimSpace(m[1])
			ruleID := fmt.Sprintf("memo:%s:import:%s", f.ID, slugify(target))
			if !seen[ruleID] && len(target) > 2 && !isScarTissueStopWord(target) {
				esc := regexp.QuoteMeta(target)
				rx, err := regexp.Compile(`\b` + esc + `\b`)
				if err == nil {
					rules = append(rules, ScarTissueRule{
						ID:          ruleID,
						Name:        fmt.Sprintf("Banned import: %s", target),
						Category:    "memo",
						Source:      fmt.Sprintf("memo:%s", f.ID),
						Citation:    fmt.Sprintf("[Memo #%s] %s", f.ID, claim),
						Severity:    "error",
						FilePattern: "*.go|go.mod|!*_test.go|!internal/assess/rules.go",
						Regex:       rx,
					})
					seen[ruleID] = true
				}
			}
		}

		// 2. Check for banned usage or general tokens
		if m := usageBannedRx.FindStringSubmatch(claim); len(m) == 2 {
			target := strings.TrimSpace(m[1])
			ruleID := fmt.Sprintf("memo:%s:usage:%s", f.ID, slugify(target))
			if !seen[ruleID] && len(target) > 2 && !isScarTissueStopWord(target) {
				esc := regexp.QuoteMeta(target)
				rx, err := regexp.Compile(`\b` + esc + `\b`)
				if err == nil {
					rules = append(rules, ScarTissueRule{
						ID:          ruleID,
						Name:        fmt.Sprintf("Prohibited pattern: %s", target),
						Category:    "memo",
						Source:      fmt.Sprintf("memo:%s", f.ID),
						Citation:    fmt.Sprintf("[Memo #%s] %s", f.ID, claim),
						Severity:    "error",
						FilePattern: "!*.md|!*.txt|!*_test.go|!internal/assess/rules.go",
						Regex:       rx,
					})
					seen[ruleID] = true
				}
			}
		}

		// 3. Backtick tokens in prohibitive claims
		backticks := extractNegativeBacktickTokens(claim)
		for _, token := range backticks {
			if len(token) < 2 {
				continue
			}
			ruleID := fmt.Sprintf("memo:%s:token:%s", f.ID, slugify(token))
			if !seen[ruleID] {
				esc := regexp.QuoteMeta(token)
				rx, err := regexp.Compile(`\b` + esc + `\b`)
				if err == nil {
					rules = append(rules, ScarTissueRule{
						ID:          ruleID,
						Name:        fmt.Sprintf("Prohibited directive: %s", token),
						Category:    "memo",
						Source:      fmt.Sprintf("memo:%s", f.ID),
						Citation:    fmt.Sprintf("[Memo #%s] %s", f.ID, claim),
						Severity:    "error",
						FilePattern: "!*.md|!*.txt|!*_test.go|!internal/assess/rules.go",
						Regex:       rx,
					})
					seen[ruleID] = true
				}
			}
		}
	}

	return rules
}

// CompileProjectRules compiles all active scar tissue rules for a repository,
// merging base prohibitions with project AGENTS.md and knowledge store memos.
func CompileProjectRules(repoDir string, st *store.Store, projectID string) []ScarTissueRule {
	var rules []ScarTissueRule

	// 1. Locate and parse AGENTS.md
	agentsCandidates := []string{
		filepath.Join(repoDir, "AGENTS.md"),
		filepath.Join(repoDir, "cli", "AGENTS.md"),
		filepath.Join(repoDir, "agents", "AGENTS.latest.md"),
	}

	foundAgents := false
	for _, p := range agentsCandidates {
		data, err := os.ReadFile(p)
		if err == nil && len(data) > 0 {
			rules = append(rules, ExtractRulesFromAgentsMD(string(data))...)
			foundAgents = true
			break
		}
	}
	if !foundAgents {
		rules = append(rules, DefaultHardProhibitions()...)
	}

	// 2. Load facts from knowledge store if available
	if st != nil && projectID != "" {
		facts, err := st.FactsByProject(projectID)
		if err == nil && len(facts) > 0 {
			memoRules := ExtractRulesFromMemos(facts)
			rules = append(rules, memoRules...)
		}
	}

	return deduplicateRules(rules)
}

func deduplicateRules(rules []ScarTissueRule) []ScarTissueRule {
	var result []ScarTissueRule
	seen := make(map[string]bool)
	for _, r := range rules {
		if seen[r.ID] {
			continue
		}
		seen[r.ID] = true
		result = append(result, r)
	}
	return result
}

func isStandardProhibition(name string) bool {
	lower := strings.ToLower(name)
	for _, term := range []string{
		"workflow", "package manager", "cgo", "directional css",
		"arabic", "telemetry", "secret", "automation script",
		"copyleft", "remote push", "redacted", "orphaned", "speculative", "sycophancy",
	} {
		if strings.Contains(lower, term) {
			return true
		}
	}
	return false
}

func extractNegativeBacktickTokens(text string) []string {
	var tokens []string
	re := regexp.MustCompile(`(?i)(?:never\s+use|never\s+import|do\s+not\s+use|don't\s+use|banned:?|prohibit(?:ed)?:?|avoid:?)\s+` + "`" + `([^` + "`" + `]+)` + "`")
	matches := re.FindAllStringSubmatch(text, -1)
	for _, m := range matches {
		if len(m) == 2 {
			tok := strings.TrimSpace(m[1])
			if tok != "" {
				tokens = append(tokens, tok)
			}
		}
	}
	return tokens
}

func extractBacktickTokens(text string) []string {
	var tokens []string
	re := regexp.MustCompile("`([^`]+)`")
	matches := re.FindAllStringSubmatch(text, -1)
	for _, m := range matches {
		if len(m) == 2 {
			tok := strings.TrimSpace(m[1])
			if tok != "" {
				tokens = append(tokens, tok)
			}
		}
	}
	return tokens
}

func slugify(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else if r == '-' || r == '_' || r == ' ' || r == '.' || r == '/' {
			b.WriteRune('-')
		}
	}
	res := strings.Trim(b.String(), "-")
	for strings.Contains(res, "--") {
		res = strings.ReplaceAll(res, "--", "-")
	}
	return res
}

func isScarTissueStopWord(token string) bool {
	switch strings.ToLower(strings.TrimSpace(token)) {
	case "use", "using", "policy", "policies", "rule", "rules", "code", "pattern", "patterns",
		"practice", "practices", "action", "actions", "activity", "activities", "the", "this",
		"that", "any", "all", "each", "every", "some", "terms", "method", "methods", "mode",
		"type", "types", "case", "cases":
		return true
	default:
		return false
	}
}
