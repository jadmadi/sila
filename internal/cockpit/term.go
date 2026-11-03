package cockpit

import (
	"os"
	"strconv"

	"golang.org/x/term"
)

// setupRawStdin puts stdin into raw mode for single-character reads without echo.
// It returns a restore closure that reinstates the original terminal state.
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

// terminalDimensions returns the width and height of stdout, falling back to
// $COLUMNS, $LINES, or standard 80x24 when stdout is not a TTY.
func terminalDimensions() (int, int) {
	fd := int(os.Stdout.Fd())
	if w, h, err := term.GetSize(fd); err == nil && w > 0 && h > 0 {
		return w, h
	}
	width := 80
	if c := os.Getenv("COLUMNS"); c != "" {
		if n, err := strconv.Atoi(c); err == nil && n > 0 {
			width = n
		}
	}
	height := 24
	if r := os.Getenv("LINES"); r != "" {
		if n, err := strconv.Atoi(r); err == nil && n > 0 {
			height = n
		}
	}
	return width, height
}
