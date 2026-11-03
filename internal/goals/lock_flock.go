//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package goals

import (
	"errors"
	"os"
	"syscall"
)

// flockSupported reports whether this platform provides advisory file locks.
const flockSupported = true

// tryLockFile takes a non-blocking exclusive flock on f. It returns
// errLockBusy when another process or goroutine holds the lock.
func tryLockFile(f *os.File) error {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		return nil
	}
	if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
		return errLockBusy
	}
	return err
}

// unlockFile releases the advisory lock held on f.
func unlockFile(f *os.File) {
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
