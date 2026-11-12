package dream

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/assist"
	"github.com/jadmadi/sila/internal/store"
)

// RecallHit is one prior resolution found across projects.
type RecallHit struct {
	Kind        string  `json:"kind"` // lesson | fact | transcript
	ID          string  `json:"id"`
	MemoryID    string  `json:"memory_id,omitempty"`
	ProjectID   string  `json:"project_id,omitempty"`
	Tool        string  `json:"tool,omitempty"`
	Role        string  `json:"role,omitempty"`
	Timestamp   string  `json:"timestamp,omitempty"`
	Symptom     string  `json:"symptom,omitempty"`
	Cause       string  `json:"cause,omitempty"`
	Fix         string  `json:"fix,omitempty"`
	Resolution  string  `json:"resolution"`
	Grounded    bool    `json:"grounded"`
	Score       float64 `json:"score"`
	RetrievalID string  `json:"retrieval_id,omitempty"`
	Outcome     string  `json:"outcome,omitempty"`
	SourceRef   string  `json:"source_ref,omitempty"`

	// Local scoring inputs. They are not part of the JSON contract: emitting
	// them would echo stored path lists that recall never rendered before.
	RepoFiles []string `json:"-"`
	Branch    string   `json:"-"`
	Head      string   `json:"-"`
}

// RecallResult is the full answer to a recall query.
type RecallResult struct {
	Query            string      `json:"query"`
	Project          string      `json:"project,omitempty"`
	Total            int         `json:"total"`
	Hits             []RecallHit `json:"hits"`
	Lessons          []RecallHit `json:"lessons,omitempty"`
	Cases            []RecallHit `json:"cases,omitempty"`
	Warnings         []string    `json:"warnings,omitempty"`
	SemanticReranked bool        `json:"semantic_reranked,omitempty"`

	// Additive pack fields (goal D-011). Hits keep their existing shape and
	// order when no context is supplied.
	Pack           *RecallPack           `json:"pack,omitempty"`
	Context        *RecallContextSummary `json:"context,omitempty"`
	EmptyReason    string                `json:"empty_reason,omitempty"`
	OmittedCount   int                   `json:"omitted_count,omitempty"`
	BudgetTokens   int                   `json:"budget_tokens,omitempty"`
	RankingVersion string                `json:"ranking_version,omitempty"`
}

// RecallOptions scopes a recall query.
type RecallOptions struct {
	Project         string
	ProjectDir      string
	Limit           int
	Semantic        bool
	Context         *RecallContext
	BudgetTokens    int
	IncludeArchived bool
	Trace           *RecallTrace

	// ACLScoped limits cross-project hits to the requester's own project plus
	// records explicitly marked global. It is a no-op without a RequesterProject.
	ACLScoped        bool
	RequesterProject string

	// Retrieval identity and feedback scoring. Agent and Session label the
	// recorded retrieval; SkipRetrieval lets a caller that re-packs hits
	// (the CLI symbol tier) record the final pack instead.
	Agent         string
	Session       string
	SkipRetrieval bool
	Now           time.Time
}

// Tier bases keep deliberate records above claims and transcripts, and a
// project boost reorders within a tier without crossing it.
const (
	recallLessonBase     = 1000.0
	recallFactBase       = 500.0
	recallTranscriptBase = 100.0
	recallProjectBoost   = 200.0
)

// RecallSolutions finds prior resolutions for a query in tier order: deliberate
// lessons first, then grounded facts and memos, then transcript hits. The
// current project's records rank first within their tier. Local by default;
// semantic reranking only runs when consented and requested.
func RecallSolutions(st *store.Store, query string, opts RecallOptions) (*RecallResult, error) {
	if opts.Limit <= 0 {
		opts.Limit = 5
	}
	if opts.Limit > RecallPackMaxItems {
		opts.Limit = RecallPackMaxItems
	}
	now := opts.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}

	var ctx *RecallContext
	var ctxDiags []PathDiagnostic
	if opts.Context != nil {
		normalized, diags, err := ValidateRecallContext(*opts.Context, opts.ProjectDir)
		if err != nil {
			return nil, err
		}
		ctx = &normalized
		ctxDiags = diags
	}

	budgetTokens := EffectiveRecallBudget(opts.BudgetTokens)
	packLimit := opts.Limit
	if packLimit > RecallPackDefaultItems {
		packLimit = RecallPackDefaultItems
	}

	temporalTarget, cleanQuery := store.ParseTemporalAnchor(query, time.Now().UTC())
	searchQuery := query
	if temporalTarget != nil && strings.TrimSpace(cleanQuery) != "" {
		searchQuery = cleanQuery
	}
	terms := strings.Fields(searchQuery)
	res := &RecallResult{Query: query, Project: opts.Project}
	if len(terms) == 0 {
		pack := BuildRecallPack(nil, budgetTokens, packLimit)
		if ctx != nil {
			summary := SummarizeRecallContext(*ctx, ctxDiags)
			pack.Context = &summary
			res.Context = &summary
		}
		if !opts.SkipRetrieval {
			if err := RecordPackRetrieval(st, &pack, RecallRetrievalOptions{
				Project: opts.Project, Agent: opts.Agent, Session: opts.Session, Query: query, Now: now,
			}); err != nil {
				res.Warnings = append(res.Warnings, "retrieval ledger unavailable ("+err.Error()+"); ranking unchanged")
			}
		}
		res.Pack = &pack
		res.EmptyReason = pack.EmptyReason
		res.BudgetTokens = budgetTokens
		res.RankingVersion = RankingVersion
		return res, nil
	}

	seen := map[string]bool{}
	excluded := map[string]bool{}
	if !opts.IncludeArchived {
		if blocked, blockErr := st.NonCurrentMemoryFactIDs(); blockErr == nil {
			excluded = blocked
		}
	}
	projectMatch := func(projectID string) bool {
		return opts.Project != "" && strings.EqualFold(projectID, opts.Project)
	}
	boost := func(projectID string, score float64) float64 {
		if projectMatch(projectID) {
			return score + recallProjectBoost
		}
		return score
	}
	firstTool := func(tools []string) string {
		if len(tools) == 0 {
			return ""
		}
		return tools[0]
	}

	// Tier 1: deliberate solution lessons, cross-project by default.
	if lessons, err := st.LessonFacts("", 200); err == nil {
		lessons = store.FilterNonCurrentFacts(lessons, excluded)
		for _, l := range lessons {
			if seen[l.ID] {
				continue
			}
			if !aclAllows(opts.ACLScoped, opts.RequesterProject, l.ProjectID, l.Visibility) {
				continue
			}
			score := MatchScore(l, terms)
			projectBoost := 0.0
			if projectMatch(l.ProjectID) {
				projectBoost = recallProjectBoost
			}
			if score <= 0 {
				opts.Trace.Add(RecallTraceEntry{Stage: "lesson", ID: l.ID, Kind: "lesson", Base: recallLessonBase, Match: score, ProjectBoost: projectBoost, Decision: "below-threshold", Reason: "no term overlap"})
				continue
			}
			seen[l.ID] = true
			opts.Trace.Add(RecallTraceEntry{Stage: "lesson", ID: l.ID, Kind: "lesson", Base: recallLessonBase, Match: score, ProjectBoost: projectBoost, FinalScore: recallLessonBase + float64(score) + projectBoost, Decision: "kept"})
			symptom, cause, fix, _ := store.ParseLesson(l.Claim)
			resolution := fix
			if resolution == "" {
				resolution = symptom
			}
			res.Hits = append(res.Hits, RecallHit{
				Kind:       "lesson",
				ID:         l.ID,
				ProjectID:  l.ProjectID,
				Tool:       firstTool(l.Tools),
				Timestamp:  l.FirstSeen,
				Symptom:    symptom,
				Cause:      cause,
				Fix:        fix,
				Resolution: resolution,
				Grounded:   l.FileRefsExist || l.Verified,
				Score:      boost(l.ProjectID, recallLessonBase+float64(score)),
				SourceRef:  "sila lesson show " + l.ID,
				RepoFiles:  l.RepoFiles,
			})
		}
	}

	// Tier 1b: deliberate architectural procedural cases, cross-project by default.
	if cases, err := st.CaseFacts("", 200); err == nil {
		cases = store.FilterNonCurrentFacts(cases, excluded)
		for _, c := range cases {
			if seen[c.ID] {
				continue
			}
			if !aclAllows(opts.ACLScoped, opts.RequesterProject, c.ProjectID, c.Visibility) {
				continue
			}
			score := MatchScore(c, terms)
			projectBoost := 0.0
			if projectMatch(c.ProjectID) {
				projectBoost = recallProjectBoost
			}
			if score <= 0 {
				opts.Trace.Add(RecallTraceEntry{Stage: "case", ID: c.ID, Kind: "case", Base: recallLessonBase, Match: score, ProjectBoost: projectBoost, Decision: "below-threshold", Reason: "no term overlap"})
				continue
			}
			seen[c.ID] = true
			opts.Trace.Add(RecallTraceEntry{Stage: "case", ID: c.ID, Kind: "case", Base: recallLessonBase, Match: score, ProjectBoost: projectBoost, FinalScore: recallLessonBase + float64(score) + projectBoost, Decision: "kept"})
			intent, approach, invariant, _ := store.ParseCase(c.Claim)
			resolution := approach
			if resolution == "" {
				resolution = intent
			}
			res.Hits = append(res.Hits, RecallHit{
				Kind:       "case",
				ID:         c.ID,
				ProjectID:  c.ProjectID,
				Tool:       firstTool(c.Tools),
				Timestamp:  c.FirstSeen,
				Symptom:    intent,
				Cause:      approach,
				Fix:        invariant,
				Resolution: resolution,
				Grounded:   c.FileRefsExist || c.Verified,
				Score:      boost(c.ProjectID, recallLessonBase+float64(score)),
				SourceRef:  "sila case show " + c.ID,
				RepoFiles:  c.RepoFiles,
			})
		}
	}

	// Tier 2: grounded facts, memos, and distilled claims.
	if facts, err := st.SearchFactsFTS(query, opts.Project, false, 25); err == nil {
		facts = store.FilterNonCurrentFacts(facts, excluded)
		for _, f := range facts {
			if seen[f.ID] {
				continue
			}
			if !aclAllows(opts.ACLScoped, opts.RequesterProject, f.ProjectID, f.Visibility) {
				continue
			}
			seen[f.ID] = true
			text := f.Distilled
			if text == "" {
				text = f.Claim
			}
			match := MatchScore(f, terms)
			projectBoost := 0.0
			if projectMatch(f.ProjectID) {
				projectBoost = recallProjectBoost
			}
			opts.Trace.Add(RecallTraceEntry{Stage: "fact", ID: f.ID, Kind: "fact", Base: recallFactBase, Match: match, ProjectBoost: projectBoost, FinalScore: recallFactBase + float64(match) + projectBoost, Decision: "kept"})
			res.Hits = append(res.Hits, RecallHit{
				Kind:       "fact",
				ID:         f.ID,
				ProjectID:  f.ProjectID,
				Tool:       firstTool(f.Tools),
				Timestamp:  f.FirstSeen,
				Resolution: truncateRecall(text, 400),
				Grounded:   f.FileRefsExist || f.Verified,
				Score:      recallFactBase + float64(match) + projectBoost,
				SourceRef:  "sila search expand " + f.ID,
				RepoFiles:  f.RepoFiles,
			})
		}
	}

	// Tier 3: raw transcript hits, preferring assistant turns over questions.
	if msgs, err := st.SearchMessagesFTS(query, store.SearchMessagesOptions{Limit: 25}); err == nil {
		for _, m := range msgs {
			if seen[fmt.Sprint(m.ID)] {
				continue
			}
			if !aclAllows(opts.ACLScoped, opts.RequesterProject, m.ProjectID, "") {
				continue
			}
			seen[fmt.Sprint(m.ID)] = true
			overlap := recallTermOverlap(m.Content, terms)
			score := recallTranscriptBase + float64(overlap*5)
			if m.Role == "assistant" {
				score += 10
			}
			projectBoost := 0.0
			if projectMatch(m.ProjectID) {
				projectBoost = recallProjectBoost
			}
			weight := OutcomeWeight(m.Outcome)
			score *= weight
			opts.Trace.Add(RecallTraceEntry{Stage: "transcript", ID: fmt.Sprint(m.ID), Kind: "transcript", Base: recallTranscriptBase, Match: overlap, ProjectBoost: projectBoost, FinalScore: score + projectBoost, Decision: "kept"})
			res.Hits = append(res.Hits, RecallHit{
				Kind:       "transcript",
				ID:         fmt.Sprint(m.ID),
				ProjectID:  m.ProjectID,
				Tool:       firstTool(m.SourceTools),
				Role:       m.Role,
				Timestamp:  m.TS,
				Resolution: truncateRecall(m.Content, 400),
				Score:      score + projectBoost,
				SourceRef:  "sila search expand " + fmt.Sprint(m.ID),
				Outcome:    m.Outcome,
			})
		}
	}

	if temporalTarget != nil {
		for i := range res.Hits {
			if tDoc, ok := store.ParseDocTimestamp(res.Hits[i].Timestamp); ok {
				boost := store.CalculateTemporalBoost(tDoc, temporalTarget)
				res.Hits[i].Score = store.ApplyTemporalBoostHigherBetter(res.Hits[i].Score, boost)
			}
		}
	}

	if ctx != nil {
		for i := range res.Hits {
			res.Hits[i] = ApplyRecallContext(res.Hits[i], *ctx, opts.ProjectDir)
		}
	}

	// Feedback is a bounded ranking input applied after the context boost and
	// before semantic rerank. A read failure degrades to zero adjustment.
	if st != nil {
		withIDs, idErr := attachRecallMemoryIDs(st, res.Hits)
		if idErr != nil {
			res.Warnings = append(res.Warnings, "memory identity lookup unavailable ("+idErr.Error()+"); feedback cannot bind this pack")
		} else {
			adjusted, fbErr := applyRecallFeedback(st, withIDs, now)
			if fbErr != nil {
				res.Warnings = append(res.Warnings, "feedback adjustment unavailable ("+fbErr.Error()+"); preserved deterministic local ranking")
			} else {
				res.Hits = adjusted
			}
		}
	}

	sortRecallHits(res.Hits)

	if opts.Semantic && len(res.Hits) > 1 {
		provider := assist.ActiveProviderFor("search", opts.Project)
		if provider == nil {
			res.Warnings = append(res.Warnings, "semantic assistance not consented or provider unavailable; preserved deterministic local ranking")
		} else {
			candCount := min(len(res.Hits), 25)
			candidates := make([]assist.RerankCandidate, 0, candCount)
			for i := 0; i < candCount; i++ {
				candidates = append(candidates, assist.RerankCandidate{
					Key:     res.Hits[i].ID,
					Excerpt: fmt.Sprintf("[%s] %s", res.Hits[i].Kind, res.Hits[i].Resolution),
				})
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2000*time.Millisecond)
			rerank, err := assist.Rerank(ctx, provider, query, candidates)
			cancel()
			if err != nil {
				res.Warnings = append(res.Warnings, fmt.Sprintf("semantic reranking unavailable (%v); preserved deterministic local ranking", err))
			} else if rerank == nil || len(rerank.Scores) < len(candidates) || len(rerank.Scores) == 0 {
				res.Warnings = append(res.Warnings, "semantic provider returned no valid candidate scores; preserved deterministic local ranking")
			} else {
				res.SemanticReranked = true
				for i := range res.Hits {
					if score, ok := rerank.Scores[res.Hits[i].ID]; ok {
						if score < 0 {
							score = 0
						}
						if score > 1 {
							score = 1
						}
						// Bounded so a semantic score orders within a tier, never across it.
						res.Hits[i].Score += float64(score) * 80
						if ctx != nil {
							if base, ok := recallTierBase(res.Hits[i].Kind); ok {
								if ceiling := base + recallTierSpan; res.Hits[i].Score > ceiling {
									res.Hits[i].Score = ceiling
								}
							}
						}
					}
				}
				sortRecallHits(res.Hits)
			}
		}
	}

	if opts.Context != nil {
		if blended, changed := ApplyHybridGraphBlend(res.Hits, query, opts.Context, opts.ProjectDir); changed {
			res.Hits = blended
			sortRecallHits(res.Hits)
		}
	}
	res.Total = len(res.Hits)
	pack := BuildRecallPack(res.Hits, budgetTokens, packLimit)
	if ctx != nil {
		summary := SummarizeRecallContext(*ctx, ctxDiags)
		pack.Context = &summary
		res.Context = &summary
	}
	if !opts.SkipRetrieval {
		if err := RecordPackRetrieval(st, &pack, RecallRetrievalOptions{
			Project: opts.Project, Agent: opts.Agent, Session: opts.Session, Query: query, Now: now,
		}); err != nil {
			res.Warnings = append(res.Warnings, "retrieval ledger unavailable ("+err.Error()+"); ranking unchanged")
		}
	}
	res.Pack = &pack
	res.EmptyReason = pack.EmptyReason
	res.OmittedCount = pack.Omitted
	res.BudgetTokens = budgetTokens
	res.RankingVersion = RankingVersion
	if len(res.Hits) > opts.Limit {
		res.Hits = res.Hits[:opts.Limit]
	}
	FinalizeTrace(opts.Trace, res.Hits, opts.Limit)
	for _, h := range res.Hits {
		if h.Kind == "lesson" {
			res.Lessons = append(res.Lessons, h)
		} else if h.Kind == "case" {
			res.Cases = append(res.Cases, h)
		}
	}
	return res, nil
}

// sortRecallHits orders by score, then newest, then id for determinism.
func sortRecallHits(hits []RecallHit) {
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		if hits[i].Timestamp != hits[j].Timestamp {
			return hits[i].Timestamp > hits[j].Timestamp
		}
		return hits[i].ID < hits[j].ID
	})
}

// recallTermOverlap counts query terms present in a text.
func recallTermOverlap(text string, terms []string) int {
	lower := strings.ToLower(text)
	n := 0
	for _, t := range terms {
		if t != "" && strings.Contains(lower, strings.ToLower(t)) {
			n++
		}
	}
	return n
}

// truncateRecall collapses whitespace and cuts long text for display.
func truncateRecall(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= max {
		return s
	}
	cut := s[:max]
	if idx := strings.LastIndex(cut, " "); idx > max/2 {
		cut = cut[:idx]
	}
	return cut + "…"
}
