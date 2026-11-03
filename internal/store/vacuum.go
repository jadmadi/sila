package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"time"
)

// ErrBackupMarkerUnrecorded signals that Backup produced a valid snapshot but
// could not record the last_backup state key, so LastBackupTime will not
// reflect the run. The snapshot file itself is usable: callers warn rather than
// discard it.
var ErrBackupMarkerUnrecorded = errors.New("backup snapshot written, but the last_backup marker was not recorded")

// DBStats contains storage and layout statistics for the SQLite database.
type DBStats struct {
	Path             string  `json:"path"`
	FileSize         int64   `json:"file_size"`
	WALSize          int64   `json:"wal_size"`
	PageCount        int64   `json:"page_count"`
	PageSize         int64   `json:"page_size"`
	FreelistCount    int64   `json:"freelist_count"`
	FreeBytes        int64   `json:"free_bytes"`
	FragmentationPct float64 `json:"fragmentation_pct"`
}

// VacuumResult captures before and after metrics of a database vacuum operation.
type VacuumResult struct {
	Before         DBStats       `json:"before"`
	After          DBStats       `json:"after"`
	ReclaimedBytes int64         `json:"reclaimed_bytes"`
	ReclaimedPct   float64       `json:"reclaimed_pct"`
	Duration       time.Duration `json:"duration"`
	DurationMS     int64         `json:"duration_ms"`
	Optimized      bool          `json:"optimized"`
}

// FilePath resolves the absolute filesystem path to the underlying SQLite database file.
func (s *Store) FilePath() string {
	if s.Path != "" {
		return s.Path
	}
	var seq int
	var name, file string
	if err := s.DB.QueryRow("PRAGMA database_list").Scan(&seq, &name, &file); err == nil && file != "" {
		return file
	}
	return ""
}

// Stats returns the current storage metrics, page count, and freelist statistics.
func (s *Store) Stats() (*DBStats, error) {
	var pageCount, pageSize, freelistCount int64
	if err := s.DB.QueryRow("PRAGMA page_count").Scan(&pageCount); err != nil {
		return nil, fmt.Errorf("read page_count: %w", err)
	}
	if err := s.DB.QueryRow("PRAGMA page_size").Scan(&pageSize); err != nil {
		return nil, fmt.Errorf("read page_size: %w", err)
	}
	if err := s.DB.QueryRow("PRAGMA freelist_count").Scan(&freelistCount); err != nil {
		return nil, fmt.Errorf("read freelist_count: %w", err)
	}

	path := s.FilePath()
	var fileSize, walSize int64
	if path != "" {
		if fi, err := os.Stat(path); err == nil {
			fileSize = fi.Size()
		}
		if fi, err := os.Stat(path + "-wal"); err == nil {
			walSize = fi.Size()
		}
	} else {
		fileSize = pageCount * pageSize
	}

	freeBytes := freelistCount * pageSize
	var fragPct float64
	if pageCount > 0 {
		fragPct = (float64(freelistCount) / float64(pageCount)) * 100.0
	}

	return &DBStats{
		Path:             path,
		FileSize:         fileSize,
		WALSize:          walSize,
		PageCount:        pageCount,
		PageSize:         pageSize,
		FreelistCount:    freelistCount,
		FreeBytes:        freeBytes,
		FragmentationPct: fragPct,
	}, nil
}

// Vacuum defragments the SQLite store, reclaims dead pages, checkpoints the WAL log,
// and runs query planner optimization (PRAGMA optimize).
func (s *Store) Vacuum() (*VacuumResult, error) {
	start := time.Now()
	before, err := s.Stats()
	if err != nil {
		return nil, err
	}

	// 1. Flush and truncate WAL log before vacuum
	_, _ = s.DB.Exec("PRAGMA wal_checkpoint(TRUNCATE)")

	// 2. Execute VACUUM outside transaction
	if _, err := s.DB.Exec("VACUUM"); err != nil {
		return nil, fmt.Errorf("execute vacuum: %w", err)
	}

	// 3. Optimize query planner statistics
	if _, err := s.DB.Exec("PRAGMA optimize"); err != nil {
		// non-fatal optimization
	}

	// 4. Truncate WAL log after vacuum
	_, _ = s.DB.Exec("PRAGMA wal_checkpoint(TRUNCATE)")

	// 5. Record last_vacuum timestamp in state
	now := time.Now().UTC().Format(time.RFC3339)
	_, _ = s.DB.Exec(`INSERT INTO state(key, value, updated_at) VALUES('last_vacuum', ?, ?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`,
		now, now)

	after, err := s.Stats()
	if err != nil {
		return nil, err
	}

	dur := time.Since(start)
	totalBefore := before.FileSize + before.WALSize
	totalAfter := after.FileSize + after.WALSize
	reclaimed := max(totalBefore-totalAfter, 0)
	var reclaimedPct float64
	if totalBefore > 0 && reclaimed > 0 {
		reclaimedPct = (float64(reclaimed) / float64(totalBefore)) * 100.0
	}

	return &VacuumResult{
		Before:         *before,
		After:          *after,
		ReclaimedBytes: reclaimed,
		ReclaimedPct:   reclaimedPct,
		Duration:       dur,
		DurationMS:     dur.Milliseconds(),
		Optimized:      true,
	}, nil
}

// LastVacuumTime returns the timestamp of the last vacuum run, or zero time if never run.
func (s *Store) LastVacuumTime() (time.Time, error) {
	var val string
	err := s.DB.QueryRow("SELECT value FROM state WHERE key='last_vacuum'").Scan(&val)
	if err == sql.ErrNoRows {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	return time.Parse(time.RFC3339, val)
}

// Backup produces a consistent snapshot of the store at dest via
// VACUUM INTO. Unlike copying sila.db alone, the snapshot includes every
// committed transaction even while writers are active and the WAL holds
// unchecked pages, so a restore never loses committed data. The destination
// must not already exist, and the run is recorded under the last_backup state
// key.
func (s *Store) Backup(dest string) error {
	if dest == "" {
		return fmt.Errorf("backup destination is required")
	}
	if _, err := os.Stat(dest); err == nil {
		return fmt.Errorf("backup destination %s already exists (backups are immutable; choose a new name)", dest)
	}

	// VACUUM INTO runs outside any transaction and refuses to overwrite an
	// existing file, matching the immutability contract above. It reads a
	// consistent snapshot on its own, so callers may hold this store read-only.
	if _, err := s.DB.Exec("VACUUM INTO ?", dest); err != nil {
		return fmt.Errorf("execute vacuum into %s: %w", dest, err)
	}
	// The snapshot holds the same private knowledge as the store, so it gets
	// the same restrictive mode the store files use.
	if err := os.Chmod(dest, 0o600); err != nil {
		return fmt.Errorf("secure backup %s: %w", dest, err)
	}

	// Record the run. A read-only handle cannot write, so the marker is
	// written through a short-lived writable connection to the same store —
	// this timestamp is the only write a backup performs.
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := s.DB.Exec(`INSERT INTO state(key, value, updated_at) VALUES('last_backup', ?, ?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`,
		now, now); err != nil {
		return recordBackupMarker(s.Path, now)
	}
	return nil
}

// recordBackupMarker stores the last_backup timestamp through a short-lived
// writable connection, which is what a read-only store handle needs in order to
// write at all. The snapshot is already valid when this runs, so a failure here
// is surfaced as ErrBackupMarkerUnrecorded rather than a failed backup.
func recordBackupMarker(storePath, now string) error {
	wdb, err := sql.Open("sqlite", fmt.Sprintf("file:%s?_pragma=busy_timeout(%d)", storePath, BusyTimeoutMS))
	if err != nil {
		return fmt.Errorf("%w: cannot open marker connection: %v", ErrBackupMarkerUnrecorded, err)
	}
	defer wdb.Close()
	if _, err := wdb.Exec(`INSERT INTO state(key, value, updated_at) VALUES('last_backup', ?, ?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`,
		now, now); err != nil {
		return fmt.Errorf("%w: %v", ErrBackupMarkerUnrecorded, err)
	}
	return nil
}

// LastBackupTime returns the timestamp of the last backup, or zero time if never run.
func (s *Store) LastBackupTime() (time.Time, error) {
	var val string
	err := s.DB.QueryRow("SELECT value FROM state WHERE key='last_backup'").Scan(&val)
	if err == sql.ErrNoRows {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	return time.Parse(time.RFC3339, val)
}

// AutoVacuumIfNeeded checks if freelist threshold or fragmentation warrants a vacuum,
// respecting minInterval to prevent excessive churn. If conditions are met, it executes Vacuum().
func (s *Store) AutoVacuumIfNeeded(minFreelistPages int64, minInterval time.Duration) (bool, *VacuumResult, error) {
	if minFreelistPages <= 0 {
		minFreelistPages = 50 // default: ~200 KB free pages
	}
	if minInterval <= 0 {
		minInterval = 24 * time.Hour
	}

	lastTime, err := s.LastVacuumTime()
	if err == nil && !lastTime.IsZero() && time.Since(lastTime) < minInterval {
		return false, nil, nil
	}

	stats, err := s.Stats()
	if err != nil {
		return false, nil, err
	}

	// Trigger if freelist count exceeds threshold or fragmentation >= 5%
	if stats.FreelistCount < minFreelistPages && stats.FragmentationPct < 5.0 {
		return false, nil, nil
	}

	res, err := s.Vacuum()
	if err != nil {
		return false, nil, err
	}
	return true, res, nil
}
