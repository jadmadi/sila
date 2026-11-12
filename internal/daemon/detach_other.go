//go:build !unix

package daemon

import "os/exec"

// Detach leaves the child in the parent session.
func Detach(cmd *exec.Cmd) {}
