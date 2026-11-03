package hire

import (
	"os"
	"regexp"
	"strings"
)

var (
	// tokenPatterns matches common API keys, tokens, and authorization credentials.
	tokenPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._\-]{16,}`),
		regexp.MustCompile(`ghp_[A-Za-z0-9]{36,}`),
		regexp.MustCompile(`gho_[A-Za-z0-9]{36,}`),
		regexp.MustCompile(`github_pat_[A-Za-z0-9_]{82,}`),
		regexp.MustCompile(`sk-[A-Za-z0-9_\-]{32,}`),
		regexp.MustCompile(`(?i)(api[_-]?key|auth[_-]?token|secret|password)["']?\s*[:=]\s*["']?([A-Za-z0-9._\-]{8,})["']?`),
		regexp.MustCompile(`xox[baprs]-[A-Za-z0-9_\-]{10,}`),
	}

	// genericUserPathPattern detects /home/<username> or /Users/<username> paths.
	genericUserPathPattern = regexp.MustCompile(`/(home|Users)/[a-zA-Z0-9_\-]+(/|$)`)
)

// ScrubPayload redacts local filesystem paths, user identities, and sensitive credentials
// from outbound prompt payloads, diffs, and audit artifacts before transmission.
func ScrubPayload(text string) string {
	if text == "" {
		return ""
	}

	scrubbed := text

	// 1. Scrub exact user home directory if known
	if home, err := os.UserHomeDir(); err == nil && home != "" && home != "/" {
		scrubbed = strings.ReplaceAll(scrubbed, home, "/workspace")
	}
	if homeEnv := os.Getenv("HOME"); homeEnv != "" && homeEnv != "/" {
		scrubbed = strings.ReplaceAll(scrubbed, homeEnv, "/workspace")
	}

	// 2. Scrub explicit local username
	if user := os.Getenv("USER"); user != "" && user != "root" {
		scrubbed = strings.ReplaceAll(scrubbed, "/home/"+user, "/workspace")
		scrubbed = strings.ReplaceAll(scrubbed, "/Users/"+user, "/workspace")
	}
	if logname := os.Getenv("LOGNAME"); logname != "" && logname != "root" {
		scrubbed = strings.ReplaceAll(scrubbed, "/home/"+logname, "/workspace")
		scrubbed = strings.ReplaceAll(scrubbed, "/Users/"+logname, "/workspace")
	}

	// 3. Scrub any remaining generic /home/<user> or /Users/<user> path patterns
	scrubbed = genericUserPathPattern.ReplaceAllString(scrubbed, "/workspace$2")

	// 4. Scrub authentication tokens and API keys
	for _, re := range tokenPatterns {
		scrubbed = re.ReplaceAllStringFunc(scrubbed, func(match string) string {
			lower := strings.ToLower(match)
			if strings.HasPrefix(lower, "bearer ") {
				return "Bearer <REDACTED_TOKEN>"
			}
			if strings.Contains(match, "=") || strings.Contains(match, ":") {
				parts := strings.FieldsFunc(match, func(r rune) bool {
					return r == '=' || r == ':'
				})
				if len(parts) >= 2 {
					return parts[0] + "=<REDACTED_SECRET>"
				}
			}
			return "<REDACTED_CREDENTIAL>"
		})
	}

	return scrubbed
}

// ScrubDiff scrubs outbound git diffs ensuring code modifications contain no private path leaks.
func ScrubDiff(diff string) string {
	return ScrubPayload(diff)
}
