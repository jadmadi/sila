package dream

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jadmadi/sila/internal/graph"
)

func TestHybridGraphAbsentFileGracefulNoOp(t *testing.T) {
	hits := []RecallHit{
		{Kind: "fact", ID: "f1", Score: 600},
		{Kind: "fact", ID: "f2", Score: 550},
	}
	before := append([]RecallHit(nil), hits...)
	out, changed := ApplyHybridGraphBlend(hits, "fix the auth bug", nil, t.TempDir())
	if changed {
		t.Fatal("absent graph must be a no-op")
	}
	if len(out) != len(before) || out[0].Score != before[0].Score || out[1].Score != before[1].Score {
		t.Fatalf("absent graph mutated hits: %+v", out)
	}
	bad := t.TempDir()
	if err := os.MkdirAll(filepath.Join(bad, ".sila"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bad, graph.GraphPath), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, changed := ApplyHybridGraphBlend(before, "fix the auth bug", nil, bad); changed {
		t.Fatal("invalid graph must be a no-op")
	}
}

func TestClassifyQueryAlphaStructuralVsConceptual(t *testing.T) {
	if got := ClassifyQueryAlpha("Store.Open", nil); got != 0.25 {
		t.Fatalf("qualified symbol alpha = %v", got)
	}
	if got := ClassifyQueryAlpha("find callers of Server.Close", nil); got != 0.25 {
		t.Fatalf("relational alpha = %v", got)
	}
	if got := ClassifyQueryAlpha("fix the auth bug", &RecallContext{Paths: []string{"internal/store/store.go"}}); got != 0.50 {
		t.Fatalf("prose with paths alpha = %v", got)
	}
	if got := ClassifyQueryAlpha("fix the auth bug", nil); got != 0.80 {
		t.Fatalf("conceptual alpha = %v", got)
	}
	if got := ClassifyQueryAlpha("why does auth fail", &RecallContext{Symbols: []string{"Auth"}}); got != 0.25 {
		t.Fatalf("symbol context alpha = %v", got)
	}
}

func writeHybridGraph(t *testing.T, dir string, g *graph.Graph) {
	t.Helper()
	if err := graph.SaveGraph(filepath.Join(dir, graph.GraphPath), g, graph.BuildOptions{}); err != nil {
		t.Fatal(err)
	}
}

func TestHybridGraphStructuralBridgePromotionWithinTier(t *testing.T) {
	repo := t.TempDir()
	g := &graph.Graph{Version: 1, Nodes: []graph.Node{
		{ID: "pkg::Open", Label: "Open", Kind: "func", Modality: "code", SourceFile: "open.go"},
		{ID: "fact:f1", Label: "Open closes files", Kind: "fact", Modality: "memory"},
	}, Edges: []graph.Edge{
		{Source: "fact:f1", Target: "pkg::Open", Relation: "cites", Confidence: graph.ConfidenceInferred, ConfidenceScore: 0.75, SourceFile: "open.go"},
	}}
	writeHybridGraph(t, repo, g)
	hits := []RecallHit{
		{Kind: "fact", ID: "f1", Score: 500, RepoFiles: []string{"open.go"}},
		{Kind: "fact", ID: "f2", Score: 500},
	}
	out, changed := ApplyHybridGraphBlend(hits, "Open", nil, repo)
	if !changed {
		t.Fatal("expected a structural promotion")
	}
	if out[0].Score <= 500 {
		t.Fatalf("linked fact was not promoted: %v", out[0].Score)
	}
	if out[0].Score > recallLessonBase-1 {
		t.Fatalf("fact crossed into the lesson tier: %v", out[0].Score)
	}
	if out[1].Score != 500 {
		t.Fatalf("unlinked fact changed: %v", out[1].Score)
	}
}

func TestHybridGraphNeverCrossesAuthorityTiers(t *testing.T) {
	repo := t.TempDir()
	g := &graph.Graph{Version: 1, Nodes: []graph.Node{
		{ID: "pkg::Open", Label: "Open", Kind: "func", Modality: "code", SourceFile: "open.go"},
		{ID: "fact:f1", Label: "Open closes files", Kind: "fact", Modality: "memory"},
		{ID: "lesson:l1", Label: "Open lesson", Kind: "lesson", Modality: "memory"},
	}, Edges: []graph.Edge{
		{Source: "fact:f1", Target: "pkg::Open", Relation: "cites", Confidence: graph.ConfidenceInferred, ConfidenceScore: 0.75, SourceFile: "open.go"},
		{Source: "lesson:l1", Target: "pkg::Open", Relation: "cites", Confidence: graph.ConfidenceInferred, ConfidenceScore: 0.75, SourceFile: "open.go"},
	}}
	writeHybridGraph(t, repo, g)
	hits := []RecallHit{
		{Kind: "transcript", ID: "t1", Score: 490, RepoFiles: []string{"open.go"}},
		{Kind: "fact", ID: "f1", Score: 995, RepoFiles: []string{"open.go"}},
		{Kind: "lesson", ID: "l1", Score: 1490, RepoFiles: []string{"open.go"}},
	}
	out, changed := ApplyHybridGraphBlend(hits, "Open", nil, repo)
	if !changed {
		t.Fatal("expected blending")
	}
	if out[0].Score > recallFactBase-1 {
		t.Fatalf("transcript crossed into facts: %v", out[0].Score)
	}
	if out[1].Score > recallLessonBase-1 {
		t.Fatalf("fact crossed into lessons: %v", out[1].Score)
	}
	if out[2].Score > recallLessonBase+recallTierSpan {
		t.Fatalf("lesson crossed its ceiling: %v", out[2].Score)
	}
}

func TestHybridGraphAmbiguousOnlyLinkIsNoOp(t *testing.T) {
	repo := t.TempDir()
	g := &graph.Graph{Version: 1, Nodes: []graph.Node{
		{ID: "pkg::Open", Label: "Open", Kind: "func", Modality: "code", SourceFile: "open.go"},
		{ID: "fact:f1", Label: "Open closes files", Kind: "fact", Modality: "memory"},
	}, Edges: []graph.Edge{
		{Source: "fact:f1", Target: "pkg::Open", Relation: "cites", Confidence: graph.ConfidenceAmbiguous, ConfidenceScore: 0.3, SourceFile: "open.go"},
	}}
	writeHybridGraph(t, repo, g)
	hits := []RecallHit{{Kind: "fact", ID: "f1", Score: 500, RepoFiles: []string{"open.go"}}}
	if _, changed := ApplyHybridGraphBlend(hits, "Open", nil, repo); changed {
		t.Fatal("an AMBIGUOUS-only link must not promote")
	}
}
