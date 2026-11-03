package plugins

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPluginActivation(t *testing.T) {
	tempDir := t.TempDir()

	// Initial check: no config, returns false
	if IsPluginActive(tempDir, "cfclean") {
		t.Errorf("expected cfclean to be inactive initially")
	}

	active, err := ListActivePlugins(tempDir)
	if err != nil {
		t.Fatalf("unexpected error listing plugins: %v", err)
	}
	if len(active) != 0 {
		t.Errorf("expected 0 active plugins, got %d", len(active))
	}

	// Activate cfclean
	if err := ActivatePlugin(tempDir, "cfclean"); err != nil {
		t.Fatalf("activate error: %v", err)
	}

	if !IsPluginActive(tempDir, "cfclean") {
		t.Errorf("expected cfclean to be active")
	}
	// Case insensitive check
	if !IsPluginActive(tempDir, "CFCLEAN") {
		t.Errorf("expected CFCLEAN to match case-insensitively")
	}

	// Activate another plugin
	if err := ActivatePlugin(tempDir, "d1ops"); err != nil {
		t.Fatalf("activate d1ops error: %v", err)
	}

	active, err = ListActivePlugins(tempDir)
	if err != nil {
		t.Fatalf("list error: %v", err)
	}
	if len(active) != 2 || active[0] != "cfclean" || active[1] != "d1ops" {
		t.Errorf("expected [cfclean, d1ops], got %v", active)
	}

	// Idempotent activate
	if err := ActivatePlugin(tempDir, "cfclean"); err != nil {
		t.Fatalf("idempotent activate error: %v", err)
	}
	active, _ = ListActivePlugins(tempDir)
	if len(active) != 2 {
		t.Errorf("expected 2 active plugins after duplicate activate, got %d", len(active))
	}

	// Deactivate cfclean
	if err := DeactivatePlugin(tempDir, "cfclean"); err != nil {
		t.Fatalf("deactivate error: %v", err)
	}
	if IsPluginActive(tempDir, "cfclean") {
		t.Errorf("expected cfclean to be inactive after deactivation")
	}
	if !IsPluginActive(tempDir, "d1ops") {
		t.Errorf("expected d1ops to remain active")
	}

	// Deactivate non-existent is no-op
	if err := DeactivatePlugin(tempDir, "unknown"); err != nil {
		t.Fatalf("deactivate unknown error: %v", err)
	}

	// Verify file exists on disk
	cfgPath := filepath.Join(tempDir, ".sila", "plugins.json")
	if _, err := os.Stat(cfgPath); err != nil {
		t.Errorf("expected config file to exist: %v", err)
	}
}
