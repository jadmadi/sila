package main

import (
	"os"
	"strconv"

	"golang.org/x/term"
)

// setupRawStdin puts the terminal attached to stdin into raw (non-canonical,
// no-echo) mode so that single keypresses can be read without Enter. It
// returns a restore function to call on exit. If stdin is not a TTY the
// restore function is a no-op.
func setupRawStdin() func() {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return func() {}
	}
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return func() {}
	}
	return func() {
		_ = term.Restore(fd, oldState)
	}
}

// termWidth returns the visible width of the terminal attached to stdout,
// falling back to $COLUMNS or 80 when it cannot be detected. Watch status
// lines are truncated to this width so long worker lists never wrap and
// break fixed-height in-place redrawing.
func termWidth() int {
	fd := int(os.Stdout.Fd())
	if w, _, err := term.GetSize(fd); err == nil && w > 0 {
		return w
	}
	if c := os.Getenv("COLUMNS"); c != "" {
		if n, err := strconv.Atoi(c); err == nil && n > 0 {
			return n
		}
	}
	return 80
}

// enableTerminalCbreak disables canonical mode and echo on stdin if it is a terminal,
// allowing single-keystroke inputs (like 'q') without waiting for Enter.
// It returns a restore function that reinstates the original terminal configuration.
func enableTerminalCbreak() func() {
	return setupRawStdin()
}
