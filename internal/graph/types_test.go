package graph

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGraphFreshnessMetadata(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module audit\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "pkg", "a.go"), []byte("package pkg\n\nfunc Alpha() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	g, err := Build(context.Background(), repo, nil, BuildOptions{CodeOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if g.BuiltAt == "" || g.SourceMtime == "" {
		t.Fatalf("missing metadata: %+v", g)
	}
	if fresh := CheckFreshness(g, repo); fresh.Stale {
		t.Fatalf("new graph is stale: %+v", fresh)
	}
	if err := SaveGraph(filepath.Join(repo, GraphPath), g, BuildOptions{}); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadGraph(filepath.Join(repo, GraphPath))
	if err != nil || loaded == nil {
		t.Fatalf("load graph: %v", err)
	}
	if loaded.SourceMtime == "" {
		t.Fatalf("source_mtime was not persisted: %+v", loaded)
	}
	if fresh := CheckFreshness(loaded, repo); fresh.Stale {
		t.Fatalf("loaded graph is stale: %+v", fresh)
	}
	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(repo, "pkg", "b.go"), []byte("package pkg\n\nfunc Beta() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if fresh := CheckFreshness(g, repo); !fresh.Stale {
		t.Fatalf("source change was not stale: %+v", fresh)
	}
}

func TestSaveGraphHonorsMaxBytes(t *testing.T) {
	t.Setenv("SILA_GRAPH_MAX_BYTES", "256")
	path := filepath.Join(t.TempDir(), "graph.json")
	g := &Graph{Version: 1, Nodes: []Node{{ID: "a", Label: strings.Repeat("x", 512), Kind: "func", Modality: "code"}}}
	if err := SaveGraph(path, g, BuildOptions{}); err == nil || !strings.Contains(err.Error(), "SILA_GRAPH_MAX_BYTES") {
		t.Fatalf("SaveGraph error = %v", err)
	}
}

func TestBuildDigestAndCache(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module cachetest\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(repo, "a.go")
	if err := os.WriteFile(source, []byte("package cachetest\n\nfunc A() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	first, err := BuildDigest(repo)
	if err != nil {
		t.Fatal(err)
	}
	touched := time.Now().Add(-time.Hour)
	if err := os.Chtimes(source, touched, touched); err != nil {
		t.Fatal(err)
	}
	second, err := BuildDigest(repo)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("mtime touch changed the digest")
	}
	if err := os.WriteFile(source, []byte("package cachetest\n\nfunc B() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	third, err := BuildDigest(repo)
	if err != nil {
		t.Fatal(err)
	}
	if third == first {
		t.Fatalf("content change did not change the digest")
	}
	if err := os.WriteFile(filepath.Join(repo, "notes.txt"), []byte("ignored"), 0o644); err != nil {
		t.Fatal(err)
	}
	fourth, err := BuildDigest(repo)
	if err != nil {
		t.Fatal(err)
	}
	if fourth != third {
		t.Fatalf("non-Go file changed the digest")
	}
	cachePath := filepath.Join(repo, BuildCachePath)
	if err := SaveBuildCache(cachePath, BuildCache{Version: 1, Digest: third, Memory: "m", GraphSHA: "sha"}); err != nil {
		t.Fatal(err)
	}
	cache, err := LoadBuildCache(cachePath)
	if err != nil || cache == nil || cache.Digest != third || cache.GraphSHA != "sha" {
		t.Fatalf("cache = %+v, err = %v", cache, err)
	}
	if fingerprint := FileFingerprint(filepath.Join(repo, "missing"), source); fingerprint == "" {
		t.Fatal("empty fingerprint")
	}
	if sha, err := FileSHA256(source); err != nil || sha == "" {
		t.Fatalf("file sha = %q, err = %v", sha, err)
	}
}
