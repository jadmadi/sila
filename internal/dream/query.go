package dream

import (
	"slices"
	"sort"
	"strings"

	"github.com/jadmadi/sila/internal/store"
)

// MatchScore counts how many of terms appear (case-insensitive) in the claim.
func MatchScore(f store.Fact, terms []string) int {
	claim := strings.ToLower(f.Claim)
	n := 0
	for _, t := range terms {
		if t != "" && strings.Contains(claim, strings.ToLower(t)) {
			n++
		}
	}
	return n
}

// weightSum maps a corroboration tier to a ranking tiebreak weight.
func weightSum(confidence string) int {
	switch confidence {
	case "invariant":
		return 15
	case "corroborated", "high":
		return 10
	case "grounded", "mid":
		return 5
	default:
		return 2
	}
}

// Weight exposes the tiebreak weight (used by tests/CLI). Kept for compat.
func Weight(confidence string) int { return weightSum(confidence) }

// score ranks claims primarily by term relevance (MatchScore << relevance), so
// a query returns the claims most about its terms. FileRefsExist adds a tiebreak
// bonus and corroboration a small tertiary weight.
func score(f store.Fact, terms []string) int {
	m := MatchScore(f, terms)
	s := m * 1000
	if f.IsInvariant() || f.FileRefsExist || f.Verified {
		s += 200
	}
	s += weightSum(f.Corroboration)
	return s
}

// Rank orders claims best-first. When terms are provided, claims that match none
// of them are dropped. refsExistOnly drops claims whose referenced files do not exist on disk.
func Rank(facts []store.Fact, terms []string, refsExistOnly bool) []store.Fact {
	out := make([]store.Fact, 0, len(facts))
	for _, f := range facts {
		if refsExistOnly && !f.IsInvariant() && !(f.FileRefsExist || f.Verified) {
			continue
		}
		if len(terms) > 0 && MatchScore(f, terms) == 0 {
			continue
		}
		out = append(out, f)
	}
	sort.SliceStable(out, func(i, j int) bool {
		si, sj := score(out[i], terms), score(out[j], terms)
		if si != sj {
			return si > sj
		}
		return out[i].FirstSeen > out[j].FirstSeen
	})
	return out
}

// QueryStoreFacts queries claims from the store using FTS5 when available with fallback to in-memory Rank.
func QueryStoreFacts(st *store.Store, terms []string, projectID, tool string, refsExistOnly, staleOnly, factsOnly bool, top int) ([]store.Fact, error) {
	return QueryStoreFactsPaged(st, terms, projectID, tool, refsExistOnly, staleOnly, factsOnly, top, 0)
}

// QueryStoreFactsPaged queries claims with pagination (limit and offset) support.
// Archived and superseded memories are excluded by default.
func QueryStoreFactsPaged(st *store.Store, terms []string, projectID, tool string, refsExistOnly, staleOnly, factsOnly bool, limit, offset int) ([]store.Fact, error) {
	return QueryStoreFactsPagedIncludingArchived(st, terms, projectID, tool, refsExistOnly, staleOnly, factsOnly, limit, offset, false)
}

// QueryStoreFactsPagedIncludingArchived is QueryStoreFactsPaged with an
// explicit opt-in for archived and superseded memories.
func QueryStoreFactsPagedIncludingArchived(st *store.Store, terms []string, projectID, tool string, refsExistOnly, staleOnly, factsOnly bool, limit, offset int, includeArchived bool) ([]store.Fact, error) {
	facts, err := queryStoreFactsPaged(st, terms, projectID, tool, refsExistOnly, staleOnly, factsOnly, limit, offset)
	if err != nil || includeArchived {
		return facts, err
	}
	excluded, blockErr := st.NonCurrentMemoryFactIDs()
	if blockErr != nil {
		return facts, nil
	}
	return store.FilterNonCurrentFacts(facts, excluded), nil
}

func queryStoreFactsPaged(st *store.Store, terms []string, projectID, tool string, refsExistOnly, staleOnly, factsOnly bool, limit, offset int) ([]store.Fact, error) {
	if staleOnly {
		refsExistOnly = false
	}
	if len(terms) == 0 {
		var facts []store.Fact
		var err error
		if projectID != "" {
			facts, err = st.FactsByProject(projectID)
		} else {
			facts, err = st.FactsAll()
		}
		if err != nil {
			return nil, err
		}
		if tool != "" {
			facts = filterByTool(facts, tool)
		}
		if factsOnly {
			var onlyFacts []store.Fact
			for _, f := range facts {
				if f.IsFact() {
					onlyFacts = append(onlyFacts, f)
				}
			}
			facts = onlyFacts
		}
		if staleOnly {
			facts = store.FilterStale(facts)
		}
		facts = Rank(facts, nil, refsExistOnly)
		if offset > 0 {
			if offset >= len(facts) {
				return nil, nil
			}
			facts = facts[offset:]
		}
		if limit > 0 && len(facts) > limit {
			facts = facts[:limit]
		}
		return facts, nil
	}

	// Try FTS5 search first
	queryStr := strings.Join(terms, " ")
	ftsFacts, err := st.SearchFactsFTS(queryStr, projectID, refsExistOnly, 0)
	if err == nil && len(ftsFacts) > 0 {
		if tool != "" {
			ftsFacts = filterByTool(ftsFacts, tool)
		}
		if factsOnly {
			var onlyFacts []store.Fact
			for _, f := range ftsFacts {
				if f.IsFact() {
					onlyFacts = append(onlyFacts, f)
				}
			}
			ftsFacts = onlyFacts
		}
		if staleOnly {
			ftsFacts = store.FilterStale(ftsFacts)
		}
		ranked := Rank(ftsFacts, terms, refsExistOnly)
		if offset > 0 {
			if offset >= len(ranked) {
				return nil, nil
			}
			ranked = ranked[offset:]
		}
		if limit > 0 && len(ranked) > limit {
			ranked = ranked[:limit]
		}
		return ranked, nil
	}

	// Fallback to in-memory matching across all project facts
	var allFacts []store.Fact
	if projectID != "" {
		allFacts, err = st.FactsByProject(projectID)
	} else {
		allFacts, err = st.FactsAll()
	}
	if err != nil {
		return nil, err
	}
	if tool != "" {
		allFacts = filterByTool(allFacts, tool)
	}
	if factsOnly {
		var onlyFacts []store.Fact
		for _, f := range allFacts {
			if f.IsFact() {
				onlyFacts = append(onlyFacts, f)
			}
		}
		allFacts = onlyFacts
	}
	if staleOnly {
		allFacts = store.FilterStale(allFacts)
	}
	ranked := Rank(allFacts, terms, refsExistOnly)
	if offset > 0 {
		if offset >= len(ranked) {
			return nil, nil
		}
		ranked = ranked[offset:]
	}
	if limit > 0 && len(ranked) > limit {
		ranked = ranked[:limit]
	}
	return ranked, nil
}

func filterByTool(facts []store.Fact, tool string) []store.Fact {
	out := make([]store.Fact, 0, len(facts))
	for _, f := range facts {
		if slices.Contains(f.Tools, tool) {
			out = append(out, f)
		}
	}
	return out
}
