package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// MatchingProjectIDs resolves a selector without substring fallback. If sel is
// empty, nil means all projects; an unknown selector returns no IDs.
func (s *Store) MatchingProjectIDs(sel string) ([]string, error) {
	sel = strings.TrimSpace(sel)
	if sel == "" {
		return nil, nil
	}
	projects, err := s.Projects()
	if err != nil {
		return nil, err
	}
	for _, p := range projects {
		if strings.EqualFold(p.ID, sel) {
			return []string{p.ID}, nil
		}
	}
	var matched []string
	for _, p := range projects {
		if ProjectSelectorMatches(p, sel) {
			matched = append(matched, p.ID)
		}
	}
	if len(matched) > 1 {
		return nil, fmt.Errorf("project selector %q is ambiguous; use an exact ID or path", sel)
	}
	// Keep historical rows queryable when they predate the projects table, but
	// treat the selector as an exact project ID rather than broadening it.
	if len(matched) == 0 {
		return []string{sel}, nil
	}
	return matched, nil
}

// FactsByProject returns all facts matching projectSel (case-insensitive substring/path), newest first.
func (s *Store) FactsByProject(projectSel string) ([]Fact, error) {
	matching, err := s.MatchingProjectIDs(projectSel)
	if err != nil {
		return nil, err
	}
	if projectSel == "" {
		return s.FactsAll()
	}
	if len(matching) == 0 {
		return nil, nil
	}
	if len(matching) == 1 {
		return s.facts(`SELECT `+selectFactsFields+`
			FROM facts WHERE project_id=? ORDER BY first_seen DESC`, matching[0])
	}
	placeholders := strings.Repeat("?,", len(matching))
	placeholders = strings.TrimSuffix(placeholders, ",")
	args := make([]any, len(matching))
	for i, id := range matching {
		args[i] = id
	}
	return s.facts(`SELECT `+selectFactsFields+`
		FROM facts WHERE project_id IN (`+placeholders+`) ORDER BY first_seen DESC`, args...)
}

// FactsAll returns every fact, newest first.
func (s *Store) FactsAll() ([]Fact, error) {
	return s.facts(`SELECT ` + selectFactsFields + ` FROM facts ORDER BY first_seen DESC`)
}

// FactByID retrieves a single fact by its ID, or nil if not found.
func (s *Store) FactByID(id string) (*Fact, error) {
	facts, err := s.facts(`SELECT `+selectFactsFields+` FROM facts WHERE id = ? LIMIT 1`, id)
	if err != nil {
		return nil, err
	}
	if len(facts) == 0 {
		return nil, nil
	}
	return &facts[0], nil
}

// LatestMessageID returns the newest message row id for a project (0 when the
// project has no messages).
func (s *Store) LatestMessageID(projectID string) (int64, error) {
	var id int64
	err := s.DB.QueryRow(`SELECT COALESCE(MAX(id), 0) FROM messages WHERE project_id=?`, projectID).Scan(&id)
	return id, err
}

// DistillWatermark returns the newest message id already distilled for a
// project (0 when the project has never been distilled).
func (s *Store) DistillWatermark(projectID string) (int64, error) {
	var raw string
	err := s.DB.QueryRow(`SELECT value FROM state WHERE key=?`, "distill:"+projectID).Scan(&raw)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	id, perr := strconv.ParseInt(raw, 10, 64)
	if perr != nil {
		return 0, nil
	}
	return id, nil
}

// SetDistillWatermark records the newest message id distilled for a project, so
// a later sync can skip projects whose history has not changed.
func (s *Store) SetDistillWatermark(projectID string, messageID int64) error {
	if messageID <= 0 {
		return nil
	}
	_, err := s.DB.Exec(`INSERT INTO state(key, value, updated_at) VALUES(?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`,
		"distill:"+projectID, strconv.FormatInt(messageID, 10), time.Now().UTC().Format(time.RFC3339))
	return err
}

// FormatFTS5Query turns arbitrary user input into a safe, effective FTS5 query.
// It handles punctuation, special operators, and applies prefix wildcard matching.
func FormatFTS5Query(raw string) string {
	raw = strings.ReplaceAll(raw, `"`, ` `)
	raw = strings.ReplaceAll(raw, `'`, ` `)
	raw = strings.ReplaceAll(raw, `*`, ` `)
	raw = strings.ReplaceAll(raw, `:`, ` `)
	raw = strings.ReplaceAll(raw, `-`, ` `)
	raw = strings.ReplaceAll(raw, `+`, ` `)
	raw = strings.ReplaceAll(raw, `^`, ` `)
	raw = strings.ReplaceAll(raw, `(`, ` `)
	raw = strings.ReplaceAll(raw, `)`, ` `)

	tokens := strings.Fields(raw)
	if len(tokens) == 0 {
		return ""
	}

	var formatted []string
	for _, tok := range tokens {
		upper := strings.ToUpper(tok)
		if upper == "AND" || upper == "OR" || upper == "NOT" || upper == "NEAR" {
			continue
		}
		formatted = append(formatted, fmt.Sprintf(`"%s"*`, tok))
	}

	if len(formatted) == 0 {
		return ""
	}
	return strings.Join(formatted, " ")
}

// SearchFactsFTS performs full-text search against facts_fts.
// When projectSel is non-empty, only facts matching the project selector are returned.
// When refsExistOnly is true, only facts with file_refs_exist=1 are returned.
func (s *Store) SearchFactsFTS(query string, projectSel string, refsExistOnly bool, limit int) ([]Fact, error) {
	target, cleanQuery := ParseTemporalAnchor(query, time.Now().UTC())
	effectiveQuery := query
	if target != nil && strings.TrimSpace(cleanQuery) != "" {
		effectiveQuery = cleanQuery
	}

	ftsQ := FormatFTS5Query(effectiveQuery)
	if ftsQ == "" {
		if projectSel != "" {
			return s.FactsByProject(projectSel)
		}
		return s.FactsAll()
	}

	matching, err := s.MatchingProjectIDs(projectSel)
	if err != nil {
		return nil, err
	}

	sqlQ := `
		SELECT ` + selectFactsPrefixed + `
		FROM facts_fts
		JOIN facts f ON f.id = facts_fts.id
		WHERE facts_fts MATCH ?`
	args := []any{ftsQ}

	if len(matching) == 1 {
		sqlQ += ` AND f.project_id = ?`
		args = append(args, matching[0])
	} else if len(matching) > 1 {
		placeholders := strings.Repeat("?,", len(matching))
		placeholders = strings.TrimSuffix(placeholders, ",")
		sqlQ += ` AND f.project_id IN (` + placeholders + `)`
		for _, id := range matching {
			args = append(args, id)
		}
	} else if projectSel != "" {
		return nil, nil
	}

	if refsExistOnly {
		sqlQ += ` AND (f.file_refs_exist = 1 OR f.verified = 1)`
	}
	sqlQ += ` ORDER BY bm25(facts_fts), f.first_seen DESC`
	if limit > 0 {
		sqlQ += ` LIMIT ?`
		args = append(args, limit)
	}

	facts, err := s.factsFTS(sqlQ, args...)
	if err != nil {
		return nil, err
	}

	if target != nil && len(facts) > 1 {
		sort.SliceStable(facts, func(i, j int) bool {
			ti, oki := ParseDocTimestamp(facts[i].FirstSeen)
			tj, okj := ParseDocTimestamp(facts[j].FirstSeen)
			bi := CalculateTemporalBoost(ti, target)
			bj := CalculateTemporalBoost(tj, target)
			if !oki {
				bi = 0
			}
			if !okj {
				bj = 0
			}
			if math.Abs(bi-bj) > 1e-4 {
				return bi > bj
			}
			return facts[i].FirstSeen > facts[j].FirstSeen
		})
	}

	for i := range facts {
		_ = s.BumpFactAccess(context.Background(), facts[i].ID, "search")
	}
	return facts, nil
}

func (s *Store) factsFTS(query string, args ...any) ([]Fact, error) {
	executeSearch := func() ([]Fact, error) {
		ctx, cancel := context.WithTimeout(context.Background(), FTSQueryTimeout)
		defer cancel()

		out, err := s.QueryRows(ctx, query, scanFact, args...)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "interrupted") {
				return nil, fmt.Errorf("fts5 query timed out after %v: %w", FTSQueryTimeout, context.DeadlineExceeded)
			}
			return nil, err
		}
		return out, nil
	}

	out, err := executeSearch()
	if err != nil && isFTSCorruption(err) {
		if healErr := s.RebuildFTSIndex("facts_fts"); healErr == nil {
			out, err = executeSearch()
		}
	}
	return out, err
}

// ListFactsByKind returns facts of one kind with deterministic ordering.
// kind "memo" includes legacy rows whose kind is empty. projectSel "*" spans
// every registered project; since and limit are optional.
func (s *Store) ListFactsByKind(kind, projectSel string, since time.Time, limit int) ([]Fact, error) {
	var matching []string
	var err error
	if projectSel != "*" {
		matching, err = s.MatchingProjectIDs(projectSel)
		if err != nil {
			return nil, err
		}
		if projectSel != "" && len(matching) == 0 {
			return nil, nil
		}
	}
	q := `SELECT ` + selectFactsFields + ` FROM facts WHERE `
	var args []any
	switch kind {
	case "memo":
		q += `(kind='' OR kind='memo')`
	case "":
		q += `kind=''`
	default:
		q += `kind=?`
		args = append(args, kind)
	}
	if projectSel != "*" {
		if len(matching) == 1 {
			q += ` AND project_id=?`
			args = append(args, matching[0])
		} else if len(matching) > 1 {
			placeholders := strings.Repeat("?,", len(matching))
			placeholders = strings.TrimSuffix(placeholders, ",")
			q += ` AND project_id IN (` + placeholders + `)`
			for _, id := range matching {
				args = append(args, id)
			}
		}
	}
	if !since.IsZero() {
		q += ` AND first_seen >= ?`
		args = append(args, since.UTC().Format(time.RFC3339))
	}
	q += ` ORDER BY first_seen DESC, id DESC`
	if limit <= 0 || limit > 500 {
		limit = 500
	}
	q += ` LIMIT ?`
	args = append(args, limit)

	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var facts []Fact
	for rows.Next() {
		f, err := scanFact(rows)
		if err != nil {
			return nil, err
		}
		facts = append(facts, f)
	}
	return facts, rows.Err()
}

func (s *Store) LessonFacts(projectSel string, limit int) ([]Fact, error) {
	return s.ListFactsByKind("lesson", projectSel, time.Time{}, limit)
}

// CaseFacts returns architectural case records for the project selector, newest first.
func (s *Store) CaseFacts(projectSel string, limit int) ([]Fact, error) {
	return s.ListFactsByKind("case", projectSel, time.Time{}, limit)
}

// VisionFacts retrieves all vision-related facts (non-goals, milestones, architectural invariants) for a project.
func (s *Store) VisionFacts(projectID string) ([]Fact, error) {
	facts, err := s.FactsByProject(projectID)
	if err != nil {
		return nil, err
	}
	var out []Fact
	for _, f := range facts {
		if f.Kind == "nongoal" || f.Kind == "milestone" || f.IsNonGoal() || f.IsMilestone() || f.IsInvariant() {
			out = append(out, f)
		}
	}
	return out, nil
}

// TopFactsForProject retrieves the most relevant facts and file-grounded claims
// for a project. Ordering is by authority: invariants and explicit memos first,
// then distilled facts, then claims that merely reference real files. Ungrounded
// extracted claims are excluded — a claim that quotes a real file is still a
// claim, and one that references nothing is indistinguishable from conversation;
// both stay in the reviewable queue (see CandidateFacts). A manual memo is the
// exception: a person wrote it on purpose, so it is authority even when its
// files are gone. Callers that must distinguish the two returned classes can:
// IsFact()/IsInvariant()/IsManualMemo() is the authority bar, the rest are
// file-referenced claims.
func (s *Store) TopFactsForProject(projectSel string, limit int) ([]Fact, error) {
	if limit <= 0 {
		limit = 5
	}
	facts, err := s.FactsByProject(projectSel)
	if err != nil {
		return nil, err
	}
	var authority []Fact
	var claims []Fact
	for _, f := range facts {
		if f.IsCandidate() {
			continue
		}
		if f.IsInvariant() || f.IsManualMemo() || f.IsFact() {
			authority = append(authority, f)
		} else if f.FileRefsExist || f.Verified {
			claims = append(claims, f)
		}
	}
	now := time.Now().UTC()
	params := DefaultDecayParams()
	sort.SliceStable(authority, func(i, j int) bool {
		ri := CalculateFactRetention(authority[i], now, params)
		rj := CalculateFactRetention(authority[j], now, params)
		if math.Abs(ri-rj) > 1e-4 {
			return ri > rj
		}
		return authority[i].FirstSeen > authority[j].FirstSeen
	})
	sort.SliceStable(claims, func(i, j int) bool {
		ri := CalculateFactRetention(claims[i], now, params)
		rj := CalculateFactRetention(claims[j], now, params)
		if math.Abs(ri-rj) > 1e-4 {
			return ri > rj
		}
		return claims[i].FirstSeen > claims[j].FirstSeen
	})
	combined := append(authority, claims...)
	if len(combined) > limit {
		combined = combined[:limit]
	}
	for i := range combined {
		_ = s.BumpFactAccess(context.Background(), combined[i].ID, "prime")
	}
	return combined, nil
}

// CandidateFacts returns inferred, not-yet-approved facts for a project,
// newest first. These are reviewable observations, never trusted rules.
func (s *Store) CandidateFacts(projectSel string, limit int) ([]Fact, error) {
	if limit <= 0 {
		limit = 3
	}
	ids, err := s.MatchingProjectIDs(projectSel)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, nil
	}
	q := `SELECT ` + selectFactsFields + ` FROM facts WHERE review_status = 'candidate'`
	var args []any
	if len(ids) == 1 {
		q += ` AND project_id = ?`
		args = append(args, ids[0])
	} else {
		placeholders := strings.Repeat("?,", len(ids))
		placeholders = strings.TrimSuffix(placeholders, ",")
		q += ` AND project_id IN (` + placeholders + `)`
		for _, id := range ids {
			args = append(args, id)
		}
	}
	q += ` ORDER BY first_seen DESC LIMIT ?`
	args = append(args, limit)
	return s.facts(q, args...)
}

// GetCompactionCandidates returns episodic facts eligible for A2 extractive compaction:
// non-invariants where compacted_at is empty.
func (s *Store) GetCompactionCandidates(projectID string, limit int) ([]Fact, error) {
	matching, err := s.MatchingProjectIDs(projectID)
	if err != nil {
		return nil, err
	}
	if projectID != "" && len(matching) == 0 {
		return nil, nil
	}

	where := "WHERE (compacted_at IS NULL OR compacted_at = '') AND kind NOT IN ('invariant', 'nongoal')"
	var args []any
	if len(matching) == 1 {
		where += " AND project_id = ?"
		args = append(args, matching[0])
	} else if len(matching) > 1 {
		placeholders := strings.Repeat("?,", len(matching))
		placeholders = "(" + strings.TrimSuffix(placeholders, ",") + ")"
		where += " AND project_id IN " + placeholders
		for _, m := range matching {
			args = append(args, m)
		}
	}

	q := `SELECT ` + selectFactsFields + ` FROM facts ` + where + ` ORDER BY first_seen ASC`
	if limit > 0 {
		q += " LIMIT ?"
		args = append(args, limit)
	}
	return s.facts(q, args...)
}
