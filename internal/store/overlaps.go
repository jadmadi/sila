package store

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"time"
)

// NearMessage is a same-project message within a time window, used for lenient
// cross-tool overlap detection. ID is the mesource content_hash.
type NearMessage struct {
	ID      string
	Content string
	Tools   []string
	TS      string
}

// MessagesNear returns messages in projectID with role in [lo, hi] (RFC3339).
func (s *Store) MessagesNear(projectID, role, lo, hi string) ([]NearMessage, error) {
	rows, err := s.DB.Query(
		`SELECT content_hash, content, source_tools, ts FROM messages
		 WHERE project_id=? AND role=? AND ts BETWEEN ? AND ?`,
		projectID, role, lo, hi)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NearMessage
	for rows.Next() {
		var nm NearMessage
		var tools string
		if err := rows.Scan(&nm.ID, &nm.Content, &tools, &nm.TS); err != nil {
			return nil, err
		}
		nm.Tools = unjoinJSON(tools)
		out = append(out, nm)
	}
	return out, rows.Err()
}

// Overlap links two messages (by content_hash id) that leniently match across
// different tools. RecordOverlap is idempotent on the pairwise id.
type Overlap struct {
	ID         string
	ProjectID  string
	MsgA, MsgB string
	ToolsA     []string
	ToolsB     []string
	Similarity float64
	FirstSeen  string
}

// OverlapID builds a deterministic id from the two sorted content hashes.
func OverlapID(projectID, a, b string) string {
	pa := []string{a, b}
	sort.Strings(pa)
	sum := sha256.Sum256([]byte(projectID + "\x00" + pa[0] + "\x00" + pa[1]))
	return "ovl_" + hex.EncodeToString(sum[:4])
}

// RecordOverlap persists an overlap (idempotent on the sorted pairwise id).
func (s *Store) RecordOverlap(o Overlap) error {
	if o.MsgA > o.MsgB {
		o.MsgA, o.MsgB = o.MsgB, o.MsgA
	}
	o.ID = OverlapID(o.ProjectID, o.MsgA, o.MsgB)
	now := time.Now().UTC().Format(time.RFC3339)
	if o.FirstSeen == "" {
		o.FirstSeen = now
	}
	_, err := s.DB.Exec(`INSERT INTO overlaps(id,project_id,msg_a,msg_b,tools_a,tools_b,similarity,first_seen)
		VALUES(?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET similarity=excluded.similarity`,
		o.ID, o.ProjectID, o.MsgA, o.MsgB,
		joinJSON(o.ToolsA), joinJSON(o.ToolsB), o.Similarity, o.FirstSeen)
	return err
}

// Overlaps returns every stored overlap, optionally filtered by project.
func (s *Store) Overlaps(projectID string) ([]Overlap, error) {
	q := `SELECT id,project_id,msg_a,msg_b,tools_a,tools_b,similarity,first_seen FROM overlaps`
	args := []any{}
	if projectID != "" {
		q += ` WHERE project_id=?`
		args = append(args, projectID)
	}
	q += ` ORDER BY first_seen DESC`
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Overlap
	for rows.Next() {
		var o Overlap
		var ta, tb string
		if err := rows.Scan(&o.ID, &o.ProjectID, &o.MsgA, &o.MsgB, &ta, &tb, &o.Similarity, &o.FirstSeen); err != nil {
			return nil, err
		}
		o.ToolsA = unjoinJSON(ta)
		o.ToolsB = unjoinJSON(tb)
		out = append(out, o)
	}
	return out, rows.Err()
}
