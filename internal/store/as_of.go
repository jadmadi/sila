package store

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrAsOfFormat reports an unparseable as-of timestamp.
var ErrAsOfFormat = errors.New("as_of must be YYYY-MM-DD or RFC3339")

// AsOfError names the rejected input and both accepted formats.
type AsOfError struct{ Input string }

func (e *AsOfError) Error() string {
	return fmt.Sprintf("as_of must be YYYY-MM-DD or RFC3339, got %q", e.Input)
}

func (e *AsOfError) Unwrap() error { return ErrAsOfFormat }

// ParseAsOf parses a date or RFC3339 timestamp and clamps future values to now,
// reporting whether it clamped.
func ParseAsOf(input string, now time.Time) (time.Time, bool, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return time.Time{}, false, &AsOfError{Input: input}
	}
	if parsed, err := time.Parse("2006-01-02", input); err == nil {
		return clampAsOf(parsed.UTC(), now)
	}
	if parsed, err := time.Parse(time.RFC3339, input); err == nil {
		return clampAsOf(parsed, now)
	}
	return time.Time{}, false, &AsOfError{Input: input}
}

func clampAsOf(value, now time.Time) (time.Time, bool, error) {
	if !now.IsZero() && value.After(now) {
		return now.UTC(), true, nil
	}
	return value, false, nil
}

// FactVisibleAt reports whether a fact is visible at asOf. Empty timestamps
// count as visible; an empty created timestamp marks a legacy row.
func FactVisibleAt(created, supersededAt, archivedAt, compactedAt string, asOf time.Time) (visible bool, legacy bool) {
	parse := func(value string) (time.Time, bool) {
		if strings.TrimSpace(value) == "" {
			return time.Time{}, false
		}
		parsed, err := time.Parse(time.RFC3339, value)
		if err != nil {
			return time.Time{}, false
		}
		return parsed, true
	}
	if created, ok := parse(created); !ok {
		legacy = true
	} else if created.After(asOf) {
		return false, false
	}
	if superseded, ok := parse(supersededAt); ok && !superseded.After(asOf) {
		return false, legacy
	}
	if archived, ok := parse(archivedAt); ok && !archived.After(asOf) {
		return false, legacy
	}
	if compacted, ok := parse(compactedAt); ok && !compacted.After(asOf) {
		return false, legacy
	}
	return true, legacy
}

// FactsVisibleAt returns project facts visible at asOf, oldest first.
func (s *Store) FactsVisibleAt(projectSel string, asOf time.Time, limit int) ([]Fact, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	matching, err := s.MatchingProjectIDs(projectSel)
	if err != nil {
		return nil, err
	}
	if len(matching) == 0 {
		return nil, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(matching)), ",")
	args := make([]any, 0, len(matching)+3)
	for _, id := range matching {
		args = append(args, id)
	}
	args = append(args, asOf.UTC().Format(time.RFC3339), asOf.UTC().Format(time.RFC3339), limit)
	query := `SELECT ` + selectFactsFields + ` FROM facts f
		WHERE f.project_id IN (` + placeholders + `)
		AND (f.first_seen = '' OR f.first_seen <= ?)
		AND COALESCE(f.review_status, '') != 'archived'
		AND NOT EXISTS (
			SELECT 1 FROM facts s
			WHERE s.supersedes_id = f.id AND s.first_seen != '' AND s.first_seen <= ?
		)
		ORDER BY f.first_seen ASC, f.id ASC LIMIT ?`
	rows, err := s.DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Fact
	for rows.Next() {
		fact, scanErr := scanFact(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		if strings.TrimSpace(fact.FirstSeen) == "" {
			fact.LegacyUnknownTimestamp = true
		}
		out = append(out, fact)
	}
	return out, rows.Err()
}

// MemoryRecordsVisibleAt returns memory records whose lifecycle state at asOf is
// current, oldest first. The latest lifecycle event at or before asOf wins.
func (s *Store) MemoryRecordsVisibleAt(projectSel string, asOf time.Time, limit int) ([]MemoryRecord, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	matching, err := s.MatchingProjectIDs(projectSel)
	if err != nil {
		return nil, err
	}
	if len(matching) == 0 {
		return nil, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(matching)), ",")
	args := make([]any, 0, len(matching)+3)
	for _, id := range matching {
		args = append(args, id)
	}
	args = append(args, asOf.UTC().Format(time.RFC3339), asOf.UTC().Format(time.RFC3339), limit)
	query := memoryRecordSelect + ` WHERE r.project_id IN (` + placeholders + `)
		AND (r.created_at = '' OR r.created_at <= ?)
		AND COALESCE((
			SELECT e.to_state FROM memory_lifecycle e
			WHERE e.memory_id = r.id AND e.created_at <= ?
			ORDER BY e.created_at DESC, e.event_id DESC LIMIT 1
		), r.state) = 'current'
		ORDER BY r.created_at ASC, r.id ASC LIMIT ?`
	rows, err := s.DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MemoryRecord
	for rows.Next() {
		record, scanErr := scanMemoryRecord(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, *record)
	}
	return out, rows.Err()
}
