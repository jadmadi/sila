package testmatrix

import (
	"bytes"
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	silaast "github.com/jadmadi/sila/internal/ast"
)

// TestTarget represents a runnable Go test associated with a symbol or package.
type TestTarget struct {
	TestFunction string `json:"test_function"`
	TestFile     string `json:"test_file"`
	Package      string `json:"package"`
}

// SelectorResult contains the identified blast-radius test matrix.
type SelectorResult struct {
	Status          string       `json:"status"` // "tests_identified", "no_changes", "no_tests_found"
	ChangedSymbols  []string     `json:"changed_symbols"`
	TestsToRun      []TestTarget `json:"tests_to_run"`
	UntestedSymbols []string     `json:"untested_symbols"`
	Command         string       `json:"command"`
	ExitCode        int          `json:"exit_code"`
}

// BipartiteCallGraph maps production symbols to the tests that exercise them.
type BipartiteCallGraph struct {
	SymbolToTests map[string][]TestTarget
	PackageTests  map[string][]TestTarget
}

// BuildTestCallGraph inspects all *_test.go files in repoRoot.
func BuildTestCallGraph(repoRoot string) (*BipartiteCallGraph, error) {
	graph := &BipartiteCallGraph{
		SymbolToTests: make(map[string][]TestTarget),
		PackageTests:  make(map[string][]TestTarget),
	}

	fset := token.NewFileSet()

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
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}

		relPath, err := filepath.Rel(repoRoot, path)
		if err != nil {
			relPath = path
		}
		relPath = filepath.ToSlash(relPath)

		src, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		fileNode, err := parser.ParseFile(fset, relPath, src, parser.ParseComments)
		if err != nil {
			return nil
		}

		pkgPath := silaast.ResolvePackagePath(path, repoRoot)
		relDir := filepath.Dir(relPath)
		dotPkg := "./" + relDir
		if dotPkg == "./." {
			dotPkg = "."
		}

		// Map file imports (alias -> pkgPath)
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

		for _, decl := range fileNode.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			name := fn.Name.Name
			if !strings.HasPrefix(name, "Test") && !strings.HasPrefix(name, "Benchmark") {
				continue
			}

			target := TestTarget{
				TestFunction: name,
				TestFile:     relPath,
				Package:      dotPkg,
			}

			graph.PackageTests[dotPkg] = append(graph.PackageTests[dotPkg], target)

			// Walk test function body to find production calls
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}

				switch fun := call.Fun.(type) {
				case *ast.Ident:
					// Direct in-package call: Foo(...)
					calledSym := fmt.Sprintf("%s::%s", pkgPath, fun.Name)
					graph.SymbolToTests[calledSym] = append(graph.SymbolToTests[calledSym], target)
					// Also index by bare name
					graph.SymbolToTests[fun.Name] = append(graph.SymbolToTests[fun.Name], target)

				case *ast.SelectorExpr:
					// Selector call: pkg.Foo(...) or obj.Method(...)
					xIdent, isIdent := fun.X.(*ast.Ident)
					if isIdent {
						if impPath, isPkg := imports[xIdent.Name]; isPkg {
							calledSym := fmt.Sprintf("%s::%s", impPath, fun.Sel.Name)
							graph.SymbolToTests[calledSym] = append(graph.SymbolToTests[calledSym], target)
						}
					}
					graph.SymbolToTests[fun.Sel.Name] = append(graph.SymbolToTests[fun.Sel.Name], target)
				}
				return true
			})
		}
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("build test call graph: %w", err)
	}

	// Deduplicate test targets
	for sym, targets := range graph.SymbolToTests {
		graph.SymbolToTests[sym] = dedupTestTargets(targets)
	}
	for pkg, targets := range graph.PackageTests {
		graph.PackageTests[pkg] = dedupTestTargets(targets)
	}

	return graph, nil
}

// GetChangedSymbols inspects git status / diff against HEAD and returns modified symbols.
func GetChangedSymbols(repoRoot string) ([]string, map[string][]int, error) {
	cmd := exec.Command("git", "-C", repoRoot, "diff", "HEAD", "--unified=0")
	out, err := cmd.Output()
	if err != nil {
		// Fallback to git diff without HEAD (or staged)
		cmd = exec.Command("git", "-C", repoRoot, "diff", "--unified=0")
		out, _ = cmd.Output()
	}

	// Parse diff to find modified files and line numbers
	modifiedLines := make(map[string][]int) // relPath -> list of modified lines
	var currentFile string

	hunkRegex := regexp.MustCompile(`@@ -\d+(?:,\d+)? \+(\d+)(?:,(\d+))? @@`)

	lines := strings.Split(string(out), "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "+++ b/") {
			currentFile = filepath.ToSlash(strings.TrimPrefix(line, "+++ b/"))
			continue
		}
		if currentFile == "" || !strings.HasSuffix(currentFile, ".go") || strings.HasSuffix(currentFile, "_test.go") {
			continue
		}

		if matches := hunkRegex.FindStringSubmatch(line); len(matches) > 1 {
			startLine, err := strconv.Atoi(matches[1])
			if err != nil {
				continue
			}
			lineCount := 1
			if len(matches) > 2 && matches[2] != "" {
				if cnt, err := strconv.Atoi(matches[2]); err == nil && cnt > 0 {
					lineCount = cnt
				}
			}
			for l := startLine; l < startLine+lineCount; l++ {
				modifiedLines[currentFile] = append(modifiedLines[currentFile], l)
			}
		}
	}

	// If no unified diff lines found, check status for modified files
	if len(modifiedLines) == 0 {
		statusCmd := exec.Command("git", "-C", repoRoot, "status", "--porcelain")
		statusOut, _ := statusCmd.Output()
		statusLines := strings.Split(string(statusOut), "\n")
		for _, s := range statusLines {
			s = strings.TrimSpace(s)
			if len(s) < 3 {
				continue
			}
			file := strings.TrimSpace(s[2:])
			file = filepath.ToSlash(file)
			if strings.HasSuffix(file, ".go") && !strings.HasSuffix(file, "_test.go") {
				modifiedLines[file] = []int{1} // Mark as touched
			}
		}
	}

	var changedSymbols []string
	fset := token.NewFileSet()

	for file, linesList := range modifiedLines {
		absPath := filepath.Join(repoRoot, file)
		src, err := os.ReadFile(absPath)
		if err != nil {
			continue
		}

		fileNode, err := parser.ParseFile(fset, file, src, 0)
		if err != nil {
			continue
		}

		pkgPath := silaast.ResolvePackagePath(absPath, repoRoot)
		syms, _ := silaast.ExtractSymbolsFromSource(fset, file, src, pkgPath)

		for _, sym := range syms {
			// Check if any modified line intersects symbol span
			touched := false
			for _, ml := range linesList {
				if ml >= sym.StartLine && ml <= sym.EndLine {
					touched = true
					break
				}
			}
			// If file was modified with line 1 (entire file fallback), include all symbols
			if !touched && len(linesList) == 1 && linesList[0] == 1 {
				touched = true
			}

			if touched {
				changedSymbols = append(changedSymbols, sym.CanonicalID)
			}
		}
		_ = fileNode
	}

	sort.Strings(changedSymbols)
	return dedupStrings(changedSymbols), modifiedLines, nil
}

// SelectTargetedTests computes the targeted test suite for modified code in repoRoot.
func SelectTargetedTests(repoRoot string) (*SelectorResult, error) {
	graph, err := BuildTestCallGraph(repoRoot)
	if err != nil {
		return nil, err
	}

	changedSymbols, _, err := GetChangedSymbols(repoRoot)
	if err != nil {
		return nil, err
	}

	if len(changedSymbols) == 0 {
		return &SelectorResult{
			Status:          "no_changes",
			ChangedSymbols:  nil,
			TestsToRun:      nil,
			UntestedSymbols: nil,
			Command:         "",
			ExitCode:        0,
		}, nil
	}

	var testsToRun []TestTarget
	var untestedSymbols []string
	testSet := make(map[string]bool)

	for _, sym := range changedSymbols {
		var matched []TestTarget

		// 1. Direct canonical symbol lookup
		if targets, ok := graph.SymbolToTests[sym]; ok && len(targets) > 0 {
			matched = append(matched, targets...)
		}

		// 2. Bare name lookup
		bareName := sym
		if idx := strings.Index(sym, "::"); idx != -1 {
			bareName = sym[idx+2:]
		}
		if dotIdx := strings.Index(bareName, "."); dotIdx != -1 {
			bareName = bareName[dotIdx+1:]
		}

		if targets, ok := graph.SymbolToTests[bareName]; ok && len(targets) > 0 {
			matched = append(matched, targets...)
		}

		// 3. Name heuristic: Test<BareName>
		heuristicPrefix := "Test" + bareName
		for _, targets := range graph.PackageTests {
			for _, t := range targets {
				if strings.HasPrefix(t.TestFunction, heuristicPrefix) {
					matched = append(matched, t)
				}
			}
		}

		matched = dedupTestTargets(matched)

		if len(matched) == 0 {
			untestedSymbols = append(untestedSymbols, sym)
		} else {
			for _, t := range matched {
				key := fmt.Sprintf("%s::%s", t.Package, t.TestFunction)
				if !testSet[key] {
					testSet[key] = true
					testsToRun = append(testsToRun, t)
				}
			}
		}
	}

	sort.Slice(testsToRun, func(i, j int) bool {
		if testsToRun[i].Package != testsToRun[j].Package {
			return testsToRun[i].Package < testsToRun[j].Package
		}
		return testsToRun[i].TestFunction < testsToRun[j].TestFunction
	})

	command := BuildGoTestCommand(testsToRun)
	status := "tests_identified"
	if len(testsToRun) == 0 {
		status = "no_tests_found"
	}

	return &SelectorResult{
		Status:          status,
		ChangedSymbols:  changedSymbols,
		TestsToRun:      testsToRun,
		UntestedSymbols: untestedSymbols,
		Command:         command,
		ExitCode:        0,
	}, nil
}

// BuildGoTestCommand constructs a targeted `go test -race -run ...` command string.
func BuildGoTestCommand(tests []TestTarget) string {
	if len(tests) == 0 {
		return ""
	}

	// Group tests by package
	pkgMap := make(map[string][]string)
	for _, t := range tests {
		pkgMap[t.Package] = append(pkgMap[t.Package], t.TestFunction)
	}

	var pkgs []string
	for p := range pkgMap {
		pkgs = append(pkgs, p)
	}
	sort.Strings(pkgs)

	// Collect unique test function patterns
	funcSet := make(map[string]bool)
	var funcs []string
	for _, t := range tests {
		if !funcSet[t.TestFunction] {
			funcSet[t.TestFunction] = true
			funcs = append(funcs, t.TestFunction)
		}
	}
	sort.Strings(funcs)

	pattern := fmt.Sprintf("^%s$", strings.Join(funcs, "|"))
	if len(funcs) > 1 {
		pattern = fmt.Sprintf("^(%s)$", strings.Join(funcs, "|"))
	}

	return fmt.Sprintf("go test -race -run '%s' %s", pattern, strings.Join(pkgs, " "))
}

// RunTargeted executes the selector's generated test command.
func RunTargeted(ctx context.Context, repoRoot string, command string) (string, int, error) {
	if strings.TrimSpace(command) == "" {
		return "No targeted tests to run.", 0, nil
	}

	args := splitCommandArgs(command)
	if len(args) == 0 {
		return "", 0, nil
	}

	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = repoRoot
	var outBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &outBuf

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = 1
		}
	}

	return outBuf.String(), exitCode, err
}

// splitCommandArgs parses a command line string into separate arguments,
// stripping surrounding single or double quotes from quoted arguments.
func splitCommandArgs(cmdStr string) []string {
	var args []string
	var current strings.Builder
	inSingle := false
	inDouble := false

	for i := 0; i < len(cmdStr); i++ {
		ch := cmdStr[i]
		switch {
		case ch == '\'' && !inDouble:
			inSingle = !inSingle
		case ch == '"' && !inSingle:
			inDouble = !inDouble
		case (ch == ' ' || ch == '\t' || ch == '\n') && !inSingle && !inDouble:
			if current.Len() > 0 {
				args = append(args, current.String())
				current.Reset()
			}
		default:
			current.WriteByte(ch)
		}
	}
	if current.Len() > 0 {
		args = append(args, current.String())
	}
	return args
}

func dedupTestTargets(targets []TestTarget) []TestTarget {
	seen := make(map[string]bool)
	var res []TestTarget
	for _, t := range targets {
		key := fmt.Sprintf("%s::%s", t.Package, t.TestFunction)
		if !seen[key] {
			seen[key] = true
			res = append(res, t)
		}
	}
	return res
}

func dedupStrings(slice []string) []string {
	seen := make(map[string]bool)
	var res []string
	for _, s := range slice {
		if !seen[s] {
			seen[s] = true
			res = append(res, s)
		}
	}
	return res
}
