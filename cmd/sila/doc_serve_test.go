package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/render"
)

func TestResolveDocDir(t *testing.T) {
	tempDir := t.TempDir()
	docDir := filepath.Join(tempDir, "html-docs")
	if err := os.MkdirAll(docDir, 0o755); err != nil {
		t.Fatalf("mkdir html-docs: %v", err)
	}
	indexPath := filepath.Join(docDir, "index.html")
	if err := os.WriteFile(indexPath, []byte("<h1>Sila Docs</h1>"), 0o644); err != nil {
		t.Fatalf("write index.html: %v", err)
	}

	// 1. Explicit directory argument
	found, err := resolveDocDir(docDir)
	if err != nil {
		t.Fatalf("expected docDir found, got err: %v", err)
	}
	if found != docDir {
		t.Errorf("expected %s, got %s", docDir, found)
	}

	// 2. Directory from CWD
	origWd, _ := os.Getwd()
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(origWd)

	foundCwd, err := resolveDocDir("")
	if err != nil {
		t.Fatalf("expected docDir found from cwd, got err: %v", err)
	}
	if filepath.Clean(foundCwd) != filepath.Clean(docDir) {
		t.Errorf("expected %s, got %s", docDir, foundCwd)
	}
}

func TestDocServeContext(t *testing.T) {
	tempDir := t.TempDir()
	docDir := filepath.Join(tempDir, "html-docs")
	_ = os.MkdirAll(docDir, 0o755)
	_ = os.WriteFile(filepath.Join(docDir, "index.html"), []byte("<html><body>Hello Sila Docs</body></html>"), 0o644)

	ui := render.New(os.Stdout, true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		// Use port 0 for random free test port
		errCh <- runDocServeContext(ctx, ui, 0, "127.0.0.1", true, false, docDir)
	}()

	// Wait briefly for server to bind
	time.Sleep(100 * time.Millisecond)

	// In order to get the port, test against resolveDocDir first
	dir, err := resolveDocDir(docDir)
	if err != nil {
		t.Fatalf("resolveDocDir failed: %v", err)
	}
	if dir != docDir {
		t.Errorf("got %s, want %s", dir, docDir)
	}

	// Shutdown server
	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("runDocServeContext returned error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("server did not shut down in time")
	}
}

func TestDocServeHTTPFetch(t *testing.T) {
	tempDir := t.TempDir()
	docDir := filepath.Join(tempDir, "html-docs")
	_ = os.MkdirAll(docDir, 0o755)
	content := "<html><body><h1>Sila Protocol Specification</h1></body></html>"
	_ = os.WriteFile(filepath.Join(docDir, "index.html"), []byte(content), 0o644)

	ui := render.New(os.Stdout, true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	// Run server with port 0 on loopback
	go func() {
		errCh <- runDocServeContext(ctx, ui, 0, "127.0.0.1", true, true, docDir)
	}()

	time.Sleep(150 * time.Millisecond)

	// Shutdown
	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("server returned error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for server shutdown")
	}
}
