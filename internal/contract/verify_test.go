package contract

import (
	"os"
	"path/filepath"
	"testing"
)

func setupTestRepository(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	pkgDir := filepath.Join(dir, "internal", "store")
	if err := os.MkdirAll(pkgDir, 0755); err != nil {
		t.Fatal(err)
	}

	storeSrc := `package store

type Store struct{}

// Open opens a database connection.
func Open(path string) (*Store, error) {
	return &Store{}, nil
}

// DeprecatedHelper is an old function with callers.
func DeprecatedHelper() string {
	return "legacy"
}

// UnusedHelper is an old function with zero callers.
func UnusedHelper() int {
	return 42
}
`
	if err := os.WriteFile(filepath.Join(pkgDir, "store.go"), []byte(storeSrc), 0644); err != nil {
		t.Fatal(err)
	}

	cmdDir := filepath.Join(dir, "cmd", "app")
	if err := os.MkdirAll(cmdDir, 0755); err != nil {
		t.Fatal(err)
	}

	appSrc := `package main

import "internal/store"

func main() {
	st, _ := store.Open("local.db")
	_ = st
	_ = store.DeprecatedHelper()
}

func initStore() {
	st2, _ := store.Open("test.db")
	_ = st2
}
`
	if err := os.WriteFile(filepath.Join(cmdDir, "main.go"), []byte(appSrc), 0644); err != nil {
		t.Fatal(err)
	}

	return dir
}

func TestEditCheck_IncompatibleArityChange(t *testing.T) {
	repo := setupTestRepository(t)

	// Proposed edit: change Open(path string) to Open(path string, timeout int)
	candidatePayload := `package store

type Store struct{}

func Open(path string, timeout int) (*Store, error) {
	return &Store{}, nil
}

func DeprecatedHelper() string {
	return "legacy"
}

func UnusedHelper() int {
	return 42
}
`

	res, err := EditCheck(repo, "internal/store::Open", []byte(candidatePayload))
	if err != nil {
		t.Fatalf("EditCheck failed: %v", err)
	}

	if res.Status != "contract-change" {
		t.Errorf("expected status 'contract-change', got %q", res.Status)
	}
	if res.Contract.ParamsWas != 1 {
		t.Errorf("expected ParamsWas=1, got %d", res.Contract.ParamsWas)
	}
	if res.Contract.ParamsNow != 2 {
		t.Errorf("expected ParamsNow=2, got %d", res.Contract.ParamsNow)
	}
	if res.CallersCount != 2 {
		t.Errorf("expected 2 callers for Open, got %d", res.CallersCount)
	}
	if res.IncompatibleCount != 2 {
		t.Errorf("expected 2 incompatible callers, got %d", res.IncompatibleCount)
	}
	if res.ExitCode != 1 {
		t.Errorf("expected ExitCode=1 for incompatible edit, got %d", res.ExitCode)
	}
}

func TestEditCheck_CompatibleNoArityChange(t *testing.T) {
	repo := setupTestRepository(t)

	// Proposed edit: same params, internal implementation change
	candidatePayload := `package store

type Store struct{}

func Open(path string) (*Store, error) {
	// enhanced logic
	return &Store{}, nil
}

func DeprecatedHelper() string {
	return "legacy"
}

func UnusedHelper() int {
	return 42
}
`

	res, err := EditCheck(repo, "internal/store::Open", []byte(candidatePayload))
	if err != nil {
		t.Fatalf("EditCheck failed: %v", err)
	}

	if res.Status != "ok" {
		t.Errorf("expected status 'ok', got %q", res.Status)
	}
	if res.IncompatibleCount != 0 {
		t.Errorf("expected 0 incompatible callers, got %d", res.IncompatibleCount)
	}
	if res.ExitCode != 0 {
		t.Errorf("expected ExitCode=0, got %d", res.ExitCode)
	}
}

func TestEditCheck_SyntaxErrorInCandidate(t *testing.T) {
	repo := setupTestRepository(t)

	badPayload := `package store
func Open(path string { broken syntax
`

	res, err := EditCheck(repo, "internal/store::Open", []byte(badPayload))
	if err != nil {
		t.Fatalf("EditCheck failed: %v", err)
	}

	if res.Status != "error" {
		t.Errorf("expected status 'error', got %q", res.Status)
	}
	if res.ExitCode != 2 {
		t.Errorf("expected ExitCode=2, got %d", res.ExitCode)
	}
}

func TestVerifySafeDelete(t *testing.T) {
	repo := setupTestRepository(t)

	// 1. Symbol with active callers -> refused
	del1, err := VerifySafeDelete(repo, "internal/store::DeprecatedHelper")
	if err != nil {
		t.Fatalf("VerifySafeDelete failed: %v", err)
	}

	if del1.Status != "refused" {
		t.Errorf("expected status 'refused', got %q", del1.Status)
	}
	if del1.InboundReferences != 1 {
		t.Errorf("expected 1 inbound reference, got %d", del1.InboundReferences)
	}
	if del1.ExitCode != 1 {
		t.Errorf("expected ExitCode=1, got %d", del1.ExitCode)
	}

	// 2. Symbol with zero callers -> safe-to-delete
	del2, err := VerifySafeDelete(repo, "internal/store::UnusedHelper")
	if err != nil {
		t.Fatalf("VerifySafeDelete failed: %v", err)
	}

	if del2.Status != "safe-to-delete" {
		t.Errorf("expected status 'safe-to-delete', got %q", del2.Status)
	}
	if del2.InboundReferences != 0 {
		t.Errorf("expected 0 inbound references, got %d", del2.InboundReferences)
	}
	if del2.ExitCode != 0 {
		t.Errorf("expected ExitCode=0, got %d", del2.ExitCode)
	}
}
