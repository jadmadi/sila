package mcp

import (
	"time"

	"github.com/jadmadi/sila/internal/dream"
	"github.com/jadmadi/sila/internal/store"
)

type asOfPayload struct {
	AsOf       string            `json:"as_of"`
	Historical bool              `json:"historical"`
	Clamped    bool              `json:"clamped,omitempty"`
	Facts      []store.Fact      `json:"facts,omitempty"`
	Hits       []dream.RecallHit `json:"hits,omitempty"`
}

// parseAsOfArg parses an MCP as_of argument.
func parseAsOfArg(raw string) (time.Time, bool, string) {
	when, clamped, err := store.ParseAsOf(raw, time.Now().UTC())
	if err != nil {
		return time.Time{}, false, err.Error()
	}
	return when, clamped, ""
}

func historicalRecallHits(facts []store.Fact, records []store.MemoryRecord) []dream.RecallHit {
	hits := make([]dream.RecallHit, 0, len(facts)+len(records))
	for _, fact := range facts {
		kind := fact.Kind
		if kind == "" {
			kind = "fact"
		}
		hits = append(hits, dream.RecallHit{
			Kind: kind, ID: fact.ID, ProjectID: fact.ProjectID,
			Timestamp: fact.FirstSeen, Resolution: fact.Claim,
			Grounded: fact.FileRefsExist, Score: 1,
		})
	}
	for _, record := range records {
		hits = append(hits, dream.RecallHit{
			Kind: "memo", ID: record.ID, ProjectID: record.ProjectID,
			Timestamp: record.CreatedAt, Resolution: record.Summary, Score: 1,
		})
	}
	return hits
}

func clampSuffix(clamped bool) string {
	if clamped {
		return " (clamped to now)"
	}
	return ""
}
