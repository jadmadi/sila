package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// DecisionOption represents a lettered trade-off option for an addressed decision.
type DecisionOption struct {
	Letter   string `json:"letter"`              // "A", "B", "C", etc.
	Title    string `json:"title"`               // Title or summary of option
	TradeOff string `json:"trade_off,omitempty"` // Trade-offs or consequences
}

// Decision represents an addressed epistemic decision record (D<number>).
type Decision struct {
	ID              string           `json:"id"`
	ProjectID       string           `json:"project_id"`
	Address         string           `json:"address"`    // "D1", "D2", etc.
	SeqNumber       int              `json:"seq_number"` // 1, 2, etc.
	Title           string           `json:"title"`
	Context         string           `json:"context,omitempty"`
	Options         []DecisionOption `json:"options"`
	Recommendation  string           `json:"recommendation"` // "A", "B", etc.
	Rationale       string           `json:"rationale,omitempty"`
	Status          string           `json:"status"`               // "open", "resolved", "superseded", "abandoned"
	Resolution      string           `json:"resolution,omitempty"` // Selected letter (e.g. "A") or resolution summary
	ResolutionNotes string           `json:"resolution_notes,omitempty"`
	UserQuote       string           `json:"user_quote,omitempty"`
	RouteNote       string           `json:"route_note,omitempty"`
	ResolvedBy      string           `json:"resolved_by,omitempty"`
	ResolvedAt      string           `json:"resolved_at,omitempty"`
	CreatedBy       string           `json:"created_by"`
	SessionID       string           `json:"session_id,omitempty"`
	GoalSlug        string           `json:"goal_slug,omitempty"`
	CreatedAt       string           `json:"created_at"`
	UpdatedAt       string           `json:"updated_at"`
}

// DecisionFilter specifies query parameters for retrieving decisions.
type DecisionFilter struct {
	ProjectID string
	Status    string // "open", "resolved", "superseded", "abandoned", "all"
	GoalSlug  string
	Limit     int
}

var addressPattern = regexp.MustCompile(`^(?:[dD])?(\d+)$`)

// ParseAddress parses a decision address like "D1", "d1", or "1" into a sequence number and canonical format "D<n>".
func ParseAddress(addr string) (int, string, error) {
	trimmed := strings.TrimSpace(addr)
	m := addressPattern.FindStringSubmatch(trimmed)
	if m == nil {
		return 0, "", fmt.Errorf("invalid decision address %q (expected format D<number>, e.g. D1)", addr)
	}
	seq, err := strconv.Atoi(m[1])
	if err != nil || seq <= 0 {
		return 0, "", fmt.Errorf("invalid sequence number in decision address %q", addr)
	}
	return seq, fmt.Sprintf("D%d", seq), nil
}

// CreateDecision inserts a new addressed decision with immutable sequence allocation.
func (s *Store) CreateDecision(d *Decision) (*Decision, error) {
	if d == nil {
		return nil, fmt.Errorf("decision is nil")
	}
	d.Title = strings.TrimSpace(d.Title)
	if d.Title == "" {
		return nil, fmt.Errorf("decision title cannot be empty")
	}
	if len(d.Options) < 2 {
		return nil, fmt.Errorf("every decision must define at least two lettered options (got %d)", len(d.Options))
	}

	seenLetters := make(map[string]bool)
	var letters []string
	for i := range d.Options {
		opt := &d.Options[i]
		opt.Letter = strings.ToUpper(strings.TrimSpace(opt.Letter))
		opt.Title = strings.TrimSpace(opt.Title)
		opt.TradeOff = strings.TrimSpace(opt.TradeOff)
		if len(opt.Letter) != 1 || opt.Letter[0] < 'A' || opt.Letter[0] > 'Z' {
			return nil, fmt.Errorf("invalid option letter %q: must be a single letter A-Z", opt.Letter)
		}
		if seenLetters[opt.Letter] {
			return nil, fmt.Errorf("duplicate option letter %q", opt.Letter)
		}
		if opt.Title == "" {
			return nil, fmt.Errorf("option %s must have a non-empty title", opt.Letter)
		}
		seenLetters[opt.Letter] = true
		letters = append(letters, opt.Letter)
	}

	d.Recommendation = strings.ToUpper(strings.TrimSpace(d.Recommendation))
	if d.Recommendation == "" {
		return nil, fmt.Errorf("every open decision must define an explicit recommendation")
	}
	if !seenLetters[d.Recommendation] {
		return nil, fmt.Errorf("recommendation %q must match one of the lettered options (%s)", d.Recommendation, strings.Join(letters, ", "))
	}

	if d.Status == "" {
		d.Status = "open"
	}
	d.Status = strings.ToLower(strings.TrimSpace(d.Status))
	switch d.Status {
	case "open", "deferred", "resolved", "superseded", "abandoned":
	default:
		return nil, fmt.Errorf("invalid decision status %q (allowed: open, deferred, resolved, superseded, abandoned)", d.Status)
	}

	optionsJSON, err := json.Marshal(d.Options)
	if err != nil {
		return nil, fmt.Errorf("marshal options: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	if d.CreatedAt == "" {
		d.CreatedAt = now
	}
	d.UpdatedAt = now

	tx, err := s.DB.Begin()
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Immutable address allocation
	var nextSeq int
	err = tx.QueryRow(`SELECT COALESCE(MAX(seq_number), 0) + 1 FROM decisions WHERE project_id = ?`, d.ProjectID).Scan(&nextSeq)
	if err != nil {
		return nil, fmt.Errorf("allocate next sequence number: %w", err)
	}

	if d.Address != "" || d.SeqNumber > 0 {
		var reqSeq int
		var reqAddr string
		if d.Address != "" {
			var pErr error
			reqSeq, reqAddr, pErr = ParseAddress(d.Address)
			if pErr != nil {
				return nil, pErr
			}
		} else {
			reqSeq = d.SeqNumber
			reqAddr = fmt.Sprintf("D%d", reqSeq)
		}

		// Check if requested address is already allocated (collision detection)
		var existingCount int
		_ = tx.QueryRow(`SELECT COUNT(*) FROM decisions WHERE project_id = ? AND (address = ? OR seq_number = ?)`, d.ProjectID, reqAddr, reqSeq).Scan(&existingCount)
		if existingCount > 0 {
			return nil, fmt.Errorf("decision address %s is already allocated (collision detected)", reqAddr)
		}
		d.SeqNumber = reqSeq
		d.Address = reqAddr
	} else {
		d.SeqNumber = nextSeq
		d.Address = fmt.Sprintf("D%d", nextSeq)
	}

	if d.ID == "" {
		d.ID = fmt.Sprintf("dec_%s_%s_%d", d.ProjectID, d.Address, time.Now().UTC().UnixNano())
	}

	_, err = tx.Exec(`
		INSERT INTO decisions (
			id, project_id, address, seq_number, title, context, options,
			recommendation, rationale, status, resolution, resolution_notes,
			resolved_by, resolved_at, created_by, session_id, goal_slug, user_quote, route_note,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		d.ID, d.ProjectID, d.Address, d.SeqNumber, d.Title, d.Context, string(optionsJSON),
		d.Recommendation, d.Rationale, d.Status, d.Resolution, d.ResolutionNotes,
		d.ResolvedBy, d.ResolvedAt, d.CreatedBy, d.SessionID, d.GoalSlug, d.UserQuote, d.RouteNote,
		d.CreatedAt, d.UpdatedAt,
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return nil, fmt.Errorf("decision address %s collision detected: %w", d.Address, err)
		}
		return nil, fmt.Errorf("insert decision: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit decision: %w", err)
	}

	return d, nil
}

// ResolveDecision records an in-place resolution for an open addressed decision.
func (s *Store) ResolveDecision(projectID, address, choice, notes, resolvedBy string) (*Decision, error) {
	return s.ResolveDecisionWithOptions(projectID, address, choice, notes, resolvedBy, "", "", false)
}

// ResolveDecisionWithOptions resolves an open or deferred decision, optionally
// requiring a verbatim user quote and recording a routing note.
func (s *Store) ResolveDecisionWithOptions(projectID, address, choice, notes, resolvedBy, userQuote, routeNote string, requireQuote bool) (*Decision, error) {
	_, canonicalAddr, err := ParseAddress(address)
	if err != nil {
		return nil, err
	}
	userQuote = strings.TrimSpace(userQuote)
	if requireQuote && userQuote == "" {
		return nil, fmt.Errorf("decision %s requires a verbatim user quote", canonicalAddr)
	}

	choice = strings.TrimSpace(choice)
	if choice == "" {
		return nil, fmt.Errorf("resolution choice cannot be empty")
	}

	tx, err := s.DB.Begin()
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	var d Decision
	var optionsRaw string
	err = tx.QueryRow(`
		SELECT id, project_id, address, seq_number, title, context, options,
		       recommendation, rationale, status, resolution, resolution_notes,
		       resolved_by, resolved_at, created_by, session_id, goal_slug, user_quote, route_note,
		       created_at, updated_at
		FROM decisions
		WHERE project_id = ? AND address = ?
	`, projectID, canonicalAddr).Scan(
		&d.ID, &d.ProjectID, &d.Address, &d.SeqNumber, &d.Title, &d.Context, &optionsRaw,
		&d.Recommendation, &d.Rationale, &d.Status, &d.Resolution, &d.ResolutionNotes,
		&d.ResolvedBy, &d.ResolvedAt, &d.CreatedBy, &d.SessionID, &d.GoalSlug, &d.UserQuote, &d.RouteNote,
		&d.CreatedAt, &d.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("decision %s not found in project %s", canonicalAddr, projectID)
	}
	if err != nil {
		return nil, fmt.Errorf("query decision: %w", err)
	}

	if d.Status != "open" && d.Status != "deferred" {
		return nil, fmt.Errorf("decision %s is already %s (resolved at %s by %s)", canonicalAddr, d.Status, d.ResolvedAt, d.ResolvedBy)
	}

	_ = json.Unmarshal([]byte(optionsRaw), &d.Options)

	// Validate choice: if single letter, normalize to uppercase
	upperChoice := strings.ToUpper(choice)
	for _, opt := range d.Options {
		if opt.Letter == upperChoice {
			choice = upperChoice
			break
		}
	}

	now := time.Now().UTC().Format(time.RFC3339)
	d.Status = "resolved"
	d.Resolution = choice
	d.ResolutionNotes = strings.TrimSpace(notes)
	d.ResolvedBy = strings.TrimSpace(resolvedBy)
	d.ResolvedAt = now
	d.UserQuote = userQuote
	d.RouteNote = strings.TrimSpace(routeNote)
	d.UpdatedAt = now

	_, err = tx.Exec(`
		UPDATE decisions
		SET status = ?, resolution = ?, resolution_notes = ?, resolved_by = ?, resolved_at = ?,
		    user_quote = ?, route_note = ?, updated_at = ?
		WHERE project_id = ? AND address = ?
	`, d.Status, d.Resolution, d.ResolutionNotes, d.ResolvedBy, d.ResolvedAt,
		d.UserQuote, d.RouteNote, d.UpdatedAt, projectID, canonicalAddr)
	if err != nil {
		return nil, fmt.Errorf("update decision: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit decision resolution: %w", err)
	}

	return &d, nil
}

// DeferDecision parks an open decision without resolving it.
func (s *Store) DeferDecision(projectID, address, notes, deferredBy string) (*Decision, error) {
	_, canonicalAddr, err := ParseAddress(address)
	if err != nil {
		return nil, err
	}
	d, err := s.GetDecision(projectID, canonicalAddr)
	if err != nil {
		return nil, err
	}
	if d.Status != "open" && d.Status != "deferred" {
		return nil, fmt.Errorf("decision %s is already %s", canonicalAddr, d.Status)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.DB.Exec(`
		UPDATE decisions
		SET status = 'deferred', resolution = '', resolution_notes = ?, resolved_by = ?,
		    resolved_at = '', updated_at = ?
		WHERE project_id = ? AND address = ? AND status IN ('open', 'deferred')
	`, strings.TrimSpace(notes), strings.TrimSpace(deferredBy), now, projectID, canonicalAddr)
	if err != nil {
		return nil, fmt.Errorf("defer decision: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, fmt.Errorf("decision %s changed concurrently", canonicalAddr)
	}
	return s.GetDecision(projectID, canonicalAddr)
}

// GetDecision retrieves a decision by its address (e.g. "D1") or unique ID.
func (s *Store) GetDecision(projectID, addressOrID string) (*Decision, error) {
	trimmed := strings.TrimSpace(addressOrID)
	var query string
	var arg string
	if _, canonical, err := ParseAddress(trimmed); err == nil {
		query = `SELECT id, project_id, address, seq_number, title, context, options,
		       recommendation, rationale, status, resolution, resolution_notes,
		       resolved_by, resolved_at, created_by, session_id, goal_slug, user_quote, route_note,
		       created_at, updated_at
		FROM decisions WHERE project_id = ? AND address = ?`
		arg = canonical
	} else {
		query = `SELECT id, project_id, address, seq_number, title, context, options,
		       recommendation, rationale, status, resolution, resolution_notes,
		       resolved_by, resolved_at, created_by, session_id, goal_slug, user_quote, route_note,
		       created_at, updated_at
		FROM decisions WHERE project_id = ? AND id = ?`
		arg = trimmed
	}

	var d Decision
	var optionsRaw string
	err := s.DB.QueryRow(query, projectID, arg).Scan(
		&d.ID, &d.ProjectID, &d.Address, &d.SeqNumber, &d.Title, &d.Context, &optionsRaw,
		&d.Recommendation, &d.Rationale, &d.Status, &d.Resolution, &d.ResolutionNotes,
		&d.ResolvedBy, &d.ResolvedAt, &d.CreatedBy, &d.SessionID, &d.GoalSlug, &d.UserQuote, &d.RouteNote,
		&d.CreatedAt, &d.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("decision %q not found in project %s", addressOrID, projectID)
	}
	if err != nil {
		return nil, fmt.Errorf("query decision: %w", err)
	}
	_ = json.Unmarshal([]byte(optionsRaw), &d.Options)
	return &d, nil
}

// ListDecisions retrieves decisions matching the specified filter, ordered by sequence number ascending.
func (s *Store) ListDecisions(filter DecisionFilter) ([]*Decision, error) {
	query := `SELECT id, project_id, address, seq_number, title, context, options,
	                 recommendation, rationale, status, resolution, resolution_notes,
	                 resolved_by, resolved_at, created_by, session_id, goal_slug, user_quote, route_note,
	                 created_at, updated_at
	          FROM decisions WHERE 1=1`
	var args []any
	if filter.ProjectID != "" {
		query += " AND project_id = ?"
		args = append(args, filter.ProjectID)
	}
	if filter.Status != "" && filter.Status != "all" {
		query += " AND status = ?"
		args = append(args, strings.ToLower(filter.Status))
	}
	if filter.GoalSlug != "" {
		query += " AND goal_slug = ?"
		args = append(args, filter.GoalSlug)
	}
	query += " ORDER BY seq_number ASC"
	if filter.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", filter.Limit)
	}

	rows, err := s.DB.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query decisions: %w", err)
	}
	defer rows.Close()

	var out []*Decision
	for rows.Next() {
		var d Decision
		var optionsRaw string
		err := rows.Scan(
			&d.ID, &d.ProjectID, &d.Address, &d.SeqNumber, &d.Title, &d.Context, &optionsRaw,
			&d.Recommendation, &d.Rationale, &d.Status, &d.Resolution, &d.ResolutionNotes,
			&d.ResolvedBy, &d.ResolvedAt, &d.CreatedBy, &d.SessionID, &d.GoalSlug, &d.UserQuote, &d.RouteNote,
			&d.CreatedAt, &d.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan decision: %w", err)
		}
		_ = json.Unmarshal([]byte(optionsRaw), &d.Options)
		out = append(out, &d)
	}
	return out, rows.Err()
}

// CheckDecisionCollisions inspects all decisions for a project, verifying address uniqueness, formatting, and invariants.
func (s *Store) CheckDecisionCollisions(projectID string) ([]string, error) {
	decisions, err := s.ListDecisions(DecisionFilter{ProjectID: projectID})
	if err != nil {
		return nil, err
	}

	var anomalies []string
	seenAddr := make(map[string]string) // address -> id
	seenSeq := make(map[int]string)     // seq -> id

	for _, d := range decisions {
		// 1. Address format check
		seq, canonical, err := ParseAddress(d.Address)
		if err != nil {
			anomalies = append(anomalies, fmt.Sprintf("decision %s has invalid address format %q: %v", d.ID, d.Address, err))
			continue
		}
		if d.Address != canonical {
			anomalies = append(anomalies, fmt.Sprintf("decision %s address %q is not canonical (expected %s)", d.ID, d.Address, canonical))
		}
		if d.SeqNumber != seq {
			anomalies = append(anomalies, fmt.Sprintf("decision %s address %s mismatches seq_number %d", d.ID, d.Address, d.SeqNumber))
		}

		// 2. Collision detection
		if prevID, exists := seenAddr[d.Address]; exists {
			anomalies = append(anomalies, fmt.Sprintf("address collision on %s between decisions %s and %s", d.Address, prevID, d.ID))
		}
		seenAddr[d.Address] = d.ID

		if prevID, exists := seenSeq[d.SeqNumber]; exists {
			anomalies = append(anomalies, fmt.Sprintf("seq_number collision on %d between decisions %s and %s", d.SeqNumber, prevID, d.ID))
		}
		seenSeq[d.SeqNumber] = d.ID

		// 3. Option & Recommendation invariants
		if len(d.Options) < 2 {
			anomalies = append(anomalies, fmt.Sprintf("decision %s has %d options (must define at least 2)", d.Address, len(d.Options)))
		}
		hasRec := false
		for _, opt := range d.Options {
			if opt.Letter == d.Recommendation {
				hasRec = true
				break
			}
		}
		if !hasRec {
			anomalies = append(anomalies, fmt.Sprintf("decision %s recommendation %q not found in options", d.Address, d.Recommendation))
		}

		// 4. Resolution invariant
		if d.Status == "resolved" && d.Resolution == "" {
			anomalies = append(anomalies, fmt.Sprintf("decision %s is marked resolved but missing resolution", d.Address))
		}
		if d.Status == "deferred" && d.Resolution != "" {
			anomalies = append(anomalies, fmt.Sprintf("decision %s is deferred but has resolution %q", d.Address, d.Resolution))
		}
	}

	return anomalies, nil
}

func init() {
	migrations = append(migrations, migration{
		version: 54,
		name:    "decision_verbatim_quotes_and_route_notes",
		up: func(tx *sql.Tx) error {
			return ensureDecisionQuoteColumns(tx)
		},
	})
}

// ensureDecisionQuoteColumns adds the verbatim quote and routing note columns.
// The pragma guard keeps the migration idempotent for out-of-order databases.
func ensureDecisionQuoteColumns(tx *sql.Tx) error {
	columns := []struct{ name, ddl string }{
		{"user_quote", `ALTER TABLE decisions ADD COLUMN user_quote TEXT NOT NULL DEFAULT ''`},
		{"route_note", `ALTER TABLE decisions ADD COLUMN route_note TEXT NOT NULL DEFAULT ''`},
	}
	for _, column := range columns {
		var count int
		if err := tx.QueryRow(fmt.Sprintf("SELECT count(*) FROM pragma_table_info('decisions') WHERE name='%s'", column.name)).Scan(&count); err != nil {
			return err
		}
		if count == 0 {
			if _, err := tx.Exec(column.ddl); err != nil {
				return fmt.Errorf("migration 54 add %s: %w", column.name, err)
			}
		}
	}
	return nil
}
