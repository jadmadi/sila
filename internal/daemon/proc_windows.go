//go:build windows

package daemon

import "os"

func terminateProcess(pid int) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return proc.Kill()
}

func processAlive(pid int) bool {
	return false
}
