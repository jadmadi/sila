package testutil

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
)

// MockPEMKey returns a synthetic RSA private key PEM block explicitly marked
// as a safe test fixture for unit tests and secret redaction suites.
func MockPEMKey() string {
	return `-----BEGIN RSA PRIVATE KEY-----
MIIEowIBAAKCAQEA0mockkeytestingfixture00000000000000000000000000000000
MOCK+TEST+FIXTURE+DO+NOT+USE+IN+PRODUCTION+MOCK+FIXTURE+FOR+UNIT+TESTING
abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789
-----END RSA PRIVATE KEY-----`
}

// MockToken returns a synthetic API token matching vendor prefix heuristics
// but containing unambiguous mock markers so sentry heuristics recognize it
// as a test fixture and do not block commits.
func MockToken(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "github", "ghp":
		return "ghp_" + "mocktest1234567890abcdef123456789012"
	case "github_oauth", "gho":
		return "gho_" + "mocktest1234567890abcdef123456789012"
	case "gitlab", "glpat":
		return "glpat-" + "mocktest123456789012"
	case "slack", "xoxb":
		return "xoxb-" + "mocktest-123456789012-123456789012-mocktoken"
	case "openai", "sk":
		return "sk-" + "mocktest-1234567890abcdef1234567890abcdef"
	case "random":
		b := make([]byte, 16)
		_, _ = rand.Read(b)
		return "mock_sec_" + hex.EncodeToString(b)
	default:
		name := strings.ToLower(strings.TrimSpace(provider))
		if name == "" {
			name = "generic"
		}
		return fmt.Sprintf("mock_%s_token_abcdef1234567890", name)
	}
}

// MockSecretKeyValue returns a safe key-value assignment for configuration and parser tests.
func MockSecretKeyValue(keyName string) string {
	if keyName == "" {
		keyName = "api_key"
	}
	return fmt.Sprintf("%s = \"mock_secret_fixture_1234567890abcdef\"", keyName)
}

// IsMockCredential returns true if the input text matches recognizable test fixture markers.
func IsMockCredential(text string) bool {
	lower := strings.ToLower(text)
	if strings.HasPrefix(lower, "mock_") || strings.HasPrefix(lower, "mock-") {
		return true
	}
	markers := []string{
		"mocktest",
		"mock_sec_",
		"mock_secret",
		"mockkeytestingfixture",
		"testfixture",
		"mock_token",
		"mock-token",
		"testutil.mock",
		"sila:test-fixture",
		"sila:mock",
	}
	for _, m := range markers {
		if strings.Contains(lower, m) {
			return true
		}
	}
	return false
}
