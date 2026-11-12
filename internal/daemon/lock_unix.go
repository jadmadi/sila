//go:build unix

package daemon

import (
	"errors"
	"os"
	"syscall"
)

// ErrLocked reports that another process holds the daemon lock.
var ErrLocked = errors.New("daemon lock held")

// Lock is an exclusive advisory lock on the pid file.
type Lock struct{ file *os.File }

// AcquireLock takes a non-blocking exclusive lock.
func AcquireLock(path string) (*Lock, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, ErrLocked
		}
		return nil, err
	}
	return &Lock{file: file}, nil
}

// WritePID records the daemon pid inside the locked file.
func (l *Lock) WritePID(pid int) error {
	if err := l.file.Truncate(0); err != nil {
		return err
	}
	if _, err := l.file.Seek(0, 0); err != nil {
		return err
	}
	_, err := l.file.WriteString(itoa(pid))
	return err
}

// Release unlocks and closes the file.
func (l *Lock) Release() {
	if l == nil || l.file == nil {
		return
	}
	_ = syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	_ = l.file.Close()
}

// SameOwner reports whether path belongs to the current user.
func SameOwner(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return true
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return true
	}
	return int(stat.Uid) == os.Getuid()
}
