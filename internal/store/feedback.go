package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Feedback represents an agent observation, suggestion, issue, or tip.
type Feedback struct {
	ID             string `json:"id"`
	ProjectID      string `json:"project_id"`
	Agent          string `json:"agent"`
	Category       string `json:"category"` // "idea", "issue", "tip", "praise", "challenge"
	Content        string `json:"content"`
	Status         string `json:"status"`     // "open", "in_progress", "closed", "wontfix"
	CreatedAt      string `json:"created_at"` // RFC3339 UTC
	ResolvedAt     string `json:"resolved_at,omitempty"`
	ResolvedBy     string `json:"resolved_by,omitempty"`
	ResolvedGoal   string `json:"resolved_goal,omitempty"`
	ResolutionNote string `json:"resolution_note,omitempty"`
	FactID         string `json:"fact_id,omitempty"`
}

// MarshalJSON serializes empty ProjectID as null on both project_id and project
// while preserving string unmarshaling into Feedback.ProjectID.
func (f Feedback) MarshalJSON() ([]byte, error) {
	type Alias Feedback
	var proj *string
	if f.ProjectID != "" {
		p := f.ProjectID
		proj = &p
	}
	return json.Marshal(&struct {
		Alias
		ProjectID *string `json:"project_id"`
		Project   *string `json:"project"`
	}{
		Alias:     Alias(f),
		ProjectID: proj,
		Project:   proj,
	})
}

// FeedbackFilter configures query parameters for feedback retrieval.
type FeedbackFilter struct {
	ProjectID string
	Category  string
	Status    string
	Limit     int
}

// FeedbackID derives a deterministic epistemic grammar ID for feedback.
func FeedbackID(projectID, category, content string, ts time.Time) string {
	slug := DeriveFeedbackSlug(category, content)
	return SanitizeFeedbackID(slug)
}

// ensureUniqueFeedbackID checks if candidateID already exists in the feedback table.
// If it does, it appends a deterministic 3-character entropy suffix (e.g. -a7f)
// to guarantee uniqueness without breaking the mnemonic epistemic prefix.
func (s *Store) ensureUniqueFeedbackID(candidateID, projectID, content string, ts time.Time) string {
	var exists int
	err := s.DB.QueryRow(`SELECT 1 FROM feedback WHERE id = ?`, candidateID).Scan(&exists)
	if err != nil || exists == 0 {
		return candidateID
	}

	// Collision detected: derive 3-character entropy suffix
	h := sha256.Sum256([]byte(fmt.Sprintf("%s:%s:%s:%d", candidateID, projectID, content, ts.UnixNano())))
	hashStr := hex.EncodeToString(h[:])
	for i := 0; i+3 <= len(hashStr); i++ {
		attempt := fmt.Sprintf("%s-%s", candidateID, hashStr[i:i+3])
		var attemptExists int
		err := s.DB.QueryRow(`SELECT 1 FROM feedback WHERE id = ?`, attempt).Scan(&attemptExists)
		if err != nil || attemptExists == 0 {
			return attempt
		}
	}
	return fmt.Sprintf("%s-%x", candidateID, ts.UnixNano()%0xfff)
}

// SaveFeedback inserts a feedback record into the database.
func (s *Store) SaveFeedback(f *Feedback) error {
	if f == nil {
		return fmt.Errorf("feedback is nil")
	}
	if f.Content == "" {
		return fmt.Errorf("feedback content cannot be empty")
	}
	if f.Category == "" {
		f.Category = "idea"
	}
	f.Category = strings.ToLower(strings.TrimSpace(f.Category))

	if f.Status == "" {
		f.Status = "open"
	}
	f.Status = strings.ToLower(strings.TrimSpace(f.Status))

	now := time.Now().UTC()
	if f.CreatedAt == "" {
		f.CreatedAt = now.Format(time.RFC3339)
	}
	if f.ID == "" {
		baseID := FeedbackID(f.ProjectID, f.Category, f.Content, now)
		f.ID = s.ensureUniqueFeedbackID(baseID, f.ProjectID, f.Content, now)
	} else {
		cleanID := SanitizeFeedbackID(f.ID)
		f.ID = s.ensureUniqueFeedbackID(cleanID, f.ProjectID, f.Content, now)
	}

	err := WithRetry(context.Background(), func() error {
		_, execErr := s.DB.Exec(`
			INSERT INTO feedback (id, project_id, agent, category, content, status, created_at, resolved_at, resolved_by, resolved_goal, resolution_note)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, f.ID, f.ProjectID, f.Agent, f.Category, f.Content, f.Status, f.CreatedAt, f.ResolvedAt, f.ResolvedBy, f.ResolvedGoal, f.ResolutionNote)
		return execErr
	})
	if err != nil {
		return fmt.Errorf("save feedback: %w", err)
	}

	if f.FactID != "" {
		_ = s.RecordFactFeedback(context.Background(), f.FactID, f.Category)
	}
	return nil
}

// RecordFeedback records feedback and applies any associated fact salience adjustments.
func (s *Store) RecordFeedback(f *Feedback) error {
	return s.SaveFeedback(f)
}

func altFeedbackID(id string) string {
	if strings.HasPrefix(id, "fb_") {
		return "fb-" + id[3:]
	}
	if strings.HasPrefix(id, "fb-") {
		return "fb_" + id[3:]
	}
	return id
}

// GetFeedback retrieves a single feedback item by ID.
func (s *Store) GetFeedback(id string) (*Feedback, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("feedback id cannot be empty")
	}
	var f Feedback
	err := s.DB.QueryRow(`
		SELECT id, project_id, agent, category, content, status, created_at, resolved_at, resolved_by, resolved_goal, resolution_note
		FROM feedback
		WHERE id = ? OR id = ?
	`, id, altFeedbackID(id)).Scan(&f.ID, &f.ProjectID, &f.Agent, &f.Category, &f.Content, &f.Status, &f.CreatedAt, &f.ResolvedAt, &f.ResolvedBy, &f.ResolvedGoal, &f.ResolutionNote)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("feedback %q not found", id)
		}
		return nil, fmt.Errorf("get feedback: %w", err)
	}
	return &f, nil
}

// ResolveFeedback marks a feedback item as closed with attribution and resolution details.
func (s *Store) ResolveFeedback(id, agent, goal, note string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("feedback id cannot be empty")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.DB.Exec(`
		UPDATE feedback
		SET status = 'closed', resolved_at = ?, resolved_by = ?, resolved_goal = ?, resolution_note = ?
		WHERE id = ? OR id = ?
	`, now, agent, goal, note, id, altFeedbackID(id))
	if err != nil {
		return fmt.Errorf("resolve feedback: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("feedback %q not found", id)
	}
	return nil
}

// ReopenFeedback resets a closed or resolved feedback item back to open.
func (s *Store) ReopenFeedback(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("feedback id cannot be empty")
	}
	res, err := s.DB.Exec(`
		UPDATE feedback
		SET status = 'open', resolved_at = '', resolved_by = '', resolved_goal = '', resolution_note = ''
		WHERE id = ? OR id = ?
	`, id, altFeedbackID(id))
	if err != nil {
		return fmt.Errorf("reopen feedback: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("feedback %q not found", id)
	}
	return nil
}

// UpdateFeedbackNote updates the resolution note or external issue reference of a feedback item.
func (s *Store) UpdateFeedbackNote(id, note string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("feedback id cannot be empty")
	}
	res, err := s.DB.Exec(`
		UPDATE feedback
		SET resolution_note = ?
		WHERE id = ? OR id = ?
	`, note, id, altFeedbackID(id))
	if err != nil {
		return fmt.Errorf("update feedback note: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("feedback %q not found", id)
	}
	return nil
}

// QueryFeedback returns feedback records matching the provided filter criteria.
func (s *Store) QueryFeedback(filter FeedbackFilter) ([]Feedback, error) {
	if filter.Limit <= 0 {
		filter.Limit = 50
	}

	var conditions []string
	var args []any

	if filter.ProjectID != "" {
		if p, err := s.ResolveProject(filter.ProjectID); err == nil && p.ID != "" && p.ID != filter.ProjectID {
			conditions = append(conditions, "(project_id = ? OR project_id = ?)")
			args = append(args, filter.ProjectID, p.ID)
		} else {
			conditions = append(conditions, "project_id = ?")
			args = append(args, filter.ProjectID)
		}
	}
	if filter.Category != "" {
		conditions = append(conditions, "category = ?")
		args = append(args, strings.ToLower(filter.Category))
	}
	if filter.Status != "" {
		conditions = append(conditions, "status = ?")
		args = append(args, strings.ToLower(filter.Status))
	}

	query := "SELECT id, project_id, agent, category, content, status, created_at, resolved_at, resolved_by, resolved_goal, resolution_note FROM feedback"
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY created_at DESC LIMIT ?"
	args = append(args, filter.Limit)

	return s.QueryRows(context.Background(), query, func(rows *sql.Rows) (Feedback, error) {
		var f Feedback
		if err := rows.Scan(&f.ID, &f.ProjectID, &f.Agent, &f.Category, &f.Content, &f.Status, &f.CreatedAt, &f.ResolvedAt, &f.ResolvedBy, &f.ResolvedGoal, &f.ResolutionNote); err != nil {
			return f, fmt.Errorf("scan feedback: %w", err)
		}
		return f, nil
	}, args...)
}

// ListFeedback returns feedback records with optional filtering by project, category, and limit.
func (s *Store) ListFeedback(projectID, category string, limit int) ([]Feedback, error) {
	return s.QueryFeedback(FeedbackFilter{
		ProjectID: projectID,
		Category:  category,
		Limit:     limit,
	})
}

// FeedbackStats returns count of feedbacks by category.
func (s *Store) FeedbackStats() (map[string]int, error) {
	rows, err := s.DB.Query(`SELECT category, COUNT(*) FROM feedback GROUP BY category`)
	if err != nil {
		if err == sql.ErrNoRows {
			return make(map[string]int), nil
		}
		return nil, err
	}
	defer rows.Close()

	stats := make(map[string]int)
	for rows.Next() {
		var cat string
		var count int
		if err := rows.Scan(&cat, &count); err != nil {
			return nil, err
		}
		stats[cat] = count
	}
	return stats, rows.Err()
}

// FeedbackStatusStats returns count of feedbacks by status (open, closed, etc.).
func (s *Store) FeedbackStatusStats() (map[string]int, error) {
	rows, err := s.DB.Query(`SELECT status, COUNT(*) FROM feedback GROUP BY status`)
	if err != nil {
		if err == sql.ErrNoRows {
			return make(map[string]int), nil
		}
		return nil, err
	}
	defer rows.Close()

	stats := make(map[string]int)
	for rows.Next() {
		var st string
		var count int
		if err := rows.Scan(&st, &count); err != nil {
			return nil, err
		}
		stats[st] = count
	}
	return stats, rows.Err()
}
