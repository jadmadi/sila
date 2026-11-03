package render

import "testing"

func TestCursorHelpers(t *testing.T) {
	var ui UI
	if got := ui.RewriteLine("x"); got != "\r\x1b[2Kx" {
		t.Errorf("RewriteLine = %q", got)
	}
	if got := ui.ClearScreen("x"); got != "\x1b[H\x1b[2Jx" {
		t.Errorf("ClearScreen = %q", got)
	}
}
