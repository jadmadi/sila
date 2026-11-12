package graph

import "testing"

func TestMergeSingletonCommunities(t *testing.T) {
	ids := []string{"a", "b", "c", "d", "s", "i", "j"}
	community := []int{0, 0, 1, 1, 2, 3, 4}
	adjacency := map[string][]string{
		"a": {"b", "s"}, "b": {"a"}, "c": {"d"}, "d": {"c"}, "s": {"a"},
		"i": {}, "j": {},
	}
	weights := map[weightedPair]float64{
		{minString("a", "b"), maxString("a", "b")}: 1,
		{minString("c", "d"), maxString("c", "d")}: 1,
		{minString("a", "s"), maxString("a", "s")}: 1,
	}
	merged := mergeSingletonCommunities(community, ids, adjacency, weights)
	if len(merged) != len(ids) {
		t.Fatalf("merged = %v", merged)
	}
	if merged[4] != merged[0] {
		t.Fatalf("singleton s was not merged into a: %v", merged)
	}
	if merged[5] != merged[6] {
		t.Fatalf("isolated nodes were not grouped: %v", merged)
	}
	seen := map[int]bool{}
	for _, key := range merged {
		seen[key] = true
	}
	if len(seen) != 3 {
		t.Fatalf("communities = %v", merged)
	}
}

func TestClusterCommunitiesLabelsIsolatedGroup(t *testing.T) {
	g := &Graph{Version: 1, Nodes: []Node{
		{ID: "i", Label: "i", Kind: "func", Modality: "code"},
		{ID: "j", Label: "j", Kind: "func", Modality: "code"},
	}}
	summaries := ClusterCommunities(g)
	if len(summaries) != 1 || summaries[0].Label != "unclustered" || summaries[0].Size != 2 {
		t.Fatalf("summaries = %+v", summaries)
	}
}
