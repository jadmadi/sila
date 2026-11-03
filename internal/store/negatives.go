package store

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// NegativeSignal represents an unverified or silenced architectural invariant rule.
type NegativeSignal struct {
	RuleID             string  `json:"rule_id"`
	RuleText           string  `json:"rule_text"`
	RuleKind           string  `json:"rule_kind"`
	LastAssertedAt     string  `json:"last_asserted_at,omitempty"`
	DaysSinceAssertion float64 `json:"days_since_assertion"`
	TotalAssertions    int     `json:"total_assertions"`
	SuspicionScore     float64 `json:"suspicion_score"` // 0.0 to 1.0+ (Phi or normalized decay)
	Status             string  `json:"status"`          // "silenced", "decaying", "never_asserted"
}

// DetectNegativeSignals queries for architectural invariants and semantic rules that have not
// received verification assertions or heartbeats within maxSilenceAge.
func (s *Store) DetectNegativeSignals(ctx context.Context, projectID string, maxSilenceAge time.Duration) ([]NegativeSignal, error) {
	if maxSilenceAge <= 0 {
		maxSilenceAge = 7 * 24 * time.Hour // default 7 days lookback
	}

	cutoffTime := time.Now().UTC().Add(-maxSilenceAge).Format(time.RFC3339)
	now := time.Now().UTC()

	proj, _ := s.ResolveProject(projectID)
	projPath := proj.Path

	// Fetch all pass heartbeats for this project and verify evidence grounding on disk
	hbRows, err := s.DB.QueryContext(ctx, `
		SELECT invariant_rule, evidence_type, evidence_ref, created_at
		FROM epistemic_heartbeats
		WHERE project_id = ? AND status = 'pass' AND evidence_ref != ''
	`, projectID)
	if err != nil {
		return nil, fmt.Errorf("detect negative signals (query heartbeats): %w", err)
	}
	defer hbRows.Close()

	type ruleGrounding struct {
		lastHeartbeat string
		count         int
	}
	ruleMap := make(map[string]*ruleGrounding)

	for hbRows.Next() {
		var invRule, evType, evRef, createdAt string
		if err := hbRows.Scan(&invRule, &evType, &evRef, &createdAt); err != nil {
			return nil, err
		}
		if evType == "file" {
			exists := false
			if _, err := os.Stat(evRef); err == nil {
				exists = true
			} else if projPath != "" {
				if _, err := os.Stat(filepath.Join(projPath, evRef)); err == nil {
					exists = true
				}
			}
			if !exists {
				// Ungrounded evidence: does not clear invariant warnings
				continue
			}
		}
		rg, ok := ruleMap[invRule]
		if !ok {
			rg = &ruleGrounding{}
			ruleMap[invRule] = rg
		}
		rg.count++
		if createdAt > rg.lastHeartbeat {
			rg.lastHeartbeat = createdAt
		}
	}

	query := `
		SELECT f.id, f.claim, COALESCE(f.kind, 'invariant'), f.first_seen,
		       COALESCE(MAX(a.created_at), '') as last_assertion,
		       COUNT(a.id) as assertion_count
		FROM facts f
		LEFT JOIN assertions a
		  ON f.project_id = a.project_id
		 AND f.id = a.statement_id
		WHERE f.project_id = ?
		  AND (f.kind = 'invariant' OR f.tier = 'semantic' OR f.confidence = 'invariant' OR f.claim LIKE 'Decision:%' OR f.claim LIKE 'Rule:%')
		GROUP BY f.id
	`

	rows, err := s.DB.QueryContext(ctx, query, projectID)
	if err != nil {
		return nil, fmt.Errorf("detect negative signals: %w", err)
	}
	defer rows.Close()

	var signals []NegativeSignal

	for rows.Next() {
		var (
			ruleID         string
			claim          string
			kind           string
			firstSeen      string
			lastAssertion  string
			assertionCount int
		)

		if err := rows.Scan(&ruleID, &claim, &kind, &firstSeen, &lastAssertion, &assertionCount); err != nil {
			return nil, err
		}

		lastHeartbeat := ""
		heartbeatCount := 0
		for rule, rg := range ruleMap {
			if rule == claim || strings.Contains(rule, ruleID) {
				heartbeatCount += rg.count
				if rg.lastHeartbeat > lastHeartbeat {
					lastHeartbeat = rg.lastHeartbeat
				}
			}
		}

		lastActivity := lastHeartbeat
		if lastAssertion > lastActivity {
			lastActivity = lastAssertion
		}
		totalCount := heartbeatCount + assertionCount

		var daysSince float64
		var status string
		var suspicion float64

		if lastActivity == "" {
			// Rule was never asserted since registration
			status = "never_asserted"
			firstSeenTime, err := time.Parse(time.RFC3339, firstSeen)
			if err != nil {
				daysSince = 14.0
			} else {
				daysSince = now.Sub(firstSeenTime).Hours() / 24.0
			}
			suspicion = math.Min(1.0, 0.5+0.05*daysSince)
		} else {
			lastTime, err := time.Parse(time.RFC3339, lastActivity)
			if err != nil {
				lastTime = now.Add(-maxSilenceAge)
			}
			daysSince = now.Sub(lastTime).Hours() / 24.0

			if lastActivity < cutoffTime {
				status = "silenced"
				elapsedPastCutoff := daysSince - (maxSilenceAge.Hours() / 24.0)
				suspicion = math.Min(1.0, 0.4+0.1*math.Max(0, elapsedPastCutoff))
			} else if daysSince > (maxSilenceAge.Hours()/24.0)*0.7 {
				status = "decaying"
				suspicion = 0.3 + 0.1*(daysSince/(maxSilenceAge.Hours()/24.0))
			} else {
				// Clean: active recently
				continue
			}
		}

		signals = append(signals, NegativeSignal{
			RuleID:             ruleID,
			RuleText:           claim,
			RuleKind:           kind,
			LastAssertedAt:     lastActivity,
			DaysSinceAssertion: math.Round(daysSince*10) / 10,
			TotalAssertions:    totalCount,
			SuspicionScore:     math.Round(suspicion*100) / 100,
			Status:             status,
		})
	}

	return signals, rows.Err()
}

// QueryNegativeSignals is an alias for DetectNegativeSignals providing compatibility
// with epistemic invariant auditing across harnesses.
func (s *Store) QueryNegativeSignals(ctx context.Context, projectID string, maxSilenceAge time.Duration) ([]NegativeSignal, error) {
	return s.DetectNegativeSignals(ctx, projectID, maxSilenceAge)
}
