package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// SessionCardSchema is the wire schema id for a typed session card.
const SessionCardSchema = "sila/session-card/v1"

// SessionCardMaxBytes is the hard serialized cap for one card.
const SessionCardMaxBytes = 1536

// SessionCardMaxList caps how many cards a caller may request at once.
const SessionCardMaxList = 20

// ErrSessionCardInvalid reports a card that cannot be stored.
var ErrSessionCardInvalid = errors.New("invalid session card")

// CardDecision is one durable decision recorded during a session.
type CardDecision struct {
	Text       string   `json:"text"`
	Files      []string `json:"files,omitempty"`
	Confidence string   `json:"confidence,omitempty"` // high | medium | low
}

// CardNextAction is the single next step the session left behind.
type CardNextAction struct {
	Text  string   `json:"text"`
	Files []string `json:"files,omitempty"`
	Owner string   `json:"owner,omitempty"` // agent | human
}

// CardSource records how a card was produced.
type CardSource struct {
	Transcript string `json:"transcript,omitempty"`
	Messages   int    `json:"messages,omitempty"`
	Generator  string `json:"generator,omitempty"` // deterministic
	Truncated  bool   `json:"truncated,omitempty"`
}

// SessionCard is the typed, size-bounded record written at session stop.
type SessionCard struct {
	Schema        string         `json:"schema"`
	ID            string         `json:"card_id"`
	ProjectID     string         `json:"project_id"`
	SessionID     string         `json:"session_id"`
	Agent         string         `json:"agent"`
	CreatedAt     time.Time      `json:"created_at"`
	Intent        string         `json:"intent,omitempty"`
	Outcome       string         `json:"outcome,omitempty"`
	Decisions     []CardDecision `json:"decisions,omitempty"`
	FilesTouched  []string       `json:"files_touched,omitempty"`
	OpenQuestions []string       `json:"open_questions,omitempty"`
	NextAction    CardNextAction `json:"next_action"`
	Pitfalls      []string       `json:"pitfalls,omitempty"`
	Partial       bool           `json:"partial,omitempty"`
	Source        CardSource     `json:"source"`
}

func init() {
	migrations = append(migrations, migration{
		version: 45,
		name:    "session_cards",
		up: func(tx *sql.Tx) error {
			stmts := []string{
				`CREATE TABLE IF NOT EXISTS session_cards (
					id           TEXT PRIMARY KEY,
					project_id   TEXT NOT NULL,
					session_id   TEXT NOT NULL,
					agent        TEXT NOT NULL,
					payload_json TEXT NOT NULL,
					partial      INTEGER NOT NULL DEFAULT 0,
					created_at   INTEGER NOT NULL
				);`,
				`CREATE INDEX IF NOT EXISTS idx_session_cards_project_time
					ON session_cards(project_id, created_at DESC);`,
			}
			for _, stmt := range stmts {
				if _, err := tx.Exec(stmt); err != nil {
					return fmt.Errorf("migration 45: %w", err)
				}
			}
			return nil
		},
	})
}

// SessionCardID derives the stable id for a project and session pair.
func SessionCardID(projectID, sessionID string) string {
	sum := sha256.Sum256([]byte(projectID + "\x00" + sessionID))
	return "sha256:" + hex.EncodeToString(sum[:])[:16]
}

// SaveSessionCard validates and upserts one card. Re-running the same session
// replaces the same row; it never creates a duplicate.
func (s *Store) SaveSessionCard(ctx context.Context, card SessionCard) (string, error) {
	if card.Schema == "" {
		card.Schema = SessionCardSchema
	}
	if card.Schema != SessionCardSchema {
		return "", fmt.Errorf("%w: schema %q", ErrSessionCardInvalid, card.Schema)
	}
	if card.ID == "" {
		card.ID = SessionCardID(card.ProjectID, card.SessionID)
	}
	if card.ProjectID == "" || card.SessionID == "" || card.ID == "" {
		return "", fmt.Errorf("%w: card_id, project_id, and session_id are required", ErrSessionCardInvalid)
	}
	if card.Agent == "" {
		card.Agent = "unknown"
	}
	if card.CreatedAt.IsZero() {
		return "", fmt.Errorf("%w: created_at is required", ErrSessionCardInvalid)
	}
	payload, err := json.Marshal(card)
	if err != nil {
		return "", fmt.Errorf("marshal session card: %w", err)
	}
	if len(payload) > SessionCardMaxBytes {
		return "", fmt.Errorf("%w: payload is %d bytes, limit %d", ErrSessionCardInvalid, len(payload), SessionCardMaxBytes)
	}
	_, err = s.DB.ExecContext(ctx, `
		INSERT INTO session_cards (id, project_id, session_id, agent, payload_json, partial, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			project_id = excluded.project_id,
			session_id = excluded.session_id,
			agent = excluded.agent,
			payload_json = excluded.payload_json,
			partial = excluded.partial,
			created_at = excluded.created_at`,
		card.ID, card.ProjectID, card.SessionID, card.Agent, string(payload), boolToInt(card.Partial), card.CreatedAt.UTC().UnixMilli())
	if err != nil {
		return "", fmt.Errorf("save session card: %w", err)
	}
	return card.ID, nil
}

// LatestSessionCards returns the newest cards for one project.
func (s *Store) LatestSessionCards(ctx context.Context, projectID string, limit int) ([]SessionCard, error) {
	if limit <= 0 {
		limit = 10
	}
	if limit > SessionCardMaxList {
		limit = SessionCardMaxList
	}
	rows, err := s.DB.QueryContext(ctx, `
		SELECT payload_json FROM session_cards
		WHERE project_id = ?
		ORDER BY created_at DESC, id ASC
		LIMIT ?`, projectID, limit)
	if err != nil {
		return nil, fmt.Errorf("latest session cards: %w", err)
	}
	defer rows.Close()
	cards := make([]SessionCard, 0, limit)
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, fmt.Errorf("scan session card: %w", err)
		}
		var card SessionCard
		if err := json.Unmarshal([]byte(payload), &card); err != nil {
			return nil, fmt.Errorf("parse session card: %w", err)
		}
		cards = append(cards, card)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate session cards: %w", err)
	}
	return cards, nil
}

// GetSessionCard returns one card by id.
func (s *Store) GetSessionCard(ctx context.Context, id string) (SessionCard, bool, error) {
	var payload string
	err := s.DB.QueryRowContext(ctx, `SELECT payload_json FROM session_cards WHERE id = ?`, id).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return SessionCard{}, false, nil
	}
	if err != nil {
		return SessionCard{}, false, fmt.Errorf("get session card: %w", err)
	}
	var card SessionCard
	if err := json.Unmarshal([]byte(payload), &card); err != nil {
		return SessionCard{}, false, fmt.Errorf("parse session card: %w", err)
	}
	return card, true, nil
}
