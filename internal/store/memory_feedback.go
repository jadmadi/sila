package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Memory relevance feedback records whether a recalled memory helped. It is a
// ranking input only: it never edits claims, evidence, state or relations.
type MemoryFeedbackSignal string

const (
	MemoryFeedbackHelped        MemoryFeedbackSignal = "helped"
	MemoryFeedbackVerified      MemoryFeedbackSignal = "verified"
	MemoryFeedbackLessImportant MemoryFeedbackSignal = "less_important"
	MemoryFeedbackIrrelevant    MemoryFeedbackSignal = "irrelevant"
	MemoryFeedbackWrong         MemoryFeedbackSignal = "wrong"
)

// Feedback error codes. They are public contract: scripts branch on them.
const (
	MemoryCodeFeedbackSignal      = "FEEDBACK_SIGNAL"
	MemoryCodeFeedbackNotReturned = "FEEDBACK_NOT_RETURNED"
	MemoryCodeFeedbackExpired     = "FEEDBACK_RETRIEVAL_EXPIRED"
	MemoryCodeFeedbackSecret      = "FEEDBACK_SECRET"
	MemoryCodeFeedbackNoteLimit   = "FEEDBACK_NOTE_LIMIT"
	MemoryCodeFeedbackBound       = "FEEDBACK_BOUND"
	MemoryCodeFeedbackSuperseded  = "FEEDBACK_SUPERSEDED"
	MemoryCodeFeedbackProject     = "FEEDBACK_PROJECT"
)

// Feedback bounds. Weights are halved for unbound CLI signals and aggregated
// with a 90-day half-life inside a hard ±120 cap.
const (
	MemoryFeedbackMaxNote      = 500
	MemoryFeedbackWeightCap    = 120.0
	MemoryFeedbackHalfLifeDays = 90.0
	MemoryFeedbackLifetime     = 180 * 24 * time.Hour
	MemoryRetrievalLifetime    = 30 * 24 * time.Hour
	memoryFeedbackCleanupBatch = 100
	memoryRetrievalHashSalt    = "sila/memory-retrieval/v1"
)

var memoryFeedbackWeights = map[MemoryFeedbackSignal]float64{
	MemoryFeedbackHelped:        40,
	MemoryFeedbackVerified:      60,
	MemoryFeedbackLessImportant: -20,
	MemoryFeedbackIrrelevant:    -40,
	MemoryFeedbackWrong:         -80,
}

// MemoryFeedbackSignalValues lists the accepted signals in declaration order.
func MemoryFeedbackSignalValues() []string {
	return []string{
		string(MemoryFeedbackHelped), string(MemoryFeedbackVerified),
		string(MemoryFeedbackLessImportant), string(MemoryFeedbackIrrelevant),
		string(MemoryFeedbackWrong),
	}
}

// ParseMemoryFeedbackSignal normalizes one signal value.
func ParseMemoryFeedbackSignal(raw string) (MemoryFeedbackSignal, bool) {
	sig := MemoryFeedbackSignal(strings.TrimSpace(raw))
	if _, ok := memoryFeedbackWeights[sig]; !ok {
		return "", false
	}
	return sig, true
}

// MemoryFeedbackBaseWeight returns the signed weight before decay or the
// unbound halving.
func MemoryFeedbackBaseWeight(sig MemoryFeedbackSignal) (float64, bool) {
	weight, ok := memoryFeedbackWeights[sig]
	return weight, ok
}

// MemoryRetrieval is one recorded pack identity. It stores a salted query
// hash, counts, and returned IDs; never query text, prompts, code or content.
type MemoryRetrieval struct {
	ID         string `json:"retrieval_id"`
	ProjectID  string `json:"project_id"`
	Agent      string `json:"agent"`
	SessionRef string `json:"session_ref,omitempty"`
	QueryHash  string `json:"query_hash"`
	ItemCount  int    `json:"item_count"`
	CreatedAt  string `json:"created_at"`
	ExpiresAt  string `json:"expires_at"`
}

// MemoryRetrievalItem is one memory returned by a recorded pack.
type MemoryRetrievalItem struct {
	RetrievalID string  `json:"retrieval_id"`
	MemoryID    string  `json:"memory_id"`
	Rank        int     `json:"rank"`
	Kind        string  `json:"kind"`
	Score       float64 `json:"score"`
}

// MemoryFeedback is one accepted or superseded relevance judgment.
type MemoryFeedback struct {
	ID           string               `json:"id"`
	ProjectID    string               `json:"project_id"`
	MemoryID     string               `json:"memory_id"`
	RetrievalID  string               `json:"retrieval_id,omitempty"`
	Signal       MemoryFeedbackSignal `json:"signal"`
	Weight       float64              `json:"weight"`
	Unbound      bool                 `json:"unbound"`
	Note         string               `json:"note,omitempty"`
	Agent        string               `json:"agent"`
	SessionRef   string               `json:"session_ref,omitempty"`
	CreatedAt    string               `json:"created_at"`
	ExpiresAt    string               `json:"expires_at"`
	SupersededBy string               `json:"superseded_by,omitempty"`
	MetadataJSON string               `json:"metadata_json,omitempty"`
}

// FeedbackAdjustment is the decayed, capped ranking input for one memory.
type FeedbackAdjustment struct {
	MemoryID          string   `json:"memory_id"`
	Adjustment        float64  `json:"adjustment"`
	Accepted          int      `json:"accepted"`
	Unbound           int      `json:"unbound"`
	Wrong             int      `json:"wrong"`
	Superseded        int      `json:"superseded"`
	RowIDs            []string `json:"row_ids,omitempty"`
	RecommendedAction string   `json:"recommended_action,omitempty"`
}

// MemoryFeedbackInput is one feedback write request.
type MemoryFeedbackInput struct {
	ProjectID   string
	MemoryID    string
	RetrievalID string
	Signal      MemoryFeedbackSignal
	Note        string
	Agent       string
	SessionRef  string
	Unbound     bool
}

// MemoryFeedbackOutcome reports the accepted row and the resulting adjustment.
type MemoryFeedbackOutcome struct {
	Feedback          MemoryFeedback     `json:"feedback"`
	Created           bool               `json:"created"`
	SupersededID      string             `json:"superseded_id,omitempty"`
	Adjustment        FeedbackAdjustment `json:"adjustment"`
	RecommendedAction string             `json:"recommended_action,omitempty"`
}

// MemoryFeedbackTarget names one memory in its project for batch scoring.
type MemoryFeedbackTarget struct {
	ProjectID string
	MemoryID  string
}

// MemoryFeedbackListOptions filters feedback history.
type MemoryFeedbackListOptions struct {
	ProjectID string
	MemoryID  string
	Signal    MemoryFeedbackSignal
	Since     time.Time
	Now       time.Time
	Limit     int
}

// MemoryFeedbackSummary is the content-free list projection. Notes are carried
// only on the returned rows, never in counts or diagnostics.
type MemoryFeedbackSummary struct {
	Schema      string               `json:"schema"`
	Feedback    []MemoryFeedback     `json:"feedback"`
	Counts      map[string]int       `json:"counts"`
	Total       int                  `json:"total"`
	Accepted    int                  `json:"accepted"`
	Superseded  int                  `json:"superseded"`
	Bound       int                  `json:"bound"`
	Unbound     int                  `json:"unbound"`
	TopAdjusted []FeedbackAdjustment `json:"top_adjusted,omitempty"`
}

// MemoryFeedbackDiagnostics is a content-free health report.
type MemoryFeedbackDiagnostics struct {
	Retrievals        int `json:"retrievals"`
	Items             int `json:"items"`
	Feedback          int `json:"feedback"`
	Superseded        int `json:"superseded"`
	OrphanedItems     int `json:"orphaned_items"`
	ExpiredRetrievals int `json:"expired_retrievals"`
	ExpiredFeedback   int `json:"expired_feedback"`
	OrphanedFeedback  int `json:"orphaned_feedback"`
}

// A single process serializes feedback writes so a read-check-write sequence
// cannot race itself; SQLite still serializes across processes.
var memoryFeedbackMu sync.Mutex

var memoryFeedbackSecretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(sk-[a-z0-9]{16,}|gh[pousr]_[a-z0-9]{16,}|github_pat_[a-z0-9_]{20,}|xox[abprs]-[a-z0-9-]{10,}|AKIA[0-9A-Z]{16}|AIza[0-9A-Za-z_-]{35})`),
	regexp.MustCompile(`(?i)-----BEGIN [A-Z ]*PRIVATE KEY-----`),
	regexp.MustCompile(`(?i)\b(bearer|token|password|passwd|secret|api[_-]?key)\s*[:=]\s*\S{8,}`),
}

var memoryFeedbackPathPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(^|\s)~(/|\s|$)`),
	regexp.MustCompile(`(^|\s)/(home|users|root|etc|var|tmp|mnt|opt|srv|usr|private|proc|sys|dev)/`),
	regexp.MustCompile(`(^|\s)/[A-Za-z0-9._-]+/[A-Za-z0-9._/-]+`),
	regexp.MustCompile(`(^|\s)[A-Za-z]:[\\/]`),
}

// ValidateMemoryFeedbackNote bounds a note and rejects secrets, home paths and
// absolute paths.
func ValidateMemoryFeedbackNote(note string) error {
	if utf8.RuneCountInString(note) > MemoryFeedbackMaxNote {
		return memoryErr(MemoryCodeFeedbackNoteLimit, "note", "note exceeds %d characters", MemoryFeedbackMaxNote)
	}
	if strings.TrimSpace(note) == "" {
		return nil
	}
	for _, re := range memoryFeedbackSecretPatterns {
		if re.MatchString(note) {
			return memoryErr(MemoryCodeFeedbackSecret, "note", "note contains a credential or secret pattern")
		}
	}
	for _, re := range memoryFeedbackPathPatterns {
		if re.MatchString(note) {
			return memoryErr(MemoryCodeFeedbackSecret, "note", "note contains a home or absolute path")
		}
	}
	return nil
}

// RecordRetrieval records one pack identity and its returned memory IDs.
func (s *Store) RecordRetrieval(ctx context.Context, project, agent, session, query string, items []MemoryRetrievalItem) (string, error) {
	return s.RecordRetrievalAt(ctx, project, agent, session, query, items, time.Now().UTC())
}

// RecordRetrievalAt is RecordRetrieval with an explicit clock.
func (s *Store) RecordRetrievalAt(ctx context.Context, project, agent, session, query string, items []MemoryRetrievalItem, now time.Time) (string, error) {
	if s == nil || s.DB == nil {
		return "", errors.New("record retrieval: store is nil")
	}
	if s.ReadOnly {
		return "", errors.New("record retrieval: store is read-only")
	}
	now = now.UTC()
	project = strings.TrimSpace(project)
	if project == "" {
		project = "default"
	}
	agent = strings.TrimSpace(agent)
	if agent == "" {
		agent = "agent"
	}

	ordered := make([]MemoryRetrievalItem, 0, len(items))
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		memoryID := strings.TrimSpace(item.MemoryID)
		if memoryID == "" || seen[memoryID] {
			continue
		}
		seen[memoryID] = true
		rank := item.Rank
		if rank <= 0 {
			rank = len(ordered) + 1
		}
		ordered = append(ordered, MemoryRetrievalItem{
			MemoryID: memoryID,
			Rank:     rank,
			Kind:     item.Kind,
			Score:    item.Score,
		})
		if len(ordered) >= 100 {
			break
		}
	}

	retrievalID := "ret_" + NewV7UUID()
	createdAt := now.Format(time.RFC3339)
	expiresAt := now.Add(MemoryRetrievalLifetime).Format(time.RFC3339)
	queryHash := memoryRetrievalQueryHash(query)

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("begin retrieval tx: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO memory_retrievals
		(id, project_id, agent, session_ref, query_hash, item_count, created_at, expires_at)
		VALUES(?,?,?,?,?,?,?,?)`,
		retrievalID, project, agent, memoryNullString(strings.TrimSpace(session)),
		queryHash, len(ordered), createdAt, expiresAt); err != nil {
		return "", fmt.Errorf("insert retrieval: %w", err)
	}
	for _, item := range ordered {
		if _, err := tx.ExecContext(ctx, `INSERT INTO memory_retrieval_items
			(retrieval_id, memory_id, rank, kind, score) VALUES(?,?,?,?,?)`,
			retrievalID, item.MemoryID, item.Rank, item.Kind, item.Score); err != nil {
			return "", fmt.Errorf("insert retrieval item: %w", err)
		}
	}
	if err := pruneMemoryFeedbackTx(ctx, tx, now); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("commit retrieval: %w", err)
	}
	return retrievalID, nil
}

func memoryRetrievalQueryHash(query string) string {
	sum := sha256.Sum256([]byte(memoryRetrievalHashSalt + "\x00" + query))
	return hex.EncodeToString(sum[:])
}

// pruneMemoryFeedbackTx removes at most 100 expired ledger rows, 100 expired
// feedback rows, and 100 orphaned retrieval items per call.
func pruneMemoryFeedbackTx(ctx context.Context, tx *sql.Tx, now time.Time) error {
	stamp := now.UTC().Format(time.RFC3339)
	limit := memoryFeedbackCleanupBatch
	prunes := []struct {
		query string
		args  []any
	}{
		{`DELETE FROM memory_retrieval_items WHERE rowid IN (
			SELECT i.rowid FROM memory_retrieval_items i
			JOIN memory_retrievals r ON r.id = i.retrieval_id
			WHERE r.expires_at <= ? LIMIT ?)`, []any{stamp, limit}},
		{`DELETE FROM memory_retrievals WHERE rowid IN (
			SELECT rowid FROM memory_retrievals WHERE expires_at <= ? LIMIT ?)`, []any{stamp, limit}},
		{`DELETE FROM memory_retrieval_items WHERE rowid IN (
			SELECT i.rowid FROM memory_retrieval_items i
			WHERE NOT EXISTS(SELECT 1 FROM memory_retrievals r WHERE r.id = i.retrieval_id)
			LIMIT ?)`, []any{limit}},
		{`DELETE FROM memory_feedback WHERE rowid IN (
			SELECT rowid FROM memory_feedback WHERE expires_at <= ? LIMIT ?)`, []any{stamp, limit}},
	}
	for _, prune := range prunes {
		if _, err := tx.ExecContext(ctx, prune.query, prune.args...); err != nil {
			return fmt.Errorf("prune memory feedback ledger: %w", err)
		}
	}
	return nil
}

// MemoryRetrievalByID loads one ledger row.
func (s *Store) MemoryRetrievalByID(id string) (*MemoryRetrieval, error) {
	var r MemoryRetrieval
	err := s.DB.QueryRow(`SELECT id, project_id, agent, COALESCE(session_ref, ''),
		query_hash, item_count, created_at, expires_at FROM memory_retrievals WHERE id = ?`, id).
		Scan(&r.ID, &r.ProjectID, &r.Agent, &r.SessionRef, &r.QueryHash, &r.ItemCount, &r.CreatedAt, &r.ExpiresAt)
	if err == sql.ErrNoRows {
		return nil, memoryErr(MemoryCodeFeedbackNotReturned, "retrieval_id", "retrieval %q not found", id)
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// MemoryRetrievalItems lists the memories a retrieval returned.
func (s *Store) MemoryRetrievalItems(retrievalID string) ([]MemoryRetrievalItem, error) {
	rows, err := s.DB.Query(`SELECT retrieval_id, memory_id, rank, kind, score
		FROM memory_retrieval_items WHERE retrieval_id = ? ORDER BY rank, memory_id`, retrievalID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MemoryRetrievalItem
	for rows.Next() {
		var item MemoryRetrievalItem
		if err := rows.Scan(&item.RetrievalID, &item.MemoryID, &item.Rank, &item.Kind, &item.Score); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// RecordMemoryFeedback validates and persists one relevance signal.
func (s *Store) RecordMemoryFeedback(ctx context.Context, in MemoryFeedbackInput) (*MemoryFeedbackOutcome, error) {
	return s.RecordMemoryFeedbackAt(ctx, in, time.Now().UTC())
}

// RecordMemoryFeedbackAt is RecordMemoryFeedback with an explicit clock.
func (s *Store) RecordMemoryFeedbackAt(ctx context.Context, in MemoryFeedbackInput, now time.Time) (*MemoryFeedbackOutcome, error) {
	if s == nil || s.DB == nil {
		return nil, errors.New("record memory feedback: store is nil")
	}
	if s.ReadOnly {
		return nil, errors.New("record memory feedback: store is read-only")
	}
	now = now.UTC()
	signal, ok := ParseMemoryFeedbackSignal(string(in.Signal))
	if !ok {
		return nil, memoryErr(MemoryCodeFeedbackSignal, "signal", "signal must be one of %s", strings.Join(MemoryFeedbackSignalValues(), ", "))
	}
	if err := ValidateMemoryFeedbackNote(in.Note); err != nil {
		return nil, err
	}
	memoryID := strings.TrimSpace(in.MemoryID)
	if memoryID == "" {
		return nil, memoryErr(MemoryCodeFeedbackBound, "memory_id", "memory_id is required")
	}
	retrievalID := strings.TrimSpace(in.RetrievalID)
	if in.Unbound && retrievalID != "" {
		return nil, memoryErr(MemoryCodeFeedbackBound, "retrieval_id", "unbound feedback must not cite a retrieval")
	}
	if !in.Unbound && retrievalID == "" {
		return nil, memoryErr(MemoryCodeFeedbackNotReturned, "retrieval_id", "retrieval_id is required unless unbound is set")
	}
	agent := strings.TrimSpace(in.Agent)
	if agent == "" {
		agent = "agent"
	}
	project := strings.TrimSpace(in.ProjectID)

	memoryFeedbackMu.Lock()
	defer memoryFeedbackMu.Unlock()

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin memory feedback tx: %w", err)
	}
	defer tx.Rollback()

	var memoryProject string
	err = tx.QueryRowContext(ctx, `SELECT project_id FROM memory_records WHERE id = ?`, memoryID).Scan(&memoryProject)
	if err == sql.ErrNoRows {
		return nil, memoryErr(MemoryCodeFeedbackProject, "memory_id", "memory %q not found", memoryID)
	}
	if err != nil {
		return nil, err
	}
	if project == "" {
		project = memoryProject
	}
	if memoryProject != project {
		return nil, memoryErr(MemoryCodeFeedbackProject, "project_id", "memory %q belongs to project %q, not %q", memoryID, memoryProject, project)
	}
	if retrievalID != "" {
		if err := validateFeedbackRetrievalTx(ctx, tx, retrievalID, memoryID, project, now); err != nil {
			return nil, err
		}
	}

	base, _ := MemoryFeedbackBaseWeight(signal)
	weight := base
	if in.Unbound {
		weight = base / 2
	}

	var activeID, activeSignal string
	if retrievalID != "" {
		err = tx.QueryRowContext(ctx, `SELECT id, signal FROM memory_feedback
			WHERE retrieval_id = ? AND memory_id = ? AND agent = ? AND superseded_by IS NULL`,
			retrievalID, memoryID, agent).Scan(&activeID, &activeSignal)
		if err != nil && err != sql.ErrNoRows {
			return nil, err
		}
	}
	if activeID != "" && activeSignal == string(signal) {
		row, err := memoryFeedbackByIDTx(ctx, tx, activeID)
		if err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return s.feedbackOutcome(ctx, *row, false, "", now), nil
	}

	feedbackID := "fb_" + NewV7UUID()
	createdAt := now.Format(time.RFC3339)
	expiresAt := now.Add(MemoryFeedbackLifetime).Format(time.RFC3339)
	// Retire the prior active row first: the partial unique index allows only
	// one accepted row per (retrieval, memory, agent).
	if activeID != "" {
		res, err := tx.ExecContext(ctx, `UPDATE memory_feedback SET superseded_by = ?
			WHERE id = ? AND superseded_by IS NULL`, feedbackID, activeID)
		if err != nil {
			return nil, fmt.Errorf("supersede memory feedback: %w", err)
		}
		if changed, _ := res.RowsAffected(); changed == 0 {
			return nil, memoryErr(MemoryCodeFeedbackSuperseded, "retrieval_id", "feedback row changed concurrently; retry")
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO memory_feedback
		(id, project_id, memory_id, retrieval_id, signal, weight, unbound, note, agent,
		 session_ref, created_at, expires_at, superseded_by, metadata_json)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,NULL,'{}')`,
		feedbackID, project, memoryID, memoryNullString(retrievalID), string(signal),
		weight, boolToInt(in.Unbound), memoryNullString(strings.TrimSpace(in.Note)),
		agent, memoryNullString(strings.TrimSpace(in.SessionRef)), createdAt, expiresAt); err != nil {
		return nil, fmt.Errorf("insert memory feedback: %w", err)
	}
	row, err := memoryFeedbackByIDTx(ctx, tx, feedbackID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit memory feedback: %w", err)
	}
	return s.feedbackOutcome(ctx, *row, true, activeID, now), nil
}

func (s *Store) feedbackOutcome(ctx context.Context, row MemoryFeedback, created bool, supersededID string, now time.Time) *MemoryFeedbackOutcome {
	// The write already committed. A scoring read failure must not turn an
	// accepted signal into an error; report the row with a zero adjustment.
	adjustment, err := s.FeedbackAdjustmentAt(ctx, row.ProjectID, row.MemoryID, now)
	if err != nil {
		adjustment = FeedbackAdjustment{MemoryID: row.MemoryID}
	}
	return &MemoryFeedbackOutcome{
		Feedback:          row,
		Created:           created,
		SupersededID:      supersededID,
		Adjustment:        adjustment,
		RecommendedAction: adjustment.RecommendedAction,
	}
}

func validateFeedbackRetrievalTx(ctx context.Context, tx *sql.Tx, retrievalID, memoryID, project string, now time.Time) error {
	var retrievalProject, expiresAt string
	err := tx.QueryRowContext(ctx, `SELECT project_id, expires_at FROM memory_retrievals WHERE id = ?`,
		retrievalID).Scan(&retrievalProject, &expiresAt)
	if err == sql.ErrNoRows {
		return memoryErr(MemoryCodeFeedbackNotReturned, "retrieval_id", "retrieval %q not found", retrievalID)
	}
	if err != nil {
		return err
	}
	if retrievalProject != project {
		return memoryErr(MemoryCodeFeedbackProject, "retrieval_id", "retrieval %q belongs to project %q, not %q", retrievalID, retrievalProject, project)
	}
	expires, err := time.Parse(time.RFC3339, expiresAt)
	if err != nil || !now.Before(expires) {
		return memoryErr(MemoryCodeFeedbackExpired, "retrieval_id", "retrieval %q expired; recall again before reporting", retrievalID)
	}
	var exists int
	err = tx.QueryRowContext(ctx, `SELECT 1 FROM memory_retrieval_items WHERE retrieval_id = ? AND memory_id = ?`,
		retrievalID, memoryID).Scan(&exists)
	if err == sql.ErrNoRows {
		return memoryErr(MemoryCodeFeedbackNotReturned, "memory_id", "memory %q was not returned by retrieval %q", memoryID, retrievalID)
	}
	return err
}

const memoryFeedbackSelect = `SELECT id, project_id, memory_id, COALESCE(retrieval_id, ''),
	signal, weight, unbound, COALESCE(note, ''), agent, COALESCE(session_ref, ''),
	created_at, expires_at, COALESCE(superseded_by, ''), metadata_json
	FROM memory_feedback`

func scanMemoryFeedback(scanner interface{ Scan(...any) error }) (*MemoryFeedback, error) {
	var fb MemoryFeedback
	var unbound int
	if err := scanner.Scan(
		&fb.ID, &fb.ProjectID, &fb.MemoryID, &fb.RetrievalID, &fb.Signal, &fb.Weight,
		&unbound, &fb.Note, &fb.Agent, &fb.SessionRef, &fb.CreatedAt, &fb.ExpiresAt,
		&fb.SupersededBy, &fb.MetadataJSON,
	); err != nil {
		return nil, err
	}
	fb.Unbound = unbound != 0
	return &fb, nil
}

func memoryFeedbackByIDTx(ctx context.Context, tx *sql.Tx, id string) (*MemoryFeedback, error) {
	return scanMemoryFeedback(tx.QueryRowContext(ctx, memoryFeedbackSelect+` WHERE id = ?`, id))
}

// MemoryFeedbackByID loads one feedback row.
func (s *Store) MemoryFeedbackByID(id string) (*MemoryFeedback, error) {
	fb, err := scanMemoryFeedback(s.DB.QueryRow(memoryFeedbackSelect+` WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return nil, memoryErr(MemoryCodeFeedbackNotReturned, "id", "feedback %q not found", id)
	}
	return fb, err
}

// FeedbackAdjustmentAt aggregates accepted, non-superseded feedback for one
// memory with a 90-day half-life and a hard ±120 cap.
func (s *Store) FeedbackAdjustmentAt(ctx context.Context, project, memoryID string, now time.Time) (FeedbackAdjustment, error) {
	adjustments, err := s.FeedbackAdjustmentsAt(ctx, []MemoryFeedbackTarget{{ProjectID: project, MemoryID: memoryID}}, now)
	if err != nil {
		return FeedbackAdjustment{}, err
	}
	return adjustments[memoryID], nil
}

// FeedbackAdjustmentsAt batch-scores memories. The result is keyed by memory
// ID; missing IDs carry a zero adjustment.
func (s *Store) FeedbackAdjustmentsAt(ctx context.Context, targets []MemoryFeedbackTarget, now time.Time) (map[string]FeedbackAdjustment, error) {
	out := make(map[string]FeedbackAdjustment, len(targets))
	if s == nil || s.DB == nil {
		return out, nil
	}
	now = now.UTC()
	seen := make(map[string]bool, len(targets))
	var clauses []string
	var args []any
	for _, target := range targets {
		memoryID := strings.TrimSpace(target.MemoryID)
		if memoryID == "" || seen[memoryID] {
			continue
		}
		seen[memoryID] = true
		out[memoryID] = FeedbackAdjustment{MemoryID: memoryID}
		clauses = append(clauses, `(project_id = ? AND memory_id = ?)`)
		args = append(args, strings.TrimSpace(target.ProjectID), memoryID)
		if len(clauses) >= 500 {
			break
		}
	}
	if len(clauses) == 0 {
		return out, nil
	}
	query := `SELECT id, memory_id, signal, weight, unbound, created_at FROM memory_feedback
		WHERE superseded_by IS NULL AND expires_at > ? AND (` + strings.Join(clauses, " OR ") + `)
		ORDER BY created_at, id`
	args = append([]any{now.Format(time.RFC3339)}, args...)
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	sums := make(map[string]float64, len(out))
	wrong := make(map[string]int, len(out))
	accepted := make(map[string]int, len(out))
	unbound := make(map[string]int, len(out))
	rowIDs := make(map[string][]string, len(out))
	for rows.Next() {
		var id, memoryID, signal, createdAt string
		var weight float64
		var isUnbound int
		if err := rows.Scan(&id, &memoryID, &signal, &weight, &isUnbound, &createdAt); err != nil {
			return nil, err
		}
		created, err := time.Parse(time.RFC3339, createdAt)
		if err != nil {
			continue
		}
		sums[memoryID] += weight * memoryFeedbackDecay(created, now)
		accepted[memoryID]++
		if isUnbound != 0 {
			unbound[memoryID]++
		}
		if MemoryFeedbackSignal(signal) == MemoryFeedbackWrong {
			wrong[memoryID]++
		}
		rowIDs[memoryID] = append(rowIDs[memoryID], id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	superseded, err := s.supersededFeedbackCounts(ctx, targets)
	if err != nil {
		return nil, err
	}
	for memoryID, adj := range out {
		adj.Adjustment = clampMemoryFeedbackAdjustment(sums[memoryID])
		adj.Accepted = accepted[memoryID]
		adj.Unbound = unbound[memoryID]
		adj.Wrong = wrong[memoryID]
		adj.Superseded = superseded[memoryID]
		adj.RowIDs = rowIDs[memoryID]
		if adj.Wrong > 0 {
			adj.RecommendedAction = "archive"
		}
		out[memoryID] = adj
	}
	return out, nil
}

func (s *Store) supersededFeedbackCounts(ctx context.Context, targets []MemoryFeedbackTarget) (map[string]int, error) {
	out := map[string]int{}
	var clauses []string
	var args []any
	seen := map[string]bool{}
	for _, target := range targets {
		memoryID := strings.TrimSpace(target.MemoryID)
		if memoryID == "" || seen[memoryID] {
			continue
		}
		seen[memoryID] = true
		clauses = append(clauses, `(project_id = ? AND memory_id = ?)`)
		args = append(args, strings.TrimSpace(target.ProjectID), memoryID)
	}
	if len(clauses) == 0 {
		return out, nil
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT memory_id, COUNT(*) FROM memory_feedback
		WHERE superseded_by IS NOT NULL AND (`+strings.Join(clauses, " OR ")+`) GROUP BY memory_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var memoryID string
		var count int
		if err := rows.Scan(&memoryID, &count); err != nil {
			return nil, err
		}
		out[memoryID] = count
	}
	return out, rows.Err()
}

func memoryFeedbackDecay(created, now time.Time) float64 {
	ageDays := now.Sub(created).Hours() / 24
	if ageDays < 0 {
		ageDays = 0
	}
	return math.Pow(0.5, ageDays/MemoryFeedbackHalfLifeDays)
}

func clampMemoryFeedbackAdjustment(sum float64) float64 {
	if sum > MemoryFeedbackWeightCap {
		return MemoryFeedbackWeightCap
	}
	if sum < -MemoryFeedbackWeightCap {
		return -MemoryFeedbackWeightCap
	}
	return math.Round(sum*1000) / 1000
}

// MemoryIDsForFactIDs maps fact-ledger IDs onto current typed memory IDs.
func (s *Store) MemoryIDsForFactIDs(factIDs []string) (map[string]string, error) {
	out := map[string]string{}
	var wanted []string
	seen := map[string]bool{}
	for _, id := range factIDs {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		wanted = append(wanted, id)
	}
	if len(wanted) == 0 {
		return out, nil
	}
	placeholders := strings.TrimRight(strings.Repeat("?,", len(wanted)), ",")
	args := make([]any, 0, len(wanted))
	for _, id := range wanted {
		args = append(args, id)
	}
	rows, err := s.DB.Query(`SELECT id, fact_id FROM memory_records r
		WHERE r.fact_id IN (`+placeholders+`) AND r.state = 'current'
		AND NOT EXISTS(SELECT 1 FROM memory_relations x WHERE x.target_memory_id = r.id AND x.relation_type = 'supersedes')
		ORDER BY r.created_at DESC, r.id DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var memoryID, factID string
		if err := rows.Scan(&memoryID, &factID); err != nil {
			return nil, err
		}
		if out[factID] == "" {
			out[factID] = memoryID
		}
	}
	return out, rows.Err()
}

// ListMemoryFeedback returns bounded feedback history plus content-free counts.
func (s *Store) ListMemoryFeedback(opts MemoryFeedbackListOptions) (*MemoryFeedbackSummary, error) {
	summary := &MemoryFeedbackSummary{
		Schema:   "sila/memory-feedback-list/v1",
		Feedback: []MemoryFeedback{},
		Counts:   map[string]int{},
	}
	query := memoryFeedbackSelect
	var where []string
	var args []any
	if project := strings.TrimSpace(opts.ProjectID); project != "" {
		where = append(where, "project_id = ?")
		args = append(args, project)
	}
	if memoryID := strings.TrimSpace(opts.MemoryID); memoryID != "" {
		where = append(where, "memory_id = ?")
		args = append(args, memoryID)
	}
	if signal := strings.TrimSpace(string(opts.Signal)); signal != "" {
		where = append(where, "signal = ?")
		args = append(args, signal)
	}
	if !opts.Since.IsZero() {
		where = append(where, "created_at >= ?")
		args = append(args, opts.Since.UTC().Format(time.RFC3339))
	}
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY created_at DESC, id DESC"
	limit := opts.Limit
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	query += " LIMIT ?"
	args = append(args, limit)

	rows, err := s.DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		fb, err := scanMemoryFeedback(rows)
		if err != nil {
			return nil, err
		}
		summary.Feedback = append(summary.Feedback, *fb)
		summary.Total++
		summary.Counts[string(fb.Signal)]++
		if fb.SupersededBy != "" {
			summary.Superseded++
		} else {
			summary.Accepted++
		}
		if fb.Unbound {
			summary.Unbound++
		} else {
			summary.Bound++
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	summary.TopAdjusted, err = s.topAdjustedFeedback(opts)
	if err != nil {
		return nil, err
	}
	return summary, nil
}

func (s *Store) topAdjustedFeedback(opts MemoryFeedbackListOptions) ([]FeedbackAdjustment, error) {
	query := `SELECT DISTINCT memory_id, project_id FROM memory_feedback WHERE superseded_by IS NULL`
	var args []any
	if project := strings.TrimSpace(opts.ProjectID); project != "" {
		query += " AND project_id = ?"
		args = append(args, project)
	}
	if memoryID := strings.TrimSpace(opts.MemoryID); memoryID != "" {
		query += " AND memory_id = ?"
		args = append(args, memoryID)
	}
	query += " LIMIT 50"
	now := opts.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	rows, err := s.DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var targets []MemoryFeedbackTarget
	for rows.Next() {
		var target MemoryFeedbackTarget
		if err := rows.Scan(&target.MemoryID, &target.ProjectID); err != nil {
			return nil, err
		}
		targets = append(targets, target)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	adjustments, err := s.FeedbackAdjustmentsAt(context.Background(), targets, now)
	if err != nil {
		return nil, err
	}
	out := make([]FeedbackAdjustment, 0, len(adjustments))
	for _, adj := range adjustments {
		if adj.Adjustment == 0 && adj.Accepted == 0 {
			continue
		}
		out = append(out, adj)
	}
	sort.Slice(out, func(i, j int) bool {
		absI, absJ := math.Abs(out[i].Adjustment), math.Abs(out[j].Adjustment)
		if absI != absJ {
			return absI > absJ
		}
		return out[i].MemoryID < out[j].MemoryID
	})
	if len(out) > 5 {
		out = out[:5]
	}
	return out, nil
}

// MemoryFeedbackDiagnostics reports ledger health without notes or content.
func (s *Store) MemoryFeedbackDiagnostics(now time.Time) (MemoryFeedbackDiagnostics, error) {
	var d MemoryFeedbackDiagnostics
	counts := []struct {
		dest  *int
		query string
	}{
		{&d.Retrievals, `SELECT COUNT(*) FROM memory_retrievals`},
		{&d.Items, `SELECT COUNT(*) FROM memory_retrieval_items`},
		{&d.Feedback, `SELECT COUNT(*) FROM memory_feedback`},
		{&d.Superseded, `SELECT COUNT(*) FROM memory_feedback WHERE superseded_by IS NOT NULL`},
		{&d.OrphanedItems, `SELECT COUNT(*) FROM memory_retrieval_items i
			WHERE NOT EXISTS(SELECT 1 FROM memory_retrievals r WHERE r.id = i.retrieval_id)`},
		{&d.OrphanedFeedback, `SELECT COUNT(*) FROM memory_feedback f
			WHERE NOT EXISTS(SELECT 1 FROM memory_records r WHERE r.id = f.memory_id)`},
	}
	for _, c := range counts {
		if err := s.DB.QueryRow(c.query).Scan(c.dest); err != nil {
			return d, err
		}
	}
	stamp := now.UTC().Format(time.RFC3339)
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM memory_retrievals WHERE expires_at <= ?`, stamp).Scan(&d.ExpiredRetrievals); err != nil {
		return d, err
	}
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM memory_feedback WHERE expires_at <= ?`, stamp).Scan(&d.ExpiredFeedback); err != nil {
		return d, err
	}
	return d, nil
}
