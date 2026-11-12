package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func init() {
	migrations = append(migrations, migration{
		version: 47,
		name:    "facts_verification_scope_maturity",
		up: func(tx *sql.Tx) error {
			columns := []struct{ name, ddl string }{
				{"scope_key", `ALTER TABLE facts ADD COLUMN scope_key TEXT NOT NULL DEFAULT ''`},
				{"disputed_by", `ALTER TABLE facts ADD COLUMN disputed_by TEXT NOT NULL DEFAULT ''`},
				{"stale_reason", `ALTER TABLE facts ADD COLUMN stale_reason TEXT NOT NULL DEFAULT ''`},
				{"maturity_tier", `ALTER TABLE facts ADD COLUMN maturity_tier TEXT NOT NULL DEFAULT 'draft'`},
				{"maturity_score", `ALTER TABLE facts ADD COLUMN maturity_score REAL NOT NULL DEFAULT 40`},
				{"recurrence_count", `ALTER TABLE facts ADD COLUMN recurrence_count INTEGER NOT NULL DEFAULT 0`},
				{"auto_promoted", `ALTER TABLE facts ADD COLUMN auto_promoted INTEGER NOT NULL DEFAULT 0`},
				{"valid_from", `ALTER TABLE facts ADD COLUMN valid_from TEXT NOT NULL DEFAULT ''`},
				{"valid_until", `ALTER TABLE facts ADD COLUMN valid_until TEXT NOT NULL DEFAULT ''`},
			}
			for _, column := range columns {
				var count int
				if err := tx.QueryRow(fmt.Sprintf("SELECT count(*) FROM pragma_table_info('facts') WHERE name='%s'", column.name)).Scan(&count); err != nil {
					return err
				}
				if count == 0 {
					if _, err := tx.Exec(column.ddl); err != nil {
						return fmt.Errorf("migration 47 add %s: %w", column.name, err)
					}
				}
			}
			for _, stmt := range []string{
				`CREATE INDEX IF NOT EXISTS idx_facts_scope ON facts(project_id, scope_key);`,
				`CREATE INDEX IF NOT EXISTS idx_facts_maturity ON facts(project_id, maturity_tier);`,
			} {
				if _, err := tx.Exec(stmt); err != nil {
					return err
				}
			}
			return backfillScopeKeys(tx)
		},
	})
}

// backfillScopeKeys fills scope_key for memo facts that predate migration 47.
func backfillScopeKeys(tx *sql.Tx) error {
	rows, err := tx.Query(`SELECT id, repo_files FROM facts WHERE kind = 'memo' AND scope_key = ''`)
	if err != nil {
		return err
	}
	type backfill struct {
		id  string
		key string
	}
	var updates []backfill
	for rows.Next() {
		var id, repoJSON string
		if err := rows.Scan(&id, &repoJSON); err != nil {
			rows.Close()
			return err
		}
		var files []string
		_ = json.Unmarshal([]byte(repoJSON), &files)
		if key := ScopeKey(files); key != "" {
			updates = append(updates, backfill{id: id, key: key})
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, update := range updates {
		if _, err := tx.Exec(`UPDATE facts SET scope_key = ? WHERE id = ?`, update.key, update.id); err != nil {
			return err
		}
	}
	return nil
}

// DefaultFactTTL is the read-time reference lifetime.

const DefaultFactTTL = 30 * 24 * time.Hour

// VerificationState is the effective state of one fact.
type VerificationState string

const (
	VerificationVerified   VerificationState = "verified"
	VerificationStale      VerificationState = "stale"
	VerificationUnverified VerificationState = "unverified"
	VerificationDisputed   VerificationState = "disputed"
)

// FactCounts is the four-state census plus broken-chain rows.
type FactCounts struct {
	Verified      int
	Stale         int
	Unverified    int
	Disputed      int
	BrokenChain   int
	HiddenExpired int
}

// ScopeKey hashes the sorted, de-duplicated, cleaned repository paths. A fact
// with no files has no scope key.
func ScopeKey(files []string) string {
	seen := map[string]bool{}
	normalized := make([]string, 0, len(files))
	for _, file := range files {
		clean := filepath.ToSlash(filepath.Clean(strings.TrimSpace(file)))
		if clean == "" || clean == "." || seen[clean] {
			continue
		}
		seen[clean] = true
		normalized = append(normalized, clean)
	}
	if len(normalized) == 0 {
		return ""
	}
	sort.Strings(normalized)
	encoded, _ := json.Marshal(normalized)
	sum := sha256.Sum256(encoded)
	return "scope-" + hex.EncodeToString(sum[:])[:16]
}

// ClassifyFact applies the precedence: disputed, stale, unverified, verified.
// A zero TTL means references never expire; future check times clamp to now.
func ClassifyFact(f Fact, now time.Time, ttl time.Duration) VerificationState {
	if strings.TrimSpace(f.DisputedBy) != "" {
		return VerificationDisputed
	}
	if f.IsStaleFlag {
		return VerificationStale
	}
	if f.PrevHash == "" && f.FactHash != "" && f.FactHash != GenesisHash {
		return VerificationStale
	}
	checked := time.Time{}
	if strings.TrimSpace(f.RefsCheckedAt) != "" {
		if parsed, err := time.Parse(time.RFC3339, f.RefsCheckedAt); err == nil {
			checked = parsed.UTC()
		}
	}
	if !checked.IsZero() {
		if checked.After(now) {
			checked = now
		}
		if ttl > 0 && now.Sub(checked) > ttl {
			return VerificationStale
		}
	}
	if !f.FileRefsExist || checked.IsZero() {
		return VerificationUnverified
	}
	return VerificationVerified
}

// FactCounts classifies every fact in one query.
func (s *Store) FactCounts(projectID string, now time.Time, ttl time.Duration) (FactCounts, error) {
	var counts FactCounts
	query := `SELECT id, claim, kind, confidence, confidence, file_refs_exist, repo_files, tools, session_ref,
		first_seen, promoted_at, refs_checked_at, fact_hash, prev_hash, is_stale, disputed_by
		FROM facts WHERE 1=1`
	args := []any{}
	if projectID != "" {
		query += ` AND project_id = ?`
		args = append(args, projectID)
	}
	rows, err := s.DB.Query(query, args...)
	if err != nil {
		return counts, err
	}
	defer rows.Close()
	for rows.Next() {
		var f Fact
		var repoJSON, toolsJSON string
		var fileRefs, stale int
		var disputed string
		if err := rows.Scan(&f.ID, &f.Claim, &f.Kind, &f.Confidence, &f.Corroboration, &fileRefs, &repoJSON, &toolsJSON,
			&f.SessionRef, &f.FirstSeen, &f.PromotedAt, &f.RefsCheckedAt, &f.FactHash, &f.PrevHash, &stale, &disputed); err != nil {
			return counts, err
		}
		f.FileRefsExist = fileRefs == 1
		f.IsStaleFlag = stale == 1
		f.DisputedBy = disputed
		_ = json.Unmarshal([]byte(repoJSON), &f.RepoFiles)
		_ = json.Unmarshal([]byte(toolsJSON), &f.Tools)
		switch ClassifyFact(f, now, ttl) {
		case VerificationVerified:
			counts.Verified++
		case VerificationStale:
			counts.Stale++
		case VerificationDisputed:
			counts.Disputed++
		default:
			counts.Unverified++
		}
		if f.PrevHash == "" && f.FactHash != "" && f.FactHash != GenesisHash {
			counts.BrokenChain++
		}
	}
	return counts, rows.Err()
}
