package store

import (
	"database/sql"
	"encoding/json"
	"sort"
	"time"

	"github.com/jadmadi/sila/internal/turnoutcome"
)

// labelOutcomesTx labels assistant messages from the next user prompt in the
// same session. Re-ingesting the same prompts writes the same labels.
func labelOutcomesTx(tx *sql.Tx, messages []LoadMessage) error {
	bySession := map[string][]LoadMessage{}
	order := make([]string, 0, len(messages))
	for _, message := range messages {
		session := message.SessionID
		if _, ok := bySession[session]; !ok {
			order = append(order, session)
		}
		bySession[session] = append(bySession[session], message)
	}
	upd, err := tx.Prepare(`UPDATE messages SET outcome = ? WHERE project_id = ? AND content_hash = ?`)
	if err != nil {
		return err
	}
	defer upd.Close()
	for _, session := range order {
		group := bySession[session]
		for i, message := range group {
			if message.Role != "assistant" {
				continue
			}
			var next *LoadMessage
			for j := i + 1; j < len(group); j++ {
				if group[j].Role == "user" {
					candidate := group[j]
					next = &candidate
					break
				}
			}
			if next == nil {
				continue
			}
			gap := time.Duration(0)
			if from, err := time.Parse(time.RFC3339, message.TS); err == nil {
				if to, err := time.Parse(time.RFC3339, next.TS); err == nil && to.After(from) {
					gap = to.Sub(from)
				}
			}
			label := turnoutcome.Classify(next.Content, gap)
			if _, err := upd.Exec(label, message.ProjectID, message.ContentHash); err != nil {
				return err
			}
		}
	}
	return nil
}

// ToolOutcomeCount is the outcome breakdown for one source tool.
type ToolOutcomeCount struct {
	Tool      string `json:"tool"`
	Success   int    `json:"success"`
	Corrected int    `json:"corrected"`
	Neutral   int    `json:"neutral"`
	Total     int    `json:"total"`
}

// OutcomeCounts groups labeled messages by source tool. A message co-authored
// by two tools counts once per tool.
func (s *Store) OutcomeCounts(projectID string) ([]ToolOutcomeCount, error) {
	query := `SELECT source_tools, outcome FROM messages`
	args := []any{}
	if projectID != "" {
		query += ` WHERE project_id = ?`
		args = append(args, projectID)
	}
	rows, err := s.DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byTool := map[string]*ToolOutcomeCount{}
	for rows.Next() {
		var toolsJSON, outcome string
		if err := rows.Scan(&toolsJSON, &outcome); err != nil {
			return nil, err
		}
		var tools []string
		_ = json.Unmarshal([]byte(toolsJSON), &tools)
		if len(tools) == 0 {
			tools = []string{"unknown"}
		}
		for _, tool := range tools {
			count := byTool[tool]
			if count == nil {
				count = &ToolOutcomeCount{Tool: tool}
				byTool[tool] = count
			}
			count.Total++
			switch outcome {
			case turnoutcome.Success:
				count.Success++
			case turnoutcome.Corrected:
				count.Corrected++
			default:
				count.Neutral++
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	counts := make([]ToolOutcomeCount, 0, len(byTool))
	for _, count := range byTool {
		counts = append(counts, *count)
	}
	sort.Slice(counts, func(i, j int) bool { return counts[i].Tool < counts[j].Tool })
	if len(counts) == 0 {
		counts = append(counts, ToolOutcomeCount{Tool: "all"})
	}
	return counts, nil
}
