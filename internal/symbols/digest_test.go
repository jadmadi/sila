package symbols

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildCodebaseMap(t *testing.T) {
	tempDir := t.TempDir()

	// 1. Create a dummy multi-language project
	// util/helper.go
	utilDir := filepath.Join(tempDir, "util")
	if err := os.MkdirAll(utilDir, 0o755); err != nil {
		t.Fatalf("mkdir util: %v", err)
	}
	helperGo := `package util

type StringHelper struct{}

func FormatString(s string) string { return s }
func unexportedHelper() {}
`
	if err := os.WriteFile(filepath.Join(utilDir, "helper.go"), []byte(helperGo), 0o644); err != nil {
		t.Fatalf("write helper.go: %v", err)
	}

	// services/auth.ts
	servicesDir := filepath.Join(tempDir, "services")
	if err := os.MkdirAll(servicesDir, 0o755); err != nil {
		t.Fatalf("mkdir services: %v", err)
	}
	authTS := `export interface User { id: string; }
export class AuthService {
  login() {}
}
export async function verifyToken(t: string): Promise<boolean> { return true; }
const secret = "hidden";
export { secret as publicSecret };
`
	if err := os.WriteFile(filepath.Join(servicesDir, "auth.ts"), []byte(authTS), 0o644); err != nil {
		t.Fatalf("write auth.ts: %v", err)
	}

	// scripts/worker.py
	scriptsDir := filepath.Join(tempDir, "scripts")
	if err := os.MkdirAll(scriptsDir, 0o755); err != nil {
		t.Fatalf("mkdir scripts: %v", err)
	}
	workerPy := `class BackgroundWorker:
    pass

def process_job(job_id):
    pass

def _private_runner():
    pass
`
	if err := os.WriteFile(filepath.Join(scriptsDir, "worker.py"), []byte(workerPy), 0o644); err != nil {
		t.Fatalf("write worker.py: %v", err)
	}

	// vendor/ignored.go (should be skipped)
	vendorDir := filepath.Join(tempDir, "vendor", "lib")
	if err := os.MkdirAll(vendorDir, 0o755); err != nil {
		t.Fatalf("mkdir vendor: %v", err)
	}
	if err := os.WriteFile(filepath.Join(vendorDir, "skip.go"), []byte("package lib\nfunc Skipped() {}\n"), 0o644); err != nil {
		t.Fatalf("write skip.go: %v", err)
	}

	// 2. Build the codebase map
	cbMap, err := BuildCodebaseMap(MapOptions{
		RootPath: tempDir,
		MaxChars: 2000,
	})
	if err != nil {
		t.Fatalf("BuildCodebaseMap error: %v", err)
	}

	if cbMap == nil {
		t.Fatalf("expected non-nil codebase map")
	}

	if cbMap.TotalDirs < 3 {
		t.Errorf("expected at least 3 directories, got %d", cbMap.TotalDirs)
	}

	// Verify formatted output
	out := cbMap.Formatted
	if !strings.Contains(out, "**Codebase Map") {
		t.Errorf("missing header in formatted output: %s", out)
	}
	if !strings.Contains(out, "FormatString") || !strings.Contains(out, "StringHelper") {
		t.Errorf("missing Go exported symbols in: %s", out)
	}
	if strings.Contains(out, "unexportedHelper") {
		t.Errorf("unexported helper should not appear in: %s", out)
	}
	if !strings.Contains(out, "AuthService") || !strings.Contains(out, "verifyToken") {
		t.Errorf("missing TypeScript exported symbols in: %s", out)
	}
	if !strings.Contains(out, "BackgroundWorker") || !strings.Contains(out, "process_job") {
		t.Errorf("missing Python exported symbols in: %s", out)
	}
	if strings.Contains(out, "_private_runner") {
		t.Errorf("private runner should not appear in: %s", out)
	}
	if strings.Contains(out, "vendor/") || strings.Contains(out, "Skipped") {
		t.Errorf("vendor files should be excluded from map: %s", out)
	}

	// Verify shared directories priority: util or services should come before scripts
	idxUtil := strings.Index(out, "util/")
	idxServices := strings.Index(out, "services/")
	idxScripts := strings.Index(out, "scripts/")
	if idxUtil == -1 || idxServices == -1 || idxScripts == -1 {
		t.Fatalf("missing directory markers in: %s", out)
	}
	if idxScripts < idxUtil || idxScripts < idxServices {
		t.Errorf("shared dirs (util, services) should appear before scripts: util=%d, services=%d, scripts=%d", idxUtil, idxServices, idxScripts)
	}
}

func TestBuildCodebaseMapBudgetOmission(t *testing.T) {
	tempDir := t.TempDir()

	for i := 0; i < 15; i++ {
		dir := filepath.Join(tempDir, "pkg"+stringFromInt(i))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		code := "package pkg\nfunc FunctionA" + stringFromInt(i) + "() {}\nfunc FunctionB" + stringFromInt(i) + "() {}\n"
		if err := os.WriteFile(filepath.Join(dir, "pkg.go"), []byte(code), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	// Very tight budget: only 250 characters
	cbMap, err := BuildCodebaseMap(MapOptions{
		RootPath: tempDir,
		MaxChars: 250,
	})
	if err != nil {
		t.Fatalf("BuildCodebaseMap error: %v", err)
	}

	if cbMap.OmittedDirs == 0 {
		t.Errorf("expected omitted directories with tight budget 250 chars, got 0 (total=%d, listed=%d)", cbMap.TotalDirs, cbMap.ListedDirs)
	}
	if !strings.Contains(cbMap.Formatted, "more directories not listed") {
		t.Errorf("expected omission notice in: %s", cbMap.Formatted)
	}
}
