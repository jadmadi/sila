package cockpit

import (
	"os"
	"testing"
)

func TestTerminalDimensions_FallbackAndEnv(t *testing.T) {
	origCols := os.Getenv("COLUMNS")
	origLines := os.Getenv("LINES")
	defer func() {
		if origCols != "" {
			_ = os.Setenv("COLUMNS", origCols)
		} else {
			_ = os.Unsetenv("COLUMNS")
		}
		if origLines != "" {
			_ = os.Setenv("LINES", origLines)
		} else {
			_ = os.Unsetenv("LINES")
		}
	}()

	_ = os.Setenv("COLUMNS", "120")
	_ = os.Setenv("LINES", "40")
	w, h := terminalDimensions()
	if w <= 0 || h <= 0 {
		t.Errorf("expected w > 0 and h > 0, got %dx%d", w, h)
	}

	_ = os.Unsetenv("COLUMNS")
	_ = os.Unsetenv("LINES")
	wDef, hDef := terminalDimensions()
	if wDef <= 0 || hDef <= 0 {
		t.Errorf("expected default w > 0 and h > 0, got %dx%d", wDef, hDef)
	}
}

func TestSetupRawStdin_NonTTYSafe(t *testing.T) {
	restore := setupRawStdin()
	if restore == nil {
		t.Fatal("expected non-nil restore function")
	}
	restore()
}
