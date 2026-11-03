package assess

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"

	"github.com/jadmadi/sila/internal/render"
)

// MissingDocSymbol represents an exported Go declaration lacking a proper Go doc comment.
type MissingDocSymbol struct {
	Package string `json:"package"`
	File    string `json:"file"`
	Line    int    `json:"line"`
	Kind    string `json:"kind"` // "package", "func", "type", "method", "const", "var"
	Name    string `json:"name"`
	Detail  string `json:"detail"`
}

// DocCoverageReport contains the results of scanning exported Go symbols for doc comments.
type DocCoverageReport struct {
	Clean         bool               `json:"clean"`
	TotalExported int                `json:"total_exported"`
	Documented    int                `json:"documented"`
	MissingCount  int                `json:"missing_count"`
	CoveragePct   float64            `json:"coverage_pct"`
	Missing       []MissingDocSymbol `json:"missing"`
}

// DocLintOptions controls AST scanning behavior.
type DocLintOptions struct {
	RequireNamePrefix bool     // If true, doc comment must begin with the symbol name
	ExcludeDirs       []string // Subdirectories to skip (e.g. vendor, testdata)
	TargetDirs        []string // Directories to inspect (default: cmd, internal)
}

// DefaultDocLintOptions returns recommended options for Go public API readiness.
func DefaultDocLintOptions() DocLintOptions {
	return DocLintOptions{
		RequireNamePrefix: true,
		ExcludeDirs:       []string{"vendor", "testdata", "third_party", ".ui-craft"},
		TargetDirs:        []string{"cmd", "internal"},
	}
}

// AuditDocCoverage scans Go source packages in repoDir and evaluates doc-comment completeness.
func AuditDocCoverage(repoDir string, opts DocLintOptions) (*DocCoverageReport, error) {
	if repoDir == "" {
		var err error
		repoDir, err = os.Getwd()
		if err != nil {
			return nil, err
		}
	}

	if len(opts.TargetDirs) == 0 {
		opts.TargetDirs = []string{"cmd", "internal"}
	}

	report := &DocCoverageReport{
		Clean:   true,
		Missing: []MissingDocSymbol{},
	}

	fset := token.NewFileSet()

	for _, targetDir := range opts.TargetDirs {
		fullTarget := filepath.Join(repoDir, targetDir)
		if _, err := os.Stat(fullTarget); err != nil {
			continue
		}

		err := filepath.Walk(fullTarget, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if info.IsDir() {
				base := info.Name()
				for _, excl := range opts.ExcludeDirs {
					if base == excl {
						return filepath.SkipDir
					}
				}
				return nil
			}

			// Only inspect non-test Go source files
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}

			relPath, _ := filepath.Rel(repoDir, path)
			relPath = filepath.ToSlash(relPath)

			fileAst, parseErr := parser.ParseFile(fset, path, nil, parser.ParseComments)
			if parseErr != nil {
				return nil
			}

			pkgName := fileAst.Name.Name
			auditFileDecls(fset, relPath, pkgName, fileAst, opts, report)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	if report.TotalExported > 0 {
		report.CoveragePct = float64(report.Documented) / float64(report.TotalExported) * 100.0
	} else {
		report.CoveragePct = 100.0
	}

	report.MissingCount = len(report.Missing)
	if report.MissingCount > 0 {
		report.Clean = false
	}

	return report, nil
}

func auditFileDecls(fset *token.FileSet, filePath, pkgName string, file *ast.File, opts DocLintOptions, report *DocCoverageReport) {
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if !ast.IsExported(d.Name.Name) {
				continue
			}

			report.TotalExported++
			kind := "func"
			if d.Recv != nil && len(d.Recv.List) > 0 {
				kind = "method"
			}

			docText := strings.TrimSpace(d.Doc.Text())
			line := fset.Position(d.Pos()).Line

			if docText == "" {
				report.Missing = append(report.Missing, MissingDocSymbol{
					Package: pkgName,
					File:    filePath,
					Line:    line,
					Kind:    kind,
					Name:    d.Name.Name,
					Detail:  fmt.Sprintf("exported %s %s lacks a doc comment", kind, d.Name.Name),
				})
			} else if opts.RequireNamePrefix && !strings.HasPrefix(docText, d.Name.Name) {
				report.Missing = append(report.Missing, MissingDocSymbol{
					Package: pkgName,
					File:    filePath,
					Line:    line,
					Kind:    kind,
					Name:    d.Name.Name,
					Detail:  fmt.Sprintf("doc comment for %s %s should start with %q", kind, d.Name.Name, d.Name.Name),
				})
			} else {
				report.Documented++
			}

		case *ast.GenDecl:
			// Types, Consts, Vars
			switch d.Tok {
			case token.TYPE:
				for _, spec := range d.Specs {
					ts, ok := spec.(*ast.TypeSpec)
					if !ok || !ast.IsExported(ts.Name.Name) {
						continue
					}

					report.TotalExported++
					line := fset.Position(ts.Pos()).Line

					// Doc comment can be on GenDecl or TypeSpec
					docText := strings.TrimSpace(d.Doc.Text())
					if docText == "" && ts.Doc != nil {
						docText = strings.TrimSpace(ts.Doc.Text())
					}

					if docText == "" {
						report.Missing = append(report.Missing, MissingDocSymbol{
							Package: pkgName,
							File:    filePath,
							Line:    line,
							Kind:    "type",
							Name:    ts.Name.Name,
							Detail:  fmt.Sprintf("exported type %s lacks a doc comment", ts.Name.Name),
						})
					} else if opts.RequireNamePrefix && !strings.HasPrefix(docText, ts.Name.Name) {
						report.Missing = append(report.Missing, MissingDocSymbol{
							Package: pkgName,
							File:    filePath,
							Line:    line,
							Kind:    "type",
							Name:    ts.Name.Name,
							Detail:  fmt.Sprintf("doc comment for type %s should start with %q", ts.Name.Name, ts.Name.Name),
						})
					} else {
						report.Documented++
					}
				}

			case token.CONST, token.VAR:
				for _, spec := range d.Specs {
					vs, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for _, name := range vs.Names {
						if !ast.IsExported(name.Name) {
							continue
						}

						report.TotalExported++
						line := fset.Position(name.Pos()).Line

						docText := strings.TrimSpace(d.Doc.Text())
						if docText == "" && vs.Doc != nil {
							docText = strings.TrimSpace(vs.Doc.Text())
						}

						kind := "const"
						if d.Tok == token.VAR {
							kind = "var"
						}

						if docText == "" {
							report.Missing = append(report.Missing, MissingDocSymbol{
								Package: pkgName,
								File:    filePath,
								Line:    line,
								Kind:    kind,
								Name:    name.Name,
								Detail:  fmt.Sprintf("exported %s %s lacks a doc comment", kind, name.Name),
							})
						} else {
							report.Documented++
						}
					}
				}
			}
		}
	}
}

// RenderDocCoverageTOON produces compact Token-Oriented Object Notation (TOON) for doc coverage reports.
func RenderDocCoverageTOON(report *DocCoverageReport) string {
	var b strings.Builder
	summaryFields := [][2]string{
		{"clean", fmt.Sprintf("%t", report.Clean)},
		{"total_exported", fmt.Sprintf("%d", report.TotalExported)},
		{"documented", fmt.Sprintf("%d", report.Documented)},
		{"missing_count", fmt.Sprintf("%d", report.MissingCount)},
		{"coverage_pct", fmt.Sprintf("%.1f%%", report.CoveragePct)},
	}
	b.WriteString(render.FormatTOONObject("doc_coverage_summary", summaryFields))

	if len(report.Missing) > 0 {
		headers := []string{"package", "symbol", "kind", "location", "detail"}
		var rows [][]string
		for _, m := range report.Missing {
			loc := fmt.Sprintf("%s:%d", m.File, m.Line)
			rows = append(rows, []string{
				m.Package,
				m.Name,
				m.Kind,
				loc,
				m.Detail,
			})
		}
		b.WriteString(render.FormatTOONTable("missing_docs", headers, rows))
	}

	return b.String()
}
