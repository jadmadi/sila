package sentry

import (
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// CalculateACIFromSource computes the Attribution Coupling Index between old and new Go source
// code specifically at the enclosing targetSymbol.
func CalculateACIFromSource(oldSrc, newSrc, targetSymbol string) (float64, error) {
	if oldSrc == "" && newSrc == "" {
		return 0.0, nil
	}
	if oldSrc == newSrc {
		return 0.0, nil
	}

	fsetOld := token.NewFileSet()
	nodeOld, errOld := parser.ParseFile(fsetOld, "old.go", oldSrc, parser.ParseComments)

	fsetNew := token.NewFileSet()
	nodeNew, errNew := parser.ParseFile(fsetNew, "new.go", newSrc, parser.ParseComments)

	if errOld != nil || errNew != nil {
		// Non-Go file or syntax error: fall back to normalized token Levenshtein ratio
		return fallbackTextEditDistance(oldSrc, newSrc), nil
	}

	oldDecl := findDeclaration(nodeOld, targetSymbol)
	newDecl := findDeclaration(nodeNew, targetSymbol)

	if oldDecl == nil && newDecl == nil {
		// Symbol not found in either: check if file-level edits occurred
		if oldSrc != newSrc {
			return 0.20, nil // Weak file-level touch without symbol anchor
		}
		return 0.0, nil
	}

	if (oldDecl == nil && newDecl != nil) || (oldDecl != nil && newDecl == nil) {
		// Symbol was created or deleted: maximum coupling
		return 1.0, nil
	}

	// Compare AST structures of old and new declaration
	oldCount := countASTNodes(oldDecl)
	newCount := countASTNodes(newDecl)

	diffCount := math.Abs(float64(oldCount - newCount))
	avgCount := float64(oldCount+newCount) / 2.0
	if avgCount == 0 {
		return 0.0, nil
	}

	// Normalized structural difference
	structuralRatio := diffCount / avgCount
	if structuralRatio > 1.0 {
		structuralRatio = 1.0
	}

	// Also check statement text diff within declaration
	var oldBuf, newBuf strings.Builder
	_ = format.Node(&oldBuf, fsetOld, oldDecl)
	_ = format.Node(&newBuf, fsetNew, newDecl)

	if strings.TrimSpace(oldBuf.String()) == strings.TrimSpace(newBuf.String()) {
		// Zero changes inside the target symbol!
		return 0.0, nil
	}

	// Content modified inside symbol
	textDiffRatio := fallbackTextEditDistance(oldBuf.String(), newBuf.String())
	coupling := 0.5*structuralRatio + 0.5*textDiffRatio
	return math.Min(1.0, math.Max(0.10, coupling)), nil
}

// CalculateACIFromGit computes ACI by retrieving git diff for targetFile against HEAD or working tree.
func CalculateACIFromGit(repoDir, targetFile, targetSymbol string) (float64, error) {
	absRepo, err := filepath.Abs(repoDir)
	if err != nil {
		return 0.0, fmt.Errorf("resolve repo dir: %w", err)
	}

	target := targetFile
	if !filepath.IsAbs(target) {
		target = filepath.Join(absRepo, target)
	}
	if _, err := os.Stat(target); os.IsNotExist(err) {
		return 0.0, fmt.Errorf("target file does not exist: %s", targetFile)
	}

	relPath, err := filepath.Rel(absRepo, target)
	if err != nil || strings.HasPrefix(relPath, "..") {
		return 0.0, fmt.Errorf("target file %s is outside repository %s", targetFile, repoDir)
	}
	gitRelPath := filepath.ToSlash(relPath)

	cmd := exec.Command("git", "show", "HEAD:"+gitRelPath)
	cmd.Dir = absRepo
	oldBytes, err := cmd.Output()
	if err != nil {
		// Differentiate newly created file from git execution error
		lsCmd := exec.Command("git", "ls-files", "--error-unmatch", gitRelPath)
		lsCmd.Dir = absRepo
		if lsErr := lsCmd.Run(); lsErr != nil {
			// Untracked in git repository: treat as newly created file
			return 1.0, nil
		}
		// File is tracked in index or working tree, check if HEAD exists
		revCmd := exec.Command("git", "rev-parse", "--verify", "HEAD")
		revCmd.Dir = absRepo
		if revErr := revCmd.Run(); revErr != nil {
			// Initial commit or no HEAD yet: newly created
			return 1.0, nil
		}
		return 0.0, fmt.Errorf("git show HEAD:%s failed: %w", gitRelPath, err)
	}

	newBytes, err := os.ReadFile(target)
	if err != nil {
		return 0.0, fmt.Errorf("read current file: %w", err)
	}

	return CalculateACIFromSource(string(oldBytes), string(newBytes), targetSymbol)
}

func findDeclaration(file *ast.File, symbol string) ast.Node {
	if symbol == "" || symbol == "package" {
		return file
	}
	var target ast.Node
	ast.Inspect(file, func(n ast.Node) bool {
		switch d := n.(type) {
		case *ast.FuncDecl:
			if d.Name.Name == symbol {
				target = d
				return false
			}
		case *ast.TypeSpec:
			if d.Name.Name == symbol {
				target = d
				return false
			}
		}
		return true
	})
	return target
}

func countASTNodes(node ast.Node) int {
	if node == nil {
		return 0
	}
	count := 0
	ast.Inspect(node, func(n ast.Node) bool {
		if n != nil {
			count++
		}
		return true
	})
	return count
}

func fallbackTextEditDistance(a, b string) float64 {
	aLines := strings.Split(strings.TrimSpace(a), "\n")
	bLines := strings.Split(strings.TrimSpace(b), "\n")

	if len(aLines) == 0 && len(bLines) == 0 {
		return 0.0
	}
	common := 0
	seen := make(map[string]bool)
	for _, l := range aLines {
		seen[strings.TrimSpace(l)] = true
	}
	for _, l := range bLines {
		if seen[strings.TrimSpace(l)] {
			common++
		}
	}
	total := len(aLines) + len(bLines)
	if total == 0 {
		return 0.0
	}
	similarity := (2.0 * float64(common)) / float64(total)
	return math.Max(0.0, 1.0-similarity)
}
