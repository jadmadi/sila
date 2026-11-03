package gopls

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"go/ast"
	"go/doc"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// ReferenceLocation represents a precise source location for a symbol.
type ReferenceLocation struct {
	File         string `json:"file"`
	Line         int    `json:"line"`
	Column       int    `json:"column"`
	Snippet      string `json:"snippet,omitempty"`
	IsDefinition bool   `json:"is_definition"`
}

// SymbolReferencesResult represents the output of go_symbol_references.
type SymbolReferencesResult struct {
	Symbol       string              `json:"symbol"`
	TargetFile   string              `json:"target_file"`
	TotalMatches int                 `json:"total_matches"`
	Provider     string              `json:"provider"` // "gopls" or "native-ast"
	References   []ReferenceLocation `json:"references"`
}

// TypeDoc describes an exported type in a Go package.
type TypeDoc struct {
	Name    string   `json:"name"`
	Doc     string   `json:"doc,omitempty"`
	Kind    string   `json:"kind"` // "struct", "interface", "alias", "type"
	Methods []string `json:"methods,omitempty"`
}

// FuncDoc describes an exported function or method in a Go package.
type FuncDoc struct {
	Name      string `json:"name"`
	Doc       string `json:"doc,omitempty"`
	Signature string `json:"signature,omitempty"`
	Receiver  string `json:"receiver,omitempty"`
}

// PackageAPIResult represents the output of go_package_api.
type PackageAPIResult struct {
	Package   string    `json:"package"`
	Doc       string    `json:"doc,omitempty"`
	Import    string    `json:"import_path,omitempty"`
	Provider  string    `json:"provider"` // "gopls" or "native-ast"
	Types     []TypeDoc `json:"types"`
	Functions []FuncDoc `json:"functions"`
	Constants []string  `json:"constants,omitempty"`
	Variables []string  `json:"variables,omitempty"`
}

// Diagnostic represents a compiler, vet, or gopls diagnostic message.
type Diagnostic struct {
	File     string `json:"file"`
	Line     int    `json:"line"`
	Column   int    `json:"column"`
	Message  string `json:"message"`
	Severity string `json:"severity"` // "error", "warning", "info"
	Category string `json:"category,omitempty"`
}

// DiagnosticsResult represents the output of go_diagnostics.
type DiagnosticsResult struct {
	Path        string       `json:"path"`
	Clean       bool         `json:"clean"`
	Provider    string       `json:"provider"` // "gopls" or "native-vet"
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// Connector manages interactions with gopls and native Go toolchains.
type Connector struct {
	goplsPath string
	hasGopls  bool
}

// NewConnector creates and initializes a gopls connector.
func NewConnector() *Connector {
	c := &Connector{}
	c.goplsPath = findGoplsBinary()
	c.hasGopls = c.goplsPath != ""
	return c
}

// HasGopls reports whether gopls binary was discovered on host.
func (c *Connector) HasGopls() bool {
	return c.hasGopls
}

// GoplsPath returns the absolute path to gopls if found.
func (c *Connector) GoplsPath() string {
	return c.goplsPath
}

// findGoplsBinary searches standard PATH and Go development directories for gopls.
func findGoplsBinary() string {
	if p, err := exec.LookPath("gopls"); err == nil {
		return p
	}

	// Check GOPATH/bin
	home, _ := os.UserHomeDir()
	candidates := []string{
		filepath.Join(home, "go", "bin", "gopls"),
		filepath.Join(home, ".local", "bin", "gopls"),
		"/usr/local/go/bin/gopls",
		"/usr/local/bin/gopls",
	}

	if gopath := os.Getenv("GOPATH"); gopath != "" {
		candidates = append([]string{filepath.Join(gopath, "bin", "gopls")}, candidates...)
	}

	for _, cand := range candidates {
		if fi, err := os.Stat(cand); err == nil && !fi.IsDir() && (fi.Mode()&0111 != 0) {
			return cand
		}
	}
	return ""
}

// FindSymbolReferences searches for all usages and declarations of symbol in target file or dir.
func (c *Connector) FindSymbolReferences(ctx context.Context, file, symbol, dir string) (*SymbolReferencesResult, error) {
	symbol = strings.TrimSpace(symbol)
	if symbol == "" {
		return nil, fmt.Errorf("symbol is required")
	}

	targetDir := dir
	if targetDir == "" {
		if file != "" {
			targetDir = filepath.Dir(file)
		} else {
			targetDir = "."
		}
	}

	// If gopls is available and file is specified, try running gopls references
	if c.hasGopls && file != "" {
		res, err := c.runGoplsReferences(ctx, file, symbol, targetDir)
		if err == nil && len(res.References) > 0 {
			return res, nil
		}
	}

	// Fallback: Pure-Go native AST parser across target directory
	return c.findReferencesNativeAST(file, symbol, targetDir)
}

func (c *Connector) runGoplsReferences(ctx context.Context, file, symbol, dir string) (*SymbolReferencesResult, error) {
	// Look up line:col of symbol in file first
	line, col, err := findSymbolPositionInFile(file, symbol)
	if err != nil {
		return nil, err
	}

	posArg := fmt.Sprintf("%s:%d:%d", file, line, col)
	cmd := exec.CommandContext(ctx, c.goplsPath, "references", posArg)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("gopls error: %w (output: %s)", err, string(out))
	}

	var refs []ReferenceLocation
	scanner := bufio.NewScanner(bytes.NewReader(out))
	refRe := regexp.MustCompile(`^([^:]+):(\d+):(\d+)(.*)$`)
	for scanner.Scan() {
		text := strings.TrimSpace(scanner.Text())
		if matches := refRe.FindStringSubmatch(text); matches != nil {
			l, _ := strconv.Atoi(matches[2])
			c, _ := strconv.Atoi(matches[3])
			snippet := strings.TrimSpace(matches[4])
			snippet = strings.TrimPrefix(snippet, ":")
			refs = append(refs, ReferenceLocation{
				File:         matches[1],
				Line:         l,
				Column:       c,
				Snippet:      strings.TrimSpace(snippet),
				IsDefinition: (l == line && matches[1] == file),
			})
		}
	}

	return &SymbolReferencesResult{
		Symbol:       symbol,
		TargetFile:   file,
		TotalMatches: len(refs),
		Provider:     "gopls",
		References:   refs,
	}, nil
}

func findSymbolPositionInFile(file, symbol string) (int, int, error) {
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, file, nil, 0)
	if err != nil {
		return 0, 0, err
	}

	var foundLine, foundCol int
	ast.Inspect(node, func(n ast.Node) bool {
		if ident, ok := n.(*ast.Ident); ok {
			if ident.Name == symbol {
				pos := fset.Position(ident.Pos())
				foundLine = pos.Line
				foundCol = pos.Column
				return false
			}
		}
		return true
	})

	if foundLine == 0 {
		return 0, 0, fmt.Errorf("symbol %q not found in %s", symbol, file)
	}
	return foundLine, foundCol, nil
}

func (c *Connector) findReferencesNativeAST(file, symbol, targetDir string) (*SymbolReferencesResult, error) {
	fset := token.NewFileSet()
	var refs []ReferenceLocation

	err := filepath.Walk(targetDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if strings.HasPrefix(info.Name(), ".") && info.Name() != "." {
				return filepath.SkipDir
			}
			if info.Name() == "vendor" || info.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}

		node, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return nil
		}

		fileLines := readFileLines(path)

		ast.Inspect(node, func(n ast.Node) bool {
			if ident, ok := n.(*ast.Ident); ok {
				if ident.Name == symbol {
					pos := fset.Position(ident.Pos())
					snippet := ""
					if pos.Line > 0 && pos.Line <= len(fileLines) {
						snippet = strings.TrimSpace(fileLines[pos.Line-1])
					}
					isDef := (ident.Obj != nil && ident.Obj.Decl != nil && ident.Obj.Name == symbol)
					refs = append(refs, ReferenceLocation{
						File:         path,
						Line:         pos.Line,
						Column:       pos.Column,
						Snippet:      snippet,
						IsDefinition: isDef,
					})
				}
			}
			return true
		})
		return nil
	})

	if err != nil {
		return nil, err
	}

	return &SymbolReferencesResult{
		Symbol:       symbol,
		TargetFile:   file,
		TotalMatches: len(refs),
		Provider:     "native-ast",
		References:   refs,
	}, nil
}

func readFileLines(path string) []string {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var lines []string
	scanner := bufio.NewScanner(bytes.NewReader(content))
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines
}

// GetPackageAPI extracts exported symbols, signatures, and docstrings from a package.
func (c *Connector) GetPackageAPI(ctx context.Context, pkgPathOrDir string, includeUnexported bool) (*PackageAPIResult, error) {
	dir := pkgPathOrDir
	if dir == "" {
		dir = "."
	}

	// If directory doesn't exist on disk, check if it's relative or module path
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		// Attempt to resolve relative to current dir
		if relFi, relErr := os.Stat(filepath.Clean(dir)); relErr == nil && relFi.IsDir() {
			dir = filepath.Clean(dir)
		}
	}

	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, nil, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("failed to parse package directory %s: %w", dir, err)
	}

	if len(pkgs) == 0 {
		return nil, fmt.Errorf("no Go package found in %s", dir)
	}

	// Pick primary package (skipping _test package if primary exists)
	var primary *ast.Package
	for name, p := range pkgs {
		if !strings.HasSuffix(name, "_test") {
			primary = p
			break
		}
	}
	if primary == nil {
		for _, p := range pkgs {
			primary = p
			break
		}
	}

	docPkg := doc.New(primary, dir, 0)

	var types []TypeDoc
	for _, t := range docPkg.Types {
		if !includeUnexported && !ast.IsExported(t.Name) {
			continue
		}
		var methods []string
		for _, m := range t.Methods {
			if includeUnexported || ast.IsExported(m.Name) {
				methods = append(methods, m.Name)
			}
		}
		kind := "type"
		if strings.Contains(t.Doc, "struct") || len(t.Methods) > 0 {
			kind = "struct"
		}
		types = append(types, TypeDoc{
			Name:    t.Name,
			Doc:     strings.TrimSpace(t.Doc),
			Kind:    kind,
			Methods: methods,
		})
	}

	var funcs []FuncDoc
	for _, f := range docPkg.Funcs {
		if !includeUnexported && !ast.IsExported(f.Name) {
			continue
		}
		funcs = append(funcs, FuncDoc{
			Name:      f.Name,
			Doc:       strings.TrimSpace(f.Doc),
			Signature: formatFuncSignature(f.Decl),
		})
	}
	for _, t := range docPkg.Types {
		for _, f := range t.Funcs {
			if !includeUnexported && !ast.IsExported(f.Name) {
				continue
			}
			funcs = append(funcs, FuncDoc{
				Name:      f.Name,
				Doc:       strings.TrimSpace(f.Doc),
				Signature: formatFuncSignature(f.Decl),
			})
		}
	}

	var consts []string
	for _, cGroup := range docPkg.Consts {
		for _, name := range cGroup.Names {
			if includeUnexported || ast.IsExported(name) {
				consts = append(consts, name)
			}
		}
	}

	var vars []string
	for _, vGroup := range docPkg.Vars {
		for _, name := range vGroup.Names {
			if includeUnexported || ast.IsExported(name) {
				vars = append(vars, name)
			}
		}
	}

	return &PackageAPIResult{
		Package:   docPkg.Name,
		Doc:       strings.TrimSpace(docPkg.Doc),
		Import:    dir,
		Provider:  "native-ast",
		Types:     types,
		Functions: funcs,
		Constants: consts,
		Variables: vars,
	}, nil
}

func formatFuncSignature(decl *ast.FuncDecl) string {
	if decl == nil || decl.Type == nil {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("func ")
	if decl.Recv != nil && len(decl.Recv.List) > 0 {
		sb.WriteString("(...) ")
	}
	sb.WriteString(decl.Name.Name)
	sb.WriteString("(")
	if decl.Type.Params != nil {
		var params []string
		for _, p := range decl.Type.Params.List {
			for _, n := range p.Names {
				params = append(params, n.Name)
			}
		}
		sb.WriteString(strings.Join(params, ", "))
	}
	sb.WriteString(")")
	return sb.String()
}

// GetDiagnostics runs compiler and linter checks across path.
func (c *Connector) GetDiagnostics(ctx context.Context, path string, tags string) (*DiagnosticsResult, error) {
	if path == "" {
		path = "./..."
	}

	// 1. Try gopls check if available
	if c.hasGopls {
		res, err := c.runGoplsCheck(ctx, path)
		if err == nil {
			return res, nil
		}
	}

	// 2. Native toolchain check via `go vet`
	args := []string{"vet"}
	if tags != "" {
		args = append(args, "-tags", tags)
	}
	args = append(args, path)

	cmd := exec.CommandContext(ctx, "go", args...)
	out, err := cmd.CombinedOutput()

	var diags []Diagnostic
	if len(out) > 0 {
		diagRe := regexp.MustCompile(`^([^:]+):(\d+):(\d+):\s*(.*)$`)
		scanner := bufio.NewScanner(bytes.NewReader(out))
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if matches := diagRe.FindStringSubmatch(line); matches != nil {
				l, _ := strconv.Atoi(matches[2])
				col, _ := strconv.Atoi(matches[3])
				diags = append(diags, Diagnostic{
					File:     matches[1],
					Line:     l,
					Column:   col,
					Message:  matches[4],
					Severity: "warning",
					Category: "vet",
				})
			} else if strings.Contains(line, ":") {
				diags = append(diags, Diagnostic{
					File:     path,
					Message:  line,
					Severity: "error",
					Category: "compiler",
				})
			}
		}
	}

	clean := len(diags) == 0 && err == nil

	return &DiagnosticsResult{
		Path:        path,
		Clean:       clean,
		Provider:    "native-vet",
		Diagnostics: diags,
	}, nil
}

func (c *Connector) runGoplsCheck(ctx context.Context, path string) (*DiagnosticsResult, error) {
	cmd := exec.CommandContext(ctx, c.goplsPath, "check", path)
	out, err := cmd.CombinedOutput()
	if err != nil && len(out) == 0 {
		return nil, err
	}

	var diags []Diagnostic
	diagRe := regexp.MustCompile(`^([^:]+):(\d+):(\d+):\s*(.*)$`)
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if matches := diagRe.FindStringSubmatch(line); matches != nil {
			l, _ := strconv.Atoi(matches[2])
			col, _ := strconv.Atoi(matches[3])
			diags = append(diags, Diagnostic{
				File:     matches[1],
				Line:     l,
				Column:   col,
				Message:  matches[4],
				Severity: "error",
				Category: "gopls",
			})
		}
	}

	return &DiagnosticsResult{
		Path:        path,
		Clean:       len(diags) == 0,
		Provider:    "gopls",
		Diagnostics: diags,
	}, nil
}
