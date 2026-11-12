//go:build !windows

package daemon

import (
	"errors"
	"syscall"
)

func terminateProcess(pid int) error {
	return syscall.Kill(pid, syscall.SIGTERM)
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
