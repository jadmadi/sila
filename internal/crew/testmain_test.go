package crew

import (
	"os"
	"os/exec"
	"testing"
)

// TestMain removes the shared crew tmux session after the package tests. A
// detached session would otherwise keep the test process group alive and stall
// sandboxed full-suite runs.
func TestMain(m *testing.M) {
	code := m.Run()
	_ = exec.Command("tmux", "kill-session", "-t", "sila-crew").Run()
	os.Exit(code)
}
