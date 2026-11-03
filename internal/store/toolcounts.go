package store

import "encoding/json"

// ToolMessageCounts returns stored message counts per contributing tool by
// expanding each message's source_tools JSON array (a message co-authored by
// two tools counts once for each). Mirrors BenchmarkRows' Go-side expansion.
func (s *Store) ToolMessageCounts() (map[string]int64, error) {
	rows, err := s.DB.Query(`SELECT source_tools, COUNT(*) FROM messages GROUP BY source_tools`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]int64{}
	for rows.Next() {
		var raw string
		var n int64
		if err := rows.Scan(&raw, &n); err != nil {
			return nil, err
		}
		var tools []string
		if json.Unmarshal([]byte(raw), &tools) != nil {
			continue
		}
		for _, t := range tools {
			out[t] += n
		}
	}
	return out, rows.Err()
}
