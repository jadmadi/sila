package importmem

import (
	"strings"
	"testing"
)

func TestRedactSecrets(t *testing.T) {
	cases := []struct {
		name   string
		input  string
		marker string
	}{
		{"github token", "token ghp_" + strings.Repeat("a", 36) + " end", "[REDACTED_GH_TOKEN]"},
		{"bearer", "Authorization: Bearer " + strings.Repeat("b", 24), "[REDACTED]"},
		{"api key", "key sk-" + strings.Repeat("c", 24), "[REDACTED_API_KEY]"},
		{"private key", "-----BEGIN RSA " + "PRIVATE KEY-----\nMIIEow\n-----END RSA " + "PRIVATE KEY-----", "[REDACTED_PRIVATE_KEY]"},
		{"aws key", "AKIA" + strings.Repeat("D", 16), "[REDACTED_AWS_KEY]"},
		{"jwt", "eyJ" + strings.Repeat("e", 12) + "." + strings.Repeat("f", 12) + "." + strings.Repeat("g", 12), "[REDACTED_JWT]"},
		{"parameter secret", "password=hunter2", "password=[REDACTED]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := Redact(tc.input, "")
			if err != nil {
				t.Fatalf("redact: %v", err)
			}
			if !strings.Contains(result.Text, tc.marker) {
				t.Fatalf("expected %q in %q", tc.marker, result.Text)
			}
			if strings.Contains(result.Text, "hunter2") || strings.Contains(result.Text, strings.Repeat("a", 36)) {
				t.Fatalf("secret survived: %q", result.Text)
			}
			if result.Counts["secret"] == 0 {
				t.Fatalf("secret count not incremented: %+v", result.Counts)
			}
		})
	}
}

func TestRedactEmailsAndPaths(t *testing.T) {
	repo := t.TempDir()
	text := "owner alice@example.com lives at /home/alice/work\n" +
		"repo file " + repo + "/internal/store/memory.go\n" +
		"external /opt/private/vault/data.txt\n" +
		"windows C:\\Users\\alice\\secret.txt\n" +
		"drive D:\\work\\notes.txt\n" +
		"control \x01 byte"
	result, err := Redact(text, repo)
	if err != nil {
		t.Fatal(err)
	}
	out := result.Text
	if strings.Contains(out, "alice@example.com") {
		t.Fatalf("email survived: %q", out)
	}
	if !strings.Contains(out, "[REDACTED_EMAIL]") {
		t.Fatalf("email marker missing: %q", out)
	}
	if strings.Contains(out, "/home/alice") || strings.Contains(out, "C:\\Users\\alice") {
		t.Fatalf("home path survived: %q", out)
	}
	if !strings.Contains(out, "internal/store/memory.go") {
		t.Fatalf("repo path was not made relative: %q", out)
	}
	if strings.Contains(out, "/opt/private") || strings.Contains(out, "D:\\work") {
		t.Fatalf("external path survived: %q", out)
	}
	if !strings.Contains(out, "[REDACTED_PATH]") {
		t.Fatalf("path marker missing: %q", out)
	}
	if strings.ContainsRune(out, '\x01') {
		t.Fatalf("control byte survived: %q", out)
	}
	if result.Counts["email"] != 1 || result.Counts["path"] < 2 || result.Counts["home_path"] < 1 || result.Counts["control"] != 1 {
		t.Fatalf("counts: %+v", result.Counts)
	}
}

func TestRedactFailureClosed(t *testing.T) {
	if _, err := Redact("abc\x00def", ""); err == nil || CodeOf(err) != CodeRedactionFailed {
		t.Fatalf("NUL error: %v", err)
	}
	if _, err := Redact("\x01\x02\x03", ""); err == nil || CodeOf(err) != CodeRedactionFailed {
		t.Fatalf("empty result error: %v", err)
	}
	// The shared scrubber only recognizes 36-character GitHub tokens. A short
	// residual token must fail closed instead of being stored.
	short := "ghp_" + strings.Repeat("a", 20)
	if _, err := Redact(short, ""); err == nil || CodeOf(err) != CodeRedactionFailed {
		t.Fatalf("residual secret error: %v", err)
	}
}
