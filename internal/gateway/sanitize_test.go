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
