package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func TestAuditUICLI_ExecutionAndReportPersistence(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "sila.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	st.Close()

	// Create dummy component HTML file
	htmlFile := filepath.Join(tempDir, "ButtonCard.html")
	htmlContent := `
<div class="card mt-[13px] border-black">
  <h1 class="text-[17px]">Select Subscription</h1>
  <button class="bg-blue-600 btn-primary">Monthly</button>
  <button class="bg-blue-600 btn-primary">Annual</button>
  <div class="empty-state-raw">No invoices found</div>
</div>
`
	if err := os.WriteFile(htmlFile, []byte(htmlContent), 0644); err != nil {
		t.Fatal(err)
	}

	ui := render.New(os.Stdout, true)

	// Test 1: Human-readable terminal output
	out := captureStdout(t, func() {
		runAudit(ui, dbPath, false, false, auditOptions{}, []string{"ui", htmlFile})
	})

	if !strings.Contains(out, "autonomous groupthink audit") {
		t.Errorf("expected audit header, got: %s", out)
	}
	if !strings.Contains(out, "persisted to .sila/reports/") {
		t.Errorf("expected reports persisted notice, got: %s", out)
	}
	if !strings.Contains(out, "Synthesized Consensus Proposals") {
		t.Errorf("expected synthesized consensus proposals, got: %s", out)
	}

	// Test 2: JSON output
	jsonOut := captureStdout(t, func() {
		runAudit(ui, dbPath, true, false, auditOptions{}, []string{"ui", htmlFile})
	})

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(jsonOut), &parsed); err != nil {
		t.Fatalf("failed to parse json output: %v\nOutput: %s", err, jsonOut)
	}
	if _, ok := parsed["reports"]; !ok {
		t.Errorf("expected 'reports' in json output: %s", jsonOut)
	}
	if _, ok := parsed["proposals"]; !ok {
		t.Errorf("expected 'proposals' in json output: %s", jsonOut)
	}

	// Test 3: TOON output
	toonOut := captureStdout(t, func() {
		runAudit(ui, dbPath, false, true, auditOptions{}, []string{"ui", htmlFile})
	})

	if !strings.Contains(toonOut, "audit_session{target,squad_size,proposals_count,gaps_count,reports_count}:") {
		t.Errorf("expected TOON audit_session header, got: %s", toonOut)
	}
	if !strings.Contains(toonOut, "proposals[") {
		t.Errorf("expected TOON proposals list, got: %s", toonOut)
	}
}
