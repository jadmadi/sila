package lint

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// LiteralViolation represents a bare magic string or un-factored sentinel literal detected in code.
type LiteralViolation struct {
	Literal    string   `json:"literal"`
	Count      int      `json:"count"`
	Locations  []string `json:"locations"`
	Category   string   `json:"category"`
	Suggestion string   `json:"suggestion"`
}

// LintReport summarizes the results of a protected literals AST scan.
type LintReport struct {
	Violations    []LiteralViolation `json:"violations"`
	FilesScanned  int                `json:"files_scanned"`
	TotalLiterals int                `json:"total_literals"`
	Clean         bool               `json:"clean"`
	Duration      time.Duration      `json:"duration"`
	DurationMS    int64              `json:"duration_ms"`
}

// Config defines filtering rules and thresholds for literal hygiene auditing.
type Config struct {
	MinLength         int
	MinOccurrences    int
	ExemptTests       bool
	ExemptSQL         bool
	ExemptFormatVerbs bool
	IgnoredLiterals   map[string]bool
}

// DefaultConfig provides standard production defaults for Go codebases.
func DefaultConfig() Config {
	return Config{
		MinLength:         4,
		MinOccurrences:    4, // strings repeated >= 4 times outside consts are flagged
		ExemptTests:       true,
		ExemptSQL:         true,
		ExemptFormatVerbs: true,
		IgnoredLiterals: map[string]bool{
			"true": true, "false": true, "null": true, "undefined": true,
			"json": true, "yaml": true, "text": true, "html": true,
			"path": true, "user": true, "none": true, "done": true,
			"open": true, "pass": true, "fail": true, "skip": true,
			"auto": true, "main": true, "test": true, "help": true,
			"GET": true, "POST": true, "PUT": true, "DELETE": true,
			"HEAD": true, "OPTIONS": true, "PATCH": true,
			"application/json": true, "text/plain": true,
			"Usage": true, "Commands": true, "Options": true,
			"Description": true, "Examples": true, "Notes": true,
			"Arguments": true, "Hints": true, "Summary": true,
		},
	}
}

// ScanDir walks a directory hierarchy and runs AST-based literal hygiene checks.
func ScanDir(root string, cfg Config) (*LintReport, error) {
	start := time.Now()
	var goFiles []string

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if path != root {
				base := info.Name()
				if strings.HasPrefix(base, ".") || base == "vendor" || base == "node_modules" || base == "testdata" {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if strings.HasSuffix(path, ".go") {
			if cfg.ExemptTests && strings.HasSuffix(path, "_test.go") {
				return nil
			}
			goFiles = append(goFiles, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk directory %q: %w", root, err)
	}

	return ScanFiles(goFiles, cfg, start)
}

type occurrence struct {
	file  string
	line  int
	isErr bool
}

// ScanFiles parses the specified Go files and evaluates literal occurrences.
func ScanFiles(files []string, cfg Config, startTime time.Time) (*LintReport, error) {
	if startTime.IsZero() {
		startTime = time.Now()
	}

	fset := token.NewFileSet()
	literalOccurrences := make(map[string][]occurrence)
	totalLiterals := 0

	for _, file := range files {
		node, err := parser.ParseFile(fset, file, nil, parser.ParseComments)
		if err != nil {
			// Skip unparseable generated or foreign files gracefully
			continue
		}

		ast.Inspect(node, func(n ast.Node) bool {
			// Skip package-level const, var, and import declarations
			if decl, ok := n.(*ast.GenDecl); ok && (decl.Tok == token.CONST || decl.Tok == token.VAR || decl.Tok == token.IMPORT) {
				return false
			}
			if _, ok := n.(*ast.ImportSpec); ok {
				return false
			}

			// Check call expressions for errors.New("...")
			if call, ok := n.(*ast.CallExpr); ok {
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
					if pkgIdent, ok := sel.X.(*ast.Ident); ok && pkgIdent.Name == "errors" && sel.Sel.Name == "New" {
						if len(call.Args) > 0 {
							if lit, ok := call.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
								val, err := strconv.Unquote(lit.Value)
								if err == nil && len(val) >= cfg.MinLength {
									pos := fset.Position(lit.Pos())
									literalOccurrences[val] = append(literalOccurrences[val], occurrence{
										file:  pos.Filename,
										line:  pos.Line,
										isErr: true,
									})
								}
							}
						}
					}
				}
			}

			// Inspect all string literals
			if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				val, err := strconv.Unquote(lit.Value)
				if err != nil {
					return true
				}
				totalLiterals++

				if len(val) < cfg.MinLength {
					return true
				}
				if cfg.IgnoredLiterals[val] {
					return true
				}
				if isStructTag(val) {
					return true
				}
				if isDecoration(val) {
					return true
				}
				if cfg.ExemptFormatVerbs && isFormatString(val) {
					return true
				}
				if cfg.ExemptSQL && isSQL(val) {
					return true
				}

				pos := fset.Position(lit.Pos())
				literalOccurrences[val] = append(literalOccurrences[val], occurrence{
					file:  pos.Filename,
					line:  pos.Line,
					isErr: false,
				})
			}
			return true
		})
	}

	violations := make([]LiteralViolation, 0)
	for lit, occs := range literalOccurrences {
		hasErr := false
		for _, o := range occs {
			if o.isErr {
				hasErr = true
				break
			}
		}

		// Bare sentinel error repeated >= 2 times or regular literal repeated >= MinOccurrences
		threshold := cfg.MinOccurrences
		category := "magic_string"
		if hasErr {
			threshold = 2
			category = "sentinel_error"
		}

		if len(occs) >= threshold {
			locs := make([]string, 0, len(occs))
			for _, o := range occs {
				locs = append(locs, fmt.Sprintf("%s:%d", o.file, o.line))
			}

			violations = append(violations, LiteralViolation{
				Literal:    lit,
				Count:      len(occs),
				Locations:  locs,
				Category:   category,
				Suggestion: suggestConstName(lit, hasErr),
			})
		}
	}

	dur := time.Since(startTime)
	return &LintReport{
		Violations:    violations,
		FilesScanned:  len(files),
		TotalLiterals: totalLiterals,
		Clean:         len(violations) == 0,
		Duration:      dur,
		DurationMS:    dur.Milliseconds(),
	}, nil
}

// isFormatString returns true if the literal is a standard printf format string with verbs like %s, %d, %v, %w.
func isFormatString(s string) bool {
	if !strings.Contains(s, "%") {
		return false
	}
	verbs := []string{"%s", "%d", "%v", "%w", "%t", "%f", "%x", "%T", "%q", "%b", "%+v", "%#v", "%p"}
	for _, v := range verbs {
		if strings.Contains(s, v) {
			return true
		}
	}
	return false
}

// isSQL returns true if the string is likely an SQL statement or DDL.
func isSQL(s string) bool {
	upper := strings.ToUpper(strings.TrimSpace(s))
	prefixes := []string{"SELECT ", "INSERT INTO ", "UPDATE ", "DELETE FROM ", "CREATE TABLE ", "CREATE INDEX ", "CREATE VIRTUAL ", "DROP TABLE ", "ALTER TABLE ", "PRAGMA "}
	for _, p := range prefixes {
		if strings.HasPrefix(upper, p) {
			return true
		}
	}
	return false
}

// suggestConstName converts a string literal into an idiomatic Go PascalCase constant identifier.
func suggestConstName(literal string, isErr bool) string {
	if isErr {
		words := strings.FieldsFunc(literal, func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsDigit(r)
		})
		var b strings.Builder
		b.WriteString("Err")
		for _, w := range words {
			if len(w) == 0 {
				continue
			}
			b.WriteString(strings.ToUpper(w[:1]) + strings.ToLower(w[1:]))
		}
		res := b.String()
		if len(res) > 30 {
			res = res[:30]
		}
		return res
	}

	words := strings.FieldsFunc(literal, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	var b strings.Builder
	for _, w := range words {
		if len(w) == 0 {
			continue
		}
		b.WriteString(strings.ToUpper(w[:1]) + strings.ToLower(w[1:]))
	}
	res := b.String()
	if len(res) == 0 {
		return "ConstLiteral"
	}
	if len(res) > 30 {
		res = res[:30]
	}
	return res
}

// isStructTag returns true if the string literal matches Go struct field tag patterns.
func isStructTag(s string) bool {
	tags := []string{"json:", "yaml:", "db:", "schema:", "env:", "flag:"}
	for _, t := range tags {
		if strings.HasPrefix(s, t) {
			return true
		}
	}
	return false
}

// isDecoration returns true if the string is purely horizontal UI lines or delimiters.
func isDecoration(s string) bool {
	clean := strings.TrimSpace(s)
	if len(clean) == 0 {
		return true
	}
	r0 := rune(clean[0])
	if r0 != '-' && r0 != '=' && r0 != '*' && r0 != '_' && r0 != '~' && r0 != '#' {
		return false
	}
	for _, r := range clean {
		if r != r0 && !unicode.IsSpace(r) {
			return false
		}
	}
	return true
}
