package gateway

import (
	"os"
	"strings"
	"testing"
)

func TestRedact(t *testing.T) {
	home, _ := os.UserHomeDir()
	tests := []struct {
		name     string
		input    string
		contains string
		omits    string
	}{
		{
			name:     "empty text",
			input:    "",
			contains: "",
			omits:    "",
		},
		{
			name:     "user home directory path",
			input:    "Error reading file at " + home + "/secret/config.json",
			contains: "~/secret/config.json",
			omits:    home,
		},
		{
			name:     "generic unix home directory",
			input:    "Failed in /home/alice/project/code.go",
			contains: "~/project/code.go",
			omits:    "/home/alice",
		},
		{
			name:     "github personal access token",
			input:    "Using token " + "ghp" + "_123456789012345678901234567890123456 for git sync",
			contains: "[REDACTED_GH_TOKEN]",
			omits:    "ghp" + "_123456789012345678901234567890123456",
		},
		{
			name:     "bearer authorization token",
			input:    "Header: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.e30.t-IDc retry",
			contains: "Bearer [REDACTED]",
			omits:    "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.e30.t-IDc",
		},
		{
			name:     "openai style api key",
			input:    "Using key " + "sk" + "-1234567890abcdef1234567890abcdef in prompt",
			contains: "[REDACTED_API_KEY]",
			omits:    "sk" + "-1234567890abcdef1234567890abcdef",
		},
		{
			name:     "private key pem block",
			input:    "-----" + "BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA0...\n-----END RSA PRIVATE KEY-----",
			contains: "[REDACTED_PRIVATE_KEY]",
			omits:    "MIIEowIBAAKCAQEA0",
		},
		{
			name:     "parameter secrets in url",
			input:    "Connecting to https://api.service.com/v1?token=secret123&client_secret=topsecret456",
			contains: "token=[REDACTED]",
			omits:    "secret123",
		},
		{
			name:     "anthropic style api key",
			input:    "Using key " + "sk-ant-" + "api03abcdef123456 in prompt",
			contains: "[REDACTED_API_KEY]",
			omits:    "sk-ant-" + "api03abcdef123456",
		},
		{
			name:     "openai project api key",
			input:    "Using key " + "sk-proj-" + "abcdefgh12345678 in prompt",
			contains: "[REDACTED_API_KEY]",
			omits:    "sk-proj-" + "abcdefgh12345678",
		},
		{
			name:     "truncated github token",
			input:    "Using token " + "ghp_" + "1234abcd for git sync",
			contains: "[REDACTED_GH_TOKEN]",
			omits:    "ghp_" + "1234abcd",
		},
		{
			name:     "truncated github pat",
			input:    "Using token " + "github_pat_" + "1234abcd for git sync",
			contains: "[REDACTED_GH_TOKEN]",
			omits:    "github_pat_" + "1234abcd",
		},
		{
			name:     "unclosed private key",
			input:    "-----" + "BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA0truncated",
			contains: "[REDACTED_PRIVATE_KEY]",
			omits:    "MIIEowIBAAKCAQEA0truncated",
		},
		{
			name:     "short prefixes are not secrets",
			input:    "sk-short ghp_1234",
			contains: "sk-short ghp_1234",
			omits:    "[REDACTED",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Redact(tc.input)
			if tc.contains != "" && !strings.Contains(got, tc.contains) {
				t.Errorf("expected redacted output to contain %q, got: %s", tc.contains, got)
			}
			if tc.omits != "" && strings.Contains(got, tc.omits) {
				t.Errorf("expected redacted output to omit sensitive %q, got: %s", tc.omits, got)
			}
		})
	}
}

func TestRedactPreservesSafeTextBetweenPEMBlocks(t *testing.T) {
	closedBody := "MIIEclosed"
	openBody := "MIIEopen"
	input := "-----" + "BEGIN RSA PRIVATE KEY-----\n" + closedBody + "\n-----" + "END RSA PRIVATE KEY-----" +
		" safe diagnostic text " +
		"-----" + "BEGIN RSA PRIVATE KEY-----\n" + openBody
	got := Redact(input)
	if !strings.Contains(got, "safe diagnostic text") {
		t.Fatalf("safe text between PEM blocks was lost: %s", got)
	}
	if strings.Contains(got, closedBody) || strings.Contains(got, openBody) {
		t.Fatalf("PEM body leaked: %s", got)
	}
	if strings.Count(got, "[REDACTED_PRIVATE_KEY]") != 2 {
		t.Fatalf("expected two redactions, got: %s", got)
	}
}
