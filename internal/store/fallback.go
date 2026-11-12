package store

import (
	"context"
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

// findWorkspaceRoot locates a real repository or workspace root. It never
// accepts bare home, temp, or non-repository state directories.
func findWorkspaceRoot() (string, bool) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", false
	}
	if out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output(); err == nil {
		top := strings.TrimSpace(string(out))
		if top != "" && IsRepoOrWorkspaceDir(top) && !config.IsForbiddenLocalStateDir(top) {
			return top, true
		}
	}
	dir := cwd
	for {
		if IsRepoOrWorkspaceDir(dir) && !config.IsForbiddenLocalStateDir(dir) {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir || parent == "" {
			break
		}
		dir = parent
	}
	return "", false
}

// WorkspaceRootFromStorePath returns the owning workspace root for a store at
// <root>/.sila/sila.db.
func WorkspaceRootFromStorePath(stPath string) (string, bool) {
	if !IsWorkspaceStore(stPath) {
		return "", false
	}
	abs, err := filepath.Abs(stPath)
	if err != nil {
		return "", false
	}
	return filepath.Dir(filepath.Dir(abs)), true
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

	if config.IsForbiddenLocalStateDir(wsRoot) {
		return nil, err
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
	Messages        int
	Facts           int
	Handoffs        int
	Decisions       int
	Feedback        int
	MemoryRecords   int
	MemoryLifecycle int
}

// Total returns the aggregate count of all drained records.
func (d DrainStats) Total() int {
	return d.Messages + d.Facts + d.Handoffs + d.Decisions + d.Feedback + d.MemoryRecords + d.MemoryLifecycle
}

// DrainWorkspaceStore merges unmerged records from a workspace-local store
// (.sila/sila.db) into the receiving primary store using a schema-complete
// ATTACH DATABASE merge. It does not delete the workspace store.
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
	ctx := context.Background()
	conn, err := s.DB.Conn(ctx)
	if err != nil {
		return stats, fmt.Errorf("open primary connection for drain: %w", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `ATTACH DATABASE ? AS ws`, cleanWS); err != nil {
		return stats, fmt.Errorf("attach workspace store %s: %w", cleanWS, err)
	}
	defer func() { _, _ = conn.ExecContext(ctx, `DETACH DATABASE ws`) }()

	projectRows, err := conn.QueryContext(ctx, `SELECT path, id, created_at FROM ws.projects`)
	if err != nil {
		return stats, fmt.Errorf("query workspace projects: %w", err)
	}
	type wsProject struct{ path, id, createdAt string }
	var projects []wsProject
	for projectRows.Next() {
		var project wsProject
		if scanErr := projectRows.Scan(&project.path, &project.id, &project.createdAt); scanErr == nil {
			projects = append(projects, project)
		}
	}
	projectRows.Close()
	for _, project := range projects {
		if project.path == "" || config.IsForbiddenLocalStateDir(project.path) {
			continue
		}
		if _, err := conn.ExecContext(ctx, `INSERT OR IGNORE INTO projects(path, id, created_at) VALUES (?, ?, ?)`, project.path, project.id, project.createdAt); err != nil {
			return stats, fmt.Errorf("merge project %s: %w", project.path, err)
		}
	}

	tables := []struct {
		name   string
		target *int
	}{
		{"messages", &stats.Messages},
		{"facts", &stats.Facts},
		{"handoffs", &stats.Handoffs},
		{"decisions", &stats.Decisions},
		{"feedback", &stats.Feedback},
		{"retrieval_feedback", &stats.Feedback},
		{"memory_records", &stats.MemoryRecords},
		{"memory_lifecycle", &stats.MemoryLifecycle},
	}
	for _, table := range tables {
		columns, colErr := sharedColumns(ctx, conn, table.name)
		if colErr != nil {
			return stats, colErr
		}
		if len(columns) == 0 {
			continue
		}
		quoted := make([]string, len(columns))
		for i, column := range columns {
			quoted[i] = `"` + column + `"`
		}
		query := `INSERT OR IGNORE INTO main."` + table.name + `" (` + strings.Join(quoted, ", ") + `) SELECT ` + strings.Join(quoted, ", ") + ` FROM ws."` + table.name + `"`
		res, execErr := conn.ExecContext(ctx, query)
		if execErr != nil {
			return stats, fmt.Errorf("merge %s: %w", table.name, execErr)
		}
		if affected, affErr := res.RowsAffected(); affErr == nil && affected > 0 {
			*table.target += int(affected)
		}
	}

	return stats, nil
}

// sharedColumns returns the columns present in both main and ws for a table.
func sharedColumns(ctx context.Context, conn *sql.Conn, table string) ([]string, error) {
	mainCols, err := drainTableColumns(ctx, conn, "main", table)
	if err != nil {
		return nil, err
	}
	wsCols, err := drainTableColumns(ctx, conn, "ws", table)
	if err != nil {
		return nil, err
	}
	wsSet := make(map[string]bool, len(wsCols))
	for _, column := range wsCols {
		wsSet[column] = true
	}
	shared := make([]string, 0, len(mainCols))
	for _, column := range mainCols {
		if wsSet[column] {
			shared = append(shared, column)
		}
	}
	return shared, nil
}

func drainTableColumns(ctx context.Context, conn *sql.Conn, schema, table string) ([]string, error) {
	rows, err := conn.QueryContext(ctx, `PRAGMA `+schema+`.table_info("`+table+`")`)
	if err != nil {
		return nil, fmt.Errorf("read %s.%s columns: %w", schema, table, err)
	}
	defer rows.Close()
	var columns []string
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull int
		var defaultValue any
		var primaryKey int
		if scanErr := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); scanErr != nil {
			continue
		}
		columns = append(columns, name)
	}
	return columns, nil
}
