package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// ErrSyncBusy indicates another sync process currently holds the exclusive lease on the store.
var ErrSyncBusy = errors.New("another sync is running")

// CanonicalDBPath resolves the canonical filesystem path for a database file.
// If the target exists, symlinks are evaluated.
// If the target does not exist, its parent directory is resolved before appending the filename.
func CanonicalDBPath(dbPath string) (string, error) {
	if dbPath == "" {
		return "", errors.New("empty database path")
	}
	abs, err := filepath.Abs(dbPath)
	if err != nil {
		return "", fmt.Errorf("resolve absolute path: %w", err)
	}
	clean := filepath.Clean(abs)

	// If the file exists, evaluate symlinks directly
	if realPath, err := filepath.EvalSymlinks(clean); err == nil {
		return realPath, nil
	}

	// If clean is a symlink whose target does not exist yet on disk, resolve target
	if fi, err := os.Lstat(clean); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		if dest, err := os.Readlink(clean); err == nil {
			if !filepath.IsAbs(dest) {
				dest = filepath.Join(filepath.Dir(clean), dest)
			}
			return CanonicalDBPath(dest)
		}
	}

	// If the file does not exist yet, evaluate parent directory symlinks
	dir := filepath.Dir(clean)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("ensure parent directory: %w", err)
	}
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return clean, nil
	}
	return filepath.Join(realDir, filepath.Base(clean)), nil
}

// SyncLeasePath returns the path to the advisory flock file for the given DB path.
func SyncLeasePath(dbPath string) (string, error) {
	canonical, err := CanonicalDBPath(dbPath)
	if err != nil {
		return "", err
	}
	return canonical + ".sync.flock", nil
}

// AcquireSyncLease acquires an exclusive non-blocking advisory lease for the canonical database.
// It returns a safe, idempotent release function and an error if busy or failed.
func AcquireSyncLease(dbPath string) (func() error, error) {
	lockPath, err := SyncLeasePath(dbPath)
	if err != nil {
		return nil, err
	}

	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}

	// Attempt LOCK_EX|LOCK_NB once
	err = tryFlockExclusive(f)
	if err != nil {
		_ = f.Close()
		if errors.Is(err, errFlockBusy) {
			return nil, fmt.Errorf("%w: lock held on %s", ErrSyncBusy, lockPath)
		}
		return nil, fmt.Errorf("acquire sync lease on %s: %w", lockPath, err)
	}

	// Verify os.SameFile between opened handle and path on disk to protect against inode replacement
	fiHandle, err := f.Stat()
	if err != nil {
		_ = unlockFlock(f)
		_ = f.Close()
		return nil, fmt.Errorf("stat lock handle: %w", err)
	}
	fiPath, err := os.Stat(lockPath)
	if err != nil {
		_ = unlockFlock(f)
		_ = f.Close()
		return nil, fmt.Errorf("stat lock path: %w", err)
	}
	if !os.SameFile(fiHandle, fiPath) {
		_ = unlockFlock(f)
		_ = f.Close()
		return nil, fmt.Errorf("lock file inode replaced during acquisition: %s", lockPath)
	}

	var once sync.Once
	var releaseErr error
	release := func() error {
		once.Do(func() {
			unErr := unlockFlock(f)
			clErr := f.Close()
			if unErr != nil {
				releaseErr = unErr
			} else {
				releaseErr = clErr
			}
		})
		return releaseErr
	}
	return release, nil
}
