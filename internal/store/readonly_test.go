package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestOpenInspection_ReadOnlySupportedStore(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "k.db")

	// 1. Create a normal store with data
	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	p, err := st.EnsureProject(dir)
	if err != nil {
		t.Fatalf("EnsureProject failed: %v", err)
	}
	if err := st.InsertHandoff(Handoff{
		ProjectID: p.ID,
		Agent:     "test-agent",
		Summary:   "Handoff before making store readonly",
		CreatedAt: "2026-09-25T00:00:00Z",
	}); err != nil {
		t.Fatalf("InsertHandoff failed: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// 2. Make directory and file read-only (chmod 0555 on dir, 0444 on files)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if err := os.Chmod(filepath.Join(dir, e.Name()), 0444); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(dir, 0555); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.Chmod(dir, 0755)
		for _, e := range entries {
			_ = os.Chmod(filepath.Join(dir, e.Name()), 0644)
		}
	}()

	// 3. OpenInspection must succeed without chmod, migration, or write error 1544
	inspSt, exists, err := OpenInspection(dbPath)
	if err != nil {
		t.Fatalf("OpenInspection failed on read-only store: %v", err)
	}
	if !exists || inspSt == nil {
		t.Fatalf("expected store to exist, got exists=%v, inspSt=%v", exists, inspSt)
	}
	defer inspSt.Close()

	// 4. Verify querying works
	proj, ok, err := inspSt.ProjectForDir(dir)
	if err != nil || !ok {
		t.Fatalf("ProjectForDir failed: ok=%v, err=%v", ok, err)
	}
	if proj.ID != p.ID {
		t.Fatalf("expected project ID %q, got %q", p.ID, proj.ID)
	}

	h, err := inspSt.LatestHandoff(p.ID)
	if err != nil || h == nil {
		t.Fatalf("LatestHandoff failed: h=%v, err=%v", h, err)
	}
	if h.Summary != "Handoff before making store readonly" {
		t.Fatalf("unexpected handoff summary: %s", h.Summary)
	}
}

func TestOpenInspection_AbsentStore(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "absent.db")

	inspSt, exists, err := OpenInspection(dbPath)
	if err != nil {
		t.Fatalf("OpenInspection failed for absent store: %v", err)
	}
	if exists || inspSt != nil {
		t.Fatalf("expected store to not exist, got exists=%v, inspSt=%v", exists, inspSt)
	}

	// Verify no file was created
	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		t.Fatalf("absent store path should not exist on disk, got err=%v", err)
	}
}

func TestOpenInspection_FutureSchema(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "future.db")

	// Create a store and stamp it with future version
	st, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec("PRAGMA user_version = 999"); err != nil {
		st.Close()
		t.Fatal(err)
	}
	_ = st.Close()

	inspSt, exists, err := OpenInspection(dbPath)
	if err == nil {
		if inspSt != nil {
			inspSt.Close()
		}
		t.Fatal("expected OpenInspection to fail for future schema version")
	}
	if exists {
		t.Errorf("expected exists=false on error, got true")
	}
	if !strings.Contains(err.Error(), "newer than this sila build supports") {
		t.Fatalf("expected future schema error message, got: %v", err)
	}
}

func TestOpenInspection_CorruptFile(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "corrupt.db")

	// Write garbage bytes
	if err := os.WriteFile(dbPath, []byte("NOT A VALID SQLITE DATABASE FILE GARBAGE DATA"), 0644); err != nil {
		t.Fatal(err)
	}

	inspSt, exists, err := OpenInspection(dbPath)
	if err == nil {
		if inspSt != nil {
			inspSt.Close()
		}
		t.Fatal("expected OpenInspection to fail for corrupt database file")
	}
	if exists {
		t.Errorf("expected exists=false on error, got true")
	}
}

func TestOpenRO_ReadOnlyFilesystem(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "k.db")

	st, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_ = st.Close()

	// Make dir and file read-only
	if err := os.Chmod(dbPath, 0444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0555); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.Chmod(dir, 0755)
		_ = os.Chmod(dbPath, 0644)
	}()

	roSt, err := OpenRO(dbPath)
	if err != nil {
		t.Fatalf("OpenRO failed on read-only dir/file: %v", err)
	}
	defer roSt.Close()

	var uv int
	if err := roSt.DB.QueryRow("PRAGMA user_version").Scan(&uv); err != nil {
		t.Fatalf("PRAGMA user_version failed: %v", err)
	}
	if uv != CurrentSchemaVersion() {
		t.Fatalf("expected user_version=%d, got %d", CurrentSchemaVersion(), uv)
	}
}

func TestOpen_ReadOnlyGlobalStoreFallback(t *testing.T) {
	tmp := t.TempDir()
	globalDir := filepath.Join(tmp, "global_share", "sila")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("XDG_DATA_HOME", filepath.Join(tmp, "global_share"))
	t.Setenv("SILA_DB", "")
	SetFallbackAllowed(true)
	ResetFallbackNoticeOnce()

	// Make global directory read-only
	if err := os.Chmod(globalDir, 0o555); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.Chmod(globalDir, 0o755)
	}()

	wsDir := filepath.Join(tmp, "workspace")
	if err := os.MkdirAll(filepath.Join(wsDir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(wsDir)

	st, err := Open(DefaultDBPath())
	if err != nil {
		t.Fatalf("Open should have fallen back cleanly to workspace store, got err: %v", err)
	}
	defer st.Close()

	expectedWSDB := filepath.Join(wsDir, ".sila", "sila.db")
	if filepath.Clean(st.Path) != filepath.Clean(expectedWSDB) {
		t.Fatalf("expected store path %s, got %s", expectedWSDB, st.Path)
	}

	// Verify .gitignore in workspace has .sila/
	ignoreContent, err := os.ReadFile(filepath.Join(wsDir, ".gitignore"))
	if err != nil {
		t.Fatalf("reading .gitignore: %v", err)
	}
	if !strings.Contains(string(ignoreContent), ".sila/") {
		t.Fatalf("expected .gitignore to contain .sila/, got:\n%s", string(ignoreContent))
	}

	// Verify write operations succeed on the fallback store
	p, err := st.EnsureProject(wsDir)
	if err != nil {
		t.Fatalf("EnsureProject failed on fallback store: %v", err)
	}
	if err := st.InsertHandoff(Handoff{
		ProjectID: p.ID,
		Agent:     "codex-sandbox",
		Summary:   "Verified fallback store write",
		CreatedAt: "2026-10-06T20:00:00Z",
		Status:    "pending",
	}); err != nil {
		t.Fatalf("InsertHandoff failed on fallback store: %v", err)
	}

	h, err := st.LatestHandoff(p.ID)
	if err != nil || h == nil || h.Summary != "Verified fallback store write" {
		t.Fatalf("unexpected handoff result: %v", h)
	}
}

func TestOpen_ExplicitOverrideFailsClosed(t *testing.T) {
	tmp := t.TempDir()
	globalDir := filepath.Join(tmp, "global_share", "sila")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(globalDir, 0o555); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.Chmod(globalDir, 0o755)
	}()

	wsDir := filepath.Join(tmp, "workspace")
	if err := os.MkdirAll(filepath.Join(wsDir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(wsDir)

	// 1. SILA_DB set to unwritable path fails closed
	t.Setenv("SILA_DB", filepath.Join(globalDir, "sila.db"))
	st, err := Open(DefaultDBPath())
	if err == nil {
		if st != nil {
			st.Close()
		}
		t.Fatalf("expected error when SILA_DB override is unwritable, got success")
	}
	if !IsReadOnlyOrPermissionError(err) {
		t.Fatalf("expected read-only/permission error, got: %v", err)
	}

	// 2. SetFallbackAllowed(false) fails closed even if SILA_DB is unset
	t.Setenv("SILA_DB", "")
	t.Setenv("XDG_DATA_HOME", filepath.Join(tmp, "global_share"))
	SetFallbackAllowed(false)
	defer SetFallbackAllowed(true)

	st2, err2 := Open(DefaultDBPath())
	if err2 == nil {
		if st2 != nil {
			st2.Close()
		}
		t.Fatalf("expected error when fallback is disallowed, got success")
	}
	if !IsReadOnlyOrPermissionError(err2) {
		t.Fatalf("expected read-only/permission error, got: %v", err2)
	}
}

func TestDrainWorkspaceStore(t *testing.T) {
	tmp := t.TempDir()
	wsDBPath := filepath.Join(tmp, "workspace", ".sila", "sila.db")
	primaryDBPath := filepath.Join(tmp, "global", "sila.db")

	wsSt, err := Open(wsDBPath)
	if err != nil {
		t.Fatalf("Open wsSt: %v", err)
	}
	wsProj, err := wsSt.EnsureProject(filepath.Join(tmp, "workspace"))
	if err != nil {
		t.Fatalf("EnsureProject: %v", err)
	}

	// Insert handoff into ws store
	if err := wsSt.InsertHandoff(Handoff{
		ProjectID: wsProj.ID,
		Agent:     "sandboxed-agent",
		Summary:   "Handoff from sandbox",
		CreatedAt: "2026-10-06T20:00:00Z",
		Status:    "pending",
	}); err != nil {
		t.Fatalf("InsertHandoff: %v", err)
	}

	// Insert decision into ws store
	if _, err := wsSt.CreateDecision(&Decision{
		ProjectID: wsProj.ID,
		Title:     "Sandbox storage fallback decision",
		Options: []DecisionOption{
			{Letter: "A", Title: "Opt A"},
			{Letter: "B", Title: "Opt B"},
		},
		Recommendation: "A",
		Status:         "resolved",
	}); err != nil {
		t.Fatalf("CreateDecision: %v", err)
	}
	_ = wsSt.Close()

	// Now open primary store
	primarySt, err := Open(primaryDBPath)
	if err != nil {
		t.Fatalf("Open primarySt: %v", err)
	}
	defer primarySt.Close()

	// Drain
	stats, err := primarySt.DrainWorkspaceStore(wsDBPath)
	if err != nil {
		t.Fatalf("DrainWorkspaceStore failed: %v", err)
	}
	if stats.Handoffs != 1 {
		t.Errorf("expected 1 handoff drained, got %d", stats.Handoffs)
	}
	if stats.Decisions != 1 {
		t.Errorf("expected 1 decision drained, got %d", stats.Decisions)
	}

	// Verify handoff in primary store
	h, err := primarySt.LatestHandoff(wsProj.ID)
	if err != nil || h == nil || h.Summary != "Handoff from sandbox" {
		t.Fatalf("expected handoff in primary store, got: %v (err %v)", h, err)
	}

	// Second drain should be idempotent (0 new items)
	stats2, err := primarySt.DrainWorkspaceStore(wsDBPath)
	if err != nil {
		t.Fatalf("second DrainWorkspaceStore failed: %v", err)
	}
	if stats2.Total() != 0 {
		t.Errorf("expected 0 items on second drain, got %d", stats2.Total())
	}
}

// Suppress unused import warning if any
var _ = sql.ErrNoRows

func TestIsReadOnlyOrPermissionError_EPERM(t *testing.T) {
	if !IsReadOnlyOrPermissionError(syscall.EPERM) {
		t.Errorf("expected syscall.EPERM to be classified as read-only or permission error")
	}
	if !IsReadOnlyOrPermissionError(errors.New("operation not permitted")) {
		t.Errorf("expected 'operation not permitted' to be classified as read-only or permission error")
	}
	if !IsReadOnlyOrPermissionError(errors.New("chown /db: eperm")) {
		t.Errorf("expected 'eperm' string to be classified as read-only or permission error")
	}
	if IsReadOnlyOrPermissionError(errors.New("syntax error in SQL")) {
		t.Errorf("expected syntax error NOT to be classified as read-only or permission error")
	}
}

func TestSecurePermissions_ReadOnlyStore(t *testing.T) {
	s := &Store{ReadOnly: true, Path: "/nonexistent/path/to/db.db"}
	if err := s.SecurePermissions(); err != nil {
		t.Errorf("SecurePermissions on ReadOnly store should return nil without attempting chmod, got %v", err)
	}
}

func TestOpenInspection_UninitializedSchema(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "uninit.db")

	// Create an empty database file with user_version = 0
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("PRAGMA user_version = 0"); err != nil {
		db.Close()
		t.Fatal(err)
	}
	_ = db.Close()

	inspSt, exists, err := OpenInspection(dbPath)
	if err == nil {
		if inspSt != nil {
			inspSt.Close()
		}
		t.Fatal("expected OpenInspection to fail for uninitialized schema v0")
	}
	if exists {
		t.Errorf("expected exists=false on error, got true")
	}
	if !strings.Contains(err.Error(), "uninitialized schema v0") {
		t.Fatalf("expected uninitialized schema error, got: %v", err)
	}
}

func TestOpenRO_IncompatibleOlderStoreOnReadOnlyFilesystem(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "old_ro.db")

	// Create a store and stamp it with older schema version
	st, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec("PRAGMA user_version = 1"); err != nil {
		st.Close()
		t.Fatal(err)
	}
	_ = st.Close()

	// Make file and dir read-only
	if err := os.Chmod(dbPath, 0444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0555); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.Chmod(dir, 0755)
		_ = os.Chmod(dbPath, 0644)
	}()

	roSt, err := OpenRO(dbPath)
	if err == nil {
		if roSt != nil {
			roSt.Close()
		}
		t.Fatal("expected OpenRO to fail on older store on read-only filesystem without mutating")
	}
	if !strings.Contains(err.Error(), "cannot migrate read-only store") && !strings.Contains(err.Error(), "incompatible") {
		t.Fatalf("expected actionable incompatible read-only error message, got: %v", err)
	}

	// Verify permissions remained 0444 and directory remained 0555
	fi, err := os.Stat(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0444 {
		t.Errorf("expected perm 0444, got %v", fi.Mode().Perm())
	}
}

func TestSecurePermissions_AlreadySecureDoesNotChmod(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "secure.db")

	// Create file with 0600
	if err := os.WriteFile(dbPath, []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}

	s := &Store{ReadOnly: false, Path: dbPath}
	if err := s.SecurePermissions(); err != nil {
		t.Fatalf("expected nil error on already secure file, got: %v", err)
	}
}

func TestInspection_NoFilesCreatedOrModified(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "inspect_pure.db")

	st, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	p, err := st.EnsureProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.InsertHandoff(Handoff{
		ProjectID: p.ID,
		Agent:     "test",
		Summary:   "summary",
		CreatedAt: "2026-09-25T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()

	// Record directory contents, sizes, and mtimes
	entriesBefore, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	statsBefore := make(map[string]os.FileInfo)
	for _, e := range entriesBefore {
		info, err := e.Info()
		if err != nil {
			t.Fatal(err)
		}
		statsBefore[e.Name()] = info
	}

	// Open for inspection and query
	inspSt, exists, err := OpenInspection(dbPath)
	if err != nil || !exists || inspSt == nil {
		t.Fatalf("OpenInspection failed: %v", err)
	}
	h, err := inspSt.LatestHandoff(p.ID)
	if err != nil || h == nil {
		t.Fatalf("LatestHandoff failed: %v", err)
	}
	_ = inspSt.Close()

	// Verify no new files were created
	entriesAfter, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entriesAfter) != len(entriesBefore) {
		t.Fatalf("file count changed: before %d, after %d", len(entriesBefore), len(entriesAfter))
	}
	for _, e := range entriesAfter {
		infoBefore, ok := statsBefore[e.Name()]
		if !ok {
			t.Fatalf("unexpected new file created during inspection: %s", e.Name())
		}
		infoAfter, err := e.Info()
		if err != nil {
			t.Fatal(err)
		}
		if infoAfter.Size() != infoBefore.Size() {
			t.Errorf("file %s size changed: was %d, now %d", e.Name(), infoBefore.Size(), infoAfter.Size())
		}
	}
}

func TestOpenRO_AllInspectionQueriesOnReadOnlyStore(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "inspect_all.db")

	st, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	p, err := st.EnsureProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.InsertHandoff(Handoff{
		ProjectID: p.ID,
		Agent:     "test-agent",
		Summary:   "inspection handoff",
		CreatedAt: "2026-09-25T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertFact(Fact{
		ProjectID:  p.ID,
		Claim:      "test symptom --cause=test cause --fix=test fix --tags=go,test",
		Kind:       "lesson",
		Confidence: "proven",
		FirstSeen:  "2026-09-25T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertFact(Fact{
		ProjectID:  p.ID,
		Claim:      "case title: case description",
		Kind:       "case",
		Confidence: "proven",
		FirstSeen:  "2026-09-25T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateDecision(&Decision{
		ProjectID: p.ID,
		Title:     "architecture decision",
		Options: []DecisionOption{
			{Letter: "A", Title: "Opt A"},
			{Letter: "B", Title: "Opt B"},
		},
		Recommendation: "A",
		Status:         "resolved",
	}); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()

	// Make file 0444 and directory 0555
	if err := os.Chmod(dbPath, 0444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0555); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.Chmod(dir, 0755)
		_ = os.Chmod(dbPath, 0644)
	}()

	// Open with OpenRO
	roSt, err := OpenRO(dbPath)
	if err != nil {
		t.Fatalf("OpenRO failed on read-only store: %v", err)
	}
	defer roSt.Close()

	// 1. Query lessons
	lessons, err := roSt.LessonFacts(p.ID, 100)
	if err != nil {
		t.Fatalf("LessonFacts failed: %v", err)
	}
	if len(lessons) != 1 {
		t.Errorf("expected 1 lesson, got %d", len(lessons))
	}

	// 2. Query cases
	cases, err := roSt.CaseFacts(p.ID, 100)
	if err != nil {
		t.Fatalf("CaseFacts failed: %v", err)
	}
	if len(cases) != 1 {
		t.Errorf("expected 1 case, got %d", len(cases))
	}

	// 3. Query decisions
	decisions, err := roSt.ListDecisions(DecisionFilter{ProjectID: p.ID})
	if err != nil {
		t.Fatalf("ListDecisions failed: %v", err)
	}
	if len(decisions) != 1 {
		t.Errorf("expected 1 decision, got %d", len(decisions))
	}

	// 4. Query SchemaVersion
	v, err := roSt.SchemaVersion()
	if err != nil {
		t.Fatalf("SchemaVersion failed: %v", err)
	}
	if v != CurrentSchemaVersion() {
		t.Errorf("expected schema version %d, got %d", CurrentSchemaVersion(), v)
	}

	// 5. Query Stats
	stats, err := roSt.Stats()
	if err != nil {
		t.Fatalf("Stats failed: %v", err)
	}
	if stats.PageCount <= 0 {
		t.Errorf("expected PageCount > 0, got %d", stats.PageCount)
	}

	// 6. PRAGMA integrity_check
	var integrity string
	if err := roSt.DB.QueryRow("PRAGMA integrity_check(1)").Scan(&integrity); err != nil || !strings.EqualFold(integrity, "ok") {
		t.Fatalf("integrity_check failed: %s (err %v)", integrity, err)
	}

	// Verify permissions remained read-only
	fi, err := os.Stat(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0444 {
		t.Errorf("expected perm 0444, got %v", fi.Mode().Perm())
	}
}

func TestOpenInspectionContext_Cancellation(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "cancel.db")

	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	_ = st.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	start := time.Now()
	_, _, err = OpenInspectionContext(ctx, dbPath)
	dur := time.Since(start)

	if err == nil {
		t.Fatal("expected error with cancelled context, got nil")
	}
	if dur > 1*time.Second {
		t.Fatalf("expected immediate cancellation, took %v", dur)
	}
}

func TestOpenInspectionContext_DeadlineTimeout(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "timeout.db")

	// Create database in WAL mode with active uncommitted WAL transaction and exclusive file lock
	db, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=journal_mode(WAL)&_pragma=locking_mode(EXCLUSIVE)")
	if err != nil {
		t.Fatalf("sql.Open failed: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec("CREATE TABLE t (id INT); PRAGMA user_version=18;"); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	// Acquire exclusive lock on a dedicated connection
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatalf("Conn failed: %v", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(context.Background(), "BEGIN EXCLUSIVE"); err != nil {
		t.Fatalf("BEGIN EXCLUSIVE failed: %v", err)
	}
	defer func() { _, _ = conn.ExecContext(context.Background(), "ROLLBACK") }()

	if _, err := conn.ExecContext(context.Background(), "INSERT INTO t VALUES (1)"); err != nil {
		t.Fatalf("insert failed: %v", err)
	}

	// Now try to open with a 250ms deadline context
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, _, err = OpenInspectionContext(ctx, dbPath)
	dur := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout or lock error on locked database, got nil")
	}
	// Verify it respected the bounded deadline (~250ms with tolerance)
	if dur > 2*time.Second {
		t.Fatalf("expected deadline timeout near 250ms, took %v (exceeded deadline)", dur)
	}
}

func TestOpenInspection_ConcurrentWithActiveWriter(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "wal_concurrency.db")

	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer st.Close()

	p, err := st.EnsureProject(dir)
	if err != nil {
		t.Fatalf("EnsureProject failed: %v", err)
	}

	// Active writer begins exclusive transaction
	tx, err := st.DB.Begin()
	if err != nil {
		t.Fatalf("Begin failed: %v", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec("INSERT INTO facts(id, project_id, claim, confidence, repo_files, tools, session_ref, first_seen, promoted_at) VALUES('f1', ?, 'claim', 'high', '', '', '', datetime('now'), datetime('now'))", p.ID); err != nil {
		t.Fatalf("tx insert failed: %v", err)
	}

	// While transaction is uncommitted, OpenInspection should open and read without blocking
	inspSt, ok, err := OpenInspection(dbPath)
	if err != nil {
		t.Fatalf("OpenInspection failed during active writer transaction: %v", err)
	}
	if !ok || inspSt == nil {
		t.Fatal("expected store to exist")
	}
	defer inspSt.Close()

	// Query project through inspection handle
	proj, ok, err := inspSt.ProjectForDir(dir)
	if err != nil || !ok {
		t.Fatalf("ProjectForDir failed on inspection handle: %v", err)
	}
	if proj.ID != p.ID {
		t.Fatalf("expected project %s, got %s", p.ID, proj.ID)
	}
}
