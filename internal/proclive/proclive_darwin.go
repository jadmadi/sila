//go:build darwin

package proclive

import (
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// readProcess uses ps for ancestry. macOS does not expose kernel start ticks,
// so StartTicks is the process start time in Unix seconds scaled by 100.
func readProcess(pid int) (Ancestor, error) {
	out, err := exec.Command("ps", "-o", "pid=,ppid=,comm=,lstart=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return Ancestor{}, fmt.Errorf("%w: %d", ErrProcessNotFound, pid)
	}
	fields := strings.Fields(strings.TrimSpace(string(out)))
	if len(fields) < 7 {
		return Ancestor{}, fmt.Errorf("short ps output for pid %d", pid)
	}
	ppid, _ := strconv.Atoi(fields[1])
	// lstart example: Wed Nov  6 12:00:00 2026
	startText := strings.Join(fields[3:], " ")
	started, startErr := time.Parse("Mon Jan _2 15:04:05 2006", startText)
	if startErr != nil {
		started = time.Time{}
	}
	return Ancestor{PID: pid, PPID: ppid, Name: fields[2], StartTicks: started.Unix() * 100}, nil
}

var _ = errors.Is
