package symbols

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	silaast "github.com/jadmadi/sila/internal/ast"
)

// CallSite represents an inbound invocation of an AST symbol.
type CallSite struct {
	Caller       string `json:"caller"`
	File         string `json:"file"`
	Line         int    `json:"line"`
	Col          int    `json:"col"`
	ArgsPassed   int    `json:"args_passed"`
	ArgsExpected int    `json:"args_expected,omitempty"`
	TargetSymbol string `json:"target_symbol"`
	TargetFile   string `json:"target_file,omitempty"`
	IsDirect     bool   `json:"is_direct"`
}

// ParsedTarget decomposes a target symbol selector.
// Formats:
//   - "cli/internal/store::Open" -> Pkg: "cli/internal/store", Receiver: "", Name: "Open"
//   - "cli/internal/store::Store.Close" -> Pkg: "cli/internal/store", Receiver: "Store", Name: "Close"
//   - "Store.Close" -> Pkg: "", Receiver: "Store", Name: "Close"
//   - "Open" -> Pkg: "", Receiver: "", Name: "Open"
//   - "internal/store/store.go::Open" -> File: "internal/store/store.go", Receiver: "", Name: "Open"
type ParsedTarget struct {
	PkgFilter string
	File      string
	Receiver  string
	Name      string
	Raw       string
}

// ParseTargetSymbol parses a target symbol string.
func ParseTargetSymbol(target string) ParsedTarget {
	raw := strings.TrimSpace(target)
	pt := ParsedTarget{Raw: raw}

	if idx := strings.Index(raw, "::"); idx != -1 {
		prefix := strings.TrimSpace(raw[:idx])
		rest := strings.TrimSpace(raw[idx+2:])

		if strings.HasSuffix(prefix, ".go") {
			pt.File = filepath.ToSlash(prefix)
		} else {
			pt.PkgFilter = filepath.ToSlash(prefix)
		}

		if dotIdx := strings.Index(rest, "."); dotIdx != -1 {
			pt.Receiver = strings.TrimSpace(rest[:dotIdx])
			pt.Name = strings.TrimSpace(rest[dotIdx+1:])
		} else {
			pt.Name = rest
		}
		return pt
	}

	if dotIdx := strings.Index(raw, "."); dotIdx != -1 {
		pt.Receiver = strings.TrimSpace(raw[:dotIdx])
		pt.Name = strings.TrimSpace(raw[dotIdx+1:])
		return pt
	}

	pt.Name = raw
	return pt
}

// MatchesSymbol evaluates whether a declared symbol matches the target specification.
func (pt ParsedTarget) MatchesSymbol(sym silaast.SymbolInfo) bool {
	if pt.Name != "" && sym.SymbolName != pt.Name {
		return false
	}
	if pt.Receiver != "" {
		cleanRecv := strings.TrimPrefix(sym.Receiver, "*")
		cleanTargetRecv := strings.TrimPrefix(pt.Receiver, "*")
		if cleanRecv != cleanTargetRecv {
			return false
		}
	}
	if pt.File != "" {
		normSymFile := filepath.ToSlash(sym.FilePath)
		normTargetFile := filepath.ToSlash(pt.File)
		if !strings.HasSuffix(normSymFile, normTargetFile) && !strings.HasSuffix(normTargetFile, normSymFile) {
			return false
		}
	}
	if pt.PkgFilter != "" {
		normPkgFilter := filepath.ToSlash(pt.PkgFilter)
		normSymPkg := filepath.ToSlash(sym.PackagePath)
		if !strings.Contains(normSymPkg, normPkgFilter) && !strings.HasSuffix(normSymPkg, normPkgFilter) {
			return false
		}
	}
	return true
}

// ParsedFile captures the AST and metadata for a single Go source file.
type ParsedFile struct {
	RelPath  string
	PkgPath  string
	PkgName  string
	FileNode *ast.File
	Imports  map[string]string // alias or pkg name -> full import path
	Symbols  []silaast.SymbolInfo
}

// ScanRepositoryGoFiles parses all active Go source files in repoRoot.
// Excludes .git, vendor, node_modules, rm, testdata, and dotfiles.
func ScanRepositoryGoFiles(repoRoot string) (*token.FileSet, []*ParsedFile, error) {
	fset := token.NewFileSet()
	var parsed []*ParsedFile

	err := filepath.WalkDir(repoRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if strings.HasPrefix(name, ".") || name == "vendor" || name == "node_modules" || name == "rm" || name == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			// Also include tests for caller analysis so test call sites are tracked
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}

		rel, err := filepath.Rel(repoRoot, path)
		if err != nil {
			rel = path
		}
		rel = filepath.ToSlash(rel)

		src, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		fileNode, err := parser.ParseFile(fset, rel, src, parser.ParseComments)
		if err != nil {
			// Skip unparseable files without aborting
			return nil
		}

		pkgPath := silaast.ResolvePackagePath(path, repoRoot)
		syms, _ := silaast.ExtractSymbolsFromSource(fset, rel, src, pkgPath)

		// Map imports
		imports := make(map[string]string)
		for _, imp := range fileNode.Imports {
			impPath := strings.Trim(imp.Path.Value, `"`)
			alias := ""
			if imp.Name != nil {
				alias = imp.Name.Name
			} else {
				alias = filepath.Base(impPath)
			}
			imports[alias] = impPath
		}

		parsed = append(parsed, &ParsedFile{
			RelPath:  rel,
			PkgPath:  pkgPath,
			PkgName:  fileNode.Name.Name,
			FileNode: fileNode,
			Imports:  imports,
			Symbols:  syms,
		})
		return nil
	})

	if err != nil {
		return nil, nil, fmt.Errorf("scan repo files in %s: %w", repoRoot, err)
	}
	return fset, parsed, nil
}

// FindCallers locates all call sites for targetSymbol across the repository.
func FindCallers(repoRoot, targetSymbol string) ([]CallSite, error) {
	fset, files, err := ScanRepositoryGoFiles(repoRoot)
	if err != nil {
		return nil, err
	}
	return FindCallersInParsedFiles(fset, files, targetSymbol), nil
}

// FindCallersInParsedFiles searches for invocations of targetSymbol in pre-parsed files.
func FindCallersInParsedFiles(fset *token.FileSet, files []*ParsedFile, targetSymbol string) []CallSite {
	pt := ParseTargetSymbol(targetSymbol)
	var callSites []CallSite

	// First, collect target symbols matching target specification
	var matchedSymbols []silaast.SymbolInfo
	for _, pf := range files {
		for _, sym := range pf.Symbols {
			if pt.MatchesSymbol(sym) {
				matchedSymbols = append(matchedSymbols, sym)
			}
		}
	}

	targetName := pt.Name
	targetRecv := pt.Receiver
	targetPkg := pt.PkgFilter

	for _, pf := range files {
		var currentCaller string

		ast.Inspect(pf.FileNode, func(n ast.Node) bool {
			if n == nil {
				return true
			}

			switch decl := n.(type) {
			case *ast.FuncDecl:
				pkgPrefix := pf.PkgPath
				if decl.Recv != nil && len(decl.Recv.List) > 0 {
					recvName := exprString(decl.Recv.List[0].Type)
					cleanRecv := strings.TrimPrefix(recvName, "*")
					currentCaller = fmt.Sprintf("%s::%s.%s", pkgPrefix, cleanRecv, decl.Name.Name)
				} else {
					currentCaller = fmt.Sprintf("%s::%s", pkgPrefix, decl.Name.Name)
				}
			}

			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}

			matched, isDirect := matchesCall(call, pf, targetName, targetRecv, targetPkg, matchedSymbols)
			if matched {
				pos := fset.Position(call.Pos())
				callerName := currentCaller
				if callerName == "" {
					callerName = fmt.Sprintf("%s::<init>", pf.PkgPath)
				}

				callSites = append(callSites, CallSite{
					Caller:       callerName,
					File:         pf.RelPath,
					Line:         pos.Line,
					Col:          pos.Column,
					ArgsPassed:   len(call.Args),
					TargetSymbol: pt.Raw,
					IsDirect:     isDirect,
				})
			}
			return true
		})
	}

	return callSites
}

// matchesCall determines if a CallExpr calls the specified target symbol.
func matchesCall(
	call *ast.CallExpr,
	pf *ParsedFile,
	targetName string,
	targetRecv string,
	targetPkg string,
	matchedSyms []silaast.SymbolInfo,
) (bool, bool) {
	if targetName == "" {
		return false, false
	}

	switch fun := call.Fun.(type) {
	case *ast.Ident:
		// Direct call: fun.Name(...)
		if fun.Name != targetName {
			return false, false
		}
		// Direct calls must be in the same package as declaration or target receiver must be empty
		if targetRecv != "" {
			return false, false
		}
		if targetPkg != "" {
			normTargetPkg := filepath.ToSlash(targetPkg)
			normPfPkg := filepath.ToSlash(pf.PkgPath)
			if !strings.Contains(normPfPkg, normTargetPkg) && !strings.HasSuffix(normPfPkg, normTargetPkg) {
				return false, false
			}
		}
		return true, true

	case *ast.SelectorExpr:
		// Selector call: X.Name(...)
		if fun.Sel.Name != targetName {
			return false, false
		}

		xIdent, isIdent := fun.X.(*ast.Ident)
		if isIdent {
			// Case 1: X is an imported package alias (e.g. store.Open)
			if impPath, ok := pf.Imports[xIdent.Name]; ok {
				if targetPkg != "" {
					normTargetPkg := filepath.ToSlash(targetPkg)
					normImpPath := filepath.ToSlash(impPath)
					if !strings.Contains(normImpPath, normTargetPkg) && !strings.HasSuffix(normImpPath, normTargetPkg) {
						return false, false
					}
				}
				if targetRecv != "" {
					return false, false
				}
				return true, true
			}

			// Case 2: X is a variable / receiver in a method call (e.g. s.Close())
			if targetRecv != "" {
				// Method call matching target receiver
				return true, false
			}
		}

		// Fallback: If method target has receiver, selector expression matches name
		if targetRecv != "" {
			return true, false
		}

		// If matched symbols show this is a method on some type and selector matches
		for _, sym := range matchedSyms {
			if sym.SymbolKind == silaast.KindMethod && sym.SymbolName == targetName {
				return true, false
			}
		}
	}

	return false, false
}

// exprString turns an AST expression into a compact string representation.
func exprString(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.StarExpr:
		return "*" + exprString(e.X)
	case *ast.SelectorExpr:
		return exprString(e.X) + "." + e.Sel.Name
	default:
		return ""
	}
}
