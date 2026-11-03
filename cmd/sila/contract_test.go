package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/contract"
	"github.com/jadmadi/sila/internal/render"
)

func setupContractCLIRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	pkgDir := filepath.Join(dir, "pkg", "service")
	if err := os.MkdirAll(pkgDir, 0755); err != nil {
		t.Fatal(err)
	}

	svcSrc := `package service

type Server struct{}

func Start(port int) (*Server, error) {
	return &Server{}, nil
}

func DeadCode() {
}
`
	if err := os.WriteFile(filepath.Join(pkgDir, "service.go"), []byte(svcSrc), 0644); err != nil {
		t.Fatal(err)
	}

	cmdDir := filepath.Join(dir, "cmd", "server")
	if err := os.MkdirAll(cmdDir, 0755); err != nil {
		t.Fatal(err)
	}

	cmdSrc := `package main

import "pkg/service"

func main() {
	_, _ = service.Start(8080)
}
`
	if err := os.WriteFile(filepath.Join(cmdDir, "main.go"), []byte(cmdSrc), 0644); err != nil {
		t.Fatal(err)
	}

	return dir
}

func TestCLI_EditCheck_Direct(t *testing.T) {
	repo := setupContractCLIRepo(t)
	oldWd, _ := os.Getwd()
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(oldWd) }()

	// 1. Incompatible payload: Start(port int, host string)
	candidatePayload := `package service

type Server struct{}

func Start(port int, host string) (*Server, error) {
	return &Server{}, nil
}

func DeadCode() {}
`
	payloadFile := filepath.Join(repo, "candidate.go")
	if err := os.WriteFile(payloadFile, []byte(candidatePayload), 0644); err != nil {
		t.Fatal(err)
	}

	ui := render.New(os.Stdout, true)

	// Direct test on contract.EditCheck
	res, err := contract.EditCheck(repo, "pkg/service::Start", []byte(candidatePayload))
	if err != nil {
		t.Fatalf("EditCheck failed: %v", err)
	}

	if res.ExitCode != 1 {
		t.Errorf("expected ExitCode=1, got %d", res.ExitCode)
	}
	if res.IncompatibleCount != 1 {
		t.Errorf("expected 1 incompatible caller, got %d", res.IncompatibleCount)
	}

	// Test reporting functions do not panic
	printEditCheckReport(ui, res, true)

	// 2. Safe-delete on active symbol
	delRes, err := contract.VerifySafeDelete(repo, "pkg/service::Start")
	if err != nil {
		t.Fatalf("VerifySafeDelete failed: %v", err)
	}
	if delRes.Status != "refused" {
		t.Errorf("expected status 'refused', got %q", delRes.Status)
	}
	if delRes.InboundReferences != 1 {
		t.Errorf("expected 1 inbound reference, got %d", delRes.InboundReferences)
	}
	printSafeDeleteReport(ui, delRes, true)

	// 3. Safe-delete on unused symbol
	delDead, err := contract.VerifySafeDelete(repo, "pkg/service::DeadCode")
	if err != nil {
		t.Fatalf("VerifySafeDelete for DeadCode failed: %v", err)
	}
	if delDead.Status != "safe-to-delete" {
		t.Errorf("expected status 'safe-to-delete', got %q", delDead.Status)
	}
	if delDead.InboundReferences != 0 {
		t.Errorf("expected 0 inbound references, got %d", delDead.InboundReferences)
	}
	printSafeDeleteReport(ui, delDead, true)
}

func TestCLI_EditCheck_JSONOutput(t *testing.T) {
	repo := setupContractCLIRepo(t)

	res, err := contract.VerifySafeDelete(repo, "pkg/service::DeadCode")
	if err != nil {
		t.Fatal(err)
	}

	data, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("json marshal failed: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("json unmarshal failed: %v", err)
	}

	if parsed["status"] != "safe-to-delete" {
		t.Errorf("expected status 'safe-to-delete', got %v", parsed["status"])
	}
	if parsed["exit_code"] != float64(0) {
		t.Errorf("expected exit_code 0, got %v", parsed["exit_code"])
	}
}

func TestGateContractSentry_BreakingSignature(t *testing.T) {
	repo := setupContractCLIRepo(t)

	// Initialize git repository
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\nOutput: %s", args, err, string(out))
		}
	}

	runGit("init")
	runGit("config", "user.name", "Test Agent")
	runGit("config", "user.email", "test@example.com")
	runGit("add", ".")
	runGit("commit", "-m", "initial commit")

	// Verify gateContractSentry passes before any changes
	checkClean := gateContractSentry(repo)
	if checkClean.Status != "pass" {
		t.Fatalf("expected pass on clean repo, got %s: %s", checkClean.Status, checkClean.Detail)
	}

	// Modify Start signature: port int -> port int, host string
	breakingSrc := `package service

type Server struct{}

func Start(port int, host string) (*Server, error) {
	return &Server{}, nil
}

func DeadCode() {}
`
	if err := os.WriteFile(filepath.Join(repo, "pkg", "service", "service.go"), []byte(breakingSrc), 0644); err != nil {
		t.Fatal(err)
	}

	// Stage the breaking change
	runGit("add", "pkg/service/service.go")

	// Sentry must detect breaking contract change and return fail
	checkBreaking := gateContractSentry(repo)
	if checkBreaking.Status != "fail" {
		t.Fatalf("expected fail on breaking contract change, got %s: %s", checkBreaking.Status, checkBreaking.Detail)
	}
	if !strings.Contains(checkBreaking.Detail, "breaking contract change on symbol \"Start\"") {
		t.Errorf("unexpected detail: %s", checkBreaking.Detail)
	}
}
