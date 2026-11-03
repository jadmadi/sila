package symbols

import (
	"bytes"
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// MapOptions configures repository symbol mapping and character budgets.
type MapOptions struct {
	RootPath string
	MaxChars int // Default: 2000
	MaxFiles int // Default: 4000
}

// DirectorySymbols groups discovered exported symbols under a directory.
type DirectorySymbols struct {
	Dir     string   `json:"dir"`
	Symbols []string `json:"symbols"`
}

// CodebaseMap captures a high-level table-of-contents symbol digest.
type CodebaseMap struct {
	Directories []DirectorySymbols `json:"directories"`
	Formatted   string             `json:"formatted"`
	TotalDirs   int                `json:"total_dirs"`
	ListedDirs  int                `json:"listed_dirs"`
	OmittedDirs int                `json:"omitted_dirs"`
}

var (
	srcExtRegex = regexp.MustCompile(`\.(go|ts|tsx|js|jsx|mjs|cjs|py|rs|rb)$`)
	skipDirRe   = regexp.MustCompile(`(^|[/\\])(node_modules|\.git|dist|build|out|vendor|venv|\.venv|__pycache__|coverage|\.next|target|migrations|versions|fixtures|tests?|__tests__|spec|rm)($|[/\\])`)
	skipFileRe  = regexp.MustCompile(`(^|[/\\])(test_[^/\\]*|[^/\\]*(_test|\.test|\.spec|\.gen|\.min|\.d)\.[a-z]+)$`)
	sharedDirRe = regexp.MustCompile(`(?i)(util|helper|lib|common|shared|service|component|hook|core|model|schema|api|store|internal)`)

	jsExportFuncOrClassRe = regexp.MustCompile(`(?m)^export\s+(?:default\s+)?(?:async\s+)?(?:function\*?|class|const|let|var|interface|type|enum)\s+([A-Za-z_$][\w$]*)`)
	jsExportBraceRe       = regexp.MustCompile(`(?m)^export\s*\{([^}]*)\}`)
	pyDefOrClassRe        = regexp.MustCompile(`(?m)^(?:async\s+)?(?:def|class)\s+([A-Za-z]\w*)`)
	rsPubItemRe           = regexp.MustCompile(`(?m)^pub\s+(?:async\s+)?(?:fn|struct|enum|trait|type)\s+(\w+)`)
	rbDefOrClassRe        = regexp.MustCompile(`(?m)^\s*(?:def|class|module)\s+([A-Za-z][\w.]*)`)
)

// BuildCodebaseMap scans repository source files and emits a token-budgeted directory symbol digest.
func BuildCodebaseMap(opts MapOptions) (*CodebaseMap, error) {
	root := opts.RootPath
	if root == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		root = cwd
	}
	maxChars := opts.MaxChars
	if maxChars <= 0 {
		maxChars = 2000
	}
	maxFiles := opts.MaxFiles
	if maxFiles <= 0 {
		maxFiles = 4000
	}

	files := listSourceFiles(root, maxFiles)
	if len(files) == 0 {
		return &CodebaseMap{}, nil
	}

	// Group symbols by relative directory
	dirMap := make(map[string][]string)
	fset := token.NewFileSet()

	for _, relFile := range files {
		absPath := filepath.Join(root, relFile)
		symbols := extractFileSymbols(fset, absPath, relFile)
		if len(symbols) == 0 {
			continue
		}
		dir := filepath.ToSlash(filepath.Dir(relFile))
		if dir == "." {
			dir = ""
		}
		dirMap[dir] = append(dirMap[dir], symbols...)
	}

	if len(dirMap) == 0 {
		return &CodebaseMap{}, nil
	}

	// Deduplicate symbols per directory
	type dirEntry struct {
		dir      string
		symbols  []string
		isShared bool
	}
	var entries []dirEntry
	for d, syms := range dirMap {
		unique := make([]string, 0, len(syms))
		seen := make(map[string]bool)
		for _, s := range syms {
			if !seen[s] && s != "" && !strings.HasPrefix(s, "_") {
				seen[s] = true
				unique = append(unique, s)
			}
		}
		if len(unique) > 0 {
			entries = append(entries, dirEntry{
				dir:      d,
				symbols:  unique,
				isShared: sharedDirRe.MatchString(d),
			})
		}
	}

	// Sort directories: shared-first, then alphabetical
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].isShared != entries[j].isShared {
			return entries[i].isShared
		}
		return entries[i].dir < entries[j].dir
	})

	totalDirs := len(entries)
	var listedDirs []DirectorySymbols
	var lines []string
	usedChars := 0
	omittedDirs := 0

	header := "**Codebase Map (Key symbols by directory; reuse before you build)**:\n"
	budget := maxChars - len(header) - 50 // Reserve for omission note

	for _, entry := range entries {
		// Cap symbols per directory to keep lines concise
		symLimit := 25
		if len(entry.symbols) > 8 {
			symLimit = 15
		}
		selected := entry.symbols
		if len(selected) > symLimit {
			selected = selected[:symLimit]
		}

		displayDir := entry.dir
		if displayDir == "" {
			displayDir = "."
		}
		moreNotice := ""
		if len(entry.symbols) > len(selected) {
			moreNotice = ", ..."
		}
		line := "• `" + displayDir + "/`: " + strings.Join(selected, ", ") + moreNotice

		if usedChars+len(line)+1 > budget {
			omittedDirs++
			continue
		}

		lines = append(lines, line)
		usedChars += len(line) + 1
		listedDirs = append(listedDirs, DirectorySymbols{
			Dir:     displayDir,
			Symbols: selected,
		})
	}

	var formatted strings.Builder
	formatted.WriteString(header)
	for _, l := range lines {
		formatted.WriteString(l)
		formatted.WriteByte('\n')
	}
	if omittedDirs > 0 {
		formatted.WriteString("• ... (" + stringFromInt(omittedDirs) + " more directories not listed)\n")
	}

	return &CodebaseMap{
		Directories: listedDirs,
		Formatted:   formatted.String(),
		TotalDirs:   totalDirs,
		ListedDirs:  len(listedDirs),
		OmittedDirs: omittedDirs,
	}, nil
}

func listSourceFiles(root string, maxFiles int) []string {
	// Attempt fast git ls-files first
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", "ls-files")
	cmd.Dir = root
	out, err := cmd.Output()
	if err == nil && len(out) > 0 {
		lines := strings.Split(string(out), "\n")
		var matched []string
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			norm := filepath.ToSlash(line)
			if srcExtRegex.MatchString(norm) && !skipDirRe.MatchString(norm) && !skipFileRe.MatchString(norm) {
				matched = append(matched, norm)
				if len(matched) >= maxFiles {
					break
				}
			}
		}
		if len(matched) > 0 {
			return matched
		}
	}

	// Fallback to directory walk
	var matched []string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil || rel == "." {
			return nil
		}
		norm := filepath.ToSlash(rel)
		if d.IsDir() {
			if d.Name() != "." && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			if skipDirRe.MatchString(norm + "/") {
				return filepath.SkipDir
			}
			return nil
		}
		if srcExtRegex.MatchString(norm) && !skipDirRe.MatchString(norm) && !skipFileRe.MatchString(norm) {
			matched = append(matched, norm)
			if len(matched) >= maxFiles {
				return filepath.SkipAll
			}
		}
		return nil
	})

	return matched
}

func extractFileSymbols(fset *token.FileSet, absPath, relFile string) []string {
	ext := filepath.Ext(relFile)
	if ext == ".go" {
		return extractGoSymbols(fset, absPath)
	}

	content, err := os.ReadFile(absPath)
	if err != nil || len(content) == 0 {
		return nil
	}
	// Limit content scan to first 64KB for speed
	if len(content) > 65536 {
		content = content[:65536]
	}

	switch ext {
	case ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs":
		return extractJSSymbols(content)
	case ".py":
		return extractPySymbols(content)
	case ".rs":
		return extractRsSymbols(content)
	case ".rb":
		return extractRbSymbols(content)
	}
	return nil
}

func extractGoSymbols(fset *token.FileSet, path string) []string {
	node, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil || node == nil {
		return nil
	}

	var symbols []string
	for _, decl := range node.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Name != nil && ast.IsExported(d.Name.Name) {
				// Standalone exported function
				if d.Recv == nil {
					symbols = append(symbols, d.Name.Name)
				}
			}
		case *ast.GenDecl:
			if d.Tok == token.TYPE {
				for _, spec := range d.Specs {
					if ts, ok := spec.(*ast.TypeSpec); ok && ts.Name != nil {
						if ast.IsExported(ts.Name.Name) {
							symbols = append(symbols, ts.Name.Name)
						}
					}
				}
			}
		}
	}
	return symbols
}

func extractJSSymbols(content []byte) []string {
	var symbols []string
	matches := jsExportFuncOrClassRe.FindAllSubmatch(content, -1)
	for _, m := range matches {
		if len(m) > 1 {
			symbols = append(symbols, string(m[1]))
		}
	}
	braceMatches := jsExportBraceRe.FindAllSubmatch(content, -1)
	for _, bm := range braceMatches {
		if len(bm) > 1 {
			parts := bytes.Split(bm[1], []byte(","))
			for _, p := range parts {
				item := strings.TrimSpace(string(p))
				if idx := strings.Index(item, " as "); idx != -1 {
					item = strings.TrimSpace(item[idx+4:])
				}
				if item != "" && !strings.ContainsAny(item, " \t\r\n") {
					symbols = append(symbols, item)
				}
			}
		}
	}
	return symbols
}

func extractPySymbols(content []byte) []string {
	var symbols []string
	matches := pyDefOrClassRe.FindAllSubmatch(content, -1)
	for _, m := range matches {
		if len(m) > 1 {
			name := string(m[1])
			if !strings.HasPrefix(name, "_") {
				symbols = append(symbols, name)
			}
		}
	}
	return symbols
}

func extractRsSymbols(content []byte) []string {
	var symbols []string
	matches := rsPubItemRe.FindAllSubmatch(content, -1)
	for _, m := range matches {
		if len(m) > 1 {
			symbols = append(symbols, string(m[1]))
		}
	}
	return symbols
}

func extractRbSymbols(content []byte) []string {
	var symbols []string
	matches := rbDefOrClassRe.FindAllSubmatch(content, -1)
	for _, m := range matches {
		if len(m) > 1 {
			name := string(m[1])
			if !strings.HasPrefix(name, "_") {
				symbols = append(symbols, name)
			}
		}
	}
	return symbols
}

func stringFromInt(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	for n > 0 {
		b = append(b, byte('0'+n%10))
		n /= 10
	}
	if neg {
		b = append(b, '-')
	}
	for i, j := 0, len(b)-1; i < j; i, j = i+1, j-1 {
		b[i], b[j] = b[j], b[i]
	}
	return string(b)
}
