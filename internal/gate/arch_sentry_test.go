package gate

import (
	"strings"
	"testing"
)

func TestScanArchDiff_D1LoopDetected(t *testing.T) {
	diff := `diff --git a/worker/src/drain.ts b/worker/src/drain.ts
index abcdef1..1234567 100644
--- a/worker/src/drain.ts
+++ b/worker/src/drain.ts
@@ -10,6 +10,10 @@ export async function drain(messages: Message[], env: Env) {
+  for (const msg of messages) {
+    await env.DB.prepare("INSERT INTO messages (id, body) VALUES (?, ?)").bind(msg.id, msg.body).run();
+  }
   return new Response("ok");
 }
`
	res := ScanArchDiff(diff)
	if res.Clean {
		t.Fatal("expected unbatched D1 loop to be flagged")
	}
	if len(res.Violations) != 1 {
		t.Fatalf("expected 1 violation, got %d", len(res.Violations))
	}
	v := res.Violations[0]
	if v.Rule != "unbatched-d1-loop" {
		t.Errorf("expected rule 'unbatched-d1-loop', got %q", v.Rule)
	}
	if v.File != "worker/src/drain.ts" {
		t.Errorf("expected file 'worker/src/drain.ts', got %q", v.File)
	}
	if v.Line != 11 {
		t.Errorf("expected line 11, got %d", v.Line)
	}
	if !strings.Contains(v.Remedy, "env.DB.batch") {
		t.Errorf("expected remedy to mention env.DB.batch: %s", v.Remedy)
	}
}

func TestScanArchDiff_D1BatchAllowed(t *testing.T) {
	diff := `diff --git a/worker/src/drain.ts b/worker/src/drain.ts
index abcdef1..1234567 100644
--- a/worker/src/drain.ts
+++ b/worker/src/drain.ts
@@ -10,6 +10,10 @@ export async function drain(messages: Message[], env: Env) {
+  const stmts = messages.map(m => env.DB.prepare("INSERT INTO messages VALUES (?)").bind(m.id));
+  await env.DB.batch(stmts);
   return new Response("ok");
 }
`
	res := ScanArchDiff(diff)
	if !res.Clean {
		t.Fatalf("expected D1 batch to pass cleanly, got violations: %+v", res.Violations)
	}
}

func TestScanArchDiff_RawANSIEscapeDetected(t *testing.T) {
	diff := `diff --git a/cmd/sila/status.go b/cmd/sila/status.go
index abcdef1..1234567 100644
--- a/cmd/sila/status.go
+++ b/cmd/sila/status.go
@@ -45,6 +45,7 @@ func printStatus() {
+	fmt.Printf("\033[31mFailed\033[0m\n")
 }
`
	res := ScanArchDiff(diff)
	if res.Clean {
		t.Fatal("expected raw ANSI escape to be flagged in CLI source file")
	}
	if len(res.Violations) != 1 {
		t.Fatalf("expected 1 violation, got %d", len(res.Violations))
	}
	v := res.Violations[0]
	if v.Rule != "raw-ansi-escape" {
		t.Errorf("expected rule 'raw-ansi-escape', got %q", v.Rule)
	}
	if v.File != "cmd/sila/status.go" || v.Line != 45 {
		t.Errorf("unexpected file/line: %s:%d", v.File, v.Line)
	}
	if !strings.Contains(v.Remedy, "render.UI") {
		t.Errorf("expected remedy to mention render.UI: %s", v.Remedy)
	}
}

func TestScanArchDiff_RawANSIEscapeInRenderAllowed(t *testing.T) {
	diff := `diff --git a/internal/render/ansi.go b/internal/render/ansi.go
index abcdef1..1234567 100644
--- a/internal/render/ansi.go
+++ b/internal/render/ansi.go
@@ -12,6 +12,7 @@ func (u *TerminalUI) Red(s string) string {
+	return "\x1b[31m" + s + "\x1b[0m"
 }
`
	res := ScanArchDiff(diff)
	if !res.Clean {
		t.Fatalf("expected ANSI escapes in internal/render to be permitted: %+v", res.Violations)
	}
}

func TestScanArchDiff_RawANSIEscapeInTestAllowed(t *testing.T) {
	diff := `diff --git a/cmd/sila/status_test.go b/cmd/sila/status_test.go
index abcdef1..1234567 100644
--- a/cmd/sila/status_test.go
+++ b/cmd/sila/status_test.go
@@ -20,6 +20,7 @@ func TestStripAnsi(t *testing.T) {
+	raw := "\x1b[31mcolored\x1b[0m"
 }
`
	res := ScanArchDiff(diff)
	if !res.Clean {
		t.Fatalf("expected ANSI escapes in test files to be permitted: %+v", res.Violations)
	}
}

func TestScanArchDiff_ContiguousSyntheticCredentialDetected(t *testing.T) {
	diff := "diff --git a/internal/gateway/auth_test.go b/internal/gateway/auth_test.go\n" +
		"index abcdef1..1234567 100644\n" +
		"--- a/internal/gateway/auth_test.go\n" +
		"+++ b/internal/gateway/auth_test.go\n" +
		"@@ -15,6 +15,7 @@ func TestVerifySignature(t *testing.T) {\n" +
		"+\tmockKey := \"" + "-----BEGIN " + "RSA PRIVATE KEY-----\\nMIIEowIBAAKCAQEA...\"\n" +
		" }\n"
	res := ScanArchDiff(diff)
	if res.Clean {
		t.Fatal("expected contiguous mock private key to be flagged in test file")
	}
	if len(res.Violations) != 1 {
		t.Fatalf("expected 1 violation, got %d", len(res.Violations))
	}
	v := res.Violations[0]
	if v.Rule != "contiguous-synthetic-credential" {
		t.Errorf("expected rule 'contiguous-synthetic-credential', got %q", v.Rule)
	}
	if !strings.Contains(v.Remedy, "split concatenations") {
		t.Errorf("expected remedy to suggest split concatenations: %s", v.Remedy)
	}
}

func TestScanArchDiff_SplitMockCredentialAllowed(t *testing.T) {
	diff := `diff --git a/internal/gateway/auth_test.go b/internal/gateway/auth_test.go
index abcdef1..1234567 100644
--- a/internal/gateway/auth_test.go
+++ b/internal/gateway/auth_test.go
@@ -15,6 +15,7 @@ func TestVerifySignature(t *testing.T) {
+	mockKey := "-----BEGIN " + "RSA PRIVATE KEY-----\n..."
 }
`
	res := ScanArchDiff(diff)
	if !res.Clean {
		t.Fatalf("expected split mock credential to pass cleanly: %+v", res.Violations)
	}
}
