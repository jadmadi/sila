package symbols

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseTargetSymbol(t *testing.T) {
	tests := []struct {
		input       string
		expectedPkg string
		expectedRec string
		expectedNam string
		expectedFil string
	}{
		{
			input:       "cli/internal/store::Open",
			expectedPkg: "cli/internal/store",
			expectedRec: "",
			expectedNam: "Open",
		},
		{
			input:       "cli/internal/store::Store.Close",
			expectedPkg: "cli/internal/store",
			expectedRec: "Store",
			expectedNam: "Close",
		},
		{
			input:       "Store.Close",
			expectedPkg: "",
			expectedRec: "Store",
			expectedNam: "Close",
		},
		{
			input:       "Open",
			expectedPkg: "",
			expectedRec: "",
			expectedNam: "Open",
		},
		{
			input:       "internal/store/store.go::Open",
			expectedPkg: "",
			expectedFil: "internal/store/store.go",
			expectedRec: "",
			expectedNam: "Open",
		},
	}

	for _, tc := range tests {
		pt := ParseTargetSymbol(tc.input)
		if pt.PkgFilter != tc.expectedPkg {
			t.Errorf("[%s] Pkg: got %q, want %q", tc.input, pt.PkgFilter, tc.expectedPkg)
		}
		if pt.Receiver != tc.expectedRec {
			t.Errorf("[%s] Receiver: got %q, want %q", tc.input, pt.Receiver, tc.expectedRec)
		}
		if pt.Name != tc.expectedNam {
			t.Errorf("[%s] Name: got %q, want %q", tc.input, pt.Name, tc.expectedNam)
		}
		if pt.File != tc.expectedFil {
			t.Errorf("[%s] File: got %q, want %q", tc.input, pt.File, tc.expectedFil)
		}
	}
}

func TestFindCallers_SyntheticRepo(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Create a dummy package with a target function and method
	pkgDir := filepath.Join(tmpDir, "pkg", "store")
	if err := os.MkdirAll(pkgDir, 0755); err != nil {
		t.Fatal(err)
	}

	storeSrc := `package store

type DB struct{}

func Open(path string) (*DB, error) {
	return &DB{}, nil
}

func (d *DB) Ping() error {
	return nil
}

func internalHelper() {
	Open("internal.db")
}
`
	if err := os.WriteFile(filepath.Join(pkgDir, "store.go"), []byte(storeSrc), 0644); err != nil {
		t.Fatal(err)
	}

	// 2. Create a consumer package calling store.Open and db.Ping
	mainDir := filepath.Join(tmpDir, "cmd", "app")
	if err := os.MkdirAll(mainDir, 0755); err != nil {
		t.Fatal(err)
	}

	mainSrc := `package main

import "pkg/store"

func main() {
	db, _ := store.Open("production.db")
	_ = db.Ping()
}

func run() {
	db2, _ := store.Open("test.db")
	_ = db2.Ping()
}
`
	if err := os.WriteFile(filepath.Join(mainDir, "main.go"), []byte(mainSrc), 0644); err != nil {
		t.Fatal(err)
	}

	// 3. Find callers of Open
	callers, err := FindCallers(tmpDir, "pkg/store::Open")
	if err != nil {
		t.Fatalf("FindCallers failed: %v", err)
	}

	// Expected: 1 internal call + 2 external calls in main.go = 3 calls total
	if len(callers) != 3 {
		t.Errorf("expected 3 callers for pkg/store::Open, got %d", len(callers))
		for i, c := range callers {
			t.Logf("  caller %d: %s (%s:%d)", i, c.Caller, c.File, c.Line)
		}
	}

	for _, c := range callers {
		if c.ArgsPassed != 1 {
			t.Errorf("expected ArgsPassed=1, got %d for %s", c.ArgsPassed, c.Caller)
		}
	}

	// 4. Find callers of DB.Ping
	pingCallers, err := FindCallers(tmpDir, "pkg/store::DB.Ping")
	if err != nil {
		t.Fatalf("FindCallers for Ping failed: %v", err)
	}

	if len(pingCallers) != 2 {
		t.Errorf("expected 2 callers for DB.Ping, got %d", len(pingCallers))
	}
	for _, c := range pingCallers {
		if c.ArgsPassed != 0 {
			t.Errorf("expected ArgsPassed=0 for Ping, got %d", c.ArgsPassed)
		}
	}

	// 5. Test safe-delete check: symbol with zero callers
	unusedCallers, err := FindCallers(tmpDir, "UnusedFunction")
	if err != nil {
		t.Fatalf("FindCallers for UnusedFunction failed: %v", err)
	}
	if len(unusedCallers) != 0 {
		t.Errorf("expected 0 callers for UnusedFunction, got %d", len(unusedCallers))
	}
}
