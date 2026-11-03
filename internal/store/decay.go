package store

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"
)

// MemoryTier defines the memory persistence classification.
type MemoryTier string

const (
	TierWorking    MemoryTier = "working"    // 1-day half-life
	TierEpisodic   MemoryTier = "episodic"   // 35-day half-life
	TierProcedural MemoryTier = "procedural" // 90-day half-life
	TierSemantic   MemoryTier = "semantic"   // Infinite half-life (no decay)
)

// DecayParams configures retention decay and reinforcement weights.
type DecayParams struct {
	Sigma         float64 // Access count weight (default: 0.6)
	Mu            float64 // Idle access decay factor (default: 0.04)
	BreadthWeight float64 // Cross-tool corroboration multiplier (default: 0.35)
	ColdThreshold float64 // Eviction/compaction boundary (default: 0.20)
}

// DefaultDecayParams returns standard production parameters.
func DefaultDecayParams() DecayParams {
	return DecayParams{
		Sigma:         0.6,
		Mu:            0.04,
		BreadthWeight: 0.35,
		ColdThreshold: 0.20,
	}
}

// TierLambda returns the exponential decay constant λ for a given tier.
// λ = ln(2) / T_half.
func TierLambda(tier MemoryTier) float64 {
	switch strings.ToLower(string(tier)) {
	case string(TierWorking):
		return math.Ln2 / 1.0 // ~0.6931
	case string(TierEpisodic):
		return math.Ln2 / 35.0 // ~0.0198
	case string(TierProcedural):
		return math.Ln2 / 90.0 // ~0.0077
	case string(TierSemantic):
		return 0.0 // strictly immune to decay
	default:
		return math.Ln2 / 35.0
	}
}

// ClampSalience clamps salience strictly between [0.25, 2.0].
// If salience <= 0 (uninitialized), it defaults to baseline 1.0.
func ClampSalience(salience float64) float64 {
	if salience <= 0 {
		return 1.0
	}
	if salience < 0.25 {
		return 0.25
	}
	if salience > 2.0 {
		return 2.0
	}
	return salience
}

// CalculateRetentionScore implements the access-weighted mathematical retention formula:
// Retention = salience * e^(-λ_tier * age_days) + σ * ln(1 + access_count) * e^(-μ * days_since_access) * breadth
// where breadth = 1 + w_breadth * ln(1 + max(distinct_tools - 1, 0))
func CalculateRetentionScore(tier MemoryTier, salience float64, ageDays, daysSinceAccess float64, accessCount int, distinctTools int, params DecayParams) float64 {
	// Baseline sanitization
	if params.Sigma <= 0 {
		params.Sigma = 0.6
	}
	if params.Mu <= 0 {
		params.Mu = 0.04
	}
	if params.BreadthWeight <= 0 {
		params.BreadthWeight = 0.35
	}

	salience = ClampSalience(salience)
	if ageDays < 0 {
		ageDays = 0
	}
	if daysSinceAccess < 0 {
		daysSinceAccess = 0
	}
	if distinctTools < 1 {
		distinctTools = 1
	}

	// 1. Base decay term: salience * e^(-λ * age_days)
	lambda := TierLambda(tier)
	baseTerm := salience * math.Exp(-lambda*ageDays)

	// 2. Multi-actor breadth term
	actorExcess := distinctTools - 1
	if actorExcess < 0 {
		actorExcess = 0
	}
	breadth := 1.0 + params.BreadthWeight*math.Log(1.0+float64(actorExcess))

	// 3. Reinforcement term
	reinforcement := 0.0
	if accessCount > 0 {
		logAccess := math.Log(1.0 + float64(accessCount))
		idleDecay := math.Exp(-params.Mu * daysSinceAccess)
		reinforcement = params.Sigma * logAccess * idleDecay * breadth
	}

	retention := baseTerm + reinforcement
	if math.IsNaN(retention) || math.IsInf(retention, 0) {
		return 0.0
	}
	return retention
}

// CalculateFactRetention evaluates the retention score of an existing Fact at a given reference time.
func CalculateFactRetention(f Fact, now time.Time, params DecayParams) float64 {
	tier := MemoryTier(f.Tier)
	if tier == "" {
		if f.Kind == "invariant" || f.Confidence == "invariant" || f.IsInvariant() || f.IsManualMemo() {
			tier = TierSemantic
		} else {
			tier = TierEpisodic
		}
	}

	ageDays := 0.0
	if f.FirstSeen != "" {
		if t, err := time.Parse(time.RFC3339, f.FirstSeen); err == nil {
			dur := now.Sub(t)
			if dur > 0 {
				ageDays = dur.Hours() / 24.0
			}
		}
	}

	daysSinceAccess := ageDays
	if f.LastAccessedAt != "" {
		if t, err := time.Parse(time.RFC3339, f.LastAccessedAt); err == nil {
			dur := now.Sub(t)
			if dur > 0 {
				daysSinceAccess = dur.Hours() / 24.0
			} else {
				daysSinceAccess = 0.0
			}
		}
	}

	distinct := f.DistinctTools
	if distinct < 1 {
		distinct = len(f.Tools)
		if distinct < 1 {
			distinct = 1
		}
	}

	return CalculateRetentionScore(tier, f.Salience, ageDays, daysSinceAccess, f.AccessCount, distinct, params)
}

// Access bump cooldown cache: (factID + "\x00" + tool) -> last bump timestamp
var (
	accessCooldownMap sync.Map
	cooldownDuration  = 60 * time.Second
)

// ResetAccessCooldownForTest clears the in-memory cooldown cache for testing.
func ResetAccessCooldownForTest() {
	accessCooldownMap = sync.Map{}
}

// BumpFactAccess throttles access bumps to at most once per 60 seconds per (factID, toolName).
// In normal operation, it updates the database asynchronously to guarantee non-blocking reads.
func (s *Store) BumpFactAccess(ctx context.Context, factID, toolName string) error {
	if s == nil || s.DB == nil || s.ReadOnly {
		return nil
	}

	factID = strings.TrimSpace(factID)
	if factID == "" {
		return nil
	}
	toolName = strings.ToLower(strings.TrimSpace(toolName))
	if toolName == "" {
		toolName = "default"
	}

	now := time.Now().UTC()
	cacheKey := factID + "\x00" + toolName
	if val, ok := accessCooldownMap.Load(cacheKey); ok {
		if last, ok := val.(time.Time); ok && now.Sub(last) < cooldownDuration {
			return nil // throttled
		}
	}
	accessCooldownMap.Store(cacheKey, now)

	// Execute write in non-blocking goroutine so read paths (Search/Prime) never block
	go func() {
		_ = s.bumpFactAccessTx(context.Background(), factID, toolName, now)
	}()

	return nil
}

// BumpFactAccessSync executes the access bump synchronously (used by unit tests and direct callers).
func (s *Store) BumpFactAccessSync(ctx context.Context, factID, toolName string) error {
	if s == nil || s.DB == nil || s.ReadOnly {
		return nil
	}
	factID = strings.TrimSpace(factID)
	if factID == "" {
		return nil
	}
	toolName = strings.ToLower(strings.TrimSpace(toolName))
	if toolName == "" {
		toolName = "default"
	}
	now := time.Now().UTC()
	cacheKey := factID + "\x00" + toolName
	accessCooldownMap.Store(cacheKey, now)
	return s.bumpFactAccessTx(ctx, factID, toolName, now)
}

func (s *Store) bumpFactAccessTx(ctx context.Context, factID, toolName string, now time.Time) error {
	factMu.Lock()
	defer factMu.Unlock()

	nowStr := now.Format(time.RFC3339)

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// 1. Upsert tool interaction in fact_tool_access
	_, err = tx.ExecContext(ctx, `
		INSERT INTO fact_tool_access (fact_id, tool, last_accessed_at, access_count)
		VALUES (?, ?, ?, 1)
		ON CONFLICT(fact_id, tool) DO UPDATE SET
			last_accessed_at = excluded.last_accessed_at,
			access_count = fact_tool_access.access_count + 1
	`, factID, toolName, nowStr)
	if err != nil {
		return err
	}

	// 2. Update numeric access telemetry on facts (FTS-exempt due to OF clause on trigger)
	res, err := tx.ExecContext(ctx, `
		UPDATE facts SET
			access_count = access_count + 1,
			last_accessed_at = ?,
			distinct_tools = (SELECT count(DISTINCT tool) FROM fact_tool_access WHERE fact_id = ?)
		WHERE id = ?
	`, nowStr, factID, factID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}

	return tx.Commit()
}

// RecordFactFeedback adjusts a fact's salience based on explicit feedback:
// - "helpful": +0.25 (clamped at 2.0)
// - "not_helpful": -0.25 (clamped at 0.25)
// - "stale" or "wrong": immediately sets salience to 0.25 (floor) and marks is_stale = 1
func (s *Store) RecordFactFeedback(ctx context.Context, factID, feedbackType string) error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("store is nil")
	}
	factID = strings.TrimSpace(factID)
	if factID == "" {
		return fmt.Errorf("fact id cannot be empty")
	}

	var currentSalience float64
	var currentStale int
	err := s.DB.QueryRowContext(ctx, `SELECT salience, is_stale FROM facts WHERE id = ?`, factID).Scan(&currentSalience, &currentStale)
	if err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("fact %q not found", factID)
		}
		return fmt.Errorf("fetch fact salience: %w", err)
	}

	feedbackType = strings.ToLower(strings.TrimSpace(feedbackType))
	newSalience := currentSalience
	newStale := currentStale

	switch feedbackType {
	case "helpful":
		newSalience = ClampSalience(currentSalience + 0.25)
	case "not_helpful", "nothelpful", "unhelpful":
		newSalience = ClampSalience(currentSalience - 0.25)
	case "stale", "wrong":
		newSalience = 0.25
		newStale = 1
	default:
		return fmt.Errorf("unknown feedback type %q (expected helpful, not_helpful, stale, wrong)", feedbackType)
	}

	_, err = s.DB.ExecContext(ctx, `UPDATE facts SET salience = ?, is_stale = ? WHERE id = ?`, newSalience, newStale, factID)
	if err != nil {
		return fmt.Errorf("update fact salience: %w", err)
	}
	return nil
}
