package assess

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsCFWorkerProject(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Initially empty directory
	if IsCFWorkerProject(tmpDir) {
		t.Fatalf("expected false for empty directory")
	}

	// 2. Directory with wrangler.jsonc
	wranglerPath := filepath.Join(tmpDir, "wrangler.jsonc")
	if err := os.WriteFile(wranglerPath, []byte(`{"name": "test-worker"}`), 0644); err != nil {
		t.Fatalf("failed to write wrangler.jsonc: %v", err)
	}
	if !IsCFWorkerProject(tmpDir) {
		t.Fatalf("expected true for directory with wrangler.jsonc")
	}
	_ = os.Remove(wranglerPath)

	// 3. Directory with sub-repo worker/wrangler.toml
	subDir := filepath.Join(tmpDir, "worker")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatalf("failed to create worker subdir: %v", err)
	}
	tomlPath := filepath.Join(subDir, "wrangler.toml")
	if err := os.WriteFile(tomlPath, []byte(`name = "test-worker"`), 0644); err != nil {
		t.Fatalf("failed to write wrangler.toml: %v", err)
	}
	if !IsCFWorkerProject(tmpDir) {
		t.Fatalf("expected true when child directory contains wrangler.toml")
	}
}

func TestScanCFWorkerDiff_Secret500Masking(t *testing.T) {
	diffWith500Masking := `diff --git a/src/middleware/auth.ts b/src/middleware/auth.ts
new file mode 100644
index 0000000..1111111
--- /dev/null
+++ b/src/middleware/auth.ts
@@ -0,0 +1,15 @@
+export async function authMiddleware(c, next) {
+  const secret = c.req.header("X-Custom-Secret");
+  if (!secret) {
+    return c.text("Unauthorized: missing secret", 500);
+  }
+  if (secret !== c.env.AUTH_SECRET) {
+    throw new Error("Unauthorized: invalid secret token");
+  }
+  await next();
+}`

	violations := ScanCFWorkerDiff(diffWith500Masking)
	if len(violations) < 2 {
		t.Fatalf("expected at least 2 violations for secret 500 masking, got %d", len(violations))
	}

	hasReturn500 := false
	hasThrowError := false
	for _, v := range violations {
		if v.RuleID == "cf-secret-500-masking" {
			if v.LineNumber == 4 {
				hasReturn500 = true
			}
			if v.LineNumber == 7 {
				hasThrowError = true
			}
		}
	}

	if !hasReturn500 {
		t.Errorf("expected return 500 on missing secret to be flagged on line 4")
	}
	if !hasThrowError {
		t.Errorf("expected throw new Error on invalid secret to be flagged on line 7")
	}
}

func TestScanCFWorkerDiff_ValidAuthClean(t *testing.T) {
	cleanDiff := `diff --git a/src/middleware/auth.ts b/src/middleware/auth.ts
new file mode 100644
index 0000000..1111111
--- /dev/null
+++ b/src/middleware/auth.ts
@@ -0,0 +1,12 @@
+export async function authMiddleware(c, next) {
+  const secret = c.req.header("X-Custom-Secret");
+  if (!secret) {
+    return c.text("Unauthorized: missing secret", 401);
+  }
+  if (secret !== c.env.AUTH_SECRET) {
+    return c.text("Forbidden: invalid secret", 403);
+  }
+  await next();
+}`

	violations := ScanCFWorkerDiff(cleanDiff)
	if len(violations) != 0 {
		t.Fatalf("expected 0 violations for clean 401/403 auth, got %d: %+v", len(violations), violations)
	}
}

func TestScanCFWorkerDiff_TestNodeEnvCheck(t *testing.T) {
	diffWithNodeEnvInTest := `diff --git a/src/routes/api.test.ts b/src/routes/api.test.ts
new file mode 100644
index 0000000..2222222
--- /dev/null
+++ b/src/routes/api.test.ts
@@ -0,0 +1,8 @@
+import { describe, it, expect } from "vitest";
+
+describe("Worker route", () => {
+  it("runs only in test env", () => {
+    expect(process.env.NODE_ENV).toBe("test");
+  });
+});`

	violations := ScanCFWorkerDiff(diffWithNodeEnvInTest)
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation for process.env.NODE_ENV in test file, got %d", len(violations))
	}

	if violations[0].RuleID != "cf-test-node-env-check" {
		t.Errorf("expected RuleID cf-test-node-env-check, got %s", violations[0].RuleID)
	}
	if violations[0].LineNumber != 5 {
		t.Errorf("expected LineNumber 5, got %d", violations[0].LineNumber)
	}
}

func TestRunCFWorkerSentry(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Non-worker project skips
	res := RunCFWorkerSentry(tmpDir, "")
	if !res.Skipped {
		t.Fatalf("expected non-worker dir to be skipped")
	}

	// 2. Add wrangler manifest
	_ = os.WriteFile(filepath.Join(tmpDir, "wrangler.json"), []byte(`{}`), 0644)

	// 3. Run with clean diff
	resClean := RunCFWorkerSentry(tmpDir, "")
	if resClean.Skipped || !resClean.Clean {
		t.Fatalf("expected clean result for empty worker dir")
	}

	// 4. Run with bad diff
	badDiff := `diff --git a/src/worker.test.ts b/src/worker.test.ts
new file mode 100644
--- /dev/null
+++ b/src/worker.test.ts
@@ -0,0 +1,3 @@
+if (process.env.NODE_ENV === "test") {
+  console.log("running test");
+}`
	resBad := RunCFWorkerSentry(tmpDir, badDiff)
	if resBad.Clean {
		t.Fatalf("expected bad diff to fail sentry")
	}
	if len(resBad.Violations) != 1 {
		t.Fatalf("expected 1 violation, got %d", len(resBad.Violations))
	}
}
