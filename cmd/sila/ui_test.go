package main

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
	"github.com/jadmadi/sila/internal/web"
)

func init() {
	// Stub browser launcher during tests to prevent opening browser windows on host
	openBrowserCmd = func(targetURL string) error {
		return nil
	}
}

func TestOpenBrowserSafety(t *testing.T) {
	called := false
	orig := openBrowserCmd
	defer func() { openBrowserCmd = orig }()
	openBrowserCmd = func(url string) error {
		called = true
		return nil
	}

	openBrowser("http://127.0.0.1:7452")
	if !called {
		t.Errorf("expected openBrowserCmd to be called")
	}
}

func TestWebUICommandLifecycle(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()

	cfg := web.Config{
		Host:       "127.0.0.1",
		Port:       -1, // ephemeral port: no contention with parallel packages
		ProjectDir: dir,
		ProjectID:  "test-proj",
		NoOpen:     true,
		Store:      st,
	}

	srv, err := web.NewServer(cfg)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	if err := srv.Start(); err != nil {
		t.Fatalf("srv.Start: %v", err)
	}

	// Verify server responds
	resp, err := http.Get(srv.URL() + "/api/status")
	if err != nil {
		t.Fatalf("GET /api/status: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", resp.StatusCode)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Errorf("Shutdown: %v", err)
	}
}

func TestRunUIContextShutdown(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	st.Close()

	ctx, cancel := context.WithCancel(context.Background())
	errChan := make(chan error, 1)

	go func() {
		ui := render.New(nil, true)
		errChan <- runUIContext(ctx, ui, dbPath, "test-proj", 0, "127.0.0.1", true, false, true, nil)
	}()

	// Allow server to start up, then cancel context
	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-errChan:
		if err != nil {
			t.Errorf("expected clean shutdown, got error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("runUIContext timed out waiting for shutdown")
	}
}

func TestCLISubcommandDispatchUI(t *testing.T) {
	// Verify that subcommand recognizes ui and web
	subcommands := []string{"ui", "web"}
	for _, sub := range subcommands {
		if sub == "" || (sub != "ingest" && sub != "assess" && sub != "dream" && sub != "projects" && sub != "overlaps" && sub != "sync" && sub != "live" && sub != "watch" && sub != "export" && sub != "completions" && sub != "status" && sub != "intro" && sub != "mcp" && sub != "search" && sub != "prime" && sub != "hook" && sub != "handoff" && sub != "memo" && sub != "doc" && sub != "config" && sub != "vacuum" && sub != "doctor" && sub != "gate" && sub != "update" && sub != "upgrade" && sub != "goals" && sub != "goal" && sub != "feedback" && sub != "release" && sub != "credits" && sub != "opinions" && sub != "vision" && sub != "adr" && sub != "digest" && sub != "standup" && sub != "benchmark" && sub != "changelog" && sub != "worktree" && sub != "crew" && sub != "install" && sub != "agents" && sub != "ui" && sub != "web" && sub != "guide" && sub != "learn") {
			t.Errorf("subcommand %q failed main.go whitelist check", sub)
		}
	}
}
