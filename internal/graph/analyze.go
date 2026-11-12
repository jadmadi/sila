package graph

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// AuditReport is the structural and epistemic summary of a graph.
type AuditReport struct {
	ModularityQ           float64                `json:"modularity_q"`
	Communities           []CommunitySummary     `json:"communities"`
	GodNodes              []GodNode              `json:"god_nodes"`
	SurprisingConnections []SurprisingConnection `json:"surprising_connections"`
	SuggestQuestions      []AuditQuestion        `json:"suggest_questions"`
	Stale                 bool                   `json:"stale,omitempty"`
	GraphAge              string                 `json:"graph_age,omitempty"`
	BuiltAt               string                 `json:"built_at,omitempty"`
}

// GodNode is a high-degree non-container node.
type GodNode struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Kind      string `json:"kind"`
	Degree    int    `json:"degree"`
	Community int    `json:"community"`
}

// SurprisingConnection is a non-structural edge worth inspecting.
type SurprisingConnection struct {
	Source     string         `json:"source"`
	Target     string         `json:"target"`
	Relation   string         `json:"relation"`
	Score      float64        `json:"score"`
	Confidence ConfidenceTier `json:"confidence"`
	SourceFile string         `json:"source_file,omitempty"`
}

// AuditQuestion is one suggested follow-up question.
type AuditQuestion struct {
	Category string `json:"category"`
	Question string `json:"question"`
}

// AnalyzeGraph clusters the graph and produces the audit report.
func AnalyzeGraph(g *Graph) *AuditReport {
	report := &AuditReport{}
	if g == nil {
		return report
	}
	report.Communities = ClusterCommunities(g)
	report.ModularityQ = ModularityQ(g)
	_, degree, adjacency := canonicalWeights(g)
	nodes := make(map[string]Node, len(g.Nodes))
	for _, node := range g.Nodes {
		nodes[node.ID] = node
	}

	for id, node := range nodes {
		if node.Kind == "file" || node.Kind == "package" {
			continue
		}
		report.GodNodes = append(report.GodNodes, GodNode{
			ID: id, Label: node.Label, Kind: node.Kind,
			Degree: int(degree[id]), Community: node.Community,
		})
	}
	sort.Slice(report.GodNodes, func(i, j int) bool {
		if report.GodNodes[i].Degree != report.GodNodes[j].Degree {
			return report.GodNodes[i].Degree > report.GodNodes[j].Degree
		}
		return report.GodNodes[i].ID < report.GodNodes[j].ID
	})
	if len(report.GodNodes) > 10 {
		report.GodNodes = report.GodNodes[:10]
	}

	communityOf := map[string]int{}
	for _, node := range g.Nodes {
		communityOf[node.ID] = node.Community
	}
	for _, edge := range g.Edges {
		if edge.Relation == "contains" || edge.Relation == "imports" {
			continue
		}
		score := 0.0
		switch edge.Confidence {
		case ConfidenceExtracted:
			score += 2
		case ConfidenceInferred:
			score++
		}
		sourceNode, sourceOK := nodes[edge.Source]
		targetNode, targetOK := nodes[edge.Target]
		if sourceOK && targetOK {
			if sourceNode.Modality != targetNode.Modality {
				score += 3
			}
			if communityOf[edge.Source] != communityOf[edge.Target] {
				score += 2
			}
			lesser, greater := degree[edge.Source], degree[edge.Target]
			if lesser > greater {
				lesser, greater = greater, lesser
			}
			if lesser <= 1 && greater >= 4 {
				score++
			}
		}
		report.SurprisingConnections = append(report.SurprisingConnections, SurprisingConnection{
			Source: edge.Source, Target: edge.Target, Relation: edge.Relation,
			Score: score, Confidence: edge.Confidence, SourceFile: edge.SourceFile,
		})
	}
	sort.Slice(report.SurprisingConnections, func(i, j int) bool {
		a, b := report.SurprisingConnections[i], report.SurprisingConnections[j]
		if a.Score != b.Score {
			return a.Score > b.Score
		}
		if a.Source != b.Source {
			return a.Source < b.Source
		}
		if a.Target != b.Target {
			return a.Target < b.Target
		}
		return a.Relation < b.Relation
	})
	if len(report.SurprisingConnections) > 10 {
		report.SurprisingConnections = report.SurprisingConnections[:10]
	}
	report.SuggestQuestions = suggestQuestions(g, nodes, degree, adjacency, communityOf, report.Communities)
	return report
}

func suggestQuestions(g *Graph, nodes map[string]Node, degree map[string]float64, adjacency map[string][]string, communityOf map[string]int, communities []CommunitySummary) []AuditQuestion {
	questions := make([]AuditQuestion, 0, 5)
	for _, edge := range g.Edges {
		if edge.Confidence == ConfidenceAmbiguous {
			questions = append(questions, AuditQuestion{
				Category: "ambiguous_edge",
				Question: fmt.Sprintf("Why is %s -> %s %s classified as ambiguous?", edge.Source, edge.Target, edge.Relation),
			})
			break
		}
	}
	if len(questions) == 0 {
		questions = append(questions, AuditQuestion{Category: "ambiguous_edge", Question: "Which edges still need stronger evidence?"})
	}

	bridgeID := ""
	bridgeCount := 0
	for id := range nodes {
		seen := map[int]bool{}
		for _, neighbor := range adjacency[id] {
			seen[communityOf[neighbor]] = true
		}
		if len(seen) > bridgeCount || (len(seen) == bridgeCount && id < bridgeID) {
			bridgeID, bridgeCount = id, len(seen)
		}
	}
	if bridgeID != "" && bridgeCount > 1 {
		questions = append(questions, AuditQuestion{Category: "bridge_node", Question: fmt.Sprintf("How does %s connect %d communities?", bridgeID, bridgeCount)})
	} else {
		questions = append(questions, AuditQuestion{Category: "bridge_node", Question: "Which node connects otherwise separate communities?"})
	}

	inferred := ""
	for _, edge := range g.Edges {
		if edge.Confidence == ConfidenceInferred {
			inferred = fmt.Sprintf("%s -> %s %s", edge.Source, edge.Target, edge.Relation)
			break
		}
	}
	if inferred != "" {
		questions = append(questions, AuditQuestion{Category: "verify_inferred", Question: fmt.Sprintf("Can you verify the inferred link %s?", inferred)})
	} else {
		questions = append(questions, AuditQuestion{Category: "verify_inferred", Question: "Which inferred links can be verified from code?"})
	}

	isolated := ""
	for _, community := range communities {
		if community.Size <= 2 {
			isolated = community.Label
			break
		}
	}
	if isolated != "" {
		questions = append(questions, AuditQuestion{Category: "isolated_cluster", Question: fmt.Sprintf("Is the %s community isolated from the rest of the graph?", isolated)})
	} else {
		questions = append(questions, AuditQuestion{Category: "isolated_cluster", Question: "Which community has the fewest external links?"})
	}

	low := ""
	for _, community := range communities {
		if community.Size >= 3 && community.Cohesion < 0.05 {
			low = community.Label
			break
		}
	}
	if low != "" {
		questions = append(questions, AuditQuestion{Category: "low_cohesion", Question: fmt.Sprintf("Should the loosely connected %s community be split?", low)})
	} else {
		questions = append(questions, AuditQuestion{Category: "low_cohesion", Question: "Which community has weak internal cohesion?"})
	}
	return questions
}

// RenderMarkdownReport renders the audit as narrow, aligned Markdown tables.
func RenderMarkdownReport(report *AuditReport) string {
	if report == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Graph Audit\n\n")
	fmt.Fprintf(&b, "Modularity Q: %.3f\n\n", report.ModularityQ)
	b.WriteString("### Communities\n\n")
	b.WriteString("| ID | Label                  | Size | Cohesion |\n")
	b.WriteString("|----|------------------------|------|----------|\n")
	for _, community := range report.Communities {
		fmt.Fprintf(&b, "| %2d | %-22s | %4d | %8.3f |\n", community.ID, truncate(community.Label, 22), community.Size, community.Cohesion)
	}
	b.WriteString("\n### God nodes\n\n")
	b.WriteString("| Node                   | Kind  | Degree |\n")
	b.WriteString("|------------------------|-------|--------|\n")
	for _, node := range report.GodNodes {
		fmt.Fprintf(&b, "| %-22s | %-5s | %6d |\n", truncate(node.ID, 22), truncate(node.Kind, 5), node.Degree)
	}
	b.WriteString("\n### Surprising connections\n\n")
	b.WriteString("| Source                 | Target                 | Relation  | Score |\n")
	b.WriteString("|------------------------|------------------------|-----------|-------|\n")
	for _, edge := range report.SurprisingConnections {
		fmt.Fprintf(&b, "| %-22s | %-22s | %-9s | %5.1f |\n", truncate(edge.Source, 22), truncate(edge.Target, 22), truncate(edge.Relation, 9), edge.Score)
	}
	b.WriteString("\n### Suggested questions\n\n")
	for _, question := range report.SuggestQuestions {
		fmt.Fprintf(&b, "- [%s] %s\n", question.Category, question.Question)
	}
	return b.String()
}

func truncate(value string, width int) string {
	if len(value) <= width {
		return value
	}
	if width <= 1 {
		return value[:width]
	}
	return value[:width-1] + "~"
}

// Stats summarizes one graph for operators and agents.
type Stats struct {
	Path            string         `json:"path"`
	Missing         bool           `json:"missing,omitempty"`
	Nodes           int            `json:"nodes"`
	Edges           int            `json:"edges"`
	NodesByKind     map[string]int `json:"nodes_by_kind,omitempty"`
	EdgesByRelation map[string]int `json:"edges_by_relation,omitempty"`
	Files           int            `json:"files"`
	RepoFiles       int            `json:"repo_files"`
	Orphans         int            `json:"orphans"`
	SizeBytes       int64          `json:"size_bytes"`
	BuiltAt         string         `json:"built_at,omitempty"`
	GraphAge        string         `json:"graph_age,omitempty"`
	Stale           bool           `json:"stale,omitempty"`
}

// DoctorCheck is one graph health check.
type DoctorCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty"`
}

// DoctorReport is the graph health verdict.
type DoctorReport struct {
	Status string        `json:"status"`
	Checks []DoctorCheck `json:"checks"`
	Stats  Stats         `json:"stats"`
}

// ComputeStats summarizes a graph, including size, coverage, orphans, and age.
func ComputeStats(g *Graph, repoRoot, path string) Stats {
	stats := Stats{
		Path:            path,
		NodesByKind:     map[string]int{},
		EdgesByRelation: map[string]int{},
		RepoFiles:       countRepoGoFiles(repoRoot),
	}
	if g == nil {
		stats.Missing = true
		return stats
	}
	stats.Nodes = len(g.Nodes)
	stats.Edges = len(g.Edges)
	degree := map[string]int{}
	for _, node := range g.Nodes {
		stats.NodesByKind[node.Kind]++
		degree[node.ID] = 0
	}
	for _, edge := range g.Edges {
		stats.EdgesByRelation[edge.Relation]++
		degree[edge.Source]++
		degree[edge.Target]++
	}
	for _, count := range degree {
		if count == 0 {
			stats.Orphans++
		}
	}
	stats.Files = stats.NodesByKind["file"]
	if info, err := os.Stat(path); err == nil {
		stats.SizeBytes = info.Size()
	}
	fresh := CheckFreshness(g, repoRoot)
	stats.Stale = fresh.Stale
	stats.BuiltAt = fresh.BuiltAt
	stats.GraphAge = fresh.GraphAge
	return stats
}

// GraphDoctor checks graph presence, freshness, size, coverage, and orphans.
func GraphDoctor(g *Graph, repoRoot, path string) DoctorReport {
	stats := ComputeStats(g, repoRoot, path)
	report := DoctorReport{Status: "ok", Stats: stats}
	add := func(name, status, detail, fix string) {
		if status == "warn" {
			report.Status = "warn"
		}
		report.Checks = append(report.Checks, DoctorCheck{Name: name, Status: status, Detail: detail, Fix: fix})
	}
	if stats.Missing {
		add("graph", "warn", "graph is not built", "sila graph build")
		return report
	}
	add("graph", "pass", fmt.Sprintf("%d nodes, %d edges", stats.Nodes, stats.Edges), "")
	if stats.Stale {
		add("freshness", "warn", "graph is stale ("+orUnknown(stats.GraphAge)+")", "sila graph build --if-stale")
	} else {
		add("freshness", "pass", "graph is current", "")
	}
	if stats.Nodes == 0 {
		add("nodes", "warn", "graph has no nodes", "sila graph build")
	} else {
		add("nodes", "pass", fmt.Sprintf("%d nodes", stats.Nodes), "")
	}
	if stats.Orphans > 0 {
		add("orphans", "warn", fmt.Sprintf("%d orphan nodes", stats.Orphans), "review .sila/graph.json or rebuild with sila graph build")
	} else {
		add("orphans", "pass", "no orphan nodes", "")
	}
	if stats.RepoFiles > 0 && stats.Files*2 < stats.RepoFiles {
		add("coverage", "warn", fmt.Sprintf("%d of %d Go files represented", stats.Files, stats.RepoFiles), "sila graph build")
	} else {
		add("coverage", "pass", fmt.Sprintf("%d of %d Go files represented", stats.Files, stats.RepoFiles), "")
	}
	if stats.SizeBytes > MaxGraphBytes() {
		add("size", "warn", fmt.Sprintf("%d bytes is over the %d-byte cap", stats.SizeBytes, MaxGraphBytes()), "raise SILA_GRAPH_MAX_BYTES or reduce scope")
	} else {
		add("size", "pass", fmt.Sprintf("%d bytes", stats.SizeBytes), "")
	}
	return report
}

func orUnknown(value string) string {
	if value == "" {
		return "unknown age"
	}
	return value
}

func countRepoGoFiles(repoRoot string) int {
	count := 0
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
		if strings.HasSuffix(d.Name(), ".go") {
			count++
		}
		return nil
	})
	return count
}
