package dream

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/assist"
	"github.com/jadmadi/sila/internal/store"
)

// OmniItemType identifies the origin tier of a search hit.
type OmniItemType string

// OmniItemType constants specify the evidentiary origin tier of an omni search result.
const (
	OmniTypeInvariant OmniItemType = "invariant"
	OmniTypeFact      OmniItemType = "fact"
	// OmniTypeCandidate marks an inferred, unapproved directive. It is
	// deliberately distinct from a fact: presenting a candidate as a fact is
	// what let a quoted transcript line read as an authorized rule.
	OmniTypeCandidate OmniItemType = "candidate"
	OmniTypeCommit    OmniItemType = "commit"
	OmniTypeMessage   OmniItemType = "message"
	OmniTypeDoc       OmniItemType = "doc"
	OmniTypeHandoff   OmniItemType = "handoff"
)

// Tier scores group results by evidentiary primacy instead of comparing
// incomparable numbers. Within a tier, the number of matched query terms
// orders results, so a tier's hits stay ranked relative to each other.
//
// The ordering encodes an opinion the evaluation argued for: primary evidence
// (raw transcripts, commits) must not be buried under derived knowledge, and an
// unapproved candidate must never outrank it.
const (
	tierInvariant = 5000 // authorized directives (explicit memos and approved invariants)
	tierFact      = 4000 // approved, grounded claims
	tierPrimary   = 3000 // raw transcripts and commits
	tierDoc       = 2000 // attached reference documentation
	tierCandidate = 1000 // inferred candidates awaiting review
)

// OmniResultItem is a unified search hit across all memory tiers.
type OmniResultItem struct {
	Type       OmniItemType `json:"type"`
	ID         string       `json:"id"`
	ProjectID  string       `json:"project_id"`
	Title      string       `json:"title,omitempty"`
	Content    string       `json:"content"`
	Confidence string       `json:"confidence,omitempty"`
	Verified   bool         `json:"verified,omitempty"`
	RepoFiles  []string     `json:"repo_files,omitempty"`
	Tools      []string     `json:"tools,omitempty"`
	Timestamp  string       `json:"timestamp,omitempty"`
	Score      int          `json:"score"`
	Frecency   int          `json:"frecency,omitempty"`
	// Authority context, carried through every result format so a consumer
	// cannot mistake an observation for a rule.
	ReviewStatus string `json:"review_status,omitempty"`
	OriginRole   string `json:"origin_role,omitempty"`
	Quoted       bool   `json:"quoted,omitempty"`
}

// OmniSearchResult wraps matched items across all tiers.
type OmniSearchResult struct {
	Query            string           `json:"query"`
	Project          string           `json:"project,omitempty"`
	TotalHits        int              `json:"total_hits"` // legacy: number of items returned
	Returned         int              `json:"returned"`   // items in this response
	Matched          int              `json:"matched"`    // matching records found before the limit
	Items            []OmniResultItem `json:"items"`
	SemanticReranked bool             `json:"semantic_reranked,omitempty"`
	// Warnings names tiers that failed or filters that could not apply, so a
	// broken index never looks like "no evidence".
	Warnings []string `json:"warnings,omitempty"`
}

// OmniOptions filters an omni search. Zero values mean "no filter".
type OmniOptions struct {
	Project  string
	Limit    int
	Tool     string
	Role     string
	Since    string // RFC3339 lower bound; empty means all time
	Semantic bool   // whether to apply semantic reranking via assist provider
}

// OmniSearch executes a blended multi-source search across Architectural
// Invariants, Distilled Code Claims, Raw Transcripts, Git Commits, External
// Docs, and session handoffs.
func OmniSearch(st *store.Store, query, project string, limit int) (*OmniSearchResult, error) {
	return OmniSearchFiltered(st, query, OmniOptions{Project: project, Limit: limit})
}

// OmniSearchFiltered is OmniSearch with explicit filters. A tier that fails
// contributes a warning rather than silently shrinking the result set, and
// filters that a tier cannot honor are reported instead of being ignored.
func OmniSearchFiltered(st *store.Store, query string, opts OmniOptions) (*OmniSearchResult, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = 15
	}
	project := opts.Project

	target, cleanQuery := store.ParseTemporalAnchor(query, time.Now().UTC())
	effectiveQuery := query
	if target != nil && strings.TrimSpace(cleanQuery) != "" {
		effectiveQuery = cleanQuery
	}
	terms := strings.Fields(strings.ToLower(effectiveQuery))

	var items []OmniResultItem
	var warnings []string

	// 1. Distilled facts, invariants, and candidates.
	facts, err := st.SearchFactsFTS(effectiveQuery, project, false, limit)
	if err != nil {
		warnings = append(warnings, fmt.Sprintf("claims tier failed: %v", err))
	} else {
		for _, f := range facts {
			if opts.Tool != "" && !containsFold(f.Tools, opts.Tool) {
				continue
			}
			if opts.Since != "" && f.FirstSeen != "" && f.FirstSeen < opts.Since {
				continue
			}
			score := factScore(f, terms)
			if target != nil {
				if tDoc, ok := store.ParseDocTimestamp(f.FirstSeen); ok {
					boost := store.CalculateTemporalBoost(tDoc, target)
					score += int(math.Round(boost * 400.0))
				}
			}
			items = append(items, OmniResultItem{
				Type:         factItemType(f),
				ID:           f.ID,
				ProjectID:    f.ProjectID,
				Content:      f.Claim,
				Confidence:   f.Confidence,
				Verified:     f.FileRefsExist || f.Verified,
				RepoFiles:    f.RepoFiles,
				Tools:        f.Tools,
				Timestamp:    f.FirstSeen,
				Score:        score,
				ReviewStatus: f.ReviewStatus,
				OriginRole:   f.OriginRole,
				Quoted:       f.Quoted,
			})
		}
	}

	// 2. Raw messages and git commits (primary evidence).
	messages, err := st.SearchMessagesFTS(effectiveQuery, store.SearchMessagesOptions{
		Project: project,
		Tool:    opts.Tool,
		Role:    opts.Role,
		Since:   opts.Since,
		Limit:   limit,
	})
	if err != nil {
		warnings = append(warnings, fmt.Sprintf("transcript tier failed: %v", err))
	} else {
		for _, m := range messages {
			itemType := OmniTypeMessage
			if m.Role == "commit" {
				itemType = OmniTypeCommit
			}
			score := tierPrimary + matchCount(m.Content, terms)
			if strings.EqualFold(m.Role, "user") || strings.EqualFold(m.Role, "commit") || strings.EqualFold(m.Role, "tool") {
				score += 50
			}
			if target != nil {
				if tDoc, ok := store.ParseDocTimestamp(m.TS); ok {
					boost := store.CalculateTemporalBoost(tDoc, target)
					score += int(math.Round(boost * 400.0))
				}
			}
			items = append(items, OmniResultItem{
				Type:      itemType,
				ID:        fmt.Sprintf("msg-%d", m.ID),
				ProjectID: m.ProjectID,
				Content:   m.Content,
				Tools:     m.SourceTools,
				Timestamp: m.TS,
				Score:     score,
			})
		}
	}

	// 3. Attached reference documentation.
	docs, err := st.SearchDocsFTS(effectiveQuery, project, limit)
	if err != nil {
		warnings = append(warnings, fmt.Sprintf("docs tier failed: %v", err))
	} else {
		for _, d := range docs {
			score := tierDoc + matchCount(d.Title+" "+d.Content, terms)
			if target != nil {
				if tDoc, ok := store.ParseDocTimestamp(d.CreatedAt); ok {
					boost := store.CalculateTemporalBoost(tDoc, target)
					score += int(math.Round(boost * 400.0))
				}
			}
			items = append(items, OmniResultItem{
				Type:      OmniTypeDoc,
				ID:        d.ID,
				ProjectID: d.ProjectID,
				Title:     d.Title,
				Content:   d.Content,
				Timestamp: d.CreatedAt,
				Score:     score,
			})
		}
	}

	// 4. Session handoffs: recorded intent and open work, previously invisible
	// to omni even though a handoff is exactly what "what was I doing" needs.
	handoffs, err := st.SearchHandoffs(effectiveQuery, project, limit)
	if err != nil {
		warnings = append(warnings, fmt.Sprintf("handoff tier failed: %v", err))
	} else {
		for _, h := range handoffs {
			if opts.Tool != "" && !strings.EqualFold(h.Agent, opts.Tool) {
				continue
			}
			if opts.Since != "" && h.CreatedAt != "" && h.CreatedAt < opts.Since {
				continue
			}
			title := h.Summary
			content := h.Summary
			if len(h.OpenItems) > 0 {
				content += "\nOpen: " + strings.Join(h.OpenItems, "; ")
			}
			if len(h.Decisions) > 0 {
				content += "\nDecisions: " + strings.Join(h.Decisions, "; ")
			}
			score := tierPrimary + matchCount(content, terms)
			if target != nil {
				if tDoc, ok := store.ParseDocTimestamp(h.CreatedAt); ok {
					boost := store.CalculateTemporalBoost(tDoc, target)
					score += int(math.Round(boost * 400.0))
				}
			}
			items = append(items, OmniResultItem{
				Type:      OmniTypeHandoff,
				ID:        h.ID,
				ProjectID: h.ProjectID,
				Title:     title,
				Content:   content,
				Tools:     []string{h.Agent},
				Timestamp: h.CreatedAt,
				Score:     score,
			})
		}
	}

	if opts.Role != "" {
		warnings = append(warnings, "role filter applies to the transcript tier only; claims, docs, and handoffs carry no role")
	}

	// Establish deterministic local ordering across all gathered items.
	// Primary sort key is immutable authority tier; secondary key is local
	// lexical score, then frecency within equal scores.
	for i := range items {
		items[i].Frecency = frecencyScore(items[i].Timestamp)
	}
	sortOmniItems(items)

	semanticReranked := false
	if opts.Semantic && len(items) > 0 {
		provider := assist.ActiveProviderFor("search", opts.Project)
		if provider == nil {
			warnings = append(warnings, "semantic assistance not consented or provider unavailable; preserved deterministic local ranking")
		} else if len(items) > 1 {
			// Sample bounded candidates from the established local ranking (up to 25)
			candCount := min(len(items), 25)
			var candidates []assist.RerankCandidate
			for i := 0; i < candCount; i++ {
				candidates = append(candidates, assist.RerankCandidate{
					Key:     items[i].ID,
					Excerpt: fmt.Sprintf("[%s] %s", items[i].Type, items[i].Content),
				})
			}

			ctx, cancel := context.WithTimeout(context.Background(), 2000*time.Millisecond)
			res, err := assist.Rerank(ctx, provider, query, candidates)
			cancel()

			if err != nil {
				warnings = append(warnings, fmt.Sprintf("semantic reranking unavailable (%v); preserved deterministic local ranking", err))
			} else if res == nil || len(res.Scores) < len(candidates) || len(res.Scores) == 0 {
				warnings = append(warnings, "semantic provider returned no valid candidate scores; preserved deterministic local ranking")
			} else {
				semanticReranked = true
				for i := range items {
					if score, ok := res.Scores[items[i].ID]; ok {
						if score < 0 {
							score = 0
						}
						if score > 1 {
							score = 1
						}
						// Apply score only within the item's tier (max 500 points)
						items[i].Score += int(math.Round(score * 500))
					}
				}
				if res.HasAnswerExists && res.AnswerExists < 0.20 {
					warnings = append(warnings, "semantic evaluation indicates no candidate strongly addresses this query")
				}
				// Re-sort with semantic updates: immutable authority tier remains primary
				sortOmniItems(items)
			}
		}
	}

	matched := len(items)
	if len(items) > limit {
		items = items[:limit]
	}

	return &OmniSearchResult{
		Query:            query,
		Project:          project,
		TotalHits:        len(items), // legacy field: kept as the returned count
		Returned:         len(items),
		Matched:          matched,
		Items:            items,
		SemanticReranked: semanticReranked,
		Warnings:         warnings,
	}, nil
}

// factItemType reports what a claim actually is: an approved invariant, an
// approved claim, or an unapproved candidate.
func factItemType(f store.Fact) OmniItemType {
	if f.IsCandidate() {
		return OmniTypeCandidate
	}
	if f.IsInvariant() {
		return OmniTypeInvariant
	}
	return OmniTypeFact
}

// factScore places a claim in its tier and orders within the tier by how many
// query terms matched.
func factScore(f store.Fact, terms []string) int {
	tier := tierFact
	switch factItemType(f) {
	case OmniTypeInvariant:
		tier = tierInvariant
	case OmniTypeCandidate:
		tier = tierCandidate
	}
	return tier + MatchScore(f, terms)
}

// matchCount counts how many query terms appear in the text, case-insensitively.
func matchCount(text string, terms []string) int {
	lower := strings.ToLower(text)
	n := 0
	for _, t := range terms {
		if strings.Contains(lower, t) {
			n++
		}
	}
	return n
}

func containsFold(haystack []string, needle string) bool {
	for _, h := range haystack {
		if strings.EqualFold(h, needle) {
			return true
		}
	}
	return false
}

// AuthorityTier returns the integer priority of the item's evidence tier.
// Higher tiers are fundamentally more authoritative and cannot be outranked
// by lower tiers regardless of semantic bonus points.
func (it OmniResultItem) AuthorityTier() int {
	switch it.Type {
	case OmniTypeInvariant:
		return tierInvariant
	case OmniTypeFact:
		return tierFact
	case OmniTypeMessage, OmniTypeCommit, OmniTypeHandoff:
		return tierPrimary
	case OmniTypeDoc:
		return tierDoc
	case OmniTypeCandidate:
		return tierCandidate
	default:
		return 0
	}
}

// sortOmniItems orders results strictly by immutable authority tier first,
// then by internal score within the tier, and finally by stable tie-breakers.
// frecencyScore is a bounded recency-decay tiebreak (0-10) used inside an
// authority tier: half-life is 14 days. Touch counts are not available in the
// omni gather, so recency carries the frecency signal for now.
func frecencyScore(ts string) int {
	if ts == "" {
		return 0
	}
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return 0
	}
	age := max(time.Since(t), 0)
	score := 10.0 * math.Pow(0.5, age.Hours()/(14*24))
	return int(math.Round(score))
}

func sortOmniItems(items []OmniResultItem) {
	sort.SliceStable(items, func(i, j int) bool {
		tierI := items[i].AuthorityTier()
		tierJ := items[j].AuthorityTier()
		if tierI != tierJ {
			return tierI > tierJ
		}
		if items[i].Score != items[j].Score {
			return items[i].Score > items[j].Score
		}
		if items[i].Frecency != items[j].Frecency {
			return items[i].Frecency > items[j].Frecency
		}
		if items[i].Timestamp != items[j].Timestamp {
			return items[i].Timestamp > items[j].Timestamp
		}
		return items[i].ID < items[j].ID
	})
}
