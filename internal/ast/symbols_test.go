package ast

import (
	"go/token"
	"os"
	"path/filepath"
	"testing"
)

const sampleSource = `package testpkg

type Config struct {
	Timeout int
}

type Engine interface {
	Start() error
}

func NewEngine(cfg Config) *MyEngine {
	return &MyEngine{}
}

type MyEngine struct{}

func (e *MyEngine) Start() error {
	return nil
}

func (e MyEngine) Stop() {}
`

func TestExtractSymbolsFromSource(t *testing.T) {
	fset := token.NewFileSet()
	syms, err := ExtractSymbolsFromSource(fset, "internal/sample/engine.go", []byte(sampleSource), "github.com/example/engine")
	if err != nil {
		t.Fatalf("ExtractSymbolsFromSource failed: %v", err)
	}

	expected := map[string]SymbolKind{
		"github.com/example/engine::Config":         KindType,
		"github.com/example/engine::Engine":         KindInterface,
		"github.com/example/engine::NewEngine":      KindFunc,
		"github.com/example/engine::MyEngine":       KindType,
		"github.com/example/engine::MyEngine.Start": KindMethod,
		"github.com/example/engine::MyEngine.Stop":  KindMethod,
	}

	if len(syms) != len(expected) {
		t.Fatalf("expected %d symbols, got %d", len(expected), len(syms))
	}

	found := make(map[string]SymbolKind)
	for _, s := range syms {
		found[s.CanonicalID] = s.SymbolKind
	}

	for id, kind := range expected {
		gotKind, ok := found[id]
		if !ok {
			t.Errorf("missing expected symbol %s", id)
		} else if gotKind != kind {
			t.Errorf("symbol %s: expected kind %s, got %s", id, kind, gotKind)
		}
	}
}

func TestMatchSymbolTarget(t *testing.T) {
	canonical := "github.com/jadmadi/sila/cli/internal/store::Open"

	tests := []struct {
		target string
		want   bool
	}{
		{"github.com/jadmadi/sila/cli/internal/store::Open", true},
		{"cli/internal/store::Open", true},
		{"internal/store::Open", true},
		{"store::Open", true},
		{"Open", true},
		{"store::Close", false},
		{"internal/gate::Open", false},
	}

	for _, tt := range tests {
		got := MatchSymbolTarget(canonical, tt.target)
		if got != tt.want {
			t.Errorf("MatchSymbolTarget(%q, %q) = %v, want %v", canonical, tt.target, got, tt.want)
		}
	}
}

func TestExtractSymbolsFromFileAndDir(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "sample.go")
	if err := os.WriteFile(filePath, []byte(sampleSource), 0644); err != nil {
		t.Fatalf("write sample file: %v", err)
	}

	syms, err := ExtractSymbolsFromFile(filePath, tmpDir)
	if err != nil {
		t.Fatalf("ExtractSymbolsFromFile failed: %v", err)
	}
	if len(syms) < 4 {
		t.Errorf("expected at least 4 symbols, got %d", len(syms))
	}

	dirSyms, err := ExtractSymbolsFromDir(tmpDir, tmpDir)
	if err != nil {
		t.Fatalf("ExtractSymbolsFromDir failed: %v", err)
	}
	if len(dirSyms) != len(syms) {
		t.Errorf("ExtractSymbolsFromDir count %d != file count %d", len(dirSyms), len(syms))
	}
}
