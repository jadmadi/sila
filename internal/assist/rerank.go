package assist

import (
	"context"
	"fmt"
	"math"
	"time"
)

// RerankCandidate represents an item to be evaluated against a query.
type RerankCandidate struct {
	Key     string `json:"key"`
	Excerpt string `json:"excerpt"`
}

// RerankResult contains semantic relevance probabilities and global presence score.
type RerankResult struct {
	Scores          map[string]float64 `json:"scores"`
	AnswerExists    float64            `json:"answer_exists"`
	HasAnswerExists bool               `json:"has_answer_exists"`
	DurationMs      int64              `json:"duration_ms"`
}

// Rerank evaluates candidates in parallel against a search query using System One.
// It returns relevance probabilities in [0, 1] per candidate key.
// Bounded execution: enforces a maximum candidate count and fail-open timeout.
func Rerank(ctx context.Context, p Provider, query string, candidates []RerankCandidate) (*RerankResult, error) {
	if p == nil || query == "" || len(candidates) == 0 {
		return nil, nil
	}

	// Limit to top 25 candidates to stay comfortably within sub-300ms interactive budgets
	sample := candidates
	if len(sample) > 25 {
		sample = sample[:25]
	}

	state := make(map[string]any)
	state["query"] = query
	candMap := make(map[string]string, len(sample))

	questions := make(map[string]Question, len(sample)+1)
	for i, c := range sample {
		key := fmt.Sprintf("c%02d", i)
		excerpt := c.Excerpt
		if len(excerpt) > 280 {
			excerpt = excerpt[:280] + "…"
		}
		candMap[key] = excerpt

		questions[fmt.Sprintf("rel_%02d", i)] = Question{
			Type:         TypeNoul,
			Instructions: fmt.Sprintf("Does candidate %s provide relevant information or directly address the search query?", key),
		}
	}
	state["candidates"] = candMap

	questions["answer_exists"] = Question{
		Type:         TypeNoul,
		Instructions: "Does at least one candidate directly provide relevant information for the search query?",
	}

	subCtx, cancel := context.WithTimeout(ctx, 2000*time.Millisecond)
	defer cancel()

	resp, err := p.Evaluate(subCtx, "", state, questions)
	if err != nil {
		return nil, err
	}

	result := &RerankResult{
		Scores:     make(map[string]float64, len(sample)),
		DurationMs: resp.DurationMs,
	}

	for i, c := range sample {
		qID := fmt.Sprintf("rel_%02d", i)
		if ans, ok := resp.Nouls[qID]; ok {
			// Validate finite probability in [0.0, 1.0]
			if !math.IsNaN(ans.Noul) && !math.IsInf(ans.Noul, 0) && ans.Noul >= 0.0 && ans.Noul <= 1.0 {
				result.Scores[c.Key] = ans.Noul
			}
		}
	}

	// Reject partial answers: all candidates must have valid scores to avoid partial reranking
	if len(result.Scores) < len(sample) {
		result.Scores = make(map[string]float64)
	}

	if ans, ok := resp.Nouls["answer_exists"]; ok {
		if !math.IsNaN(ans.Noul) && !math.IsInf(ans.Noul, 0) && ans.Noul >= 0.0 && ans.Noul <= 1.0 {
			result.AnswerExists = ans.Noul
			result.HasAnswerExists = true
		}
	}

	return result, nil
}
