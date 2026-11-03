package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// HandoffStatus represents the lifecycle state of a tactical handoff baton.
type HandoffStatus string

const (
	HandoffPending    HandoffStatus = "pending"    // Open for claiming (synonym for legacy "open")
	HandoffClaimed    HandoffStatus = "claimed"    // Claimed by an agent session
	HandoffResolved   HandoffStatus = "resolved"   // Work completed
	HandoffSuperseded HandoffStatus = "superseded" // Replaced by newer handoff on same branch
	HandoffCancelled  HandoffStatus = "cancelled"  // Retracted by creator
)

var (
	// ErrHandoffAlreadyClaimed is returned when a handoff has already been claimed by another agent session.
	ErrHandoffAlreadyClaimed = errors.New("handoff has already been claimed by another agent session")
	// ErrHandoffNotFound is returned when a requested handoff does not exist.
	ErrHandoffNotFound = errors.New("handoff not found")
)

// Handoff represents an explicit tactical baton-passing record between agent sessions.
type Handoff struct {
	ID            string   `json:"id"`
	ProjectID     string   `json:"project_id"`
	Agent         string   `json:"agent"`
	Branch        string   `json:"branch"`
	Summary       string   `json:"summary"`
	OpenItems     []string `json:"open_items"`
	Decisions     []string `json:"decisions"`
	RepoFiles     []string `json:"repo_files"`
	FileRefsExist bool     `json:"file_refs_exist"`
	Stale         bool     `json:"stale,omitempty"`
	StaleReason   string   `json:"stale_reason,omitempty"`
	CreatedAt     string   `json:"created_at"` // RFC3339
	Status        string   `json:"status"`     // pending | claimed | resolved | superseded | cancelled
	ClaimedBy     string   `json:"claimed_by,omitempty"`
	ClaimedAt     string   `json:"claimed_at,omitempty"`
	SessionID     string   `json:"session_id,omitempty"`

	// Provenance metadata (Goal: agent-provenance-metadata-schema)
	ToolName     string `json:"tool_name,omitempty"`     // e.g. "antigravity", "cursor", "claude_code"
	ClientKind   string `json:"client_kind,omitempty"`   // e.g. "ai_agent", "human", "ci_job"
	OriginEntity string `json:"origin_entity,omitempty"` // e.g. "org:waqftech", "pat:personal", "system"
}

// IsPending returns true if the handoff is in an open/pending unconsumed state.
func (h Handoff) IsPending() bool {
	return h.Status == string(HandoffPending) || h.Status == "open"
}

// PackProvenance encodes provenance into Agent if not already packed.
func (h *Handoff) PackProvenance() {
	if h.ToolName != "" && h.Agent == "" {
		h.Agent = h.ToolName
	}
	if h.OriginEntity != "" || h.ClientKind != "" {
		tags := ""
		if h.ClientKind != "" {
			tags += " prov:client=" + h.ClientKind
		}
		if h.OriginEntity != "" {
			tags += " prov:origin=" + h.OriginEntity
		}
		if !strings.Contains(h.Agent, "prov:") {
			h.Agent = strings.TrimSpace(h.Agent + tags)
		}
	}
}

// UnpackProvenance extracts ToolName, ClientKind, OriginEntity from Agent.
func (h *Handoff) UnpackProvenance() {
	if h.Agent == "" {
		return
	}
	parts := strings.Fields(h.Agent)
	var cleanParts []string
	for _, p := range parts {
		if strings.HasPrefix(p, "prov:client=") {
			h.ClientKind = strings.TrimPrefix(p, "prov:client=")
		} else if strings.HasPrefix(p, "prov:origin=") {
			h.OriginEntity = strings.TrimPrefix(p, "prov:origin=")
		} else {
			cleanParts = append(cleanParts, p)
		}
	}
	if h.ToolName == "" && len(cleanParts) > 0 {
		h.ToolName = strings.Join(cleanParts, " ")
	}
}

// HandoffID generates a deterministic ID for a handoff record.
func HandoffID(projectID, summary, createdAt string) string {
	sum := sha256.Sum256([]byte(projectID + "\x00" + summary + "\x00" + createdAt))
	return "hoff_" + hex.EncodeToString(sum[:6])
}

// InsertHandoff inserts a new handoff record. If prior open handoffs exist for the same project,
// it marks them as 'superseded' to keep the latest handoff active.
func (s *Store) InsertHandoff(h Handoff) error {
	h.PackProvenance()
	if h.ID == "" {
		if h.CreatedAt == "" {
			h.CreatedAt = time.Now().UTC().Format(time.RFC3339)
		}
		h.ID = HandoffID(h.ProjectID, h.Summary, h.CreatedAt)
	}
	if h.Status == "" {
		h.Status = string(HandoffPending)
	}
	if h.Agent == "" {
		if h.ToolName != "" {
			h.Agent = h.ToolName
		} else {
			h.Agent = "agent"
		}
	}
	if h.CreatedAt == "" {
		h.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}

	openJSON, _ := json.Marshal(h.OpenItems)
	decJSON, _ := json.Marshal(h.Decisions)
	filesJSON, _ := json.Marshal(h.RepoFiles)
	refsExistInt := 0
	if h.FileRefsExist {
		refsExistInt = 1
	}

	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Supersede the previous open/pending handoff for this project *on the same branch*.
	// Handoff identity is branch-scoped: superseding project-wide erased the
	// active handoff of a parallel branch. A handoff with no branch supersedes
	// only the branchless open handoff, which keeps the rule deterministic.
	if h.IsPending() {
		if h.Agent == "human" || h.Agent == "user" {
			_, _ = tx.Exec(`UPDATE handoffs SET status='superseded'
				WHERE project_id=? AND (status='open' OR status='pending') AND (branch=? OR branch='' OR ?='')`,
				h.ProjectID, h.Branch, h.Branch)
		} else {
			_, _ = tx.Exec(`UPDATE handoffs SET status='superseded'
				WHERE project_id=? AND (status='open' OR status='pending') AND (branch=? OR branch='' OR ?='') AND agent != 'human' AND agent != 'user'`,
				h.ProjectID, h.Branch, h.Branch)
		}
	}

	_, err = tx.Exec(`
		INSERT INTO handoffs(id, project_id, agent, branch, summary, open_items, decisions, repo_files, file_refs_exist, created_at, status, claimed_by, claimed_at, session_id)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			agent=excluded.agent,
			branch=excluded.branch,
			summary=excluded.summary,
			open_items=excluded.open_items,
			decisions=excluded.decisions,
			repo_files=excluded.repo_files,
			file_refs_exist=excluded.file_refs_exist,
			status=excluded.status,
			claimed_by=excluded.claimed_by,
			claimed_at=excluded.claimed_at,
			session_id=excluded.session_id;
	`, h.ID, h.ProjectID, h.Agent, h.Branch, h.Summary, string(openJSON), string(decJSON), string(filesJSON), refsExistInt, h.CreatedAt, h.Status, h.ClaimedBy, h.ClaimedAt, h.SessionID)
	if err != nil {
		return fmt.Errorf("insert handoff: %w", err)
	}

	return tx.Commit()
}

// ClaimHandoff atomically claims a pending or open handoff via compare-and-set.
// If the handoff is already claimed by another session, it returns ErrHandoffAlreadyClaimed.
// If claimed by the exact same sessionID (when non-empty), it succeeds idempotently.
func (s *Store) ClaimHandoff(ctx context.Context, handoffID, claimingAgent, sessionID string) (*Handoff, error) {
	if handoffID == "" {
		return nil, errors.New("handoff ID is required")
	}
	if claimingAgent == "" {
		claimingAgent = "agent"
	}
	now := time.Now().UTC().Format(time.RFC3339)

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	// Atomic CAS
	res, err := tx.ExecContext(ctx, `
		UPDATE handoffs 
		SET status = 'claimed', 
		    claimed_by = ?, 
		    claimed_at = ?, 
		    session_id = ?
		WHERE id = ? AND (status = 'pending' OR status = 'open')
	`, claimingAgent, now, sessionID, handoffID)
	if err != nil {
		return nil, fmt.Errorf("claim handoff update: %w", err)
	}

	rowsAff, err := res.RowsAffected()
	if err != nil {
		return nil, err
	}

	if rowsAff == 0 {
		var existing Handoff
		var openJSON, decJSON, filesJSON string
		var refsExistInt int
		err := tx.QueryRowContext(ctx, `
			SELECT id, project_id, agent, branch, summary, open_items, decisions, repo_files, file_refs_exist, created_at, status, claimed_by, claimed_at, session_id
			FROM handoffs
			WHERE id = ?
		`, handoffID).Scan(
			&existing.ID, &existing.ProjectID, &existing.Agent, &existing.Branch, &existing.Summary,
			&openJSON, &decJSON, &filesJSON, &refsExistInt, &existing.CreatedAt, &existing.Status,
			&existing.ClaimedBy, &existing.ClaimedAt, &existing.SessionID,
		)
		if err == sql.ErrNoRows {
			return nil, ErrHandoffNotFound
		}
		if err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(openJSON), &existing.OpenItems)
		_ = json.Unmarshal([]byte(decJSON), &existing.Decisions)
		_ = json.Unmarshal([]byte(filesJSON), &existing.RepoFiles)
		existing.FileRefsExist = refsExistInt != 0

		// Idempotent retry: if claimed by the exact same sessionID, succeed
		if existing.Status == string(HandoffClaimed) && sessionID != "" && existing.SessionID == sessionID {
			return &existing, nil
		}
		return nil, ErrHandoffAlreadyClaimed
	}

	var claimed Handoff
	var openJSON, decJSON, filesJSON string
	var refsExistInt int
	err = tx.QueryRowContext(ctx, `
		SELECT id, project_id, agent, branch, summary, open_items, decisions, repo_files, file_refs_exist, created_at, status, claimed_by, claimed_at, session_id
		FROM handoffs
		WHERE id = ?
	`, handoffID).Scan(
		&claimed.ID, &claimed.ProjectID, &claimed.Agent, &claimed.Branch, &claimed.Summary,
		&openJSON, &decJSON, &filesJSON, &refsExistInt, &claimed.CreatedAt, &claimed.Status,
		&claimed.ClaimedBy, &claimed.ClaimedAt, &claimed.SessionID,
	)
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(openJSON), &claimed.OpenItems)
	_ = json.Unmarshal([]byte(decJSON), &claimed.Decisions)
	_ = json.Unmarshal([]byte(filesJSON), &claimed.RepoFiles)
	claimed.FileRefsExist = refsExistInt != 0

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &claimed, nil
}

// PopEligibleHandoff pops and atomically claims the newest eligible pending handoff
// matching project_id and branch (or branchless fallback). Older pending automated
// handoffs from the same CWD / branch are superseded within the same transaction.
func (s *Store) PopEligibleHandoff(ctx context.Context, projectID, branch, cwd, claimingAgent, sessionID string) (*Handoff, error) {
	if claimingAgent == "" {
		claimingAgent = "agent"
	}
	now := time.Now().UTC().Format(time.RFC3339)

	var matching []string
	if projectID != "" {
		m, err := s.MatchingProjectIDs(projectID)
		if err != nil {
			return nil, err
		}
		matching = m
	}
	if len(matching) == 0 && cwd != "" {
		p, err := s.ResolveScope("", cwd)
		if err == nil && p.ID != "" {
			matching = []string{p.ID}
		} else {
			matching = []string{ProjectID(cwd)}
		}
	}
	if len(matching) == 0 {
		return nil, nil
	}

	placeholders := strings.TrimRight(strings.Repeat("?,", len(matching)), ",")
	args := make([]any, len(matching))
	for i, id := range matching {
		args[i] = id
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	// Query newest pending handoff: prefer matching branch over branchless
	branchArgs := append(args, branch, branch)
	query := `
		SELECT id, project_id, agent, branch, summary, open_items, decisions, repo_files, file_refs_exist, created_at, status, claimed_by, claimed_at, session_id
		FROM handoffs
		WHERE project_id IN (` + placeholders + `) AND (status = 'pending' OR status = 'open') AND (branch = ? OR branch = '')
		ORDER BY (CASE WHEN branch = ? THEN 0 ELSE 1 END), created_at DESC
		LIMIT 1`

	var candidate Handoff
	var openJSON, decJSON, filesJSON string
	var refsExistInt int
	err = tx.QueryRowContext(ctx, query, branchArgs...).Scan(
		&candidate.ID, &candidate.ProjectID, &candidate.Agent, &candidate.Branch, &candidate.Summary,
		&openJSON, &decJSON, &filesJSON, &refsExistInt, &candidate.CreatedAt, &candidate.Status,
		&candidate.ClaimedBy, &candidate.ClaimedAt, &candidate.SessionID,
	)
	if err == sql.ErrNoRows {
		return nil, nil // No pending handoff
	}
	if err != nil {
		return nil, fmt.Errorf("pop handoff query: %w", err)
	}

	// Atomic CAS claim
	res, err := tx.ExecContext(ctx, `
		UPDATE handoffs 
		SET status = 'claimed', 
		    claimed_by = ?, 
		    claimed_at = ?, 
		    session_id = ?
		WHERE id = ? AND (status = 'pending' OR status = 'open')
	`, claimingAgent, now, sessionID, candidate.ID)
	if err != nil {
		return nil, fmt.Errorf("pop handoff update: %w", err)
	}
	rowsAff, err := res.RowsAffected()
	if err != nil {
		return nil, err
	}
	if rowsAff == 0 {
		return nil, ErrHandoffAlreadyClaimed
	}

	// Supersede older pending automated handoffs from the same project and branch
	_, _ = tx.ExecContext(ctx, `
		UPDATE handoffs 
		SET status = 'superseded'
		WHERE project_id = ? AND (status = 'pending' OR status = 'open')
		  AND id != ?
		  AND (agent IN ('auto', 'system', 'daemon', 'watcher') OR agent LIKE 'auto-%')
		  AND (branch = ? OR branch = '')
	`, candidate.ProjectID, candidate.ID, candidate.Branch)

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	_ = json.Unmarshal([]byte(openJSON), &candidate.OpenItems)
	_ = json.Unmarshal([]byte(decJSON), &candidate.Decisions)
	_ = json.Unmarshal([]byte(filesJSON), &candidate.RepoFiles)
	candidate.FileRefsExist = refsExistInt != 0
	candidate.Status = string(HandoffClaimed)
	candidate.ClaimedBy = claimingAgent
	candidate.ClaimedAt = now
	candidate.SessionID = sessionID

	return &candidate, nil
}

// ResolveHandoff marks a claimed handoff as resolved (work completed).
func (s *Store) ResolveHandoff(ctx context.Context, handoffID string) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE handoffs SET status = 'resolved' WHERE id = ?`, handoffID)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrHandoffNotFound
	}
	return nil
}

// CancelHandoff retracts an open or pending handoff.
func (s *Store) CancelHandoff(ctx context.Context, handoffID string) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE handoffs SET status = 'cancelled' WHERE id = ?`, handoffID)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrHandoffNotFound
	}
	return nil
}

// GetHandoff retrieves a single handoff by its ID.
func (s *Store) GetHandoff(ctx context.Context, handoffID string) (*Handoff, error) {
	var h Handoff
	var openJSON, decJSON, filesJSON string
	var refsExistInt int
	err := s.DB.QueryRowContext(ctx, `
		SELECT id, project_id, agent, branch, summary, open_items, decisions, repo_files, file_refs_exist, created_at, status, claimed_by, claimed_at, session_id
		FROM handoffs
		WHERE id = ?
	`, handoffID).Scan(
		&h.ID, &h.ProjectID, &h.Agent, &h.Branch, &h.Summary,
		&openJSON, &decJSON, &filesJSON, &refsExistInt, &h.CreatedAt, &h.Status,
		&h.ClaimedBy, &h.ClaimedAt, &h.SessionID,
	)
	if err == sql.ErrNoRows {
		return nil, ErrHandoffNotFound
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(openJSON), &h.OpenItems)
	_ = json.Unmarshal([]byte(decJSON), &h.Decisions)
	_ = json.Unmarshal([]byte(filesJSON), &h.RepoFiles)
	h.FileRefsExist = refsExistInt != 0
	return &h, nil
}

// ListHandoffs returns handoffs for a project optionally filtered by status (pending, claimed, resolved, superseded, cancelled, or all).
func (s *Store) ListHandoffs(ctx context.Context, projectSel, status string, limit int) ([]Handoff, error) {
	if limit <= 0 {
		limit = 50
	}
	matching, err := s.MatchingProjectIDs(projectSel)
	if err != nil {
		return nil, err
	}
	if projectSel != "" && len(matching) == 0 {
		return nil, nil
	}

	var where strings.Builder
	where.WriteString("WHERE 1=1")
	var args []any

	if len(matching) == 1 {
		where.WriteString(" AND project_id = ?")
		args = append(args, matching[0])
	} else if len(matching) > 1 {
		placeholders := strings.TrimRight(strings.Repeat("?,", len(matching)), ",")
		where.WriteString(" AND project_id IN (" + placeholders + ")")
		for _, id := range matching {
			args = append(args, id)
		}
	}

	if status != "" && status != "all" {
		if status == "pending" || status == "open" {
			where.WriteString(" AND (status = 'pending' OR status = 'open')")
		} else {
			where.WriteString(" AND status = ?")
			args = append(args, status)
		}
	}

	where.WriteString(" ORDER BY created_at DESC LIMIT ?")
	args = append(args, limit)

	query := `SELECT id, project_id, agent, branch, summary, open_items, decisions, repo_files, file_refs_exist, created_at, status, claimed_by, claimed_at, session_id
		FROM handoffs ` + where.String()

	return s.QueryRows(ctx, query, scanHandoff, args...)
}

// LatestHandoffForBranch returns the newest open or pending handoff for a project, preferring
// one recorded on the given branch. Pass an empty branch to ignore branching.
func (s *Store) LatestHandoffForBranch(projectSel, branch string) (*Handoff, error) {
	h, err := s.LatestHandoff(projectSel)
	if err != nil || branch == "" || h == nil || h.Branch == branch {
		return h, err
	}
	if onBranch, err := s.latestOpenHandoffOnBranch(projectSel, branch); err == nil && onBranch != nil {
		return onBranch, nil
	}
	return h, nil
}

func (s *Store) latestOpenHandoffOnBranch(projectSel, branch string) (*Handoff, error) {
	all, err := s.HandoffsByProject(projectSel, 0)
	if err != nil {
		return nil, err
	}
	for i := range all {
		if (all[i].Status == string(HandoffPending) || all[i].Status == "open") && all[i].Branch == branch {
			return &all[i], nil
		}
	}
	return nil, nil
}

func scanHandoff(rows *sql.Rows) (Handoff, error) {
	var h Handoff
	var openJSON, decJSON, filesJSON string
	var refsExistInt int
	if err := rows.Scan(
		&h.ID, &h.ProjectID, &h.Agent, &h.Branch, &h.Summary,
		&openJSON, &decJSON, &filesJSON, &refsExistInt, &h.CreatedAt, &h.Status,
		&h.ClaimedBy, &h.ClaimedAt, &h.SessionID,
	); err != nil {
		return h, err
	}
	_ = json.Unmarshal([]byte(openJSON), &h.OpenItems)
	_ = json.Unmarshal([]byte(decJSON), &h.Decisions)
	_ = json.Unmarshal([]byte(filesJSON), &h.RepoFiles)
	h.FileRefsExist = refsExistInt != 0
	h.UnpackProvenance()
	return h, nil
}

func scanHandoffRow(row *sql.Row) (Handoff, error) {
	var h Handoff
	var openJSON, decJSON, filesJSON string
	var refsExistInt int
	if err := row.Scan(
		&h.ID, &h.ProjectID, &h.Agent, &h.Branch, &h.Summary,
		&openJSON, &decJSON, &filesJSON, &refsExistInt, &h.CreatedAt, &h.Status,
		&h.ClaimedBy, &h.ClaimedAt, &h.SessionID,
	); err != nil {
		return h, err
	}
	_ = json.Unmarshal([]byte(openJSON), &h.OpenItems)
	_ = json.Unmarshal([]byte(decJSON), &h.Decisions)
	_ = json.Unmarshal([]byte(filesJSON), &h.RepoFiles)
	h.FileRefsExist = refsExistInt != 0
	h.UnpackProvenance()
	return h, nil
}

// LatestHandoff retrieves the most recent open or pending (or last created) handoff for a project.
func (s *Store) LatestHandoff(projectSel string) (*Handoff, error) {
	matching, err := s.MatchingProjectIDs(projectSel)
	if err != nil {
		return nil, err
	}
	if len(matching) == 0 {
		return nil, nil
	}

	placeholders := strings.TrimRight(strings.Repeat("?,", len(matching)), ",")
	args := make([]any, len(matching))
	for i, m := range matching {
		args[i] = m
	}

	// Try first to find the latest 'pending' or 'open' handoff
	query := `
		SELECT id, project_id, agent, branch, summary, open_items, decisions, repo_files, file_refs_exist, created_at, status, claimed_by, claimed_at, session_id
		FROM handoffs
		WHERE project_id IN (` + placeholders + `) AND (status='pending' OR status='open')
		ORDER BY created_at DESC
		LIMIT 1`

	h, found, err := s.QueryOne(context.Background(), query, scanHandoffRow, args...)
	if err != nil {
		return nil, err
	}
	if found {
		return &h, nil
	}

	// Fallback to most recent handoff regardless of status
	queryAll := `
		SELECT id, project_id, agent, branch, summary, open_items, decisions, repo_files, file_refs_exist, created_at, status, claimed_by, claimed_at, session_id
		FROM handoffs
		WHERE project_id IN (` + placeholders + `)
		ORDER BY created_at DESC
		LIMIT 1`
	h, found, err = s.QueryOne(context.Background(), queryAll, scanHandoffRow, args...)
	if err != nil {
		return nil, err
	}
	if found {
		return &h, nil
	}
	return nil, nil
}

// HandoffsByProject returns handoffs for a project ordered by recency.
func (s *Store) HandoffsByProject(projectSel string, limit int) ([]Handoff, error) {
	if limit <= 0 {
		limit = 20
	}
	matching, err := s.MatchingProjectIDs(projectSel)
	if err != nil {
		return nil, err
	}
	if len(matching) == 0 {
		return nil, nil
	}

	placeholders := strings.TrimRight(strings.Repeat("?,", len(matching)), ",")
	args := make([]any, len(matching)+1)
	for i, m := range matching {
		args[i] = m
	}
	args[len(matching)] = limit

	query := `
		SELECT id, project_id, agent, branch, summary, open_items, decisions, repo_files, file_refs_exist, created_at, status, claimed_by, claimed_at, session_id
		FROM handoffs
		WHERE project_id IN (` + placeholders + `)
		ORDER BY created_at DESC
		LIMIT ?`

	return s.QueryRows(context.Background(), query, scanHandoff, args...)
}

// SearchHandoffs returns handoffs whose summary, open items, or decisions match
// every whitespace-separated query term (case-insensitive substring match).
func (s *Store) SearchHandoffs(query, projectSel string, limit int) ([]Handoff, error) {
	terms := strings.Fields(strings.ToLower(query))
	if len(terms) == 0 {
		return nil, nil
	}
	matching, err := s.MatchingProjectIDs(projectSel)
	if err != nil {
		return nil, err
	}
	if projectSel != "" && len(matching) == 0 {
		return nil, nil
	}

	var where strings.Builder
	where.WriteString("WHERE 1=1")
	var args []any
	if len(matching) == 1 {
		where.WriteString(" AND project_id = ?")
		args = append(args, matching[0])
	} else if len(matching) > 1 {
		placeholders := strings.TrimRight(strings.Repeat("?,", len(matching)), ",")
		where.WriteString(" AND project_id IN (" + placeholders + ")")
		for _, id := range matching {
			args = append(args, id)
		}
	}
	for _, t := range terms {
		where.WriteString(" AND lower(summary || ' ' || open_items || ' ' || decisions) LIKE ?")
		args = append(args, "%"+t+"%")
	}
	if limit > 0 {
		where.WriteString(" ORDER BY created_at DESC LIMIT ?")
		args = append(args, limit)
	} else {
		where.WriteString(" ORDER BY created_at DESC")
	}

	return s.QueryRows(context.Background(), `SELECT id, project_id, agent, branch, summary, open_items, decisions, repo_files, file_refs_exist, created_at, status, claimed_by, claimed_at, session_id
		FROM handoffs `+where.String(), scanHandoff, args...)
}
