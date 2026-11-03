package goals

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCreateVerificationContract(t *testing.T) {
	dir := t.TempDir()
	slug := "test-contract-goal"
	goalDir := filepath.Join(dir, "goals", slug)
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	testFile := filepath.Join(goalDir, "logic_test.go")
	if err := os.WriteFile(testFile, []byte("package test\n// test assertion\n"), 0644); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	c, err := CreateVerificationContract(dir, "go test ./goals/"+slug+"/...", 120, 4, 0, []string{testFile})
	if err != nil {
		t.Fatalf("CreateVerificationContract failed: %v", err)
	}
	if c.Command != "go test ./goals/"+slug+"/..." {
		t.Errorf("unexpected command: %s", c.Command)
	}
	if c.TimeoutSeconds != 120 {
		t.Errorf("unexpected timeout: %d", c.TimeoutSeconds)
	}
	if c.MaxRounds != 4 {
		t.Errorf("unexpected max rounds: %d", c.MaxRounds)
	}
	if c.ContractHash == "" {
		t.Errorf("contract hash cannot be empty")
	}

	// Verify integrity passes on unmodified files
	if err := VerifyContractIntegrity(dir, c); err != nil {
		t.Fatalf("VerifyContractIntegrity failed on valid contract: %v", err)
	}
}

func TestVerifyContractIntegrity_TamperCommand(t *testing.T) {
	dir := t.TempDir()
	c, err := CreateVerificationContract(dir, "go test -race ./...", 60, 5, 0, nil)
	if err != nil {
		t.Fatalf("create contract: %v", err)
	}

	// Tamper command to weaken testing
	c.Command = "true"
	err = VerifyContractIntegrity(dir, c)
	if err == nil {
		t.Fatalf("expected VerifyContractIntegrity to fail after command tampering")
	}
}

func TestVerifyContractIntegrity_TamperTestFile(t *testing.T) {
	dir := t.TempDir()
	slug := "tamper-goal"
	goalDir := filepath.Join(dir, "goals", slug)
	_ = os.MkdirAll(goalDir, 0755)

	testPath := filepath.Join(goalDir, "unit_test.go")
	_ = os.WriteFile(testPath, []byte("func TestAssert(t *testing.T) { t.Fatal() }"), 0644)

	c, err := CreateVerificationContract(dir, "go test ./...", 60, 5, 0, []string{testPath})
	if err != nil {
		t.Fatalf("create contract: %v", err)
	}

	// Tamper test file: remove assertion to fake passing
	_ = os.WriteFile(testPath, []byte("func TestAssert(t *testing.T) { /* deleted assertion */ }"), 0644)

	err = VerifyContractIntegrity(dir, c)
	if err == nil {
		t.Fatalf("expected VerifyContractIntegrity to fail when test file content was modified")
	}
}

func TestSaveAndLoadContract(t *testing.T) {
	dir := t.TempDir()
	slug := "persistence-goal"
	goalDir := filepath.Join(dir, "goals", slug)
	_ = os.MkdirAll(goalDir, 0755)

	c, err := CreateVerificationContract(dir, "go test -v ./...", 90, 3, 0, nil)
	if err != nil {
		t.Fatalf("create contract: %v", err)
	}

	if err := SaveContract(dir, slug, c); err != nil {
		t.Fatalf("SaveContract failed: %v", err)
	}

	loaded, err := LoadContract(dir, slug)
	if err != nil {
		t.Fatalf("LoadContract failed: %v", err)
	}
	if loaded.Command != c.Command {
		t.Errorf("loaded command mismatch: %s vs %s", loaded.Command, c.Command)
	}
	if loaded.ContractHash != c.ContractHash {
		t.Errorf("loaded hash mismatch: %s vs %s", loaded.ContractHash, c.ContractHash)
	}
}

func TestParseEmbeddedContract(t *testing.T) {
	goalMd := `---
title: Embedded Contract Test
verification_command: "go test ./internal/store/..."
contract_hash: "a1b2c3d4"
contract_timeout: 45
max_rounds: 3
---
# Goal description
`
	c, err := parseEmbeddedContract(goalMd)
	if err != nil {
		t.Fatalf("parseEmbeddedContract failed: %v", err)
	}
	if c.Command != "go test ./internal/store/..." {
		t.Errorf("unexpected command: %s", c.Command)
	}
	if c.ContractHash != "a1b2c3d4" {
		t.Errorf("unexpected hash: %s", c.ContractHash)
	}
	if c.TimeoutSeconds != 45 {
		t.Errorf("unexpected timeout: %d", c.TimeoutSeconds)
	}
	if c.MaxRounds != 3 {
		t.Errorf("unexpected max rounds: %d", c.MaxRounds)
	}
}
