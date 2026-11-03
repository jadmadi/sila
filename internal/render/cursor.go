package render

import "fmt"

// RewriteLine returns s prefixed with the sequence that moves to column 0 and
// erases the line, so a live view can redraw one line in place. Callers decide
// whether stdout is a terminal; on a pipe they must not use it.
func (UI) RewriteLine(s string) string { return "\r\x1b[2K" + s }

// ClearScreen returns s prefixed with the sequence that homes the cursor and
// clears the screen, so a live view can redraw a multi-line frame in place.
// Callers decide whether stdout is a terminal; on a pipe they must not use it.
func (UI) ClearScreen(s string) string { return "\x1b[H\x1b[2J" + s }

// CursorUp returns the sequence that moves the cursor up n rows and to carriage return.
func CursorUp(n int) string { return fmt.Sprintf("\x1b[%dA\r", n) }

// EraseLine returns the sequence to erase the entire current line and carriage return.
func EraseLine() string { return "\x1b[2K\r" }

// CursorDown returns the sequence to move the cursor down 1 row.
func CursorDown() string { return "\x1b[1B" }
