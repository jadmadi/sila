package store

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// FactFilter selects which verification states a read may include. The zero
// value is the strict default: only verified, in-window rows.
type FactFilter struct {
	IncludeUnverified bool
	IncludeStale      bool
	IncludeDisputed   bool
	IncludeExpired    bool
}

// FactHiddenCounts reports rows the default filter excluded.
type FactHiddenCounts struct {
	Unverified int `json:"hidden_unverified"`
	Stale      int `json:"hidden_stale"`
	Disputed   int `json:"hidden_disputed"`
	Expired    int `json:"hidden_expired"`
}

// FactRead is one filtered read result.
type FactRead struct {
	Facts  []Fact           `json:"facts"`
	Hidden FactHiddenCounts `json:"hidden"`
}

// QueryFactsFiltered returns facts under the filter. Archived and superseded
// rows never appear; rows outside their validity window are hidden unless
// IncludeExpired is set. Hidden counts come from the same scan.
func (s *Store) QueryFactsFiltered(projectID string, filter FactFilter, now time.Time, ttl time.Duration, limit int) (FactRead, error) {
	var read FactRead
	query := `SELECT id, project_id, claim, kind, confidence, file_refs_exist, repo_files, tools, session_ref,
		first_seen, promoted_at, refs_checked_at, fact_hash, prev_hash, is_stale, stale_reason, disputed_by,
		review_status, supersedes_id, valid_from, valid_until, visibility
		FROM facts
		WHERE COALESCE(review_status, '') != 'archived'
		  AND id NOT IN (SELECT supersedes_id FROM facts WHERE COALESCE(supersedes_id, '') != '')`
	args := []any{}
	if strings.TrimSpace(projectID) != "" {
		query += ` AND project_id = ?`
		args = append(args, projectID)
	}
	query += ` ORDER BY promoted_at DESC, id`
	rows, err := s.DB.Query(query, args...)
	if err != nil {
		return read, err
	}
	defer rows.Close()
	for rows.Next() {
		var f Fact
		var repoJSON, toolsJSON string
		var fileRefs, stale int
		if err := rows.Scan(&f.ID, &f.ProjectID, &f.Claim, &f.Kind, &f.Confidence, &fileRefs, &repoJSON, &toolsJSON,
			&f.SessionRef, &f.FirstSeen, &f.PromotedAt, &f.RefsCheckedAt, &f.FactHash, &f.PrevHash, &stale,
			&f.StaleReason, &f.DisputedBy, &f.ReviewStatus, &f.SupersedesID, &f.ValidFrom, &f.ValidUntil, &f.Visibility); err != nil {
			return read, err
		}
		f.FileRefsExist = fileRefs == 1
		f.IsStaleFlag = stale == 1
		_ = json.Unmarshal([]byte(repoJSON), &f.RepoFiles)
		_ = json.Unmarshal([]byte(toolsJSON), &f.Tools)

		if !InValidityWindow(f, now) {
			if filter.IncludeExpired {
				read.Facts = append(read.Facts, f)
			} else {
				read.Hidden.Expired++
			}
			continue
		}
		switch ClassifyFact(f, now, ttl) {
		case VerificationVerified:
			read.Facts = append(read.Facts, f)
		case VerificationStale:
			if filter.IncludeStale {
				read.Facts = append(read.Facts, f)
			} else {
				read.Hidden.Stale++
			}
		case VerificationDisputed:
			if filter.IncludeDisputed {
				read.Facts = append(read.Facts, f)
			} else {
				read.Hidden.Disputed++
			}
		default:
			if filter.IncludeUnverified {
				read.Facts = append(read.Facts, f)
			} else {
				read.Hidden.Unverified++
			}
		}
	}
	if limit > 0 && len(read.Facts) > limit {
		read.Facts = read.Facts[:limit]
	}
	return read, rows.Err()
}

// InValidityWindow reports whether now is inside the window: valid_from is
// inclusive and valid_until is exclusive. Empty bounds are unbounded.
func InValidityWindow(f Fact, now time.Time) bool {
	if strings.TrimSpace(f.ValidFrom) != "" {
		if from, err := time.Parse(time.RFC3339, f.ValidFrom); err == nil && now.Before(from) {
			return false
		}
	}
	if strings.TrimSpace(f.ValidUntil) != "" {
		if until, err := time.Parse(time.RFC3339, f.ValidUntil); err == nil && !now.Before(until) {
			return false
		}
	}
	return true
}

// ValidateValidityWindow checks RFC3339 bounds and requires from < until.
func ValidateValidityWindow(from, until string) error {
	var fromTime, untilTime time.Time
	if strings.TrimSpace(from) != "" {
		parsed, err := time.Parse(time.RFC3339, from)
		if err != nil {
			return fmt.Errorf("valid_from must be RFC3339: %w", err)
		}
		fromTime = parsed
	}
	if strings.TrimSpace(until) != "" {
		parsed, err := time.Parse(time.RFC3339, until)
		if err != nil {
			return fmt.Errorf("valid_until must be RFC3339: %w", err)
		}
		untilTime = parsed
	}
	if !fromTime.IsZero() && !untilTime.IsZero() && !fromTime.Before(untilTime) {
		return fmt.Errorf("valid_from must be before valid_until")
	}
	return nil
}
