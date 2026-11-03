package gate

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAuditCFCleanNonWorker(t *testing.T) {
	tempDir := t.TempDir()
	res := AuditCFClean(tempDir, nil)
	if !res.Skipped {
		t.Errorf("expected AuditCFClean to skip non-worker repo, got skipped=%v", res.Skipped)
	}
}

func TestAuditCFCleanClean(t *testing.T) {
	tempDir := t.TempDir()
	wranglerContent := `{
  "name": "worker-app",
  "account_id": "<CLOUDFLARE_ACCOUNT_ID>",
  "d1_databases": [
    {
      "binding": "DB",
      "database_name": "test-db",
      "database_id": "00000000-0000-0000-0000-000000000000"
    }
  ]
}`
	if err := os.WriteFile(filepath.Join(tempDir, "wrangler.jsonc"), []byte(wranglerContent), 0o644); err != nil {
		t.Fatal(err)
	}

	res := AuditCFClean(tempDir, nil)
	if res.Skipped {
		t.Errorf("expected worker project to not be skipped")
	}
	if !res.Clean {
		t.Errorf("expected clean result for placeholder credentials, got %d violations: %+v", len(res.Violations), res.Violations)
	}
}

func TestAuditCFCleanUnmaskedCredentials(t *testing.T) {
	tempDir := t.TempDir()
	wranglerContent := `{
  "name": "worker-app",
  "account_id": "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4",
  "d1_databases": [
    {
      "binding": "DB",
      "database_name": "test-db",
      "database_id": "12345678-1234-1234-1234-123456789abc"
    }
  ]
}`
	if err := os.WriteFile(filepath.Join(tempDir, "wrangler.json"), []byte(wranglerContent), 0o644); err != nil {
		t.Fatal(err)
	}

	res := AuditCFClean(tempDir, nil)
	if res.Clean {
		t.Errorf("expected unmasked credentials to fail audit")
	}
	if len(res.Violations) < 2 {
		t.Errorf("expected at least 2 violations (account_id and database_id), got %d: %+v", len(res.Violations), res.Violations)
	}
}

func TestScanFileForCFCredentials(t *testing.T) {
	content := []byte(`
# Cloudflare config
account_id = "0123456789abcdef0123456789abcdef"
database_id = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
`)
	violations := ScanFileForCFCredentials("wrangler.toml", content, "", "")
	if len(violations) != 2 {
		t.Errorf("expected 2 violations for TOML syntax, got %d", len(violations))
	}

	cleanContent := []byte(`
# Cloudflare config
account_id = "<CLOUDFLARE_ACCOUNT_ID>"
database_id = "00000000-0000-0000-0000-000000000000"
`)
	cleanViolations := ScanFileForCFCredentials("wrangler.toml", cleanContent, "", "")
	if len(cleanViolations) != 0 {
		t.Errorf("expected 0 violations for clean TOML, got %d", len(cleanViolations))
	}
}
