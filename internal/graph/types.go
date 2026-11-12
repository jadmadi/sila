// Package graph builds and persists the epistemic Code + Memory Graph.
package graph

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// GraphPath is the canonical repository-relative graph location.
const GraphPath = ".sila/graph.json"

// BuildCachePath is the repository-relative content-addressed build cache.
const BuildCachePath = ".sila/graph-cache.json"

// BuildCache records the source digest and graph hash of a successful build.
type BuildCache struct {
	Version  int    `json:"version"`
	Digest   string `json:"digest"`
	CodeOnly bool   `json:"code_only,omitempty"`
	Memory   string `json:"memory,omitempty"`
	GraphSHA string `json:"graph_sha256"`
}

// BuildDigest hashes repository Go sources and module files.
func BuildDigest(repoRoot string) (string, error) {
	root, err := filepath.Abs(repoRoot)
	if err != nil {
		return "", err
	}
	var files []string
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if strings.HasPrefix(name, ".") || name == "vendor" || name == "node_modules" || name == "rm" || name == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if strings.HasSuffix(name, ".go") || name == "go.mod" || name == "go.sum" {
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return nil
			}
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(files)
	h := sha256.New()
	for _, rel := range files {
		data, readErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if readErr != nil {
			return "", readErr
		}
		io.WriteString(h, rel)
		h.Write([]byte{0})
		h.Write(data)
		h.Write([]byte{0})
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

// FileSHA256 returns the hex digest of a file, or an empty string if absent.
func FileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// LoadBuildCache reads the cache, returning nil on absence or invalid content.
func LoadBuildCache(path string) (*BuildCache, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var cache BuildCache
	if err := json.Unmarshal(data, &cache); err != nil {
		return nil, nil
	}
	return &cache, nil
}

// SaveBuildCache writes the cache atomically.
func SaveBuildCache(path string, cache BuildCache) error {
	if cache.Version == 0 {
		cache.Version = 1
	}
	data, err := json.Marshal(cache)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, byte(10)), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// MaxGraphBytes returns the configured graph size cap. The default is 50 MiB;
// SILA_GRAPH_MAX_BYTES overrides it with a positive integer.
func MaxGraphBytes() int64 {
	if raw := strings.TrimSpace(os.Getenv("SILA_GRAPH_MAX_BYTES")); raw != "" {
		if n, err := strconv.ParseInt(raw, 10, 64); err == nil && n > 0 {
			return n
		}
	}
	return 50 << 20
}

// ConfidenceTier is the provenance class of an edge.
type ConfidenceTier string

const (
	// ConfidenceExtracted is direct structural evidence (AST, file pins).
	ConfidenceExtracted ConfidenceTier = "EXTRACTED"
	// ConfidenceInferred is a reasoned link between code and memory.
	ConfidenceInferred ConfidenceTier = "INFERRED"
	// ConfidenceAmbiguous is a weak name or token overlap.
	ConfidenceAmbiguous ConfidenceTier = "AMBIGUOUS"
)

// Rank returns the provenance rank, higher is stronger.
func (c ConfidenceTier) Rank() int {
	switch c {
	case ConfidenceExtracted:
		return 3
	case ConfidenceInferred:
		return 2
	case ConfidenceAmbiguous:
		return 1
	}
	return 0
}

// Score returns the representative score for a tier.
func (c ConfidenceTier) Score() float64 {
	switch c {
	case ConfidenceExtracted:
		return 1.0
	case ConfidenceInferred:
		return 0.75
	case ConfidenceAmbiguous:
		return 0.30
	}
	return 0
}

// ClampScore keeps a score inside the tier's documented band.
func (c ConfidenceTier) ClampScore(score float64) float64 {
	switch c {
	case ConfidenceExtracted:
		return 1.0
	case ConfidenceInferred:
		if score < 0.65 || score > 0.85 {
			return 0.75
		}
	case ConfidenceAmbiguous:
		if score < 0.10 || score > 0.50 {
			return 0.30
		}
	}
	return score
}

// Node is one canonical graph vertex.
type Node struct {
	ID         string   `json:"id"`
	Label      string   `json:"label"`
	Kind       string   `json:"kind"`
	Modality   string   `json:"modality"`
	SourceFile string   `json:"source_file,omitempty"`
	StartLine  int      `json:"start_line,omitempty"`
	EndLine    int      `json:"end_line,omitempty"`
	Community  int      `json:"community,omitempty"`
	Findings   []string `json:"findings,omitempty"`
}

// EvidenceRef records one folded provenance observation.
type EvidenceRef struct {
	Confidence      ConfidenceTier `json:"confidence"`
	ConfidenceScore float64        `json:"confidence_score"`
	SourceFile      string         `json:"source_file,omitempty"`
	SourceLocation  string         `json:"source_location,omitempty"`
}

// Edge is one directed, typed, provenance-ranked relation.
type Edge struct {
	Source          string         `json:"source"`
	Target          string         `json:"target"`
	Relation        string         `json:"relation"`
	Confidence      ConfidenceTier `json:"confidence"`
	ConfidenceScore float64        `json:"confidence_score"`
	SourceFile      string         `json:"source_file,omitempty"`
	SourceLocation  string         `json:"source_location,omitempty"`
	AlsoSeen        []EvidenceRef  `json:"also_seen,omitempty"`
}

// Graph is the canonical, sorted Code + Memory Graph.
type Graph struct {
	Version     int    `json:"version"`
	BuiltAt     string `json:"built_at,omitempty"`
	SourceMtime string `json:"source_mtime,omitempty"`
	Head        string `json:"head,omitempty"`
	RepoRoot    string `json:"repo_root,omitempty"`
	Nodes       []Node `json:"nodes"`
	Edges       []Edge `json:"edges"`
}

// Freshness reports whether a graph still matches its source tree.
type Freshness struct {
	Stale     bool   `json:"stale"`
	BuiltAt   string `json:"built_at,omitempty"`
	GraphAge  string `json:"graph_age,omitempty"`
	Head      string `json:"head,omitempty"`
	GraphHead string `json:"graph_head,omitempty"`
}

// CheckFreshness compares a graph's source snapshot with the live tree.
// It is conservative: a graph without metadata or with no Go files is stale.
func CheckFreshness(g *Graph, repoRoot string) Freshness {
	f := Freshness{Stale: true}
	if g == nil {
		return f
	}
	f.BuiltAt = g.BuiltAt
	f.GraphHead = g.Head
	stamp := g.SourceMtime
	if stamp == "" {
		stamp = g.BuiltAt
	}
	graphTime, err := time.Parse(time.RFC3339Nano, stamp)
	if err != nil || graphTime.IsZero() {
		return f
	}
	f.BuiltAt = graphTime.UTC().Format(time.RFC3339Nano)
	sourceTime := newestSourceMtime(repoRoot)
	if sourceTime.IsZero() {
		return f
	}
	f.Head = gitHead(repoRoot)
	if sourceTime.After(graphTime) {
		return f
	}
	if f.GraphHead != "" && f.Head != "" && f.GraphHead != f.Head {
		return f
	}
	f.Stale = false
	f.GraphAge = time.Since(graphTime).Round(time.Second).String()
	return f
}

func newestSourceMtime(repoRoot string) time.Time {
	var newest time.Time
	_ = filepath.WalkDir(repoRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "vendor", "node_modules", "rm":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") {
			return nil
		}
		info, statErr := d.Info()
		if statErr == nil && info.ModTime().After(newest) {
			newest = info.ModTime()
		}
		return nil
	})
	return newest
}

func gitHead(repoRoot string) string {
	cmd := exec.Command("git", "-C", repoRoot, "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// BuildOptions controls one build and its persistence policy.
type BuildOptions struct {
	CodeOnly    bool
	AllowShrink bool
}

// Sort canonicalizes nodes by ID and edges by (Source, Target, Relation).
func (g *Graph) Sort() {
	sort.SliceStable(g.Nodes, func(i, j int) bool { return g.Nodes[i].ID < g.Nodes[j].ID })
	sort.SliceStable(g.Edges, func(i, j int) bool {
		return edgeLess(g.Edges[i], g.Edges[j])
	})
	for i := range g.Edges {
		sortEvidence(g.Edges[i].AlsoSeen)
	}
}

func edgeLess(a, b Edge) bool {
	if a.Source != b.Source {
		return a.Source < b.Source
	}
	if a.Target != b.Target {
		return a.Target < b.Target
	}
	return a.Relation < b.Relation
}

func sortEvidence(refs []EvidenceRef) {
	sort.SliceStable(refs, func(i, j int) bool {
		if refs[i].Confidence != refs[j].Confidence {
			return refs[i].Confidence.Rank() > refs[j].Confidence.Rank()
		}
		if refs[i].ConfidenceScore != refs[j].ConfidenceScore {
			return refs[i].ConfidenceScore > refs[j].ConfidenceScore
		}
		if refs[i].SourceFile != refs[j].SourceFile {
			return refs[i].SourceFile < refs[j].SourceFile
		}
		return refs[i].SourceLocation < refs[j].SourceLocation
	})
}

// FoldEdge merges duplicate (Source, Target, Relation) edges. The strongest
// provenance stays primary and every other observation is kept in AlsoSeen.
func FoldEdge(edges []Edge) []Edge {
	byKey := make(map[string]Edge, len(edges))
	for _, edge := range edges {
		edge.ConfidenceScore = edge.Confidence.ClampScore(edge.ConfidenceScore)
		key := edge.Source + "\x00" + edge.Target + "\x00" + edge.Relation
		current, ok := byKey[key]
		if !ok {
			byKey[key] = edge
			continue
		}
		byKey[key] = mergeEdges(current, edge)
	}
	out := make([]Edge, 0, len(byKey))
	for _, edge := range byKey {
		sortEvidence(edge.AlsoSeen)
		out = append(out, edge)
	}
	sort.Slice(out, func(i, j int) bool { return edgeLess(out[i], out[j]) })
	return out
}

func mergeEdges(current, incoming Edge) Edge {
	primary, secondary := current, incoming
	if edgeStronger(incoming, current) {
		primary, secondary = incoming, current
	}
	primary.AlsoSeen = append(primary.AlsoSeen, evidenceOf(secondary))
	primary.AlsoSeen = dedupeEvidence(primary.AlsoSeen)
	return primary
}

func edgeStronger(a, b Edge) bool {
	if a.Confidence.Rank() != b.Confidence.Rank() {
		return a.Confidence.Rank() > b.Confidence.Rank()
	}
	if a.ConfidenceScore != b.ConfidenceScore {
		return a.ConfidenceScore > b.ConfidenceScore
	}
	return a.SourceFile < b.SourceFile
}

func evidenceOf(edge Edge) EvidenceRef {
	return EvidenceRef{
		Confidence:      edge.Confidence,
		ConfidenceScore: edge.ConfidenceScore,
		SourceFile:      edge.SourceFile,
		SourceLocation:  edge.SourceLocation,
	}
}

func dedupeEvidence(refs []EvidenceRef) []EvidenceRef {
	seen := make(map[EvidenceRef]bool, len(refs))
	out := refs[:0]
	for _, ref := range refs {
		if seen[ref] {
			continue
		}
		seen[ref] = true
		out = append(out, ref)
	}
	return out
}

// HasMemoryNodes reports whether the graph contains any memory or doc vertex.
func (g *Graph) HasMemoryNodes() bool {
	if g == nil {
		return false
	}
	for _, node := range g.Nodes {
		if node.Modality != "code" {
			return true
		}
	}
	return false
}

// LoadGraph reads a graph, returning (nil, nil) when the file is absent.
func LoadGraph(path string) (*Graph, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var g Graph
	if err := json.Unmarshal(data, &g); err != nil {
		return nil, fmt.Errorf("decode graph %s: %w", path, err)
	}
	g.Sort()
	return &g, nil
}

// SaveGraph writes the graph atomically. A code-only build refuses to replace
// an existing graph that still carries memory nodes unless AllowShrink is set.
func SaveGraph(path string, g *Graph, opts BuildOptions) error {
	if g == nil {
		return fmt.Errorf("graph is nil")
	}
	if opts.CodeOnly && !opts.AllowShrink {
		existing, err := LoadGraph(path)
		if err != nil {
			return err
		}
		if existing != nil && existing.HasMemoryNodes() && !g.HasMemoryNodes() {
			return fmt.Errorf("refusing to shrink %s: code-only build would drop %d memory nodes; rerun with --force to allow it", path, countMemoryNodes(existing))
		}
	}
	copyGraph := &Graph{
		Version: g.Version, BuiltAt: g.BuiltAt, SourceMtime: g.SourceMtime,
		Head: g.Head, RepoRoot: g.RepoRoot,
		Nodes: append([]Node(nil), g.Nodes...), Edges: append([]Edge(nil), g.Edges...),
	}
	for i := range copyGraph.Edges {
		copyGraph.Edges[i].AlsoSeen = append([]EvidenceRef(nil), copyGraph.Edges[i].AlsoSeen...)
	}
	copyGraph.Sort()
	data, err := json.MarshalIndent(copyGraph, "", "  ")
	if err != nil {
		return fmt.Errorf("encode graph: %w", err)
	}
	data = append(data, '\n')
	if limit := MaxGraphBytes(); int64(len(data)) > limit {
		return fmt.Errorf("graph %s is %d bytes, over the %d-byte SILA_GRAPH_MAX_BYTES cap; raise the cap or reduce scope", path, len(data), limit)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".graph-*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func countMemoryNodes(g *Graph) int {
	count := 0
	for _, node := range g.Nodes {
		if node.Modality != "code" {
			count++
		}
	}
	return count
}

// GraphBytes returns the canonical JSON bytes for comparison and tests.
func GraphBytes(g *Graph) ([]byte, error) {
	copyGraph := &Graph{
		Version: g.Version, BuiltAt: g.BuiltAt, SourceMtime: g.SourceMtime,
		Head: g.Head, RepoRoot: g.RepoRoot,
		Nodes: append([]Node(nil), g.Nodes...), Edges: append([]Edge(nil), g.Edges...),
	}
	copyGraph.Sort()
	data, err := json.MarshalIndent(copyGraph, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// TitleFromMarkdown returns the first heading title without the leading "# ".
func TitleFromMarkdown(content string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "# "))
		}
	}
	return ""
}

// FileFingerprint returns a cheap stat-based fingerprint for cache invalidation.
func FileFingerprint(paths ...string) string {
	parts := make([]string, 0, len(paths))
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			parts = append(parts, path+":absent")
			continue
		}
		parts = append(parts, fmt.Sprintf("%s:%d:%d", path, info.ModTime().UnixNano(), info.Size()))
	}
	return strings.Join(parts, "|")
}

// MemoryFingerprint keys the cache on store content signals without volatile WAL mtimes.
func MemoryFingerprint(dbPath string) string {
	mainInfo, mainErr := os.Stat(dbPath)
	walInfo, walErr := os.Stat(dbPath + "-wal")
	mainPart := "absent"
	if mainErr == nil {
		mainPart = fmt.Sprintf("%d:%d", mainInfo.ModTime().UnixNano(), mainInfo.Size())
	}
	walSize := int64(0)
	if walErr == nil {
		walSize = walInfo.Size()
	}
	return fmt.Sprintf("%s|%d", mainPart, walSize)
}
