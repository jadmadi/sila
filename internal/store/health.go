package store

// HealthReport is a read-only store health and concurrency snapshot.
type HealthReport struct {
	Path            string `json:"path"`
	FileSize        int64  `json:"file_size"`
	WALSize         int64  `json:"wal_size"`
	SchemaVersion   int    `json:"schema_version"`
	QuickCheck      string `json:"quick_check"`
	ActiveWriters   int64  `json:"active_writers"`
	LastLockWaitMS  int64  `json:"last_lock_wait_ms"`
	RetryableErrors int64  `json:"retryable_errors"`
	BusyTimeoutMS   int    `json:"busy_timeout_ms"`
}

// Health returns the store size, WAL size, schema state, and process-local
// writer contention metrics. It never mutates the store.
func (s *Store) Health() (*HealthReport, error) {
	stats, err := s.Stats()
	if err != nil {
		return nil, err
	}
	version, err := s.SchemaVersion()
	if err != nil {
		return nil, err
	}
	quick := "ok"
	if err := s.QuickCheck(); err != nil {
		quick = err.Error()
	}
	m := CurrentConcurrencyMetrics()
	return &HealthReport{
		Path:            stats.Path,
		FileSize:        stats.FileSize,
		WALSize:         stats.WALSize,
		SchemaVersion:   version,
		QuickCheck:      quick,
		ActiveWriters:   m.ActiveWriters,
		LastLockWaitMS:  m.LastLockWaitMS,
		RetryableErrors: m.RetryableErrors,
		BusyTimeoutMS:   m.BusyTimeoutMS,
	}, nil
}
