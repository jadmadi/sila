package gateway

import (
	"os"
	"regexp"
	"strings"
)

var (
	homeUnixRE     = regexp.MustCompile(`/(?:home|Users)/[a-zA-Z0-9_\.\-]+`)
	homeWindowsRE  = regexp.MustCompile(`[a-zA-Z]:\\(?:[Uu]sers)\\[a-zA-Z0-9_\.\-]+`)
	ghTokenRE      = regexp.MustCompile(`\b(?:ghp|gho|ghu|ghs|ghr)_[a-zA-Z0-9_]{8,}\b|\bgithub_pat_[a-zA-Z0-9_]{8,}\b`)
	bearerRE       = regexp.MustCompile(`(?i)\b(Bearer\s+)[a-zA-Z0-9_\-\.]{20,}\b`)
	apiKeyRE       = regexp.MustCompile(`\b(?:sk-(?:ant-|proj-)?[a-zA-Z0-9_\-]{8,})\b`)
	pemClosedRE    = regexp.MustCompile("-----" + "BEGIN [A-Z ]*PRIVATE KEY-----[\\s\\S]*?-----END [A-Z ]*PRIVATE KEY-----")
	pemTruncatedRE = regexp.MustCompile("-----" + "BEGIN [A-Z ]*PRIVATE KEY-----[\\s\\S]*$")
	paramSecretRE  = regexp.MustCompile(`(?i)(password|token|secret|api_key|client_secret)=([^&\s]+)`)
)

// Redact sanitizes sensitive data from outgoing gateway notifications, stripping
// absolute user home paths, private keys, API tokens, and authorization parameters.
func Redact(text string) string {
	if text == "" {
		return ""
	}

	// 1. Scrub actual user home directory if known
	if h, err := os.UserHomeDir(); err == nil && h != "" && h != "/" {
		text = strings.ReplaceAll(text, h, "~")
	}

	// 2. Scrub other potential user home paths
	text = homeUnixRE.ReplaceAllString(text, "~")
	text = homeWindowsRE.ReplaceAllString(text, "~")

	// 3. Scrub secrets, tokens, and private keys. Closed PEM blocks are
	// replaced first so the truncated pattern cannot swallow safe text
	// between two blocks.
	text = pemClosedRE.ReplaceAllString(text, "[REDACTED_PRIVATE_KEY]")
	text = pemTruncatedRE.ReplaceAllString(text, "[REDACTED_PRIVATE_KEY]")
	text = ghTokenRE.ReplaceAllString(text, "[REDACTED_GH_TOKEN]")
	text = bearerRE.ReplaceAllString(text, "$1[REDACTED]")
	text = apiKeyRE.ReplaceAllString(text, "[REDACTED_API_KEY]")
	text = paramSecretRE.ReplaceAllString(text, "$1=[REDACTED]")

	return text
}
