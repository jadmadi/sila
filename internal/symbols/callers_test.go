package symbols

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

func TestFindCallersGoInterfaceDispatch(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte("module testmod\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var a strings.Builder
	a.WriteString(`package a

type Service interface{ Close() error }
type Pinger interface{ Ping() error }
type Closer interface{ Close() error }
type Runner interface{ run() }

type Server struct{}
func (s *Server) Close() error { return nil }
func (s *Server) Ping() error { return nil }

type Other struct{}
func (o *Other) Close() error { return nil }

type same struct{}
func (s *same) run() {}
`)
	for i := 1; i <= 11; i++ {
		fmt.Fprintf(&a, "\ntype Impl%d struct{}\nfunc (i *Impl%d) Close() error { return nil }\n", i, i)
	}
	aDir := filepath.Join(tmpDir, "pkg", "a")
	if err := os.MkdirAll(aDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(aDir, "a.go"), []byte(a.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	bDir := filepath.Join(tmpDir, "pkg", "b")
	if err := os.MkdirAll(bDir, 0o755); err != nil {
		t.Fatal(err)
	}
	bSrc := `package b

import "testmod/pkg/a"

type foreign struct{}
func (f *foreign) run() {}

func Use(p a.Pinger, srv *a.Server, other *a.Other, c a.Closer, r a.Runner) {
	p.Ping()
	srv.Close()
	other.Close()
	c.Close()
	r.run()
}
`
	if err := os.WriteFile(filepath.Join(bDir, "b.go"), []byte(bSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	callers, err := FindCallers(tmpDir, "testmod/pkg/a::Server.Close")
	if err != nil {
		t.Fatal(err)
	}
	if len(callers) != 1 || !callers[0].IsDirect {
		t.Fatalf("Server.Close callers = %+v, want one direct call", callers)
	}

	pingers, err := FindCallers(tmpDir, "testmod/pkg/a::Server.Ping")
	if err != nil {
		t.Fatal(err)
	}
	if len(pingers) != 1 || pingers[0].IsDirect {
		t.Fatalf("Server.Ping callers = %+v, want one interface dispatch", pingers)
	}

	closer, err := FindCallers(tmpDir, "testmod/pkg/a::Closer.Close")
	if err != nil {
		t.Fatal(err)
	}
	if len(closer) != 1 {
		t.Fatalf("Closer.Close callers = %d, want the direct interface call: %+v", len(closer), closer)
	}

	foreign, err := FindCallers(tmpDir, "testmod/pkg/b::foreign.run")
	if err != nil {
		t.Fatal(err)
	}
	if len(foreign) != 0 {
		t.Fatalf("unexported cross-package run should not satisfy a.Runner: %+v", foreign)
	}
}

func TestBuildCallEdgesFromParsed(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module testmod\n\ngo 1.26\n")
	write("pkg/a/a.go", `package a

type Service interface{ Close() error }
type Server struct{}
func (s *Server) Close() error { return nil }
func Use(s Service) { _ = s.Close() }
func Direct(s *Server) { _ = s.Close() }
`)

	fset, files, err := ScanRepositoryGoFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	byCaller := map[string]map[string]bool{}
	for _, edge := range BuildCallEdgesFromParsed(fset, files) {
		if byCaller[edge.Caller] == nil {
			byCaller[edge.Caller] = map[string]bool{}
		}
		byCaller[edge.Caller][edge.Callee] = true
	}
	callee := "testmod/pkg/a::Server.Close"
	if !byCaller["testmod/pkg/a::Direct"][callee] {
		t.Fatalf("direct call missing: %+v", byCaller)
	}
	if !byCaller["testmod/pkg/a::Use"][callee] {
		t.Fatalf("interface dispatch missing: %+v", byCaller)
	}
}
