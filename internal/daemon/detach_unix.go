//go:build unix

package daemon

import (
	"os/exec"
	"syscall"
)

// Detach starts the child in its own session.
func Detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
