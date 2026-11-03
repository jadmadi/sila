package skills

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveResourcePath_Valid(t *testing.T) {
	tempDir := t.TempDir()
	scriptsDir := filepath.Join(tempDir, "scripts")
	if err := os.MkdirAll(scriptsDir, 0755); err != nil {
		t.Fatal(err)
	}
	scriptFile := filepath.Join(scriptsDir, "audit.sh")
	if err := os.WriteFile(scriptFile, []byte("#!/bin/sh\necho ok"), 0755); err != nil {
		t.Fatal(err)
	}

	resolved, err := ResolveResourcePath(tempDir, "scripts/audit.sh")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved != scriptFile {
		t.Errorf("got %q, want %q", resolved, scriptFile)
	}
}

func TestResolveResourcePath_JailbreakRejections(t *testing.T) {
	tempDir := t.TempDir()

	// Prohibit absolute paths
	if _, err := ResolveResourcePath(tempDir, "/etc/passwd"); err == nil {
		t.Error("expected error for absolute path, got nil")
	}

	// Prohibit ../ traversal
	if _, err := ResolveResourcePath(tempDir, "../escape.txt"); err == nil {
		t.Error("expected error for ../ traversal, got nil")
	}

	// Prohibit escaping symlinks
	outsideDir := t.TempDir()
	secretFile := filepath.Join(outsideDir, "secret.txt")
	_ = os.WriteFile(secretFile, []byte("secret"), 0644)

	symlinkPath := filepath.Join(tempDir, "evil_link.txt")
	_ = os.Symlink(secretFile, symlinkPath)

	if _, err := ResolveResourcePath(tempDir, "evil_link.txt"); err == nil {
		t.Error("expected security violation for escaping symlink, got nil")
	}
}
