package graph

import (
	"math"
	"sort"
)

const ambiguousScale = 0.5

// CommunitySummary describes one detected community.
type CommunitySummary struct {
	ID       int      `json:"id"`
	Label    string   `json:"label"`
	Size     int      `json:"size"`
	Cohesion float64  `json:"cohesion"`
	Members  []string `json:"members,omitempty"`
}

type weightedPair struct {
	a, b string
}

// ClusterCommunities partitions the graph deterministically and assigns
// Node.Community. It returns communities ordered by descending size.
func ClusterCommunities(g *Graph) []CommunitySummary {
	if g == nil || len(g.Nodes) == 0 {
		return nil
	}
	weights, degree, adjacency := canonicalWeights(g)
	ids := make([]string, 0, len(g.Nodes))
	for _, node := range g.Nodes {
		ids = append(ids, node.ID)
	}
	sort.Strings(ids)
	index := make(map[string]int, len(ids))
	for i, id := range ids {
		index[id] = i
	}
	community := make([]int, len(ids))
	for i := range community {
		community[i] = i
	}
	hubs := hubNodes(degree, ids)

	kappa := make([]float64, len(ids))
	for i := range kappa {
		kappa[i] = degree[ids[i]]
	}
	communityDegree := append([]float64(nil), kappa...)
	twoM := 0.0
	for _, weight := range weights {
		twoM += 2 * weight
	}
	if twoM == 0 {
		for i := range community {
			community[i] = 0
		}
	} else {
		for pass := 0; pass < 24; pass++ {
			moved := false
			for i, id := range ids {
				if hubs[id] {
					continue
				}
				neighborWeights := make(map[int]float64)
				for _, neighbor := range adjacency[id] {
					neighborWeights[community[index[neighbor]]] += weights[weightedPair{minString(id, neighbor), maxString(id, neighbor)}]
				}
				bestCommunity := community[i]
				bestGain := 0.0
				currentGain := neighborWeights[bestCommunity] - kappa[i]*communityDegree[bestCommunity]/twoM
				candidates := make([]int, 0, len(neighborWeights))
				for c := range neighborWeights {
					candidates = append(candidates, c)
				}
				sort.Ints(candidates)
				for _, c := range candidates {
					if c == community[i] {
						continue
					}
					gain := neighborWeights[c] - kappa[i]*communityDegree[c]/twoM
					if gain > bestGain+1e-12 || (math.Abs(gain-bestGain) <= 1e-12 && gain > currentGain+1e-12 && c < bestCommunity) {
						bestGain = gain
						bestCommunity = c
					}
				}
				if bestCommunity != community[i] && bestGain > currentGain+1e-12 {
					communityDegree[community[i]] -= kappa[i]
					community[i] = bestCommunity
					communityDegree[bestCommunity] += kappa[i]
					moved = true
				}
			}
			if !moved {
				break
			}
		}
	}

	community = refineComponents(community, ids, adjacency)
	community = reattachHubs(community, ids, adjacency, weights, hubs)
	community = splitCommunities(community, ids, adjacency, weights)
	community = mergeSingletonCommunities(community, ids, adjacency, weights)

	return summarizeCommunities(g, community, ids, adjacency, weights)
}

func canonicalWeights(g *Graph) (map[weightedPair]float64, map[string]float64, map[string][]string) {
	weights := map[weightedPair]float64{}
	degree := map[string]float64{}
	adjacency := map[string][]string{}
	neighborSeen := map[string]map[string]bool{}
	add := func(a, b string, weight float64) {
		if a == "" || b == "" || a == b {
			return
		}
		pair := weightedPair{minString(a, b), maxString(a, b)}
		weights[pair] += weight
		if neighborSeen[a] == nil {
			neighborSeen[a] = map[string]bool{}
		}
		if neighborSeen[b] == nil {
			neighborSeen[b] = map[string]bool{}
		}
		if !neighborSeen[a][b] {
			adjacency[a] = append(adjacency[a], b)
			neighborSeen[a][b] = true
		}
		if !neighborSeen[b][a] {
			adjacency[b] = append(adjacency[b], a)
			neighborSeen[b][a] = true
		}
	}
	for _, edge := range g.Edges {
		weight := edge.ConfidenceScore
		if weight <= 0 {
			weight = edge.Confidence.Score()
		}
		if edge.Confidence == ConfidenceAmbiguous {
			weight *= ambiguousScale
		}
		add(edge.Source, edge.Target, weight)
	}
	for node, neighbors := range adjacency {
		sort.Strings(neighbors)
		adjacency[node] = neighbors
		for _, neighbor := range neighbors {
			degree[node] += weights[weightedPair{minString(node, neighbor), maxString(node, neighbor)}]
		}
	}
	return weights, degree, adjacency
}

func hubNodes(degree map[string]float64, ids []string) map[string]bool {
	hubs := map[string]bool{}
	if len(ids) < 20 {
		return hubs
	}
	sorted := make([]float64, 0, len(ids))
	for _, id := range ids {
		sorted = append(sorted, degree[id])
	}
	sort.Float64s(sorted)
	cut := sorted[int(math.Floor(float64(len(sorted)-1)*0.99))]
	for _, id := range ids {
		if degree[id] > cut {
			hubs[id] = true
		}
	}
	return hubs
}

func refineComponents(community []int, ids []string, adjacency map[string][]string) []int {
	next := make([]int, len(community))
	copy(next, community)
	groups := map[int][]string{}
	for i, id := range ids {
		groups[community[i]] = append(groups[community[i]], id)
	}
	keys := make([]int, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Ints(keys)
	index := map[string]int{}
	for i, id := range ids {
		index[id] = i
	}
	nextID := 0
	for _, key := range keys {
		members := append([]string(nil), groups[key]...)
		sort.Strings(members)
		remaining := map[string]bool{}
		for _, member := range members {
			remaining[member] = true
		}
		for _, member := range members {
			if !remaining[member] {
				continue
			}
			queue := []string{member}
			remaining[member] = false
			for len(queue) > 0 {
				current := queue[0]
				queue = queue[1:]
				next[index[current]] = nextID
				for _, neighbor := range adjacency[current] {
					if remaining[neighbor] {
						remaining[neighbor] = false
						queue = append(queue, neighbor)
					}
				}
			}
			nextID++
		}
	}
	return next
}

func reattachHubs(community []int, ids []string, adjacency map[string][]string, weights map[weightedPair]float64, hubs map[string]bool) []int {
	index := map[string]int{}
	for i, id := range ids {
		index[id] = i
	}
	for _, id := range ids {
		if !hubs[id] {
			continue
		}
		votes := map[int]float64{}
		for _, neighbor := range adjacency[id] {
			votes[community[index[neighbor]]] += weights[weightedPair{minString(id, neighbor), maxString(id, neighbor)}]
		}
		if len(votes) == 0 {
			continue
		}
		best, bestWeight := -1, -1.0
		candidates := make([]int, 0, len(votes))
		for c := range votes {
			candidates = append(candidates, c)
		}
		sort.Ints(candidates)
		for _, c := range candidates {
			if votes[c] > bestWeight {
				best, bestWeight = c, votes[c]
			}
		}
		community[index[id]] = best
	}
	return community
}

func splitCommunities(community []int, ids []string, adjacency map[string][]string, weights map[weightedPair]float64) []int {
	index := make(map[string]int, len(ids))
	for i, id := range ids {
		index[id] = i
	}
	for round := 0; round < 12; round++ {
		changed := false
		groups := map[int][]string{}
		for i, id := range ids {
			groups[community[i]] = append(groups[community[i]], id)
		}
		next := make([]int, len(community))
		copy(next, community)
		nextID := 0
		keys := make([]int, 0, len(groups))
		for key := range groups {
			keys = append(keys, key)
		}
		sort.Ints(keys)
		for _, key := range keys {
			members := groups[key]
			sort.Strings(members)
			cohesion := cohesionOf(members, adjacency, weights)
			oversized := len(ids) >= 20 && len(members)*4 > len(ids)
			lowCohesion := len(members) >= 6 && cohesion < 0.05
			if !oversized && !lowCohesion {
				for _, member := range members {
					next[index[member]] = nextID
				}
				nextID++
				continue
			}
			left := members[:len(members)/2]
			right := members[len(members)/2:]
			for _, group := range [][]string{left, right} {
				for _, member := range group {
					next[index[member]] = nextID
				}
				nextID++
			}
			changed = true
		}
		community = refineComponents(next, ids, adjacency)
		if !changed {
			break
		}
	}
	return community
}

func cohesionOf(members []string, adjacency map[string][]string, weights map[weightedPair]float64) float64 {
	n := len(members)
	if n < 2 {
		return 1
	}
	inSet := map[string]bool{}
	for _, member := range members {
		inSet[member] = true
	}
	internal := 0.0
	for _, member := range members {
		for _, neighbor := range adjacency[member] {
			if inSet[neighbor] && member < neighbor {
				internal += weights[weightedPair{minString(member, neighbor), maxString(member, neighbor)}]
			}
		}
	}
	return 2 * internal / float64(n*(n-1))
}

func summarizeCommunities(g *Graph, community []int, ids []string, adjacency map[string][]string, weights map[weightedPair]float64) []CommunitySummary {
	index := map[string]int{}
	for i, id := range ids {
		index[id] = i
	}
	groups := map[int][]string{}
	for i, id := range ids {
		groups[community[i]] = append(groups[community[i]], id)
	}
	type sizeKey struct {
		key  int
		size int
		head string
	}
	keys := make([]sizeKey, 0, len(groups))
	for key, members := range groups {
		sort.Strings(members)
		keys = append(keys, sizeKey{key: key, size: len(members), head: members[0]})
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].size != keys[j].size {
			return keys[i].size > keys[j].size
		}
		return keys[i].head < keys[j].head
	})
	nodeByID := map[string]Node{}
	for _, node := range g.Nodes {
		nodeByID[node.ID] = node
	}
	summaries := make([]CommunitySummary, 0, len(keys))
	for newID, entry := range keys {
		members := groups[entry.key]
		label := communityLabel(members, nodeByID, adjacency)
		for _, member := range members {
			if i, ok := index[member]; ok {
				community[i] = newID
			}
		}
		summaries = append(summaries, CommunitySummary{
			ID: newID, Label: label, Size: len(members),
			Cohesion: round3(cohesionOf(members, adjacency, weights)),
			Members:  members,
		})
	}
	for i := range g.Nodes {
		if idx, ok := index[g.Nodes[i].ID]; ok {
			g.Nodes[i].Community = community[idx]
		}
	}
	return summaries
}

func communityLabel(members []string, nodeByID map[string]Node, adjacency map[string][]string) string {
	if len(members) > 1 {
		isolated := true
		for _, member := range members {
			if len(adjacency[member]) > 0 {
				isolated = false
				break
			}
		}
		if isolated {
			return "unclustered"
		}
	}
	bestID := ""
	bestDegree := -1
	for _, member := range members {
		node := nodeByID[member]
		if node.Kind == "file" || node.Kind == "package" {
			continue
		}
		degree := len(adjacency[member])
		if degree > bestDegree || (degree == bestDegree && member < bestID) {
			bestID, bestDegree = member, degree
		}
	}
	if bestID == "" {
		bestID = members[0]
	}
	return nodeByID[bestID].Label
}

// ModularityQ returns the modularity of the current Node.Community assignment.
func ModularityQ(g *Graph) float64 {
	if g == nil || len(g.Nodes) == 0 {
		return 0
	}
	community := map[string]int{}
	for _, node := range g.Nodes {
		community[node.ID] = node.Community
	}
	weights, degree, _ := canonicalWeights(g)
	m := 0.0
	for _, weight := range weights {
		m += weight
	}
	if m == 0 {
		return 0
	}
	internal := map[int]float64{}
	for pair, weight := range weights {
		if community[pair.a] == community[pair.b] {
			internal[community[pair.a]] += weight
		}
	}
	total := map[int]float64{}
	for id, degreeWeight := range degree {
		total[community[id]] += degreeWeight
	}
	q := 0.0
	seen := map[int]bool{}
	for _, node := range g.Nodes {
		c := community[node.ID]
		if seen[c] {
			continue
		}
		seen[c] = true
		q += internal[c]/m - math.Pow(total[c]/(2*m), 2)
	}
	return round3(q)
}

func minString(a, b string) string {
	if a < b {
		return a
	}
	return b
}

func maxString(a, b string) string {
	if a > b {
		return a
	}
	return b
}

func round3(value float64) float64 {
	return math.Round(value*1000) / 1000
}

// mergeSingletonCommunities folds each singleton into its strongest neighbor
// community and groups fully isolated nodes into one unclustered community.
func mergeSingletonCommunities(community []int, ids []string, adjacency map[string][]string, weights map[weightedPair]float64) []int {
	index := make(map[string]int, len(ids))
	sizes := map[int]int{}
	for i, id := range ids {
		index[id] = i
		sizes[community[i]]++
	}
	parent := map[int]int{}
	for _, key := range community {
		parent[key] = key
	}
	var find func(int) int
	find = func(key int) int {
		if parent[key] != key {
			parent[key] = find(parent[key])
		}
		return parent[key]
	}
	union := func(a, b int) {
		ra, rb := find(a), find(b)
		if ra == rb {
			return
		}
		if ra < rb {
			parent[rb] = ra
		} else {
			parent[ra] = rb
		}
	}
	var isolated []int
	for i, id := range ids {
		if sizes[community[i]] != 1 {
			continue
		}
		if len(adjacency[id]) == 0 {
			isolated = append(isolated, community[i])
			continue
		}
		neighborWeights := map[int]float64{}
		for _, neighbor := range adjacency[id] {
			key := weightedPair{minString(id, neighbor), maxString(id, neighbor)}
			neighborWeights[community[index[neighbor]]] += weights[key]
		}
		bestCommunity, bestWeight := -1, -1.0
		for c, weight := range neighborWeights {
			if weight > bestWeight+1e-12 || (math.Abs(weight-bestWeight) <= 1e-12 && (bestCommunity == -1 || c < bestCommunity)) {
				bestCommunity, bestWeight = c, weight
			}
		}
		if bestCommunity >= 0 && bestCommunity != community[i] {
			union(community[i], bestCommunity)
		}
	}
	sort.Ints(isolated)
	if len(isolated) > 1 {
		for _, key := range isolated[1:] {
			union(isolated[0], key)
		}
	}
	for i := range community {
		community[i] = find(community[i])
	}
	return community
}
