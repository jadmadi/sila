package store

import (
	"strings"
	"unicode"
)

// NormalizeSymptom lowercases, strips punctuation, and collapses whitespace.
func NormalizeSymptom(text string) string {
	var b strings.Builder
	space := false
	for _, r := range strings.ToLower(text) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if space && b.Len() > 0 {
				b.WriteRune(' ')
			}
			space = false
			b.WriteRune(r)
		default:
			space = true
		}
	}
	return b.String()
}

// bumpRecurrence finds an active lesson with the same normalized symptom,
// increments its count, promotes at three, and returns it unchanged in text.
func (s *Store) bumpRecurrence(projectID, content string) (*Fact, bool, error) {
	key := NormalizeSymptom(content)
	if key == "" {
		return nil, false, nil
	}
	rows, err := s.DB.Query(`SELECT id, claim, recurrence_count FROM facts
		WHERE project_id = ? AND kind = 'lesson' AND COALESCE(review_status, '') != 'archived'`, projectID)
	if err != nil {
		return nil, false, err
	}
	type candidate struct {
		id    string
		claim string
		count int
	}
	var match *candidate
	for rows.Next() {
		var item candidate
		if err := rows.Scan(&item.id, &item.claim, &item.count); err != nil {
			rows.Close()
			return nil, false, err
		}
		if NormalizeSymptom(item.claim) == key {
			found := item
			match = &found
			break
		}
	}
	rows.Close()
	if match == nil {
		return nil, false, nil
	}
	next := match.count + 1
	promoted := 0
	if next >= 3 {
		promoted = 1
	}
	if _, err := s.DB.Exec(`UPDATE facts SET recurrence_count = ?,
		auto_promoted = CASE WHEN ? = 1 THEN 1 ELSE auto_promoted END WHERE id = ?`, next, promoted, match.id); err != nil {
		return nil, false, err
	}
	fact, err := s.FactByID(match.id)
	return fact, true, err
}

// PromotedLessons returns auto-promoted lessons for a project, newest first.
func (s *Store) PromotedLessons(projectID string, limit int) ([]Fact, error) {
	if limit <= 0 {
		limit = 5
	}
	rows, err := s.DB.Query(`SELECT `+selectFactsFields+` FROM facts
		WHERE project_id = ? AND kind = 'lesson' AND auto_promoted = 1
		  AND COALESCE(review_status, '') != 'archived'
		ORDER BY recurrence_count DESC, promoted_at DESC LIMIT ?`, projectID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Fact
	for rows.Next() {
		f, err := scanFact(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}
