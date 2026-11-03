package store

import "strings"

// Counts returns store-wide totals: messages, facts, verified facts, projects.
func (s *Store) Counts() (messages, facts, verified, projects int, err error) {
	err = s.DB.QueryRow(`SELECT
		(SELECT COUNT(*) FROM messages),
		(SELECT COUNT(*) FROM facts),
		(SELECT COUNT(*) FROM facts WHERE verified = 1),
		(SELECT COUNT(*) FROM projects)`).
		Scan(&messages, &facts, &verified, &projects)
	return
}

// WatermarkTimes returns tool -> last-updated RFC3339 for every per-tool
// watermark table known (scanning `watermark:` keys). Executed read-only.
func (s *Store) WatermarkTimes() (map[string]string, error) {
	rows, err := s.DB.Query(`SELECT key, updated_at FROM state WHERE key LIKE 'watermark:%' AND value != '0' AND value != ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var key, at string
		if err := rows.Scan(&key, &at); err != nil {
			return nil, err
		}
		out[strings.TrimPrefix(key, "watermark:")] = at
	}
	return out, rows.Err()
}

// StatusReport summarizes the global status of the knowledge store.
type StatusReport struct {
	Messages       int               `json:"messages"`
	Facts          int               `json:"facts"`
	VerifiedFacts  int               `json:"verified_facts"`
	Projects       int               `json:"projects"`
	WatermarkTimes map[string]string `json:"watermarks"`
}

// Status returns an aggregated StatusReport.
func (s *Store) Status() (*StatusReport, error) {
	msgs, facts, verified, projs, err := s.Counts()
	if err != nil {
		return nil, err
	}
	wmTimes, err := s.WatermarkTimes()
	if err != nil {
		return nil, err
	}
	return &StatusReport{
		Messages:       msgs,
		Facts:          facts,
		VerifiedFacts:  verified,
		Projects:       projs,
		WatermarkTimes: wmTimes,
	}, nil
}

// ExistingContentHashes returns the subset of content hashes already present
// in the messages table. Ingest cursors re-emit the record at the watermark
// boundary, so callers that report "what a run would add" must subtract what
// is already stored rather than trusting a raw count.
func (s *Store) ExistingContentHashes(hashes []string) (map[string]struct{}, error) {
	out := map[string]struct{}{}
	if len(hashes) == 0 {
		return out, nil
	}
	const chunk = 500
	for start := 0; start < len(hashes); start += chunk {
		end := min(start+chunk, len(hashes))
		batch := hashes[start:end]
		placeholders := strings.Repeat("?,", len(batch))
		placeholders = strings.TrimSuffix(placeholders, ",")
		args := make([]any, len(batch))
		for i, h := range batch {
			args[i] = h
		}
		rows, err := s.DB.Query(`SELECT DISTINCT content_hash FROM messages WHERE content_hash IN (`+placeholders+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var h string
			if err := rows.Scan(&h); err != nil {
				rows.Close()
				return nil, err
			}
			out[h] = struct{}{}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return out, nil
}
