package dream

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jadmadi/sila/internal/store"
)

// RankingVersion identifies the context-aware recall ranking contract.
const RankingVersion = "recall-pack/v1"

// Context validation limits (goal D-001).
const (
	RecallContextMaxTaskChars   = 200
	RecallContextMaxPaths       = 32
	RecallContextMaxSymbols     = 16
	RecallContextMaxBranchChars = 255
)

// Pack budget defaults and bounds (goal D-005).
const (
	RecallPackDefaultItems  = 8
	RecallPackDefaultTokens = 1200
	RecallPackMinTokens     = 256
	RecallPackMaxTokens     = 8192
	RecallPackMaxItems      = 50
)

// Empty-pack reasons (goal D-007).
const (
	RecallEmptyNoCandidates      = "no_candidates"
	RecallEmptyAllBelowThreshold = "all_below_threshold"
	RecallEmptyBudgetExhausted   = "budget_exhausted"
)

// Bounded context boosts (goal D-004). The total cap stays below the 500-point
// gap between authority tiers.
const (
	recallPathBoost      = 100.0
	recallPathBoostMax   = 300.0
	recallTaskTermBoost  = 20.0
	recallTaskTermMax    = 60.0
	recallBranchBoost    = 25.0
	recallHeadBoost      = 15.0
	recallSymbolBoost    = 20.0
	recallSymbolBoostMax = 40.0
	recallContextCap     = 300.0
	recallTierSpan       = 499.0
)

var recallHeadRE = regexp.MustCompile(`^[0-9a-f]{7,64}$`)

// RecallContextError reports an invalid context shape with a stable code so
// CLI and MCP surfaces can return a one-line diagnostic.
type RecallContextError struct {
	Code    string
	Message string
}

func (e *RecallContextError) Error() string { return e.Message }

// recallTaskStopwords drops grammar-only words so the boost reflects task
// substance rather than sentence glue.
var recallTaskStopwords = map[string]bool{
	"a": true, "an": true, "and": true, "are": true, "as": true, "at": true,
	"be": true, "by": true, "for": true, "from": true, "in": true, "into": true,
	"is": true, "it": true, "of": true, "on": true, "or": true, "that": true,
	"the": true, "this": true, "to": true, "was": true, "were": true,
	"will": true, "with": true,
}

// ValidateRecallContext normalizes a context and rejects shapes that cannot be
// ranked safely. Rejected paths are reported by index, never by text.
func ValidateRecallContext(ctx RecallContext, projectDir string) (RecallContext, []PathDiagnostic, error) {
	out := RecallContext{
		Task:   strings.TrimSpace(ctx.Task),
		Branch: strings.TrimSpace(ctx.Branch),
		Head:   strings.ToLower(strings.TrimSpace(ctx.Head)),
	}
	if utf8.RuneCountInString(out.Task) > RecallContextMaxTaskChars {
		return RecallContext{}, nil, &RecallContextError{
			Code:    "task_too_long",
			Message: fmt.Sprintf("context task exceeds %d characters", RecallContextMaxTaskChars),
		}
	}
	if len(out.Branch) > RecallContextMaxBranchChars {
		return RecallContext{}, nil, &RecallContextError{
			Code:    "branch_too_long",
			Message: fmt.Sprintf("context branch exceeds %d characters", RecallContextMaxBranchChars),
		}
	}
	if out.Head != "" && !recallHeadRE.MatchString(out.Head) {
		return RecallContext{}, nil, &RecallContextError{
			Code:    "invalid_head",
			Message: "context head must be 7-64 hex characters",
		}
	}

	seenSymbols := make(map[string]bool, len(ctx.Symbols))
	for _, raw := range ctx.Symbols {
		sym := strings.TrimSpace(raw)
		if sym == "" {
			continue
		}
		key := strings.ToLower(sym)
		if seenSymbols[key] {
			continue
		}
		seenSymbols[key] = true
		out.Symbols = append(out.Symbols, sym)
	}
	if len(out.Symbols) > RecallContextMaxSymbols {
		return RecallContext{}, nil, &RecallContextError{
			Code:    "too_many_symbols",
			Message: fmt.Sprintf("context symbols exceed %d entries", RecallContextMaxSymbols),
		}
	}

	paths, diags := normalizeRecallPaths(ctx.Paths, projectDir)
	out.Paths = paths
	return out, diags, nil
}

// normalizeRecallPaths makes separators portable, strips a leading "./",
// rejects absolute, home and traversing paths, rejects symlink escapes,
// deduplicates while preserving order, and caps the list at 32 entries.
func normalizeRecallPaths(paths []string, projectDir string) ([]string, []PathDiagnostic) {
	out := make([]string, 0, len(paths))
	seen := make(map[string]bool, len(paths))
	var diags []PathDiagnostic
	capped := false

	for i, raw := range paths {
		if len(out) >= RecallContextMaxPaths {
			if !capped {
				diags = append(diags, PathDiagnostic{
					Index:  i,
					Code:   "too_many_paths",
					Reason: fmt.Sprintf("path list capped at %d entries", RecallContextMaxPaths),
				})
				capped = true
			}
			continue
		}
		p := strings.TrimSpace(raw)
		if p == "" {
			diags = append(diags, PathDiagnostic{Index: i, Code: "empty_path"})
			continue
		}
		p = strings.ReplaceAll(p, `\`, "/")
		for strings.Contains(p, "//") {
			p = strings.ReplaceAll(p, "//", "/")
		}
		for strings.HasPrefix(p, "./") {
			p = strings.TrimPrefix(p, "./")
		}
		p = collapseDotSegments(p)
		if p == "" || p == "." {
			diags = append(diags, PathDiagnostic{Index: i, Code: "empty_path"})
			continue
		}
		if strings.HasPrefix(p, "~") {
			diags = append(diags, PathDiagnostic{Index: i, Code: "home_path"})
			continue
		}
		if recallPathIsAbsolute(p) {
			diags = append(diags, PathDiagnostic{Index: i, Code: "absolute_path"})
			continue
		}
		if recallPathTraverses(p) {
			diags = append(diags, PathDiagnostic{Index: i, Code: "traversal"})
			continue
		}
		if recallPathEscapesRoot(projectDir, p) {
			diags = append(diags, PathDiagnostic{Index: i, Code: "symlink_escape"})
			continue
		}
		key := p
		if runtime.GOOS == "windows" {
			key = strings.ToLower(p)
		}
		if seen[key] {
			diags = append(diags, PathDiagnostic{Index: i, Code: "duplicate_path"})
			continue
		}
		seen[key] = true
		out = append(out, p)
	}
	return out, diags
}

func collapseDotSegments(p string) string {
	if !strings.Contains(p, "/./") && !strings.HasSuffix(p, "/.") {
		return p
	}
	parts := strings.Split(p, "/")
	kept := parts[:0]
	for _, part := range parts {
		if part == "." {
			continue
		}
		kept = append(kept, part)
	}
	return strings.Join(kept, "/")
}

func recallPathIsAbsolute(p string) bool {
	if strings.HasPrefix(p, "/") {
		return true
	}
	// Windows drive and UNC spellings after separator normalization.
	if len(p) >= 2 && p[1] == ':' {
		return true
	}
	return strings.HasPrefix(p, "//")
}

func recallPathTraverses(p string) bool {
	for _, part := range strings.Split(p, "/") {
		if part == ".." {
			return true
		}
	}
	return false
}

// recallPathEscapesRoot resolves symlinks and rejects a target outside the
// project root. A path that does not exist cannot disclose content, so it is
// left to textual traversal checks.
func recallPathEscapesRoot(projectDir, rel string) bool {
	if strings.TrimSpace(projectDir) == "" {
		return false
	}
	root, err := filepath.Abs(filepath.Clean(projectDir))
	if err != nil {
		return false
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		realRoot = root
	}
	target := filepath.Join(realRoot, filepath.FromSlash(rel))
	realTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		return false
	}
	relToRoot, err := filepath.Rel(realRoot, realTarget)
	if err != nil {
		return true
	}
	return relToRoot == ".." || strings.HasPrefix(relToRoot, ".."+string(filepath.Separator)) || filepath.IsAbs(relToRoot)
}

// ContextBoost scores how strongly a hit matches the caller's task context.
// It is additive and capped at +300 so it can never cross an authority tier.
func ContextBoost(hit RecallHit, ctx RecallContext, projectDir string) float64 {
	boost := 0.0

	pathMatches := 0
	for _, wanted := range ctx.Paths {
		for _, have := range hit.RepoFiles {
			if recallPathIntersects(wanted, have) {
				pathMatches++
				break
			}
		}
		if pathMatches >= 3 {
			break
		}
	}
	boost += float64(pathMatches) * recallPathBoost

	if strings.TrimSpace(ctx.Task) != "" {
		terms := recallTaskTerms(ctx.Task)
		text := strings.ToLower(strings.Join([]string{hit.Resolution, hit.Symptom, hit.Cause, hit.Fix}, " "))
		matched := 0
		for _, term := range terms {
			if strings.Contains(text, term) {
				matched++
				if matched >= 3 {
					break
				}
			}
		}
		boost += float64(matched) * recallTaskTermBoost
	}

	// Provenance is only ever read from the hit. An absent branch or head
	// never earns a boost, so the ranking cannot invent git context.
	if ctx.Branch != "" && hit.Branch != "" && strings.EqualFold(strings.TrimSpace(hit.Branch), ctx.Branch) {
		boost += recallBranchBoost
	}
	if ctx.Head != "" && hit.Head != "" {
		have := strings.ToLower(strings.TrimSpace(hit.Head))
		want := strings.ToLower(strings.TrimSpace(ctx.Head))
		if have != "" && want != "" && (strings.HasPrefix(have, want) || strings.HasPrefix(want, have)) {
			boost += recallHeadBoost
		}
	}

	if len(ctx.Symbols) > 0 {
		text := strings.ToLower(hit.SourceRef + " " + hit.Resolution)
		matched := 0
		for _, sym := range ctx.Symbols {
			if sym != "" && strings.Contains(text, strings.ToLower(sym)) {
				matched++
				if matched >= 2 {
					break
				}
			}
		}
		boost += float64(matched) * recallSymbolBoost
	}

	return math.Min(boost, recallContextCap)
}

// recallPathIntersects reports an exact match or a slash-boundary prefix in
// either direction. A directory in play therefore intersects the files under
// it, and a file in play intersects a stored directory reference.
func recallPathIntersects(a, b string) bool {
	a = strings.TrimSuffix(strings.TrimSpace(a), "/")
	b = strings.TrimSuffix(strings.TrimSpace(b), "/")
	if a == "" || b == "" {
		return false
	}
	if runtime.GOOS == "windows" {
		return recallPathPrefixFold(a, b) || recallPathPrefixFold(b, a)
	}
	return recallPathPrefix(a, b) || recallPathPrefix(b, a)
}

func recallPathPrefix(a, b string) bool {
	return a == b || strings.HasPrefix(a, b+"/")
}

func recallPathPrefixFold(a, b string) bool {
	a = strings.ToLower(a)
	b = strings.ToLower(b)
	return a == b || strings.HasPrefix(a, b+"/")
}

// recallTaskTerms extracts up to eight substantive, deduplicated terms from a
// task description. Terms stay local to scoring and are never rendered.
func recallTaskTerms(task string) []string {
	fields := strings.FieldsFunc(strings.ToLower(task), func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '_' && r != '-'
	})
	terms := make([]string, 0, 8)
	seen := make(map[string]bool, 8)
	for _, f := range fields {
		if len(f) < 2 || recallTaskStopwords[f] || seen[f] {
			continue
		}
		seen[f] = true
		terms = append(terms, f)
		if len(terms) == 8 {
			break
		}
	}
	return terms
}

// ApplyRecallContext adds the context boost to a hit and clamps it to its own
// authority tier. The clamp runs after the boost so a boosted fact can never
// outrank an unboosted lesson.
func ApplyRecallContext(hit RecallHit, ctx RecallContext, projectDir string) RecallHit {
	boost := ContextBoost(hit, ctx, projectDir)
	if boost <= 0 {
		return hit
	}
	hit.Score += boost
	if base, ok := recallTierBase(hit.Kind); ok {
		if ceiling := base + recallTierSpan; hit.Score > ceiling {
			hit.Score = ceiling
		}
	}
	return hit
}

// recallTierBase maps a hit kind to its authority tier base.
func recallTierBase(kind string) (float64, bool) {
	switch {
	case kind == "lesson" || kind == "case":
		return recallLessonBase, true
	case kind == "fact":
		return recallFactBase, true
	case kind == "transcript":
		return recallTranscriptBase, true
	case strings.HasPrefix(kind, "symbol_"):
		// Symbol notes are a CLI-only tier above deliberate lessons.
		return 1500.0, true
	default:
		return 0, false
	}
}

// EffectiveRecallBudget clamps a requested pack budget to the documented range.
func EffectiveRecallBudget(budget int) int {
	if budget <= 0 {
		return RecallPackDefaultTokens
	}
	if budget < RecallPackMinTokens {
		return RecallPackMinTokens
	}
	if budget > RecallPackMaxTokens {
		return RecallPackMaxTokens
	}
	return budget
}

// SummarizeRecallContext projects a context for output without task text or a
// path list.
func SummarizeRecallContext(ctx RecallContext, diags []PathDiagnostic) RecallContextSummary {
	return RecallContextSummary{
		TaskPresent:     strings.TrimSpace(ctx.Task) != "",
		PathsCount:      len(ctx.Paths),
		Branch:          ctx.Branch,
		Head:            ctx.Head,
		SymbolsCount:    len(ctx.Symbols),
		PathDiagnostics: diags,
	}
}

// BuildRecallPack selects a ranked prefix of hits that fits both the item limit
// and the estimated token budget. Selection stops at the first item that does
// not fit, so truncation is deterministic.
func BuildRecallPack(hits []RecallHit, budgetTokens, limit int) RecallPack {
	budgetTokens = EffectiveRecallBudget(budgetTokens)
	if limit <= 0 {
		limit = RecallPackDefaultItems
	}
	if limit > RecallPackMaxItems {
		limit = RecallPackMaxItems
	}

	pack := RecallPack{Items: []RecallPackItem{}, RankingVersion: RankingVersion}
	candidates := make([]RecallHit, 0, len(hits))
	for _, h := range hits {
		if h.Score > 0 {
			candidates = append(candidates, h)
		}
	}
	if len(candidates) == 0 {
		if len(hits) == 0 {
			pack.EmptyReason = RecallEmptyNoCandidates
		} else {
			pack.EmptyReason = RecallEmptyAllBelowThreshold
		}
		return pack
	}

	for i, h := range candidates {
		if i >= limit {
			break
		}
		item := RecallPackItem{
			Kind:       h.Kind,
			ID:         h.ID,
			MemoryID:   h.MemoryID,
			ProjectID:  h.ProjectID,
			Resolution: truncateRecall(h.Resolution, 400),
			Score:      h.Score,
			Grounded:   h.Grounded,
			SourceRef:  h.SourceRef,
		}
		raw, _ := json.Marshal(item)
		cost := store.EstimateTokens(len(raw))
		if pack.Tokens+cost > budgetTokens {
			break
		}
		pack.Items = append(pack.Items, item)
		pack.Tokens += cost
	}
	pack.Omitted = len(candidates) - len(pack.Items)
	if len(pack.Items) == 0 {
		pack.EmptyReason = RecallEmptyBudgetExhausted
	}
	return pack
}

// BuildRecallPackForContext runs a task-scoped recall and returns only its
// bounded pack. It is the shared seam used by prime_context.
func BuildRecallPackForContext(st *store.Store, projectSel, projectDir string, ctx RecallContext, budget, limit int) (*RecallPack, error) {
	query := strings.TrimSpace(ctx.Task)
	if query == "" {
		query = strings.Join(ctx.Symbols, " ")
	}
	if query == "" {
		return nil, nil
	}
	res, err := RecallSolutions(st, query, RecallOptions{
		Project:      projectSel,
		ProjectDir:   projectDir,
		Limit:        limit,
		Context:      &ctx,
		BudgetTokens: budget,
	})
	if err != nil {
		return nil, err
	}
	return res.Pack, nil
}

// RecallRetrievalOptions labels a recorded retrieval.
type RecallRetrievalOptions struct {
	Project string
	Agent   string
	Session string
	Query   string
	Now     time.Time
}

// RecordPackRetrieval records the pack's returned typed memory IDs under one
// stable retrieval_id. Items without a memory_id (transcripts, legacy facts)
// stay in the pack but cannot receive feedback.
func RecordPackRetrieval(st *store.Store, pack *RecallPack, opts RecallRetrievalOptions) error {
	if st == nil || pack == nil {
		return nil
	}
	items := make([]store.MemoryRetrievalItem, 0, len(pack.Items))
	for i, item := range pack.Items {
		if item.MemoryID == "" {
			continue
		}
		items = append(items, store.MemoryRetrievalItem{
			MemoryID: item.MemoryID,
			Rank:     i + 1,
			Kind:     item.Kind,
			Score:    item.Score,
		})
	}
	id, err := st.RecordRetrievalAt(context.Background(), opts.Project, opts.Agent, opts.Session, opts.Query, items, opts.Now)
	if err != nil {
		return err
	}
	pack.RetrievalID = id
	return nil
}

// attachRecallMemoryIDs maps fact-backed hits onto their typed memory record.
func attachRecallMemoryIDs(st *store.Store, hits []RecallHit) ([]RecallHit, error) {
	factIDs := make([]string, 0, len(hits))
	for _, hit := range hits {
		switch hit.Kind {
		case "lesson", "case", "fact":
			factIDs = append(factIDs, hit.ID)
		}
	}
	if len(factIDs) == 0 {
		return hits, nil
	}
	mapping, err := st.MemoryIDsForFactIDs(factIDs)
	if err != nil {
		return hits, err
	}
	for i := range hits {
		if memoryID := mapping[hits[i].ID]; memoryID != "" {
			hits[i].MemoryID = memoryID
		}
	}
	return hits, nil
}

// applyRecallFeedback adds the decayed feedback adjustment and clamps each hit
// to its own authority tier, so feedback can reorder but never promote a hit
// across tiers.
func applyRecallFeedback(st *store.Store, hits []RecallHit, now time.Time) ([]RecallHit, error) {
	targets := make([]store.MemoryFeedbackTarget, 0, len(hits))
	for _, hit := range hits {
		if hit.MemoryID == "" {
			continue
		}
		targets = append(targets, store.MemoryFeedbackTarget{ProjectID: hit.ProjectID, MemoryID: hit.MemoryID})
	}
	if len(targets) == 0 {
		return hits, nil
	}
	adjustments, err := st.FeedbackAdjustmentsAt(context.Background(), targets, now)
	if err != nil {
		return hits, err
	}
	for i := range hits {
		adj, ok := adjustments[hits[i].MemoryID]
		if !ok || adj.Adjustment == 0 {
			continue
		}
		hits[i].Score += adj.Adjustment
		if base, ok := recallTierBase(hits[i].Kind); ok {
			if hits[i].Score < base {
				hits[i].Score = base
			}
			if ceiling := base + recallTierSpan; hits[i].Score > ceiling {
				hits[i].Score = ceiling
			}
		}
	}
	return hits, nil
}
