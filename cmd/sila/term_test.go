package main

import (
	"os"
	"testing"
)

func TestTermWidth_FallbackAndEnv(t *testing.T) {
	orig := os.Getenv("COLUMNS")
	defer func() {
		if orig != "" {
			_ = os.Setenv("COLUMNS", orig)
		} else {
			_ = os.Unsetenv("COLUMNS")
		}
	}()

	_ = os.Setenv("COLUMNS", "132")
	w := termWidth()
	// In non-interactive test runs where stdout is not a TTY or GetSize fails, COLUMNS is honored.
	// If stdout happens to be a real TTY, w > 0.
	if w <= 0 {
		t.Errorf("expected termWidth > 0, got %d", w)
	}

	_ = os.Unsetenv("COLUMNS")
	wFallback := termWidth()
	if wFallback <= 0 {
		t.Errorf("expected termWidth fallback > 0, got %d", wFallback)
	}
}

func TestSetupRawStdin_NonTTYSafe(t *testing.T) {
	restore := setupRawStdin()
	if restore == nil {
		t.Fatal("expected non-nil restore function")
	}
	// Calling restore should never panic or fail
	restore()
}

func TestEnableTerminalCbreak_NonTTYSafe(t *testing.T) {
	restore := enableTerminalCbreak()
	if restore == nil {
		t.Fatal("expected non-nil restore function")
	}
	restore()
}
