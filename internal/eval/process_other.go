//go:build !unix

package eval

import (
	"os/exec"
)

func configureProcessGroup(cmd *exec.Cmd) {
	// Fallback to default cancellation on non-unix platforms
}
