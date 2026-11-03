package store

import (
	"encoding/json"
	"strings"
)

// MessagesSince returns messages with ts >= sinceRFC3339, optionally filtered
// by project selector. Results are ordered newest-first. Used by `sila digest`.
func (s *Store) MessagesSince(projectSel, sinceRFC3339 string, limit int) ([]StoredMessage, error) {
	matching, err := s.MatchingProjectIDs(projectSel)
	if err != nil {
		return nil, err
	}
	args := []any{sinceRFC3339}
	where := "WHERE m.ts >= ?"
	if len(matching) == 1 {
		where += " AND m.project_id = ?"
		args = append(args, matching[0])
	} else if len(matching) > 1 {
		placeholders := strings.Repeat("?,", len(matching))
		placeholders = "(" + strings.TrimSuffix(placeholders, ",") + ")"
		where += " AND m.project_id IN " + placeholders
		for _, id := range matching {
			args = append(args, id)
		}
	} else if projectSel != "" {
		return nil, nil
	}
	q := `SELECT m.id, m.project_id, m.content_hash, m.ts, m.role, m.content, m.source_tools, m.first_seen
		FROM messages m ` + where + ` ORDER BY m.ts DESC`
	if limit > 0 {
		q += " LIMIT ?"
		args = append(args, limit)
	}
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StoredMessage
	for rows.Next() {
		var sm StoredMessage
		var tools string
		if err := rows.Scan(&sm.ID, &sm.ProjectID, &sm.ContentHash, &sm.TS, &sm.Role, &sm.Content, &tools, &sm.FirstSeen); err != nil {
			return nil, err
		}
		sm.SourceTools = unjoinJSON(tools)
		out = append(out, sm)
	}
	return out, rows.Err()
}

// FactsSince returns facts with promoted_at >= sinceRFC3339, optionally filtered
// by project selector, newest-first. Used by `sila digest` to surface newly
// recorded invariants, memos, and distilled claims.
func (s *Store) FactsSince(projectSel, sinceRFC3339 string, limit int) ([]Fact, error) {
	matching, err := s.MatchingProjectIDs(projectSel)
	if err != nil {
		return nil, err
	}
	args := []any{sinceRFC3339}
	where := "WHERE promoted_at >= ?"
	if len(matching) == 1 {
		where += " AND project_id = ?"
		args = append(args, matching[0])
	} else if len(matching) > 1 {
		placeholders := strings.Repeat("?,", len(matching))
		placeholders = "(" + strings.TrimSuffix(placeholders, ",") + ")"
		where += " AND project_id IN " + placeholders
		for _, id := range matching {
			args = append(args, id)
		}
	} else if projectSel != "" {
		return nil, nil
	}
	q := `SELECT ` + selectFactsFields + ` FROM facts ` + where + ` ORDER BY promoted_at DESC`
	if limit > 0 {
		q += " LIMIT ?"
		args = append(args, limit)
	}
	return s.facts(q, args...)
}

// HandoffsSince returns handoffs with created_at >= sinceRFC3339, optionally
// filtered by project selector, newest-first. Used by `sila digest` to surface
// open baton-passing items.
func (s *Store) HandoffsSince(projectSel, sinceRFC3339 string, limit int) ([]Handoff, error) {
	matching, err := s.MatchingProjectIDs(projectSel)
	if err != nil {
		return nil, err
	}
	where := "WHERE created_at >= ?"
	args := []any{sinceRFC3339}
	if len(matching) == 1 {
		where += " AND project_id = ?"
		args = append(args, matching[0])
	} else if len(matching) > 1 {
		placeholders := strings.Repeat("?,", len(matching))
		placeholders = "(" + strings.TrimSuffix(placeholders, ",") + ")"
		where += " AND project_id IN " + placeholders
		for _, m := range matching {
			args = append(args, m)
		}
	} else if projectSel != "" {
		return nil, nil
	}
	q := `SELECT id, project_id, agent, branch, summary, open_items, decisions, repo_files, file_refs_exist, created_at, status, claimed_by, claimed_at, session_id
		FROM handoffs ` + where + ` ORDER BY created_at DESC`
	if limit > 0 {
		q += " LIMIT ?"
		args = append(args, limit)
	}
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Handoff
	for rows.Next() {
		var h Handoff
		var openJSON, decJSON, filesJSON string
		var refsExistInt int
		if err := rows.Scan(&h.ID, &h.ProjectID, &h.Agent, &h.Branch, &h.Summary,
			&openJSON, &decJSON, &filesJSON, &refsExistInt, &h.CreatedAt, &h.Status,
			&h.ClaimedBy, &h.ClaimedAt, &h.SessionID); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(openJSON), &h.OpenItems)
		_ = json.Unmarshal([]byte(decJSON), &h.Decisions)
		_ = json.Unmarshal([]byte(filesJSON), &h.RepoFiles)
		h.FileRefsExist = refsExistInt != 0
		out = append(out, h)
	}
	return out, rows.Err()
}

// StaleFacts returns facts whose referenced repo_files do not exist on disk
// (where file_refs_exist is zero) and which are neither invariants nor unapproved
// candidates, optionally filtered by project selector. Used by `sila digest`
// to flag ungrounded claims.
func (s *Store) StaleFacts(projectSel string, limit int) ([]Fact, error) {
	matching, err := s.MatchingProjectIDs(projectSel)
	if err != nil {
		return nil, err
	}
	args := []any{}
	where := "WHERE file_refs_exist = 0 AND is_stale = 0 AND session_ref != 'manual_memo' AND review_status != 'candidate' AND kind NOT IN ('invariant', 'nongoal', 'milestone') AND confidence != 'invariant'"
	if len(matching) == 1 {
		where += " AND project_id = ?"
		args = append(args, matching[0])
	} else if len(matching) > 1 {
		placeholders := strings.Repeat("?,", len(matching))
		placeholders = "(" + strings.TrimSuffix(placeholders, ",") + ")"
		where += " AND project_id IN " + placeholders
		for _, id := range matching {
			args = append(args, id)
		}
	} else if projectSel != "" {
		return nil, nil
	}
	q := `SELECT ` + selectFactsFields + ` FROM facts ` + where + ` ORDER BY promoted_at DESC`
	if limit > 0 {
		q += " LIMIT ?"
		args = append(args, limit)
	}
	return s.facts(q, args...)
}
