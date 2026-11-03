package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/jadmadi/sila/internal/config"
)

var (
	fallbackMu         sync.RWMutex
	fallbackAllowed    = true
	fallbackNoticeOnce sync.Once
	fallbackNoticeMu   sync.Mutex
)

// SetFallbackAllowed controls whether automatic fallback to the workspace-local
// store is permitted when the global store is read-only.
func SetFallbackAllowed(allowed bool) {
	fallbackMu.Lock()
	defer fallbackMu.Unlock()
	fallbackAllowed = allowed
}

// IsFallbackAllowed reports whether workspace-local store fallback is permitted.
func IsFallbackAllowed() bool {
	fallbackMu.RLock()
	defer fallbackMu.RUnlock()
	return fallbackAllowed
}

// ResetFallbackNoticeOnce resets the one-time notice state for testing.
func ResetFallbackNoticeOnce() {
	fallbackNoticeMu.Lock()
	defer fallbackNoticeMu.Unlock()
	fallbackNoticeOnce = sync.Once{}
}

func printFallbackNotice() {
	fallbackNoticeOnce.Do(func() {
		fmt.Fprintln(os.Stderr, "[sila] global store is read-only; using workspace store at .sila/sila.db")
	})
}

// IsReadOnlyOrPermissionError reports whether an error indicates a read-only
// filesystem, permission restriction, or SQLite readonly constraint.
func IsReadOnlyOrPermissionError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, os.ErrPermission) || errors.Is(err, syscall.EROFS) || errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EPERM) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "read-only") ||
		strings.Contains(msg, "readonly") ||
		strings.Contains(msg, "permission denied") ||
		strings.Contains(msg, "operation not permitted") ||
		strings.Contains(msg, "1544") ||
		strings.Contains(msg, "erofs") ||
		strings.Contains(msg, "eacces") ||
		strings.Contains(msg, "eperm")
}

// isDefaultStorePath reports whether path corresponds to Sila's primary default store.
func isDefaultStorePath(path string) bool {
	if os.Getenv("SILA_DB") != "" {
		return false
	}
	defaultPath := filepath.Clean(filepath.Join(config.DataDir(), config.PrimaryDBName))
	return filepath.Clean(path) == defaultPath
}

// IsWorkspaceStore reports whether the path points to a workspace-local store (.sila/sila.db).
func IsWorkspaceStore(path string) bool {
	clean := filepath.ToSlash(filepath.Clean(path))
	return strings.HasSuffix(clean, "/.sila/sila.db") ||
		strings.HasSuffix(clean, "/.sila/"+config.PrimaryDBName) ||
		clean == ".sila/"+config.PrimaryDBName
}

// findWorkspaceRoot locates the current workspace or repository root by
// checking git toplevel, walking up looking for .git or .sila, or falling back
// to the current working directory if inside a valid directory.
func findWorkspaceRoot() (string, bool) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", false
	}

	// 1. Try git rev-parse --show-toplevel
	if cmd := exec.Command("git", "rev-parse", "--show-toplevel"); cmd != nil {
		if out, err := cmd.Output(); err == nil {
			top := strings.TrimSpace(string(out))
			if top != "" {
				return top, true
			}
		}
	}

	// 2. Walk up directory hierarchy looking for .git or .sila
	dir := cwd
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir, true
		}
		if _, err := os.Stat(filepath.Join(dir, ".sila")); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir || parent == "" {
			break
		}
		dir = parent
	}

	// 3. If cwd exists and is a directory, use it
	if info, err := os.Stat(cwd); err == nil && info.IsDir() {
		return cwd, true
	}
	return "", false
}

// OpenWithFallback opens the knowledge store at path, falling back to a workspace-local
// store (.sila/sila.db) in workspaceDir (or the detected workspace root) if path is
// the default global store and fails with a read-only or permission error.
func OpenWithFallback(path, workspaceDir string) (*Store, error) {
	st, err := openDirect(path)
	if err == nil {
		return st, nil
	}

	// Determine if fallback should be evaluated
	if !IsReadOnlyOrPermissionError(err) || !IsFallbackAllowed() {
		return nil, err
	}

	// Explicit environment override ($SILA_DB) fails closed unless workspaceDir was explicitly passed
	if os.Getenv("SILA_DB") != "" && workspaceDir == "" {
		return nil, err
	}

	// If path is not the default store and no workspaceDir was provided, fail closed
	if workspaceDir == "" && !isDefaultStorePath(path) {
		return nil, err
	}

	wsRoot := workspaceDir
	if wsRoot == "" {
		detected, ok := findWorkspaceRoot()
		if !ok {
			return nil, err
		}
		wsRoot = detected
	}

	// Verify workspace root exists and is a directory
	wsInfo, statErr := os.Stat(wsRoot)
	if statErr != nil || !wsInfo.IsDir() {
		return nil, err
	}

	localDir := filepath.Join(wsRoot, ".sila")
	if mkErr := os.MkdirAll(localDir, 0o755); mkErr != nil {
		return nil, err
	}

	// Verify .sila directory is actually writable with a probe file
	probeFile := filepath.Join(localDir, fmt.Sprintf(".write_probe_%d", os.Getpid()))
	if f, probeErr := os.OpenFile(probeFile, os.O_CREATE|os.O_RDWR, 0o600); probeErr != nil {
		return nil, err
	} else {
		_ = f.Close()
		_ = os.Remove(probeFile)
	}

	// Automatically ensure .sila/ is ignored in the repository's .gitignore
	_, _ = config.EnsureGitIgnoreWorkspaceStore(wsRoot)

	localDBPath := filepath.Join(localDir, config.PrimaryDBName)
	fallbackStore, openErr := openDirect(localDBPath)
	if openErr != nil {
		return nil, fmt.Errorf("open workspace fallback store %s: %w (global store failed: %v)", localDBPath, openErr, err)
	}

	printFallbackNotice()
	return fallbackStore, nil
}

// DrainStats summarizes records merged from a workspace-local store into the primary store.
type DrainStats struct {
	Messages  int
	Facts     int
	Handoffs  int
	Decisions int
	Feedback  int
}

// Total returns the aggregate count of all drained records.
func (d DrainStats) Total() int {
	return d.Messages + d.Facts + d.Handoffs + d.Decisions + d.Feedback
}

// DrainWorkspaceStore merges unmerged records from a workspace-local store (.sila/sila.db)
// into the receiving primary store.
func (s *Store) DrainWorkspaceStore(wsDBPath string) (DrainStats, error) {
	var stats DrainStats

	cleanWS := filepath.Clean(wsDBPath)
	cleanPrimary := filepath.Clean(s.Path)
	if cleanWS == cleanPrimary {
		return stats, nil
	}

	if _, err := os.Stat(cleanWS); os.IsNotExist(err) {
		return stats, nil
	}

	src, err := OpenRO(cleanWS)
	if err != nil {
		return stats, fmt.Errorf("open workspace store %s for drain: %w", cleanWS, err)
	}
	defer src.Close()

	tx, err := s.DB.Begin()
	if err != nil {
		return stats, fmt.Errorf("begin drain tx: %w", err)
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()

	// 1. Map & ensure projects
	projRows, err := src.DB.Query(`SELECT path, id, created_at FROM projects`)
	if err != nil {
		return stats, fmt.Errorf("query workspace projects: %w", err)
	}
	projMap := make(map[string]string) // srcProjectID -> dstProjectID
	type projRecord struct {
		path, id, createdAt string
	}
	var srcProjects []projRecord
	for projRows.Next() {
		var pr projRecord
		if scanErr := projRows.Scan(&pr.path, &pr.id, &pr.createdAt); scanErr == nil {
			srcProjects = append(srcProjects, pr)
		}
	}
	_ = projRows.Close()

	for _, pr := range srcProjects {
		var dstID string
		err := tx.QueryRow(`SELECT id FROM projects WHERE path = ?`, pr.path).Scan(&dstID)
		if err == sql.ErrNoRows {
			// Insert project
			dstID = pr.id
			_, _ = tx.Exec(`INSERT OR IGNORE INTO projects(path, id, created_at) VALUES (?, ?, ?)`,
				pr.path, pr.id, pr.createdAt)
		}
		projMap[pr.id] = dstID
	}

	mapProject := func(srcID string) string {
		if dst, ok := projMap[srcID]; ok && dst != "" {
			return dst
		}
		return srcID
	}

	// 2. Messages & source occurrences
	msgRows, err := src.DB.Query(`SELECT id, project_id, content_hash, ts, role, content, source_tools, first_seen FROM messages`)
	if err == nil {
		type msgRecord struct {
			srcID                                                       int64
			projectID, contentHash, ts, role, content, tools, firstSeen string
		}
		var msgs []msgRecord
		for msgRows.Next() {
			var m msgRecord
			if scanErr := msgRows.Scan(&m.srcID, &m.projectID, &m.contentHash, &m.ts, &m.role, &m.content, &m.tools, &m.firstSeen); scanErr == nil {
				msgs = append(msgs, m)
			}
		}
		_ = msgRows.Close()

		for _, m := range msgs {
			dstProj := mapProject(m.projectID)
			res, insErr := tx.Exec(`INSERT OR IGNORE INTO messages(project_id, content_hash, ts, role, content, source_tools, first_seen)
				VALUES (?, ?, ?, ?, ?, ?, ?)`, dstProj, m.contentHash, m.ts, m.role, m.content, m.tools, m.firstSeen)
			if insErr == nil {
				if aff, _ := res.RowsAffected(); aff > 0 {
					stats.Messages++
				}
			}

			// Copy occurrences if table exists
			var dstMsgID int64
			if err := tx.QueryRow(`SELECT id FROM messages WHERE project_id = ? AND content_hash = ?`, dstProj, m.contentHash).Scan(&dstMsgID); err == nil {
				occRows, occErr := src.DB.Query(`SELECT tool, session_id, source_location, source_order, source_time FROM source_occurrences WHERE message_id = ?`, m.srcID)
				if occErr == nil {
					for occRows.Next() {
						var tool, sessID, loc, sTime string
						var sOrder int64
						if scanErr := occRows.Scan(&tool, &sessID, &loc, &sOrder, &sTime); scanErr == nil {
							var occExists int
							_ = tx.QueryRow(`SELECT 1 FROM source_occurrences WHERE message_id = ? AND tool = ? AND session_id = ? AND source_order = ?`,
								dstMsgID, tool, sessID, sOrder).Scan(&occExists)
							if occExists == 0 {
								_, _ = tx.Exec(`INSERT INTO source_occurrences(message_id, tool, session_id, source_location, source_order, source_time)
									VALUES (?, ?, ?, ?, ?, ?)`, dstMsgID, tool, sessID, loc, sOrder, sTime)
							}
						}
					}
					_ = occRows.Close()
				}
			}
		}
	}

	// 3. Facts
	factRows, err := src.DB.Query(`SELECT id, project_id, claim, confidence, verified, repo_files, tools, session_ref, first_seen, promoted_at, file_refs_exist, refs_checked_at, kind, review_status, origin_role, fact_hash, prev_hash, tier, salience FROM facts`)
	if err == nil {
		type factRecord struct {
			id, projectID, claim, confidence, repoFiles, tools, sessionRef, firstSeen, promotedAt, refsCheckedAt, kind, reviewStatus, originRole, factHash, prevHash, tier string
			verified, fileRefsExist                                                                                                                                        int
			salience                                                                                                                                                       float64
		}
		var facts []factRecord
		for factRows.Next() {
			var f factRecord
			if scanErr := factRows.Scan(&f.id, &f.projectID, &f.claim, &f.confidence, &f.verified, &f.repoFiles, &f.tools, &f.sessionRef, &f.firstSeen, &f.promotedAt, &f.fileRefsExist, &f.refsCheckedAt, &f.kind, &f.reviewStatus, &f.originRole, &f.factHash, &f.prevHash, &f.tier, &f.salience); scanErr == nil {
				facts = append(facts, f)
			}
		}
		_ = factRows.Close()

		for _, f := range facts {
			dstProj := mapProject(f.projectID)
			res, insErr := tx.Exec(`INSERT OR IGNORE INTO facts(id, project_id, claim, confidence, verified, repo_files, tools, session_ref, first_seen, promoted_at, file_refs_exist, refs_checked_at, kind, review_status, origin_role, fact_hash, prev_hash, tier, salience)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				f.id, dstProj, f.claim, f.confidence, f.verified, f.repoFiles, f.tools, f.sessionRef, f.firstSeen, f.promotedAt, f.fileRefsExist, f.refsCheckedAt, f.kind, f.reviewStatus, f.originRole, f.factHash, f.prevHash, f.tier, f.salience)
			if insErr == nil {
				if aff, _ := res.RowsAffected(); aff > 0 {
					stats.Facts++
				}
			}
		}
	}

	// 4. Handoffs
	handoffRows, err := src.DB.Query(`SELECT id, project_id, agent, branch, summary, open_items, decisions, repo_files, file_refs_exist, created_at, status, claimed_by, claimed_at, session_id FROM handoffs`)
	if err == nil {
		type handoffRecord struct {
			id, projectID, agent, branch, summary, openItems, decisions, repoFiles, createdAt, status, claimedBy, claimedAt, sessionID string
			fileRefsExist                                                                                                              int
		}
		var handoffs []handoffRecord
		for handoffRows.Next() {
			var h handoffRecord
			if scanErr := handoffRows.Scan(&h.id, &h.projectID, &h.agent, &h.branch, &h.summary, &h.openItems, &h.decisions, &h.repoFiles, &h.fileRefsExist, &h.createdAt, &h.status, &h.claimedBy, &h.claimedAt, &h.sessionID); scanErr == nil {
				handoffs = append(handoffs, h)
			}
		}
		_ = handoffRows.Close()

		for _, h := range handoffs {
			dstProj := mapProject(h.projectID)
			res, insErr := tx.Exec(`INSERT OR IGNORE INTO handoffs(id, project_id, agent, branch, summary, open_items, decisions, repo_files, file_refs_exist, created_at, status, claimed_by, claimed_at, session_id)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				h.id, dstProj, h.agent, h.branch, h.summary, h.openItems, h.decisions, h.repoFiles, h.fileRefsExist, h.createdAt, h.status, h.claimedBy, h.claimedAt, h.sessionID)
			if insErr == nil {
				if aff, _ := res.RowsAffected(); aff > 0 {
					stats.Handoffs++
				}
			}
		}
	}

	// 5. Decisions (if table exists)
	var decTableExists int
	_ = src.DB.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='decisions'`).Scan(&decTableExists)
	if decTableExists > 0 {
		decRows, err := src.DB.Query(`SELECT id, project_id, address, seq_number, title, context, options, recommendation, rationale, status, resolution, resolution_notes, resolved_by, resolved_at, created_by, session_id, goal_slug, created_at, updated_at FROM decisions`)
		if err == nil {
			type decRecord struct {
				id, projectID, address, title, context, options, recommendation, rationale, status, resolution, resolutionNotes, resolvedBy, resolvedAt, createdBy, sessionID, goalSlug, createdAt, updatedAt string
				seqNumber                                                                                                                                                                                     int
			}
			var decs []decRecord
			for decRows.Next() {
				var d decRecord
				if scanErr := decRows.Scan(&d.id, &d.projectID, &d.address, &d.seqNumber, &d.title, &d.context, &d.options, &d.recommendation, &d.rationale, &d.status, &d.resolution, &d.resolutionNotes, &d.resolvedBy, &d.resolvedAt, &d.createdBy, &d.sessionID, &d.goalSlug, &d.createdAt, &d.updatedAt); scanErr == nil {
					decs = append(decs, d)
				}
			}
			_ = decRows.Close()

			for _, d := range decs {
				dstProj := mapProject(d.projectID)
				res, insErr := tx.Exec(`INSERT OR IGNORE INTO decisions(id, project_id, address, seq_number, title, context, options, recommendation, rationale, status, resolution, resolution_notes, resolved_by, resolved_at, created_by, session_id, goal_slug, created_at, updated_at)
					VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
					d.id, dstProj, d.address, d.seqNumber, d.title, d.context, d.options, d.recommendation, d.rationale, d.status, d.resolution, d.resolutionNotes, d.resolvedBy, d.resolvedAt, d.createdBy, d.sessionID, d.goalSlug, d.createdAt, d.updatedAt)
				if insErr == nil {
					if aff, _ := res.RowsAffected(); aff > 0 {
						stats.Decisions++
					}
				}
			}
		}
	}

	// 6. Feedback (if table exists)
	var fbTableExists int
	_ = src.DB.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='feedback'`).Scan(&fbTableExists)
	if fbTableExists > 0 {
		fbRows, err := src.DB.Query(`SELECT id, project_id, category, message, status, agent, session_id, created_at, resolved_at FROM feedback`)
		if err == nil {
			type fbRecord struct {
				id, projectID, category, message, status, agent, sessionID, createdAt, resolvedAt string
			}
			var fbs []fbRecord
			for fbRows.Next() {
				var fb fbRecord
				if scanErr := fbRows.Scan(&fb.id, &fb.projectID, &fb.category, &fb.message, &fb.status, &fb.agent, &fb.sessionID, &fb.createdAt, &fb.resolvedAt); scanErr == nil {
					fbs = append(fbs, fb)
				}
			}
			_ = fbRows.Close()

			for _, fb := range fbs {
				dstProj := mapProject(fb.projectID)
				res, insErr := tx.Exec(`INSERT OR IGNORE INTO feedback(id, project_id, category, message, status, agent, session_id, created_at, resolved_at)
					VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
					fb.id, dstProj, fb.category, fb.message, fb.status, fb.agent, fb.sessionID, fb.createdAt, fb.resolvedAt)
				if insErr == nil {
					if aff, _ := res.RowsAffected(); aff > 0 {
						stats.Feedback++
					}
				}
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return stats, fmt.Errorf("commit drain tx: %w", err)
	}
	tx = nil

	// Refresh FTS tables if records were added
	if stats.Facts > 0 {
		_ = s.RebuildFTSIndex("facts_fts")
	}
	if stats.Messages > 0 {
		_ = s.RebuildFTSIndex("messages_fts")
	}

	return stats, nil
}
