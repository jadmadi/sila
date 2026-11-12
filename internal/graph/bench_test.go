package graph

import (
	"context"
	"path/filepath"
	"testing"
)

// BenchmarkBuildCode times a code-only build of this repository.
func BenchmarkBuildCode(b *testing.B) {
	root, err := filepath.Abs("../..")
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Build(context.Background(), root, nil, BuildOptions{CodeOnly: true}); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkAnalyzeRepo times AnalyzeGraph on this repository's saved graph.
func BenchmarkAnalyzeRepo(b *testing.B) {
	g, err := LoadGraph(filepath.Join("..", "..", GraphPath))
	if err != nil || g == nil {
		b.Skip("no saved graph")
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		AnalyzeGraph(g)
	}
}
