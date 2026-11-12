package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/jadmadi/sila/internal/store"
)

// TestMain keeps discovery, loaders and default store access away from the real
// user profile. Individual tests can override these paths with t.Setenv.
func TestMain(m *testing.M) {
	// Child test processes inherit the parent's isolated profile and may supply
	// their own HOME/config fixture. Keep those explicitly supplied paths.
	if os.Getenv("SILA_TEST_PROFILE_ISOLATED") == "1" {
		os.Exit(m.Run())
	}
	root, err := os.MkdirTemp("", "sila-cli-test-profile-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	// Preserve the installed toolchain's module cache before changing its home.
	gopath := os.Getenv("GOPATH")
	if gopath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		gopath = filepath.Join(home, "go")
	}
	env := map[string]string{
		"SILA_TEST_PROFILE_ISOLATED": "1",
		"HOME":                       root,
		"XDG_CONFIG_HOME":            filepath.Join(root, "config"),
		"XDG_DATA_HOME":              filepath.Join(root, "data"),
		"SILA_DB":                    filepath.Join(root, "sila.db"),
		// The CLI suite creates hundreds of fresh stores. The A/B smoke
		// wrapper is exercised by the store package; skipping it here keeps
		// the race suite inside its timeout without skipping any migration.
		"SILA_SKIP_SMOKE_AB": "1",
		"GOPATH":             gopath,
	}
	for key, value := range env {
		if err := os.Setenv(key, value); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	// Build one migrated store and seed every fresh test store from it. The
	// migrations still run once per process, and the store package keeps its
	// own migration and smoke coverage.
	template := filepath.Join(root, "template.db")
	tmplStore, err := store.Open(template)
	if err != nil {
		fmt.Fprintln(os.Stderr, "build test db template:", err)
		os.Exit(1)
	}
	if _, err := tmplStore.DB.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		fmt.Fprintln(os.Stderr, "checkpoint test db template:", err)
		_ = tmplStore.Close()
		os.Exit(1)
	}
	if err := tmplStore.Close(); err != nil {
		fmt.Fprintln(os.Stderr, "close test db template:", err)
		os.Exit(1)
	}
	if err := os.Setenv("SILA_TEST_DB_TEMPLATE", template); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	if err := os.RemoveAll(root); err != nil {
		fmt.Fprintln(os.Stderr, err)
		code = 1
	}
	os.Exit(code)
}
