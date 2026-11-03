//go:build unix

package main

import (
	"os/signal"
	"syscall"
)

// ignoreTerminalFlowSignals keeps the watch loop running when it reads from a
// terminal in the background. SIGTTIN and SIGTTOU are Unix-only.
func ignoreTerminalFlowSignals() {
	signal.Ignore(syscall.SIGTTIN, syscall.SIGTTOU)
}
