package gopls

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestConnector_Discovery(t *testing.T) {
	c := NewConnector()
	if c == nil {
		t.Fatal("NewConnector returned nil")
	}
	// HasGopls may be true or false depending on host PATH; should never panic or crash
	t.Logf("gopls discovery: hasGopls=%t, path=%q", c.HasGopls(), c.GoplsPath())
}

func TestConnector_FindSymbolReferences_NativeAST(t *testing.T) {
	c := NewConnector()
	ctx := context.Background()

	// Locate this test file or connector.go
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	connectorPath := filepath.Join(cwd, "connector.go")
	if _, err := os.Stat(connectorPath); err != nil {
		t.Skipf("connector.go not in cwd (%s), skipping", cwd)
	}

	// 1. Search for SymbolReferencesResult
	res, err := c.FindSymbolReferences(ctx, connectorPath, "SymbolReferencesResult", cwd)
	if err != nil {
		t.Fatalf("FindSymbolReferences failed: %v", err)
	}
	if res.Symbol != "SymbolReferencesResult" {
		t.Errorf("expected symbol SymbolReferencesResult, got %s", res.Symbol)
	}
	if len(res.References) == 0 {
		t.Errorf("expected at least 1 reference to SymbolReferencesResult, got 0")
	}

	foundDef := false
	for _, ref := range res.References {
		if ref.IsDefinition {
			foundDef = true
		}
		if ref.Line <= 0 {
			t.Errorf("invalid line number: %d", ref.Line)
		}
	}
	if !foundDef {
		t.Errorf("expected to find definition of SymbolReferencesResult")
	}

	// 2. Empty symbol error
	if _, err := c.FindSymbolReferences(ctx, connectorPath, "", cwd); err == nil {
		t.Errorf("expected error for empty symbol")
	}
}

func TestConnector_GetPackageAPI(t *testing.T) {
	c := NewConnector()
	ctx := context.Background()

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	res, err := c.GetPackageAPI(ctx, cwd, false)
	if err != nil {
		t.Fatalf("GetPackageAPI failed: %v", err)
	}

	if res.Package != "gopls" {
		t.Errorf("expected package name 'gopls', got %s", res.Package)
	}

	foundConnectorType := false
	for _, td := range res.Types {
		if td.Name == "Connector" {
			foundConnectorType = true
			break
		}
	}
	if !foundConnectorType {
		t.Errorf("expected to find Connector type in API documentation")
	}

	foundNewConnectorFunc := false
	for _, fd := range res.Functions {
		if fd.Name == "NewConnector" {
			foundNewConnectorFunc = true
			break
		}
	}
	if !foundNewConnectorFunc {
		t.Errorf("expected to find NewConnector function in API documentation")
	}

	// Non-existent directory
	if _, err := c.GetPackageAPI(ctx, filepath.Join(cwd, "non_existent_subdir_12345"), false); err == nil {
		t.Errorf("expected error for non-existent directory")
	}
}

func TestConnector_GetDiagnostics(t *testing.T) {
	c := NewConnector()
	ctx := context.Background()

	res, err := c.GetDiagnostics(ctx, ".", "")
	if err != nil {
		t.Fatalf("GetDiagnostics failed: %v", err)
	}

	if !res.Clean {
		t.Logf("diagnostics found in current package: %+v", res.Diagnostics)
	}
	if res.Provider == "" {
		t.Errorf("expected non-empty provider, got empty string")
	}
}
