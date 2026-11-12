package graph

import (
	"context"
	"fmt"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	silaast "github.com/jadmadi/sila/internal/ast"
	"github.com/jadmadi/sila/internal/store"
	"github.com/jadmadi/sila/internal/symbols"
	"github.com/jadmadi/sila/internal/testmatrix"
)

// Build fuses Go code structure, tests, and Sila memory into one graph.
func Build(ctx context.Context, repoRoot string, st *store.Store, opts BuildOptions) (*Graph, error) {
	if strings.TrimSpace(repoRoot) == "" {
		return nil, fmt.Errorf("repository root is required")
	}
	repoRoot, err := filepath.Abs(repoRoot)
	if err != nil {
		return nil, err
	}
	g := &Graph{Version: 1}
	nodes := map[string]Node{}
	addNode := func(node Node) {
		if node.ID == "" {
			return
		}
		if _, exists := nodes[node.ID]; !exists {
			nodes[node.ID] = node
		}
	}
	var edges []Edge
	addEdge := func(source, target, relation string, tier ConfidenceTier, score float64, sourceFile, location string) {
		if source == "" || target == "" || source == target {
			return
		}
		edges = append(edges, Edge{
			Source:          source,
			Target:          target,
			Relation:        relation,
			Confidence:      tier,
			ConfidenceScore: tier.ClampScore(score),
			SourceFile:      sourceFile,
			SourceLocation:  location,
		})
	}

	fset, parsed, err := symbols.ScanRepositoryGoFiles(repoRoot)
	if err != nil {
		return nil, fmt.Errorf("scan repository: %w", err)
	}
	var syms []silaast.SymbolInfo
	for _, pf := range parsed {
		syms = append(syms, pf.Symbols...)
	}
	sort.Slice(syms, func(i, j int) bool { return syms[i].CanonicalID < syms[j].CanonicalID })
	packages := map[string]bool{}
	files := map[string]bool{}
	filePackage := map[string]string{}
	for _, sym := range syms {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		kind := string(sym.SymbolKind)
		if sym.SymbolKind == silaast.KindInterface {
			kind = "interface"
		}
		addNode(Node{
			ID: sym.CanonicalID, Label: sym.SymbolName, Kind: kind, Modality: "code",
			SourceFile: sym.FilePath, StartLine: sym.StartLine, EndLine: sym.EndLine,
		})
		packages[sym.PackagePath] = true
		files[sym.FilePath] = true
		filePackage[sym.FilePath] = sym.PackagePath
		fileID := "file:" + sym.FilePath
		addEdge(fileID, sym.CanonicalID, "contains", ConfidenceExtracted, 1.0, sym.FilePath, fmt.Sprintf("%d", sym.StartLine))
	}
	for file := range files {
		addNode(Node{ID: "file:" + file, Label: filepath.Base(file), Kind: "file", Modality: "code", SourceFile: file})
	}
	filesByPackage := map[string][]string{}
	for file, pkg := range filePackage {
		filesByPackage[pkg] = append(filesByPackage[pkg], file)
	}
	for pkg, pkgFiles := range filesByPackage {
		addNode(Node{ID: "package:" + pkg, Label: pkg, Kind: "package", Modality: "code"})
		sort.Strings(pkgFiles)
		for _, file := range pkgFiles {
			addEdge("package:"+pkg, "file:"+file, "contains", ConfidenceExtracted, 1.0, file, "")
		}
	}

	if err := buildCalls(ctx, fset, parsed, addEdge); err != nil {
		return nil, err
	}
	buildTests(repoRoot, addNode, addEdge)

	if !opts.CodeOnly && st != nil {
		if err := buildMemory(ctx, repoRoot, st, syms, addNode, addEdge); err != nil {
			return nil, err
		}
	}

	for _, node := range nodes {
		g.Nodes = append(g.Nodes, node)
	}
	g.Edges = FoldEdge(edges)
	g.Head = gitHead(repoRoot)
	g.RepoRoot = repoRoot
	if newest := newestSourceMtime(repoRoot); !newest.IsZero() {
		g.BuiltAt = newest.UTC().Format(time.RFC3339Nano)
		g.SourceMtime = g.BuiltAt
	}
	g.Sort()
	return g, nil
}

func buildCalls(ctx context.Context, fset *token.FileSet, parsed []*symbols.ParsedFile, addEdge func(string, string, string, ConfidenceTier, float64, string, string)) error {
	for _, edge := range symbols.BuildCallEdgesFromParsed(fset, parsed) {
		if err := ctx.Err(); err != nil {
			return err
		}
		addEdge(edge.Caller, edge.Callee, "calls", ConfidenceExtracted, 1.0, edge.File, fmt.Sprintf("%d", edge.Line))
	}
	return nil
}

func buildTests(repoRoot string, addNode func(Node), addEdge func(string, string, string, ConfidenceTier, float64, string, string)) {
	tg, err := testmatrix.BuildTestCallGraph(repoRoot)
	if err != nil || tg == nil {
		return
	}
	keys := make([]string, 0, len(tg.SymbolToTests))
	for symbol := range tg.SymbolToTests {
		keys = append(keys, symbol)
	}
	sort.Strings(keys)
	for _, symbol := range keys {
		tests := append([]testmatrix.TestTarget(nil), tg.SymbolToTests[symbol]...)
		sort.Slice(tests, func(i, j int) bool {
			if tests[i].TestFunction != tests[j].TestFunction {
				return tests[i].TestFunction < tests[j].TestFunction
			}
			return tests[i].TestFile < tests[j].TestFile
		})
		for _, test := range tests {
			id := "test:" + test.Package + "::" + test.TestFunction
			addNode(Node{ID: id, Label: test.TestFunction, Kind: "test", Modality: "code", SourceFile: test.TestFile})
			addEdge(symbol, id, "tests", ConfidenceInferred, 0.75, test.TestFile, "")
		}
	}
}

func buildMemory(ctx context.Context, repoRoot string, st *store.Store, syms []silaast.SymbolInfo, addNode func(Node), addEdge func(string, string, string, ConfidenceTier, float64, string, string)) error {
	projectID := store.ProjectID(repoRoot)
	codeFiles := codeFileSet(repoRoot)
	symbolNames := symbolNamesFromSyms(syms)
	fileList := sortedKeys(codeFiles)

	goalsDir := filepath.Join(repoRoot, "goals")
	entries, _ := os.ReadDir(goalsDir)
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !entry.IsDir() {
			continue
		}
		slug := entry.Name()
		content, err := os.ReadFile(filepath.Join(goalsDir, slug, "goal.md"))
		if err != nil {
			continue
		}
		text := string(content)
		title := TitleFromMarkdown(text)
		if title == "" {
			title = slug
		}
		id := "goal:" + slug
		addNode(Node{ID: id, Label: title, Kind: "goal", Modality: "memory", SourceFile: filepath.Join("goals", slug, "goal.md")})
		for _, file := range fileList {
			if strings.Contains(text, file) {
				addEdge(id, "file:"+file, "governs", ConfidenceInferred, 0.70, file, "")
			}
		}
		linkMemorySymbolsWith(id, text, symbolNames, addEdge, "governs")
	}

	facts, err := st.FactsByProject(projectID)
	if err != nil {
		return err
	}
	lessons, _ := st.LessonFacts(projectID, 0)
	lessonIDs := map[string]bool{}
	for _, lesson := range lessons {
		lessonIDs[lesson.ID] = true
		addNode(Node{ID: "lesson:" + lesson.ID, Label: firstLine(lesson.Claim), Kind: "lesson", Modality: "memory"})
		linkMemoryFiles("lesson:"+lesson.ID, lesson, codeFiles, addEdge)
	}
	for _, fact := range facts {
		if lessonIDs[fact.ID] {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		addNode(Node{ID: "fact:" + fact.ID, Label: firstLine(fact.Claim), Kind: "fact", Modality: "memory"})
		linkMemoryFiles("fact:"+fact.ID, fact, codeFiles, addEdge)
		linkMemorySymbolsWith("fact:"+fact.ID, fact.Claim, symbolNames, addEdge, "cites")
	}

	decisions, err := st.ListDecisions(store.DecisionFilter{ProjectID: projectID})
	if err == nil {
		for _, decision := range decisions {
			if err := ctx.Err(); err != nil {
				return err
			}
			id := "decision:" + decision.Address
			addNode(Node{ID: id, Label: decision.Title, Kind: "decision", Modality: "memory"})
			if decision.GoalSlug != "" {
				addEdge(id, "goal:"+decision.GoalSlug, "rationale_for", ConfidenceInferred, 0.70, decision.Address, "")
			}
			text := decision.Title + " " + decision.Context + " " + decision.Rationale
			for _, file := range sortedKeys(codeFiles) {
				if strings.Contains(text, file) {
					addEdge(id, "file:"+file, "cites", ConfidenceInferred, 0.70, file, "")
				}
			}
			linkMemorySymbolsWith(id, text, symbolNames, addEdge, "cites")
		}
	}

	docs, err := st.DocsByProject(projectID, 100)
	if err == nil {
		for _, doc := range docs {
			id := "doc:" + doc.ID
			addNode(Node{ID: id, Label: doc.Title, Kind: "doc", Modality: "doc", SourceFile: doc.URL})
			text := doc.Title + " " + doc.URL + " " + doc.Content
			for _, file := range sortedKeys(codeFiles) {
				if strings.Contains(text, file) {
					addEdge(id, "file:"+file, "cites", ConfidenceAmbiguous, 0.30, file, "")
				}
			}
		}
	}
	return nil
}

func linkMemoryFiles(id string, fact store.Fact, codeFiles map[string]bool, addEdge func(string, string, string, ConfidenceTier, float64, string, string)) {
	for _, repoFile := range fact.RepoFiles {
		clean := filepath.ToSlash(filepath.Clean(repoFile))
		if !codeFiles[clean] {
			continue
		}
		addEdge(id, "file:"+clean, "cites", ConfidenceExtracted, 1.0, clean, "")
		// Content mentions are weaker evidence and fold into AlsoSeen.
		addEdge(id, "file:"+clean, "cites", ConfidenceInferred, 0.70, clean, "")
	}
}

func linkMemorySymbolsWith(id, text string, symbolNames map[string]string, addEdge func(string, string, string, ConfidenceTier, float64, string, string), relation string) {
	seen := map[string]bool{}
	for _, word := range memoryWords(text) {
		symbolID, ok := symbolNames[word]
		if !ok || seen[word] {
			continue
		}
		seen[word] = true
		addEdge(id, symbolID, relation, ConfidenceAmbiguous, 0.30, "", word)
	}
}

func memoryWords(text string) []string {
	return strings.FieldsFunc(text, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_')
	})
}

func codeFileSet(repoRoot string) map[string]bool {
	out := map[string]bool{}
	_ = filepath.WalkDir(repoRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			if rel, err := filepath.Rel(repoRoot, path); err == nil {
				out[filepath.ToSlash(rel)] = true
			}
		}
		return nil
	})
	return out
}

func symbolNamesFromSyms(syms []silaast.SymbolInfo) map[string]string {
	out := map[string]string{}
	for _, sym := range syms {
		if sym.SymbolName == "" {
			continue
		}
		if _, exists := out[sym.SymbolName]; !exists {
			out[sym.SymbolName] = sym.CanonicalID
		}
	}
	return out
}

func sortedKeys[V any](values map[string]V) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func firstLine(text string) string {
	if idx := strings.IndexByte(text, '\n'); idx >= 0 {
		return strings.TrimSpace(text[:idx])
	}
	return strings.TrimSpace(text)
}
