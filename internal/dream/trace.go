package dream

// RecallTraceEntry records one candidate's path through the ranking stages.
type RecallTraceEntry struct {
	Stage        string  `json:"stage"`
	ID           string  `json:"id"`
	Kind         string  `json:"kind"`
	Base         float64 `json:"base"`
	Match        int     `json:"match"`
	ProjectBoost float64 `json:"project_boost"`
	FinalScore   float64 `json:"final_score"`
	Decision     string  `json:"decision"`
	Reason       string  `json:"reason,omitempty"`
}

// RecallTrace collects staged candidates for inspect surfaces.
type RecallTrace struct {
	Entries []RecallTraceEntry `json:"entries"`
}

// Add appends one trace entry.
func (t *RecallTrace) Add(entry RecallTraceEntry) {
	if t == nil {
		return
	}
	t.Entries = append(t.Entries, entry)
}

// FinalizeTrace updates final scores for kept hits and marks the rows dropped
// by the limit.
func FinalizeTrace(trace *RecallTrace, hits []RecallHit, limit int) {
	if trace == nil {
		return
	}
	kept := map[string]float64{}
	for _, hit := range hits {
		kept[hit.ID] = hit.Score
	}
	for i := range trace.Entries {
		entry := &trace.Entries[i]
		if entry.Decision != "kept" {
			continue
		}
		if score, ok := kept[entry.ID]; ok {
			entry.FinalScore = score
			continue
		}
		entry.Decision = "limit"
		entry.Reason = "beyond the result limit"
	}
}
