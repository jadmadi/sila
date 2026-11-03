package goals

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Standard diagnostic codes required by the task readiness contract.
const (
	DiagAcceptanceUnowned      = "ACCEPTANCE_UNOWNED"
	DiagTaskDependencyCycle    = "TASK_DEPENDENCY_CYCLE"
	DiagTaskDependencyMissing  = "TASK_DEPENDENCY_MISSING"
	DiagReferenceMissing       = "REFERENCE_MISSING"
	DiagReferenceSymbolMissing = "REFERENCE_SYMBOL_MISSING"
	DiagReferenceStale         = "REFERENCE_STALE"
	DiagTaskScopeTooLarge      = "TASK_SCOPE_TOO_LARGE"
	DiagUnresolvedDecision     = "UNRESOLVED_DECISION"
	DiagCheckAssertionsMissing = "CHECK_ASSERTIONS_MISSING"
	DiagSpecReviewMissing      = "SPEC_REVIEW_MISSING"
	DiagSpecReviewStale        = "SPEC_REVIEW_STALE"
	DiagSpecNotReady           = "SPEC_NOT_READY"
)

var placeholderPattern = regexp.MustCompile(`(?i)\b(TODO|TBD)\b`)

// ValidateTaskReadiness performs deterministic verification of a WorkPackage against repository state.
// It returns a deterministically sorted slice of Diagnostic findings.
func ValidateTaskReadiness(rootDir string, pkg *WorkPackage) []Diagnostic {
	if pkg == nil {
		return []Diagnostic{
			{
				Code:        DiagSpecNotReady,
				Severity:    "error",
				Message:     "work package is nil",
				Remediation: "provide a non-nil WorkPackage",
			},
		}
	}

	var diags []Diagnostic
	goalSlug := pkg.GoalSlug

	// 1. Check unresolved decisions
	for _, d := range pkg.UnresolvedDecisions {
		diags = append(diags, Diagnostic{
			Code:        DiagUnresolvedDecision,
			Severity:    "error",
			GoalSlug:    goalSlug,
			Path:        "unresolved_decisions",
			Message:     fmt.Sprintf("unresolved decision %q blocks readiness", d),
			Remediation: "resolve all pending decisions before beginning implementation",
		})
	}

	// 2. Check normative package-level placeholders
	if placeholderPattern.MatchString(pkg.Purpose) {
		diags = append(diags, Diagnostic{
			Code:        DiagSpecNotReady,
			Severity:    "error",
			GoalSlug:    goalSlug,
			Path:        "purpose",
			Message:     "purpose contains unresolved placeholder (TODO/TBD)",
			Remediation: "replace placeholder with settled purpose description",
		})
	}

	// 3. Task and acceptance indexing
	taskIDs := make(map[string]bool)
	for _, t := range pkg.Tasks {
		taskIDs[t.ID] = true
	}

	ownedCases := make(map[string]bool)
	for _, t := range pkg.Tasks {
		if len(t.AcceptanceIDs) == 0 {
			diags = append(diags, Diagnostic{
				Code:        DiagAcceptanceUnowned,
				Severity:    "error",
				GoalSlug:    goalSlug,
				Path:        fmt.Sprintf("tasks[%s].acceptance_ids", t.ID),
				Message:     fmt.Sprintf("task %q must name at least one acceptance case", t.ID),
				Remediation: "assign at least one acceptance case to the task",
			})
		}
		for _, acID := range t.AcceptanceIDs {
			ownedCases[acID] = true
		}
	}

	for _, ac := range pkg.Acceptance {
		if ac.Mandatory && !ownedCases[ac.ID] {
			diags = append(diags, Diagnostic{
				Code:        DiagAcceptanceUnowned,
				Severity:    "error",
				GoalSlug:    goalSlug,
				Path:        fmt.Sprintf("acceptance[%s]", ac.ID),
				Message:     fmt.Sprintf("mandatory acceptance case %q has no owning task", ac.ID),
				Remediation: "assign mandatory acceptance case to at least one task",
			})
		}

		// Normative acceptance placeholders and empty expected results
		if strings.TrimSpace(ac.Expected) == "" {
			diags = append(diags, Diagnostic{
				Code:        DiagSpecNotReady,
				Severity:    "error",
				GoalSlug:    goalSlug,
				Path:        fmt.Sprintf("acceptance[%s].expected", ac.ID),
				Message:     fmt.Sprintf("acceptance case %q has empty expected result", ac.ID),
				Remediation: "specify observable expected outcome",
			})
		}
		for fieldName, val := range map[string]string{
			"requirement": ac.Requirement,
			"given":       ac.Given,
			"action":      ac.Action,
			"expected":    ac.Expected,
		} {
			if placeholderPattern.MatchString(val) {
				diags = append(diags, Diagnostic{
					Code:        DiagSpecNotReady,
					Severity:    "error",
					GoalSlug:    goalSlug,
					Path:        fmt.Sprintf("acceptance[%s].%s", ac.ID, fieldName),
					Message:     fmt.Sprintf("acceptance case %q field %q contains unresolved placeholder (TODO/TBD)", ac.ID, fieldName),
					Remediation: "replace placeholder with concrete acceptance criteria",
				})
			}
		}
	}

	// 4. Task dependency validation and cycle detection
	for _, t := range pkg.Tasks {
		for _, dep := range t.DependsOn {
			if !taskIDs[dep] {
				diags = append(diags, Diagnostic{
					Code:        DiagTaskDependencyMissing,
					Severity:    "error",
					GoalSlug:    goalSlug,
					Path:        fmt.Sprintf("tasks[%s].depends_on", t.ID),
					Message:     fmt.Sprintf("task %q depends on unknown task %q", t.ID, dep),
					Remediation: "remove dependency or declare target task in work package",
				})
			}
		}
	}

	cycleTasks := detectTaskDependencyCycles(pkg.Tasks)
	for _, id := range cycleTasks {
		diags = append(diags, Diagnostic{
			Code:        DiagTaskDependencyCycle,
			Severity:    "error",
			GoalSlug:    goalSlug,
			Path:        fmt.Sprintf("tasks[%s].depends_on", id),
			Message:     fmt.Sprintf("task dependency cycle detected involving task %q", id),
			Remediation: "break dependency cycle to establish a directed acyclic graph",
		})
	}

	// 5. Per-task scope, file boundaries, references, and placeholders
	for _, t := range pkg.Tasks {
		// Normative task placeholders
		for fieldName, val := range map[string]string{
			"title":   t.Title,
			"context": t.Context,
			"why":     t.Why,
		} {
			if placeholderPattern.MatchString(val) {
				diags = append(diags, Diagnostic{
					Code:        DiagSpecNotReady,
					Severity:    "error",
					GoalSlug:    goalSlug,
					Path:        fmt.Sprintf("tasks[%s].%s", t.ID, fieldName),
					Message:     fmt.Sprintf("task %q field %q contains unresolved placeholder (TODO/TBD)", t.ID, fieldName),
					Remediation: "replace placeholder with settled contract text",
				})
			}
		}

		// Steps validation
		for _, s := range t.Steps {
			if strings.TrimSpace(s.Expected) == "" {
				diags = append(diags, Diagnostic{
					Code:        DiagSpecNotReady,
					Severity:    "error",
					GoalSlug:    goalSlug,
					Path:        fmt.Sprintf("tasks[%s].steps[%s].expected", t.ID, s.ID),
					Message:     fmt.Sprintf("step %q of task %q has empty expected result", s.ID, t.ID),
					Remediation: "specify expected result for step",
				})
			}
			for sField, sVal := range map[string]string{
				"action":     s.Action,
				"expected":   s.Expected,
				"on_failure": s.OnFailure,
			} {
				if placeholderPattern.MatchString(sVal) {
					diags = append(diags, Diagnostic{
						Code:        DiagSpecNotReady,
						Severity:    "error",
						GoalSlug:    goalSlug,
						Path:        fmt.Sprintf("tasks[%s].steps[%s].%s", t.ID, s.ID, sField),
						Message:     fmt.Sprintf("step %q of task %q field %q contains unresolved placeholder (TODO/TBD)", s.ID, t.ID, sField),
						Remediation: "replace placeholder with concrete step instructions",
					})
				}
			}
		}

		// Checks assertion requirement
		for _, c := range t.Checks {
			if len(c.Assertions) == 0 {
				diags = append(diags, Diagnostic{
					Code:        DiagCheckAssertionsMissing,
					Severity:    "error",
					GoalSlug:    goalSlug,
					Path:        fmt.Sprintf("tasks[%s].checks[%s]", t.ID, c.ID),
					Message:     fmt.Sprintf("check %q in task %q has no assertions", c.ID, t.ID),
					Remediation: "declare at least one assertion (go_test_pass, stdout_exact, json_pointer_equals)",
				})
			}
		}

		// Scope check: max 5 unique edit/create files
		uniqueTouched := make(map[string]bool)
		editSet := make(map[string]bool)
		for _, p := range t.Files.Edit {
			if p != "" {
				clean := filepath.Clean(p)
				uniqueTouched[clean] = true
				editSet[clean] = true
			}
		}
		for _, p := range t.Files.Create {
			if p != "" {
				clean := filepath.Clean(p)
				uniqueTouched[clean] = true
			}
		}
		if len(uniqueTouched) > 5 {
			diags = append(diags, Diagnostic{
				Code:        DiagTaskScopeTooLarge,
				Severity:    "error",
				GoalSlug:    goalSlug,
				Path:        fmt.Sprintf("tasks[%s].files", t.ID),
				Message:     fmt.Sprintf("task %q touches %d edit/create files (maximum allowed is 5)", t.ID, len(uniqueTouched)),
				Remediation: "decompose task into smaller sequential tasks touching at most 5 files",
			})
		}

		// Read and edit files must exist
		for _, p := range t.Files.Read {
			full := filepath.Join(rootDir, p)
			if !fileExists(full) {
				diags = append(diags, Diagnostic{
					Code:        DiagReferenceMissing,
					Severity:    "error",
					GoalSlug:    goalSlug,
					Path:        fmt.Sprintf("tasks[%s].files.read", t.ID),
					Message:     fmt.Sprintf("read file %q does not exist on disk", p),
					Remediation: "ensure read file exists or remove from read list",
				})
			}
		}
		for _, p := range t.Files.Edit {
			full := filepath.Join(rootDir, p)
			if !fileExists(full) {
				diags = append(diags, Diagnostic{
					Code:        DiagReferenceMissing,
					Severity:    "error",
					GoalSlug:    goalSlug,
					Path:        fmt.Sprintf("tasks[%s].files.edit", t.ID),
					Message:     fmt.Sprintf("edit file %q does not exist on disk", p),
					Remediation: "ensure target file exists before editing, or place in create list",
				})
			}
		}

		// Create files must not already exist unless listed in edit
		for _, p := range t.Files.Create {
			cleanP := filepath.Clean(p)
			if !editSet[cleanP] {
				full := filepath.Join(rootDir, p)
				if fileExists(full) {
					diags = append(diags, Diagnostic{
						Code:        DiagSpecNotReady,
						Severity:    "error",
						GoalSlug:    goalSlug,
						Path:        fmt.Sprintf("tasks[%s].files.create", t.ID),
						Message:     fmt.Sprintf("create file %q already exists on disk and is not listed in edit files", p),
						Remediation: "move existing file to edit list or select an unused path",
					})
				}
			}
		}

		// References validation
		for _, ref := range t.References {
			full := filepath.Join(rootDir, ref.Path)
			data, err := os.ReadFile(full)
			if err != nil {
				diags = append(diags, Diagnostic{
					Code:        DiagReferenceMissing,
					Severity:    "error",
					GoalSlug:    goalSlug,
					Path:        fmt.Sprintf("tasks[%s].references[%s]", t.ID, ref.Path),
					Message:     fmt.Sprintf("referenced file %q does not exist: %v", ref.Path, err),
					Remediation: "create referenced file or update reference path",
				})
				continue
			}

			// Validate file digest if pinned
			if ref.SHA256 != "" {
				h := sha256.Sum256(data)
				actualHex := hex.EncodeToString(h[:])
				if actualHex != strings.ToLower(ref.SHA256) {
					diags = append(diags, Diagnostic{
						Code:        DiagReferenceStale,
						Severity:    "error",
						GoalSlug:    goalSlug,
						Path:        fmt.Sprintf("tasks[%s].references[%s]", t.ID, ref.Path),
						Message:     fmt.Sprintf("reference %q digest mismatch: expected %s, got %s", ref.Path, strings.ToLower(ref.SHA256), actualHex),
						Remediation: "update reference sha256 to current file digest",
					})
				}
			}

			// Validate Go symbol resolution
			if strings.HasSuffix(ref.Path, ".go") && ref.Symbol != "" {
				if !resolveGoSymbolInFile(full, data, ref.Symbol) {
					diags = append(diags, Diagnostic{
						Code:        DiagReferenceSymbolMissing,
						Severity:    "error",
						GoalSlug:    goalSlug,
						Path:        fmt.Sprintf("tasks[%s].references[%s]", t.ID, ref.Path),
						Message:     fmt.Sprintf("symbol %q not found in Go file %q", ref.Symbol, ref.Path),
						Remediation: "verify symbol name against AST declarations in file",
					})
				}
			}

			// Validate excerpt matching
			if ref.Excerpt != "" && !strings.Contains(string(data), ref.Excerpt) {
				diags = append(diags, Diagnostic{
					Code:        DiagReferenceStale,
					Severity:    "error",
					GoalSlug:    goalSlug,
					Path:        fmt.Sprintf("tasks[%s].references[%s]", t.ID, ref.Path),
					Message:     fmt.Sprintf("reference %q excerpt no longer matches file content", ref.Path),
					Remediation: "refresh reference excerpt to match current file contents",
				})
			}
		}
	}

	// Deterministic sorting: task ID, field (path), code, message
	sort.SliceStable(diags, func(i, j int) bool {
		taskI := extractTaskIDFromPath(diags[i].Path)
		taskJ := extractTaskIDFromPath(diags[j].Path)
		if taskI != taskJ {
			return taskI < taskJ
		}
		if diags[i].Path != diags[j].Path {
			return diags[i].Path < diags[j].Path
		}
		if diags[i].Code != diags[j].Code {
			return diags[i].Code < diags[j].Code
		}
		return diags[i].Message < diags[j].Message
	})

	return diags
}

// detectTaskDependencyCycles identifies task IDs involved in dependency cycles.
func detectTaskDependencyCycles(tasks []TaskContract) []string {
	adj := make(map[string][]string)
	for _, t := range tasks {
		adj[t.ID] = t.DependsOn
	}

	state := make(map[string]int) // 0: unvisited, 1: visiting, 2: visited
	cycleIDs := make(map[string]bool)

	var path []string
	var visit func(id string)
	visit = func(id string) {
		if state[id] == 1 {
			// Found cycle: record all tasks currently in recursion path
			inCycle := false
			for _, node := range path {
				if node == id {
					inCycle = true
				}
				if inCycle {
					cycleIDs[node] = true
				}
			}
			return
		}
		if state[id] == 2 {
			return
		}
		state[id] = 1
		path = append(path, id)
		for _, dep := range adj[id] {
			visit(dep)
		}
		path = path[:len(path)-1]
		state[id] = 2
	}

	for _, t := range tasks {
		if state[t.ID] == 0 {
			visit(t.ID)
		}
	}

	result := make([]string, 0, len(cycleIDs))
	for id := range cycleIDs {
		result = append(result, id)
	}
	sort.Strings(result)
	return result
}

// resolveGoSymbolInFile inspects Go source AST to determine whether the requested symbol exists.
func resolveGoSymbolInFile(path string, src []byte, symbol string) bool {
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, path, src, parser.ParseComments)
	if err != nil {
		return false
	}

	target := strings.TrimSpace(symbol)
	if target == "" {
		return true
	}

	// 1. Check package name
	if node.Name != nil && (target == node.Name.Name || target == "package "+node.Name.Name) {
		return true
	}

	// 2. Check declarations
	for _, decl := range node.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Name == nil {
				continue
			}
			// Standalone function
			if d.Recv == nil || len(d.Recv.List) == 0 {
				if d.Name.Name == target {
					return true
				}
			} else {
				// Method with receiver
				recvType := exprToTypeName(d.Recv.List[0].Type)
				cleanRecv := strings.TrimPrefix(recvType, "*")
				if target == d.Name.Name ||
					target == recvType+"."+d.Name.Name ||
					target == cleanRecv+"."+d.Name.Name {
					return true
				}
			}

		case *ast.GenDecl:
			switch d.Tok {
			case token.TYPE:
				for _, spec := range d.Specs {
					if ts, ok := spec.(*ast.TypeSpec); ok && ts.Name != nil {
						if ts.Name.Name == target {
							return true
						}
					}
				}
			case token.CONST, token.VAR:
				for _, spec := range d.Specs {
					if vs, ok := spec.(*ast.ValueSpec); ok {
						for _, name := range vs.Names {
							if name != nil && name.Name == target {
								return true
							}
						}
					}
				}
			}
		}
	}

	return false
}

// exprToTypeName extracts the base type name string from an AST expression.
func exprToTypeName(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.StarExpr:
		return "*" + exprToTypeName(e.X)
	case *ast.SelectorExpr:
		return exprToTypeName(e.X) + "." + e.Sel.Name
	case *ast.IndexExpr:
		return exprToTypeName(e.X)
	default:
		return ""
	}
}

var taskIDPattern = regexp.MustCompile(`tasks\[([^\]]+)\]`)

// extractTaskIDFromPath extracts the task ID prefix if the path is task-scoped.
func extractTaskIDFromPath(path string) string {
	match := taskIDPattern.FindStringSubmatch(path)
	if len(match) > 1 {
		return match[1]
	}
	return ""
}
