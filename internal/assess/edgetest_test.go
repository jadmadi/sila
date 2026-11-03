package assess

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuditEdgeTests_NonWorkerRepo(t *testing.T) {
	tempDir := t.TempDir()

	// Empty directory without wrangler or worker packages
	rep, err := AuditEdgeTests(tempDir, DefaultEdgeAuditOptions())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if rep.Applicable {
		t.Errorf("expected non-worker repo to not be applicable, got true")
	}
	if rep.Status != "SKIPPED" {
		t.Errorf("expected SKIPPED status, got %s", rep.Status)
	}
	if !strings.Contains(rep.Reason, "No Cloudflare Worker configuration") {
		t.Errorf("unexpected skip reason: %s", rep.Reason)
	}
}

func TestAuditEdgeTests_IsolatedSuite(t *testing.T) {
	tempDir := t.TempDir()

	// Create wrangler.jsonc
	wranglerJSONC := `{
  "name": "test-worker",
  "main": "src/index.ts",
  "d1_databases": [
    { "binding": "DB", "database_name": "test_db", "database_id": "xxx" }
  ]
}`
	if err := os.WriteFile(filepath.Join(tempDir, "wrangler.jsonc"), []byte(wranglerJSONC), 0644); err != nil {
		t.Fatal(err)
	}

	// Create vitest.config.ts with matching binding
	vitestConfig := `import { defineWorkersConfig } from '@cloudflare/vitest-pool-workers/config';
export default defineWorkersConfig({
  test: {
    poolOptions: {
      workers: {
        miniflare: {
          d1Databases: ['DB'],
        },
      },
    },
  },
});`
	if err := os.WriteFile(filepath.Join(tempDir, "vitest.config.ts"), []byte(vitestConfig), 0644); err != nil {
		t.Fatal(err)
	}

	// Create properly isolated test file
	testContent := `import { env } from 'cloudflare:test';
import { describe, it, expect, beforeAll, beforeEach } from 'vitest';

describe('Isolated Worker Test', () => {
  beforeAll(async () => {
    await env.DB.prepare('CREATE TABLE IF NOT EXISTS users (id TEXT PRIMARY KEY, name TEXT)').run();
  });

  beforeEach(async () => {
    await env.DB.prepare('DELETE FROM users').run();
  });

  it('inserts user A', async () => {
    await env.DB.prepare("INSERT INTO users (id, name) VALUES ('u1', 'Alice')").run();
    const row = await env.DB.prepare("SELECT * FROM users WHERE id = 'u1'").first();
    expect(row).toBeDefined();
  });

  it('inserts user B in a fresh clean state', async () => {
    await env.DB.prepare("INSERT INTO users (id, name) VALUES ('u2', 'Bob')").run();
    const rows = await env.DB.prepare("SELECT * FROM users").all();
    expect(rows.results.length).toBe(1);
  });
});
`
	testDir := filepath.Join(tempDir, "test")
	if err := os.MkdirAll(testDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(testDir, "isolated.spec.ts"), []byte(testContent), 0644); err != nil {
		t.Fatal(err)
	}

	rep, err := AuditEdgeTests(tempDir, DefaultEdgeAuditOptions())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !rep.Applicable {
		t.Fatalf("expected worker project to be applicable")
	}
	if !rep.Passed {
		t.Errorf("expected isolated suite to PASS, got %s with issues: %+v", rep.Status, rep.Issues)
	}
	if rep.Summary.TotalFiles != 1 {
		t.Errorf("expected 1 total file, got %d", rep.Summary.TotalFiles)
	}
	if rep.Summary.TotalTests != 2 {
		t.Errorf("expected 2 tests, got %d", rep.Summary.TotalTests)
	}
	if rep.Summary.MutatingTests != 2 {
		t.Errorf("expected 2 mutating tests, got %d", rep.Summary.MutatingTests)
	}
	if rep.Summary.IsolatedFiles != 1 {
		t.Errorf("expected 1 isolated file, got %d", rep.Summary.IsolatedFiles)
	}
	if rep.Summary.Violations != 0 {
		t.Errorf("expected 0 violations, got %d", rep.Summary.Violations)
	}
}

func TestAuditEdgeTests_D1StateLeakageViolation(t *testing.T) {
	tempDir := t.TempDir()

	if err := os.WriteFile(filepath.Join(tempDir, "wrangler.json"), []byte(`{"name":"w"}`), 0644); err != nil {
		t.Fatal(err)
	}

	// Test suite without beforeEach reset hook
	unisolatedContent := `import { env } from 'cloudflare:test';
import { describe, it, expect, beforeAll } from 'vitest';

describe('Leaky Worker Test', () => {
  beforeAll(async () => {
    await env.DB.prepare('CREATE TABLE IF NOT EXISTS messages (id TEXT PRIMARY KEY, text TEXT)').run();
  });

  it('inserts message 1', async () => {
    await env.DB.prepare("INSERT INTO messages (id, text) VALUES ('msg-1', 'hello')").run();
  });

  it('inserts message 2 without cleaning previous message', async () => {
    await env.DB.prepare("INSERT INTO messages (id, text) VALUES ('msg-2', 'world')").run();
  });
});
`
	testDir := filepath.Join(tempDir, "test")
	if err := os.MkdirAll(testDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(testDir, "leaky.spec.ts"), []byte(unisolatedContent), 0644); err != nil {
		t.Fatal(err)
	}

	rep, err := AuditEdgeTests(tempDir, DefaultEdgeAuditOptions())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if rep.Passed {
		t.Errorf("expected unisolated suite to fail, but Passed = true")
	}
	if rep.Status != "FAIL" {
		t.Errorf("expected Status == FAIL, got %s", rep.Status)
	}
	if rep.Summary.Violations == 0 {
		t.Errorf("expected at least 1 violation, got 0")
	}

	var hasEdge001 bool
	for _, is := range rep.Issues {
		if is.RuleID == "EDGE-001" && is.Severity == "FAIL" {
			hasEdge001 = true
			if !strings.Contains(is.Message, "Unisolated D1 database mutations") {
				t.Errorf("unexpected message: %s", is.Message)
			}
		}
	}
	if !hasEdge001 {
		t.Errorf("expected EDGE-001 violation to be reported, got: %+v", rep.Issues)
	}
}

func TestAuditEdgeTests_KVLeakageAndSharedState(t *testing.T) {
	tempDir := t.TempDir()

	if err := os.WriteFile(filepath.Join(tempDir, "wrangler.toml"), []byte(`name = "kv-worker"`), 0644); err != nil {
		t.Fatal(err)
	}

	// Test suite with KV writes and top-level mutable let without beforeEach
	testContent := `import { env } from 'cloudflare:test';
import { describe, it, expect } from 'vitest';

let testRunCount = 0;

describe('KV and Global State Test', () => {
  it('writes to KV cache', async () => {
    testRunCount++;
    await env.CACHE_KV.put('session_123', 'active');
  });

  it('reads from KV cache', async () => {
    testRunCount = 42;
    const val = await env.CACHE_KV.get('session_123');
    expect(val).toBe('active');
  });
});
`
	if err := os.WriteFile(filepath.Join(tempDir, "kv.test.ts"), []byte(testContent), 0644); err != nil {
		t.Fatal(err)
	}

	rep, err := AuditEdgeTests(tempDir, DefaultEdgeAuditOptions())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if rep.Status != "WARN" {
		t.Errorf("expected status WARN, got %s", rep.Status)
	}

	var hasKV, hasSharedState bool
	for _, is := range rep.Issues {
		if is.RuleID == "EDGE-002" {
			hasKV = true
		}
		if is.RuleID == "EDGE-004" && strings.Contains(is.Message, "testRunCount") {
			hasSharedState = true
		}
	}

	if !hasKV {
		t.Errorf("expected EDGE-002 (KV state leakage), got issues: %+v", rep.Issues)
	}
	if !hasSharedState {
		t.Errorf("expected EDGE-004 (shared mutable state for testRunCount), got issues: %+v", rep.Issues)
	}
}

func TestAuditEdgeTests_ConfigParity(t *testing.T) {
	tempDir := t.TempDir()

	// wrangler.jsonc declares DB and SESSIONS_KV
	wranglerJSONC := `{
  "name": "worker-parity",
  "d1_databases": [
    { "binding": "DB", "database_name": "prod_db" }
  ],
  "kv_namespaces": [
    { "binding": "SESSIONS_KV", "id": "123" }
  ]
}`
	if err := os.WriteFile(filepath.Join(tempDir, "wrangler.jsonc"), []byte(wranglerJSONC), 0644); err != nil {
		t.Fatal(err)
	}

	// vitest.config.ts only configures DB, missing SESSIONS_KV
	vitestConfig := `import { defineWorkersConfig } from '@cloudflare/vitest-pool-workers/config';
export default defineWorkersConfig({
  test: {
    poolOptions: {
      workers: {
        miniflare: {
          d1Databases: ['DB'],
        },
      },
    },
  },
});`
	if err := os.WriteFile(filepath.Join(tempDir, "vitest.config.ts"), []byte(vitestConfig), 0644); err != nil {
		t.Fatal(err)
	}

	rep, err := AuditEdgeTests(tempDir, DefaultEdgeAuditOptions())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var hasParityIssue bool
	for _, is := range rep.Issues {
		if is.RuleID == "EDGE-006" && strings.Contains(is.Message, "SESSIONS_KV") {
			hasParityIssue = true
		}
	}
	if !hasParityIssue {
		t.Errorf("expected EDGE-006 for missing SESSIONS_KV binding in vitest config, got: %+v", rep.Issues)
	}
}

func TestRenderEdgeAuditTOON(t *testing.T) {
	rep := &EdgeAuditReport{
		Applicable: true,
		Passed:     false,
		Status:     "FAIL",
		Framework:  "@cloudflare/vitest-pool-workers",
		Summary: EdgeAuditSummary{
			TotalFiles:    2,
			TotalTests:    10,
			MutatingTests: 4,
			IsolatedFiles: 1,
			Violations:    1,
			Warnings:      1,
		},
		Issues: []EdgeIssue{
			{
				File:       "test/index.spec.ts",
				Line:       42,
				TestName:   "should insert item",
				RuleID:     "EDGE-001",
				Severity:   "FAIL",
				Message:    "Unisolated D1 mutation",
				Suggestion: "Add beforeEach reset",
			},
		},
	}

	toon := RenderEdgeAuditTOON(rep)
	if !strings.Contains(toon, "edge_audit_summary:") {
		t.Errorf("expected edge_audit_summary object header in TOON, got: %s", toon)
	}
	if !strings.Contains(toon, "issues[1]{rule_id,severity,file,line,test_name,message,suggestion}:") {
		t.Errorf("expected issues table in TOON, got: %s", toon)
	}
	if !strings.Contains(toon, "EDGE-001") {
		t.Errorf("expected EDGE-001 in TOON, got: %s", toon)
	}
}

func TestAuditEdgeTests_RealWorkerWorkspace(t *testing.T) {
	workerDir := filepath.Join("..", "..", "..", "worker")
	if _, err := os.Stat(workerDir); err != nil {
		t.Skip("skipping test against sibling worker repo; not found at", workerDir)
	}

	rep, err := AuditEdgeTests(workerDir, DefaultEdgeAuditOptions())
	if err != nil {
		t.Fatalf("unexpected error auditing real worker directory: %v", err)
	}

	if !rep.Applicable {
		t.Fatalf("expected real worker directory to be recognized as Cloudflare Worker project")
	}

	// Verify that index.spec.ts was discovered and audited
	if rep.Summary.TotalFiles == 0 {
		t.Fatalf("expected at least 1 test file in worker repo, found 0")
	}

	t.Logf("Audited real worker repo: %d files, %d tests, %d mutating, %d issues (Status: %s)",
		rep.Summary.TotalFiles, rep.Summary.TotalTests, rep.Summary.MutatingTests, len(rep.Issues), rep.Status)
}
