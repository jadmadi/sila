//go:build linux

package proclive

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func readProcess(pid int) (Ancestor, error) {
	stat, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Ancestor{}, fmt.Errorf("%w: %d", ErrProcessNotFound, pid)
		}
		return Ancestor{}, err
	}
	text := string(stat)
	close := strings.LastIndex(text, ")")
	if close < 0 {
		return Ancestor{}, fmt.Errorf("malformed stat for pid %d", pid)
	}
	name := strings.TrimSpace(text[strings.Index(text, "(")+1 : close])
	fields := strings.Fields(text[close+1:])
	if len(fields) < 20 {
		return Ancestor{}, fmt.Errorf("short stat for pid %d", pid)
	}
	ppid, _ := strconv.Atoi(fields[1])
	ticks, _ := strconv.ParseInt(fields[19], 10, 64)
	if cmdline, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline")); err == nil {
		if first, _, ok := strings.Cut(string(cmdline), "\x00"); ok && first != "" {
			name = filepath.Base(first)
		}
	}
	return Ancestor{PID: pid, PPID: ppid, Name: name, StartTicks: ticks}, nil
}
