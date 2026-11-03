package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/assess"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func TestMysteryAuditCLI_GoalsAudit(t *testing.T) {
	if _, err := exec.LookPath("sila"); err != nil {
		t.Skip("sila binary not on PATH; mystery audit CLI test requires an installed binary")
	}
	if !assess.IsBubblewrapAvailable() {
		t.Skip("bubblewrap not available; PASS receipts require blindfold verification")
	}
	tempDir := t.TempDir()
	slug := "test-mystery-cli-goal"
	goalDir := filepath.Join(tempDir, "goals", slug)
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		t.Fatal(err)
	}

	goalContent := `# Goal: Test Mystery CLI Goal

## Chain of Custody (Isnad)
- **Registered By**: @test | Session: s1 | Date: 2026-10-03
- **Spec Audited By**: @test | Session: s1 | Date: 2026-10-03
- **Implemented By**: @test | Session: s1 | Date: 2026-10-03

## Goal Description
Provide a public ping endpoint for health checks.

## Target Surface & Contracts
- **CLI Commands**: echo "healthy"

## Strict Acceptance Criteria (Non-Negotiable)
1. CLI ping returns healthy
2. Exit code is 0
3. Response is fast
4. Zero errors
5. Proper headers
6. Clean output
`
	if err := os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte(goalContent), 0644); err != nil {
		t.Fatal(err)
	}

	dbPath := filepath.Join(tempDir, "sila.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	st.Close()

	ui := render.New(os.Stdout, true)

	// Test 1: sila goals audit --json
	outJSON := captureStdout(t, func() {
		runGoals(ui, dbPath, tempDir, "antigravity", "", "", "", false, false, "", "", "", "", "", 0, false, false, false, false, false, false, false, false, true, false, false, false, false, "", "", "", false, []string{"audit", slug, "--skip-canary"})
	})

	var eval assess.AuditEvaluation
	if err := json.Unmarshal([]byte(outJSON), &eval); err != nil {
		t.Fatalf("failed to parse json output: %v, raw: %s", err, outJSON)
	}

	if eval.Verdict != "PASS" {
		t.Fatalf("expected PASS verdict, got %s (reason: %s)", eval.Verdict, eval.CCDSI.FailureReason)
	}
	if eval.CCDSI.CCDSIScore < 0.40 {
		t.Fatalf("expected CCDSI >= 0.40, got %f", eval.CCDSI.CCDSIScore)
	}
	if !eval.BlindfoldVerified {
		t.Fatalf("expected blindfold verification to pass")
	}

	// Verify receipt was written
	receiptPath := filepath.Join(goalDir, "audit-receipt.json")
	if _, err := os.Stat(receiptPath); err != nil {
		t.Fatalf("expected audit-receipt.json to exist: %v", err)
	}
}

func TestMysteryAuditCLI_HireAuditGoal(t *testing.T) {
	if !assess.IsBubblewrapAvailable() {
		t.Skip("bubblewrap not available; PASS receipts require blindfold verification")
	}
	tempDir := t.TempDir()
	slug := "test-hire-audit-goal"
	goalDir := filepath.Join(tempDir, "goals", slug)
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		t.Fatal(err)
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/api/mcp") {
			auth := r.Header.Get("Authorization")
			if auth == "" {
				w.WriteHeader(http.StatusUnauthorized)
				w.Write([]byte(`{"error":"unauthorized"}`))
				return
			}
		}
		if strings.HasSuffix(r.URL.Path, "/api/drain") {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				w.Write([]byte(`{"error":"bad_json"}`))
				return
			}
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"drained":true}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer ts.Close()

	goalContent := `# Goal: Test Hire Audit Goal

## Chain of Custody (Isnad)
- **Registered By**: @test | Session: s1 | Date: 2026-10-03
- **Spec Audited By**: @test | Session: s1 | Date: 2026-10-03
- **Implemented By**: @test | Session: s1 | Date: 2026-10-03

## Goal Description
HTTP service health endpoint.

## Target Surface & Contracts
- **Web / Edge Endpoints**: ` + ts.URL + `

## Strict Acceptance Criteria (Non-Negotiable)
1. Returns HTTP 200
2. Valid JSON payload
3. Status is ok
4. Fast response
5. Proper content type
6. Production ready
`
	if err := os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte(goalContent), 0644); err != nil {
		t.Fatal(err)
	}

	dbPath := filepath.Join(tempDir, "sila.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	ui := render.New(os.Stdout, true)

	// Test 2: sila hire audit-goal <slug> --url=<url> --skip-canary
	out := captureStdout(t, func() {
		runHire(ui, dbPath, false, false, auditOptions{}, []string{"audit-goal", slug, "--url=" + ts.URL, "--project=" + tempDir, "--skip-canary"})
	})

	if !strings.Contains(out, "passed epistemic mystery audit") {
		t.Fatalf("expected audit pass output, got: %s", out)
	}
	if !strings.Contains(out, "CCDSI Score:") {
		t.Fatalf("expected CCDSI Score line, got: %s", out)
	}
}
