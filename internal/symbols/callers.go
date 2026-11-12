package symbols

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
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
		syms := silaast.ExtractSymbolsFromNode(fset, rel, fileNode, pkgPath)

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
	idx := buildSymbolTypeIndex(files)
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
		var currentFunc *ast.FuncDecl

		ast.Inspect(pf.FileNode, func(n ast.Node) bool {
			if n == nil {
				return true
			}

			switch decl := n.(type) {
			case *ast.FuncDecl:
				currentFunc = decl
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

			matched, isDirect := matchesCall(call, pf, targetName, targetRecv, targetPkg, matchedSymbols, idx, currentFunc)
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
	idx *symbolTypeIndex,
	fn *ast.FuncDecl,
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

		// Method-call dispatch: resolve the receiver's local static type.
		if targetRecv != "" {
			if key, ok := resolveReceiverType(fun.X, pf, fn, idx); ok {
				return dispatchMatches(idx, key, targetName, targetRecv, targetPkg)
			}
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

			// Case 2: unresolved variable/receiver keeps the historical match.
			if targetRecv != "" {
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

const maxImplicitImplementers = 10

type symbolTypeKey struct{ pkg, name string }

type symbolInterface struct {
	methods []string
	embeds  []string
}

type symbolTypeIndex struct {
	methods    map[symbolTypeKey]map[string]bool
	concrete   map[symbolTypeKey]bool
	interfaces map[symbolTypeKey]symbolInterface
	fields     map[symbolTypeKey]map[string]string
}

func buildSymbolTypeIndex(files []*ParsedFile) *symbolTypeIndex {
	idx := &symbolTypeIndex{
		methods:    map[symbolTypeKey]map[string]bool{},
		concrete:   map[symbolTypeKey]bool{},
		interfaces: map[symbolTypeKey]symbolInterface{},
		fields:     map[symbolTypeKey]map[string]string{},
	}
	for _, pf := range files {
		for _, sym := range pf.Symbols {
			key := symbolTypeKey{pkg: sym.PackagePath, name: sym.SymbolName}
			switch sym.SymbolKind {
			case silaast.KindMethod:
				methodKey := symbolTypeKey{pkg: sym.PackagePath, name: sym.Receiver}
				if idx.methods[methodKey] == nil {
					idx.methods[methodKey] = map[string]bool{}
				}
				idx.methods[methodKey][sym.SymbolName] = true
			case silaast.KindType:
				idx.concrete[key] = true
			case silaast.KindInterface:
				idx.interfaces[key] = symbolInterface{methods: sym.InterfaceMethods, embeds: sym.EmbeddedInterfaces}
			}
		}
		ast.Inspect(pf.FileNode, func(n ast.Node) bool {
			ts, ok := n.(*ast.TypeSpec)
			if !ok {
				return true
			}
			structType, ok := ts.Type.(*ast.StructType)
			if !ok {
				return true
			}
			fields := map[string]string{}
			for _, field := range structType.Fields.List {
				for _, name := range field.Names {
					fields[name.Name] = normalizeTypeName(exprString(field.Type))
				}
			}
			idx.fields[symbolTypeKey{pkg: pf.PkgPath, name: ts.Name.Name}] = fields
			return true
		})
	}
	return idx
}

func normalizeTypeName(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "*")
	raw = strings.TrimPrefix(raw, "[]")
	if i := strings.IndexByte(raw, '['); i >= 0 {
		raw = raw[:i]
	}
	if i := strings.IndexByte(raw, ' '); i >= 0 {
		raw = raw[:i]
	}
	return raw
}

func (idx *symbolTypeIndex) findByName(name, pkg string) (symbolTypeKey, bool) {
	name = normalizeTypeName(name)
	if dot := strings.LastIndex(name, "."); dot >= 0 {
		name = name[dot+1:]
	}
	if pkg != "" {
		key := symbolTypeKey{pkg: pkg, name: name}
		if idx.concrete[key] {
			return key, true
		}
		if _, ok := idx.interfaces[key]; ok {
			return key, true
		}
		for cand := range idx.concrete {
			if cand.name == name && pkgMatches(cand.pkg, pkg) {
				return cand, true
			}
		}
		for cand := range idx.interfaces {
			if cand.name == name && pkgMatches(cand.pkg, pkg) {
				return cand, true
			}
		}
	}
	for cand := range idx.concrete {
		if cand.name == name {
			return cand, true
		}
	}
	for cand := range idx.interfaces {
		if cand.name == name {
			return cand, true
		}
	}
	return symbolTypeKey{}, false
}

func (idx *symbolTypeIndex) ifaceMethods(key symbolTypeKey, visited map[symbolTypeKey]bool) map[string]bool {
	if visited[key] {
		return nil
	}
	visited[key] = true
	out := map[string]bool{}
	info, ok := idx.interfaces[key]
	if !ok {
		return out
	}
	for _, method := range info.methods {
		out[method] = true
	}
	for _, embedded := range info.embeds {
		name := normalizeTypeName(embedded)
		if dot := strings.LastIndex(name, "."); dot >= 0 {
			name = name[dot+1:]
		}
		for cand := range idx.interfaces {
			if cand.name != name {
				continue
			}
			for method := range idx.ifaceMethods(cand, visited) {
				out[method] = true
			}
		}
	}
	return out
}

func (idx *symbolTypeIndex) implements(concrete, iface symbolTypeKey) bool {
	required := idx.ifaceMethods(iface, map[symbolTypeKey]bool{})
	if len(required) == 0 {
		return false
	}
	methods := idx.methods[concrete]
	for method := range required {
		if !methods[method] {
			return false
		}
		if !ast.IsExported(method) && concrete.pkg != iface.pkg {
			return false
		}
	}
	return true
}

func (idx *symbolTypeIndex) implementerCount(iface symbolTypeKey) int {
	count := 0
	for key := range idx.concrete {
		if idx.implements(key, iface) {
			count++
		}
	}
	return count
}

func pkgMatches(got, want string) bool {
	if want == "" || got == "" {
		return true
	}
	g, w := filepath.ToSlash(got), filepath.ToSlash(want)
	return g == w || strings.HasSuffix(g, "/"+w) || strings.HasSuffix(w, "/"+g)
}

func resolveReceiverType(expr ast.Expr, pf *ParsedFile, fn *ast.FuncDecl, idx *symbolTypeIndex) (symbolTypeKey, bool) {
	switch e := expr.(type) {
	case *ast.Ident:
		raw, ok := lookupIdentType(e.Name, fn)
		if !ok {
			return symbolTypeKey{}, false
		}
		return rawTypeKey(raw, pf)
	case *ast.SelectorExpr:
		base, ok := resolveReceiverType(e.X, pf, fn, idx)
		if !ok {
			return symbolTypeKey{}, false
		}
		fieldType, ok := idx.fields[base][e.Sel.Name]
		if !ok {
			return symbolTypeKey{}, false
		}
		return rawTypeKey(fieldType, pf)
	}
	return symbolTypeKey{}, false
}

func rawTypeKey(raw string, pf *ParsedFile) (symbolTypeKey, bool) {
	raw = normalizeTypeName(raw)
	if raw == "" || raw == "interface{}" || strings.HasPrefix(raw, "func(") {
		return symbolTypeKey{}, false
	}
	if dot := strings.IndexByte(raw, '.'); dot >= 0 {
		alias, name := raw[:dot], raw[dot+1:]
		if imp, ok := pf.Imports[alias]; ok {
			return symbolTypeKey{pkg: imp, name: name}, true
		}
		return symbolTypeKey{name: name}, true
	}
	return symbolTypeKey{pkg: pf.PkgPath, name: raw}, true
}

func lookupIdentType(name string, fn *ast.FuncDecl) (string, bool) {
	if fn == nil {
		return "", false
	}
	if fn.Recv != nil {
		for _, field := range fn.Recv.List {
			for _, ident := range field.Names {
				if ident.Name == name {
					return exprString(field.Type), true
				}
			}
		}
	}
	for _, list := range []*ast.FieldList{fn.Type.Params, fn.Type.Results} {
		if list == nil {
			continue
		}
		for _, field := range list.List {
			for _, ident := range field.Names {
				if ident.Name == name {
					return exprString(field.Type), true
				}
			}
		}
	}
	var found string
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if found != "" {
			return false
		}
		switch stmt := n.(type) {
		case *ast.AssignStmt:
			if stmt.Tok != token.DEFINE {
				return true
			}
			for i, lhs := range stmt.Lhs {
				ident, ok := lhs.(*ast.Ident)
				if !ok || ident.Name != name || i >= len(stmt.Rhs) {
					continue
				}
				found = typeFromExpr(stmt.Rhs[i])
			}
		case *ast.DeclStmt:
			gen, ok := stmt.Decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.VAR {
				return true
			}
			for _, spec := range gen.Specs {
				valueSpec, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, ident := range valueSpec.Names {
					if ident.Name != name {
						continue
					}
					if valueSpec.Type != nil {
						found = exprString(valueSpec.Type)
					} else if i < len(valueSpec.Values) {
						found = typeFromExpr(valueSpec.Values[i])
					}
				}
			}
		}
		return true
	})
	return found, found != ""
}

func typeFromExpr(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.CompositeLit:
		return exprString(e.Type)
	case *ast.UnaryExpr:
		if e.Op == token.AND {
			return typeFromExpr(e.X)
		}
	case *ast.Ident:
		return e.Name
	}
	return ""
}

func dispatchMatches(idx *symbolTypeIndex, key symbolTypeKey, targetName, targetRecv, targetPkg string) (bool, bool) {
	if idx.concrete[key] {
		matched := key.name == targetRecv && pkgMatches(key.pkg, targetPkg)
		return matched, matched
	}
	if _, isInterface := idx.interfaces[key]; isInterface {
		methods := idx.ifaceMethods(key, map[symbolTypeKey]bool{})
		if !methods[targetName] {
			return false, false
		}
		if key.name == targetRecv && pkgMatches(key.pkg, targetPkg) {
			return true, false
		}
		targetKey, ok := idx.findByName(targetRecv, targetPkg)
		if !ok || !idx.concrete[targetKey] || !idx.implements(targetKey, key) {
			return false, false
		}
		return idx.implementerCount(key) <= maxImplicitImplementers, false
	}
	return true, false
}

// CallEdge is one resolved caller -> callee call site.
type CallEdge struct {
	Caller string
	Callee string
	File   string
	Line   int
	Col    int
}

// BuildCallEdgesFromParsed resolves all call expressions in one AST pass. It is
// the fast path for graph construction; FindCallersInParsedFiles keeps the
// per-target search used by edit-check.
func BuildCallEdgesFromParsed(fset *token.FileSet, files []*ParsedFile) []CallEdge {
	idx := buildSymbolTypeIndex(files)
	known := make(map[string]bool)
	for _, pf := range files {
		for _, sym := range pf.Symbols {
			known[sym.CanonicalID] = true
		}
	}
	var edges []CallEdge
	for _, pf := range files {
		var fn *ast.FuncDecl
		ast.Inspect(pf.FileNode, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.FuncDecl:
				fn = node
			case *ast.CallExpr:
				caller := callEdgeCaller(pf, fn)
				if caller == "" {
					return true
				}
				for _, callee := range resolveCallEdgeTargets(node, pf, fn, idx, known) {
					pos := fset.Position(node.Pos())
					edges = append(edges, CallEdge{Caller: caller, Callee: callee, File: pf.RelPath, Line: pos.Line, Col: pos.Column})
				}
			}
			return true
		})
	}
	return edges
}

// BuildCallEdges scans a repository and returns its resolved call edges.
func BuildCallEdges(repoRoot string) ([]CallEdge, error) {
	fset, files, err := ScanRepositoryGoFiles(repoRoot)
	if err != nil {
		return nil, err
	}
	return BuildCallEdgesFromParsed(fset, files), nil
}

func callEdgeCaller(pf *ParsedFile, fn *ast.FuncDecl) string {
	if fn == nil {
		return ""
	}
	if fn.Recv != nil && len(fn.Recv.List) > 0 {
		receiver := strings.TrimPrefix(exprString(fn.Recv.List[0].Type), "*")
		return pf.PkgPath + "::" + receiver + "." + fn.Name.Name
	}
	return pf.PkgPath + "::" + fn.Name.Name
}

func resolveCallEdgeTargets(call *ast.CallExpr, pf *ParsedFile, fn *ast.FuncDecl, idx *symbolTypeIndex, known map[string]bool) []string {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		id := pf.PkgPath + "::" + fun.Name
		if known[id] {
			return []string{id}
		}
	case *ast.SelectorExpr:
		if x, ok := fun.X.(*ast.Ident); ok {
			if imported, ok := pf.Imports[x.Name]; ok {
				id := imported + "::" + fun.Sel.Name
				if known[id] {
					return []string{id}
				}
				return nil
			}
		}
		key, ok := resolveReceiverType(fun.X, pf, fn, idx)
		if !ok {
			return nil
		}
		if idx.concrete[key] {
			id := key.pkg + "::" + key.name + "." + fun.Sel.Name
			if known[id] {
				return []string{id}
			}
			return nil
		}
		if _, isInterface := idx.interfaces[key]; isInterface {
			if !idx.ifaceMethods(key, map[symbolTypeKey]bool{})[fun.Sel.Name] {
				return nil
			}
			var out []string
			for concrete := range idx.concrete {
				if !idx.implements(concrete, key) {
					continue
				}
				id := concrete.pkg + "::" + concrete.name + "." + fun.Sel.Name
				if known[id] {
					out = append(out, id)
				}
			}
			sort.Strings(out)
			if len(out) > maxImplicitImplementers {
				out = out[:maxImplicitImplementers]
			}
			return out
		}
	}
	return nil
}
