// Package proclive reads a process and its ancestors without external tools on
// Linux, and with ps on macOS. It never leaves the machine and never writes.
package proclive

import (
	"errors"
	"fmt"
	"time"
)

// ErrAncestryUnavailable is returned on platforms without an implementation.
var ErrAncestryUnavailable = errors.New("process ancestry is unavailable on this platform")

// ErrProcessNotFound is returned when the starting PID does not exist.
var ErrProcessNotFound = errors.New("process not found")

// Ancestor is one process in the chain.
type Ancestor struct {
	PID        int    `json:"pid"`
	PPID       int    `json:"ppid"`
	Name       string `json:"name,omitempty"`
	StartTicks int64  `json:"start_ticks"`
}

// Ancestry returns the chain from pid upward. It stops at PID 1, a missing
// parent, maxDepth, or the five millisecond budget. A missing starting PID is a
// typed error; a missing parent returns the chain built so far.
func Ancestry(pid, maxDepth int) ([]Ancestor, error) {
	if pid <= 0 {
		return nil, fmt.Errorf("%w: invalid pid %d", ErrProcessNotFound, pid)
	}
	if maxDepth <= 0 {
		maxDepth = 64
	}
	deadline := time.Now().Add(5 * time.Millisecond)
	var chain []Ancestor
	current := pid
	for depth := 0; depth < maxDepth; depth++ {
		if current <= 0 || time.Now().After(deadline) {
			break
		}
		ancestor, err := readProcess(current)
		if err != nil {
			if depth == 0 {
				return nil, err
			}
			break
		}
		chain = append(chain, ancestor)
		if ancestor.PPID <= 1 || ancestor.PPID == current {
			break
		}
		current = ancestor.PPID
	}
	return chain, nil
}
