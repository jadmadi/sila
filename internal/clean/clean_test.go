package clean

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRegistrySelection(t *testing.T) {
	reg := DefaultRegistry()
	all := reg.All()
	if len(all) < 9 {
		t.Fatalf("expected at least 9 default cleaners, got %d", len(all))
	}

	// Test selection by target name
	selected, err := reg.Select("", []string{"go", "rust", "aube"})
	if err != nil {
		t.Fatalf("unexpected error selecting targets: %v", err)
	}
	if len(selected) != 3 {
		t.Fatalf("expected 3 selected cleaners, got %d", len(selected))
	}

	// Test selection by alias (rust -> cargo, python -> py/uv/pip)
	selectedRust, err := reg.Select("", []string{"rust"})
	if err != nil || len(selectedRust) != 1 || selectedRust[0].Name() != "cargo" {
		t.Fatalf("expected rust alias to map to cargo, got %v (err: %v)", selectedRust, err)
	}

	selectedPy, err := reg.Select("", []string{"uv", "pip"})
	if err != nil || len(selectedPy) != 1 || selectedPy[0].Name() != "python" {
		t.Fatalf("expected uv/pip aliases to map to python, got %v (err: %v)", selectedPy, err)
	}

	// Test unknown target returns error
	_, err = reg.Select("", []string{"nonexistent-toolchain"})
	if err == nil {
		t.Fatalf("expected error for unknown target, got nil")
	}
}

func TestRemovePathSafe(t *testing.T) {
	tmpDir := t.TempDir()
	subDir := filepath.Join(tmpDir, "sub", "target")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatal(err)
	}
	testFile := filepath.Join(subDir, "file.txt")
	if err := os.WriteFile(testFile, []byte("hello safe clean"), 0644); err != nil {
		t.Fatal(err)
	}

	// 1. Safe deletion inside boundary
	bytes, err := RemovePathSafe(subDir, tmpDir)
	if err != nil {
		t.Fatalf("unexpected error removing safe path: %v", err)
	}
	if bytes == 0 {
		t.Fatalf("expected non-zero bytes reclaimed, got 0")
	}
	if _, err := os.Stat(subDir); !os.IsNotExist(err) {
		t.Fatalf("expected %s to be deleted", subDir)
	}

	// 2. Traversal rejection (target outside boundary)
	outsideDir := t.TempDir()
	_, err = RemovePathSafe(outsideDir, tmpDir)
	if err == nil {
		t.Fatalf("expected safety violation when target is outside boundary, got nil")
	}

	// 3. Root directory rejection
	_, err = RemovePathSafe("/", tmpDir)
	if err == nil {
		t.Fatalf("expected safety violation when target is /, got nil")
	}
}

func TestGoCleanerRepoResidue(t *testing.T) {
	tmpDir := t.TempDir()

	// Create mock Go repo
	if err := os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte("module example.com/test\ngo 1.27\n"), 0644); err != nil {
		t.Fatal(err)
	}

	c := NewGoCleaner()
	if !c.DetectWorkspace(tmpDir) {
		t.Fatalf("expected GoCleaner to detect workspace with go.mod")
	}

	// Create dummy test/profiling files
	covFile := filepath.Join(tmpDir, "coverage.out")
	if err := os.WriteFile(covFile, []byte("mode: set\nmain.go:1.1 1 1"), 0644); err != nil {
		t.Fatal(err)
	}
	testBin := filepath.Join(tmpDir, "pkg.test")
	if err := os.WriteFile(testBin, []byte("\x7fELFfakebinary"), 0755); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()

	// 1. Inspect
	rep, err := c.Inspect(ctx, tmpDir, ScopeRepo, false)
	if err != nil {
		t.Fatalf("unexpected inspect error: %v", err)
	}
	if len(rep.Items) != 2 {
		t.Fatalf("expected 2 residue items, got %d", len(rep.Items))
	}

	// 2. Clean with dryRun=true (must NOT delete files)
	resDry, err := c.Clean(ctx, tmpDir, ScopeRepo, false, true)
	if err != nil {
		t.Fatalf("unexpected clean dry-run error: %v", err)
	}
	if !resDry.DryRun {
		t.Fatalf("expected DryRun = true")
	}
	if _, err := os.Stat(covFile); os.IsNotExist(err) {
		t.Fatalf("dry-run deleted coverage.out!")
	}
	if _, err := os.Stat(testBin); os.IsNotExist(err) {
		t.Fatalf("dry-run deleted pkg.test!")
	}

	// 3. Clean with dryRun=false (must delete files)
	resLive, err := c.Clean(ctx, tmpDir, ScopeRepo, false, false)
	if err != nil {
		t.Fatalf("unexpected clean live error: %v", err)
	}
	if resLive.TotalReclaimed == 0 {
		t.Fatalf("expected non-zero reclaimed bytes")
	}
	if _, err := os.Stat(covFile); !os.IsNotExist(err) {
		t.Fatalf("expected coverage.out to be removed")
	}
	if _, err := os.Stat(testBin); !os.IsNotExist(err) {
		t.Fatalf("expected pkg.test to be removed")
	}
}

func TestCargoCleanerRepoResidue(t *testing.T) {
	tmpDir := t.TempDir()

	if err := os.WriteFile(filepath.Join(tmpDir, "Cargo.toml"), []byte("[package]\nname=\"foo\"\nversion=\"0.1.0\"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	c := NewCargoCleaner()
	if !c.DetectWorkspace(tmpDir) {
		t.Fatalf("expected CargoCleaner to detect Cargo.toml")
	}

	targetDir := filepath.Join(tmpDir, "target")
	if err := os.MkdirAll(filepath.Join(targetDir, "debug"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "debug", "binary"), []byte("dummy build artifact"), 0755); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	rep, err := c.Inspect(ctx, tmpDir, ScopeRepo, false)
	if err != nil {
		t.Fatalf("inspect error: %v", err)
	}
	if len(rep.Items) != 1 {
		t.Fatalf("expected 1 item for target/, got %d", len(rep.Items))
	}

	// Clean dry-run
	_, err = c.Clean(ctx, tmpDir, ScopeRepo, false, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(targetDir); os.IsNotExist(err) {
		t.Fatalf("dry-run deleted target dir")
	}

	// Clean live
	res, err := c.Clean(ctx, tmpDir, ScopeRepo, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.TotalReclaimed == 0 {
		t.Fatalf("expected reclaimed bytes > 0")
	}
	if _, err := os.Stat(targetDir); !os.IsNotExist(err) {
		t.Fatalf("expected target/ to be deleted")
	}
}

func TestPnpmAndAubeCleanerRepo(t *testing.T) {
	tmpDir := t.TempDir()

	// Mock pnpm workspace
	if err := os.WriteFile(filepath.Join(tmpDir, "pnpm-lock.yaml"), []byte("lockfileVersion: '9.0'\n"), 0644); err != nil {
		t.Fatal(err)
	}

	pnpmCleaner := NewPnpmCleaner()
	if !pnpmCleaner.DetectWorkspace(tmpDir) {
		t.Fatalf("expected pnpm detection")
	}

	cacheDir := filepath.Join(tmpDir, "node_modules", ".cache")
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, "build.cache"), []byte("cache data"), 0644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	rep, err := pnpmCleaner.Inspect(ctx, tmpDir, ScopeRepo, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(rep.Items))
	}

	res, err := pnpmCleaner.Clean(ctx, tmpDir, ScopeRepo, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.TotalReclaimed == 0 {
		t.Fatalf("expected reclaimed bytes")
	}
	if _, err := os.Stat(cacheDir); !os.IsNotExist(err) {
		t.Fatalf("expected node_modules/.cache to be deleted")
	}

	// Mock aube local cache
	aubeCleaner := NewAubeCleaner()
	aubeCache := filepath.Join(tmpDir, ".aube", "cache")
	if err := os.MkdirAll(aubeCache, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(aubeCache, "index.json"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}

	repAube, err := aubeCleaner.Inspect(ctx, tmpDir, ScopeRepo, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(repAube.Items) != 1 {
		t.Fatalf("expected 1 aube item, got %d", len(repAube.Items))
	}
	_, err = aubeCleaner.Clean(ctx, tmpDir, ScopeRepo, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(aubeCache); !os.IsNotExist(err) {
		t.Fatalf("expected .aube/cache to be deleted")
	}
}

func TestPythonCleanerRepo(t *testing.T) {
	tmpDir := t.TempDir()

	if err := os.WriteFile(filepath.Join(tmpDir, "pyproject.toml"), []byte("[project]\nname=\"foo\"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	pyCleaner := NewPythonCleaner()
	if !pyCleaner.DetectWorkspace(tmpDir) {
		t.Fatalf("expected python workspace detection")
	}

	pytestCache := filepath.Join(tmpDir, ".pytest_cache")
	if err := os.MkdirAll(pytestCache, 0755); err != nil {
		t.Fatal(err)
	}
	pycache := filepath.Join(tmpDir, "src", "__pycache__")
	if err := os.MkdirAll(pycache, 0755); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	rep, err := pyCleaner.Inspect(ctx, tmpDir, ScopeRepo, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Items) < 2 {
		t.Fatalf("expected at least 2 items, got %d", len(rep.Items))
	}

	_, err = pyCleaner.Clean(ctx, tmpDir, ScopeRepo, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(pytestCache); !os.IsNotExist(err) {
		t.Fatalf("expected .pytest_cache to be deleted")
	}
	if _, err := os.Stat(pycache); !os.IsNotExist(err) {
		t.Fatalf("expected __pycache__ to be deleted")
	}
}

func TestConcurrentInspectAll(t *testing.T) {
	reg := DefaultRegistry()
	tmpDir := t.TempDir()

	// Seed some files for Go and Rust
	_ = os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte("module foo\n"), 0644)
	_ = os.WriteFile(filepath.Join(tmpDir, "Cargo.toml"), []byte("[package]\nname=\"bar\"\n"), 0644)
	_ = os.WriteFile(filepath.Join(tmpDir, "coverage.out"), []byte("data"), 0644)

	ctx := context.Background()
	reports, err := reg.InspectAll(ctx, tmpDir, nil, ScopeRepo, false)
	if err != nil {
		t.Fatalf("InspectAll failed: %v", err)
	}
	if len(reports) == 0 {
		t.Fatalf("expected reports, got 0")
	}
}

func TestLockErrorDetectionAndFormatting(t *testing.T) {
	// 1. Verify lock signatures are properly recognized
	testCases := []struct {
		name     string
		errStr   string
		output   string
		isLocked bool
	}{
		{
			name:     "pnpm process lock",
			errStr:   "exit status 1",
			output:   "WARN The store is locked by another process with PID 41235",
			isLocked: true,
		},
		{
			name:     "pnpm error lockfile",
			errStr:   "ERR_PNPM_LOCKFILE The store is locked",
			output:   "",
			isLocked: true,
		},
		{
			name:     "sqlite database lock",
			errStr:   "database is locked",
			output:   "",
			isLocked: true,
		},
		{
			name:     "os resource busy",
			errStr:   "unlink ~/.cache/foo: resource busy (EBUSY)",
			output:   "",
			isLocked: true,
		},
		{
			name:     "cargo cache lock",
			errStr:   "exit status 101",
			output:   "Blocking waiting for file lock on package cache",
			isLocked: true,
		},
		{
			name:     "non-lock generic error",
			errStr:   "permission denied",
			output:   "",
			isLocked: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var err error
			if tc.errStr != "" {
				err = fmt.Errorf("%s", tc.errStr)
			}
			locked := IsLockError(err, tc.output)
			if locked != tc.isLocked {
				t.Fatalf("expected isLocked=%v for %q, got %v", tc.isLocked, tc.name, locked)
			}
		})
	}

	// 2. Verify lock message formatting
	reason := FormatLockReason("pnpm", "pnpm Global Package Store", nil, "The store is locked")
	if !strings.Contains(reason, "is currently locked by an active process") {
		t.Fatalf("expected lock reason to explain active process, got %q", reason)
	}
	if !strings.Contains(reason, "someone or another task is working and using it") {
		t.Fatalf("expected lock reason to explain someone is working and using it, got %q", reason)
	}
	if !strings.Contains(reason, "When that process is over, you can clean it") {
		t.Fatalf("expected lock reason to inform user when to retry, got %q", reason)
	}
}

func TestItemResultLockPreservation(t *testing.T) {
	// Verify that a cleaner capturing a LockError preserves Locked=true without failing overall
	lockErr := &LockError{
		Toolchain: "pnpm",
		ItemName:  "pnpm Global Package Store",
		Output:    "The store is locked by another process",
	}

	if !IsLockError(lockErr, "") {
		t.Fatalf("expected LockError to be recognized by IsLockError")
	}

	ir := ItemResult{
		Item: CleanItem{
			Toolchain: "pnpm",
			Name:      "pnpm Global Package Store",
		},
	}

	if IsLockError(lockErr, "") {
		ir.Locked = true
		ir.LockReason = FormatLockReason("pnpm", "pnpm Global Package Store", lockErr, "")
		ir.Error = ir.LockReason
		ir.Success = false
	}

	if !ir.Locked {
		t.Fatalf("expected ir.Locked to be true")
	}
	if ir.Success {
		t.Fatalf("expected ir.Success to be false on locked resource")
	}
	if !strings.Contains(ir.LockReason, "someone or another task is working and using it") {
		t.Fatalf("expected friendly lock reason, got: %s", ir.LockReason)
	}
}
