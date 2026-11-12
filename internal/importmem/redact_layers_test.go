package importmem

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"
)

func redactWith(t *testing.T, mask MaskMode, text string) RedactionResult {
	t.Helper()
	r, err := NewRedactor(RedactorOptions{Mask: mask})
	if err != nil {
		t.Fatal(err)
	}
	result, err := r.Redact(text, "")
	if err != nil {
		t.Fatalf("redact: %v", err)
	}
	return result
}

func syntheticToken() string {
	sum := sha256.Sum256([]byte("sila-redaction-layer-six-sample"))
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	out := make([]byte, 32)
	for i := 0; i < len(out); i++ {
		out[i] = alphabet[int(sum[i])%len(alphabet)]
	}
	return string(out)
}

func syntheticJWT() string {
	encoder := base64.RawURLEncoding
	header := encoder.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payload := encoder.EncodeToString([]byte(`{"sub":"1234567890"}`))
	sum := sha256.Sum256([]byte("sila-redaction-jwt-sample"))
	return header + "." + payload + "." + hex.EncodeToString(sum[:])
}

func TestRedactionLayers(t *testing.T) {
	token := syntheticToken()
	pemBody := base64.RawStdEncoding.EncodeToString([]byte("sila redaction pem body sample"))
	pemBegin := "-----BEGIN RSA " + "PRIVATE KEY-----"
	pemEnd := "-----END RSA " + "PRIVATE KEY-----"
	cases := []struct {
		name   string
		input  string
		secret string
		marker string
	}{
		{"dsn password", "postgres://user:pg-secret-pass@db.local:5432/app", "pg-secret-pass", "[REDACTED]"},
		{"encoded dsn", "postgres://user:p%40ssw0rd@db.local/app", "p%40ssw0rd", "[REDACTED]"},
		{"password equals", "password=hunter2", "hunter2", "password=[REDACTED]"},
		{"pwd colon", "pwd: topsecret99", "topsecret99", "pwd: [REDACTED]"},
		{"secret put", "echo $WRANGLER_KEY | wrangler secret put API_KEY", "$WRANGLER_KEY", "[REDACTED]"},
		{"export assignment", "export GITHUB_TOKEN=ghp_abcdefghijklmnopqrstuvwxyz", "ghp_abcdefghijklmnopqrstuvwxyz", "GITHUB_TOKEN=[REDACTED]"},
		{"inline assignment", "API_SECRET=rawvalue123", "rawvalue123", "API_SECRET=[REDACTED]"},
		{"entropy token", "token " + token + " end", token, "[REDACTED_SECRET]"},
		{"jwt", syntheticJWT(), "dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U", "[REDACTED_JWT]"},
		{"pem", pemBegin + "\n" + pemBody + "\n" + pemEnd, pemBody, "[REDACTED_PRIVATE_KEY]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := redactWith(t, MaskFull, tc.input)
			if strings.Contains(result.Text, tc.secret) {
				t.Fatalf("secret survived: %q", result.Text)
			}
			if !strings.Contains(result.Text, tc.marker) {
				t.Fatalf("marker %q missing: %q", tc.marker, result.Text)
			}
			if result.Counts["secret"] == 0 {
				t.Fatalf("secret count not incremented: %+v", result.Counts)
			}
		})
	}
}

func TestRedactionMaskModes(t *testing.T) {
	secret := syntheticToken()
	input := "value " + secret + " end"
	full := redactWith(t, MaskFull, input)
	if strings.Contains(full.Text, "Zx9Q") {
		t.Fatalf("full mode leaked a prefix: %q", full.Text)
	}
	prefix := redactWith(t, MaskPrefix, input)
	if !strings.Contains(prefix.Text, secret[:4]+"[REDACTED_SECRET]") {
		t.Fatalf("prefix mode = %q", prefix.Text)
	}
	short := redactWith(t, MaskPrefix, "value shortie1 end")
	if strings.Contains(short.Text, "shor[") {
		t.Fatalf("short secret kept a prefix: %q", short.Text)
	}
	second := redactWith(t, MaskFull, full.Text)
	if second.Text != full.Text {
		t.Fatalf("double redaction differs:\n%q\n%q", full.Text, second.Text)
	}
	if second.Counts["secret"] != 0 {
		t.Fatalf("double redaction counted %d new secrets", second.Counts["secret"])
	}
}

func TestRedactionLayerNineFailsClosed(t *testing.T) {
	r, err := NewRedactor(RedactorOptions{})
	if err != nil {
		t.Fatal(err)
	}
	testDetector = func(string) bool { return true }
	defer func() { testDetector = nil }()
	if _, err := r.Redact("clean text", ""); err == nil || !strings.Contains(err.Error(), "REDACTION_INCOMPLETE") {
		t.Fatalf("err = %v", err)
	}
}
