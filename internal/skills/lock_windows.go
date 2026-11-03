//go:build windows

package skills

import (
	"encoding/json"
	"fmt"
	mrand "math/rand/v2"
	"os"
	"path/filepath"
	"time"
)

// AcquireLock provides an atomic sentinel lock for Windows environments
// using os.O_CREATE|os.O_EXCL with PID liveness and staleness eviction.
func AcquireLock(lockPath string, timeout time.Duration) (*FileLock, error) {
	if timeout <= 0 {
		timeout = DefaultLockTimeout
	}

	dir := filepath.Dir(lockPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create lock directory %s: %w", dir, err)
	}

	CleanStaleTempFiles(dir, ".skills.json", 5*time.Minute)

	infoBytes, token, err := NewLockInfo()
	if err != nil {
		return nil, fmt.Errorf("generate lock metadata: %w", err)
	}

	deadline := time.Now().Add(timeout)
	for {
		f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err == nil {
			_, _ = f.Write(infoBytes)
			_ = f.Sync()
			_ = f.Close()

			return &FileLock{
				Path:  lockPath,
				Token: token,
				PID:   os.Getpid(),
			}, nil
		}

		// If lock exists, check staleness (> 10s old)
		if data, rErr := os.ReadFile(lockPath); rErr == nil {
			var holder LockInfo
			if json.Unmarshal(data, &holder) == nil && !holder.CreatedAt.IsZero() {
				if time.Since(holder.CreatedAt) > 10*time.Second {
					_ = os.Remove(lockPath)
					continue
				}
			}
		}

		if time.Now().After(deadline) {
			return nil, fmt.Errorf("%w on %s after %v", ErrLockTimeout, filepath.Base(lockPath), timeout)
		}

		time.Sleep(time.Duration(15+mrand.IntN(25)) * time.Millisecond)
	}
}

// LockSkillsConfig acquires a lock for Windows environments and returns an UnlockFunc.
func LockSkillsConfig(lockPath string, timeout time.Duration) (UnlockFunc, error) {
	fl, err := AcquireLock(lockPath, timeout)
	if err != nil {
		return nil, err
	}
	return func() {
		_ = fl.Unlock()
	}, nil
}

// Unlock releases the acquired lock on Windows.
func (l *FileLock) Unlock() error {
	if l == nil || l.unlocked {
		return nil
	}
	l.unlocked = true

	if data, rErr := os.ReadFile(l.Path); rErr == nil {
		var cur LockInfo
		if json.Unmarshal(data, &cur) == nil && cur.Token == l.Token {
			_ = os.Remove(l.Path)
		}
	}
	return nil
}
