//go:build !unix

package daemon

import (
	"errors"
	"os"
)

// ErrLocked reports that another process holds the daemon lock.
var ErrLocked = errors.New("daemon lock held")

// Lock is a best-effort file lock on non-Unix platforms.
type Lock struct{ file *os.File }

// AcquireLock creates the pid file.
func AcquireLock(path string) (*Lock, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	return &Lock{file: file}, nil
}

// WritePID records the daemon pid.
func (l *Lock) WritePID(pid int) error {
	if err := l.file.Truncate(0); err != nil {
		return err
	}
	_, err := l.file.WriteString(itoa(pid))
	return err
}

// Release closes the file.
func (l *Lock) Release() { _ = l.file.Close() }

// SameOwner is a no-op check on platforms without uid stat data.
func SameOwner(path string) bool { return true }
