//go:build !unix

package main

// ignoreTerminalFlowSignals is a no-op on platforms that have no SIGTTIN or
// SIGTTOU (Windows and plan9).
func ignoreTerminalFlowSignals() {}
