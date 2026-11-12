package graph

import "testing"

func TestAffectedByCodeOnly(t *testing.T) {
	g := &Graph{Version: 1, Nodes: []Node{
		{ID: "pkg::Use", Label: "Use", Kind: "func", Modality: "code"},
		{ID: "pkg::Server", Label: "Server", Kind: "type", Modality: "code"},
		{ID: "fact:f1", Label: "Server note", Kind: "fact", Modality: "memory"},
	}, Edges: []Edge{
		{Source: "pkg::Use", Target: "pkg::Server", Relation: "calls", Confidence: ConfidenceExtracted, ConfidenceScore: 1},
		{Source: "fact:f1", Target: "pkg::Server", Relation: "cites", Confidence: ConfidenceInferred, ConfidenceScore: 0.75},
	}}
	full, err := AffectedBy(g, "pkg::Server", 2)
	if err != nil {
		t.Fatal(err)
	}
	has := func(result *BlastRadiusResult, id string) bool {
		for _, node := range result.Impacted {
			if node.ID == id {
				return true
			}
		}
		return false
	}
	if !has(full, "fact:f1") || !has(full, "pkg::Use") {
		t.Fatalf("full result = %+v", full.Impacted)
	}
	code, err := AffectedByWithOptions(g, "pkg::Server", AffectedOptions{MaxDepth: 2, CodeOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if !has(code, "pkg::Use") || has(code, "fact:f1") {
		t.Fatalf("code-only result = %+v", code.Impacted)
	}
	if _, err := AffectedByWithOptions(g, "fact:f1", AffectedOptions{MaxDepth: 2, CodeOnly: true}); err == nil {
		t.Fatal("code-only memory target should be rejected")
	}
}
