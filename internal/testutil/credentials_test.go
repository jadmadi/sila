package testutil_test

import (
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/testutil"
)

func TestMockPEMKey(t *testing.T) {
	key := testutil.MockPEMKey()
	if !strings.Contains(key, "-----BEGIN RSA PRIVATE KEY-----") {
		t.Errorf("expected BEGIN RSA header")
	}
	if !strings.Contains(key, "-----END RSA PRIVATE KEY-----") {
		t.Errorf("expected END RSA footer")
	}
	if !testutil.IsMockCredential(key) {
		t.Errorf("expected IsMockCredential=true for MockPEMKey")
	}
}

func TestMockToken(t *testing.T) {
	cases := []struct {
		provider string
		prefix   string
	}{
		{"github", "ghp_mocktest"},
		{"ghp", "ghp_mocktest"},
		{"github_oauth", "gho_mocktest"},
		{"gitlab", "glpat-mocktest"},
		{"slack", "xoxb-mocktest"},
		{"openai", "sk-mocktest"},
		{"random", "mock_sec_"},
		{"custom", "mock_custom_token_"},
	}

	for _, tc := range cases {
		tok := testutil.MockToken(tc.provider)
		if !strings.HasPrefix(tok, tc.prefix) {
			t.Errorf("provider %q: expected prefix %q, got %q", tc.provider, tc.prefix, tok)
		}
		if !testutil.IsMockCredential(tok) {
			t.Errorf("provider %q: expected IsMockCredential=true for token %q", tc.provider, tok)
		}
	}
}

func TestMockSecretKeyValue(t *testing.T) {
	kv := testutil.MockSecretKeyValue("auth_token")
	if !strings.HasPrefix(kv, "auth_token = ") {
		t.Errorf("expected key prefix, got %q", kv)
	}
	if !testutil.IsMockCredential(kv) {
		t.Errorf("expected IsMockCredential=true for MockSecretKeyValue")
	}
}
