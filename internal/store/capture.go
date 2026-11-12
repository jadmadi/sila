package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

func init() {
	migrations = append(migrations, migration{
		version: 46,
		name:    "capture_state",
		up: func(tx *sql.Tx) error {
			stmts := []string{
				`CREATE TABLE IF NOT EXISTS capture_state (
					project_id          TEXT PRIMARY KEY,
					paused              INTEGER NOT NULL DEFAULT 0,
					global_pause        INTEGER NOT NULL DEFAULT 0,
					since               TEXT NOT NULL DEFAULT '',
					reason              TEXT NOT NULL DEFAULT '',
					agent               TEXT NOT NULL DEFAULT '',
					forgotten           INTEGER NOT NULL DEFAULT 0,
					forgotten_at        TEXT NOT NULL DEFAULT '',
					exclude_transcripts INTEGER NOT NULL DEFAULT 0
				);`,
			}
			for _, stmt := range stmts {
				if _, err := tx.Exec(stmt); err != nil {
					return fmt.Errorf("migration 46: %w", err)
				}
			}
			return nil
		},
	})
}

// captureGlobalKey is the capture_state row that applies to every project.
const captureGlobalKey = "*"

// CaptureState is the effective capture state for one project.
type CaptureState struct {
	Paused             bool
	Global             bool
	Since              time.Time
	Reason             string
	Agent              string
	Forgotten          bool
	ForgottenAt        time.Time
	ExcludeTranscripts bool
}

// ForgetCounts reports what a forget operation archived.
type ForgetCounts struct {
	Memos    int
	Lessons  int
	Cases    int
	Facts    int
	Handoffs int
}

type captureRow struct {
	paused, forgotten, excludeTranscripts bool
	since, reason, agent, forgottenAt     string
}

func (s *Store) readCaptureRow(projectID string) (captureRow, bool, error) {
	var row captureRow
	var paused, forgotten, exclude int
	err := s.DB.QueryRow(`SELECT paused, since, reason, agent, forgotten, forgotten_at, exclude_transcripts
		FROM capture_state WHERE project_id = ?`, projectID).Scan(&paused, &row.since, &row.reason, &row.agent, &forgotten, &row.forgottenAt, &exclude)
	if errors.Is(err, sql.ErrNoRows) {
		return captureRow{}, false, nil
	}
	if err != nil {
		return captureRow{}, false, err
	}
	row.paused, row.forgotten, row.excludeTranscripts = paused == 1, forgotten == 1, exclude == 1
	return row, true, nil
}

// CaptureState returns the effective project state, merging the global pause.
func (s *Store) CaptureState(projectID string) (CaptureState, error) {
	project, hasProject, err := s.readCaptureRow(projectID)
	if err != nil {
		return CaptureState{}, err
	}
	global, hasGlobal, err := s.readCaptureRow(captureGlobalKey)
	if err != nil {
		return CaptureState{}, err
	}
	var state CaptureState
	if hasProject {
		state.Forgotten = project.forgotten
		state.ExcludeTranscripts = project.excludeTranscripts
		state.ForgottenAt = parseCaptureTime(project.forgottenAt)
		if project.paused {
			state.Paused = true
			state.Since = parseCaptureTime(project.since)
			state.Reason = project.reason
			state.Agent = project.agent
		}
	}
	if hasGlobal && global.paused {
		state.Paused = true
		state.Global = true
		if state.Since.IsZero() {
			state.Since = parseCaptureTime(global.since)
		}
		if state.Reason == "" {
			state.Reason = global.reason
		}
		if state.Agent == "" {
			state.Agent = global.agent
		}
	}
	return state, nil
}

func parseCaptureTime(raw string) time.Time {
	if raw == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}
	}
	return parsed
}

// CapturePaused reports whether hooks and writes must stop for this project.
func (s *Store) CapturePaused(projectID string) (bool, error) {
	state, err := s.CaptureState(projectID)
	if err != nil {
		return false, err
	}
	return state.Paused, nil
}

// CaptureExcluded reports whether read surfaces hide this project by default.
func (s *Store) CaptureExcluded(projectID string) (bool, error) {
	state, err := s.CaptureState(projectID)
	if err != nil {
		return false, err
	}
	return state.Forgotten, nil
}

// PauseCapture pauses a project or the global capture. Pausing again keeps the
// original since timestamp and updates the reason.
func (s *Store) PauseCapture(projectID, reason, agent string, global bool) error {
	key := projectID
	if global {
		key = captureGlobalKey
	}
	if key == "" {
		return errors.New("pause requires a project")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	globalFlag := 0
	if global {
		globalFlag = 1
	}
	_, err := s.DB.Exec(`INSERT INTO capture_state (project_id, paused, global_pause, since, reason, agent)
		VALUES (?, 1, ?, ?, ?, ?)
		ON CONFLICT(project_id) DO UPDATE SET
			paused = 1,
			global_pause = excluded.global_pause,
			since = CASE WHEN capture_state.paused = 0 THEN excluded.since ELSE capture_state.since END,
			reason = excluded.reason,
			agent = excluded.agent`,
		key, globalFlag, now, reason, agent)
	return err
}

// ResumeCapture clears the pause and the forget exclusion. It never unarchives.
func (s *Store) ResumeCapture(projectID string, global bool) error {
	key := projectID
	if global {
		key = captureGlobalKey
	}
	_, err := s.DB.Exec(`UPDATE capture_state SET paused = 0, forgotten = 0, exclude_transcripts = 0 WHERE project_id = ?`, key)
	return err
}

// ForgetProject archives current knowledge rows and marks the project excluded
// from default reads until resume.
func (s *Store) ForgetProject(ctx context.Context, projectID string, includeTranscripts bool) (ForgetCounts, error) {
	var counts ForgetCounts
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return counts, err
	}
	defer func() { _ = tx.Rollback() }()
	count := func(query string, args ...any) (int, error) {
		var n int
		err := tx.QueryRow(query, args...).Scan(&n)
		return n, err
	}
	if counts.Memos, err = count(`SELECT COUNT(*) FROM memory_records WHERE project_id = ? AND kind = 'memo' AND state = 'current'`, projectID); err != nil {
		return counts, err
	}
	if counts.Lessons, err = count(`SELECT COUNT(*) FROM memory_records WHERE project_id = ? AND kind = 'lesson' AND state = 'current'`, projectID); err != nil {
		return counts, err
	}
	if counts.Cases, err = count(`SELECT COUNT(*) FROM memory_records WHERE project_id = ? AND kind = 'case' AND state = 'current'`, projectID); err != nil {
		return counts, err
	}
	if counts.Facts, err = count(`SELECT COUNT(*) FROM facts WHERE project_id = ? AND COALESCE(review_status, '') != 'archived'`, projectID); err != nil {
		return counts, err
	}
	if counts.Handoffs, err = count(`SELECT COUNT(*) FROM handoffs WHERE project_id = ? AND status != 'archived'`, projectID); err != nil {
		return counts, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := tx.Exec(`UPDATE memory_records SET state = 'archived', updated_at = ? WHERE project_id = ? AND state = 'current'`, now, projectID); err != nil {
		return counts, err
	}
	if _, err := tx.Exec(`UPDATE facts SET review_status = 'archived' WHERE project_id = ?`, projectID); err != nil {
		return counts, err
	}
	if _, err := tx.Exec(`UPDATE handoffs SET status = 'archived' WHERE project_id = ? AND status != 'archived'`, projectID); err != nil {
		return counts, err
	}
	includeFlag := 0
	if includeTranscripts {
		includeFlag = 1
	}
	if _, err := tx.Exec(`INSERT INTO capture_state (project_id, forgotten, forgotten_at, exclude_transcripts)
		VALUES (?, 1, ?, ?)
		ON CONFLICT(project_id) DO UPDATE SET
			forgotten = 1, forgotten_at = excluded.forgotten_at, exclude_transcripts = excluded.exclude_transcripts`,
		projectID, now, includeFlag); err != nil {
		return counts, err
	}
	return counts, tx.Commit()
}
