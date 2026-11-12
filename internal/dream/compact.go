package dream

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/jadmadi/sila/internal/store"
)

const (
	// CompactMaxRows bounds one compact response.
	CompactMaxRows = 25
	// CompactMaxTokens bounds the estimated token cost of compact rows.
	CompactMaxTokens = 1200
	// CompactTitleRunes bounds the compact title before the ellipsis.
	CompactTitleRunes = 80
)

// CompactHit is one layer-0 index row.
type CompactHit struct {
	ID     string  `json:"id"`
	Kind   string  `json:"kind"`
	Title  string  `json:"title"`
	When   string  `json:"when,omitempty"`
	Score  float64 `json:"score"`
	Tokens int     `json:"tokens"`
}

// CompactResult is the compact response with cap accounting.
type CompactResult struct {
	Hits      []CompactHit `json:"hits"`
	Truncated bool         `json:"truncated"`
	Tokens    int          `json:"tokens"`
}

// CompactHits projects ranked hits into at most limit rows and tokenBudget
// estimated tokens, truncating titles on rune boundaries.
func CompactHits(hits []RecallHit, limit, tokenBudget int) CompactResult {
	if limit <= 0 || limit > CompactMaxRows {
		limit = CompactMaxRows
	}
	if tokenBudget <= 0 || tokenBudget > CompactMaxTokens {
		tokenBudget = CompactMaxTokens
	}
	ordered := append([]RecallHit{}, hits...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Score == ordered[j].Score {
			return ordered[i].ID < ordered[j].ID
		}
		return ordered[i].Score > ordered[j].Score
	})
	result := CompactResult{Hits: []CompactHit{}}
	for _, hit := range ordered {
		if len(result.Hits) >= limit {
			result.Truncated = true
			break
		}
		row := CompactHit{
			ID:    hit.ID,
			Kind:  hit.Kind,
			Title: compactTitle(hit),
			When:  hit.Timestamp,
			Score: hit.Score,
		}
		raw, _ := json.Marshal(row)
		cost := store.EstimateTokens(len(raw))
		if result.Tokens+cost > tokenBudget {
			result.Truncated = true
			break
		}
		row.Tokens = cost
		result.Tokens += cost
		result.Hits = append(result.Hits, row)
	}
	return result
}

func compactTitle(hit RecallHit) string {
	text := hit.Symptom
	if strings.TrimSpace(text) == "" {
		text = hit.Resolution
	}
	text = strings.TrimSpace(text)
	runes := []rune(text)
	if len(runes) <= CompactTitleRunes {
		return text
	}
	return string(runes[:CompactTitleRunes]) + "..."
}

// CompactOmniHits projects omni results into layer-0 rows with the same caps.
func CompactOmniHits(items []OmniResultItem, limit, tokenBudget int) CompactResult {
	if limit <= 0 || limit > CompactMaxRows {
		limit = CompactMaxRows
	}
	if tokenBudget <= 0 || tokenBudget > CompactMaxTokens {
		tokenBudget = CompactMaxTokens
	}
	ordered := append([]OmniResultItem{}, items...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Score == ordered[j].Score {
			return ordered[i].ID < ordered[j].ID
		}
		return ordered[i].Score > ordered[j].Score
	})
	result := CompactResult{Hits: []CompactHit{}}
	for _, item := range ordered {
		if len(result.Hits) >= limit {
			result.Truncated = true
			break
		}
		title := item.Title
		if strings.TrimSpace(title) == "" {
			title = item.Content
		}
		row := CompactHit{
			ID: item.ID, Kind: string(item.Type),
			Title: truncateCompactText(title), When: item.Timestamp, Score: float64(item.Score),
		}
		raw, _ := json.Marshal(row)
		cost := store.EstimateTokens(len(raw))
		if result.Tokens+cost > tokenBudget {
			result.Truncated = true
			break
		}
		row.Tokens = cost
		result.Tokens += cost
		result.Hits = append(result.Hits, row)
	}
	return result
}

func truncateCompactText(text string) string {
	text = strings.TrimSpace(text)
	runes := []rune(text)
	if len(runes) <= CompactTitleRunes {
		return text
	}
	return string(runes[:CompactTitleRunes]) + "..."
}
