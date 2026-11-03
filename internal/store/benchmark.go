package store

import "strings"

// ToolMetricRow is one tool's aggregated benchmark metrics, computed by
// scanning messages and facts in Go (source_tools/tools are JSON arrays in
// TEXT columns, so SQL-side aggregation is impractical).
type ToolMetricRow struct {
	Tool             string
	Projects         int
	Messages         int
	AssistantTurns   int
	UserTurns        int
	TotalClaims      int
	GroundedClaims   int // file_refs_exist = true
	UngroundedClaims int // file_refs_exist = false
}

// BenchmarkRows scans messages and facts (optionally filtered by project
// selector) and aggregates per-tool metrics. Each message's source_tools and
// each fact's tools JSON arrays are expanded so a single row may contribute
// to multiple tools.
func (s *Store) BenchmarkRows(projectSel string) ([]ToolMetricRow, error) {
	matching, err := s.MatchingProjectIDs(projectSel)
	if err != nil {
		return nil, err
	}
	if projectSel != "" && len(matching) == 0 {
		return nil, nil
	}

	// Aggregates keyed by tool.
	type agg struct {
		projects       map[string]bool
		messages       int
		assistantTurns int
		userTurns      int
		totalClaims    int
		grounded       int
		ungrounded     int
	}
	aggs := map[string]*agg{}

	ensure := func(tool string) *agg {
		a, ok := aggs[tool]
		if !ok {
			a = &agg{projects: map[string]bool{}}
			aggs[tool] = a
		}
		return a
	}

	// 1. Scan messages.
	msgArgs := []any{}
	msgWhere := ""
	if len(matching) == 1 {
		msgWhere = "WHERE project_id = ?"
		msgArgs = append(msgArgs, matching[0])
	} else if len(matching) > 1 {
		placeholders := strings.Repeat("?,", len(matching))
		placeholders = "(" + strings.TrimSuffix(placeholders, ",") + ")"
		msgWhere = "WHERE project_id IN " + placeholders
		for _, id := range matching {
			msgArgs = append(msgArgs, id)
		}
	}
	rows, err := s.DB.Query(`SELECT project_id, role, source_tools FROM messages `+msgWhere, msgArgs...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var projectID, role, toolsJSON string
		if err := rows.Scan(&projectID, &role, &toolsJSON); err != nil {
			rows.Close()
			return nil, err
		}
		tools := unjoinJSON(toolsJSON)
		if len(tools) == 0 {
			tools = []string{"unknown"}
		}
		for _, t := range tools {
			a := ensure(t)
			a.messages++
			a.projects[projectID] = true
			if strings.ToLower(role) == "assistant" {
				a.assistantTurns++
			} else if strings.ToLower(role) == "user" {
				a.userTurns++
			}
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// 2. Scan facts.
	factArgs := []any{}
	factWhere := ""
	if len(matching) == 1 {
		factWhere = "WHERE project_id = ?"
		factArgs = append(factArgs, matching[0])
	} else if len(matching) > 1 {
		placeholders := strings.Repeat("?,", len(matching))
		placeholders = "(" + strings.TrimSuffix(placeholders, ",") + ")"
		factWhere = "WHERE project_id IN " + placeholders
		for _, id := range matching {
			factArgs = append(factArgs, id)
		}
	}
	rows, err = s.DB.Query(`SELECT tools, file_refs_exist FROM facts `+factWhere, factArgs...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var toolsJSON string
		var refsExistInt int
		if err := rows.Scan(&toolsJSON, &refsExistInt); err != nil {
			rows.Close()
			return nil, err
		}
		tools := unjoinJSON(toolsJSON)
		grounded := refsExistInt != 0
		for _, t := range tools {
			a := ensure(t)
			a.totalClaims++
			if grounded {
				a.grounded++
			} else {
				a.ungrounded++
			}
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// 3. Flatten to sorted rows.
	var out []ToolMetricRow
	for tool, a := range aggs {
		out = append(out, ToolMetricRow{
			Tool:             tool,
			Projects:         len(a.projects),
			Messages:         a.messages,
			AssistantTurns:   a.assistantTurns,
			UserTurns:        a.userTurns,
			TotalClaims:      a.totalClaims,
			GroundedClaims:   a.grounded,
			UngroundedClaims: a.ungrounded,
		})
	}
	// Sort by messages desc, then tool name.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0; j-- {
			if out[j].Messages > out[j-1].Messages || (out[j].Messages == out[j-1].Messages && out[j].Tool < out[j-1].Tool) {
				out[j], out[j-1] = out[j-1], out[j]
			} else {
				break
			}
		}
	}
	return out, nil
}
