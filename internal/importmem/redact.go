package importmem

import (
	"regexp"
	"strings"
)

// RedactionCategories lists the report keys in stable order.
var RedactionCategories = []string{"secret", "home_path", "email", "path", "control"}

// RedactionResult is sanitized text plus per-category replacement counts.
type RedactionResult struct {
	Text   string
	Counts map[string]int
	Rules  map[string]int `json:"rules,omitempty"`
}

var (
	emailRE = regexp.MustCompile(`\b[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}\b`)

	homePathRE    = regexp.MustCompile(`/(?:home|Users)/[A-Za-z0-9._\-]+`)
	homeWinPathRE = regexp.MustCompile(`[A-Za-z]:\\[Uu]sers\\[A-Za-z0-9._\-]+`)

	posixAbsRE = regexp.MustCompile(`(^|[\s(\[{"'=:,;])(/[A-Za-z0-9._\-+@/][A-Za-z0-9._\-+@/]*)`)
	winAbsRE   = regexp.MustCompile(`(^|[\s(\[{"'=:,;])([A-Za-z]:\\[^\s"'` + "`" + `<>|]+)`)
	uncAbsRE   = regexp.MustCompile(`(^|[\s(\[{"'=:,;])(\\\\[A-Za-z0-9._\-]+\\[^\s"'` + "`" + `<>|]+)`)

	secretMarkerRE = regexp.MustCompile(`\[REDACTED(?:_[A-Z_]+)?\]`)

	residualSecretREs = []*regexp.Regexp{
		regexp.MustCompile(`\b(?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{20,}\b`),
		regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{20,}\b`),
		regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`),
		regexp.MustCompile(`\bsk-[A-Za-z0-9]{20,}\b`),
		regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\b`),
		regexp.MustCompile(`-----` + `BEGIN [A-Z ]*PRIVATE KEY-----`),
		regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9_.\-]{20,}\b`),
	}
)

// Redact removes secrets, personal identifiers and absolute paths from text
// with the default full-mask redactor.
func Redact(text, repoRoot string) (RedactionResult, error) {
	return defaultRedactor.Redact(text, repoRoot)
}
func normalizeRepoRoot(repoRoot string) string {
	repoRoot = strings.TrimSpace(repoRoot)
	if repoRoot == "" {
		return ""
	}
	return strings.TrimRight(strings.ReplaceAll(repoRoot, "\\", "/"), "/")
}

func replaceAbsolutePaths(text, repoRoot string, counts map[string]int) string {
	return posixAbsRE.ReplaceAllStringFunc(text, func(match string) string {
		prefix := ""
		path := match
		if len(match) > 0 && !strings.HasPrefix(match, "/") {
			prefix = match[:1]
			path = match[1:]
		}
		if strings.HasPrefix(path, "//") {
			return match
		}
		trimmed := strings.TrimRight(path, ".,;:)")
		suffix := path[len(trimmed):]
		if repoRoot != "" && (trimmed == repoRoot || strings.HasPrefix(trimmed, repoRoot+"/")) {
			rel := strings.TrimPrefix(strings.TrimPrefix(trimmed, repoRoot), "/")
			if rel == "" {
				rel = "."
			}
			return prefix + rel + suffix
		}
		counts["path"]++
		return prefix + "[REDACTED_PATH]" + suffix
	})
}

func stripControl(text string) (string, int) {
	var b strings.Builder
	b.Grow(len(text))
	removed := 0
	for _, r := range text {
		if r == '\n' || r == '\t' || r == '\r' {
			b.WriteRune(r)
			continue
		}
		if r < 0x20 || r == 0x7f {
			removed++
			continue
		}
		b.WriteRune(r)
	}
	return b.String(), removed
}

func keepBoundary(match string) string {
	if match == "" {
		return ""
	}
	c := match[0]
	if c == ' ' || c == '\t' || c == '\n' || strings.ContainsRune(`([{"'=:,;`, rune(c)) {
		return string(c)
	}
	return ""
}
