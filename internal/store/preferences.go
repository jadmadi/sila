package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// Preference represents an empirical or agentic pairwise preference record.
type Preference struct {
	ID              string `json:"id"`
	ProjectID       string `json:"project_id"`
	ContextQuery    string `json:"context_query"`
	CandidateA      string `json:"candidate_a"`
	CandidateB      string `json:"candidate_b"`
	Winner          string `json:"winner"` // "A", "B", or "TIE"
	Rationale       string `json:"rationale"`
	Agent           string `json:"agent"`
	DecisionAddress string `json:"decision_address,omitempty"`
	CreatedAt       string `json:"created_at"` // RFC3339 UTC
}

// PreferenceFilter configures query parameters for preferences retrieval.
type PreferenceFilter struct {
	ProjectID string
	Limit     int
}

// PreferenceID computes a deterministic unique ID for a preference entry.
func PreferenceID(projectID, contextQuery, candA, candB string, ts time.Time) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s:%s:%s:%s:%d", projectID, contextQuery, candA, candB, ts.UnixNano())))
	return "pref_" + hex.EncodeToString(h[:8])
}

// SavePreference inserts a preference record into SQLite.
func (s *Store) SavePreference(p *Preference) error {
	if p == nil {
		return fmt.Errorf("preference is nil")
	}
	if strings.TrimSpace(p.CandidateA) == "" || strings.TrimSpace(p.CandidateB) == "" {
		return fmt.Errorf("candidates A and B cannot be empty")
	}
	if p.Winner == "" {
		p.Winner = "A"
	}
	p.Winner = strings.ToUpper(strings.TrimSpace(p.Winner))
	if p.Winner != "A" && p.Winner != "B" && p.Winner != "TIE" {
		p.Winner = "A"
	}

	now := time.Now().UTC()
	if p.CreatedAt == "" {
		p.CreatedAt = now.Format(time.RFC3339)
	}
	if p.ID == "" {
		p.ID = PreferenceID(p.ProjectID, p.ContextQuery, p.CandidateA, p.CandidateB, now)
	}

	_, err := s.DB.Exec(`
		INSERT INTO preferences (id, project_id, context_query, candidate_a, candidate_b, winner, rationale, agent, decision_address, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, p.ID, p.ProjectID, p.ContextQuery, p.CandidateA, p.CandidateB, p.Winner, p.Rationale, p.Agent, p.DecisionAddress, p.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert preference: %w", err)
	}
	return nil
}

// Preferences retrieves stored preferences for a project.
func (s *Store) Preferences(filter PreferenceFilter) ([]Preference, error) {
	query := `SELECT id, project_id, context_query, candidate_a, candidate_b, winner, rationale, agent, decision_address, created_at FROM preferences WHERE 1=1`
	var args []any

	if filter.ProjectID != "" {
		query += ` AND (project_id = ? OR project_id = '')`
		args = append(args, filter.ProjectID)
	}

	query += ` ORDER BY created_at DESC`
	if filter.Limit > 0 {
		query += ` LIMIT ?`
		args = append(args, filter.Limit)
	} else {
		query += ` LIMIT 100`
	}

	rows, err := s.DB.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query preferences: %w", err)
	}
	defer rows.Close()

	var out []Preference
	for rows.Next() {
		var p Preference
		var cQuery, rat, ag, dAddr sql.NullString
		if err := rows.Scan(&p.ID, &p.ProjectID, &cQuery, &p.CandidateA, &p.CandidateB, &p.Winner, &rat, &ag, &dAddr, &p.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan preference: %w", err)
		}
		p.ContextQuery = cQuery.String
		p.Rationale = rat.String
		p.Agent = ag.String
		p.DecisionAddress = dAddr.String
		out = append(out, p)
	}
	return out, nil
}
