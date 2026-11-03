package ast

import (
	"bufio"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

// SymbolKind classifies the kind of Go AST symbol.
type SymbolKind string

const (
	KindFunc      SymbolKind = "func"
	KindMethod    SymbolKind = "method"
	KindType      SymbolKind = "type"
	KindInterface SymbolKind = "interface"
)

// SymbolInfo captures metadata and source span for an AST symbol.
type SymbolInfo struct {
	CanonicalID string     `json:"canonical_id"`
	PackagePath string     `json:"package_path"`
	PackageName string     `json:"package_name"`
	SymbolName  string     `json:"symbol_name"`
	SymbolKind  SymbolKind `json:"symbol_kind"`
	Receiver    string     `json:"receiver,omitempty"`
	FilePath    string     `json:"file_path"`
	StartLine   int        `json:"start_line"`
	EndLine     int        `json:"end_line"`
}

// ResolveModulePath looks for go.mod at rootDir or its parents to find the module name.
func ResolveModulePath(rootDir string) (string, string) {
	curr := rootDir
	for i := 0; i < 6; i++ {
		goModPath := filepath.Join(curr, "go.mod")
		if data, err := os.ReadFile(goModPath); err == nil {
			scanner := bufio.NewScanner(strings.NewReader(string(data)))
			for scanner.Scan() {
				line := strings.TrimSpace(scanner.Text())
				if strings.HasPrefix(line, "module ") {
					mod := strings.TrimSpace(strings.TrimPrefix(line, "module"))
					return mod, curr
				}
			}
		}
		parent := filepath.Dir(curr)
		if parent == curr {
			break
		}
		curr = parent
	}
	return "", rootDir
}

// ResolvePackagePath calculates the canonical Go import path for a file or directory.
func ResolvePackagePath(filePath, repoRoot string) string {
	absFile, err := filepath.Abs(filePath)
	if err != nil {
		absFile = filePath
	}
	dir := absFile
	fi, err := os.Stat(absFile)
	if err == nil && !fi.IsDir() {
		dir = filepath.Dir(absFile)
	}

	modPath, modRoot := ResolveModulePath(repoRoot)
	if modPath != "" && modRoot != "" {
		absModRoot, err := filepath.Abs(modRoot)
		if err == nil {
			if rel, err := filepath.Rel(absModRoot, dir); err == nil && !strings.HasPrefix(rel, "..") {
				if rel == "." {
					return modPath
				}
				return filepath.ToSlash(filepath.Join(modPath, rel))
			}
		}
	}

	if rel, err := filepath.Rel(repoRoot, dir); err == nil && !strings.HasPrefix(rel, "..") {
		if rel == "." {
			return filepath.Base(repoRoot)
		}
		return filepath.ToSlash(rel)
	}
	return filepath.Base(dir)
}

// BuildCanonicalID constructs the canonical symbol ID in the format:
//   - Functions: <pkg>::<FuncName>
//   - Methods: <pkg>::<Receiver>.<MethodName>
//   - Types/Interfaces: <pkg>::<TypeName>
func BuildCanonicalID(pkgPath, receiver, name string) string {
	cleanPkg := filepath.ToSlash(strings.TrimSpace(pkgPath))
	cleanRecv := strings.TrimSpace(strings.TrimPrefix(receiver, "*"))
	cleanName := strings.TrimSpace(name)

	if cleanRecv != "" {
		return fmt.Sprintf("%s::%s.%s", cleanPkg, cleanRecv, cleanName)
	}
	return fmt.Sprintf("%s::%s", cleanPkg, cleanName)
}

// ExtractSymbolsFromSource parses Go source code and extracts symbol declarations.
func ExtractSymbolsFromSource(fset *token.FileSet, relPath string, src []byte, pkgPath string) ([]SymbolInfo, error) {
	if fset == nil {
		fset = token.NewFileSet()
	}
	node, err := parser.ParseFile(fset, relPath, src, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parse Go file %s: %w", relPath, err)
	}

	pkgName := node.Name.Name
	var symbols []SymbolInfo

	for _, decl := range node.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			sym := SymbolInfo{
				PackagePath: pkgPath,
				PackageName: pkgName,
				SymbolName:  d.Name.Name,
				FilePath:    relPath,
				StartLine:   fset.Position(d.Pos()).Line,
				EndLine:     fset.Position(d.End()).Line,
			}
			if d.Recv != nil && len(d.Recv.List) > 0 {
				sym.SymbolKind = KindMethod
				recvType := exprToString(d.Recv.List[0].Type)
				cleanRecv := strings.TrimPrefix(recvType, "*")
				sym.Receiver = cleanRecv
				sym.CanonicalID = BuildCanonicalID(pkgPath, cleanRecv, d.Name.Name)
			} else {
				sym.SymbolKind = KindFunc
				sym.CanonicalID = BuildCanonicalID(pkgPath, "", d.Name.Name)
			}
			symbols = append(symbols, sym)

		case *ast.GenDecl:
			if d.Tok != token.TYPE {
				continue
			}
			for _, spec := range d.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				sym := SymbolInfo{
					PackagePath: pkgPath,
					PackageName: pkgName,
					SymbolName:  ts.Name.Name,
					FilePath:    relPath,
					StartLine:   fset.Position(ts.Pos()).Line,
					EndLine:     fset.Position(ts.End()).Line,
				}
				if _, isInterface := ts.Type.(*ast.InterfaceType); isInterface {
					sym.SymbolKind = KindInterface
				} else {
					sym.SymbolKind = KindType
				}
				sym.CanonicalID = BuildCanonicalID(pkgPath, "", ts.Name.Name)
				symbols = append(symbols, sym)
			}
		}
	}

	return symbols, nil
}

// ExtractSymbolsFromFile parses a single Go file from disk and returns its symbols.
func ExtractSymbolsFromFile(filePath, repoRoot string) ([]SymbolInfo, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("read file %s: %w", filePath, err)
	}
	relPath := filePath
	if repoRoot != "" {
		if r, err := filepath.Rel(repoRoot, filePath); err == nil && !strings.HasPrefix(r, "..") {
			relPath = r
		}
	}
	pkgPath := ResolvePackagePath(filePath, repoRoot)
	fset := token.NewFileSet()
	return ExtractSymbolsFromSource(fset, relPath, data, pkgPath)
}

// ExtractSymbolsFromDir recursively scans a directory for .go files and extracts symbols.
func ExtractSymbolsFromDir(dirPath, repoRoot string) ([]SymbolInfo, error) {
	var symbols []SymbolInfo
	err := filepath.Walk(dirPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			base := filepath.Base(path)
			if strings.HasPrefix(base, ".") || base == "vendor" || base == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fileSyms, err := ExtractSymbolsFromFile(path, repoRoot)
		if err != nil {
			return nil // skip unparseable files gracefully
		}
		symbols = append(symbols, fileSyms...)
		return nil
	})
	return symbols, err
}

// MatchSymbolTarget compares a canonical symbol ID against an agent-supplied target string.
// Supported targets:
//   - "github.com/jadmadi/sila/cli/internal/store::Open"
//   - "cli/internal/store::Open"
//   - "internal/store::Open"
//   - "store::Open"
//   - "Store.Open"
//   - "Open"
func MatchSymbolTarget(canonicalID, target string) bool {
	normCanon := filepath.ToSlash(strings.TrimSpace(canonicalID))
	normTarget := filepath.ToSlash(strings.TrimSpace(target))
	if normCanon == "" || normTarget == "" {
		return false
	}
	if normCanon == normTarget {
		return true
	}

	partsCanon := strings.Split(normCanon, "::")
	partsTarget := strings.Split(normTarget, "::")

	if len(partsTarget) == 2 && len(partsCanon) == 2 {
		canonPkg := partsCanon[0]
		canonSym := partsCanon[1]
		targetPkg := partsTarget[0]
		targetSym := partsTarget[1]

		if canonSym != targetSym {
			return false
		}
		if canonPkg == targetPkg || strings.HasSuffix(canonPkg, "/"+targetPkg) || strings.HasSuffix(targetPkg, "/"+canonPkg) {
			return true
		}
		return false
	}

	if len(partsTarget) == 1 && len(partsCanon) == 2 {
		canonSym := partsCanon[1]
		targetSym := partsTarget[0]
		if canonSym == targetSym {
			return true
		}
		if strings.HasSuffix(canonSym, "."+targetSym) {
			return true
		}
	}

	return strings.HasSuffix(normCanon, normTarget)
}

func exprToString(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return "*" + exprToString(t.X)
	case *ast.SelectorExpr:
		return exprToString(t.X) + "." + t.Sel.Name
	default:
		return ""
	}
}
