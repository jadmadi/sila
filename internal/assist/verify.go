package assist

import (
	"context"
	"fmt"
	"time"
)

// FactVerificationItem represents a claim and its candidate file excerpt for verification.
type FactVerificationItem struct {
	ID          string `json:"id"`
	Claim       string `json:"claim"`
	RepoFile    string `json:"repo_file"`
	CodeExcerpt string `json:"code_excerpt"`
}

// FactVerificationResult contains the semantic evaluation of a claim against code.
type FactVerificationResult struct {
	ID            string  `json:"id"`
	Claim         string  `json:"claim"`
	RepoFile      string  `json:"repo_file"`
	Substantiated float64 `json:"substantiated"` // P(code confirms claim)
	Contradicted  float64 `json:"contradicted"`  // P(code refutes claim)
	Verdict       string  `json:"verdict"`       // "substantiated", "contradicted", "indeterminate"
	DurationMs    int64   `json:"duration_ms"`
}

// VerifyFacts audits a batch of claims against their on-disk file excerpts using TypeSafe System One.
// It executes judgments in a single parallel call, returning sub-300ms verification.
func VerifyFacts(ctx context.Context, p Provider, items []FactVerificationItem) ([]FactVerificationResult, error) {
	if p == nil || len(items) == 0 {
		return nil, nil
	}

	// Limit to top 20 items to stay safely within sub-300ms interactive budget
	sample := items
	if len(sample) > 20 {
		sample = sample[:20]
	}

	state := make(map[string]any)
	factsMap := make(map[string]map[string]string, len(sample))
	questions := make(map[string]Question, len(sample)*2)

	for i, it := range sample {
		key := fmt.Sprintf("f%02d", i)
		excerpt := it.CodeExcerpt
		if len(excerpt) > 400 {
			excerpt = excerpt[:400] + "…"
		}
		factsMap[key] = map[string]string{
			"claim":   it.Claim,
			"file":    it.RepoFile,
			"excerpt": excerpt,
		}

		questions[fmt.Sprintf("sub_%02d", i)] = Question{
			Type:         TypeNoul,
			Instructions: fmt.Sprintf("Does the code excerpt for %s substantiate, implement, or agree with its factual claim?", key),
		}
		questions[fmt.Sprintf("contra_%02d", i)] = Question{
			Type:         TypeNoul,
			Instructions: fmt.Sprintf("Does the code excerpt for %s directly contradict, refute, or show behavior opposite to its factual claim?", key),
		}
	}
	state["facts"] = factsMap

	start := time.Now()
	resp, err := p.Evaluate(ctx, "", state, questions)
	elapsed := time.Since(start).Milliseconds()
	if err != nil {
		return nil, err
	}

	results := make([]FactVerificationResult, 0, len(sample))
	for i, it := range sample {
		subKey := fmt.Sprintf("sub_%02d", i)
		contraKey := fmt.Sprintf("contra_%02d", i)

		subAns, subOK := resp.Nouls[subKey]
		contraAns, contraOK := resp.Nouls[contraKey]

		if !subOK || !contraOK {
			results = append(results, FactVerificationResult{
				ID:            it.ID,
				Claim:         it.Claim,
				RepoFile:      it.RepoFile,
				Substantiated: 0,
				Contradicted:  0,
				Verdict:       "indeterminate",
				DurationMs:    elapsed,
			})
			continue
		}

		subScore := subAns.Noul
		contraScore := contraAns.Noul
		verdict := "indeterminate"
		if subScore >= 0.70 && contraScore < 0.35 {
			verdict = "substantiated"
		} else if contraScore >= 0.70 && subScore < 0.35 {
			verdict = "contradicted"
		}

		results = append(results, FactVerificationResult{
			ID:            it.ID,
			Claim:         it.Claim,
			RepoFile:      it.RepoFile,
			Substantiated: subScore,
			Contradicted:  contraScore,
			Verdict:       verdict,
			DurationMs:    elapsed,
		})
	}

	return results, nil
}

// VerifyFact audits a single claim against a code excerpt.
func VerifyFact(ctx context.Context, p Provider, claim, repoFile, codeExcerpt string) (*FactVerificationResult, error) {
	res, err := VerifyFacts(ctx, p, []FactVerificationItem{{
		Claim:       claim,
		RepoFile:    repoFile,
		CodeExcerpt: codeExcerpt,
	}})
	if err != nil || len(res) == 0 {
		return nil, err
	}
	return &res[0], nil
}
