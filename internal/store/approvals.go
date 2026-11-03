package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Approval represents an approval request created by an agent or system.
type Approval struct {
	ID             string            `json:"id"`
	Project        string            `json:"project"`
	Agent          string            `json:"agent"`
	Prompt         string            `json:"prompt"`
	RiskLevel      string            `json:"risk_level"` // "low", "medium", "high", "critical"
	Metadata       map[string]string `json:"metadata,omitempty"`
	TimeoutSeconds int               `json:"timeout_seconds"`
	Status         string            `json:"status"` // "pending", "approved", "rejected", "timed_out", "cancelled"
	DecidedBy      string            `json:"decided_by,omitempty"`
	DecisionReason string            `json:"decision_reason,omitempty"`
	CreatedAt      time.Time         `json:"created_at"`
	DecidedAt      time.Time         `json:"decided_at,omitempty"`
}

// InsertApproval persists a new approval request.
func (s *Store) InsertApproval(a *Approval) error {
	if a == nil {
		return fmt.Errorf("approval cannot be nil")
	}
	if strings.TrimSpace(a.ID) == "" {
		return fmt.Errorf("approval id is required")
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now()
	}
	if a.RiskLevel == "" {
		a.RiskLevel = "medium"
	}
	if a.Status == "" {
		a.Status = "pending"
	}
	if a.TimeoutSeconds <= 0 {
		a.TimeoutSeconds = 300
	}
	metaBytes, err := json.Marshal(a.Metadata)
	if err != nil {
		metaBytes = []byte("{}")
	}

	decidedAtStr := ""
	if !a.DecidedAt.IsZero() {
		decidedAtStr = a.DecidedAt.UTC().Format(time.RFC3339)
	}

	query := `INSERT INTO approvals (
		id, project, agent, prompt, risk_level, metadata, timeout_seconds, status, decided_by, decision_reason, created_at, decided_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	_, err = s.DB.Exec(query,
		a.ID,
		a.Project,
		a.Agent,
		a.Prompt,
		a.RiskLevel,
		string(metaBytes),
		a.TimeoutSeconds,
		a.Status,
		a.DecidedBy,
		a.DecisionReason,
		a.CreatedAt.UTC().Format(time.RFC3339),
		decidedAtStr,
	)
	return err
}

// GetApproval retrieves an approval by its identifier.
func (s *Store) GetApproval(id string) (*Approval, error) {
	row := s.DB.QueryRow(`SELECT id, project, agent, prompt, risk_level, metadata, timeout_seconds, status, decided_by, decision_reason, created_at, decided_at FROM approvals WHERE id = ?`, id)

	var a Approval
	var metaStr, createdStr, decidedStr string
	if err := row.Scan(
		&a.ID,
		&a.Project,
		&a.Agent,
		&a.Prompt,
		&a.RiskLevel,
		&metaStr,
		&a.TimeoutSeconds,
		&a.Status,
		&a.DecidedBy,
		&a.DecisionReason,
		&createdStr,
		&decidedStr,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	if metaStr != "" {
		_ = json.Unmarshal([]byte(metaStr), &a.Metadata)
	}
	if createdStr != "" {
		a.CreatedAt, _ = time.Parse(time.RFC3339, createdStr)
	}
	if decidedStr != "" {
		a.DecidedAt, _ = time.Parse(time.RFC3339, decidedStr)
	}

	return &a, nil
}

// UpdateApprovalDecision records a decision on an approval request.
func (s *Store) UpdateApprovalDecision(id string, status string, decidedBy string, reason string) error {
	nowStr := time.Now().UTC().Format(time.RFC3339)
	res, err := s.DB.Exec(`UPDATE approvals SET status = ?, decided_by = ?, decision_reason = ?, decided_at = ? WHERE id = ? AND status = 'pending'`,
		status, decidedBy, reason, nowStr, id)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		// Could be already decided or not found
		existing, err := s.GetApproval(id)
		if err != nil {
			return err
		}
		if existing == nil {
			return fmt.Errorf("approval %q not found", id)
		}
		return fmt.Errorf("approval %q is already %s", id, existing.Status)
	}
	return nil
}

// ListPendingApprovals lists all currently pending approvals, optionally filtered by project.
func (s *Store) ListPendingApprovals(project string) ([]Approval, error) {
	query := `SELECT id, project, agent, prompt, risk_level, metadata, timeout_seconds, status, decided_by, decision_reason, created_at, decided_at FROM approvals WHERE status = 'pending'`
	var args []any
	if project != "" {
		query += ` AND project = ?`
		args = append(args, project)
	}
	query += ` ORDER BY created_at DESC`

	rows, err := s.DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []Approval
	for rows.Next() {
		var a Approval
		var metaStr, createdStr, decidedStr string
		if err := rows.Scan(
			&a.ID,
			&a.Project,
			&a.Agent,
			&a.Prompt,
			&a.RiskLevel,
			&metaStr,
			&a.TimeoutSeconds,
			&a.Status,
			&a.DecidedBy,
			&a.DecisionReason,
			&createdStr,
			&decidedStr,
		); err != nil {
			return nil, err
		}
		if metaStr != "" {
			_ = json.Unmarshal([]byte(metaStr), &a.Metadata)
		}
		if createdStr != "" {
			a.CreatedAt, _ = time.Parse(time.RFC3339, createdStr)
		}
		if decidedStr != "" {
			a.DecidedAt, _ = time.Parse(time.RFC3339, decidedStr)
		}
		list = append(list, a)
	}
	return list, rows.Err()
}

// ListApprovals returns recent approvals up to limit, optionally filtered by project.
func (s *Store) ListApprovals(project string, limit int) ([]Approval, error) {
	if limit <= 0 {
		limit = 50
	}
	query := `SELECT id, project, agent, prompt, risk_level, metadata, timeout_seconds, status, decided_by, decision_reason, created_at, decided_at FROM approvals`
	var args []any
	if project != "" {
		query += ` WHERE project = ?`
		args = append(args, project)
	}
	query += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []Approval
	for rows.Next() {
		var a Approval
		var metaStr, createdStr, decidedStr string
		if err := rows.Scan(
			&a.ID,
			&a.Project,
			&a.Agent,
			&a.Prompt,
			&a.RiskLevel,
			&metaStr,
			&a.TimeoutSeconds,
			&a.Status,
			&a.DecidedBy,
			&a.DecisionReason,
			&createdStr,
			&decidedStr,
		); err != nil {
			return nil, err
		}
		if metaStr != "" {
			_ = json.Unmarshal([]byte(metaStr), &a.Metadata)
		}
		if createdStr != "" {
			a.CreatedAt, _ = time.Parse(time.RFC3339, createdStr)
		}
		if decidedStr != "" {
			a.DecidedAt, _ = time.Parse(time.RFC3339, decidedStr)
		}
		list = append(list, a)
	}
	return list, rows.Err()
}
