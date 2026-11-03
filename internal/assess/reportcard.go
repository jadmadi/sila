package assess

import (
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/jadmadi/sila/internal/render"
)

// ComponentGrade represents the evaluated score and diagnostics for a single report card tool.
type ComponentGrade struct {
	Name   string   `json:"name"`
	Weight float64  `json:"weight"`
	Score  float64  `json:"score"`
	Passed bool     `json:"passed"`
	Detail string   `json:"detail"`
	Issues []string `json:"issues"`
}

// ReportCard aggregates local code quality scores across multiple static analysis dimensions.
type ReportCard struct {
	Grade        string           `json:"grade"` // "A+", "A", "B", "C", "D", "F"
	Score        float64          `json:"score"` // 0.0 - 100.0
	Clean        bool             `json:"clean"` // true if grade is A+ or A (score >= 90.0)
	FilesScanned int              `json:"files_scanned"`
	Components   []ComponentGrade `json:"components"`
}

// Common programmer typos for misspell static analysis.
var commonTypos = map[string]string{
	"teh":           "the",
	"recieve":       "receive",
	"recieved":      "received",
	"seperate":      "separate",
	"seperated":     "separated",
	"occurance":     "occurrence",
	"refering":      "referring",
	"inconsistant":  "inconsistent",
	"dependancy":    "dependency",
	"dependancies":  "dependencies",
	"paramater":     "parameter",
	"paramaters":    "parameters",
	"retun":         "return",
	"calback":       "callback",
	"unexcepted":    "unexpected",
	"unkown":        "unknown",
	"cancelling":    "canceling",
	"acheive":       "achieve",
	"adress":        "address",
	"allign":        "align",
	"appropiate":    "appropriate",
	"availabe":      "available",
	"compatability": "compatibility",
	"definately":    "definitely",
	"existance":     "existence",
	"implmentation": "implementation",
	"inherant":      "inherent",
	"maintainance":  "maintenance",
	"occured":       "occurred",
	"preceeding":    "preceding",
	"refered":       "referred",
	"succesful":     "successful",
	"threshhold":    "threshold",
	"truely":        "truly",
	"writting":      "writing",
}

var wordTokenRe = regexp.MustCompile(`\b[a-zA-Z]{3,}\b`)

// AuditReportCard evaluates repository code quality across gofmt, govet, gocyclo, ineffassign, and misspell.
func AuditReportCard(repoDir string) (*ReportCard, error) {
	if repoDir == "" {
		var err error
		repoDir, err = os.Getwd()
		if err != nil {
			return nil, err
		}
	}

	targetDirs := []string{"cmd", "internal"}
	var goFiles []string
	fset := token.NewFileSet()

	for _, d := range targetDirs {
		fullDir := filepath.Join(repoDir, d)
		if _, err := os.Stat(fullDir); err != nil {
			continue
		}
		_ = filepath.Walk(fullDir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
				goFiles = append(goFiles, path)
			}
			return nil
		})
	}

	gofmtComp := auditGofmt(repoDir, goFiles)
	govetComp := auditGovet(repoDir)
	gocycloComp := auditGocyclo(repoDir, goFiles, fset, 15)
	ineffComp := auditIneffassign(repoDir, goFiles, fset)
	misspellComp := auditMisspell(repoDir, goFiles)

	components := []ComponentGrade{
		gofmtComp,
		govetComp,
		gocycloComp,
		ineffComp,
		misspellComp,
	}

	totalScore := 0.0
	for _, c := range components {
		totalScore += c.Score * c.Weight
	}

	grade := "F"
	switch {
	case totalScore >= 95.0:
		grade = "A+"
	case totalScore >= 90.0:
		grade = "A"
	case totalScore >= 80.0:
		grade = "B"
	case totalScore >= 70.0:
		grade = "C"
	case totalScore >= 60.0:
		grade = "D"
	}

	return &ReportCard{
		Grade:        grade,
		Score:        totalScore,
		Clean:        totalScore >= 90.0,
		FilesScanned: len(goFiles),
		Components:   components,
	}, nil
}

func auditGofmt(repoDir string, files []string) ComponentGrade {
	comp := ComponentGrade{
		Name:   "gofmt",
		Weight: 0.20,
		Passed: true,
	}
	if len(files) == 0 {
		comp.Score = 100.0
		comp.Detail = "no files to format"
		return comp
	}

	var unformatted []string
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		formatted, err := format.Source(data)
		if err != nil {
			continue
		}
		if string(data) != string(formatted) {
			rel, _ := filepath.Rel(repoDir, f)
			unformatted = append(unformatted, filepath.ToSlash(rel))
		}
	}

	comp.Issues = unformatted
	if len(unformatted) == 0 {
		comp.Score = 100.0
		comp.Detail = fmt.Sprintf("100%% formatted (%d files clean)", len(files))
	} else {
		comp.Passed = false
		comp.Score = float64(len(files)-len(unformatted)) / float64(len(files)) * 100.0
		comp.Detail = fmt.Sprintf("%d file(s) require gofmt formatting", len(unformatted))
	}
	return comp
}

func auditGovet(repoDir string) ComponentGrade {
	comp := ComponentGrade{
		Name:   "govet",
		Weight: 0.25,
		Passed: true,
	}

	cmd := exec.Command("go", "vet", "./cmd/...", "./internal/...")
	cmd.Dir = repoDir
	out, err := cmd.CombinedOutput()
	outStr := strings.TrimSpace(string(out))

	if err == nil && len(outStr) == 0 {
		comp.Score = 100.0
		comp.Detail = "clean (0 vet diagnostics)"
		return comp
	}

	lines := strings.Split(outStr, "\n")
	var issues []string
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if trimmed != "" {
			issues = append(issues, trimmed)
		}
	}

	comp.Issues = issues
	comp.Passed = false
	deduction := float64(len(issues)) * 10.0
	if deduction > 100.0 {
		deduction = 100.0
	}
	comp.Score = 100.0 - deduction
	comp.Detail = fmt.Sprintf("%d vet diagnostic(s) detected", len(issues))
	return comp
}

func auditGocyclo(repoDir string, files []string, fset *token.FileSet, maxComplexity int) ComponentGrade {
	comp := ComponentGrade{
		Name:   "gocyclo",
		Weight: 0.20,
		Passed: true,
	}
	if len(files) == 0 {
		comp.Score = 100.0
		comp.Detail = "no functions scanned"
		return comp
	}

	totalFuncs := 0
	var complexFuncs []string

	for _, path := range files {
		fileAst, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			continue
		}

		rel, _ := filepath.Rel(repoDir, path)
		rel = filepath.ToSlash(rel)

		for _, decl := range fileAst.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			totalFuncs++
			complexity := 1

			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.IfStmt, *ast.ForStmt, *ast.RangeStmt:
					complexity++
				case *ast.CaseClause:
					if len(x.List) > 0 {
						complexity++
					}
				case *ast.CommClause:
					if x.Comm != nil {
						complexity++
					}
				case *ast.BinaryExpr:
					if x.Op == token.LAND || x.Op == token.LOR {
						complexity++
					}
				}
				return true
			})

			if complexity > maxComplexity {
				line := fset.Position(fn.Pos()).Line
				complexFuncs = append(complexFuncs,
					fmt.Sprintf("%s:%d %s (complexity %d > %d)", rel, line, fn.Name.Name, complexity, maxComplexity))
			}
		}
	}

	comp.Issues = complexFuncs
	if totalFuncs == 0 {
		comp.Score = 100.0
		comp.Detail = "clean (0 functions)"
		return comp
	}

	cleanFuncs := totalFuncs - len(complexFuncs)
	comp.Score = float64(cleanFuncs) / float64(totalFuncs) * 100.0
	if len(complexFuncs) == 0 {
		comp.Detail = fmt.Sprintf("100%% within complexity limits (%d functions)", totalFuncs)
	} else {
		if comp.Score < 85.0 {
			comp.Passed = false
		}
		comp.Detail = fmt.Sprintf("%d/%d functions exceed complexity threshold %d (%.1f%% clean)",
			len(complexFuncs), totalFuncs, maxComplexity, comp.Score)
	}
	return comp
}

func auditIneffassign(repoDir string, files []string, fset *token.FileSet) ComponentGrade {
	comp := ComponentGrade{
		Name:   "ineffassign",
		Weight: 0.20,
		Passed: true,
	}

	var issues []string
	for _, path := range files {
		fileAst, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			continue
		}

		rel, _ := filepath.Rel(repoDir, path)
		rel = filepath.ToSlash(rel)

		ast.Inspect(fileAst, func(n ast.Node) bool {
			block, ok := n.(*ast.BlockStmt)
			if !ok {
				return true
			}

			// Scan consecutive statements in block for immediately overwritten simple assignments
			for i := 0; i < len(block.List)-1; i++ {
				as1, ok1 := block.List[i].(*ast.AssignStmt)
				as2, ok2 := block.List[i+1].(*ast.AssignStmt)
				if !ok1 || !ok2 {
					continue
				}

				if len(as1.Lhs) == 1 && len(as2.Lhs) == 1 {
					id1, ok1 := as1.Lhs[0].(*ast.Ident)
					id2, ok2 := as2.Lhs[0].(*ast.Ident)
					if ok1 && ok2 && id1.Name == id2.Name && id1.Name != "_" {
						// Verify as2.Rhs doesn't use id1
						usesId1 := false
						for _, r := range as2.Rhs {
							ast.Inspect(r, func(sub ast.Node) bool {
								if subId, ok := sub.(*ast.Ident); ok && subId.Name == id1.Name {
									usesId1 = true
								}
								return true
							})
						}
						if !usesId1 {
							line := fset.Position(as1.Pos()).Line
							issues = append(issues, fmt.Sprintf("%s:%d variable %q overwritten on next statement without being read", rel, line, id1.Name))
						}
					}
				}
			}
			return true
		})
	}

	comp.Issues = issues
	if len(issues) == 0 {
		comp.Score = 100.0
		comp.Detail = "clean (0 ineffectual assignments)"
	} else {
		comp.Passed = false
		deduction := float64(len(issues)) * 5.0
		if deduction > 100.0 {
			deduction = 100.0
		}
		comp.Score = 100.0 - deduction
		comp.Detail = fmt.Sprintf("%d ineffectual assignment(s) detected", len(issues))
	}
	return comp
}

func auditMisspell(repoDir string, files []string) ComponentGrade {
	comp := ComponentGrade{
		Name:   "misspell",
		Weight: 0.15,
		Passed: true,
	}

	var issues []string
	fset := token.NewFileSet()

	for _, path := range files {
		fileAst, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			continue
		}

		rel, _ := filepath.Rel(repoDir, path)
		rel = filepath.ToSlash(rel)

		// Check comments
		for _, cg := range fileAst.Comments {
			for _, c := range cg.List {
				words := wordTokenRe.FindAllString(c.Text, -1)
				for _, w := range words {
					lower := strings.ToLower(w)
					if correction, found := commonTypos[lower]; found {
						line := fset.Position(c.Pos()).Line
						issues = append(issues, fmt.Sprintf("%s:%d %q -> %q", rel, line, w, correction))
					}
				}
			}
		}
	}

	comp.Issues = issues
	if len(issues) == 0 {
		comp.Score = 100.0
		comp.Detail = "clean (0 spelling errors)"
	} else {
		deduction := float64(len(issues)) * 2.0
		if deduction > 100.0 {
			deduction = 100.0
		}
		comp.Score = 100.0 - deduction
		if comp.Score < 90.0 {
			comp.Passed = false
		}
		comp.Detail = fmt.Sprintf("%d typo(s) detected in comments", len(issues))
	}
	return comp
}

// RenderReportCardTOON produces compact Token-Oriented Object Notation for a report card.
func RenderReportCardTOON(rc *ReportCard) string {
	var b strings.Builder
	summaryFields := [][2]string{
		{"grade", rc.Grade},
		{"score", fmt.Sprintf("%.1f", rc.Score)},
		{"clean", fmt.Sprintf("%t", rc.Clean)},
		{"files_scanned", fmt.Sprintf("%d", rc.FilesScanned)},
	}
	b.WriteString(render.FormatTOONObject("report_card_summary", summaryFields))

	headers := []string{"component", "score", "weight", "passed", "detail"}
	var rows [][]string
	for _, c := range rc.Components {
		rows = append(rows, []string{
			c.Name,
			fmt.Sprintf("%.1f", c.Score),
			fmt.Sprintf("%.2f", c.Weight),
			fmt.Sprintf("%t", c.Passed),
			c.Detail,
		})
	}
	b.WriteString(render.FormatTOONTable("components", headers, rows))

	return b.String()
}
