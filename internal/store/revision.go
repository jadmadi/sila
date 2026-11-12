package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"
)

// ErrRevisionConflict reports a write whose expected revision is stale.
var ErrRevisionConflict = errors.New("revision conflict")

// RevisionConflictError carries the current revision and its owner.
type RevisionConflictError struct {
	CurrentRevision string
	OwnerAgent      string
}

func (e *RevisionConflictError) Error() string {
	return fmt.Sprintf("revision conflict: current %s owned by %s", e.CurrentRevision, e.OwnerAgent)
}

func (e *RevisionConflictError) Unwrap() error { return ErrRevisionConflict }

func revisionHash(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func sortedCopy(values []string) []string {
	out := append([]string{}, values...)
	sort.Strings(out)
	return out
}

// HandoffRevision hashes the canonical handoff projection. Timestamps, session
// references, claims, and derived flags are excluded.
func HandoffRevision(h Handoff) string {
	projection := struct {
		ProjectID string   `json:"project_id"`
		Branch    string   `json:"branch"`
		Summary   string   `json:"summary"`
		OpenItems []string `json:"open_items"`
		Decisions []string `json:"decisions"`
		RepoFiles []string `json:"repo_files"`
		Status    string   `json:"status"`
	}{
		ProjectID: h.ProjectID,
		Branch:    h.Branch,
		Summary:   h.Summary,
		OpenItems: sortedCopy(h.OpenItems),
		Decisions: sortedCopy(h.Decisions),
		RepoFiles: sortedCopy(h.RepoFiles),
		Status:    h.Status,
	}
	return revisionHash(projection)
}

// MemoryRecordRevision hashes the canonical envelope projection: title,
// summary, kind, assertions, and evidence. Provenance and timestamps are out.
func MemoryRecordRevision(r MemoryRecord) (string, error) {
	env := r.Envelope
	if env == nil {
		if r.EnvelopeJSON == "" {
			return "", fmt.Errorf("memory record %s has no envelope", r.ID)
		}
		var parsed MemoryEnvelope
		if err := json.Unmarshal([]byte(r.EnvelopeJSON), &parsed); err != nil {
			return "", err
		}
		env = &parsed
	}
	type assertionProjection struct {
		ID           string   `json:"id"`
		Type         string   `json:"type"`
		Statement    string   `json:"statement"`
		EvidenceRefs []string `json:"evidence_refs"`
	}
	type evidenceProjection struct {
		ID      string `json:"id"`
		Kind    string `json:"kind"`
		Path    string `json:"path"`
		Summary string `json:"summary"`
	}
	assertions := make([]assertionProjection, 0, len(env.Assertions))
	for _, assertion := range env.Assertions {
		assertions = append(assertions, assertionProjection{
			ID: assertion.ID, Type: assertion.AssertionType, Statement: assertion.Statement,
			EvidenceRefs: sortedCopy(assertion.EvidenceRefs),
		})
	}
	sort.Slice(assertions, func(i, j int) bool { return assertions[i].ID < assertions[j].ID })
	evidence := make([]evidenceProjection, 0, len(env.Evidence))
	for _, item := range env.Evidence {
		evidence = append(evidence, evidenceProjection{ID: item.ID, Kind: item.Kind, Path: item.Locator.Path, Summary: item.Summary})
	}
	sort.Slice(evidence, func(i, j int) bool { return evidence[i].ID < evidence[j].ID })
	projection := struct {
		SchemaVersion string                `json:"schema_version"`
		Kind          string                `json:"kind"`
		Title         string                `json:"title"`
		Summary       string                `json:"summary"`
		State         string                `json:"state"`
		Assertions    []assertionProjection `json:"assertions"`
		Evidence      []evidenceProjection  `json:"evidence"`
	}{
		SchemaVersion: env.SchemaVersion, Kind: env.Kind, Title: env.Title, Summary: env.Summary,
		State: string(MemoryState(r.State)), Assertions: assertions, Evidence: evidence,
	}
	return revisionHash(projection), nil
}

// SaveHandoffChecked writes a handoff only when expected matches the current
// revision (or is empty) inside one BEGIN IMMEDIATE transaction. A replay of the
// same content reports replayed without writing.
func (s *Store) SaveHandoffChecked(ctx context.Context, h Handoff, expected string) (string, bool, error) {
	normalized, err := normalizeHandoffForWrite(h)
	if err != nil {
		return "", false, err
	}
	conn, err := s.DB.Conn(ctx)
	if err != nil {
		return "", false, err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return "", false, err
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()

	existing, err := handoffByID(ctx, conn, normalized.ID)
	if err != nil {
		return "", false, err
	}
	if existing == nil {
		if expected != "" {
			return "", false, ErrHandoffNotFound
		}
	} else {
		current := HandoffRevision(*existing)
		if expected != "" && expected != current {
			return "", false, &RevisionConflictError{CurrentRevision: current, OwnerAgent: existing.Agent}
		}
		if expected != "" && HandoffRevision(normalized) == current {
			return current, true, nil
		}
	}
	openJSON, _ := json.Marshal(normalized.OpenItems)
	decJSON, _ := json.Marshal(normalized.Decisions)
	filesJSON, _ := json.Marshal(normalized.RepoFiles)
	refsExist := 0
	if normalized.FileRefsExist {
		refsExist = 1
	}
	if _, err := conn.ExecContext(ctx, `
		INSERT INTO handoffs(id, project_id, agent, branch, summary, open_items, decisions, repo_files, file_refs_exist, created_at, status, claimed_by, claimed_at, session_id)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			agent=excluded.agent, branch=excluded.branch, summary=excluded.summary,
			open_items=excluded.open_items, decisions=excluded.decisions,
			repo_files=excluded.repo_files, file_refs_exist=excluded.file_refs_exist,
			status=excluded.status, claimed_by=excluded.claimed_by,
			claimed_at=excluded.claimed_at, session_id=excluded.session_id`,
		normalized.ID, normalized.ProjectID, normalized.Agent, normalized.Branch, normalized.Summary,
		string(openJSON), string(decJSON), string(filesJSON), refsExist, normalized.CreatedAt,
		normalized.Status, normalized.ClaimedBy, normalized.ClaimedAt, normalized.SessionID); err != nil {
		return "", false, err
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return "", false, err
	}
	committed = true
	return HandoffRevision(normalized), false, nil
}

func normalizeHandoffForWrite(h Handoff) (Handoff, error) {
	if h.ProjectID == "" {
		return h, fmt.Errorf("handoff requires a project")
	}
	if h.CreatedAt == "" {
		h.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	if h.ID == "" {
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
	return h, nil
}

// handoffByID reads one handoff on an existing connection.
func handoffByID(ctx context.Context, conn *sql.Conn, id string) (*Handoff, error) {
	var h Handoff
	var openJSON, decJSON, filesJSON, created string
	var refsExist int
	var claimedBy, claimedAt, sessionID sql.NullString
	err := conn.QueryRowContext(ctx, `SELECT id, project_id, agent, branch, summary, open_items, decisions, repo_files,
		file_refs_exist, created_at, status, claimed_by, claimed_at, session_id FROM handoffs WHERE id = ?`, id).Scan(
		&h.ID, &h.ProjectID, &h.Agent, &h.Branch, &h.Summary, &openJSON, &decJSON, &filesJSON,
		&refsExist, &created, &h.Status, &claimedBy, &claimedAt, &sessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	h.FileRefsExist = refsExist == 1
	h.CreatedAt = created
	h.ClaimedBy, h.ClaimedAt, h.SessionID = claimedBy.String, claimedAt.String, sessionID.String
	_ = json.Unmarshal([]byte(openJSON), &h.OpenItems)
	_ = json.Unmarshal([]byte(decJSON), &h.Decisions)
	_ = json.Unmarshal([]byte(filesJSON), &h.RepoFiles)
	return &h, nil
}

// MemoryRevision returns the canonical revision and owner agent for one record.
func (s *Store) MemoryRevision(ctx context.Context, memoryID string) (string, string, bool, error) {
	var envelopeJSON, state string
	err := s.DB.QueryRowContext(ctx, `SELECT envelope_json, state FROM memory_records WHERE id = ?`, memoryID).Scan(&envelopeJSON, &state)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, err
	}
	var env MemoryEnvelope
	if err := json.Unmarshal([]byte(envelopeJSON), &env); err != nil {
		return "", "", false, err
	}
	revision, revErr := MemoryRecordRevision(MemoryRecord{ID: memoryID, Envelope: &env, State: state})
	if revErr != nil {
		return "", "", false, revErr
	}
	owner := ""
	if env.Provenance != nil {
		owner = env.Provenance.Agent
		if owner == "" {
			owner = env.Provenance.ToolName
		}
	}
	if owner == "" {
		owner = "unknown"
	}
	return revision, owner, true, nil
}
