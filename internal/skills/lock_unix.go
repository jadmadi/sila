//go:build !windows

package skills

import (
	"encoding/json"
	"errors"
	"fmt"
	mrand "math/rand/v2"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// AcquireLock acquires an exclusive advisory lock on the specified lock file.
// It uses non-blocking syscall.Flock with an inode-verified bounded retry loop.
func AcquireLock(lockPath string, timeout time.Duration) (*FileLock, error) {
	if timeout <= 0 {
		timeout = DefaultLockTimeout
	}

	dir := filepath.Dir(lockPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create lock directory %s: %w", dir, err)
	}

	// Scavenge any abandoned temp files older than 5 minutes
	CleanStaleTempFiles(dir, ".skills.json", 5*time.Minute)

	infoBytes, token, err := NewLockInfo()
	if err != nil {
		return nil, fmt.Errorf("generate lock metadata: %w", err)
	}

	deadline := time.Now().Add(timeout)
	for {
		f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0644)
		if err != nil {
			return nil, fmt.Errorf("open lock file %s: %w", lockPath, err)
		}

		// Non-blocking exclusive flock
		flockErr := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if flockErr == nil {
			// Inode verification check: ensure file descriptor points to the live path
			fi, ferr := f.Stat()
			pi, perr := os.Stat(lockPath)
			if ferr == nil && perr == nil && os.SameFile(fi, pi) {
				// Record ownership metadata
				_ = f.Truncate(0)
				_, _ = f.Seek(0, 0)
				_, _ = f.Write(infoBytes)
				_ = f.Sync()

				lock := &FileLock{
					Path:  lockPath,
					file:  f,
					Token: token,
					PID:   os.Getpid(),
				}
				return lock, nil
			}

			// Inode mismatch: file was replaced/unlinked while we opened it. Retry.
			_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
			_ = f.Close()
		} else {
			_ = f.Close()
			if !errors.Is(flockErr, syscall.EWOULDBLOCK) && !errors.Is(flockErr, syscall.EAGAIN) {
				return nil, fmt.Errorf("lock %s: %w", lockPath, flockErr)
			}
		}

		if time.Now().After(deadline) {
			// Extract holder diagnostics for informative error
			if data, err := os.ReadFile(lockPath); err == nil {
				var holder LockInfo
				if json.Unmarshal(data, &holder) == nil && holder.PID > 0 {
					return nil, fmt.Errorf("%w on %s (held by PID %d on %s)", ErrLockTimeout, filepath.Base(lockPath), holder.PID, holder.Hostname)
				}
			}
			return nil, fmt.Errorf("%w on %s after %v", ErrLockTimeout, filepath.Base(lockPath), timeout)
		}

		// Jittered sleep: 10ms - 30ms
		time.Sleep(time.Duration(10+mrand.IntN(20)) * time.Millisecond)
	}
}

// LockSkillsConfig acquires an exclusive advisory lock and returns an UnlockFunc.
func LockSkillsConfig(lockPath string, timeout time.Duration) (UnlockFunc, error) {
	fl, err := AcquireLock(lockPath, timeout)
	if err != nil {
		return nil, err
	}
	return func() {
		_ = fl.Unlock()
	}, nil
}

// Unlock releases the acquired file lock and removes the lockfile if ownership matches.
func (l *FileLock) Unlock() error {
	if l == nil || l.unlocked {
		return nil
	}
	l.unlocked = true

	// Verify token ownership before removing
	if data, err := os.ReadFile(l.Path); err == nil {
		var cur LockInfo
		if json.Unmarshal(data, &cur) == nil && cur.Token == l.Token {
			_ = os.Remove(l.Path)
		}
	} else if os.IsNotExist(err) {
		// Already unlinked
	}

	if l.file != nil {
		_ = syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
		_ = l.file.Close()
	}
	return nil
}
