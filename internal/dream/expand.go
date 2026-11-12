package dream

import (
	"strings"

	"github.com/jadmadi/sila/internal/store"
)

// RecallHitsByIDs loads full items in request order and reports unknown ids.
func RecallHitsByIDs(st *store.Store, ids []string) (hits []RecallHit, missing []string) {
	seen := map[string]bool{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		fact, err := st.FactByID(id)
		if err != nil || fact == nil {
			missing = append(missing, id)
			continue
		}
		hits = append(hits, recallHitFromFact(*fact))
	}
	return hits, missing
}

// RecallTimeline expands an anchor into chronological neighbours, oldest first.
func RecallTimeline(st *store.Store, projectID, anchorID string, before, after int) (hits []RecallHit, reason string) {
	anchor, err := st.FactByID(anchorID)
	if err != nil || anchor == nil {
		return nil, "anchor not found"
	}
	if projectID != "" && anchor.ProjectID != projectID {
		return nil, "anchor not found in project"
	}
	facts, err := st.FactsAround(anchor.ProjectID, anchor.ID, before, after)
	if err != nil {
		return nil, "timeline query failed"
	}
	for _, fact := range facts {
		hits = append(hits, recallHitFromFact(fact))
	}
	return hits, ""
}

func recallHitFromFact(fact store.Fact) RecallHit {
	kind := strings.TrimSpace(fact.Kind)
	if kind == "" {
		kind = "fact"
	}
	return RecallHit{
		Kind:       kind,
		ID:         fact.ID,
		ProjectID:  fact.ProjectID,
		Timestamp:  fact.FirstSeen,
		Symptom:    firstLine(fact.Claim),
		Resolution: fact.Claim,
		Grounded:   fact.FileRefsExist,
		Score:      1,
		SourceRef:  fact.SourceRef,
	}
}
