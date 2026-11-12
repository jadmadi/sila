package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// Merge slot errors.
var (
	ErrMergeSlotBusy  = errors.New("merge slot busy")
	ErrMergeSlotToken = errors.New("merge slot token mismatch")
)

const (
	MergeSlotMinLease = time.Minute
	MergeSlotMaxLease = 4 * time.Hour
)

// MergeSlot is one project's exclusive merge lease.
type MergeSlot struct {
	ProjectID     string    `json:"project_id"`
	Holder        string    `json:"holder"`
	Token         string    `json:"token,omitempty"`
	Expiry        time.Time `json:"expiry"`
	ReclaimedFrom string    `json:"reclaimed_from,omitempty"`
}

// MergeSlotBusyError carries the current holder and expiry.
type MergeSlotBusyError struct {
	Holder string
	Expiry time.Time
}

func (e *MergeSlotBusyError) Error() string {
	return fmt.Sprintf("merge slot busy: held by %s until %s", e.Holder, e.Expiry.Format(time.RFC3339))
}

func (e *MergeSlotBusyError) Unwrap() error { return ErrMergeSlotBusy }

func init() {
	migrations = append(migrations, migration{
		version: 50,
		name:    "merge_slots",
		up: func(tx *sql.Tx) error {
			_, err := tx.Exec(`CREATE TABLE IF NOT EXISTS merge_slots (
				project_id     TEXT PRIMARY KEY,
				holder         TEXT NOT NULL,
				token          TEXT NOT NULL,
				expiry         TEXT NOT NULL,
				reclaimed_from TEXT NOT NULL DEFAULT '',
				updated_at     TEXT NOT NULL
			)`)
			return err
		},
	})
}

func clampMergeLease(lease time.Duration) time.Duration {
	if lease < MergeSlotMinLease {
		return MergeSlotMinLease
	}
	if lease > MergeSlotMaxLease {
		return MergeSlotMaxLease
	}
	return lease
}

func newMergeToken() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return ""
	}
	return hex.EncodeToString(buf)
}

// AcquireMergeSlot takes the project lease, reclaiming an expired one.
func (s *Store) AcquireMergeSlot(ctx context.Context, projectID, holder string, lease time.Duration, now time.Time) (*MergeSlot, error) {
	if projectID == "" || holder == "" {
		return nil, fmt.Errorf("merge slot requires a project and holder")
	}
	lease = clampMergeLease(lease)
	if now.IsZero() {
		now = time.Now().UTC()
	}
	conn, err := s.DB.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return nil, err
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()
	current, err := mergeSlotByProject(ctx, conn, projectID)
	if err != nil {
		return nil, err
	}
	reclaimed := ""
	if current != nil {
		if current.Expiry.After(now) {
			return nil, &MergeSlotBusyError{Holder: current.Holder, Expiry: current.Expiry}
		}
		reclaimed = current.Holder
	}
	slot := &MergeSlot{
		ProjectID: projectID, Holder: holder, Token: newMergeToken(),
		Expiry: now.Add(lease).UTC(), ReclaimedFrom: reclaimed,
	}
	if slot.Token == "" {
		return nil, fmt.Errorf("merge slot token generation failed")
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO merge_slots (project_id, holder, token, expiry, reclaimed_from, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(project_id) DO UPDATE SET holder=excluded.holder, token=excluded.token,
			expiry=excluded.expiry, reclaimed_from=excluded.reclaimed_from, updated_at=excluded.updated_at`,
		slot.ProjectID, slot.Holder, slot.Token, slot.Expiry.Format(time.RFC3339), slot.ReclaimedFrom, now.UTC().Format(time.RFC3339)); err != nil {
		return nil, err
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return nil, err
	}
	committed = true
	return slot, nil
}

// HeartbeatMergeSlot extends a lease from now when the token matches.
func (s *Store) HeartbeatMergeSlot(ctx context.Context, projectID, token string, lease time.Duration, now time.Time) (*MergeSlot, error) {
	lease = clampMergeLease(lease)
	if now.IsZero() {
		now = time.Now().UTC()
	}
	slot, err := s.MergeSlot(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if slot == nil || slot.Token != token {
		return nil, ErrMergeSlotToken
	}
	expiry := now.Add(lease).UTC()
	_, err = s.DB.Exec(`UPDATE merge_slots SET expiry = ?, updated_at = ? WHERE project_id = ? AND token = ?`,
		expiry.Format(time.RFC3339), now.UTC().Format(time.RFC3339), projectID, token)
	if err != nil {
		return nil, err
	}
	slot.Expiry = expiry
	return slot, nil
}

// ReleaseMergeSlot clears the lease when the token matches or force is set.
func (s *Store) ReleaseMergeSlot(ctx context.Context, projectID, token string, force bool) error {
	if !force {
		slot, err := s.MergeSlot(ctx, projectID)
		if err != nil {
			return err
		}
		if slot == nil || slot.Token != token {
			return ErrMergeSlotToken
		}
	}
	_, err := s.DB.Exec(`DELETE FROM merge_slots WHERE project_id = ?`, projectID)
	return err
}

// MergeSlot reads the current lease, if any.
func (s *Store) MergeSlot(ctx context.Context, projectID string) (*MergeSlot, error) {
	var slot MergeSlot
	var expiry, updated string
	err := s.DB.QueryRowContext(ctx, `SELECT project_id, holder, token, expiry, reclaimed_from, updated_at FROM merge_slots WHERE project_id = ?`, projectID).
		Scan(&slot.ProjectID, &slot.Holder, &slot.Token, &expiry, &slot.ReclaimedFrom, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if parsed, parseErr := time.Parse(time.RFC3339, expiry); parseErr == nil {
		slot.Expiry = parsed
	}
	return &slot, nil
}

func mergeSlotByProject(ctx context.Context, conn *sql.Conn, projectID string) (*MergeSlot, error) {
	var slot MergeSlot
	var expiry, updated string
	err := conn.QueryRowContext(ctx, `SELECT project_id, holder, token, expiry, reclaimed_from, updated_at FROM merge_slots WHERE project_id = ?`, projectID).
		Scan(&slot.ProjectID, &slot.Holder, &slot.Token, &expiry, &slot.ReclaimedFrom, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if parsed, parseErr := time.Parse(time.RFC3339, expiry); parseErr == nil {
		slot.Expiry = parsed
	}
	return &slot, nil
}
