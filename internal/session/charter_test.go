package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func signedTestCharter(t *testing.T, key []byte, mutate func(*SubagentCharter)) SubagentCharter {
	t.Helper()
	now := time.Now().UTC()
	charter := SubagentCharter{
		Schema: CharterSchema, CharterID: "sub_s1", ParentSessionID: "p1", SubagentID: "s1",
		ProjectID: "proj", GoalSlug: "goal", AllowPaths: []string{"cmd/sila/"},
		DenyPaths: []string{".git/"}, HandoffPolicy: "parent-only",
		CreatedAt: now.Format(time.RFC3339), ExpiresAt: now.Add(time.Hour).Format(time.RFC3339),
	}
	if mutate != nil {
		mutate(&charter)
	}
	signed, err := SignCharter(charter, key)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

func TestCharterSignVerifyAndTamper(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	charter := signedTestCharter(t, key, nil)
	if err := VerifyCharter(charter, key); err != nil {
		t.Fatalf("valid charter rejected: %v", err)
	}
	if err := VerifyCharter(charter, []byte("another-key-another-key-another!")); err == nil {
		t.Fatal("wrong key accepted")
	}
	tampered := charter
	tampered.ParentSessionID = "p2"
	if err := VerifyCharter(tampered, key); err == nil {
		t.Fatal("tampered charter accepted")
	}
	expired := signedTestCharter(t, key, func(c *SubagentCharter) {
		now := time.Now().UTC()
		c.CreatedAt = now.Add(-2 * time.Hour).Format(time.RFC3339)
		c.ExpiresAt = now.Add(-time.Hour).Format(time.RFC3339)
	})
	if err := VerifyCharter(expired, key); err == nil {
		t.Fatal("expired charter accepted")
	}
}

func TestCharterPathRules(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	charter := signedTestCharter(t, key, nil)
	cases := []struct {
		path    string
		allowed bool
	}{
		{"cmd/sila/hook.go", true},
		{"cmd/sila/gate.go", true},
		{"internal/store/facts.go", false},
		{"../escape", false},
		{"/abs/path", false},
		{".git/config", false},
	}
	for _, tc := range cases {
		err := charter.ValidatePath(tc.path)
		if (err == nil) != tc.allowed {
			t.Errorf("ValidatePath(%q) = %v, allowed=%v", tc.path, err, tc.allowed)
		}
	}
}

func TestCharterSaveLoadAndKey(t *testing.T) {
	dir := t.TempDir()
	key, err := LoadOrCreateCharterKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(key) != 32 {
		t.Fatalf("key length = %d", len(key))
	}
	info, err := os.Stat(CharterKeyPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("key mode = %o", info.Mode().Perm())
	}
	charter, path, err := NewSubagentCharter(dir, "proj", "p1", "s1", "command-code", "", "goal", "lane", []string{"cmd/"}, "parent-only")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "s1.json" {
		t.Fatalf("charter path = %q", path)
	}
	loaded, err := LoadCharter(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.CharterID != charter.CharterID || loaded.Signature == "" {
		t.Fatalf("loaded = %+v", loaded)
	}
	if err := VerifyCharter(loaded, key); err != nil {
		t.Fatalf("loaded charter invalid: %v", err)
	}
}
