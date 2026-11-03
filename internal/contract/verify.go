package contract

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	silaast "github.com/jadmadi/sila/internal/ast"
	"github.com/jadmadi/sila/internal/symbols"
)

// SignatureContract captures parameter and return arity for a function/method.
type SignatureContract struct {
	ParamsCount  int  `json:"params_count"`
	ResultsCount int  `json:"results_count"`
	IsExported   bool `json:"is_exported"`
}

// ContractDiff captures differences between baseline and candidate signatures.
type ContractDiff struct {
	ParamsWas     int  `json:"params_was"`
	ParamsNow     int  `json:"params_now"`
	ResultsWas    int  `json:"results_was"`
	ResultsNow    int  `json:"results_now"`
	IsExported    bool `json:"is_exported"`
	ExportDropped bool `json:"export_dropped,omitempty"`
}

// IncompatibleCaller describes a call site passing an invalid number of arguments.
type IncompatibleCaller struct {
	Caller       string `json:"caller"`
	File         string `json:"file"`
	Line         int    `json:"line"`
	Col          int    `json:"col"`
	ArgsPassed   int    `json:"args_passed"`
	ArgsExpected int    `json:"args_expected"`
}

// EditCheckResult is the result returned by sila edit-check.
type EditCheckResult struct {
	Status              string               `json:"status"` // "ok", "contract-change", "breaking-change", "error"
	Symbol              string               `json:"symbol"`
	File                string               `json:"file"`
	Contract            ContractDiff         `json:"contract"`
	CallersCount        int                  `json:"callers_count"`
	IncompatibleCount   int                  `json:"incompatible_count"`
	IncompatibleCallers []IncompatibleCaller `json:"incompatible_callers"`
	Detail              string               `json:"detail,omitempty"`
	ExitCode            int                  `json:"exit_code"`
}

// SafeDeleteResult is the result returned by sila safe-delete.
type SafeDeleteResult struct {
	Status            string             `json:"status"` // "refused", "safe-to-delete", "error"
	Symbol            string             `json:"symbol"`
	File              string             `json:"file,omitempty"`
	InboundReferences int                `json:"inbound_references"`
	Callers           []symbols.CallSite `json:"callers,omitempty"`
	Recommendation    string             `json:"recommendation"`
	ExitCode          int                `json:"exit_code"`
}

// CountFieldIdentifiers counts the actual parameters/results in an AST FieldList.
// Handles grouped names like: (a, b, c int) -> 3.
func CountFieldIdentifiers(fields *ast.FieldList) int {
	if fields == nil {
		return 0
	}
	count := 0
	for _, f := range fields.List {
		if len(f.Names) == 0 {
			count++ // unnamed parameter/result
		} else {
			count += len(f.Names)
		}
	}
	return count
}

// FindFuncDeclInFile searches an AST file for a function or method matching name and optional receiver.
func FindFuncDeclInFile(fileNode *ast.File, funcName, receiver string) *ast.FuncDecl {
	cleanTargetRecv := strings.TrimPrefix(strings.TrimSpace(receiver), "*")

	for _, decl := range fileNode.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		if fn.Name.Name != funcName {
			continue
		}

		if cleanTargetRecv != "" {
			if fn.Recv == nil || len(fn.Recv.List) == 0 {
				continue
			}
			recvStr := exprToString(fn.Recv.List[0].Type)
			cleanRecv := strings.TrimPrefix(recvStr, "*")
			if cleanRecv != cleanTargetRecv {
				continue
			}
		} else {
			if fn.Recv != nil && len(fn.Recv.List) > 0 {
				continue
			}
		}

		return fn
	}
	return nil
}

// LocateSymbolDeclFile finds the file path containing the declaration of targetSymbol.
func LocateSymbolDeclFile(repoRoot, targetSymbol string) (string, error) {
	pt := symbols.ParseTargetSymbol(targetSymbol)
	if pt.File != "" {
		absPath := filepath.Join(repoRoot, pt.File)
		if _, err := os.Stat(absPath); err == nil {
			return pt.File, nil
		}
	}

	_, files, err := symbols.ScanRepositoryGoFiles(repoRoot)
	if err != nil {
		return "", err
	}

	for _, pf := range files {
		for _, sym := range pf.Symbols {
			if pt.MatchesSymbol(sym) {
				return sym.FilePath, nil
			}
		}
	}

	return "", fmt.Errorf("symbol %q not found in repository %s", targetSymbol, repoRoot)
}

// LoadGitHeadFile reads the version of a file committed in git HEAD.
func LoadGitHeadFile(repoRoot, relPath string) ([]byte, error) {
	normPath := filepath.ToSlash(relPath)
	cmd := exec.Command("git", "-C", repoRoot, "show", "HEAD:"+normPath)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git show HEAD:%s: %w", normPath, err)
	}
	return out, nil
}

// EditCheck performs in-memory pre-edit contract verification.
// If payload is provided, it parses payload in-memory without touching disk.
// If payload is empty, it compares the working tree file against git HEAD.
func EditCheck(repoRoot, targetSymbol string, candidatePayload []byte) (*EditCheckResult, error) {
	pt := symbols.ParseTargetSymbol(targetSymbol)

	declFile, err := LocateSymbolDeclFile(repoRoot, targetSymbol)
	if err != nil {
		return &EditCheckResult{
			Status:   "error",
			Symbol:   targetSymbol,
			Detail:   err.Error(),
			ExitCode: 2,
		}, nil
	}

	absDeclFile := filepath.Join(repoRoot, declFile)

	var baselineSrc []byte
	var candidateSrc []byte

	if len(candidatePayload) > 0 {
		// Payload provided: baseline is current file on disk, candidate is payload
		diskBytes, err := os.ReadFile(absDeclFile)
		if err != nil {
			return nil, fmt.Errorf("read baseline file %s: %w", absDeclFile, err)
		}
		baselineSrc = diskBytes
		candidateSrc = candidatePayload
	} else {
		// No payload: baseline is git HEAD, candidate is working tree file on disk
		headBytes, err := LoadGitHeadFile(repoRoot, declFile)
		if err != nil {
			// If not in git HEAD, treat working tree as baseline
			headBytes, _ = os.ReadFile(absDeclFile)
		}
		baselineSrc = headBytes

		diskBytes, err := os.ReadFile(absDeclFile)
		if err != nil {
			return nil, fmt.Errorf("read working tree file %s: %w", absDeclFile, err)
		}
		candidateSrc = diskBytes
	}

	fset := token.NewFileSet()

	// Parse baseline
	baseNode, err := parser.ParseFile(fset, declFile, baselineSrc, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parse baseline %s: %w", declFile, err)
	}

	// Parse candidate in-memory
	candNode, err := parser.ParseFile(fset, declFile, candidateSrc, parser.AllErrors|parser.ParseComments)
	if err != nil {
		return &EditCheckResult{
			Status:   "error",
			Symbol:   targetSymbol,
			File:     declFile,
			Detail:   fmt.Sprintf("syntax error in candidate payload: %v", err),
			ExitCode: 2,
		}, nil
	}

	baseFn := FindFuncDeclInFile(baseNode, pt.Name, pt.Receiver)
	candFn := FindFuncDeclInFile(candNode, pt.Name, pt.Receiver)

	if baseFn == nil && candFn == nil {
		return &EditCheckResult{
			Status:   "error",
			Symbol:   targetSymbol,
			File:     declFile,
			Detail:   "symbol declaration not found in baseline or candidate",
			ExitCode: 2,
		}, nil
	}

	diff := ContractDiff{}
	if baseFn != nil {
		diff.ParamsWas = CountFieldIdentifiers(baseFn.Type.Params)
		diff.ResultsWas = CountFieldIdentifiers(baseFn.Type.Results)
		diff.IsExported = ast.IsExported(baseFn.Name.Name)
	}

	if candFn != nil {
		diff.ParamsNow = CountFieldIdentifiers(candFn.Type.Params)
		diff.ResultsNow = CountFieldIdentifiers(candFn.Type.Results)
		candExported := ast.IsExported(candFn.Name.Name)
		if diff.IsExported && !candExported {
			diff.ExportDropped = true
		}
		diff.IsExported = candExported
	}

	// Find callers across repository
	callers, err := symbols.FindCallers(repoRoot, targetSymbol)
	if err != nil {
		return nil, fmt.Errorf("find callers: %w", err)
	}

	res := &EditCheckResult{
		Symbol:       targetSymbol,
		File:         declFile,
		Contract:     diff,
		CallersCount: len(callers),
	}

	// Check compatibility
	isParamDiff := diff.ParamsWas != diff.ParamsNow
	isExportDropped := diff.ExportDropped

	var incompatible []IncompatibleCaller
	targetPkg := silaast.ResolvePackagePath(absDeclFile, repoRoot)

	for _, c := range callers {
		// Arity mismatch
		if isParamDiff && c.ArgsPassed != diff.ParamsNow {
			incompatible = append(incompatible, IncompatibleCaller{
				Caller:       c.Caller,
				File:         c.File,
				Line:         c.Line,
				Col:          c.Col,
				ArgsPassed:   c.ArgsPassed,
				ArgsExpected: diff.ParamsNow,
			})
			continue
		}

		// Visibility drop mismatch
		if isExportDropped {
			callerPkg := silaast.ResolvePackagePath(filepath.Join(repoRoot, c.File), repoRoot)
			if callerPkg != targetPkg {
				incompatible = append(incompatible, IncompatibleCaller{
					Caller:       c.Caller,
					File:         c.File,
					Line:         c.Line,
					Col:          c.Col,
					ArgsPassed:   c.ArgsPassed,
					ArgsExpected: diff.ParamsNow,
				})
			}
		}
	}

	res.IncompatibleCount = len(incompatible)
	res.IncompatibleCallers = incompatible

	if len(incompatible) > 0 {
		res.Status = "contract-change"
		res.ExitCode = 1
	} else if isParamDiff || diff.ResultsWas != diff.ResultsNow || diff.ExportDropped {
		res.Status = "contract-change"
		res.ExitCode = 0
	} else {
		res.Status = "ok"
		res.ExitCode = 0
	}

	return res, nil
}

// VerifySafeDelete verifies that targetSymbol has zero inbound call references before deletion.
func VerifySafeDelete(repoRoot, targetSymbol string) (*SafeDeleteResult, error) {
	declFile, err := LocateSymbolDeclFile(repoRoot, targetSymbol)
	if err != nil {
		// Symbol does not exist; safe to delete or already absent
		return &SafeDeleteResult{
			Status:            "safe-to-delete",
			Symbol:            targetSymbol,
			InboundReferences: 0,
			Recommendation:    "Symbol declaration not found in repository; safe to remove",
			ExitCode:          0,
		}, nil
	}

	callers, err := symbols.FindCallers(repoRoot, targetSymbol)
	if err != nil {
		return nil, fmt.Errorf("find callers: %w", err)
	}

	res := &SafeDeleteResult{
		Symbol:            targetSymbol,
		File:              declFile,
		InboundReferences: len(callers),
		Callers:           callers,
	}

	if len(callers) > 0 {
		res.Status = "refused"
		res.Recommendation = "Remove active call sites before deleting symbol"
		res.ExitCode = 1
	} else {
		res.Status = "safe-to-delete"
		res.Recommendation = "No inbound references found; safe to remove"
		res.ExitCode = 0
	}

	return res, nil
}

// exprToString converts an ast.Expr to its string representation.
func exprToString(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.StarExpr:
		return "*" + exprToString(e.X)
	case *ast.SelectorExpr:
		return exprToString(e.X) + "." + e.Sel.Name
	default:
		var buf bytes.Buffer
		_ = ast.Fprint(&buf, token.NewFileSet(), expr, nil)
		return buf.String()
	}
}
