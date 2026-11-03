package mcp

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMCPCleanTools_AuditAndClean(t *testing.T) {
	tempDir := t.TempDir()

	// Seed some dummy test artifacts in repo
	dummyCov := filepath.Join(tempDir, "coverage.out")
	if err := os.WriteFile(dummyCov, []byte("mode: set\nmain.go:1.1,1.10 1 1\n"), 0644); err != nil {
		t.Fatalf("failed to write dummy coverage: %v", err)
	}

	// 1. Test audit_hygiene tool call
	reqAudit := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"audit_hygiene","arguments":{"scope":"repo","project":` + jsonQuote(tempDir) + `}}}`
	var outAudit bytes.Buffer
	srv := NewServer(nil, strings.NewReader(reqAudit+"\n"), &outAudit)
	if err := srv.Run(); err != nil {
		t.Fatalf("srv.Run for audit_hygiene failed: %v", err)
	}

	var respAudit JSONRPCResponse
	if err := json.Unmarshal(outAudit.Bytes(), &respAudit); err != nil {
		t.Fatalf("unmarshal audit response: %v\nOutput: %s", err, outAudit.String())
	}
	if respAudit.Error != nil {
		t.Fatalf("audit_hygiene error: %v", respAudit.Error.Message)
	}

	auditData, ok := respAudit.Result.(map[string]any)["structuredData"].(map[string]any)
	if !ok {
		t.Fatalf("missing structuredData in audit response: %v", respAudit.Result)
	}
	if auditData["scope"] != "repo" {
		t.Errorf("expected scope 'repo', got %v", auditData["scope"])
	}

	// 2. Test clean_caches dry-run
	reqCleanDry := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"clean_caches","arguments":{"scope":"repo","dry_run":true,"project":` + jsonQuote(tempDir) + `}}}`
	var outCleanDry bytes.Buffer
	srvDry := NewServer(nil, strings.NewReader(reqCleanDry+"\n"), &outCleanDry)
	if err := srvDry.Run(); err != nil {
		t.Fatalf("srvDry.Run failed: %v", err)
	}

	var respCleanDry JSONRPCResponse
	if err := json.Unmarshal(outCleanDry.Bytes(), &respCleanDry); err != nil {
		t.Fatalf("unmarshal clean dry-run response: %v", err)
	}
	if respCleanDry.Error != nil {
		t.Fatalf("clean_caches dry-run error: %v", respCleanDry.Error.Message)
	}

	// Verify file still exists after dry-run
	if _, err := os.Stat(dummyCov); os.IsNotExist(err) {
		t.Errorf("expected dummy coverage.out to still exist after dry-run")
	}

	// 3. Test clean_caches execution
	reqCleanExec := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"clean_caches","arguments":{"scope":"repo","dry_run":false,"project":` + jsonQuote(tempDir) + `}}}`
	var outCleanExec bytes.Buffer
	srvExec := NewServer(nil, strings.NewReader(reqCleanExec+"\n"), &outCleanExec)
	if err := srvExec.Run(); err != nil {
		t.Fatalf("srvExec.Run failed: %v", err)
	}

	var respCleanExec JSONRPCResponse
	if err := json.Unmarshal(outCleanExec.Bytes(), &respCleanExec); err != nil {
		t.Fatalf("unmarshal clean exec response: %v", err)
	}
	if respCleanExec.Error != nil {
		t.Fatalf("clean_caches exec error: %v", respCleanExec.Error.Message)
	}

	// Verify dummy coverage file was cleaned
	if _, err := os.Stat(dummyCov); !os.IsNotExist(err) {
		t.Errorf("expected dummy coverage.out to be deleted after clean execution")
	}
}
