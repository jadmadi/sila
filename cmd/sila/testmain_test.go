package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
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
		"GOPATH":                     gopath,
	}
	for key, value := range env {
		if err := os.Setenv(key, value); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	code := m.Run()
	if err := os.RemoveAll(root); err != nil {
		fmt.Fprintln(os.Stderr, err)
		code = 1
	}
	os.Exit(code)
}
