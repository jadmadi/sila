package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

// FeatureSoakRecord represents an automatically tracked unreleased feature or certified
// goal undergoing its 3-day (72-hour) local dogfooding soak in sila.db.
type FeatureSoakRecord struct {
	ProjectID      string   `json:"project_id"`
	FeatureID      string   `json:"feature_id"`
	Title          string   `json:"title"`
	SourceKind     string   `json:"source_kind"` // "goal" | "commit" | "worktree"
	CommitSHA      string   `json:"commit_sha,omitempty"`
	BaseTag        string   `json:"base_tag,omitempty"`
	MappedTools    []string `json:"mapped_tools,omitempty"`
	StartedAt      string   `json:"started_at"`
	ElapsedHours   float64  `json:"elapsed_hours"`
	ActiveDays     int      `json:"active_days"`
	UsageCount     int      `json:"usage_count"`
	ErrorCount     int      `json:"error_count"`
	Status         string   `json:"status"` // "soaking" | "needs_debug" | "ready" | "released"
	LastError      string   `json:"last_error,omitempty"`
	LastVerifiedAt string   `json:"last_verified_at"`
}

// FeatureSoakDailyReport is an automatically collected daily usage and error snapshot
// for a soaking feature in sila.db.
type FeatureSoakDailyReport struct {
	ID           int64   `json:"id"`
	ProjectID    string  `json:"project_id"`
	FeatureID    string  `json:"feature_id"`
	ReportDate   string  `json:"report_date"` // YYYY-MM-DD UTC
	DayNumber    int     `json:"day_number"`
	ElapsedHours float64 `json:"elapsed_hours"`
	ActiveDays   int     `json:"active_days"`
	UsageCount   int     `json:"usage_count"`
	ErrorCount   int     `json:"error_count"`
	Status       string  `json:"status"`
	Summary      string  `json:"summary"`
	CreatedAt    string  `json:"created_at"`
}

func init() {
	migrations = append(migrations, migration{
		version: 40,
		name:    "feature_soak_ledger",
		up: func(tx *sql.Tx) error {
			stmts := []string{
				`CREATE TABLE IF NOT EXISTS feature_soaks (
					project_id       TEXT NOT NULL,
					feature_id       TEXT NOT NULL,
					title            TEXT NOT NULL,
					source_kind      TEXT NOT NULL,
					commit_sha       TEXT NOT NULL DEFAULT '',
					base_tag         TEXT NOT NULL DEFAULT '',
					mapped_tools     TEXT NOT NULL DEFAULT '[]',
					started_at       TEXT NOT NULL,
					elapsed_hours    REAL NOT NULL DEFAULT 0,
					active_days      INTEGER NOT NULL DEFAULT 0,
					usage_count      INTEGER NOT NULL DEFAULT 0,
					error_count      INTEGER NOT NULL DEFAULT 0,
					status           TEXT NOT NULL DEFAULT 'soaking',
					last_error       TEXT NOT NULL DEFAULT '',
					last_verified_at TEXT NOT NULL,
					PRIMARY KEY (project_id, feature_id)
				);`,
				`CREATE INDEX IF NOT EXISTS idx_feature_soaks_status ON feature_soaks(project_id, status);`,
				`CREATE TABLE IF NOT EXISTS feature_soak_reports (
					id            INTEGER PRIMARY KEY AUTOINCREMENT,
					project_id    TEXT NOT NULL,
					feature_id    TEXT NOT NULL,
					report_date   TEXT NOT NULL,
					day_number    INTEGER NOT NULL DEFAULT 1,
					elapsed_hours REAL NOT NULL DEFAULT 0,
					active_days   INTEGER NOT NULL DEFAULT 0,
					usage_count   INTEGER NOT NULL DEFAULT 0,
					error_count   INTEGER NOT NULL DEFAULT 0,
					status        TEXT NOT NULL DEFAULT 'soaking',
					summary       TEXT NOT NULL DEFAULT '',
					created_at    TEXT NOT NULL,
					UNIQUE (project_id, feature_id, report_date)
				);`,
				`CREATE INDEX IF NOT EXISTS idx_feature_soak_reports_date ON feature_soak_reports(project_id, report_date DESC);`,
			}
			for _, stmt := range stmts {
				if _, err := tx.Exec(stmt); err != nil {
					return err
				}
			}
			return nil
		},
	})
}

// UpsertFeatureSoak inserts or updates a feature soak tracking row in sila.db.
// If started_at is already recorded for an existing feature_id, the earliest started_at
// is preserved unless the caller provides an earlier timestamp.
func (s *Store) UpsertFeatureSoak(rec FeatureSoakRecord) error {
	if s == nil || s.DB == nil || s.ReadOnly {
		return nil
	}
	if rec.ProjectID == "" {
		rec.ProjectID = "sila"
	}
	if rec.LastVerifiedAt == "" {
		rec.LastVerifiedAt = time.Now().UTC().Format(time.RFC3339)
	}
	toolsJSON, err := json.Marshal(rec.MappedTools)
	if err != nil || len(rec.MappedTools) == 0 {
		toolsJSON = []byte("[]")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	_, err = s.DB.ExecContext(ctx, `
		INSERT INTO feature_soaks (
			project_id, feature_id, title, source_kind, commit_sha, base_tag,
			mapped_tools, started_at, elapsed_hours, active_days, usage_count,
			error_count, status, last_error, last_verified_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(project_id, feature_id) DO UPDATE SET
			title            = excluded.title,
			source_kind      = excluded.source_kind,
			commit_sha       = CASE WHEN excluded.commit_sha != '' THEN excluded.commit_sha ELSE feature_soaks.commit_sha END,
			base_tag         = excluded.base_tag,
			mapped_tools     = excluded.mapped_tools,
			started_at       = CASE WHEN excluded.started_at < feature_soaks.started_at AND excluded.started_at != '' THEN excluded.started_at ELSE feature_soaks.started_at END,
			elapsed_hours    = excluded.elapsed_hours,
			active_days      = excluded.active_days,
			usage_count      = excluded.usage_count,
			error_count      = excluded.error_count,
			status           = excluded.status,
			last_error       = excluded.last_error,
			last_verified_at = excluded.last_verified_at
	`,
		rec.ProjectID, rec.FeatureID, rec.Title, rec.SourceKind, rec.CommitSHA, rec.BaseTag,
		string(toolsJSON), rec.StartedAt, rec.ElapsedHours, rec.ActiveDays, rec.UsageCount,
		rec.ErrorCount, rec.Status, rec.LastError, rec.LastVerifiedAt,
	)
	return err
}

// RecordFeatureSoakDailyReport upserts a daily usage and telemetry report row for a soaking feature.
func (s *Store) RecordFeatureSoakDailyReport(rep FeatureSoakDailyReport) error {
	if s == nil || s.DB == nil || s.ReadOnly {
		return nil
	}
	if rep.ProjectID == "" {
		rep.ProjectID = "sila"
	}
	now := time.Now().UTC()
	if rep.ReportDate == "" {
		rep.ReportDate = now.Format("2006-01-02")
	}
	if rep.CreatedAt == "" {
		rep.CreatedAt = now.Format(time.RFC3339)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO feature_soak_reports (
			project_id, feature_id, report_date, day_number, elapsed_hours,
			active_days, usage_count, error_count, status, summary, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(project_id, feature_id, report_date) DO UPDATE SET
			day_number    = excluded.day_number,
			elapsed_hours = excluded.elapsed_hours,
			active_days   = excluded.active_days,
			usage_count   = excluded.usage_count,
			error_count   = excluded.error_count,
			status        = excluded.status,
			summary       = excluded.summary,
			created_at    = excluded.created_at
	`,
		rep.ProjectID, rep.FeatureID, rep.ReportDate, rep.DayNumber, rep.ElapsedHours,
		rep.ActiveDays, rep.UsageCount, rep.ErrorCount, rep.Status, rep.Summary, rep.CreatedAt,
	)
	return err
}

// MarkPriorTagFeatureSoaksReleased marks any feature soak row whose base_tag differs
// from the current baseTag as "released" when a new release tag has been cut.
func (s *Store) MarkPriorTagFeatureSoaksReleased(projectID, currentBaseTag string) error {
	if s == nil || s.DB == nil || s.ReadOnly || currentBaseTag == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err := s.DB.ExecContext(ctx, `
		UPDATE feature_soaks
		SET status = 'released', last_verified_at = ?
		WHERE project_id = ? AND base_tag != '' AND base_tag != ? AND status != 'released'
	`, time.Now().UTC().Format(time.RFC3339), projectID, currentBaseTag)
	return err
}

// ListFeatureSoaks returns tracked feature soak rows for a project (excluding "released" unless includeReleased is true).
func (s *Store) ListFeatureSoaks(projectID string, includeReleased bool) ([]FeatureSoakRecord, error) {
	if s == nil || s.DB == nil {
		return nil, nil
	}
	query := `
		SELECT project_id, feature_id, title, source_kind, commit_sha, base_tag,
		       mapped_tools, started_at, elapsed_hours, active_days, usage_count,
		       error_count, status, last_error, last_verified_at
		FROM feature_soaks
		WHERE (? = '' OR project_id = ?)
	`
	if !includeReleased {
		query += ` AND status != 'released'`
	}
	query += ` ORDER BY started_at DESC, feature_id ASC`

	rows, err := s.DB.Query(query, projectID, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []FeatureSoakRecord
	for rows.Next() {
		var r FeatureSoakRecord
		var toolsRaw string
		if err := rows.Scan(
			&r.ProjectID, &r.FeatureID, &r.Title, &r.SourceKind, &r.CommitSHA, &r.BaseTag,
			&toolsRaw, &r.StartedAt, &r.ElapsedHours, &r.ActiveDays, &r.UsageCount,
			&r.ErrorCount, &r.Status, &r.LastError, &r.LastVerifiedAt,
		); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(toolsRaw), &r.MappedTools)
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListFeatureSoakReports returns daily collected usage reports for a project.
func (s *Store) ListFeatureSoakReports(projectID, featureID string, limit int) ([]FeatureSoakDailyReport, error) {
	if s == nil || s.DB == nil {
		return nil, nil
	}
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	rows, err := s.DB.Query(`
		SELECT id, project_id, feature_id, report_date, day_number, elapsed_hours,
		       active_days, usage_count, error_count, status, summary, created_at
		FROM feature_soak_reports
		WHERE (? = '' OR project_id = ?) AND (? = '' OR feature_id = ?)
		ORDER BY report_date DESC, feature_id ASC
		LIMIT ?
	`, projectID, projectID, featureID, featureID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []FeatureSoakDailyReport
	for rows.Next() {
		var r FeatureSoakDailyReport
		var id sql.NullInt64
		if err := rows.Scan(
			&id, &r.ProjectID, &r.FeatureID, &r.ReportDate, &r.DayNumber, &r.ElapsedHours,
			&r.ActiveDays, &r.UsageCount, &r.ErrorCount, &r.Status, &r.Summary, &r.CreatedAt,
		); err != nil {
			return nil, err
		}
		r.ID = id.Int64
		out = append(out, r)
	}
	return out, rows.Err()
}
